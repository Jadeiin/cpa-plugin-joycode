package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
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

extern int JoycodePluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void JoycodePluginFree(void*, size_t);
extern void JoycodePluginShutdown(void);

static const cliproxy_host_api* joycode_stored_host;

static void joycode_store_host_api(const cliproxy_host_api* host) {
	joycode_stored_host = host;
}

static int joycode_call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (joycode_stored_host == NULL || joycode_stored_host->call == NULL) {
		return 1;
	}
	return joycode_stored_host->call(joycode_stored_host->host_ctx, method, request, request_len, response);
}

static void joycode_free_host_buffer(void* ptr, size_t len) {
	if (joycode_stored_host != NULL && joycode_stored_host->free_buffer != NULL && ptr != NULL) {
		joycode_stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

var joycodeABIState = struct {
	sync.RWMutex
	host         *C.cliproxy_host_api
	shuttingDown bool
	inFlight     sync.WaitGroup
}{}

const maxCGoBytesLen = C.size_t(1<<31 - 1)

type abiEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *abiError       `json:"error,omitempty"`
}

type abiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	joycodeABIState.Lock()
	joycodeABIState.host = host
	joycodeABIState.shuttingDown = false
	joycodeABIState.Unlock()

	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.JoycodePluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.JoycodePluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.JoycodePluginShutdown)
	return 0
}

//export JoycodePluginCall
func JoycodePluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeABIResponse(response, abiErrorEnvelope("invalid_method", "method is required"))
		return 0
	}

	joycodeABIState.RLock()
	if joycodeABIState.shuttingDown {
		joycodeABIState.RUnlock()
		writeABIResponse(response, abiErrorEnvelope("shutting_down", "plugin is shutting down"))
		return 0
	}
	joycodeABIState.inFlight.Add(1)
	joycodeABIState.RUnlock()
	defer joycodeABIState.inFlight.Done()

	var reqBody []byte
	if request != nil && requestLen > 0 {
		if requestLen > maxCGoBytesLen {
			writeABIResponse(response, abiErrorEnvelope("request_too_large", "request payload is too large"))
			return 0
		}
		reqBody = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}

	methodStr := C.GoString(method)
	raw, errHandle := handleJoycodeABIMethod(context.Background(), methodStr, reqBody)
	if errHandle != nil {
		writeABIResponse(response, abiErrorEnvelope("plugin_error", errHandle.Error()))
		return 0
	}
	writeABIResponse(response, raw)
	return 0
}

//export JoycodePluginFree
func JoycodePluginFree(ptr unsafe.Pointer, len C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export JoycodePluginShutdown
func JoycodePluginShutdown() {
	joycodeABIState.Lock()
	joycodeABIState.shuttingDown = true
	joycodeABIState.host = nil
	joycodeABIState.Unlock()
	joycodeABIState.inFlight.Wait()
}

func handleJoycodeABIMethod(ctx context.Context, method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return handleRegister(request)
	}

	switch method {
	case pluginabi.MethodExecutorIdentifier:
		return handleExecutorIdentifier()
	case pluginabi.MethodExecutorExecute:
		return handleExecutorExecute(request)
	case pluginabi.MethodExecutorExecuteStream:
		return handleExecutorExecuteStream(request)
	case pluginabi.MethodExecutorCountTokens:
		return handleCountTokens()
	case pluginabi.MethodAuthIdentifier:
		return handleAuthIdentifier()
	case pluginabi.MethodAuthParse:
		return handleAuthParse(request)
	case pluginabi.MethodAuthLoginStart:
		return handleAuthLoginStart(request)
	case pluginabi.MethodAuthLoginPoll:
		return handleAuthLoginPoll(request)
	case pluginabi.MethodAuthRefresh:
		return handleAuthRefresh(request)
	case pluginabi.MethodModelStatic:
		return handleModelStatic(request)
	case pluginabi.MethodModelForAuth:
		return handleModelForAuth(request)
	default:
		return abiErrorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func handleRegister(request []byte) ([]byte, error) {
	plugin := buildPlugin()
	return abiOKEnvelope(abiRegistration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata:      plugin.Metadata,
		Capabilities: abiCapabilities{
			Executor:             plugin.Capabilities.Executor != nil,
			ExecutorModelScope:   string(plugin.Capabilities.ExecutorModelScope),
			ExecutorInputFormats: plugin.Capabilities.ExecutorInputFormats,
			ExecutorOutputFormats: plugin.Capabilities.ExecutorOutputFormats,
			AuthProvider:         plugin.Capabilities.AuthProvider != nil,
			ModelProvider:         plugin.Capabilities.ModelProvider != nil,
		},
	})
}

type abiRegistration struct {
	SchemaVersion    uint32             `json:"schema_version"`
	Metadata         pluginapi.Metadata `json:"metadata"`
	Capabilities     abiCapabilities    `json:"capabilities"`
}

type abiCapabilities struct {
	Executor             bool     `json:"executor"`
	ExecutorModelScope   string   `json:"executor_model_scope,omitempty"`
	ExecutorInputFormats []string `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string `json:"executor_output_formats,omitempty"`
	AuthProvider         bool     `json:"auth_provider"`
	ModelProvider        bool     `json:"model_provider"`
}

// --- Host callback helpers ---

func callHost(method string, payload []byte) ([]byte, error) {
	joycodeABIState.RLock()
	defer joycodeABIState.RUnlock()
	if joycodeABIState.host == nil {
		return nil, fmt.Errorf("host callback is unavailable")
	}

	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var reqPtr *C.uint8_t
	var reqLen C.size_t
	if len(payload) > 0 {
		reqPtr = (*C.uint8_t)(C.CBytes(payload))
		defer C.free(unsafe.Pointer(reqPtr))
		reqLen = C.size_t(len(payload))
	}

	var resp C.cliproxy_buffer
	rc := C.joycode_call_host_api(cMethod, reqPtr, reqLen, &resp)
	if rc != 0 {
		return nil, fmt.Errorf("host call %s returned %d", method, int(rc))
	}
	if resp.ptr == nil || resp.len == 0 {
		return nil, nil
	}
	result := C.GoBytes(resp.ptr, C.int(resp.len))
	C.joycode_free_host_buffer(resp.ptr, resp.len)
	return result, nil
}

func callHostJSON(method string, req any) (json.RawMessage, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal host call request: %w", err)
	}
	raw, err := callHost(method, payload)
	if err != nil {
		return nil, err
	}
	var env abiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("unmarshal host response: %w", err)
	}
	if !env.OK {
		msg := "unknown error"
		if env.Error != nil {
			msg = env.Error.Message
		}
		return nil, fmt.Errorf("host call %s failed: %s", method, msg)
	}
	return env.Result, nil
}

func hostLog(level, msg string) {
	req := map[string]any{"level": level, "message": msg, "fields": map[string]any{}}
	payload, _ := json.Marshal(req)
	callHost(pluginabi.MethodHostLog, payload)
}

// --- ABI envelope helpers ---

func abiOKEnvelope(v any) ([]byte, error) {
	raw, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(abiEnvelope{OK: true, Result: json.RawMessage(raw)})
}

func abiErrorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(abiEnvelope{OK: false, Error: &abiError{Code: code, Message: message}})
	return raw
}

func writeABIResponse(response *C.cliproxy_buffer, raw []byte) {
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
