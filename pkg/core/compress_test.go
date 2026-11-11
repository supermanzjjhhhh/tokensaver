package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.ThresholdTokens = 0
	cfg.MinSavingsTokens = 0
	cfg.PreservePrefixItems = 0
	return cfg
}

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	return v
}

func TestSafeChatCompressionProtectsCurrentTurnAndIdentifiers(t *testing.T) {
	body := []byte(`{
	  "model":"gpt-test",
	  "temperature":0.2,
	  "cache_control":{"type":"ephemeral"},
	  "messages":[
	    {"role":"system","content":"SYS  \n\n\n KEEP"},
	    {"role":"developer","content":"DEV KEEP"},
	    {"role":"user","content":"old request"},
	    {"role":"assistant","content":null,"tool_calls":[{"id":"call_123","type":"function","function":{"name":"read","arguments":"{\"path\":\"a\"}"}}]},
	    {"role":"tool","tool_call_id":"call_123","content":"{\n  \"a\": 1,\n  \"b\": [1, 2, 3],\n  \"payload\": \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n}"},
	    {"role":"user","content":"current request  \n\n\n keep me"},
	    {"role":"tool","tool_call_id":"call_current","content":"{\n  \"current\": true\n}"}
	  ],
	  "response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{"type":"object"}}}
	}`)
	cfg := testConfig()
	res := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "gpt-test"})
	if !res.Applied {
		t.Fatalf("expected applied, stats=%+v", res.Stats)
	}
	out := decodeMap(t, res.Body)
	msgs := out["messages"].([]any)
	if got := msgs[0].(map[string]any)["content"]; got != "SYS  \n\n\n KEEP" {
		t.Fatalf("system changed: %q", got)
	}
	if got := msgs[1].(map[string]any)["content"]; got != "DEV KEEP" {
		t.Fatalf("developer changed: %q", got)
	}
	tool := msgs[4].(map[string]any)
	if tool["tool_call_id"] != "call_123" {
		t.Fatalf("tool id changed: %v", tool["tool_call_id"])
	}
	if got := tool["content"]; got != `{"a":1,"b":[1,2,3],"payload":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}` {
		t.Fatalf("old tool output not minified: %#v", got)
	}
	if got := msgs[5].(map[string]any)["content"]; got != "current request  \n\n\n keep me" {
		t.Fatalf("current user changed: %q", got)
	}
	if got := msgs[6].(map[string]any)["content"]; got != "{\n  \"current\": true\n}" {
		t.Fatalf("current tool changed: %q", got)
	}
	if out["response_format"] == nil || out["cache_control"] == nil {
		t.Fatalf("top-level protected fields missing")
	}
	if res.Stats.FirstChangedItem != 4 {
		t.Fatalf("first changed item=%d, want 4", res.Stats.FirstChangedItem)
	}
}

func TestAnthropicPreservesToolUseAndCurrentToolResult(t *testing.T) {
	body := []byte(`{
	  "model":"claude-test",
	  "system":[{"type":"text","text":"SYSTEM KEEP","cache_control":{"type":"ephemeral"}}],
	  "messages":[
	    {"role":"assistant","content":[{"type":"tool_use","id":"toolu_old","name":"bash","input":{"command":"ls"}}]},
	    {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_old","content":"{\n  \"files\": [\"a\", \"b\"],\n  \"payload\": \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n}"}]},
	    {"role":"assistant","content":[{"type":"text","text":"old answer"}]},
	    {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_now","content":"{\n  \"now\": true\n}"},{"type":"text","text":"current request"}]}
	  ]
	}`)
	res := CompressRequest(body, testConfig(), RequestMeta{Protocol: "anthropic", Model: "claude-test"})
	if !res.Applied {
		t.Fatalf("expected applied, stats=%+v", res.Stats)
	}
	root := decodeMap(t, res.Body)
	msgs := root["messages"].([]any)
	oldAssistant := msgs[0].(map[string]any)
	oldToolUse := oldAssistant["content"].([]any)[0].(map[string]any)
	if oldToolUse["id"] != "toolu_old" {
		t.Fatalf("tool_use id changed")
	}
	oldResult := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if oldResult["tool_use_id"] != "toolu_old" || oldResult["content"] != `{"files":["a","b"],"payload":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}` {
		t.Fatalf("old tool result wrong: %#v", oldResult)
	}
	current := msgs[3].(map[string]any)["content"].([]any)[0].(map[string]any)
	if current["content"] != "{\n  \"now\": true\n}" {
		t.Fatalf("current tool result changed: %#v", current)
	}
	if root["system"] == nil {
		t.Fatalf("system missing")
	}
}

func TestResponsesProtectsReasoningCallsAndCurrentOutput(t *testing.T) {
	body := []byte(`{
	  "model":"gpt-5",
	  "instructions":"KEEP INSTRUCTIONS",
	  "input":[
	    {"type":"message","role":"user","content":[{"type":"input_text","text":"old request"}]},
	    {"type":"reasoning","id":"rs_1","summary":[]},
	    {"type":"function_call","id":"fc_1","call_id":"call_1","name":"shell","arguments":"{\"cmd\":\"ls\"}"},
	    {"type":"function_call_output","call_id":"call_1","output":"{\n  \"stdout\": \"a\\nb\",\n  \"payload\": \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n}"},
	    {"type":"message","role":"user","content":[{"type":"input_text","text":"current request"}]},
	    {"type":"function_call_output","call_id":"call_2","output":"{\n  \"current\": true\n}"}
	  ]
	}`)
	res := CompressRequest(body, testConfig(), RequestMeta{Protocol: "responses", Model: "gpt-5"})
	if !res.Applied {
		t.Fatalf("expected applied, stats=%+v", res.Stats)
	}
	root := decodeMap(t, res.Body)
	items := root["input"].([]any)
	if items[1].(map[string]any)["type"] != "reasoning" {
		t.Fatalf("reasoning changed")
	}
	if items[2].(map[string]any)["call_id"] != "call_1" {
		t.Fatalf("function call id changed")
	}
	if got := items[3].(map[string]any)["output"]; got != `{"stdout":"a\nb","payload":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}` {
		t.Fatalf("old output not minified: %#v", got)
	}
	if got := items[5].(map[string]any)["output"]; got != "{\n  \"current\": true\n}" {
		t.Fatalf("current output changed: %#v", got)
	}
	if root["instructions"] != "KEEP INSTRUCTIONS" {
		t.Fatalf("instructions changed")
	}
}

func TestIdempotentCompression(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"first"},{"role":"assistant","content":"answer  \n\n\n text"},{"role":"user","content":"now"}]}`)
	cfg := testConfig()
	cfg.PreservePrefixItems = 1
	r1 := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "m"})
	if !r1.Applied {
		t.Fatalf("first pass not applied: %+v", r1.Stats)
	}
	r2 := CompressRequest(r1.Body, cfg, RequestMeta{Protocol: "chat", Model: "m"})
	if string(r2.Body) != string(r1.Body) {
		t.Fatalf("not idempotent\nfirst=%s\nsecond=%s", r1.Body, r2.Body)
	}
}

func TestLossyEnginesRequireAllowLossy(t *testing.T) {
	long := strings.Repeat("line of build output\n", 2000) + "fatal: boom\n"
	body, _ := json.Marshal(map[string]any{"model": "m", "messages": []any{
		map[string]any{"role": "assistant", "content": "old"},
		map[string]any{"role": "tool", "tool_call_id": "c1", "content": long},
		map[string]any{"role": "user", "content": "now"},
	}})
	cfg := testConfig()
	cfg.Mode = ModeCustom
	cfg.Engines = []EngineConfig{{ID: EngineWhitespace, Enabled: true}, {ID: EngineDiagnosticTrim, Enabled: true}}
	res := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "m"})
	for _, e := range res.Stats.Engines {
		if IsLossyEngine(e) {
			t.Fatalf("lossy engine %s ran while allow_lossy=false", e)
		}
	}
	cfg.AllowLossy = true
	res2 := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "m"})
	if !res2.Applied || !res2.Stats.Lossy || !contains(res2.Stats.Engines, EngineDiagnosticTrim) {
		t.Fatalf("lossy engines did not run: %+v", res2.Stats)
	}
	cfg.Mode = ModeCustom
	cfg.Engines = []EngineConfig{{ID: EngineToolTruncate, Enabled: true}}
	cfg.ToolTruncate.MaxChars = 512
	cfg.ToolTruncate.MinChars = 600
	res3 := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "m"})
	if !res3.Applied || !contains(res3.Stats.Engines, EngineToolTruncate) {
		t.Fatalf("tool truncate did not run: %+v", res3.Stats)
	}
}

func TestModelDenyAndThresholdSkip(t *testing.T) {
	body := []byte(`{"model":"private","messages":[{"role":"assistant","content":"a  \n\n\n b"},{"role":"user","content":"now"}]}`)
	cfg := testConfig()
	cfg.ModelDeny = []string{"private*"}
	res := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "private"})
	if res.Applied || !strings.Contains(res.Stats.SkippedReason, "model") || string(res.Body) != string(body) {
		t.Fatalf("model deny failed: %+v", res.Stats)
	}
	cfg = testConfig()
	cfg.ThresholdTokens = 100000
	res = CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "m"})
	if res.Applied || res.Stats.SkippedReason != "below-threshold" {
		t.Fatalf("threshold failed: %+v", res.Stats)
	}
}

func TestWhitespacePreservesFencedCode(t *testing.T) {
	in := "text   \n\n\n\n```go\nfmt.Println(\"x\")   \n\n\n\n```\nend   "
	want := "text\n\n```go\nfmt.Println(\"x\")   \n\n\n\n```\nend"
	if got := normalizeWhitespaceV1(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnsupportedAndInvalidPassThrough(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`not json`),
		[]byte(`{"prompt":"x"}`),
		[]byte(`{"input":"a string"}`),
		[]byte(`{"messages":"wrong"}`),
	} {
		res := CompressRequest(body, testConfig(), RequestMeta{})
		if res.Applied || string(res.Body) != string(body) {
			t.Fatalf("unsupported body mutated: %s", body)
		}
		if res.Stats.Fallback {
			t.Fatalf("unsupported input should skip, not fallback: %+v", res.Stats)
		}
	}
}

func TestPrefixPreserved(t *testing.T) {
	body := []byte(`{"model":"m","messages":[
	{"role":"user","content":"prefix  \n\n\n keep"},
	{"role":"assistant","content":"old  \n\n\n text"},
	{"role":"user","content":"now"}]}`)
	cfg := testConfig()
	cfg.PreservePrefixItems = 1
	res := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "m"})
	if !res.Applied || res.Stats.PrefixPreservedItems < 1 {
		t.Fatalf("prefix not preserved: %+v", res.Stats)
	}
	root := decodeMap(t, res.Body)
	if got := root["messages"].([]any)[0].(map[string]any)["content"]; got != "prefix  \n\n\n keep" {
		t.Fatalf("prefix changed: %q", got)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
