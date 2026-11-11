package adapters

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"tokensaver/pkg/core"
)

func TestCPAInterceptRewritesBodyAndPreservesHeaders(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.ThresholdTokens = 0
	cfg.MinSavingsTokens = 0
	cfg.PreservePrefixItems = 0
	req := CPARequestInterceptRequest{
		RequestID:    "r1",
		SourceFormat: "claude",
		Model:        "claude-test",
		Headers:      http.Header{"Authorization": {"Bearer x"}},
		Body:         []byte(`{"messages":[{"role":"assistant","content":[{"type":"text","text":"old  \n\n\n text"}]},{"role":"user","content":"now"}]}`),
	}
	resp, stats := InterceptForCPA(req, cfg)
	if !stats.Fallback && len(resp.Body) == 0 && stats.SkippedReason == "no-effective-change" {
		t.Fatalf("unexpected no-op: %+v", stats)
	}
	if len(resp.Body) > 0 {
		if resp.Headers != nil {
			t.Fatalf("headers should not be rewritten")
		}
		if strings.Contains(string(resp.Body), "old  \n\n\n text") {
			t.Fatalf("body not compressed: %s", resp.Body)
		}
	}
}

func TestCPAConfigFromPluginConfig(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Mode = "balanced"
	inner, _ := json.Marshal(cfg)
	raw, _ := json.Marshal(map[string]any{"config_json": json.RawMessage(inner)})
	got, err := CPAConfigFromPluginConfig(raw)
	if err != nil || got.Mode != "balanced" {
		t.Fatalf("got mode=%q err=%v", got.Mode, err)
	}
}
