package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func gatewayTestConfig(upstream string) Config {
	cfg := DefaultConfig()
	cfg.Upstream = upstream
	cfg.Adapter = "agent2api"
	cfg.Listen = "127.0.0.1:9231"
	cfg.ManagementEnabled = true
	cfg.Core.ThresholdTokens = 0
	cfg.Core.MinSavingsTokens = 0
	cfg.Core.PreservePrefixItems = 0
	return cfg
}

func TestProxyCompressesAndPreservesAuth(t *testing.T) {
	var gotBody []byte
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	cfg := gatewayTestConfig(upstream.URL)
	s, err := NewServerWithConfig("", cfg)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(s.Handler())
	defer proxy.Close()

	body := `{"model":"m","messages":[{"role":"assistant","content":"old"},{"role":"tool","tool_call_id":"c1","content":"{\n  \"payload\": \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n}"},{"role":"user","content":"now"}]}`
	req, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if gotAuth != "Bearer test-secret" {
		t.Fatalf("auth header not preserved: %q", gotAuth)
	}
	if !bytes.Contains(gotBody, []byte(`"content":"{\"payload\"`)) || bytes.Contains(gotBody, []byte(`{\n  \"payload\"`)) {
		t.Fatalf("upstream did not receive compact body: %s", gotBody)
	}
	if resp.Header.Get("X-TokenSaver-Applied") != "true" {
		t.Fatalf("missing applied header: %v", resp.Header)
	}
	if s.Stats().CompressedRequests != 1 {
		t.Fatalf("stats not recorded: %+v", s.Stats())
	}
}

func TestSSEPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"delta\":\"he\"}\n\n"))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"delta\":\"llo\"}\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()
	cfg := gatewayTestConfig(upstream.URL)
	s, _ := NewServerWithConfig("", cfg)
	proxy := httptest.NewServer(s.Handler())
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// GET /v1/models is pass-through but not SSE; use a POST path to exercise SSE.
	req, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true,"messages":[{"role":"user","content":"now"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello") && !strings.Contains(string(body), `"delta":"he"`) {
		t.Fatalf("SSE payload missing: %q", body)
	}
}

func TestOversizedBodyPassesThrough(t *testing.T) {
	received := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received <- string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	cfg := gatewayTestConfig(upstream.URL)
	cfg.MaxRequestBodyBytes = 1024
	s, _ := NewServerWithConfig("", cfg)
	proxy := httptest.NewServer(s.Handler())
	defer proxy.Close()

	payload := `{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("x", 3000) + `"}]}`
	req, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case got := <-received:
		if got != payload {
			t.Fatalf("oversized body changed: got %d bytes want %d", len(got), len(payload))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive body")
	}
}

func TestUpstreamErrorSemantics(t *testing.T) {
	cfg := gatewayTestConfig("http://127.0.0.1:1")
	s, _ := NewServerWithConfig("", cfg)
	proxy := httptest.NewServer(s.Handler())
	defer proxy.Close()
	req, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["error"] == nil {
		t.Fatalf("error shape missing: %v", out)
	}
}

func TestManagementPreviewConfigAndStats(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer upstream.Close()
	cfg := gatewayTestConfig(upstream.URL)
	s, _ := NewServerWithConfig("", cfg)
	proxy := httptest.NewServer(s.Handler())
	defer proxy.Close()

	preview := `{"protocol":"chat","model":"m","body":{"model":"m","messages":[{"role":"assistant","content":"a  \n\n\n b"},{"role":"user","content":"now"}]}}`
	resp, err := http.Post(proxy.URL+"/__tokensaver/preview", "application/json", strings.NewReader(preview))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte(`"applied"`)) {
		t.Fatalf("preview failed: status=%d body=%s", resp.StatusCode, body)
	}

	cfgResp, err := http.Get(proxy.URL + "/__tokensaver/config")
	if err != nil {
		t.Fatal(err)
	}
	cfgBody, _ := io.ReadAll(cfgResp.Body)
	cfgResp.Body.Close()
	if cfgResp.StatusCode != 200 || !bytes.Contains(cfgBody, []byte(`"upstream"`)) {
		t.Fatalf("config failed: %d %s", cfgResp.StatusCode, cfgBody)
	}

	newCfg := cfg
	newCfg.Core.Mode = "off"
	data, _ := json.Marshal(newCfg)
	putReq, _ := http.NewRequest(http.MethodPut, proxy.URL+"/__tokensaver/config", bytes.NewReader(data))
	putReq.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatal(err)
	}
	putResp.Body.Close()
	if putResp.StatusCode != 200 {
		t.Fatalf("config put status=%d", putResp.StatusCode)
	}
	if s.Config().Core.Mode != "off" {
		t.Fatalf("config not applied")
	}

	statsResp, err := http.Get(proxy.URL + "/__tokensaver/stats")
	if err != nil {
		t.Fatal(err)
	}
	statsBody, _ := io.ReadAll(statsResp.Body)
	statsResp.Body.Close()
	if statsResp.StatusCode != 200 || !bytes.Contains(statsBody, []byte(`"requests"`)) {
		t.Fatalf("stats failed: %d %s", statsResp.StatusCode, statsBody)
	}
}

func TestManagementTokenAndBypass(t *testing.T) {
	t.Setenv("TEST_TOKENSAVER_TOKEN", "secret")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer upstream.Close()
	cfg := gatewayTestConfig(upstream.URL)
	cfg.ManagementTokenEnv = "TEST_TOKENSAVER_TOKEN"
	s, _ := NewServerWithConfig("", cfg)
	proxy := httptest.NewServer(s.Handler())
	defer proxy.Close()

	resp, _ := http.Get(proxy.URL + "/__tokensaver/config")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/__tokensaver/config", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestConcurrentSessionsStayIsolated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	defer upstream.Close()
	cfg := gatewayTestConfig(upstream.URL)
	s, _ := NewServerWithConfig("", cfg)
	proxy := httptest.NewServer(s.Handler())
	defer proxy.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := string(rune('a' + i))
			body := `{"model":"m","messages":[{"role":"assistant","content":"old"},{"role":"user","content":"` + id + `"}]}`
			req, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- err
				return
			}
			rb, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if !bytes.Contains(rb, []byte(`"content":"`+id+`"`)) {
				errs <- &testError{msg: "session body mixed"}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestClientCancellationPropagates(t *testing.T) {
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-req.Context().Done()
		canceled <- struct{}{}
		return nil, req.Context().Err()
	})
	cfg := gatewayTestConfig("http://upstream.invalid")
	cfg.ResponseHeaderTimeoutMS = 5000
	s, _ := NewServerWithConfig("", cfg)
	s.client = &http.Client{Transport: rt}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"x"}]}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.serveHTTP(recorder, req)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("upstream request did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream context was not canceled")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("proxy handler did not return after cancellation")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
