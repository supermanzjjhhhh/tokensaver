package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	"tokensaver/pkg/core"
)

type Case struct {
	Name     string          `json:"name"`
	Protocol string          `json:"protocol"`
	Model    string          `json:"model"`
	Body     json.RawMessage `json:"body"`
}

func main() {
	var (
		fixturesDir = flag.String("fixtures", "tests/fixtures", "fixtures directory")
		iterations  = flag.Int("iterations", 50, "timed iterations")
		output      = flag.String("output", "", "write JSON report")
	)
	flag.Parse()
	cases, err := loadCases(*fixturesDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture error:", err)
		os.Exit(2)
	}
	cfg := core.DefaultConfig()
	cfg.ThresholdTokens = 0
	cfg.MinSavingsTokens = 0
	cfg.PreservePrefixItems = 0
	if os.Getenv("TOKENSAVER_BENCH_LOSSY") == "1" {
		cfg.Mode = core.ModeCustom
		cfg.AllowLossy = true
		cfg.Engines = []core.EngineConfig{
			{ID: core.EngineWhitespace, Enabled: true},
			{ID: core.EngineJSONMinify, Enabled: true},
			{ID: core.EngineDiagnosticTrim, Enabled: true},
			{ID: core.EngineToolTruncate, Enabled: true},
		}
		cfg.DiagnosticTrim.MaxLines = 180
		cfg.DiagnosticTrim.MinChars = 4000
		cfg.ToolTruncate.MaxChars = 12000
		cfg.ToolTruncate.MinChars = 14000
	}
	results := make([]map[string]any, 0, len(cases))
	for _, c := range cases {
		// Warm up outside measurement.
		for i := 0; i < 3; i++ {
			_ = core.CompressRequest(c.Body, cfg, core.RequestMeta{Protocol: c.Protocol, Model: c.Model})
		}
		durations := make([]float64, 0, *iterations)
		var last core.Result
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for i := 0; i < *iterations; i++ {
			start := time.Now()
			last = core.CompressRequest(c.Body, cfg, core.RequestMeta{Protocol: c.Protocol, Model: c.Model})
			durations = append(durations, float64(time.Since(start).Microseconds())/1000)
		}
		runtime.ReadMemStats(&after)
		sort.Float64s(durations)
		results = append(results, map[string]any{
			"name":               c.Name,
			"protocol":           c.Protocol,
			"model":              c.Model,
			"input_bytes":        len(c.Body),
			"output_bytes":       len(last.Body),
			"applied":            last.Applied,
			"engines":            last.Stats.Engines,
			"original_tokens":    last.Stats.OriginalTokens,
			"compressed_tokens":  last.Stats.CompressedTokens,
			"savings_percent":    last.Stats.SavingsPercent,
			"compression_ms_p50": durations[len(durations)/2],
			"compression_ms_p95": durations[int(float64(len(durations)-1)*0.95)],
			"compression_ms_min": durations[0],
			"compression_ms_max": durations[len(durations)-1],
			"alloc_bytes_total":  after.TotalAlloc - before.TotalAlloc,
			"heap_alloc_end":     after.HeapAlloc,
			"iterations":         *iterations,
			"go_os":              runtime.GOOS,
			"go_arch":            runtime.GOARCH,
		})
	}
	report := map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339Nano),
		"config":       cfg,
		"results":      results,
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	if *output != "" {
		if err := os.WriteFile(*output, append(data, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Println(string(data))
}

func loadCases(dir string) ([]Case, error) {
	var cases []Case
	// Long coding fixture is a Chat Completions body wrapper hosted in the
	// fixture file's messages field; convert without copying or altering content.
	raw, err := os.ReadFile(dir + "/long-coding-session.json")
	if err != nil {
		return nil, err
	}
	var fixture struct {
		Name     string `json:"name"`
		Messages any    `json:"messages"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return nil, err
	}
	chat, _ := json.Marshal(map[string]any{"model": "long-coding-session", "messages": fixture.Messages})
	cases = append(cases, Case{Name: "chat-long-coding", Protocol: "chat", Model: "long-coding-session", Body: chat})
	for _, name := range []string{"git-diff", "eslint-output", "docker-logs", "build-vite"} {
		txt, err := os.ReadFile(dir + "/rtk/" + name + "-sample.txt")
		if err != nil {
			return nil, err
		}
		cases = append(cases, toolCase("chat-"+name, "diagnostic-sample", string(txt)))
	}
	for name, model := range map[string]string{"large-test.log": "test-log", "large-server.log": "server-log", "large.diff": "git-diff"} {
		txt, err := os.ReadFile(dir + "/generated/" + name)
		if err != nil {
			return nil, err
		}
		cases = append(cases, toolCase("chat-large-"+name, model, string(txt)))
	}
	return cases, nil
}

func toolCase(name, model, text string) Case {
	body, _ := json.Marshal(map[string]any{
		"model": model,
		"messages": []any{
			map[string]any{"role": "assistant", "content": "previous turn"},
			map[string]any{"role": "tool", "tool_call_id": "call_sample", "content": text},
			map[string]any{"role": "user", "content": "explain the failure"},
		},
	})
	return Case{Name: name, Protocol: "chat", Model: model, Body: body}
}
