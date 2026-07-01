//go:build cgo

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

typedef int (*cliproxy_plugin_call_fn)(const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

int cliproxyPluginCall(const char*, const uint8_t*, size_t, cliproxy_buffer*);
void cliproxyPluginFree(void*, size_t);
void cliproxyPluginShutdown(void);

static void cliproxy_set_plugin_api(cliproxy_plugin_api* plugin, uint32_t abi_version) {
	plugin->abi_version = abi_version;
	plugin->call = cliproxyPluginCall;
	plugin->free_buffer = cliproxyPluginFree;
	plugin->shutdown = cliproxyPluginShutdown;
}

static int cliproxy_host_call(cliproxy_host_api* host, const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	return host->call(host->host_ctx, method, request, request_len, response);
}

static void cliproxy_host_free(cliproxy_host_api* host, void* ptr, size_t len) {
	host->free_buffer(ptr, len);
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if host == nil || plugin == nil {
		return 1
	}
	if host.abi_version != C.uint32_t(pluginabi.ABIVersion) {
		return 1
	}
	if host.call == nil || host.free_buffer == nil {
		return 1
	}
	setHostCallback(func(method string, request []byte) ([]byte, error) {
		methodC := C.CString(method)
		defer C.free(unsafe.Pointer(methodC))
		requestPtr := C.CBytes(request)
		defer C.free(requestPtr)
		var response C.cliproxy_buffer
		rc := C.cliproxy_host_call(host, methodC, (*C.uint8_t)(requestPtr), C.size_t(len(request)), &response)
		if response.ptr != nil {
			defer C.cliproxy_host_free(host, response.ptr, response.len)
		}
		if rc != 0 {
			return nil, fmt.Errorf("host callback failed with rc=%d", int(rc))
		}
		if response.ptr == nil || response.len == 0 {
			return nil, nil
		}
		return C.GoBytes(response.ptr, C.int(response.len)), nil
	})
	C.cliproxy_set_plugin_api(plugin, C.uint32_t(pluginabi.ABIVersion))
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if method == nil || response == nil {
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	payload, err := handleMethod(C.GoString(method), requestBytes)
	if err != nil {
		payload = errorEnvelope("plugin_error", err.Error())
	}
	response.ptr = nil
	response.len = 0
	if len(payload) == 0 {
		return 0
	}
	ptr := C.malloc(C.size_t(len(payload)))
	if ptr == nil {
		return 1
	}
	copy((*[1 << 30]byte)(ptr)[:len(payload):len(payload)], payload)
	response.ptr = ptr
	response.len = C.size_t(len(payload))
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, len C.size_t) {
	_ = len
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	setHostCallback(nil)
}
