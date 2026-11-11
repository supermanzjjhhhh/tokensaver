package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	void* call;
	void* free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"gopkg.in/yaml.v3"

	"tokensaver/pkg/adapters"
	"tokensaver/pkg/core"
)

const (
	abiVersion    = 1
	schemaVersion = 10
)

var (
	stateMu    sync.RWMutex
	cfg        = core.DefaultConfig()
	stats      = core.NewStatsStore(500)
	configPath string
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type configRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
	ConfigPath string `json:"config_path"`
	ConfigJSON []byte `json:"config_json"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      map[string]any         `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	RequestInterceptor bool `json:"request_interceptor"`
	ManagementAPI      bool `json:"management_api"`
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(_ *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	plugin.abi_version = C.uint32_t(abiVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var raw []byte
	if request != nil && requestLen > 0 {
		raw = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	out, err := handleMethod(C.GoString(method), raw)
	if err != nil {
		writeResponse(response, errorEnvelope("plugin_error", err.Error()))
		return 1
	}
	writeResponse(response, out)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = length
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	stateMu.Lock()
	cfg = core.DefaultConfig()
	configPath = ""
	stateMu.Unlock()
}

func handleMethod(method string, raw []byte) ([]byte, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		if err := configure(raw); err != nil {
			return nil, err
		}
		return okEnvelope(registration{
			SchemaVersion: schemaVersion,
			Metadata: map[string]any{
				"Name":             "tokensaver-context-compression",
				"Version":          "0.2.0",
				"Author":           "TokenSaver",
				"GitHubRepository": "local://tokensaver-context-compression",
				"ConfigFields": []map[string]any{
					{
						"Name":        "config_path",
						"Type":        "string",
						"Description": "Absolute path to the shared TokenSaver core config JSON.",
					},
				},
			},
			Capabilities: registrationCapability{RequestInterceptor: true, ManagementAPI: true},
		})
	case "request.intercept_before":
		return intercept(raw)
	case "request.intercept_after":
		return passThrough(raw)
	case "management.register":
		return managementRegistration()
	case "management.handle":
		return managementHandle(raw)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func configure(raw []byte) error {
	if len(raw) == 0 {
		return fmt.Errorf("missing plugin config")
	}
	var req configRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return err
	}
	var next core.Config
	var nextPath string
	if len(req.ConfigJSON) > 0 {
		if err := json.Unmarshal(req.ConfigJSON, &next); err != nil {
			return fmt.Errorf("config_json: %w", err)
		}
	} else if len(req.ConfigYAML) > 0 {
		var yamlCfg struct {
			ConfigPath string `yaml:"config_path"`
		}
		if err := yaml.Unmarshal(req.ConfigYAML, &yamlCfg); err != nil {
			return fmt.Errorf("config_yaml: %w", err)
		}
		if yamlCfg.ConfigPath != "" {
			if !filepath.IsAbs(yamlCfg.ConfigPath) {
				return fmt.Errorf("config_yaml config_path must be absolute")
			}
			loaded, err := core.LoadConfig(yamlCfg.ConfigPath)
			if err != nil {
				return fmt.Errorf("config_yaml config_path: %w", err)
			}
			next = loaded
			nextPath = yamlCfg.ConfigPath
		} else {
			next = core.DefaultConfig()
		}
	} else if req.ConfigPath != "" {
		if !filepath.IsAbs(req.ConfigPath) {
			return fmt.Errorf("config_path must be absolute")
		}
		loaded, err := core.LoadConfig(req.ConfigPath)
		if err != nil {
			return fmt.Errorf("config_path: %w", err)
		}
		next = loaded
		nextPath = req.ConfigPath
	} else {
		// The host still sends JSON schema_version; YAML-only config with no
		// fields is valid and means "safe defaults".
		next = core.DefaultConfig()
		var probe map[string]any
		if len(raw) > 0 && (json.Unmarshal(raw, &probe) != nil || probe["schema_version"] == nil) {
			return fmt.Errorf("plugin.register payload is not JSON")
		}
	}
	next.ApplyDefaults()
	if err := next.Validate(); err != nil {
		return err
	}
	stateMu.Lock()
	cfg = next
	configPath = nextPath
	stateMu.Unlock()
	stats.Configure(next.Logging)
	return nil
}

func intercept(raw []byte) ([]byte, error) {
	var req adapters.CPARequestInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	stateMu.RLock()
	c := cfg.Clone()
	stateMu.RUnlock()
	resp, st := adapters.InterceptForCPA(req, c)
	stats.Record(st)
	return okEnvelope(resp)
}

func passThrough(raw []byte) ([]byte, error) {
	var req adapters.CPARequestInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	return okEnvelope(adapters.CPARequestInterceptResponse{Headers: req.Headers, Body: req.Body})
}

type managementRegistrationResponse struct {
	Routes    []managementRoute `json:"routes,omitempty"`
	Resources []resourceRoute   `json:"resources,omitempty"`
}

type managementRoute struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Menu        string `json:"menu,omitempty"`
	Description string `json:"description,omitempty"`
}

type resourceRoute struct {
	Path        string `json:"path"`
	Menu        string `json:"menu"`
	Description string `json:"description"`
}

type managementRequest struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
	Query   map[string][]string `json:"query"`
	Body    []byte              `json:"body"`
}

type managementResponse struct {
	StatusCode int                 `json:"StatusCode,omitempty"`
	Headers    map[string][]string `json:"Headers,omitempty"`
	Body       []byte              `json:"Body,omitempty"`
}

func managementRegistration() ([]byte, error) {
	return okEnvelope(managementRegistrationResponse{
		Routes: []managementRoute{
			{Method: "GET", Path: "/tokensaver/config"},
			{Method: "PUT", Path: "/tokensaver/config"},
			{Method: "POST", Path: "/tokensaver/config"},
			{Method: "POST", Path: "/tokensaver/preview"},
			{Method: "GET", Path: "/tokensaver/stats"},
		},
		Resources: []resourceRoute{
			{Path: "/ui", Menu: "TokenSaver", Description: "Configure context compression and preview requests."},
		},
	})
}

func managementHandle(raw []byte) ([]byte, error) {
	var req managementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	resp, err := serveManagement(req)
	if err != nil {
		return nil, err
	}
	return okEnvelope(resp)
}

func serveManagement(req managementRequest) (managementResponse, error) {
	switch req.Method + " " + strings.TrimSuffix(req.Path, "/") {
	case "GET /v0/management/tokensaver/config", "GET /tokensaver/config":
		body, _ := json.MarshalIndent(currentConfig(), "", "  ")
		return managementResponse{Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: append(body, '\n')}, nil
	case "PUT /v0/management/tokensaver/config", "POST /v0/management/tokensaver/config", "PUT /tokensaver/config", "POST /tokensaver/config":
		var next core.Config
		if err := json.Unmarshal(req.Body, &next); err != nil {
			return managementResponse{StatusCode: 400, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: managementError("invalid_json", err.Error())}, nil
		}
		next.ApplyDefaults()
		if err := next.Validate(); err != nil {
			return managementResponse{StatusCode: 400, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: managementError("invalid_config", err.Error())}, nil
		}
		// Persist when an absolute config path is known. The plugin mount must
		// be writable for persistence; otherwise it remains an in-memory update.
		stateMu.RLock()
		path := configPath
		stateMu.RUnlock()
		if path == "" {
			return managementResponse{StatusCode: 409, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: managementError("config_not_persistent", "no writable config_path is configured")}, nil
		}
		if err := core.SaveConfig(path, next); err != nil {
			return managementResponse{StatusCode: 500, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: managementError("save_failed", err.Error())}, nil
		}
		stateMu.Lock()
		cfg = next
		stateMu.Unlock()
		stats.Configure(next.Logging)
		body, _ := json.MarshalIndent(next, "", "  ")
		return managementResponse{Headers: map[string][]string{
			"Content-Type":           {"application/json"},
			"Cache-Control":          {"no-store"},
			"X-TokenSaver-Persisted": {"true"},
		}, Body: append(body, '\n')}, nil
	case "POST /v0/management/tokensaver/preview", "POST /tokensaver/preview":
		var env struct {
			Config   *core.Config    `json:"config"`
			Protocol string          `json:"protocol"`
			Model    string          `json:"model"`
			Body     json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal(req.Body, &env); err != nil {
			return managementResponse{StatusCode: 400, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: managementError("invalid_json", err.Error())}, nil
		}
		if env.Protocol == "" {
			var probe map[string]any
			if json.Unmarshal(env.Body, &probe) == nil {
				if _, ok := probe["input"]; ok {
					env.Protocol = "responses"
				}
				if _, ok := probe["system"]; ok {
					env.Protocol = "anthropic"
				}
				if _, ok := probe["messages"]; ok {
					env.Protocol = "chat"
				}
			}
		}
		model := env.Model
		if model == "" {
			var probe struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(env.Body, &probe) == nil {
				model = probe.Model
			}
		}
		c := currentConfig()
		if env.Config != nil {
			c = *env.Config
			c.ApplyDefaults()
			if err := c.Validate(); err != nil {
				return managementResponse{StatusCode: 400, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: managementError("invalid_config", err.Error())}, nil
			}
		}
		res := core.CompressRequest(env.Body, c, core.RequestMeta{Protocol: env.Protocol, Model: model})
		body, _ := json.Marshal(map[string]any{"applied": res.Applied, "stats": res.Stats, "body": json.RawMessage(res.Body)})
		return managementResponse{Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: body}, nil
	case "GET /v0/management/tokensaver/stats", "GET /tokensaver/stats":
		body, _ := json.MarshalIndent(stats.Snapshot(), "", "  ")
		return managementResponse{Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: append(body, '\n')}, nil
	case "GET /v0/resource/plugins/tokensaver-context-compression/ui", "GET /tokensaver-context-compression/ui", "GET /ui":
		return managementResponse{Headers: map[string][]string{
			"Content-Type":           {"text/html; charset=utf-8"},
			"Cache-Control":          {"no-store"},
			"X-Content-Type-Options": {"nosniff"},
			"Referrer-Policy":        {"no-referrer"},
		}, Body: []byte(managementUI)}, nil
	default:
		return managementResponse{StatusCode: 404, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: managementError("not_found", "unknown management route")}, nil
	}
}

func currentConfig() core.Config {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return cfg.Clone()
}

func managementError(code, message string) []byte {
	raw, _ := json.Marshal(map[string]any{"error": map[string]string{"type": code, "message": message}})
	return raw
}

func okEnvelope(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, err := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"plugin_error","message":"encode error"}}`)
	}
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
