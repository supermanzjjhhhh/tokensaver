package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkSafeChat(b *testing.B) {
	messages := []any{}
	for i := 0; i < 40; i++ {
		role := "assistant"
		if i%2 == 1 {
			role = "user"
		}
		messages = append(messages, map[string]any{"role": role, "content": strings.Repeat(fmt.Sprintf("turn-%d line\n\n\n", i), 20)})
	}
	messages = append(messages, map[string]any{"role": "tool", "tool_call_id": "c1", "content": `{
  "files": ["a.go", "b.go"],
  "payload": "` + strings.Repeat("x", 4000) + `"
}`})
	messages = append(messages, map[string]any{"role": "user", "content": "current task"})
	body, _ := json.Marshal(map[string]any{"model": "gpt-test", "messages": messages})
	cfg := DefaultConfig()
	cfg.ThresholdTokens = 0
	cfg.MinSavingsTokens = 0
	cfg.PreservePrefixItems = 0

	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for i := 0; i < b.N; i++ {
		res := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "gpt-test"})
		if len(res.Body) == 0 {
			b.Fatal("empty output")
		}
	}
	b.Logf("input_bytes=%d", len(body))
}
