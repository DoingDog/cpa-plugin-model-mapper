package main

import "testing"

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
