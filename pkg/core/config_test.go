package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRejectsTrailingJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	if err := os.WriteFile(path, []byte(`{"mode":"safe"} {"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected trailing JSON error")
	}
}

func TestConfigRoundTripAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	cfg := DefaultConfig()
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != cfg.Mode || got.ThresholdTokens != cfg.ThresholdTokens || len(got.Engines) != len(cfg.Engines) {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestConfigRejectsDuplicateAndUnknownEngines(t *testing.T) {
	for _, engines := range [][]EngineConfig{
		{{ID: EngineWhitespace, Enabled: true}, {ID: EngineWhitespace, Enabled: true}},
		{{ID: "not-an-engine", Enabled: true}},
	} {
		cfg := DefaultConfig()
		cfg.Engines = engines
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected validation error: %+v", cfg)
		}
	}
}

func TestConfigRejectsNegativeLossyThresholds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ToolTruncate.MinChars = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative tool_truncate.min_chars to be rejected")
	}
	cfg = DefaultConfig()
	cfg.DiagnosticTrim.MinChars = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative diagnostic_trim.min_chars to be rejected")
	}
}

func TestConfigNormalizesModeCase(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = "  SAFE  "
	cfg.ApplyDefaults()
	if cfg.Mode != ModeSafe {
		t.Fatalf("mode=%q, want %q", cfg.Mode, ModeSafe)
	}
}

func TestModelPatterns(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ModelAllow = []string{"openai/*", "claude-*"}
	cfg.ModelDeny = []string{"*-private"}
	tests := map[string]bool{
		"openai/gpt-5":   true,
		"claude-sonnet":  true,
		"gpt-5":          false,
		"openai/private": true,
		"x-private":      false,
	}
	for model, want := range tests {
		if got := ModelAllowed(cfg, model); got != want {
			t.Errorf("%s: got %v want %v", model, got, want)
		}
	}
}

func TestTrailingDataPassThrough(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"x"}]} garbage`)
	res := CompressRequest(body, testConfig(), RequestMeta{Protocol: "chat", Model: "m"})
	if string(res.Body) != string(body) || res.Stats.SkippedReason == "" {
		t.Fatalf("trailing data not passed through: %+v", res.Stats)
	}
	if !strings.Contains(res.Stats.SkippedReason, "trailing") {
		t.Fatalf("unexpected reason: %s", res.Stats.SkippedReason)
	}
}
