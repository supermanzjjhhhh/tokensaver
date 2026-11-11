package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	ModeOff        = "off"
	ModeSafe       = "safe"
	ModeBalanced   = "balanced"
	ModeAggressive = "aggressive"
	ModeCustom     = "custom"

	EngineWhitespace     = "whitespace"
	EngineJSONMinify     = "json-minify"
	EngineExactDedup     = "exact-dedup"
	EngineDiagnosticTrim = "diagnostic-trim"
	EngineToolTruncate   = "tool-truncate"
)

type EngineConfig struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type ToolTruncateConfig struct {
	MaxChars int     `json:"max_chars"`
	HeadPct  float64 `json:"head_pct"`
	TailPct  float64 `json:"tail_pct"`
	MinChars int     `json:"min_chars"`
}

type DiagnosticTrimConfig struct {
	MaxLines           int  `json:"max_lines"`
	MinChars           int  `json:"min_chars"`
	PreserveErrorLines bool `json:"preserve_error_lines"`
}

type LogConfig struct {
	Enabled       bool `json:"enabled"`
	MaxEntries    int  `json:"max_entries"`
	IncludeModel  bool `json:"include_model"`
	IncludeEngine bool `json:"include_engine"`
}

type Config struct {
	Enabled             bool                 `json:"enabled"`
	Mode                string               `json:"mode"`
	ThresholdTokens     int                  `json:"threshold_tokens"`
	MinSavingsTokens    int                  `json:"min_savings_tokens"`
	PreservePrefixItems int                  `json:"preserve_prefix_items"`
	PreserveLastUser    bool                 `json:"preserve_last_user"`
	ProtectSystem       bool                 `json:"protect_system"`
	AllowLossy          bool                 `json:"allow_lossy"`
	ModelAllow          []string             `json:"model_allow"`
	ModelDeny           []string             `json:"model_deny"`
	Engines             []EngineConfig       `json:"engines"`
	ToolTruncate        ToolTruncateConfig   `json:"tool_truncate"`
	DiagnosticTrim      DiagnosticTrimConfig `json:"diagnostic_trim"`
	Logging             LogConfig            `json:"logging"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:             true,
		Mode:                ModeSafe,
		ThresholdTokens:     512,
		MinSavingsTokens:    16,
		PreservePrefixItems: 1,
		PreserveLastUser:    true,
		ProtectSystem:       true,
		AllowLossy:          false,
		ModelAllow:          []string{"*"},
		ModelDeny:           []string{},
		Engines: []EngineConfig{
			{ID: EngineWhitespace, Enabled: true},
			{ID: EngineJSONMinify, Enabled: true},
			{ID: EngineExactDedup, Enabled: false},
			{ID: EngineDiagnosticTrim, Enabled: false},
			{ID: EngineToolTruncate, Enabled: false},
		},
		ToolTruncate:   ToolTruncateConfig{MaxChars: 8000, HeadPct: 0.60, TailPct: 0.30, MinChars: 9000},
		DiagnosticTrim: DiagnosticTrimConfig{MaxLines: 140, MinChars: 12000, PreserveErrorLines: true},
		Logging:        LogConfig{Enabled: true, MaxEntries: 500, IncludeModel: true, IncludeEngine: true},
	}
}

func (c Config) Clone() Config {
	out := c
	out.ModelAllow = append([]string(nil), c.ModelAllow...)
	out.ModelDeny = append([]string(nil), c.ModelDeny...)
	out.Engines = append([]EngineConfig(nil), c.Engines...)
	return out
}

func (c *Config) ApplyDefaults() {
	d := DefaultConfig()
	if c.Mode == "" {
		c.Mode = d.Mode
	}
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	if c.Engines == nil {
		c.Engines = d.Engines
	}
	if c.ThresholdTokens < 0 {
		c.ThresholdTokens = 0
	}
	if c.MinSavingsTokens < 0 {
		c.MinSavingsTokens = 0
	}
	if c.PreservePrefixItems < 0 {
		c.PreservePrefixItems = 0
	}
	if c.ToolTruncate.MaxChars == 0 {
		c.ToolTruncate = d.ToolTruncate
	}
	if c.DiagnosticTrim.MaxLines == 0 {
		c.DiagnosticTrim = d.DiagnosticTrim
	}
	if c.Logging.MaxEntries == 0 {
		c.Logging.MaxEntries = d.Logging.MaxEntries
	}
	if len(c.ModelAllow) == 0 {
		c.ModelAllow = []string{"*"}
	}
}

func (c Config) Validate() error {
	mode := strings.ToLower(strings.TrimSpace(c.Mode))
	switch mode {
	case ModeOff, ModeSafe, ModeBalanced, ModeAggressive, ModeCustom:
	default:
		return fmt.Errorf("unsupported mode %q", c.Mode)
	}
	seen := map[string]bool{}
	for _, e := range c.Engines {
		switch e.ID {
		case EngineWhitespace, EngineJSONMinify, EngineExactDedup, EngineDiagnosticTrim, EngineToolTruncate:
		default:
			return fmt.Errorf("unsupported engine %q", e.ID)
		}
		if seen[e.ID] {
			return fmt.Errorf("duplicate engine %q", e.ID)
		}
		seen[e.ID] = true
	}
	if c.ToolTruncate.MaxChars < 256 {
		return errors.New("tool_truncate.max_chars must be >= 256")
	}
	if c.ToolTruncate.HeadPct < 0 || c.ToolTruncate.TailPct < 0 || c.ToolTruncate.HeadPct+c.ToolTruncate.TailPct > 1 {
		return errors.New("tool_truncate head_pct/tail_pct must be non-negative and sum to <= 1")
	}
	if c.ToolTruncate.MinChars < 0 {
		return errors.New("tool_truncate.min_chars must be >= 0")
	}
	if c.DiagnosticTrim.MaxLines < 8 {
		return errors.New("diagnostic_trim.max_lines must be >= 8")
	}
	if c.DiagnosticTrim.MinChars < 0 {
		return errors.New("diagnostic_trim.min_chars must be >= 0")
	}
	if c.Logging.MaxEntries < 1 {
		return errors.New("logging.max_entries must be >= 1")
	}
	return nil
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg := DefaultConfig()
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if err := ensureJSONEOF(dec); err != nil {
		return Config{}, err
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return errors.New("trailing JSON data")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func SaveConfig(path string, cfg Config) error {
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tokensaver-config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (c Config) EffectiveEngines() []EngineConfig {
	if c.Mode == ModeOff || !c.Enabled {
		return nil
	}
	switch c.Mode {
	case ModeSafe:
		return []EngineConfig{
			{ID: EngineWhitespace, Enabled: true},
			{ID: EngineJSONMinify, Enabled: true},
		}
	case ModeBalanced:
		return []EngineConfig{
			{ID: EngineWhitespace, Enabled: true},
			{ID: EngineJSONMinify, Enabled: true},
			{ID: EngineDiagnosticTrim, Enabled: true},
		}
	case ModeAggressive:
		return []EngineConfig{
			{ID: EngineWhitespace, Enabled: true},
			{ID: EngineJSONMinify, Enabled: true},
			{ID: EngineDiagnosticTrim, Enabled: true},
			{ID: EngineToolTruncate, Enabled: true},
		}
	default:
		out := make([]EngineConfig, 0, len(c.Engines))
		for _, e := range c.Engines {
			if e.Enabled {
				out = append(out, e)
			}
		}
		return out
	}
}

func IsLossyEngine(id string) bool {
	return id == EngineDiagnosticTrim || id == EngineToolTruncate
}
