//go:build ignore

// cpa_abi_probe loads the TokenSaver CPA shared library via the host's exact
// C ABI and checks registration + request.intercept_before. Run with:
//
//	go run scripts/cpa_abi_probe.go dist/linux-arm64/tokensaver-cpa.so
package main

/*
#include <stdint.h>
#include <stdlib.h>
#include <dlfcn.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef struct { uint32_t abi_version; void* host_ctx; void* call; void* free_buffer; } cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown; } cliproxy_plugin_api;
typedef int (*cliproxy_plugin_init_fn)(cliproxy_host_api*, cliproxy_plugin_api*);

static int ts_call_init(cliproxy_plugin_init_fn fn, cliproxy_host_api* host, cliproxy_plugin_api* api) {
	return fn(host, api);
}
static int ts_call_plugin(cliproxy_plugin_call_fn fn, char* method, uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	return fn(method, request, request_len, response);
}
static void ts_call_free(cliproxy_plugin_free_fn fn, void* ptr, size_t len) {
	fn(ptr, len);
}
static void ts_call_shutdown(cliproxy_plugin_shutdown_fn fn) {
	fn();
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"os"
	"unsafe"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cpa_abi_probe <shared-library>")
		os.Exit(2)
	}
	path := C.CString(os.Args[1])
	defer C.free(unsafe.Pointer(path))
	handle := C.dlopen(path, C.RTLD_NOW|C.RTLD_LOCAL)
	if handle == nil {
		msg := C.GoString(C.dlerror())
		fmt.Fprintln(os.Stderr, "dlopen:", msg)
		os.Exit(1)
	}
	defer C.dlclose(handle)
	sym := C.CString("cliproxy_plugin_init")
	defer C.free(unsafe.Pointer(sym))
	raw := C.dlsym(handle, sym)
	if raw == nil {
		fmt.Fprintln(os.Stderr, "cliproxy_plugin_init not found")
		os.Exit(1)
	}
	initFn := C.cliproxy_plugin_init_fn(raw)
	var api C.cliproxy_plugin_api
	if rc := C.ts_call_init(initFn, nil, &api); rc != 0 {
		fmt.Fprintln(os.Stderr, "init failed:", int(rc))
		os.Exit(1)
	}
	if api.abi_version != 1 || api.call == nil || api.free_buffer == nil || api.shutdown == nil {
		fmt.Fprintln(os.Stderr, "invalid plugin API struct")
		os.Exit(1)
	}
	fmt.Println("ABI init OK version=", uint32(api.abi_version))

	configJSON, _ := json.Marshal(map[string]any{
		"enabled": true, "mode": "safe", "threshold_tokens": 0, "min_savings_tokens": 0,
		"preserve_prefix_items": 0, "preserve_last_user": true, "protect_system": true,
		"allow_lossy": false, "model_allow": []string{"*"}, "model_deny": []string{},
		"engines":         []map[string]any{{"id": "whitespace", "enabled": true}, {"id": "json-minify", "enabled": true}},
		"tool_truncate":   map[string]any{"max_chars": 8000, "head_pct": 0.6, "tail_pct": 0.3, "min_chars": 9000},
		"diagnostic_trim": map[string]any{"max_lines": 140, "min_chars": 12000, "preserve_error_lines": true},
		"logging":         map[string]any{"enabled": true, "max_entries": 500, "include_model": true, "include_engine": true},
	})
	regPayload, _ := json.Marshal(map[string]any{"schema_version": 10, "config_json": configJSON})
	out := call(api, "plugin.register", regPayload)
	fmt.Println("register:", string(out))

	bodyJSON := []byte(`{"model":"probe","messages":[{"role":"assistant","content":"old  \n\n\n text"},{"role":"user","content":"now"}]}`)
	interceptReq := map[string]any{
		"request_id": "probe-1", "source_format": "openai", "to_format": "openai",
		"model": "probe", "headers": map[string][]string{"Authorization": {"Bearer test"}},
		"body": bodyJSON,
	}
	intercept, _ := json.Marshal(interceptReq)
	out = call(api, "request.intercept_before", intercept)
	fmt.Println("intercept:", string(out))
	C.ts_call_shutdown(api.shutdown)
}

func call(api C.cliproxy_plugin_api, method string, payload []byte) []byte {
	cm := C.CString(method)
	defer C.free(unsafe.Pointer(cm))
	var body *C.uint8_t
	if len(payload) > 0 {
		body = (*C.uint8_t)(C.CBytes(payload))
		defer C.free(unsafe.Pointer(body))
	}
	var response C.cliproxy_buffer
	rc := C.ts_call_plugin(api.call, cm, body, C.size_t(len(payload)), &response)
	defer func() {
		if response.ptr != nil {
			C.ts_call_free(api.free_buffer, response.ptr, response.len)
		}
	}()
	if rc != 0 {
		return []byte(fmt.Sprintf(`{"transport_rc":%d}`, rc))
	}
	if response.ptr == nil || response.len == 0 {
		return nil
	}
	return C.GoBytes(response.ptr, C.int(response.len))
}
