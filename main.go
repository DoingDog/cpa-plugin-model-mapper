package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func main() {}

var pluginVersion = "0.0.0-dev"

const pluginRPCSchemaVersion uint32 = 1

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	ModelRouter           bool     `json:"model_router"`
	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope"`
	ExecutorInputFormats  []string `json:"executor_input_formats"`
	ExecutorOutputFormats []string `json:"executor_output_formats"`
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginRPCSchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "model-mapper",
			Version:          pluginVersion,
			Author:           "DoingDog",
			GitHubRepository: "https://github.com/DoingDog/cpa-plugin-model-mapper",
			Logo:             "https://raw.githubusercontent.com/DoingDog/cpa-plugin-model-mapper/refs/heads/main/logo.png",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "global_rules", Type: pluginapi.ConfigFieldTypeString, Description: "Fallback rules used when an endpoint-specific ruleset is empty."},
				{Name: "claude_messages_rules", Type: pluginapi.ConfigFieldTypeString, Description: "Rules for Claude Messages-compatible requests."},
				{Name: "codex_responses_rules", Type: pluginapi.ConfigFieldTypeString, Description: "Rules for OpenAI Responses/Codex-compatible requests."},
				{Name: "openai_completions_rules", Type: pluginapi.ConfigFieldTypeString, Description: "Rules for OpenAI Completions and Chat Completions requests."},
				{
					Name:        "rules_stack_mode",
					Type:        pluginapi.ConfigFieldTypeEnum,
					Description: "Controls global and endpoint-specific rule order; default off preserves endpoint-specific override behavior.",
					EnumValues:  []string{"off", "specific_first", "global_first"},
				},
			},
		},
		Capabilities: registrationCapabilities{
			ModelRouter:           true,
			Executor:              true,
			ExecutorModelScope:    string(pluginapi.ExecutorModelScopeStatic),
			ExecutorInputFormats:  []string{"openai", "openai-response", "claude", "gemini", "interactions"},
			ExecutorOutputFormats: []string{"openai", "openai-response", "claude", "gemini", "interactions"},
		},
	}
}

var (
	hostAPIMu      sync.RWMutex
	hostCallbackFn hostCallback
)

type hostCallback func(method string, request []byte) ([]byte, error)

func handlePluginRegister(raw []byte) ([]byte, error) {
	if err := applyLifecycleConfig(raw); err != nil {
		return nil, err
	}
	return json.Marshal(pluginRegistration())
}

func handlePluginReconfigure(raw []byte) ([]byte, error) {
	if err := applyLifecycleConfig(raw); err != nil {
		return nil, err
	}
	return json.Marshal(pluginRegistration())
}

func handleExecutorIdentifier() ([]byte, error) {
	return json.Marshal(struct {
		Identifier string `json:"identifier"`
	}{Identifier: "model-mapper"})
}

type hostCaller func(method string, payload any) (json.RawMessage, error)

type structuredPluginError struct {
	detail pluginabi.Error
	cause  error
}

func (e *structuredPluginError) Error() string { return e.detail.Message }

func (e *structuredPluginError) Unwrap() error { return e.cause }

func newStructuredPluginError(detail pluginabi.Error, cause error) error {
	return &structuredPluginError{detail: detail, cause: cause}
}

func structuredError(code, message string, retryable bool, httpStatus int, cause error) error {
	return newStructuredPluginError(pluginabi.Error{
		Code:       code,
		Message:    message,
		Retryable:  retryable,
		HTTPStatus: httpStatus,
	}, cause)
}

func wrapEnvelope(payload []byte, err error) ([]byte, error) {
	if err != nil {
		var structured *structuredPluginError
		if errors.As(err, &structured) {
			return marshalErrorEnvelope(structured.detail), nil
		}
		return errorEnvelope("plugin_error", err.Error()), nil
	}
	if len(payload) == 0 {
		payload = []byte("null")
	}
	return json.Marshal(pluginabi.Envelope{OK: true, Result: json.RawMessage(payload)})
}

func marshalErrorEnvelope(detail pluginabi.Error) []byte {
	raw, err := json.Marshal(pluginabi.Envelope{OK: false, Error: &detail})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"plugin_error","message":"failed to encode error envelope"}}`)
	}
	return raw
}

func errorEnvelope(code, message string) []byte {
	return marshalErrorEnvelope(pluginabi.Error{Code: code, Message: message})
}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister:
		return wrapEnvelope(handlePluginRegister(request))
	case pluginabi.MethodPluginReconfigure:
		return wrapEnvelope(handlePluginReconfigure(request))
	case pluginabi.MethodModelRoute:
		return wrapEnvelope(handleModelRoute(request))
	case pluginabi.MethodExecutorIdentifier:
		return wrapEnvelope(handleExecutorIdentifier())
	case pluginabi.MethodExecutorExecute:
		return wrapEnvelope(handleExecutorExecute(request, callHost))
	case pluginabi.MethodExecutorExecuteStream:
		return wrapEnvelope(handleExecutorExecuteStream(request, callHost))
	case pluginabi.MethodExecutorCountTokens:
		return errorEnvelope("unsupported", "executor.count_tokens is not supported by model-mapper"), nil
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func callHost(method string, payload any) (json.RawMessage, error) {
	hostAPIMu.RLock()
	cb := hostCallbackFn
	hostAPIMu.RUnlock()
	if cb == nil {
		return nil, fmt.Errorf("host API not initialized")
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	responseBytes, err := cb(method, rawPayload)
	if err != nil {
		return nil, err
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(responseBytes, &env); err != nil {
		return nil, fmt.Errorf("decode host envelope: %w", err)
	}
	if !env.OK {
		if env.Error == nil {
			return nil, fmt.Errorf("host callback %s failed", method)
		}
		detail := *env.Error
		return nil, newStructuredPluginError(detail, nil)
	}
	return env.Result, nil
}

func setHostCallbackForTest(cb hostCallback) {
	hostAPIMu.Lock()
	hostCallbackFn = cb
	hostAPIMu.Unlock()
}

func setHostCallback(cb hostCallback) {
	setHostCallbackForTest(cb)
}
