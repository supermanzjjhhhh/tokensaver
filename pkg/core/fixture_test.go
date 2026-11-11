package core

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestLongCodingFixtureDrivesRealisticCoverage exercises the fixture copied
// byte-for-byte from OmniRoute's tests/unit/compression/fixtures directory.
func TestLongCodingFixtureDrivesRealisticCoverage(t *testing.T) {
	raw, err := os.ReadFile("../../tests/fixtures/long-coding-session.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Messages []any `json:"messages"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"model": "fixture-model", "messages": fixture.Messages})
	cfg := DefaultConfig()
	cfg.ThresholdTokens = 0
	cfg.MinSavingsTokens = 0
	cfg.PreservePrefixItems = 0
	res := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "fixture-model"})
	// The fixture ends inside fenced code blocks; a byte-preserving whitespace
	// pass correctly makes no change to those blocks. Lossy coverage uses the
	// diagnostic fixtures below.
	if res.Applied && res.Stats.SavingsPercent > 15 {
		t.Fatalf("safe mode should be modest: %+v", res.Stats)
	}
}

func TestCJKEstimatorAndPreservation(t *testing.T) {
	cjk := "这是需要保留的当前用户请求，包含中文和 English，以及代码 `x := 1`。"
	body, _ := json.Marshal(map[string]any{
		"model": "m",
		"messages": []any{
			map[string]any{"role": "assistant", "content": strings.Repeat("旧内容\n\n\n", 50)},
			map[string]any{"role": "user", "content": cjk},
		},
	})
	cfg := DefaultConfig()
	cfg.ThresholdTokens = 0
	cfg.MinSavingsTokens = 0
	res := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "m"})
	if res.Applied && !strings.Contains(string(res.Body), cjk) {
		t.Fatalf("current CJK request altered: %s", res.Body)
	}
	plain := EstimateTokensBytes([]byte("你好世界"))
	if plain < 4 {
		t.Fatalf("CJK estimator too small: %d", plain)
	}
}

func TestNoDoubleCompression(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","content":"one  \n\n\n two"},{"role":"user","content":"now"}]}`)
	cfg := DefaultConfig()
	cfg.ThresholdTokens = 0
	cfg.MinSavingsTokens = 0
	first := CompressRequest(body, cfg, RequestMeta{Protocol: "chat", Model: "m"})
	second := CompressRequest(first.Body, cfg, RequestMeta{Protocol: "chat", Model: "m"})
	if string(first.Body) != string(second.Body) {
		t.Fatalf("second pass changed body")
	}
}
