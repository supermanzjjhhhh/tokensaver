package adapters

import (
	"encoding/json"
	"fmt"
	"net/http"

	"tokensaver/pkg/core"
)

// CPARequestInterceptRequest mirrors the fields used from CLIProxyAPI
// v8.0.60 / commit 91e0dee schema 10 request.intercept_before payload.
type CPARequestInterceptRequest struct {
	RequestID      string         `json:"request_id"`
	TraceID        string         `json:"trace_id"`
	SourceFormat   string         `json:"source_format"`
	ToFormat       string         `json:"to_format"`
	Model          string         `json:"model"`
	RequestedModel string         `json:"requested_model"`
	Stream         bool           `json:"stream"`
	Headers        http.Header    `json:"headers"`
	Body           []byte         `json:"body"`
	CallerScope    string         `json:"caller_scope"`
	Metadata       map[string]any `json:"metadata"`
}

type CPARequestInterceptResponse struct {
	Path            string      `json:"path,omitempty"`
	Headers         http.Header `json:"headers,omitempty"`
	Body            []byte      `json:"body,omitempty"`
	Terminate       bool        `json:"terminate,omitempty"`
	StatusCode      int         `json:"status_code,omitempty"`
	ResponseHeaders http.Header `json:"response_headers,omitempty"`
	ResponseBody    []byte      `json:"response_body,omitempty"`
}

func CPAConfigFromPluginConfig(raw []byte) (core.Config, error) {
	cfg := core.DefaultConfig()
	if len(raw) == 0 {
		return cfg, cfg.Validate()
	}
	// The CLIProxyAPI config YAML is not JSON. Only an explicit JSON config_json
	// is consumed here; the documented recommendation is to keep the full
	// config in a file and pass a path so both platforms share one structure.
	var wrapper struct {
		ConfigJSON json.RawMessage `json:"config_json"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil || len(wrapper.ConfigJSON) == 0 {
		return cfg, cfg.Validate()
	}
	if err := json.Unmarshal(wrapper.ConfigJSON, &cfg); err != nil {
		return core.Config{}, fmt.Errorf("config_json: %w", err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return core.Config{}, err
	}
	return cfg, nil
}

func InterceptForCPA(req CPARequestInterceptRequest, cfg core.Config) (CPARequestInterceptResponse, core.Stats) {
	protocol := req.SourceFormat
	if protocol == "" {
		protocol = req.ToFormat
	}
	res := core.CompressRequest(req.Body, cfg, core.RequestMeta{
		Protocol:     protocol,
		Model:        req.Model,
		SourceFormat: req.SourceFormat,
		RequestID:    req.RequestID,
		Stream:       req.Stream,
	})
	if !res.Applied {
		return CPARequestInterceptResponse{}, res.Stats
	}
	// Body must be returned explicitly; the host treats request body as read-only.
	return CPARequestInterceptResponse{Body: res.Body}, res.Stats
}
