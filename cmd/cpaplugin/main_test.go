package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"tokensaver/pkg/adapters"
	"tokensaver/pkg/core"
)

type callEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
}

func call(t *testing.T, method string, req any) callEnvelope {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	out, err := handleMethod(method, raw)
	if err != nil {
		t.Fatal(err)
	}
	var env callEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("invalid envelope: %v: %s", err, out)
	}
	if !env.OK {
		t.Fatalf("plugin returned error: %s", out)
	}
	return env
}

func managementCall(t *testing.T, req managementRequest) managementResponse {
	t.Helper()
	env := call(t, "management.handle", req)
	var resp managementResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("invalid management response: %v: %s", err, env.Result)
	}
	return resp
}

func resetPluginState() {
	stateMu.Lock()
	cfg = core.DefaultConfig()
	configPath = ""
	stateMu.Unlock()
	stats = core.NewStatsStore(500)
}

func TestInterceptMalformedFailsOpen(t *testing.T) {
	if _, err := intercept([]byte(`{"request_id":`)); err == nil {
		t.Fatal("expected parse error for malformed intercept")
	}
	// The C entrypoint converts the error into an error envelope and returns a
	// non-zero transport code; the host must keep the original body. There is
	// no Terminate response in this path.
}

func TestRegisterAndInterceptLifecycle(t *testing.T) {
	resetPluginState()
	cfg := coreDefaultForTest()
	configJSON, _ := json.Marshal(cfg)
	env := call(t, "plugin.register", map[string]any{
		"schema_version": 10,
		"config_json":    configJSON,
	})
	var reg registration
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatal(err)
	}
	if reg.SchemaVersion != 10 || !reg.Capabilities.RequestInterceptor || !reg.Capabilities.ManagementAPI {
		t.Fatalf("bad registration: %+v", reg)
	}

	req := adapters.CPARequestInterceptRequest{
		RequestID:    "req-1",
		SourceFormat: "openai",
		ToFormat:     "openai",
		Model:        "gpt-test",
		Stream:       true,
		Headers:      http.Header{"Authorization": {"Bearer test"}},
		Body:         []byte(`{"model":"gpt-test","messages":[{"role":"assistant","content":"old"},{"role":"tool","tool_call_id":"c1","content":"{\n  \"payload\": \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n}"},{"role":"user","content":"now"}]}`),
	}
	env = call(t, "request.intercept_before", req)
	var resp adapters.CPARequestInterceptResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Body) == 0 {
		t.Fatalf("expected rewritten body")
	}
	if resp.Headers != nil {
		t.Fatalf("headers must not be replaced when authorizing upstream")
	}
	var root map[string]any
	if err := json.Unmarshal(resp.Body, &root); err != nil {
		t.Fatalf("invalid rewritten JSON: %v", err)
	}

	pass := call(t, "request.intercept_after", req)
	var passResp adapters.CPARequestInterceptResponse
	if err := json.Unmarshal(pass.Result, &passResp); err != nil {
		t.Fatal(err)
	}
	if string(passResp.Body) != string(req.Body) {
		t.Fatalf("after-auth interceptor must preserve body")
	}

	statsResp := managementCall(t, managementRequest{Method: "GET", Path: "/v0/management/tokensaver/stats"})
	var snapshot struct {
		Count   uint64 `json:"count"`
		Applied uint64 `json:"applied"`
	}
	if err := json.Unmarshal(statsResp.Body, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Count != 1 || snapshot.Applied != 1 {
		t.Fatalf("unexpected stats snapshot: %+v", snapshot)
	}
}

func coreDefaultForTest() core.Config {
	cfg := core.DefaultConfig()
	cfg.ThresholdTokens = 0
	cfg.MinSavingsTokens = 0
	cfg.PreservePrefixItems = 0
	return cfg
}

func TestManagementRegistrationAndUI(t *testing.T) {
	env := call(t, "management.register", map[string]any{})
	var reg managementRegistrationResponse
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatal(err)
	}
	if len(reg.Resources) != 1 || reg.Resources[0].Path != "/ui" || reg.Resources[0].Menu != "TokenSaver" {
		t.Fatalf("bad resources: %+v", reg.Resources)
	}
	if len(reg.Routes) < 4 {
		t.Fatalf("management routes missing: %+v", reg.Routes)
	}

	resp := managementCall(t, managementRequest{Method: "GET", Path: "/v0/resource/plugins/tokensaver-context-compression/ui"})
	if resp.StatusCode != 0 && resp.StatusCode != 200 {
		t.Fatalf("UI status=%d", resp.StatusCode)
	}
	if !bytes.Contains(resp.Body, []byte("TokenSaver 上下文压缩")) {
		t.Fatalf("UI body missing title")
	}
	for _, marker := range [][]byte{[]byte("cli-proxy-auth"), []byte("X-Management-Key"), []byte("X-TokenSaver-Persisted")} {
		if !bytes.Contains(resp.Body, marker) {
			t.Fatalf("UI body missing %q", marker)
		}
	}
}

func TestManagementConfigPreviewAndStats(t *testing.T) {
	resetPluginState()
	cfg := coreDefaultForTest()
	body := map[string]any{"model": "test", "messages": []any{
		map[string]any{"role": "assistant", "content": "old  \n\n\n text"},
		map[string]any{"role": "user", "content": "now"},
	}}
	previewRaw, _ := json.Marshal(map[string]any{"config": cfg, "protocol": "chat", "body": body})
	resp := managementCall(t, managementRequest{
		Method: "POST", Path: "/v0/management/tokensaver/preview", Body: previewRaw,
	})
	var preview struct {
		Stats core.Stats `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Stats.OriginalTokens == 0 {
		t.Fatalf("preview stats empty: %+v", preview.Stats)
	}

	filtered := cfg
	filtered.ModelAllow = []string{"gpt-*"}
	filteredRaw, _ := json.Marshal(map[string]any{"config": filtered, "protocol": "chat", "body": map[string]any{
		"model": "claude-test",
		"messages": []any{
			map[string]any{"role": "assistant", "content": "old  \n\n\n text"},
			map[string]any{"role": "user", "content": "now"},
		},
	}})
	resp = managementCall(t, managementRequest{
		Method: "POST", Path: "/v0/management/tokensaver/preview", Body: filteredRaw,
	})
	var filteredPreview struct {
		Applied bool       `json:"applied"`
		Stats   core.Stats `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body, &filteredPreview); err != nil {
		t.Fatal(err)
	}
	if filteredPreview.Applied || filteredPreview.Stats.SkippedReason != "model-not-allowed" {
		t.Fatalf("preview ignored model filter: %+v", filteredPreview.Stats)
	}

	resp = managementCall(t, managementRequest{
		Method: "GET", Path: "/v0/management/tokensaver/stats",
	})
	var stats map[string]any
	if err := json.Unmarshal(resp.Body, &stats); err != nil {
		t.Fatal(err)
	}
	if stats["count"] == nil {
		t.Fatalf("stats snapshot empty: %+v", stats)
	}
}

func TestManagementConfigPersistsToWritablePath(t *testing.T) {
	resetPluginState()
	path := filepath.Join(t.TempDir(), "core.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stateMu.Lock()
	configPath = path
	stateMu.Unlock()

	cfg := coreDefaultForTest()
	cfg.Logging.Enabled = false
	raw, _ := json.Marshal(cfg)
	resp := managementCall(t, managementRequest{
		Method: "PUT", Path: "/v0/management/tokensaver/config/", Body: raw,
	})
	if got := resp.Headers["X-TokenSaver-Persisted"]; len(got) != 1 || got[0] != "true" {
		t.Fatalf("unexpected persistence header: %+v", resp.Headers)
	}
	loaded, err := core.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mode != cfg.Mode || loaded.Logging.Enabled {
		t.Fatalf("unexpected persisted config: %+v", loaded)
	}
}

func TestManagementConfigRequiresWritablePath(t *testing.T) {
	resetPluginState()
	before := currentConfig()
	raw, _ := json.Marshal(coreDefaultForTest())
	resp := managementCall(t, managementRequest{
		Method: "PUT", Path: "/v0/management/tokensaver/config", Body: raw,
	})
	if resp.StatusCode != 409 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if got := currentConfig(); !reflect.DeepEqual(got, before) {
		t.Fatalf("config changed despite persistence failure: before=%+v after=%+v", before, got)
	}
}

func TestManagementRejectsInvalidConfig(t *testing.T) {
	resetPluginState()
	resp := managementCall(t, managementRequest{
		Method: "PUT", Path: "/v0/management/tokensaver/config", Body: []byte(`{"mode":"unknown"}`),
	})
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestReconfigureWithoutPathClearsStalePersistencePath(t *testing.T) {
	resetPluginState()
	stateMu.Lock()
	configPath = "/tmp/old-tokensaver-core.json"
	stateMu.Unlock()

	cfg := coreDefaultForTest()
	raw, _ := json.Marshal(cfg)
	call(t, "plugin.register", map[string]any{"schema_version": 10, "config_json": raw})

	stateMu.RLock()
	path := configPath
	stateMu.RUnlock()
	if path != "" {
		t.Fatalf("stale config path retained: %q", path)
	}
}
