package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"tokensaver/pkg/adapters"
	"tokensaver/pkg/core"
)

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/__tokensaver/health" {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"version": Version,
			"adapter": cfg.Adapter,
			"mode":    cfg.Core.Mode,
			"enabled": cfg.Core.Enabled,
		})
		return
	}
	if !cfg.ManagementEnabled {
		http.NotFound(w, r)
		return
	}
	if !s.authorized(r, cfg) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="tokensaver"`)
		writeError(w, http.StatusUnauthorized, "unauthorized", "management token required")
		return
	}
	switch path {
	case "/__tokensaver/config", "/__tokensaver/export":
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, cfg)
		case http.MethodPut, http.MethodPost:
			s.replaceConfig(w, r)
		default:
			methodNotAllowed(w, "GET, PUT, POST")
		}
	case "/__tokensaver/import":
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			methodNotAllowed(w, "POST, PUT")
			return
		}
		s.replaceConfig(w, r)
	case "/__tokensaver/reload":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		if s.configPath == "" {
			writeError(w, http.StatusBadRequest, "no_config_path", "server was started without a config file")
			return
		}
		newCfg, err := LoadConfig(s.configPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
			return
		}
		if err := s.applyConfig(newCfg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": s.Config()})
	case "/__tokensaver/preview":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		s.preview(w, r)
	case "/__tokensaver/stats":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		writeJSON(w, http.StatusOK, s.stats.Snapshot())
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) authorized(r *http.Request, cfg Config) bool {
	if cfg.ManagementTokenEnv == "" {
		return ListenIsLoopback(cfg.Listen)
	}
	expected := os.Getenv(cfg.ManagementTokenEnv)
	if expected == "" {
		return ListenIsLoopback(cfg.Listen)
	}
	got := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(got), "bearer ") {
		got = strings.TrimSpace(got[7:])
	}
	if got == "" {
		got = strings.TrimSpace(r.Header.Get("X-TokenSaver-Management-Token"))
	}
	if len(got) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func (s *Server) replaceConfig(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read_error", "could not read config body")
		return
	}
	cfg := DefaultConfig()
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	if err := ensureJSONEOF(dec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	restartRequired := cfg.Listen != s.Config().Listen
	if s.configPath != "" {
		if err := SaveConfig(s.configPath, cfg); err != nil {
			writeError(w, http.StatusInternalServerError, "save_error", err.Error())
			return
		}
	}
	if err := s.applyConfig(cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"restart_required": restartRequired,
		"config":           s.Config(),
	})
}

func (s *Server) applyConfig(cfg Config) error {
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	u, err := url.Parse(cfg.Upstream)
	if err != nil {
		return err
	}
	profile, err := adapters.Resolve(cfg.Adapter)
	if err != nil {
		return err
	}
	client := buildClient(cfg)
	s.mu.Lock()
	oldClient := s.client
	s.cfg = cfg
	s.profile = profile
	s.upstream = u
	s.client = client
	s.mu.Unlock()
	if oldClient != nil {
		if tr, ok := oldClient.Transport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
	}
	return nil
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read_error", "could not read preview body")
		return
	}
	type previewEnvelope struct {
		Body     json.RawMessage `json:"body"`
		Protocol string          `json:"protocol"`
		Model    string          `json:"model"`
		Config   *core.Config    `json:"config"`
	}
	var env previewEnvelope
	requestBody := raw
	protocol := r.Header.Get("X-TokenSaver-Protocol")
	model := r.Header.Get("X-TokenSaver-Model")
	cfg := s.Config().Core
	_ = json.Unmarshal(raw, &env)
	if len(env.Body) > 0 {
		requestBody = env.Body
		if env.Protocol != "" {
			protocol = env.Protocol
		}
		if env.Model != "" {
			model = env.Model
		}
		if env.Config != nil {
			cfg = *env.Config
			cfg.ApplyDefaults()
			if err := cfg.Validate(); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
				return
			}
		}
	}
	if protocol == "" {
		protocol = inferPreviewProtocol(requestBody)
	}
	res := core.CompressRequest(requestBody, cfg, core.RequestMeta{Protocol: protocol, Model: model})
	var outBody any
	if err := json.Unmarshal(res.Body, &outBody); err != nil {
		outBody = string(res.Body)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"applied": res.Applied,
		"stats":   res.Stats,
		"body":    outBody,
	})
}

func inferPreviewProtocol(body []byte) string {
	var root map[string]any
	if json.Unmarshal(body, &root) == nil {
		if _, ok := root["input"]; ok {
			return "responses"
		}
		if _, ok := root["system"]; ok {
			return "anthropic"
		}
		if _, ok := root["messages"]; ok {
			return "chat"
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}
