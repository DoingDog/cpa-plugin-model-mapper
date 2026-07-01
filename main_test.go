package main

import (
	"encoding/json"
	"testing"
)

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
