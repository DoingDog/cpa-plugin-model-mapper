package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestPluginRegistrationMetadataAndConfigFields(t *testing.T) {
	reg := pluginRegistration()
	if reg.SchemaVersion != pluginabi.SchemaVersion {
		t.Fatalf("schema version=%d, want %d", reg.SchemaVersion, pluginabi.SchemaVersion)
	}
	if reg.Metadata.Name != "model-mapper" {
		t.Fatalf("plugin name=%q", reg.Metadata.Name)
	}
	if !reg.Capabilities.ModelRouter || !reg.Capabilities.Executor {
		t.Fatalf("capabilities=%#v, want model router and executor", reg.Capabilities)
	}
	if reg.Capabilities.ExecutorModelScope != string(pluginapi.ExecutorModelScopeStatic) {
		t.Fatalf("executor scope=%q", reg.Capabilities.ExecutorModelScope)
	}
	wantFields := []string{"enabled", "global_rules", "claude_messages_rules", "codex_responses_rules", "openai_completions_rules"}
	got := make([]string, 0, len(reg.Metadata.ConfigFields))
	for _, field := range reg.Metadata.ConfigFields {
		got = append(got, field.Name)
	}
	if !reflect.DeepEqual(got, wantFields) {
		t.Fatalf("config fields=%v, want %v", got, wantFields)
	}
}

func TestDecodeConfigDefaultAndBadRules(t *testing.T) {
	cfg, err := decodeConfig(nil)
	if err != nil {
		t.Fatalf("decodeConfig nil error = %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("decoded default enabled=false")
	}
	if _, err := decodeConfig(json.RawMessage(`{"enabled":true,"global_rules":"bad rule"}`)); err == nil {
		t.Fatalf("decodeConfig bad rules error = nil")
	}
	cfg, err = decodeConfig(json.RawMessage(`{"enabled":false,"global_rules":"a=>b"}`))
	if err != nil {
		t.Fatalf("decodeConfig valid error = %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("enabled=true, want false from config")
	}
}

func TestDefaultConfigEnabledTrue(t *testing.T) {
	cfg := defaultConfig()
	if !cfg.Enabled {
		t.Fatalf("default enabled = false, want true")
	}
	if cfg.GlobalRules != "" || cfg.ClaudeMessagesRules != "" || cfg.CodexResponsesRules != "" || cfg.OpenAICompletionsRules != "" {
		t.Fatalf("default rule fields must be empty: %#v", cfg)
	}
}

func TestParseRulesAcceptsValidRules(t *testing.T) {
	tests := []string{
		"a=>b",
		`deepseek-*=>claude-$1`,
		`a*bc*=>x$2y$1`,
		`literal\*=>star`,
		`a\;b=>c\=>d`,
		`a\=>b=>c`,
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			rules, err := parseRules(raw)
			if err != nil {
				t.Fatalf("parseRules(%q) error = %v", raw, err)
			}
			if len(rules) == 0 {
				t.Fatalf("parseRules(%q) returned no rules", raw)
			}
		})
	}
}

func TestParseRulesRejectsInvalidRules(t *testing.T) {
	tests := []string{
		"",
		"a =>b",
		`"a"=>b`,
		"a=>",
		"=>b",
		"a-b",
		"a=>b;",
		";a=>b",
		"a=>b;;c=>d",
		"a=>b=>c",
		`a\=>b`,
		`a=>b\`,
		`a=>x$`,
		`a=>x$0`,
		`a=>x$x`,
		`a=>x$1`,
		`a*=>x$2`,
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseRules(raw); err == nil {
				t.Fatalf("parseRules(%q) error = nil, want error", raw)
			}
		})
	}
}

func mustParseRules(t *testing.T, raw string) []rule {
	t.Helper()
	rules, err := parseRules(raw)
	if err != nil {
		t.Fatalf("parseRules(%q) error = %v", raw, err)
	}
	return rules
}

func TestApplyRulesFullChain(t *testing.T) {
	rules := mustParseRules(t, "deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>claude-v4-flash")
	mapped, matched, err := applyRules("deepseek-v4-pro", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "claude-v4-flash" {
		t.Fatalf("mapped=%q matched=%v, want claude-v4-flash true", mapped, matched)
	}
}

func TestApplyRulesWildcardCapture(t *testing.T) {
	rules := mustParseRules(t, "claude-*=>upstream-$1")
	mapped, matched, err := applyRules("claude-sonnet", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "upstream-sonnet" {
		t.Fatalf("mapped=%q matched=%v", mapped, matched)
	}
}

func TestApplyRulesUnmatched(t *testing.T) {
	rules := mustParseRules(t, "a=>b")
	mapped, matched, err := applyRules("z", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if matched || mapped != "z" {
		t.Fatalf("mapped=%q matched=%v, want z false", mapped, matched)
	}
}

func TestApplyRulesUnchangedStillMatched(t *testing.T) {
	rules := mustParseRules(t, "a=>a")
	mapped, matched, err := applyRules("a", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "a" {
		t.Fatalf("mapped=%q matched=%v, want a true", mapped, matched)
	}
}

func TestApplyRulesSinglePassNoLoop(t *testing.T) {
	rules := mustParseRules(t, "a=>b;b=>a")
	mapped, matched, err := applyRules("a", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "a" {
		t.Fatalf("mapped=%q matched=%v, want a true after one finite pass", mapped, matched)
	}
}

func TestSelectRulesEndpointSpecificOverridesGlobal(t *testing.T) {
	cfg := Config{Enabled: true, GlobalRules: "global=>x", ClaudeMessagesRules: "claude=>x", CodexResponsesRules: "codex=>x", OpenAICompletionsRules: "openai=>x"}
	tests := map[string]string{
		"claude":          "claude=>x",
		"openai-response": "codex=>x",
		"openai":          "openai=>x",
	}
	for format, want := range tests {
		raw, ok := selectRules(cfg, format)
		if !ok || raw != want {
			t.Fatalf("selectRules(%q)=(%q,%v), want %q true", format, raw, ok, want)
		}
	}
}

func TestSelectRulesFallsBackToGlobal(t *testing.T) {
	cfg := Config{Enabled: true, GlobalRules: "global=>x"}
	for _, format := range []string{"claude", "openai-response", "openai", "gemini"} {
		raw, ok := selectRules(cfg, format)
		if !ok || raw != "global=>x" {
			t.Fatalf("selectRules(%q)=(%q,%v), want global=>x true", format, raw, ok)
		}
	}
}

func TestSelectRulesBothEmptySkips(t *testing.T) {
	if raw, ok := selectRules(defaultConfig(), "claude"); ok || raw != "" {
		t.Fatalf("selectRules empty=(%q,%v), want empty false", raw, ok)
	}
}

func TestRouteModelSkipsDisabledNoRulesUnmatchedAndUnchanged(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		format string
		model  string
	}{
		{name: "disabled", cfg: Config{Enabled: false, GlobalRules: "a=>b"}, format: "openai", model: "a"},
		{name: "no rules", cfg: defaultConfig(), format: "openai", model: "a"},
		{name: "unmatched", cfg: Config{Enabled: true, GlobalRules: "x=>y"}, format: "openai", model: "a"},
		{name: "unchanged", cfg: Config{Enabled: true, GlobalRules: "a=>a"}, format: "openai", model: "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := routeModel(tt.cfg, tt.format, tt.model)
			if err != nil {
				t.Fatalf("routeModel error = %v", err)
			}
			if decision.Handled || decision.OriginalModel != "" || decision.UpstreamModel != "" {
				t.Fatalf("decision=%#v, want unhandled with empty models", decision)
			}
		})
	}
}

func TestRouteModelHandlesOnlyMatchedChanged(t *testing.T) {
	cfg := Config{Enabled: true, OpenAICompletionsRules: "deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>gpt-5.4-mini", GlobalRules: "deepseek-v4-pro=>wrong"}
	decision, err := routeModel(cfg, "openai", "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("routeModel error = %v", err)
	}
	if !decision.Handled || decision.OriginalModel != "deepseek-v4-pro" || decision.UpstreamModel != "gpt-5.4-mini" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestRouteModelBadSelectedRulesErrors(t *testing.T) {
	cfg := Config{Enabled: true, ClaudeMessagesRules: "bad rule"}
	if _, err := routeModel(cfg, "claude", "a"); err == nil {
		t.Fatalf("routeModel bad selected rules error = nil")
	}
}

func TestHandleModelRouteUnhandledWhenNoChange(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "a=>a"})
	raw, err := json.Marshal(pluginapi.ModelRouteRequest{SourceFormat: "openai", RequestedModel: "a"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	respRaw, err := handleModelRoute(raw)
	if err != nil {
		t.Fatalf("handleModelRoute error = %v", err)
	}
	var resp pluginapi.ModelRouteResponse
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode route response: %v", err)
	}
	if resp.Handled {
		t.Fatalf("route handled=true, want false")
	}
}

func TestHandleModelRouteHandledSelfForChangedModel(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "deepseek-v4-pro=>gpt-5.4-mini"})
	raw, err := json.Marshal(pluginapi.ModelRouteRequest{SourceFormat: "openai", RequestedModel: "deepseek-v4-pro"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	respRaw, err := handleModelRoute(raw)
	if err != nil {
		t.Fatalf("handleModelRoute error = %v", err)
	}
	var resp pluginapi.ModelRouteResponse
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode route response: %v", err)
	}
	if !resp.Handled || resp.TargetKind != pluginapi.ModelRouteTargetSelf {
		t.Fatalf("route response=%#v", resp)
	}
	if resp.TargetModel != "" {
		t.Fatalf("self route TargetModel=%q, want empty because SDK only defines it for provider routes", resp.TargetModel)
	}
}

func TestRewriteRequestModelTopLevelOnly(t *testing.T) {
	got, changed, err := rewriteRequestModel([]byte(`{"model":"A","messages":[]}`), "B")
	if err != nil {
		t.Fatalf("rewriteRequestModel error = %v", err)
	}
	if !changed || string(got) == `{"model":"A","messages":[]}` {
		t.Fatalf("changed=%v body=%s", changed, got)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("rewritten JSON invalid: %v", err)
	}
	if decoded["model"] != "B" {
		t.Fatalf("model=%v, want B", decoded["model"])
	}
}

func TestRewriteRequestModelLeavesUnsupportedBodiesUnchanged(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"payload":{"model":"A"}}`),
		[]byte(`{"messages":[]}`),
		[]byte(`{"model":123}`),
		[]byte(`not-json`),
	}
	for _, body := range tests {
		got, changed, err := rewriteRequestModel(body, "B")
		if err != nil {
			t.Fatalf("rewriteRequestModel(%s) error = %v", body, err)
		}
		if changed || string(got) != string(body) {
			t.Fatalf("rewriteRequestModel(%s)=(%s,%v), want unchanged false", body, got, changed)
		}
	}
}

func TestRestoreResponseModelTopLevelOnly(t *testing.T) {
	got, changed, err := restoreResponseModel([]byte(`{"model":"B","id":"r1"}`), "A")
	if err != nil {
		t.Fatalf("restoreResponseModel error = %v", err)
	}
	if !changed {
		t.Fatalf("changed=false, want true")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("restored JSON invalid: %v", err)
	}
	if decoded["model"] != "A" {
		t.Fatalf("model=%v, want A", decoded["model"])
	}
}

func TestRestoreResponseModelLeavesUnsupportedBodiesUnchanged(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"payload":{"model":"B"}}`),
		[]byte(`{"id":"r1"}`),
		[]byte(`{"model":123}`),
		[]byte(`not-json`),
	}
	for _, body := range tests {
		got, changed, err := restoreResponseModel(body, "A")
		if err != nil {
			t.Fatalf("restoreResponseModel(%s) error = %v", body, err)
		}
		if changed || string(got) != string(body) {
			t.Fatalf("restoreResponseModel(%s)=(%s,%v), want unchanged false", body, got, changed)
		}
	}
}

func flattenChunks(chunks [][]byte) string {
	var b strings.Builder
	for _, chunk := range chunks {
		b.Write(chunk)
	}
	return b.String()
}

func TestSSERewriterRestoresCompleteJSONEvent(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("data: {\"model\":\"B\",\"id\":\"1\"}\n\n"))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	got := flattenChunks(out)
	if !strings.Contains(got, `"model":"A"`) || strings.Contains(got, `"model":"B"`) {
		t.Fatalf("rewritten event = %q", got)
	}
}

func TestSSERewriterBuffersSplitJSONUntilComplete(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("data: {\"model\":\"B"))
	if err != nil {
		t.Fatalf("first Write error = %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("first Write emitted %q, want no partial output", flattenChunks(out))
	}
	out, err = r.Write([]byte("\"}\n\n"))
	if err != nil {
		t.Fatalf("second Write error = %v", err)
	}
	got := flattenChunks(out)
	if !strings.Contains(got, `"model":"A"`) || strings.Contains(got, `"model":"B"`) {
		t.Fatalf("rewritten split event = %q", got)
	}
}

func TestSSERewriterPassesThroughDoneCommentsAndNonJSON(t *testing.T) {
	r := newSSERewriter("A")
	input := ": keepalive\n\ndata: [DONE]\n\ndata: hello\n\n"
	out, err := r.Write([]byte(input))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if got := flattenChunks(out); got != input {
		t.Fatalf("got %q, want %q", got, input)
	}
}

func TestSSERewriterHandlesMultipleEventsCRLFAndFlush(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("data: {\"model\":\"B\"}\r\n\r\ndata: [DONE]\r\n\r\nleftover"))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	got := flattenChunks(out)
	if !strings.Contains(got, `"model":"A"`) || !strings.Contains(got, "data: [DONE]") || strings.Contains(got, "leftover") {
		t.Fatalf("Write output = %q", got)
	}
	flushed, err := r.Flush()
	if err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if string(bytes.Join(flushed, nil)) != "leftover" {
		t.Fatalf("Flush output = %q", string(bytes.Join(flushed, nil)))
	}
}

func TestSSERewriterPreservesMultilineEventBoundaries(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("event: message\ndata: {\"model\":\"B\"}\nid: 1\n\n"))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	got := flattenChunks(out)
	want := "event: message\ndata: {\"model\":\"A\"}\nid: 1\n\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSSERewriterUsesEarliestDelimiter(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("data: {\"model\":\"B1\"}\n\ndata: {\"model\":\"B2\"}\r\n\r\n"))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	got := flattenChunks(out)
	want := "data: {\"model\":\"A\"}\n\ndata: {\"model\":\"A\"}\r\n\r\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

type rpcExecutorRequest struct {
	pluginapi.ExecutorRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
	StreamID       string `json:"stream_id,omitempty"`
}

type hostModelExecutionRequest struct {
	pluginapi.HostModelExecutionRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func TestHandleExecutorExecuteForwardsMappedRequestAndRestoresResponse(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "deepseek-v4-pro=>gpt-5.4-mini"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "deepseek-v4-pro",
			Format:          "openai",
			SourceFormat:    "openai",
			Stream:          false,
			Alt:             "alt-mode",
			Headers:         http.Header{"X-Test": []string{"1"}},
			Query:           url.Values{"q": []string{"1"}},
			OriginalRequest: []byte(`{"model":"deepseek-v4-pro","messages":[]}`),
		},
		HostCallbackID: "callback-1",
	}
	var captured hostModelExecutionRequest
	fakeHost := func(method string, payload any) (json.RawMessage, error) {
		if method != pluginabi.MethodHostModelExecute {
			t.Fatalf("method=%q, want %q", method, pluginabi.MethodHostModelExecute)
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		if err := json.Unmarshal(raw, &captured); err != nil {
			t.Fatalf("decode captured payload: %v", err)
		}
		return json.Marshal(pluginapi.HostModelExecutionResponse{
			StatusCode: 200,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       []byte(`{"model":"gpt-5.4-mini","id":"ok"}`),
		})
	}
	rawReq, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	respRaw, err := handleExecutorExecute(rawReq, fakeHost)
	if err != nil {
		t.Fatalf("handleExecutorExecute error = %v", err)
	}
	if captured.HostCallbackID != "callback-1" || captured.Model != "gpt-5.4-mini" || captured.EntryProtocol != "openai" || captured.ExitProtocol != "openai" || captured.Alt != "alt-mode" {
		t.Fatalf("captured=%#v", captured)
	}
	if !strings.Contains(string(captured.Body), `"model":"gpt-5.4-mini"`) {
		t.Fatalf("captured body=%s", captured.Body)
	}
	var resp pluginapi.ExecutorResponse
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode executor response: %v", err)
	}
	if !strings.Contains(string(resp.Payload), `"model":"deepseek-v4-pro"`) {
		t.Fatalf("payload=%s", resp.Payload)
	}
}

func TestHandleExecutorExecutePreservesHostError(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "a=>b"})
	req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "a", Format: "openai", SourceFormat: "openai", OriginalRequest: []byte(`{"model":"a"}`)}, HostCallbackID: "callback-1"}
	rawReq, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	_, err = handleExecutorExecute(rawReq, func(string, any) (json.RawMessage, error) {
		return nil, fmt.Errorf("upstream rejected model")
	})
	if err == nil || !strings.Contains(err.Error(), "upstream rejected model") {
		t.Fatalf("error=%v, want upstream error", err)
	}
}

func TestHandleExecutorExecuteReturnsErrorForHostHTTPStatus(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "a=>b"})
	req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "a", Format: "openai", SourceFormat: "openai", OriginalRequest: []byte(`{"model":"a"}`)}, HostCallbackID: "callback-1"}
	rawReq, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	_, err = handleExecutorExecute(rawReq, func(string, any) (json.RawMessage, error) {
		return json.Marshal(pluginapi.HostModelExecutionResponse{StatusCode: 404, Body: []byte(`{"error":"model not found"}`)})
	})
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("error=%v, want status and body in error", err)
	}
}
