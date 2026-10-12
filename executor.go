package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func canonicalizeHeaders(headers http.Header) {
	for key, values := range headers {
		canonical := http.CanonicalHeaderKey(key)
		if canonical == key {
			continue
		}
		headers[canonical] = append(headers[canonical], values...)
		delete(headers, key)
	}
}

func removeChangedBodyHeaders(headers http.Header, response bool) {
	for _, name := range []string{
		"Content-Length",
		"Content-Digest",
		"Repr-Digest",
		"Digest",
		"Content-MD5",
	} {
		headers.Del(name)
	}
	if response {
		headers.Del("ETag")
		headers.Del("Content-Range")
	}
}

type executorRPCRequest struct {
	Model           string
	Format          string
	Alt             string
	Headers         http.Header
	Query           url.Values
	OriginalRequest []byte
	SourceFormat    string
	Metadata        map[string]any
	HostCallbackID  string `json:"host_callback_id,omitempty"`
	StreamID        string `json:"stream_id,omitempty"`
}

type hostModelExecutePayload struct {
	pluginapi.HostModelExecutionRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func handleExecutorExecute(raw []byte, call hostCaller) ([]byte, error) {
	var req executorRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	canonicalizeHeaders(req.Headers)
	scope := callerScopeFromMetadata(req.Metadata)
	cfg := loadedConfig()
	decision, err := routeModel(cfg, req.SourceFormat, req.Model, scope, callerAPIKeyForSelectedRules(cfg, req.SourceFormat, req.Headers, req.Query, scope))
	if err != nil {
		return nil, err
	}
	if !decision.Handled {
		return nil, fmt.Errorf("unhandled model route for %q", req.Model)
	}
	body, changed, err := rewriteRequestModel(req.OriginalRequest, decision.UpstreamModel)
	if err != nil {
		return nil, err
	}
	if changed {
		removeChangedBodyHeaders(req.Headers, false)
	}
	hostRaw, err := call(pluginabi.MethodHostModelExecute, hostModelExecutePayload{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: req.SourceFormat,
			ExitProtocol:  req.Format,
			Model:         decision.UpstreamModel,
			Stream:        false,
			Body:          body,
			Headers:       req.Headers,
			Query:         req.Query,
			Alt:           req.Alt,
		},
		HostCallbackID: req.HostCallbackID,
	})
	if err != nil {
		return nil, err
	}
	var hostResp pluginapi.HostModelExecutionResponse
	if err := json.Unmarshal(hostRaw, &hostResp); err != nil {
		return nil, err
	}
	canonicalizeHeaders(hostResp.Headers)
	if hostResp.StatusCode >= 400 {
		message := fmt.Sprintf("host.model.execute status %d: %s", hostResp.StatusCode, string(hostResp.Body))
		return nil, structuredError("plugin_error", message, false, hostResp.StatusCode, nil)
	}
	payload, changed, err := restoreResponseModel(hostResp.Body, decision.OriginalModel)
	if err != nil {
		return nil, err
	}
	if changed {
		removeChangedBodyHeaders(hostResp.Headers, true)
	}
	return json.Marshal(pluginapi.ExecutorResponse{Payload: payload, Headers: hostResp.Headers})
}
