package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"tokensaver/pkg/adapters"
	"tokensaver/pkg/core"
	"tokensaver/pkg/gateway"
)

type fixtureCase struct {
	Name     string
	Protocol string
	Model    string
	Body     []byte
}

func main() {
	fixtures := flag.String("fixtures", "tests/fixtures", "fixture directory")
	iterations := flag.Int("iterations", 10, "measured gateway iterations")
	output := flag.String("output", "/tmp/tokensaver-adapter-comparison.json", "output file")
	flag.Parse()
	cases, err := loadCases(*fixtures)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg := core.DefaultConfig()
	cfg.ThresholdTokens = 0
	cfg.MinSavingsTokens = 0
	cfg.PreservePrefixItems = 0

	var upstreamMu sync.Mutex
	var upstreamBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstreamMu.Lock()
		upstreamBody = body
		upstreamMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	gcfg := gateway.DefaultConfig()
	gcfg.Upstream = upstream.URL
	gcfg.Adapter = "agent2api"
	gcfg.ManagementEnabled = false
	gcfg.Core = cfg
	gw, err := gateway.NewServerWithConfig("", gcfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	proxy := httptest.NewServer(gw.Handler())
	defer proxy.Close()

	results := make([]map[string]any, 0, len(cases))
	for _, c := range cases {
		coreRes := core.CompressRequest(c.Body, cfg, core.RequestMeta{Protocol: c.Protocol, Model: c.Model})
		cpaReq := adapters.CPARequestInterceptRequest{
			RequestID: "cmp-" + c.Name, SourceFormat: c.Protocol, ToFormat: c.Protocol,
			Model: c.Model, Body: c.Body,
		}
		cpaResp, cpaStats := adapters.InterceptForCPA(cpaReq, cfg)
		cpaBody := cpaResp.Body
		if len(cpaBody) == 0 {
			cpaBody = c.Body
		}

		// Gateway path and protocol selection follow the actual profile.
		path := "/v1/chat/completions"
		if c.Protocol == "responses" {
			path = "/v1/responses"
		} else if c.Protocol == "anthropic" {
			path = "/v1/messages"
		}
		durations := make([]float64, 0, *iterations)
		for i := 0; i < *iterations; i++ {
			start := time.Now()
			req, _ := http.NewRequest(http.MethodPost, proxy.URL+path, bytes.NewReader(c.Body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				fmt.Fprintln(os.Stderr, "gateway request:", err)
				os.Exit(1)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			durations = append(durations, float64(time.Since(start).Microseconds())/1000)
		}
		sort.Float64s(durations)
		upstreamMu.Lock()
		gatewayBody := append([]byte(nil), upstreamBody...)
		upstreamMu.Unlock()
		coreBodyEqualCPA := bytes.Equal(coreRes.Body, cpaBody)
		gatewayBodyEqualsCore := bytes.Equal(coreRes.Body, gatewayBody)
		results = append(results, map[string]any{
			"name":        c.Name,
			"protocol":    c.Protocol,
			"input_bytes": len(c.Body),
			"core": map[string]any{
				"applied":    coreRes.Applied,
				"body_bytes": len(coreRes.Body),
				"stats":      coreRes.Stats,
			},
			"cpa_adapter": map[string]any{
				"body_bytes":       len(cpaBody),
				"body_equals_core": coreBodyEqualCPA,
				"stats":            cpaStats,
			},
			"agent2api_adapter": map[string]any{
				"path":                      path,
				"requests":                  *iterations,
				"p50_ms":                    durations[len(durations)/2],
				"p95_ms":                    durations[int(float64(len(durations)-1)*0.95)],
				"upstream_body_equals_core": gatewayBodyEqualsCore,
				"upstream_body_bytes":       len(gatewayBody),
			},
		})
	}
	report := map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339Nano),
		"note":         "Same fixture input through Core, CPA adapter, and the HTTP gateway.",
		"iterations":   *iterations,
		"results":      results,
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(*output, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func loadCases(dir string) ([]fixtureCase, error) {
	raw, err := os.ReadFile(dir + "/long-coding-session.json")
	if err != nil {
		return nil, err
	}
	var fixture struct {
		Messages any `json:"messages"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return nil, err
	}
	chat, _ := json.Marshal(map[string]any{"model": "long-coding-session", "messages": fixture.Messages})
	cases := []fixtureCase{{Name: "long-coding", Protocol: "chat", Model: "long-coding-session", Body: chat}}
	for name, model := range map[string]string{"large-test.log": "test-log", "large-server.log": "server-log", "large.diff": "git-diff"} {
		text, err := os.ReadFile(dir + "/generated/" + name)
		if err != nil {
			return nil, err
		}
		body, _ := json.Marshal(map[string]any{"model": model, "messages": []any{
			map[string]any{"role": "assistant", "content": "previous turn"},
			map[string]any{"role": "tool", "tool_call_id": "call_cmp", "content": string(text)},
			map[string]any{"role": "user", "content": "explain the failure"},
		}})
		cases = append(cases, fixtureCase{Name: "large-" + strings.TrimSuffix(name, ".log"), Protocol: "chat", Model: model, Body: body})
	}
	return cases, nil
}
