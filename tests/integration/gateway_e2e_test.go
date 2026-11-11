package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tokensaver/pkg/gateway"
)

func TestGatewayChatWithToolOutputAndSSE(t *testing.T) {
	var received []byte
	var auth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	cfg := gateway.DefaultConfig()
	cfg.Upstream = upstream.URL
	cfg.Adapter = "cpa"
	cfg.Core.ThresholdTokens = 0
	cfg.Core.MinSavingsTokens = 0
	s, err := gateway.NewServerWithConfig("", cfg)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(s.Handler())
	defer proxy.Close()

	body := `{"model":"m","stream":true,"messages":[
	  {"role":"assistant","content":null,"tool_calls":[{"id":"call_A","type":"function","function":{"name":"read","arguments":"{\"path\":\"a\"}"}}]},
	  {"role":"tool","tool_call_id":"call_A","content":"{\n  \"a\": \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n}"},
	  {"role":"user","content":"now"}]}`
	req, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !bytes.Contains(payload, []byte("[DONE]")) {
		t.Fatalf("stream failed: %d %s", resp.StatusCode, payload)
	}
	if auth != "Bearer x" {
		t.Fatalf("auth not forwarded")
	}
	var root map[string]any
	if err := json.Unmarshal(received, &root); err != nil {
		t.Fatalf("upstream got invalid JSON: %v", err)
	}
	msgs := root["messages"].([]any)
	if msgs[1].(map[string]any)["tool_call_id"] != "call_A" {
		t.Fatalf("tool id changed")
	}
	if root["stream"] != true {
		t.Fatalf("stream flag changed")
	}
}
