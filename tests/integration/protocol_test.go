package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"tokensaver/pkg/core"
)

func run(t *testing.T, protocol, body string) (map[string]any, core.Stats) {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.ThresholdTokens = 0
	cfg.MinSavingsTokens = 0
	cfg.PreservePrefixItems = 0
	res := core.CompressRequest([]byte(body), cfg, core.RequestMeta{Protocol: protocol, Model: "test-model"})
	if res.Stats.Fallback {
		t.Fatalf("unexpected fallback: %+v", res.Stats)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return out, res.Stats
}

func TestChatSSEAndToolRoundTripShape(t *testing.T) {
	out, st := run(t, "chat", `{
	  "model":"test-model","stream":true,
	  "messages":[
	    {"role":"assistant","content":null,"tool_calls":[{"id":"call_A","type":"function","function":{"name":"shell","arguments":"{\"cmd\":\"git diff\"}"}}]},
	    {"role":"tool","tool_call_id":"call_A","name":"shell","content":"{\n  \"stdout\": \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n}"},
	    {"role":"user","content":"now"}
	  ]
	}`)
	msgs := out["messages"].([]any)
	tc := msgs[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if tc["id"] != "call_A" {
		t.Fatalf("tool call id changed")
	}
	if msgs[1].(map[string]any)["tool_call_id"] != "call_A" {
		t.Fatalf("tool result id changed")
	}
	if out["stream"] != true {
		t.Fatalf("stream field changed")
	}
	_ = st
}

func TestResponsesReasoningAndJSONSchemaUntouched(t *testing.T) {
	out, _ := run(t, "responses", `{
	  "model":"gpt-5","instructions":"SYSTEM","text":{"format":{"type":"json_schema","name":"x","schema":{"type":"object","properties":{"a":{"type":"string"}}}}},
	  "input":[
	    {"type":"message","role":"user","content":[{"type":"input_text","text":"old"}]},
	    {"type":"reasoning","id":"rs_1","encrypted_content":"opaque"},
	    {"type":"function_call","id":"fc_1","call_id":"call_1","name":"shell","arguments":"{\"cmd\":\"ls\"}"},
	    {"type":"function_call_output","call_id":"call_1","output":"{\n  \"stdout\": \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n}"},
	    {"type":"message","role":"user","content":[{"type":"input_text","text":"current"}]}
	  ]
	}`)
	if out["instructions"] != "SYSTEM" {
		t.Fatalf("instructions changed")
	}
	text := out["text"].(map[string]any)
	if text["format"].(map[string]any)["type"] != "json_schema" {
		t.Fatalf("json schema gone")
	}
	items := out["input"].([]any)
	if items[1].(map[string]any)["encrypted_content"] != "opaque" {
		t.Fatalf("reasoning changed")
	}
	if !strings.Contains(items[3].(map[string]any)["output"].(string), `"stdout"`) {
		t.Fatalf("tool output invalid")
	}
}

func TestAnthropicSystemCacheControlPreserved(t *testing.T) {
	out, _ := run(t, "anthropic", `{
	  "model":"claude","system":[{"type":"text","text":"SYS","cache_control":{"type":"ephemeral"}}],
	  "messages":[
	    {"role":"assistant","content":[{"type":"text","text":"old  \n\n\n text"}]},
	    {"role":"user","content":"current"}
	  ],
	  "tools":[{"name":"shell","input_schema":{"type":"object"}}]
	}`)
	sys := out["system"].([]any)[0].(map[string]any)
	if sys["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Fatalf("cache_control changed")
	}
	if out["tools"] == nil {
		t.Fatalf("tools disappeared")
	}
}

func TestMixedChineseEnglishAndCode(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"model": "m",
		"messages": []any{
			map[string]any{"role": "assistant", "content": "旧内容 old  \n\n\n ```go\nfmt.Println(\"x\")   \n\n\n```"},
			map[string]any{"role": "user", "content": "请保留当前请求 with English & code `x := 1`。"},
		},
	})
	cfg := core.DefaultConfig()
	cfg.ThresholdTokens = 0
	cfg.MinSavingsTokens = 0
	cfg.PreservePrefixItems = 0
	res := core.CompressRequest(body, cfg, core.RequestMeta{Protocol: "chat", Model: "m"})
	if res.Stats.Fallback {
		t.Fatalf("fallback")
	}
	var out map[string]any
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatal(err)
	}
	msgs := out["messages"].([]any)
	if !strings.Contains(msgs[1].(map[string]any)["content"].(string), "请保留当前请求") {
		t.Fatalf("CJK current altered")
	}
}
