package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestFunctionalInteractionsAgentClassification(t *testing.T) {
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	for _, tt := range []struct {
		name    string
		body    string
		handled bool
	}{
		{name: "ASCII-tab", body: `{"agent":"\t","model":"client"}`, handled: true},
		{name: "missing", body: `{"model":"client"}`, handled: true},
		{name: "empty", body: `{"agent":"","model":"client"}`, handled: true},
		{name: "null", body: `{"agent":null,"model":"client"}`, handled: true},
		{name: "ASCII-space", body: `{"agent":" ","model":"client"}`, handled: true},
		{name: "ASCII-newline", body: `{"agent":"\n","model":"client"}`, handled: true},
		{name: "NBSP", body: `{"agent":" ","model":"client"}`, handled: true},
		{name: "Em-Space", body: `{"agent":" ","model":"client"}`, handled: true},
		{name: "Ideographic-Space", body: `{"agent":"　","model":"client"}`, handled: true},
		{name: "mixed-whitespace", body: `{"agent":" \t　","model":"client"}`, handled: true},
		{name: "Agent", body: `{"Agent":"client","model":"client"}`, handled: true},
		{name: "AGENT", body: `{"AGENT":"client","model":"client"}`, handled: true},
		{name: "escaped-uppercase", body: `{"\u0041gent":"client","model":"client"}`, handled: true},
		{name: "escaped-lowercase", body: `{"\u0061gent":"client"}`, handled: false},
		{name: "normal-native", body: `{"agent":"client"}`, handled: false},
		{name: "trimmed-native", body: `{"agent":" \tclient　"}`, handled: false},
		{name: "empty-first", body: `{"agent":"","agent":"client","model":"client"}`, handled: true},
		{name: "null-first", body: `{"agent":null,"agent":"client","model":"client"}`, handled: true},
		{name: "whitespace-first", body: `{"agent":" \t　","agent":"client","model":"client"}`, handled: true},
		{name: "native-then-agent-empty", body: `{"agent":"client","agent":""}`, handled: false},
		{name: "native-then-agent-null", body: `{"agent":"client","agent":null}`, handled: false},
		{name: "native-then-agent-false", body: `{"agent":"client","agent":false}`, handled: false},
		{name: "native-then-agent-number", body: `{"agent":"client","agent":17}`, handled: false},
		{name: "native-then-agent-object", body: `{"agent":"client","agent":{"text":"opaque"}}`, handled: false},
		{name: "native-then-agent-array", body: `{"agent":"client","agent":["opaque"]}`, handled: false},
		{name: "native-then-Agent-empty", body: `{"agent":"client","Agent":""}`, handled: false},
		{name: "native-then-Agent-null", body: `{"agent":"client","Agent":null}`, handled: false},
		{name: "native-then-Agent-false", body: `{"agent":"client","Agent":false}`, handled: false},
		{name: "native-then-Agent-number", body: `{"agent":"client","Agent":17}`, handled: false},
		{name: "native-then-Agent-object", body: `{"agent":"client","Agent":{"text":"opaque"}}`, handled: false},
		{name: "native-then-Agent-array", body: `{"agent":"client","Agent":["opaque"]}`, handled: false},
		{name: "native-then-AGENT-empty", body: `{"agent":"client","AGENT":""}`, handled: false},
		{name: "native-then-AGENT-null", body: `{"agent":"client","AGENT":null}`, handled: false},
		{name: "native-then-AGENT-false", body: `{"agent":"client","AGENT":false}`, handled: false},
		{name: "native-then-AGENT-number", body: `{"agent":"client","AGENT":17}`, handled: false},
		{name: "native-then-AGENT-object", body: `{"agent":"client","AGENT":{"text":"opaque"}}`, handled: false},
		{name: "native-then-AGENT-array", body: `{"agent":"client","AGENT":["opaque"]}`, handled: false},
		{name: "nonstring-true", body: `{"agent":true}`, handled: false},
		{name: "nonstring-false", body: `{"agent":false}`, handled: false},
		{name: "nonstring-zero", body: `{"agent":0}`, handled: false},
		{name: "nonstring-number", body: `{"agent":17}`, handled: false},
		{name: "nonstring-object", body: `{"agent":{"text":"opaque"}}`, handled: false},
		{name: "nonstring-array", body: `{"agent":["opaque"]}`, handled: false},
		{name: "invalid-incomplete", body: `{"agent":"client"`, handled: true},
		{name: "invalid-trailing", body: `{"agent":"client"} null`, handled: true},
		{name: "nonstring-true-with-model", body: `{"agent":true,"model":"client"}`, handled: false},
		{name: "nonstring-false-with-model", body: `{"agent":false,"model":"client"}`, handled: false},
		{name: "nonstring-zero-with-model", body: `{"agent":0,"model":"client"}`, handled: false},
		{name: "nonstring-number-with-model", body: `{"agent":17,"model":"client"}`, handled: false},
		{name: "nonstring-object-with-model", body: `{"agent":{"text":"opaque"},"model":"client"}`, handled: false},
		{name: "nonstring-array-with-model", body: `{"agent":["opaque"],"model":"client"}`, handled: false},
		{name: "native-then-agent-true", body: `{"agent":"client","agent":true}`, handled: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(pluginapi.ModelRouteRequest{
				SourceFormat: "interactions", RequestedModel: "client", Body: []byte(tt.body),
			})
			if err != nil {
				t.Fatal(err)
			}
			original := bytes.Clone(raw)
			responseRaw, err := handleModelRoute(raw)
			if err != nil {
				t.Fatal(err)
			}
			var response pluginapi.ModelRouteResponse
			if err := json.Unmarshal(responseRaw, &response); err != nil {
				t.Fatal(err)
			}
			want := pluginapi.ModelRouteResponse{}
			if tt.handled {
				want = pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetSelf, Reason: "model mapped by model-mapper"}
			}
			if response != want {
				t.Fatalf("body=%s route=%+v, want=%+v", tt.body, response, want)
			}
			if !bytes.Equal(raw, original) {
				t.Fatalf("route changed request=%s, want=%s", raw, original)
			}
		})
	}
}

func TestFunctionalInteractionsAgentRoute(t *testing.T) {
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
	for _, tt := range []struct {
		name, format, body string
		config             Config
		metadata           map[string]any
		handled            bool
	}{
		{name: "matched", format: "interactions", body: `{"model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, handled: true},
		{name: "unmatched", format: "interactions", body: `{"model":"client"}`, config: Config{GlobalRules: "other=>upstream"}, handled: false},
		{name: "identity", format: "interactions", body: `{"model":"client"}`, config: Config{GlobalRules: "client=>client"}, handled: false},
		{name: "whitespace-matched", format: "interactions", body: `{"agent":" \t　","model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, handled: true},
		{name: "native-matched", format: "interactions", body: `{"agent":"client"}`, config: Config{GlobalRules: "client=>upstream"}, handled: false},
		{name: "global-only-off", format: "interactions", body: `{"model":"client"}`, config: Config{GlobalRules: "client=>upstream", ClaudeMessagesRules: "client=>claude", CodexResponsesRules: "client=>codex", OpenAICompletionsRules: "client=>openai", RulesStackMode: "off"}, handled: true},
		{name: "specific-only-off", format: "interactions", body: `{"model":"client"}`, config: Config{GlobalRules: "other=>upstream", ClaudeMessagesRules: "client=>claude", CodexResponsesRules: "client=>codex", OpenAICompletionsRules: "client=>openai", RulesStackMode: "off"}, handled: false},
		{name: "global-only-specific_first", format: "interactions", body: `{"model":"client"}`, config: Config{GlobalRules: "client=>upstream", ClaudeMessagesRules: "client=>claude", CodexResponsesRules: "client=>codex", OpenAICompletionsRules: "client=>openai", RulesStackMode: "specific_first"}, handled: true},
		{name: "specific-only-specific_first", format: "interactions", body: `{"model":"client"}`, config: Config{GlobalRules: "other=>upstream", ClaudeMessagesRules: "client=>claude", CodexResponsesRules: "client=>codex", OpenAICompletionsRules: "client=>openai", RulesStackMode: "specific_first"}, handled: false},
		{name: "global-only-global_first", format: "interactions", body: `{"model":"client"}`, config: Config{GlobalRules: "client=>upstream", ClaudeMessagesRules: "client=>claude", CodexResponsesRules: "client=>codex", OpenAICompletionsRules: "client=>openai", RulesStackMode: "global_first"}, handled: true},
		{name: "specific-only-global_first", format: "interactions", body: `{"model":"client"}`, config: Config{GlobalRules: "other=>upstream", ClaudeMessagesRules: "client=>claude", CodexResponsesRules: "client=>codex", OpenAICompletionsRules: "client=>openai", RulesStackMode: "global_first"}, handled: false},
		{name: "count-tokens-interactions", format: "interactions", body: `{"agent":" ","model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, metadata: map[string]any{"request_path": "/v1/messages/count_tokens"}},
		{name: "count-tokens-openai", format: "openai", body: `{"agent":" ","model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, metadata: map[string]any{"request_path": "/v1/messages/count_tokens"}},
		{name: "count-tokens-openai-response", format: "openai-response", body: `{"agent":" ","model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, metadata: map[string]any{"request_path": "/v1/messages/count_tokens"}},
		{name: "count-tokens-claude", format: "claude", body: `{"agent":" ","model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, metadata: map[string]any{"request_path": "/v1/messages/count_tokens"}},
		{name: "count-tokens-gemini", format: "gemini", body: `{"agent":" ","model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, metadata: map[string]any{"request_path": "/v1/messages/count_tokens"}},
		{name: "agent-on-openai", format: "openai", body: `{"agent":"client","model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, handled: true},
		{name: "agent-on-openai-response", format: "openai-response", body: `{"agent":"client","model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, handled: true},
		{name: "agent-on-claude", format: "claude", body: `{"agent":"client","model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, handled: true},
		{name: "agent-on-gemini", format: "gemini", body: `{"agent":"client","model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, handled: true},
		{name: "agent-on-unknown", format: "unknown", body: `{"agent":"client","model":"client"}`, config: Config{GlobalRules: "client=>upstream"}, handled: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setLoadedConfigForTest(tt.config)
			raw, err := json.Marshal(pluginapi.ModelRouteRequest{
				SourceFormat: tt.format, RequestedModel: "client", Body: []byte(tt.body), Metadata: tt.metadata,
			})
			if err != nil {
				t.Fatal(err)
			}
			original := bytes.Clone(raw)
			responseRaw, err := handleModelRoute(raw)
			if err != nil {
				t.Fatal(err)
			}
			var response pluginapi.ModelRouteResponse
			if err := json.Unmarshal(responseRaw, &response); err != nil {
				t.Fatal(err)
			}
			want := pluginapi.ModelRouteResponse{}
			if tt.handled {
				want = pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetSelf, Reason: "model mapped by model-mapper"}
			}
			if response != want || !bytes.Equal(raw, original) {
				t.Fatalf("route=%+v want=%+v request=%s wantRequest=%s", response, want, raw, original)
			}
		})
	}
}
