package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

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
	if reg.Metadata.Version != pluginVersion || reg.Metadata.Author == "" || reg.Metadata.GitHubRepository == "" {
		t.Fatalf("metadata missing CPA-required management fields: %#v", reg.Metadata)
	}
	if !reg.Capabilities.ModelRouter || !reg.Capabilities.Executor {
		t.Fatalf("capabilities=%#v, want model router and executor", reg.Capabilities)
	}
	if reg.Capabilities.ExecutorModelScope != string(pluginapi.ExecutorModelScopeStatic) {
		t.Fatalf("executor scope=%q", reg.Capabilities.ExecutorModelScope)
	}
	if !reflect.DeepEqual(reg.Capabilities.ExecutorInputFormats, []string{"openai", "claude", "openai-response"}) {
		t.Fatalf("executor input formats=%v", reg.Capabilities.ExecutorInputFormats)
	}
	if !reflect.DeepEqual(reg.Capabilities.ExecutorOutputFormats, []string{"openai", "claude", "openai-response"}) {
		t.Fatalf("executor output formats=%v", reg.Capabilities.ExecutorOutputFormats)
	}
	wantFields := []string{"global_rules", "claude_messages_rules", "codex_responses_rules", "openai_completions_rules"}
	got := make([]string, 0, len(reg.Metadata.ConfigFields))
	for _, field := range reg.Metadata.ConfigFields {
		got = append(got, field.Name)
		if field.Description == "" {
			t.Fatalf("config field %q has empty description", field.Name)
		}
	}
	if !reflect.DeepEqual(got, wantFields) {
		t.Fatalf("config fields=%v, want %v", got, wantFields)
	}
}

func TestDecodeLifecycleConfigUnquotesYAMLEmptyRuleStrings(t *testing.T) {
	rawYAML := []byte("enabled: true\nglobal_rules: \"\"\nclaude_messages_rules: 'literal\\*=>star'\ncodex_responses_rules: \"\"\nopenai_completions_rules: \"\"\n")
	rawReq, err := json.Marshal(map[string]string{"config_yaml": base64.StdEncoding.EncodeToString(rawYAML)})
	if err != nil {
		t.Fatalf("marshal lifecycle: %v", err)
	}
	cfgRaw, _, err := decodeLifecycleConfig(rawReq)
	if err != nil {
		t.Fatalf("decodeLifecycleConfig error = %v", err)
	}
	cfg, err := decodeConfig(cfgRaw)
	if err != nil {
		t.Fatalf("decodeConfig error = %v", err)
	}
	if cfg.GlobalRules != "" || cfg.CodexResponsesRules != "" || cfg.OpenAICompletionsRules != "" {
		t.Fatalf("empty quoted YAML rules were not unquoted: %#v", cfg)
	}
	if cfg.ClaudeMessagesRules != "literal\\*=>star" {
		t.Fatalf("claude rules = %q", cfg.ClaudeMessagesRules)
	}
}

func TestDecodeLifecycleConfigPreservesCaseOperations(t *testing.T) {
	rawYAML := []byte("enabled: true\nglobal_rules: '\\a;gpt-*=>deepseek-V3;\\A'\nclaude_messages_rules: \"\"\ncodex_responses_rules: \"\"\nopenai_completions_rules: \"\"\n")
	rawReq, err := json.Marshal(map[string]string{"config_yaml": base64.StdEncoding.EncodeToString(rawYAML)})
	if err != nil {
		t.Fatalf("marshal lifecycle: %v", err)
	}
	cfgRaw, _, err := decodeLifecycleConfig(rawReq)
	if err != nil {
		t.Fatalf("decodeLifecycleConfig error = %v", err)
	}
	cfg, err := decodeConfig(cfgRaw)
	if err != nil {
		t.Fatalf("decodeConfig error = %v", err)
	}
	if cfg.GlobalRules != `\a;gpt-*=>deepseek-V3;\A` {
		t.Fatalf("global rules = %q", cfg.GlobalRules)
	}
}

func TestDecodeConfigDefaultAndBadRules(t *testing.T) {
	if _, err := decodeConfig(nil); err != nil {
		t.Fatalf("decodeConfig nil error = %v", err)
	}
	if _, err := decodeConfig(json.RawMessage(`{"enabled":true,"global_rules":"bad rule"}`)); err == nil {
		t.Fatalf("decodeConfig bad rules error = nil")
	}
	badOperation, err := json.Marshal(map[string]any{
		"enabled":      true,
		"global_rules": `\x`,
	})
	if err != nil {
		t.Fatalf("marshal bad operation config: %v", err)
	}
	if _, err := decodeConfig(badOperation); err == nil {
		t.Fatalf("decodeConfig unknown operation error = nil")
	}
}

func TestDecodeConfigIgnoresCPAEnabled(t *testing.T) {
	cfg, err := decodeConfig(json.RawMessage(`{"enabled":false,"global_rules":"a=>b"}`))
	if err != nil {
		t.Fatalf("decodeConfig error = %v", err)
	}
	decision, err := routeModel(cfg, "openai", "a", "", "")
	if err != nil {
		t.Fatalf("routeModel error = %v", err)
	}
	if !decision.Handled || decision.UpstreamModel != "b" {
		t.Fatalf("decision=%#v, want a=>b", decision)
	}
}

func TestRouteModelReusesDecodedRules(t *testing.T) {
	var rawRules strings.Builder
	for i := 0; i < 24; i++ {
		if i > 0 {
			rawRules.WriteByte(';')
		}
		fmt.Fprintf(&rawRules, "model-%d=>target-%d", i, i)
	}
	rawConfig, err := json.Marshal(map[string]string{"global_rules": rawRules.String()})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	decoded, err := decodeConfig(rawConfig)
	if err != nil {
		t.Fatalf("decodeConfig error = %v", err)
	}
	direct := Config{GlobalRules: rawRules.String()}

	decodedAllocs := testing.AllocsPerRun(100, func() {
		decision, err := routeModel(decoded, "openai", "model-23", "", "")
		if err != nil || !decision.Handled {
			panic(fmt.Sprintf("decoded route failed: %#v %v", decision, err))
		}
	})
	directAllocs := testing.AllocsPerRun(100, func() {
		decision, err := routeModel(direct, "openai", "model-23", "", "")
		if err != nil || !decision.Handled {
			panic(fmt.Sprintf("direct route failed: %#v %v", decision, err))
		}
	})
	if decodedAllocs >= directAllocs {
		t.Fatalf("decoded config allocations=%v, direct config allocations=%v; decoded rules were not reused", decodedAllocs, directAllocs)
	}
}

func TestApplyRulesExactRulesDoNotAllocate(t *testing.T) {
	var raw strings.Builder
	for i := 0; i < 24; i++ {
		if i > 0 {
			raw.WriteByte(';')
		}
		fmt.Fprintf(&raw, "model-%d=>target-%d", i, i)
	}
	rules, err := parseRules(raw.String())
	if err != nil {
		t.Fatalf("parseRules error = %v", err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		mapped, matched, err := applyRules("model-23", "", "", rules)
		if err != nil || !matched || mapped != "target-23" {
			panic(fmt.Sprintf("applyRules=(%q,%v,%v)", mapped, matched, err))
		}
	})
	if allocs != 0 {
		t.Fatalf("exact-rule allocations=%v, want 0", allocs)
	}
}

func TestMatchTokensWildcardCapturesAllocateOnce(t *testing.T) {
	rules := mustParseRules(t, `a*b*c*d*e*f*g*h=>$1$2$3$4$5$6$7`)
	allocs := testing.AllocsPerRun(100, func() {
		captures, matched := matchTokens("a1b2c3d4e5f6g7h", rules[0].patternTokens)
		if !matched || len(captures) != 7 {
			panic(fmt.Sprintf("matchTokens=(%q,%v)", captures, matched))
		}
	})
	if allocs != 1 {
		t.Fatalf("wildcard capture allocations=%v, want 1", allocs)
	}
}

func TestApplyRulesWildcardLateMissAllocatesOncePerRule(t *testing.T) {
	raw := strings.TrimSuffix(strings.Repeat(`a*b*c*d*e*f*g*h*Z=>x;`, 24), ";")
	rules := mustParseRules(t, raw)
	allocs := testing.AllocsPerRun(100, func() {
		mapped, matched, err := applyRules("a1b2c3d4e5f6g7h8Y", "", "", rules)
		if err != nil || matched || mapped != "a1b2c3d4e5f6g7h8Y" {
			panic(fmt.Sprintf("applyRules=(%q,%v,%v)", mapped, matched, err))
		}
	})
	if allocs != 24 {
		t.Fatalf("late-miss allocations=%v, want 24", allocs)
	}
}

func BenchmarkApplyRulesExact24(b *testing.B) {
	var raw strings.Builder
	for i := 0; i < 24; i++ {
		if i > 0 {
			raw.WriteByte(';')
		}
		fmt.Fprintf(&raw, "model-%d=>target-%d", i, i)
	}
	rules, err := parseRules(raw.String())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mapped, matched, err := applyRules("model-23", "", "", rules)
		if err != nil || !matched || mapped != "target-23" {
			b.Fatalf("applyRules=(%q,%v,%v)", mapped, matched, err)
		}
	}
}

func TestDefaultConfigRulesEmpty(t *testing.T) {
	cfg := defaultConfig()
	if cfg.GlobalRules != "" || cfg.ClaudeMessagesRules != "" || cfg.CodexResponsesRules != "" || cfg.OpenAICompletionsRules != "" {
		t.Fatalf("default rule fields must be empty: %#v", cfg)
	}
}

func TestCallerScopeMatchesCPA(t *testing.T) {
	const want = "c512aa71887f77cd6b915a60aed04e1d0de0ed0721f041e51d1002402b3901db"
	if got := callerScope("sk-test"); got != want {
		t.Fatalf("callerScope(sk-test)=%q, want %q", got, want)
	}
	if got := callerScope("  sk-test  "); got != want {
		t.Fatalf("trimmed caller scope=%q, want %q", got, want)
	}
	if got := callerScope("   "); got != "" {
		t.Fatalf("blank caller scope=%q, want empty", got)
	}
}

func TestCallerAPIKeyUsesOnlyAuthenticatedCredential(t *testing.T) {
	tests := []struct {
		name    string
		headers http.Header
		query   url.Values
		scope   string
		want    string
	}{
		{name: "authorization bearer", headers: http.Header{"Authorization": {"Bearer sk-auth"}}, scope: callerScope("sk-auth"), want: "sk-auth"},
		{name: "authorization raw", headers: http.Header{"Authorization": {"sk-raw"}}, scope: callerScope("sk-raw"), want: "sk-raw"},
		{name: "google header", headers: http.Header{"X-Goog-Api-Key": {"sk-google"}}, scope: callerScope("sk-google"), want: "sk-google"},
		{name: "anthropic header", headers: http.Header{"X-Api-Key": {"sk-anthropic"}}, scope: callerScope("sk-anthropic"), want: "sk-anthropic"},
		{name: "query key", query: url.Values{"key": {"sk-query"}}, scope: callerScope("sk-query"), want: "sk-query"},
		{name: "query auth token", query: url.Values{"auth_token": {"sk-token"}}, scope: callerScope("sk-token"), want: "sk-token"},
		{name: "matching candidate after wrong candidate", headers: http.Header{"Authorization": {"Bearer wrong"}, "X-Api-Key": {"sk-right"}}, scope: callerScope("sk-right"), want: "sk-right"},
		{name: "header does not match authenticated scope", headers: http.Header{"Authorization": {"Bearer spoofed"}}, scope: callerScope("sk-authenticated")},
		{name: "missing authenticated scope", headers: http.Header{"Authorization": {"Bearer sk-auth"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := callerAPIKey(tt.headers, tt.query, tt.scope); got != tt.want {
				t.Fatalf("callerAPIKey()=%q, want %q", got, tt.want)
			}
		})
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
		`#sk-test#xxx=>yyy`,
		`#sk-test#\a`,
		`sk-kimi-*#*=>kimi-k3`,
		`#sk-*#*=>kimi`,
		`sk-\*-\#-key#source=>target`,
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

func TestParseRulesAcceptsCaseOperations(t *testing.T) {
	rules, err := parseRules(`a=>b;\a;\A;c=>d`)
	if err != nil {
		t.Fatalf("parseRules error = %v", err)
	}
	if len(rules) != 4 {
		t.Fatalf("len(rules) = %d, want 4", len(rules))
	}
	want := []caseOperation{
		caseOperationNone,
		caseOperationLower,
		caseOperationUpper,
		caseOperationNone,
	}
	for i, operation := range want {
		if rules[i].caseOperation != operation {
			t.Fatalf("rules[%d].caseOperation = %v, want %v", i, rules[i].caseOperation, operation)
		}
	}
}

func TestParseRulesAcceptsAPIKeyScopesAndEscapedHash(t *testing.T) {
	rules := mustParseRules(t, `sk-test#\a;sk-test#vendor\#model=>mapped\#name;plain\#model=>plain`)
	if len(rules) != 3 {
		t.Fatalf("len(rules)=%d, want 3", len(rules))
	}
	if rules[0].callerScope != callerScope("sk-test") || rules[0].caseOperation != caseOperationLower {
		t.Fatalf("scoped operation=%#v", rules[0])
	}
	if rules[1].callerScope != callerScope("sk-test") || rules[2].callerScope != "" {
		t.Fatalf("scopes=%q %q", rules[1].callerScope, rules[2].callerScope)
	}
	mapped, matched, err := applyRules("vendor#model", callerScope("sk-test"), "sk-test", rules[1:2])
	if err != nil {
		t.Fatalf("apply scoped escaped-hash rule: %v", err)
	}
	if !matched {
		t.Fatal("scoped escaped-hash rule did not match")
	}
	if mapped != "mapped#name" {
		t.Fatalf("mapped=%q, want mapped#name", mapped)
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
		`sk-*#source=>target-$1`,
		`sk-*#source-*=>target-$2`,
		`a\/b=>x`,
		`a=>\\`,
		`a=>\;`,
		`a=>\$`,
		`\x`,
		`\a=>x`,
		`\A=>x`,
		`x=>\a`,
		`x=>\A`,
		`#a=>b`,
		`##a=>b`,
		`#sk-test`,
		`#sk-test#`,
		`#sk-test#a=>b#c`,
		`sk-test#`,
		`sk-test#a=>b#c`,
		`sk\-test#a=>b`,
		`sk-\x#a=>b`,
		`#sk-\x#a=>b`,
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

func TestApplyRulesAPIKeyScopeExactAndFallsThrough(t *testing.T) {
	rules := mustParseRules(t, `sk-test#\a;sk-test#claude-haiku-*=>gpt-5.6-luna;claude-haiku-*=>gpt-5.6-sol`)
	tests := []struct {
		name, key, model, want string
		matched                bool
	}{
		{name: "exact key runs scoped operation and mapping", key: "sk-test", model: "CLAUDE-HAIKU-X", want: "gpt-5.6-luna", matched: true},
		{name: "different key skips scoped entries and uses fallback", key: "sk-other", model: "claude-haiku-X", want: "gpt-5.6-sol", matched: true},
		{name: "missing key skips scoped entries and uses fallback", model: "claude-haiku-X", want: "gpt-5.6-sol", matched: true},
		{name: "key comparison is case sensitive", key: "SK-TEST", model: "CLAUDE-HAIKU-X", want: "CLAUDE-HAIKU-X", matched: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, matched, err := applyRules(tt.model, callerScope(tt.key), tt.key, rules)
			if err != nil || got != tt.want || matched != tt.matched {
				t.Fatalf("applyRules=(%q,%v,%v), want (%q,%v,nil)", got, matched, err, tt.want, tt.matched)
			}
		})
	}
}

func TestApplyRulesAPIKeyScopePatterns(t *testing.T) {
	tests := []struct {
		name, raw, scope, key, model, want string
		matched                            bool
	}{
		{name: "inverse exact matches another key", raw: `#sk-key#xxx=>yyy`, scope: callerScope("sk-other"), key: "sk-other", model: "xxx", want: "yyy", matched: true},
		{name: "inverse exact excludes the named key", raw: `#sk-key#xxx=>yyy`, scope: callerScope("sk-key"), key: "sk-key", model: "xxx", want: "xxx"},
		{name: "inverse exact runs case operation", raw: `#sk-key#\a`, scope: callerScope("sk-other"), key: "sk-other", model: "ABC", want: "abc", matched: true},
		{name: "inverse exact mapping and operation stay ordered", raw: `#sk-key#XXX=>YYY;#sk-key#\a`, scope: callerScope("sk-other"), key: "sk-other", model: "XXX", want: "yyy", matched: true},
		{name: "inverse exact skips missing caller", raw: `#sk-key#xxx=>yyy`, model: "xxx", want: "xxx"},
		{name: "wildcard scope matches", raw: `sk-kimi-*#*=>kimi-k3`, scope: callerScope("sk-kimi-team"), key: "sk-kimi-team", model: "gpt-5", want: "kimi-k3", matched: true},
		{name: "multiple key wildcards do not create captures", raw: `sk-*-team-*#source-*=>target-$1`, scope: callerScope("sk-kimi-team-dev"), key: "sk-kimi-team-dev", model: "source-model", want: "target-model", matched: true},
		{name: "wildcard scope does not match another key", raw: `sk-kimi-*#*=>kimi-k3`, scope: callerScope("sk-openai-team"), key: "sk-openai-team", model: "gpt-5", want: "gpt-5"},
		{name: "key wildcard does not create model capture", raw: `sk-kimi-*#source-*=>target-$1`, scope: callerScope("sk-kimi-team"), key: "sk-kimi-team", model: "source-model", want: "target-model", matched: true},
		{name: "escaped scope star is literal", raw: `sk-\*#source=>star`, scope: callerScope("sk-*"), key: "sk-*", model: "source", want: "star", matched: true},
		{name: "escaped scope star is not wildcard", raw: `sk-\*#source=>star`, scope: callerScope("sk-other"), key: "sk-other", model: "source", want: "source"},
		{name: "escaped scope hash is literal", raw: `sk-\#-key#source=>hash`, scope: callerScope("sk-#-key"), key: "sk-#-key", model: "source", want: "hash", matched: true},
		{name: "inverse wildcard matches outside pattern", raw: `#sk-*#*=>kimi`, scope: callerScope("ak-team"), key: "ak-team", model: "gpt-5", want: "kimi", matched: true},
		{name: "inverse wildcard excludes matching key", raw: `#sk-*#*=>kimi`, scope: callerScope("sk-team"), key: "sk-team", model: "gpt-5", want: "gpt-5"},
		{name: "inverse wildcard skips missing caller", raw: `#sk-*#*=>kimi`, model: "gpt-5", want: "gpt-5"},
		{name: "inverse wildcard skips unavailable caller key", raw: `#sk-*#*=>kimi`, scope: callerScope("ak-team"), model: "gpt-5", want: "gpt-5"},
		{name: "inverse wildcard rejects key not bound to scope", raw: `#sk-*#*=>kimi`, scope: callerScope("ak-team"), key: "sk-spoofed", model: "gpt-5", want: "gpt-5"},
		{name: "wildcard rejects key not bound to scope", raw: `sk-kimi-*#*=>kimi-k3`, scope: callerScope("sk-other"), key: "sk-kimi-spoofed", model: "gpt-5", want: "gpt-5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLoadedConfigForTest(defaultConfig())
			got, matched, err := applyRules(tt.model, tt.scope, tt.key, mustParseRules(t, tt.raw))
			if err != nil || got != tt.want || matched != tt.matched {
				t.Fatalf("applyRules()=(%q,%v,%v), want (%q,%v,nil)", got, matched, err, tt.want, tt.matched)
			}
		})
	}
}

func TestApplyRulesFullChain(t *testing.T) {
	rules := mustParseRules(t, "deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>claude-v4-flash")
	mapped, matched, err := applyRules("deepseek-v4-pro", "", "", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "claude-v4-flash" {
		t.Fatalf("mapped=%q matched=%v, want claude-v4-flash true", mapped, matched)
	}
}

func TestApplyRulesWildcardCapture(t *testing.T) {
	rules := mustParseRules(t, "claude-*=>upstream-$1")
	mapped, matched, err := applyRules("claude-sonnet", "", "", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "upstream-sonnet" {
		t.Fatalf("mapped=%q matched=%v", mapped, matched)
	}
}

func TestApplyRulesCharacterSemantics(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		model       string
		want        string
		wantMatched bool
	}{
		{name: "ordinary punctuation", raw: `@cf/zai-org/gpt-5.4(medium)[1M]=>mapped`, model: `@cf/zai-org/gpt-5.4(medium)[1M]`, want: "mapped", wantMatched: true},
		{name: "escaped find hash is literal", raw: `vendor\#model=>mapped`, model: `vendor#model`, want: "mapped", wantMatched: true},
		{name: "escaped replacement hash is literal", raw: `source=>mapped\#name`, model: "source", want: "mapped#name", wantMatched: true},
		{name: "case sensitive", raw: `@cf/zai-org/gpt-5.4(medium)[1M]=>mapped`, model: `@cf/zai-org/gpt-5.4(medium)[1m]`, want: `@cf/zai-org/gpt-5.4(medium)[1m]`},
		{name: "requires matching prefix", raw: `gpt-5.5=>mapped`, model: `openai/gpt-5.5`, want: `openai/gpt-5.5`},
		{name: "requires matching suffix", raw: `gpt-5.5=>mapped`, model: `gpt-5.5(high)`, want: `gpt-5.5(high)`},
		{name: "wildcard crosses slash", raw: `@cf/*=>mapped-$1`, model: `@cf/zai-org/glm-4.7-flash`, want: `mapped-zai-org/glm-4.7-flash`, wantMatched: true},
		{name: "wildcard captures empty text", raw: `@cf/*=>mapped-$1`, model: `@cf/`, want: `mapped-`, wantMatched: true},
		{name: "wildcard does not backtrack", raw: `*-pro=>mapped`, model: `vendor-pro-pro`, want: `vendor-pro-pro`},
		{name: "multiple captures are numbered left to right", raw: `a*bc*=>x$2y$1`, model: `aONEbcTWO`, want: `xTWOyONE`, wantMatched: true},
		{name: "find dollar and replacement star are literal", raw: `price$=>literal*`, model: `price$`, want: `literal*`, wantMatched: true},
		{name: "escaped find dollar is literal", raw: `price\$=>mapped`, model: `price$`, want: `mapped`, wantMatched: true},
		{name: "escaped find star is literal", raw: `literal\*=>mapped`, model: `literal*`, want: `mapped`, wantMatched: true},
		{name: "escaped find star is not wildcard", raw: `literal\*=>mapped`, model: `literalX`, want: `literalX`},
		{name: "escaped find semicolon is literal", raw: `a\;b=>mapped`, model: `a;b`, want: `mapped`, wantMatched: true},
		{name: "escaped find separator is literal", raw: `a\=>b=>mapped`, model: `a=>b`, want: `mapped`, wantMatched: true},
		{name: "find literal backslash", raw: `vendor\\model=>mapped`, model: `vendor\model`, want: `mapped`, wantMatched: true},
		{name: "replace literal separator", raw: `source=>target\=>alias`, model: `source`, want: `target=>alias`, wantMatched: true},
		{name: "capture carries replacement punctuation", raw: `*=>copy-$1`, model: `price$;vendor\model`, want: `copy-price$;vendor\model`, wantMatched: true},
		{name: "lowercase ASCII letters only", raw: `\a`, model: `AbC-Z_19/éΩ中`, want: `abc-z_19/éΩ中`, wantMatched: true},
		{name: "uppercase ASCII letters only", raw: `\A`, model: `aBc-z_19/éω中`, want: `ABC-Z_19/éω中`, wantMatched: true},
		{name: "operation before case-sensitive mapping", raw: `\a;gpt-x=>mapped`, model: `GPT-X`, want: `mapped`, wantMatched: true},
		{name: "mapping before operation", raw: `foo=>bar-v2;\A`, model: `foo`, want: `BAR-V2`, wantMatched: true},
		{name: "later mapping remains case-sensitive", raw: `\a;GPT-X=>wrong`, model: `GPT-X`, want: `gpt-x`, wantMatched: true},
		{name: "full ordered case-operation chain", raw: `\a;gpt-*=>deepseek-V3;\A;DEEPSEEK-*=>gpt-5.5;\A`, model: `GPT-X`, want: `GPT-5.5`, wantMatched: true},
		{name: "literal backslash lowercase text remains mappable", raw: `\\a=>mapped`, model: `\a`, want: `mapped`, wantMatched: true},
		{name: "literal backslash uppercase text remains mappable", raw: `\\A=>mapped`, model: `\A`, want: `mapped`, wantMatched: true},
		{name: "repeated lowercase operations stay ordered", raw: `\a;\a`, model: `ABC`, want: `abc`, wantMatched: true},
		{name: "lowercase no-op still executes", raw: `\a`, model: `already-lower/é`, want: `already-lower/é`, wantMatched: true},
		{name: "uppercase no-op still executes", raw: `\A`, model: `ALREADY-UPPER/Ω`, want: `ALREADY-UPPER/Ω`, wantMatched: true},
		{name: "case operations can return to original", raw: `\a;\A`, model: `ABC`, want: `ABC`, wantMatched: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapped, matched, err := applyRules(tt.model, "", "", mustParseRules(t, tt.raw))
			if err != nil {
				t.Fatalf("applyRules error = %v", err)
			}
			if mapped != tt.want || matched != tt.wantMatched {
				t.Fatalf("mapped=%q matched=%v, want %q %v", mapped, matched, tt.want, tt.wantMatched)
			}
		})
	}
}

func TestApplyRulesCaseOperationRejectsEmptyModel(t *testing.T) {
	for _, raw := range []string{`\a`, `\A`} {
		t.Run(raw, func(t *testing.T) {
			mapped, matched, err := applyRules("", "", "", mustParseRules(t, raw))
			if err == nil || err.Error() != "empty mapped model" {
				t.Fatalf("mapped=%q matched=%v err=%v, want empty mapped model", mapped, matched, err)
			}
			if !matched {
				t.Fatalf("matched=false, want true for executed operation")
			}
		})
	}
}

func TestApplyRulesUnmatched(t *testing.T) {
	rules := mustParseRules(t, "a=>b")
	mapped, matched, err := applyRules("z", "", "", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if matched || mapped != "z" {
		t.Fatalf("mapped=%q matched=%v, want z false", mapped, matched)
	}
}

func TestApplyRulesUnchangedStillMatched(t *testing.T) {
	rules := mustParseRules(t, "a=>a")
	mapped, matched, err := applyRules("a", "", "", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "a" {
		t.Fatalf("mapped=%q matched=%v, want a true", mapped, matched)
	}
}

func TestApplyRulesSinglePassNoLoop(t *testing.T) {
	rules := mustParseRules(t, "a=>b;b=>a")
	mapped, matched, err := applyRules("a", "", "", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "a" {
		t.Fatalf("mapped=%q matched=%v, want a true after one finite pass", mapped, matched)
	}
}

func TestSelectRulesEndpointSpecificOverridesGlobal(t *testing.T) {
	cfg := Config{GlobalRules: "global=>x", ClaudeMessagesRules: "claude=>x", CodexResponsesRules: "codex=>x", OpenAICompletionsRules: "openai=>x"}
	tests := map[string]string{
		"claude":          "claude=>x",
		"openai-response": "codex=>x",
		"openai":          "openai=>x",
	}
	for format, want := range tests {
		raw, _, ok := selectRules(cfg, format)
		if !ok || raw != want {
			t.Fatalf("selectRules(%q)=(%q,%v), want %q true", format, raw, ok, want)
		}
	}
}

func TestSelectRulesFallsBackToGlobal(t *testing.T) {
	cfg := Config{GlobalRules: "global=>x"}
	for _, format := range []string{"claude", "openai-response", "openai", "gemini"} {
		raw, _, ok := selectRules(cfg, format)
		if !ok || raw != "global=>x" {
			t.Fatalf("selectRules(%q)=(%q,%v), want global=>x true", format, raw, ok)
		}
	}
}

func TestSelectRulesBothEmptySkips(t *testing.T) {
	if raw, _, ok := selectRules(defaultConfig(), "claude"); ok || raw != "" {
		t.Fatalf("selectRules empty=(%q,%v), want empty false", raw, ok)
	}
}

func TestRouteModelAPIKeyScopeAcrossRuleSets(t *testing.T) {
	const rules = "sk-test#client-model=>scoped-target;client-model=>fallback-target"
	tests := []struct {
		name   string
		cfg    Config
		format string
	}{
		{name: "global rules plus openai", cfg: Config{GlobalRules: rules}, format: "openai"},
		{name: "claude messages rules plus claude", cfg: Config{ClaudeMessagesRules: rules}, format: "claude"},
		{name: "codex responses rules plus openai response", cfg: Config{CodexResponsesRules: rules}, format: "openai-response"},
		{name: "openai completions rules plus openai", cfg: Config{OpenAICompletionsRules: rules}, format: "openai"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, scopeTest := range []struct {
				name, scope, want string
			}{
				{name: "exact scope", scope: callerScope("sk-test"), want: "scoped-target"},
				{name: "wrong scope", scope: callerScope("sk-other"), want: "fallback-target"},
				{name: "missing scope", want: "fallback-target"},
			} {
				t.Run(scopeTest.name, func(t *testing.T) {
					decision, err := routeModel(tt.cfg, tt.format, "client-model", scopeTest.scope, "")
					if err != nil {
						t.Fatalf("routeModel error = %v", err)
					}
					if !decision.Handled || decision.OriginalModel != "client-model" || decision.UpstreamModel != scopeTest.want {
						t.Fatalf("decision=%#v, want client-model=>%q", decision, scopeTest.want)
					}
				})
			}
		})
	}
}

func TestRouteModelSkipsNoRulesUnmatchedAndUnchanged(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		format string
		model  string
	}{
		{name: "no rules", cfg: defaultConfig(), format: "openai", model: "a"},
		{name: "unmatched", cfg: Config{GlobalRules: "x=>y"}, format: "openai", model: "a"},
		{name: "unchanged", cfg: Config{GlobalRules: "a=>a"}, format: "openai", model: "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := routeModel(tt.cfg, tt.format, tt.model, "", "")
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
	cfg := Config{OpenAICompletionsRules: "deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>gpt-5.4-mini", GlobalRules: "deepseek-v4-pro=>wrong"}
	decision, err := routeModel(cfg, "openai", "deepseek-v4-pro", "", "")
	if err != nil {
		t.Fatalf("routeModel error = %v", err)
	}
	if !decision.Handled || decision.OriginalModel != "deepseek-v4-pro" || decision.UpstreamModel != "gpt-5.4-mini" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestRouteModelCaseOperationChanged(t *testing.T) {
	cfg := Config{GlobalRules: `\A`}
	decision, err := routeModel(cfg, "openai", "model-v2", "", "")
	if err != nil {
		t.Fatalf("routeModel error = %v", err)
	}
	if !decision.Handled || decision.OriginalModel != "model-v2" || decision.UpstreamModel != "MODEL-V2" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestRouteModelCaseOperationNoChangeIsUnhandled(t *testing.T) {
	tests := []struct {
		name  string
		rules string
		model string
	}{
		{name: "lowercase no-op", rules: `\a`, model: "model-v2"},
		{name: "uppercase no-op", rules: `\A`, model: "MODEL-V2"},
		{name: "net identity", rules: `\a;\A`, model: "ABC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := routeModel(Config{GlobalRules: tt.rules}, "openai", tt.model, "", "")
			if err != nil {
				t.Fatalf("routeModel error = %v", err)
			}
			if decision.Handled || decision.OriginalModel != "" || decision.UpstreamModel != "" {
				t.Fatalf("decision=%#v, want unhandled with empty models", decision)
			}
		})
	}
}

func TestRouteModelBadSelectedRulesErrors(t *testing.T) {
	cfg := Config{ClaudeMessagesRules: "bad rule"}
	if _, err := routeModel(cfg, "claude", "a", "", ""); err == nil {
		t.Fatalf("routeModel bad selected rules error = nil")
	}
}

func TestHandleModelRouteUnhandledWhenNoChange(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "a=>a"})
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
	setLoadedConfigForTest(Config{GlobalRules: "deepseek-v4-pro=>gpt-5.4-mini"})
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

func TestHandleModelRouteIgnoresUnusedBody(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "a=>b"})
	raw := []byte(`{"SourceFormat":"openai","RequestedModel":"a","Body":{"unused":true}}`)
	respRaw, err := handleModelRoute(raw)
	if err != nil {
		t.Fatalf("handleModelRoute error = %v", err)
	}
	var resp pluginapi.ModelRouteResponse
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode route response: %v", err)
	}
	if !resp.Handled || resp.TargetKind != pluginapi.ModelRouteTargetSelf {
		t.Fatalf("route response=%#v, want handled self route", resp)
	}
}

func TestHandleModelRouteUsesCallerScope(t *testing.T) {
	scopedRules := "sk-test#a=>b"
	tests := []struct {
		name     string
		rules    string
		metadata map[string]any
		headers  http.Header
		query    url.Values
		handled  bool
	}{
		{name: "matching scope", rules: scopedRules, metadata: map[string]any{"caller_scope": callerScope("sk-test")}, handled: true},
		{name: "missing metadata", rules: scopedRules, handled: false},
		{name: "wrong scope", rules: scopedRules, metadata: map[string]any{"caller_scope": callerScope("sk-other")}, handled: false},
		{name: "non-string scope", rules: scopedRules, metadata: map[string]any{"caller_scope": 1}, handled: false},
		{name: "fallback for missing metadata", rules: scopedRules + ";a=>c", handled: true},
		{name: "fallback for wrong scope", rules: scopedRules + ";a=>c", metadata: map[string]any{"caller_scope": callerScope("sk-other")}, handled: true},
		{name: "wildcard scope", rules: "sk-kimi-*#a=>b", metadata: map[string]any{"caller_scope": callerScope("sk-kimi-team")}, headers: http.Header{"Authorization": {"Bearer sk-kimi-team"}}, handled: true},
		{name: "wildcard scope from query", rules: "sk-kimi-*#a=>b", metadata: map[string]any{"caller_scope": callerScope("sk-kimi-query")}, query: url.Values{"key": {"sk-kimi-query"}}, handled: true},
		{name: "spoofed wildcard header", rules: "sk-kimi-*#a=>b", metadata: map[string]any{"caller_scope": callerScope("sk-other")}, headers: http.Header{"Authorization": {"Bearer sk-kimi-spoofed"}}, handled: false},
		{name: "inverse exact other key", rules: "#sk-test#a=>b", metadata: map[string]any{"caller_scope": callerScope("sk-other")}, handled: true},
		{name: "inverse exact named key", rules: "#sk-test#a=>b", metadata: map[string]any{"caller_scope": callerScope("sk-test")}, handled: false},
		{name: "inverse wildcard outside pattern", rules: "#sk-*#a=>b", metadata: map[string]any{"caller_scope": callerScope("ak-team")}, headers: http.Header{"X-Api-Key": {"ak-team"}}, handled: true},
		{name: "inverse wildcard inside pattern", rules: "#sk-*#a=>b", metadata: map[string]any{"caller_scope": callerScope("sk-team")}, headers: http.Header{"X-Api-Key": {"sk-team"}}, handled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLoadedConfigForTest(Config{GlobalRules: tt.rules})
			raw, err := json.Marshal(pluginapi.ModelRouteRequest{SourceFormat: "openai", RequestedModel: "a", Metadata: tt.metadata, Headers: tt.headers, Query: tt.query})
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
			if resp.Handled != tt.handled {
				t.Fatalf("Handled=%v, want %v", resp.Handled, tt.handled)
			}
		})
	}
}

func TestModelRewriteTreatsOpaqueContentAsRawJSON(t *testing.T) {
	request := []byte(`{"model":"A","counter":9007199254740993,"message":{"model":"nested"},"content":[{"model":"content"}]}`)
	rewritten, changed, err := rewriteRequestModel(request, "B")
	if err != nil {
		t.Fatalf("rewriteRequestModel error = %v", err)
	}
	if !changed || !bytes.Contains(rewritten, []byte(`9007199254740993`)) {
		t.Fatalf("changed=%v body=%s, want exact opaque integer", changed, rewritten)
	}
	var requestDoc map[string]json.RawMessage
	if err := json.Unmarshal(rewritten, &requestDoc); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if string(requestDoc["model"]) != `"B"` || string(requestDoc["message"]) != `{"model":"nested"}` || string(requestDoc["content"]) != `[{"model":"content"}]` {
		t.Fatalf("request rewrite touched unsupported fields: %s", rewritten)
	}

	response := []byte(`{"model":"upstream","modelVersion":"upstream","counter":9007199254740993,"message":{"model":"upstream","modelVersion":"keep-message-version","nested":{"model":"keep-nested"}},"response":{"model":"upstream","modelVersion":"upstream","output":[{"model":"keep-output"}]},"content":[{"model":"keep-content"}]}`)
	restored, changed, err := restoreResponseModel(response, "client")
	if err != nil {
		t.Fatalf("restoreResponseModel error = %v", err)
	}
	if !changed || !bytes.Contains(restored, []byte(`9007199254740993`)) {
		t.Fatalf("changed=%v body=%s, want exact opaque integer", changed, restored)
	}
	var responseDoc map[string]json.RawMessage
	if err := json.Unmarshal(restored, &responseDoc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, key := range []string{"model", "modelVersion"} {
		if string(responseDoc[key]) != `"client"` {
			t.Fatalf("%s=%s, want client", key, responseDoc[key])
		}
	}
	var message, nestedResponse map[string]json.RawMessage
	if err := json.Unmarshal(responseDoc["message"], &message); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if err := json.Unmarshal(responseDoc["response"], &nestedResponse); err != nil {
		t.Fatalf("decode nested response: %v", err)
	}
	if string(message["model"]) != `"client"` || string(message["modelVersion"]) != `"keep-message-version"` || string(message["nested"]) != `{"model":"keep-nested"}` {
		t.Fatalf("message=%s, response whitelist violated", responseDoc["message"])
	}
	if string(nestedResponse["model"]) != `"client"` || string(nestedResponse["modelVersion"]) != `"client"` || string(nestedResponse["output"]) != `[{"model":"keep-output"}]` {
		t.Fatalf("response=%s, response whitelist violated", responseDoc["response"])
	}
	if string(responseDoc["content"]) != `[{"model":"keep-content"}]` {
		t.Fatalf("content=%s, want unchanged", responseDoc["content"])
	}

	small := []byte(`{"model":"A","messages":[{"value":1}]}`)
	item := `{"value":9007199254740993,"content":{"model":"opaque"}}`
	large := []byte(`{"model":"A","messages":[` + strings.TrimSuffix(strings.Repeat(item+",", 256), ",") + `]}`)
	allocs := func(body []byte) float64 {
		return testing.AllocsPerRun(50, func() {
			if _, _, err := rewriteRequestModel(body, "B"); err != nil {
				panic(err)
			}
		})
	}
	smallAllocs, largeAllocs := allocs(small), allocs(large)
	if largeAllocs > smallAllocs+32 {
		t.Fatalf("rewrite allocations scale with opaque nodes: small=%v large=%v", smallAllocs, largeAllocs)
	}
}

func TestRewriteRequestModelTopLevelOnly(t *testing.T) {
	got, changed, err := rewriteRequestModel([]byte(`{"model":"A","messages":[],"message":{"model":"A"},"response":{"model":"A"},"modelVersion":"A"}`), "B")
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
	if decoded["modelVersion"] != "A" {
		t.Fatalf("request modelVersion=%v, want unchanged A", decoded["modelVersion"])
	}
	message, _ := decoded["message"].(map[string]any)
	response, _ := decoded["response"].(map[string]any)
	if message["model"] != "A" || response["model"] != "A" {
		t.Fatalf("request nested model fields changed: %s", got)
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

func TestRestoreResponseWithoutModelUsesCloneOnly(t *testing.T) {
	body := []byte(`{"type":"response.output_text.delta","delta":"hello"}`)
	allocs := testing.AllocsPerRun(100, func() {
		got, changed, err := restoreResponseModel(body, "client")
		if err != nil || changed || !bytes.Equal(got, body) {
			panic(fmt.Sprintf("restore=(%s,%v,%v)", got, changed, err))
		}
	})
	if allocs > 1 {
		t.Fatalf("no-model allocations=%v, want <=1 clone", allocs)
	}
}

func TestRestoreResponseModelFastPathPreservesEscapedSemantics(t *testing.T) {
	backslash := string(rune(92))
	tests := []struct {
		name    string
		body    []byte
		changed bool
		want    string
	}{
		{name: "escaped model key", body: []byte(`{"` + backslash + `u006dodel":"upstream"}`), changed: true, want: `"model":"client"`},
		{name: "escaped nested response and model keys", body: []byte(`{"res` + backslash + `u0070onse":{"` + backslash + `u006dodel":"upstream"}}`), changed: true, want: `"model":"client"`},
		{name: "escaped equal value remains unchanged", body: []byte(`{"model":"cli` + backslash + `u0065nt"}`), changed: false},
		{name: "ordinary escaped text remains unchanged", body: []byte(`{"text":"line` + backslash + `nnext"}`), changed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := restoreResponseModel(tt.body, "client")
			if err != nil || changed != tt.changed {
				t.Fatalf("restore=(%s,%v,%v)", got, changed, err)
			}
			if tt.want != "" && !strings.Contains(string(got), tt.want) {
				t.Fatalf("body=%s missing %s", got, tt.want)
			}
			if !tt.changed && !bytes.Equal(got, tt.body) {
				t.Fatalf("unchanged body=%s, want %s", got, tt.body)
			}
		})
	}
}

func TestRestoreResponseModelPreservesEqualInvalidUTF8(t *testing.T) {
	body := []byte{0x7b, 0x22, 0x6d, 0x6f, 0x64, 0x65, 0x6c, 0x22, 0x3a, 0x22, 0xff, 0x22, 0x7d}
	got, changed, err := restoreResponseModel(body, "�")
	if err != nil {
		t.Fatalf("restoreResponseModel error = %v", err)
	}
	if changed || !bytes.Equal(got, body) {
		t.Fatalf("restoreResponseModel=(%x,%v), want unchanged %x", got, changed, body)
	}
}

func TestSSERewriterPreservesEqualInvalidUTF8(t *testing.T) {
	body := []byte{0x7b, 0x22, 0x6d, 0x6f, 0x64, 0x65, 0x6c, 0x22, 0x3a, 0x22, 0xff, 0x22, 0x7d}
	input := append([]byte("data: "), body...)
	input = append(input, '\n', '\n')
	chunks, err := newSSERewriter("�").Write(input)
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if got := []byte(flattenChunks(chunks)); !bytes.Equal(got, input) {
		t.Fatalf("Write output=%x, want %x", got, input)
	}
}

func BenchmarkRestoreResponseWithoutModel(b *testing.B) {
	body := []byte(`{"type":"response.output_text.delta","delta":"hello"}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := restoreResponseModel(body, "client"); err != nil {
			b.Fatal(err)
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

func TestSSERewriterHandlesSplitDelimiters(t *testing.T) {
	for _, delimiter := range []string{"\n\n", "\r\n\r\n"} {
		for split := 1; split < len(delimiter); split++ {
			t.Run(fmt.Sprintf("%q at %d", delimiter, split), func(t *testing.T) {
				r := newSSERewriter("A")
				prefix := "data: hello" + delimiter[:split]
				out, err := r.Write([]byte(prefix))
				if err != nil {
					t.Fatalf("first Write error = %v", err)
				}
				if len(out) != 0 {
					t.Fatalf("first Write chunks=%q, want buffered", out)
				}
				out, err = r.Write([]byte(delimiter[split:]))
				if err != nil {
					t.Fatalf("second Write error = %v", err)
				}
				if got, want := flattenChunks(out), "data: hello"+delimiter; got != want {
					t.Fatalf("output=%q, want %q", got, want)
				}
			})
		}
	}
}

func TestIncompleteSSEPrefixLargeRawJSONDoesNotAllocate(t *testing.T) {
	payload := []byte(`{"type":"response.delta","text":"` + strings.Repeat("x", 64<<10) + `"}`)
	allocs := testing.AllocsPerRun(100, func() {
		if isIncompleteSSEPrefix(payload) {
			panic("raw JSON classified as incomplete SSE prefix")
		}
	})
	if allocs != 0 {
		t.Fatalf("isIncompleteSSEPrefix allocations=%v, want 0", allocs)
	}
}

func TestSSEEventDelimiterFromScanOffset(t *testing.T) {
	tests := []struct {
		name     string
		buf      string
		start    int
		eventLen int
		delimLen int
		next     int
	}{
		{name: "LF", buf: "data: one\n\ntail", start: 0, eventLen: 9, delimLen: 2},
		{name: "CRLF", buf: "data: one\r\n\r\ntail", start: 0, eventLen: 9, delimLen: 4},
		{name: "earliest mixed", buf: "a\n\nb\r\n\r\n", start: 0, eventLen: 1, delimLen: 2},
		{name: "scan offset", buf: "ignored\n\ndata: two\n\n", start: 9, eventLen: 18, delimLen: 2},
		{name: "split CRLF tail", buf: "data: one\r\n\r", start: 8, next: 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventLen, delimLen, next := sseEventDelimiter([]byte(tt.buf), tt.start)
			if eventLen != tt.eventLen || delimLen != tt.delimLen || next != tt.next {
				t.Fatalf("delimiter=(%d,%d,%d), want (%d,%d,%d)", eventLen, delimLen, next, tt.eventLen, tt.delimLen, tt.next)
			}
		})
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

func TestSSERewriterFlushRestoresUnterminatedDataLine(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"model\":\"B\"}}"))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("Write emitted %q, want no partial output", flattenChunks(out))
	}
	flushed, err := r.Flush()
	if err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	got := string(bytes.Join(flushed, nil))
	if !strings.Contains(got, `"response":{"model":"A"`) || strings.Contains(got, `"model":"B"`) {
		t.Fatalf("Flush output = %q", got)
	}
}

func TestSSERewriterInsertsLineBreakBetweenSplitFields(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("data: {\"model\":\"B\"}"))
	if err != nil {
		t.Fatalf("first Write error = %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("first Write emitted %q, want no partial output", flattenChunks(out))
	}
	out, err = r.Write([]byte("event: response.in_progress\ndata: {\"model\":\"B\"}\n\n"))
	if err != nil {
		t.Fatalf("second Write error = %v", err)
	}
	got := flattenChunks(out)
	want := "data: {\"model\":\"A\"}\nevent: response.in_progress\ndata: {\"model\":\"A\"}\n\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSSERewriterPreservesMultilineEventBoundaries(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("event: message\ndata: {\"model\":\"B\"}\nid: 1\n\n"))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	want := [][]byte{
		[]byte("event: message\n"),
		[]byte("data: {\"model\":\"A\"}\n"),
		[]byte("id: 1"),
		[]byte("\n\n"),
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("chunks=%q, want %q", out, want)
	}
}

func TestSSERewriterReleasesLargeEventBufferAfterSmallTail(t *testing.T) {
	r := newSSERewriter("A")
	input := "event: " + strings.Repeat("x", 256<<10) + "\n\nx"
	backing := make([]byte, len(input))
	copy(backing, input)
	r.buf = backing
	start := uintptr(unsafe.Pointer(unsafe.SliceData(backing)))
	end := start + uintptr(cap(backing))
	if _, err := r.Write(nil); err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if string(r.buf) != "x" {
		t.Fatalf("tail=%q, want x", r.buf)
	}
	tail := uintptr(unsafe.Pointer(unsafe.SliceData(r.buf)))
	runtime.KeepAlive(backing)
	if tail >= start && tail < end {
		t.Fatal("small tail still aliases the consumed event allocation")
	}
}

func TestSSERewriterAvoidsWholeEventCopy(t *testing.T) {
	input := []byte("event: " + strings.Repeat("x", 256<<10) + "\n\n")
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			r := newSSERewriter("A")
			chunks, err := r.Write(input)
			if err != nil || len(chunks) != 2 {
				b.Fatalf("Write=(%d,%v)", len(chunks), err)
			}
		}
	})
	if got, limit := result.AllocedBytesPerOp(), int64(float64(len(input))*2.6); got > limit {
		t.Fatalf("allocated bytes/op=%d, want <=%d without a whole-event copy", got, limit)
	}
}

func BenchmarkSSERewriterCompleteLargeEvent(b *testing.B) {
	payload := []byte("event: " + strings.Repeat("x", 64<<10) + "\n\n")
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		r := newSSERewriter("client")
		chunks, err := r.Write(payload)
		if err != nil || len(chunks) != 2 {
			b.Fatalf("Write=(%d,%v)", len(chunks), err)
		}
	}
}

func BenchmarkSSERewriterSplitLargeEvent(b *testing.B) {
	payload := []byte("data: {\"text\":\"" + strings.Repeat("x", 64<<10) + "\"}\n\n")
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		r := newSSERewriter("A")
		for start := 0; start < len(payload); start += 32 {
			end := min(start+32, len(payload))
			if _, err := r.Write(payload[start:end]); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkSSERewriterMultiEventBatches(b *testing.B) {
	for _, delimiter := range []struct {
		name  string
		bytes []byte
	}{
		{name: "LF", bytes: []byte("data:x\n\n")},
		{name: "CRLF", bytes: []byte("data:x\r\n\r\n")},
	} {
		for _, events := range []int{128, 512, 2048, 8192} {
			b.Run(fmt.Sprintf("%s/%d", delimiter.name, events), func(b *testing.B) {
				payload := bytes.Repeat(delimiter.bytes, events)
				b.ReportAllocs()
				b.SetBytes(int64(len(payload)))
				for i := 0; i < b.N; i++ {
					r := newSSERewriter("client")
					chunks, err := r.Write(payload)
					if err != nil || len(chunks) != 2*events {
						b.Fatalf("Write=(%d,%v), want %d chunks", len(chunks), err, 2*events)
					}
				}
			})
		}
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
	setLoadedConfigForTest(Config{GlobalRules: "deepseek-v4-pro=>gpt-5.4-mini"})
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

func TestHandleExecutorExecuteIgnoresUnusedPayload(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "a=>b"})
	rawReq, err := json.Marshal(map[string]any{
		"Model":           "a",
		"Format":          "openai",
		"SourceFormat":    "openai",
		"OriginalRequest": []byte(`{"model":"a"}`),
		"Payload":         map[string]any{"unused": true},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	respRaw, err := handleExecutorExecute(rawReq, func(method string, payload any) (json.RawMessage, error) {
		if method != pluginabi.MethodHostModelExecute {
			t.Fatalf("method=%q, want %q", method, pluginabi.MethodHostModelExecute)
		}
		return json.Marshal(pluginapi.HostModelExecutionResponse{StatusCode: 200, Body: []byte(`{"model":"b"}`)})
	})
	if err != nil {
		t.Fatalf("handleExecutorExecute error = %v", err)
	}
	var resp pluginapi.ExecutorResponse
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode executor response: %v", err)
	}
	if !bytes.Contains(resp.Payload, []byte(`"model":"a"`)) {
		t.Fatalf("response payload=%s, want restored model a", resp.Payload)
	}
}

func TestExecutorReusesCallerPatternAfterHeadersChange(t *testing.T) {
	tests := []struct {
		name, rules, key, upstream string
		headers                    http.Header
	}{
		{name: "positive wildcard", rules: "sk-kimi-*#client-model=>wildcard-target", key: "sk-kimi-team", headers: http.Header{"Authorization": {"Bearer sk-kimi-team"}}, upstream: "wildcard-target"},
		{name: "inverse wildcard", rules: "#sk-*#client-model=>inverse-target", key: "ak-team", headers: http.Header{"X-Api-Key": {"ak-team"}}, upstream: "inverse-target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLoadedConfigForTest(Config{GlobalRules: tt.rules})
			metadata := map[string]any{"caller_scope": callerScope(tt.key)}
			routeRaw, err := json.Marshal(pluginapi.ModelRouteRequest{SourceFormat: "openai", RequestedModel: "client-model", Metadata: metadata, Headers: tt.headers})
			if err != nil {
				t.Fatalf("marshal route request: %v", err)
			}
			responseRaw, err := handleModelRoute(routeRaw)
			if err != nil {
				t.Fatalf("handleModelRoute error = %v", err)
			}
			var routeResponse pluginapi.ModelRouteResponse
			if err := json.Unmarshal(responseRaw, &routeResponse); err != nil || !routeResponse.Handled {
				t.Fatalf("route response=%s err=%v, want handled", responseRaw, err)
			}

			req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
				Model:           "client-model",
				Format:          "openai",
				SourceFormat:    "openai",
				Metadata:        metadata,
				Headers:         http.Header{"Authorization": {"Bearer interceptor-replacement"}},
				OriginalRequest: []byte(`{"model":"client-model"}`),
			}}
			rawReq, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("marshal executor request: %v", err)
			}
			var captured hostModelExecutionRequest
			_, err = handleExecutorExecute(rawReq, func(method string, payload any) (json.RawMessage, error) {
				raw, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(raw, &captured); err != nil {
					return nil, err
				}
				return json.Marshal(pluginapi.HostModelExecutionResponse{StatusCode: 200, Body: []byte(`{"model":"` + tt.upstream + `"}`)})
			})
			if err != nil {
				t.Fatalf("handleExecutorExecute error = %v", err)
			}
			if captured.Model != tt.upstream {
				t.Fatalf("forwarded model=%q, want %q", captured.Model, tt.upstream)
			}
		})
	}
}

func TestSetLoadedConfigPublishesWithCallerCacheReset(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "old=>target"})
	callerPatternCacheMu.Lock()
	loadedConfigMu.RLock()
	done := make(chan struct{})
	go func() {
		setLoadedConfigForTest(Config{GlobalRules: "new=>target"})
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for loadedConfigMu.TryRLock() {
		loadedConfigMu.RUnlock()
		if time.Now().After(deadline) {
			loadedConfigMu.RUnlock()
			callerPatternCacheMu.Unlock()
			t.Fatal("reconfigure did not wait for the config write lock")
		}
		runtime.Gosched()
	}
	loadedConfigMu.RUnlock()

	observed := make(chan Config, 1)
	go func() { observed <- loadedConfig() }()
	select {
	case cfg := <-observed:
		callerPatternCacheMu.Unlock()
		<-done
		if cfg.GlobalRules == "new=>target" {
			t.Fatal("new config became visible before the caller cache reset")
		}
		t.Fatalf("observed unexpected config before reset: %#v", cfg)
	case <-time.After(100 * time.Millisecond):
		callerPatternCacheMu.Unlock()
		<-done
		cfg := <-observed
		if cfg.GlobalRules != "new=>target" {
			t.Fatalf("config after reset=%#v, want new rules", cfg)
		}
	}
}

func TestHandleModelRouteWarmCallerPatternHitDoesNotRequireConfigWriteLock(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "sk-kimi-*#client-model=>wildcard-target"})
	raw, err := json.Marshal(pluginapi.ModelRouteRequest{
		SourceFormat:   "openai",
		RequestedModel: "client-model",
		Metadata:       map[string]any{"caller_scope": callerScope("sk-kimi-team")},
		Headers:        http.Header{"Authorization": {"Bearer sk-kimi-team"}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, err := handleModelRoute(raw); err != nil {
		t.Fatalf("warm route: %v", err)
	}

	loadedConfigMu.RLock()
	done := make(chan error, 1)
	go func() {
		_, err := handleModelRoute(raw)
		done <- err
	}()
	select {
	case err := <-done:
		loadedConfigMu.RUnlock()
		if err != nil {
			t.Fatalf("warm route error = %v", err)
		}
	case <-time.After(time.Second):
		loadedConfigMu.RUnlock()
		err := <-done
		if err != nil {
			t.Fatalf("blocked route error = %v", err)
		}
		t.Fatal("warm caller-pattern hit blocked on the config write lock")
	}
}

func TestReconfigureClearsCallerPatternCache(t *testing.T) {
	const rules = "sk-kimi-*#client-model=>wildcard-target"
	setLoadedConfigForTest(Config{GlobalRules: rules})
	metadata := map[string]any{"caller_scope": callerScope("sk-kimi-team")}
	routeRaw, err := json.Marshal(pluginapi.ModelRouteRequest{
		SourceFormat:   "openai",
		RequestedModel: "client-model",
		Metadata:       metadata,
		Headers:        http.Header{"Authorization": {"Bearer sk-kimi-team"}},
	})
	if err != nil {
		t.Fatalf("marshal route request: %v", err)
	}
	responseRaw, err := handleModelRoute(routeRaw)
	if err != nil {
		t.Fatalf("warm caller pattern cache: %v", err)
	}
	var response pluginapi.ModelRouteResponse
	if err := json.Unmarshal(responseRaw, &response); err != nil || !response.Handled {
		t.Fatalf("warm route response=%s err=%v, want handled", responseRaw, err)
	}

	if _, err := handlePluginReconfigure([]byte(`{"enabled":true,"global_rules":"sk-kimi-*#client-model=>wildcard-target"}`)); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}
	routeRaw, err = json.Marshal(pluginapi.ModelRouteRequest{SourceFormat: "openai", RequestedModel: "client-model", Metadata: metadata})
	if err != nil {
		t.Fatalf("marshal route without credential: %v", err)
	}
	responseRaw, err = handleModelRoute(routeRaw)
	if err != nil {
		t.Fatalf("route after reconfigure: %v", err)
	}
	if err := json.Unmarshal(responseRaw, &response); err != nil {
		t.Fatalf("decode route after reconfigure: %v", err)
	}
	if response.Handled {
		t.Fatalf("route after reconfigure=%s, want unhandled without bound caller", responseRaw)
	}
}

func TestHandleExecutorExecuteUsesCallerScopeAcrossRuleSets(t *testing.T) {
	const rules = "sk-test#client-model=>scoped-target;sk-kimi-*#client-model=>wildcard-target;#sk-*#client-model=>inverse-target;client-model=>fallback-target"
	tests := []struct {
		name   string
		cfg    Config
		format string
	}{
		{name: "global rules", cfg: Config{GlobalRules: rules}, format: "openai"},
		{name: "openai completions rules", cfg: Config{OpenAICompletionsRules: rules}, format: "openai"},
		{name: "claude messages rules", cfg: Config{ClaudeMessagesRules: rules}, format: "claude"},
		{name: "codex responses rules", cfg: Config{CodexResponsesRules: rules}, format: "openai-response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scopeTests := []struct {
				name     string
				metadata map[string]any
				headers  http.Header
				query    url.Values
				upstream string
			}{
				{name: "matching exact scope", metadata: map[string]any{"caller_scope": callerScope("sk-test")}, upstream: "scoped-target"},
				{name: "wrong exact scope", metadata: map[string]any{"caller_scope": callerScope("sk-other")}, upstream: "fallback-target"},
				{name: "matching wildcard scope", metadata: map[string]any{"caller_scope": callerScope("sk-kimi-team")}, headers: http.Header{"Authorization": {"Bearer sk-kimi-team"}}, upstream: "wildcard-target"},
				{name: "matching inverse wildcard scope", metadata: map[string]any{"caller_scope": callerScope("ak-team")}, query: url.Values{"auth_token": {"ak-team"}}, upstream: "inverse-target"},
			}
			for _, scopeTest := range scopeTests {
				t.Run(scopeTest.name, func(t *testing.T) {
					setLoadedConfigForTest(tt.cfg)
					req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
						Model:           "client-model",
						Format:          tt.format,
						SourceFormat:    tt.format,
						Metadata:        scopeTest.metadata,
						Headers:         scopeTest.headers,
						Query:           scopeTest.query,
						OriginalRequest: []byte(`{"model":"client-model"}`),
					}}
					rawReq, err := json.Marshal(req)
					if err != nil {
						t.Fatalf("marshal request: %v", err)
					}
					var captured hostModelExecutionRequest
					respRaw, err := handleExecutorExecute(rawReq, func(method string, payload any) (json.RawMessage, error) {
						if method != pluginabi.MethodHostModelExecute {
							t.Fatalf("method=%q, want %q", method, pluginabi.MethodHostModelExecute)
						}
						raw, err := json.Marshal(payload)
						if err != nil {
							t.Fatalf("marshal host payload: %v", err)
						}
						if err := json.Unmarshal(raw, &captured); err != nil {
							t.Fatalf("decode host payload: %v", err)
						}
						return json.Marshal(pluginapi.HostModelExecutionResponse{StatusCode: 200, Body: []byte(`{"model":"` + scopeTest.upstream + `"}`)})
					})
					if err != nil {
						t.Fatalf("handleExecutorExecute error = %v", err)
					}
					if captured.Model != scopeTest.upstream {
						t.Fatalf("HostModelExecutionRequest.Model=%q, want %q", captured.Model, scopeTest.upstream)
					}
					var body struct {
						Model string `json:"model"`
					}
					if err := json.Unmarshal(captured.Body, &body); err != nil {
						t.Fatalf("unmarshal forwarded body: %v", err)
					}
					if body.Model != scopeTest.upstream {
						t.Fatalf("forwarded body model=%q, want %q", body.Model, scopeTest.upstream)
					}
					var resp pluginapi.ExecutorResponse
					if err := json.Unmarshal(respRaw, &resp); err != nil {
						t.Fatalf("decode executor response: %v", err)
					}
					if !strings.Contains(string(resp.Payload), `"model":"client-model"`) {
						t.Fatalf("response payload=%s", resp.Payload)
					}
				})
			}
		})
	}
}

func TestHandleExecutorExecuteRestoresKnownResponseModelFields(t *testing.T) {
	setLoadedConfigForTest(Config{ClaudeMessagesRules: "claude-*=>gpt-5.5"})
	req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "claude-opus-4", Format: "claude", SourceFormat: "claude", OriginalRequest: []byte(`{"model":"claude-opus-4"}`)}, HostCallbackID: "callback-1"}
	rawReq, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	respRaw, err := handleExecutorExecute(rawReq, func(string, any) (json.RawMessage, error) {
		return json.Marshal(pluginapi.HostModelExecutionResponse{StatusCode: 200, Body: []byte(`{"model":"gpt-5.5","modelVersion":"gpt-5.5","message":{"model":"gpt-5.5"},"response":{"model":"gpt-5.5","modelVersion":"gpt-5.5"},"content":[{"text":"gpt-5.5 should stay in content"}]}`)})
	})
	if err != nil {
		t.Fatalf("handleExecutorExecute error = %v", err)
	}
	var resp pluginapi.ExecutorResponse
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode executor response: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v: %s", err, resp.Payload)
	}
	if payload["model"] != "claude-opus-4" || payload["modelVersion"] != "claude-opus-4" {
		t.Fatalf("payload=%s, top-level model fields not restored", resp.Payload)
	}
	message, ok := payload["message"].(map[string]any)
	if !ok || message["model"] != "claude-opus-4" {
		t.Fatalf("payload=%s, message.model not restored", resp.Payload)
	}
	response, ok := payload["response"].(map[string]any)
	if !ok || response["model"] != "claude-opus-4" || response["modelVersion"] != "claude-opus-4" {
		t.Fatalf("payload=%s, response model fields not restored", resp.Payload)
	}
	if !strings.Contains(string(resp.Payload), `gpt-5.5 should stay in content`) {
		t.Fatalf("payload=%s, content text should not be rewritten", resp.Payload)
	}
}

func TestHandleExecutorExecutePreservesHostError(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "a=>b"})
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
	setLoadedConfigForTest(Config{GlobalRules: "a=>b"})
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

func TestHandleExecutorExecuteStreamUsesCallerScope(t *testing.T) {
	tests := []struct {
		name, rules, key, upstream string
		headers                    http.Header
		query                      url.Values
	}{
		{name: "exact", rules: "sk-test#client-model=>scoped-target", key: "sk-test", upstream: "scoped-target"},
		{name: "wildcard", rules: "sk-kimi-*#client-model=>wildcard-target", key: "sk-kimi-team", headers: http.Header{"Authorization": {"Bearer sk-kimi-team"}}, upstream: "wildcard-target"},
		{name: "inverse wildcard", rules: "#sk-*#client-model=>inverse-target", key: "ak-team", query: url.Values{"auth_token": {"ak-team"}}, upstream: "inverse-target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLoadedConfigForTest(Config{GlobalRules: tt.rules})
			req := rpcExecutorRequest{
				ExecutorRequest: pluginapi.ExecutorRequest{
					Model:           "client-model",
					Format:          "openai",
					SourceFormat:    "openai",
					Stream:          true,
					OriginalRequest: []byte(`{"model":"client-model","stream":true}`),
					Metadata:        map[string]any{"caller_scope": callerScope(tt.key)},
					Headers:         tt.headers,
					Query:           tt.query,
				},
				HostCallbackID: "callback-1",
				StreamID:       "plugin-stream-caller-scope-1",
			}
			reads := []pluginapi.HostModelStreamReadResponse{
				{Payload: []byte("data: {\"model\":\"" + tt.upstream + "\"}\n\n")},
				{Payload: []byte("data: [DONE]\n\n")},
				{Done: true},
			}
			var forwarded pluginapi.HostModelExecutionRequest
			emitted, closedHost, closedPlugin, _, err := runExecutorStreamTestWithForwarded(req, reads, &forwarded)
			if err != nil {
				t.Fatalf("handleExecutorExecuteStream error = %v", err)
			}
			if forwarded.Model != tt.upstream {
				t.Fatalf("forwarded model=%q, want %q", forwarded.Model, tt.upstream)
			}
			var body struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(forwarded.Body, &body); err != nil {
				t.Fatalf("unmarshal forwarded body: %v", err)
			}
			if body.Model != tt.upstream {
				t.Fatalf("forwarded body model=%q, want %q", body.Model, tt.upstream)
			}
			joined := strings.Join(emitted, "")
			if !strings.Contains(joined, `"model":"client-model"`) || strings.Contains(joined, tt.upstream) || !strings.Contains(joined, "data: [DONE]") {
				t.Fatalf("emitted=%q", joined)
			}
			if !closedHost || !closedPlugin {
				t.Fatalf("closedHost=%v closedPlugin=%v", closedHost, closedPlugin)
			}
		})
	}
}

func TestHandleExecutorExecuteStreamFallsBackForWrongOrMissingCallerScope(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "sk-test#client-model=>scoped-target;client-model=>fallback-target"})
	for _, scopeTest := range []struct {
		name     string
		metadata map[string]any
	}{
		{name: "wrong scope", metadata: map[string]any{"caller_scope": callerScope("other-key")}},
		{name: "missing scope"},
	} {
		t.Run(scopeTest.name, func(t *testing.T) {
			req := rpcExecutorRequest{
				ExecutorRequest: pluginapi.ExecutorRequest{
					Model:           "client-model",
					Format:          "openai",
					SourceFormat:    "openai",
					Stream:          true,
					OriginalRequest: []byte(`{"model":"client-model","stream":true}`),
					Metadata:        scopeTest.metadata,
				},
				HostCallbackID: "callback-1",
				StreamID:       "plugin-stream-caller-scope-fallback",
			}
			reads := []pluginapi.HostModelStreamReadResponse{
				{Payload: []byte("data: {\"model\":\"fallback-target\"}\n\n")},
				{Payload: []byte("data: [DONE]\n\n")},
				{Done: true},
			}
			var forwarded pluginapi.HostModelExecutionRequest
			emitted, closedHost, closedPlugin, _, err := runExecutorStreamTestWithForwarded(req, reads, &forwarded)
			if err != nil {
				t.Fatalf("handleExecutorExecuteStream error = %v", err)
			}
			if forwarded.Model != "fallback-target" {
				t.Fatalf("forwarded model=%q, want fallback-target", forwarded.Model)
			}
			var body struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(forwarded.Body, &body); err != nil {
				t.Fatalf("unmarshal forwarded body: %v", err)
			}
			if body.Model != "fallback-target" {
				t.Fatalf("forwarded body model=%q, want fallback-target", body.Model)
			}
			joined := strings.Join(emitted, "")
			if !strings.Contains(joined, `"model":"client-model"`) || strings.Contains(joined, "fallback-target") || strings.Contains(joined, "scoped-target") || !strings.Contains(joined, "data: [DONE]") {
				t.Fatalf("emitted=%q", joined)
			}
			if !closedHost || !closedPlugin {
				t.Fatalf("closedHost=%v closedPlugin=%v", closedHost, closedPlugin)
			}
		})
	}
}

func TestHandleExecutorExecuteStreamStartsForwarderAndRestoresChunks(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "deepseek-v4-pro=>gpt-5.4-mini"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "deepseek-v4-pro",
			Format:          "openai",
			SourceFormat:    "openai",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"deepseek-v4-pro","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte("data: {\"model\":\"gpt-5.4-mini\"}\n\n")},
		{Payload: []byte("data: [DONE]\n\n")},
		{Done: true},
	}
	emitted, closedHost, closedPlugin, respRaw, err := runExecutorStreamTest(req, reads)
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	var resp struct {
		Headers http.Header `json:"headers"`
	}
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode stream response: %v", err)
	}
	if resp.Headers.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("headers=%v, want text/event-stream", resp.Headers)
	}
	joined := strings.Join(emitted, "")
	if !strings.Contains(joined, `"model":"deepseek-v4-pro"`) || strings.Contains(joined, `"model":"gpt-5.4-mini"`) || !strings.Contains(joined, "data: [DONE]") {
		t.Fatalf("emitted=%q", joined)
	}
	if !closedHost || !closedPlugin {
		t.Fatalf("closedHost=%v closedPlugin=%v", closedHost, closedPlugin)
	}
}

func TestHandleExecutorExecuteStreamBuffersSplitSSEPrefix(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "deepseek-v4-pro=>gpt-5.4-mini"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "deepseek-v4-pro",
			Format:          "openai",
			SourceFormat:    "openai",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"deepseek-v4-pro","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-split-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte("da")},
		{Payload: []byte("ta: {\"model\":\"gpt-5.4-mini\"}\n\n")},
		{Done: true},
	}
	emitted, _, _, _, err := runExecutorStreamTest(req, reads)
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	for _, chunk := range emitted {
		if chunk == "da" {
			t.Fatalf("emitted raw split prefix = %q", emitted)
		}
	}
	joined := strings.Join(emitted, "")
	if !strings.Contains(joined, `"model":"deepseek-v4-pro"`) || strings.Contains(joined, `"model":"gpt-5.4-mini"`) {
		t.Fatalf("emitted=%q", joined)
	}
}

func TestHandleExecutorExecuteStreamRestoresRawJSONWebSocketLikeChunks(t *testing.T) {
	setLoadedConfigForTest(Config{CodexResponsesRules: "deepseek-v4-pro=>gpt-5.4-mini"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "deepseek-v4-pro",
			Format:          "openai-response",
			SourceFormat:    "openai-response",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"deepseek-v4-pro","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-raw-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte(`{"type":"response.completed","model":"gpt-5.4-mini"}`)},
		{Done: true},
	}
	emitted, _, _, _, err := runExecutorStreamTest(req, reads)
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	joined := strings.Join(emitted, "")
	if !strings.Contains(joined, `"model":"deepseek-v4-pro"`) || strings.Contains(joined, `"model":"gpt-5.4-mini"`) {
		t.Fatalf("emitted=%q", joined)
	}
}

func TestHandleExecutorExecuteStreamRestoresRawJSONNestedResponseModel(t *testing.T) {
	setLoadedConfigForTest(Config{CodexResponsesRules: "codex-ws*=>deepseek-v4-flash"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "codex-ws-client",
			Format:          "openai-response",
			SourceFormat:    "openai-response",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"codex-ws-client","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-raw-nested-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte(`{"type":"response.completed","response":{"model":"deepseek-v4-flash","output":[]}}`)},
		{Done: true},
	}
	emitted, _, _, _, err := runExecutorStreamTestWithHostContentType(req, reads, "application/json")
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	joined := strings.Join(emitted, "")
	if !strings.Contains(joined, `"response":{"model":"codex-ws-client"`) || strings.Contains(joined, `deepseek-v4-flash`) || strings.Contains(joined, "data: ") {
		t.Fatalf("emitted=%q", joined)
	}
}

func TestHandleExecutorExecuteStreamRestoresLineDelimitedRawJSONEvents(t *testing.T) {
	setLoadedConfigForTest(Config{CodexResponsesRules: "codex-ws*=>deepseek-v4-flash"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "codex-ws-client",
			Format:          "openai-response",
			SourceFormat:    "openai-response",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"codex-ws-client","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-raw-lines-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte(`{"type":"response.created","response":{"id":"r1"}}` + "\n" + `{"type":"response.completed","response":{"model":"deepseek-v4-flash","output":[]}}`)},
		{Done: true},
	}
	emitted, _, _, _, err := runExecutorStreamTest(req, reads)
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	joined := strings.Join(emitted, "")
	if !strings.Contains(joined, `"response":{"model":"codex-ws-client"`) || strings.Contains(joined, `deepseek-v4-flash`) {
		t.Fatalf("emitted=%q", joined)
	}
	if strings.Count(joined, "data: ") < 2 {
		t.Fatalf("emitted=%q, want each raw JSON event framed for Responses SSE", joined)
	}
}

func TestHandleExecutorExecuteStreamRestoresLineDelimitedRawJSONForWebSocket(t *testing.T) {
	setLoadedConfigForTest(Config{CodexResponsesRules: "codex-ws*=>deepseek-v4-flash"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "codex-ws-client",
			Format:          "openai-response",
			SourceFormat:    "openai-response",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"codex-ws-client","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-ws-raw-lines-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte(`{"type":"response.created","response":{"id":"r1"}}` + "\n" + `{"type":"response.completed","response":{"model":"deepseek-v4-flash","output":[]}}`)},
		{Done: true},
	}
	emitted, _, _, _, err := runExecutorStreamTestWithHostContentType(req, reads, "application/json")
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	joined := strings.Join(emitted, "")
	if !strings.Contains(joined, `"response":{"model":"codex-ws-client"`) || strings.Contains(joined, `deepseek-v4-flash`) || strings.Contains(joined, "data: ") {
		t.Fatalf("emitted=%q", joined)
	}
}

func TestHandleExecutorExecuteStreamRestoresSpaceDelimitedRawJSONForWebSocket(t *testing.T) {
	setLoadedConfigForTest(Config{CodexResponsesRules: "codex-ws*=>deepseek-v4-flash"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "codex-ws-client",
			Format:          "openai-response",
			SourceFormat:    "openai-response",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"codex-ws-client","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-ws-raw-space-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte(`{"type":"response.created","response":{"id":"r1"}} ` + `{"type":"response.completed","response":{"model":"deepseek-v4-flash","output":[]}}`)},
		{Done: true},
	}
	emitted, _, _, _, err := runExecutorStreamTestWithHostContentType(req, reads, "application/json")
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	joined := strings.Join(emitted, "")
	if !strings.Contains(joined, `"response":{"model":"codex-ws-client"`) || strings.Contains(joined, `deepseek-v4-flash`) || strings.Contains(joined, "data: ") {
		t.Fatalf("emitted=%q", joined)
	}
}

func TestHandleExecutorExecuteStreamFlushesUnterminatedSSEDataForWebSocket(t *testing.T) {
	setLoadedConfigForTest(Config{CodexResponsesRules: "codex-ws*=>deepseek-v4-flash"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "codex-ws-client",
			Format:          "openai-response",
			SourceFormat:    "openai-response",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"codex-ws-client","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-ws-sse-flush-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte(`data: {"type":"response.completed","response":{"model":"deepseek-v4-flash","output":[]}}`)},
		{Done: true},
	}
	emitted, _, _, _, err := runExecutorStreamTestWithHostContentType(req, reads, "text/event-stream")
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	joined := strings.Join(emitted, "")
	if !strings.Contains(joined, `"response":{"model":"codex-ws-client"`) || strings.Contains(joined, `deepseek-v4-flash`) {
		t.Fatalf("emitted=%q", joined)
	}
}

func TestStreamChunkRewriterDoesNotBufferRawJSONStartingWithEvent(t *testing.T) {
	rewriter := newStreamChunkRewriter("gpt-5.5-openai-compact")
	chunks, err := rewriter.Write([]byte(`{"event":"response.completed","model":"gpt-5.5"}`))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks=%q, want one raw JSON chunk", chunks)
	}
	got := string(chunks[0])
	if !strings.Contains(got, `"model":"gpt-5.5-openai-compact"`) || strings.Contains(got, `"model":"gpt-5.5"`) {
		t.Fatalf("chunk=%q", got)
	}
}

func TestStreamChunkRewriterRawJSONUsesOneRestorePass(t *testing.T) {
	item := `{"type":"output_text","text":"opaque"}`
	body := []byte(`{"type":"response.completed","model":"upstream","output":[` + strings.TrimSuffix(strings.Repeat(item+",", 128), ",") + `]}`)
	viaWrite := newStreamChunkRewriter("client")
	viaRaw := newStreamChunkRewriter("client")
	writeAllocs := testing.AllocsPerRun(50, func() {
		chunks, err := viaWrite.Write(body)
		if err != nil || len(chunks) != 1 {
			panic(fmt.Sprintf("Write=(%d,%v)", len(chunks), err))
		}
	})
	rawAllocs := testing.AllocsPerRun(50, func() {
		chunks, err := viaRaw.rawJSONChunks(body)
		if err != nil || len(chunks) != 1 {
			panic(fmt.Sprintf("rawJSONChunks=(%d,%v)", len(chunks), err))
		}
	})
	if writeAllocs > rawAllocs+4 {
		t.Fatalf("Write allocations=%v, rawJSONChunks allocations=%v; raw JSON was restored more than once", writeAllocs, rawAllocs)
	}
}

func legacyRawJSONChunksForTest(r *streamChunkRewriter, p []byte) ([][]byte, error) {
	values, ok := splitJSONValues(p)
	if !ok {
		return [][]byte{bytes.Clone(p)}, nil
	}
	if len(values) == 0 {
		return nil, nil
	}
	out := make([][]byte, 0, len(values))
	for _, value := range values {
		restored, _, err := r.sse.restoreResponseModel(value)
		if err != nil {
			return nil, err
		}
		if r.frameRawJSONAsSSE {
			out = append(out, frameSSEData(restored))
		} else {
			out = append(out, restored)
		}
	}
	return out, nil
}

func TestRawJSONSingleValueFastPathMatchesLegacy(t *testing.T) {
	backslash := string(rune(92))
	inputs := [][]byte{
		nil,
		[]byte(" \r\n\t "),
		[]byte("not-json"),
		[]byte(`{}`),
		[]byte("  {\"id\":\"r1\"} \r\n"),
		[]byte(`{"model":"upstream"}`),
		[]byte(`{"response":{"model":"upstream"}}`),
		[]byte(`{"` + backslash + `u006dodel":"upstream"}`),
		[]byte("{\"model\":\"upstream\"}\n{\"model\":\"other\"}"),
		[]byte("null true 123 \"text\""),
		[]byte(`{"valid":true} trailing`),
		[]byte{0xc2, 0xa0},
	}
	for _, framed := range []bool{false, true} {
		for _, input := range inputs {
			r := newStreamChunkRewriter("client")
			r.frameRawJSONAsSSE = framed
			got, gotErr := r.rawJSONChunks(input)
			want, wantErr := legacyRawJSONChunksForTest(r, input)
			if !reflect.DeepEqual(got, want) || (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("framed=%v input=%q\ngot=%q err=%v\nwant=%q err=%v", framed, input, got, gotErr, want, wantErr)
			}
		}
	}
}

func TestRawJSONSingleValueFastPathAllocatesLessThanLegacy(t *testing.T) {
	body := []byte(`{"type":"response.completed","model":"upstream","output":"` + strings.Repeat("x", 64<<10) + `"}`)
	production := newStreamChunkRewriter("client")
	reference := newStreamChunkRewriter("client")
	productionAllocs := testing.AllocsPerRun(50, func() {
		chunks, err := production.rawJSONChunks(body)
		if err != nil || len(chunks) != 1 {
			panic(fmt.Sprintf("production=(%d,%v)", len(chunks), err))
		}
	})
	referenceAllocs := testing.AllocsPerRun(50, func() {
		chunks, err := legacyRawJSONChunksForTest(reference, body)
		if err != nil || len(chunks) != 1 {
			panic(fmt.Sprintf("reference=(%d,%v)", len(chunks), err))
		}
	})
	if production.sse.encodedModel == nil || reference.sse.encodedModel == nil {
		t.Fatalf("encoded model cache state differs: production=%v reference=%v", production.sse.encodedModel != nil, reference.sse.encodedModel != nil)
	}
	if productionAllocs >= referenceAllocs {
		t.Fatalf("production allocations=%v, legacy allocations=%v", productionAllocs, referenceAllocs)
	}
}

func BenchmarkStreamChunkRewriterSingleJSON(b *testing.B) {
	body := []byte(`{"type":"response.completed","model":"upstream","output":"` + strings.Repeat("x", 64<<10) + `"}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for i := 0; i < b.N; i++ {
		r := newStreamChunkRewriter("client")
		chunks, err := r.Write(body)
		if err != nil || len(chunks) != 1 {
			b.Fatalf("Write=(%d,%v)", len(chunks), err)
		}
	}
}

func BenchmarkStreamChunkRewriterLateInvalidJSON(b *testing.B) {
	body := []byte(`{"model":"` + strings.Repeat("x", 64<<10))
	r := newStreamChunkRewriter("client")
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for i := 0; i < b.N; i++ {
		chunks, err := r.rawJSONChunks(body)
		if err != nil || len(chunks) != 1 || !bytes.Equal(chunks[0], body) {
			b.Fatalf("rawJSONChunks=(%q,%v), want one unchanged chunk", chunks, err)
		}
	}
}

func TestSplitJSONValuesKeepsDecoderOwnedPayload(t *testing.T) {
	input := []byte(strings.Repeat(`{"type":"event","value":1}`+"\n", 64))
	reference := func() {
		dec := json.NewDecoder(bytes.NewReader(input))
		values := make([][]byte, 0, 1)
		for {
			var raw json.RawMessage
			err := dec.Decode(&raw)
			if err != nil {
				break
			}
			values = append(values, raw)
		}
		if len(values) != 64 {
			panic(fmt.Sprintf("reference values=%d", len(values)))
		}
	}
	productionAllocs := testing.AllocsPerRun(50, func() {
		values, ok := splitJSONValues(input)
		if !ok || len(values) != 64 {
			panic(fmt.Sprintf("splitJSONValues=(%d,%v)", len(values), ok))
		}
	})
	referenceAllocs := testing.AllocsPerRun(50, reference)
	if productionAllocs > referenceAllocs+2 {
		t.Fatalf("splitJSONValues allocations=%v, decoder reference allocations=%v", productionAllocs, referenceAllocs)
	}
}

func TestHandleExecutorExecuteStreamRestoresKnownSSEModelFields(t *testing.T) {
	setLoadedConfigForTest(Config{ClaudeMessagesRules: "claude-*=>gpt-5.5"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "claude-opus-4",
			Format:          "claude",
			SourceFormat:    "claude",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"claude-opus-4","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-known-fields-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte(`data: {"model":"gpt-5.5","modelVersion":"gpt-5.5","message":{"model":"gpt-5.5"},"response":{"model":"gpt-5.5","modelVersion":"gpt-5.5"},"content":[{"text":"gpt-5.5 should stay in content"}]}` + "\n\n")},
		{Done: true},
	}
	emitted, _, _, _, err := runExecutorStreamTest(req, reads)
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	joined := strings.Join(emitted, "")
	for _, want := range []string{`"model":"claude-opus-4"`, `"modelVersion":"claude-opus-4"`, `"message":{"model":"claude-opus-4"}`, `"response":{"model":"claude-opus-4","modelVersion":"claude-opus-4"}`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("emitted=%q missing %s", joined, want)
		}
	}
	if !strings.Contains(joined, `gpt-5.5 should stay in content`) {
		t.Fatalf("emitted=%q, content text should not be rewritten", joined)
	}
}

func TestHandleExecutorExecuteStreamFramesRawResponsesJSONAsSSE(t *testing.T) {
	setLoadedConfigForTest(Config{CodexResponsesRules: "gpt-*-openai-compact=>gpt-$1"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "gpt-5.5-openai-compact",
			Format:          "openai-response",
			SourceFormat:    "openai-response",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"gpt-5.5-openai-compact","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-responses-sse-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte(`{"type":"response.completed","model":"gpt-5.5"}`)},
		{Done: true},
	}
	emitted, _, _, _, err := runExecutorStreamTest(req, reads)
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	joined := strings.Join(emitted, "")
	if !strings.Contains(joined, "data: ") || !strings.Contains(joined, `"type":"response.completed"`) || !strings.Contains(joined, `"model":"gpt-5.5-openai-compact"`) || strings.Contains(joined, `"model":"gpt-5.5"`) {
		t.Fatalf("emitted=%q", joined)
	}
}

func TestHandleExecutorExecuteStreamClosesWebSocketLikeRawJSONError(t *testing.T) {
	setLoadedConfigForTest(Config{CodexResponsesRules: "gpt-*-openai-compact=>gpt-$1"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "gpt-none-openai-compact",
			Format:          "openai-response",
			SourceFormat:    "openai-response",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"gpt-none-openai-compact","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-ws-error-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{{Error: "model not found"}}
	emitted, closedHost, closedPlugin, _, err := runExecutorStreamTest(req, reads)
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	if !closedHost || !closedPlugin {
		t.Fatalf("closedHost=%v closedPlugin=%v", closedHost, closedPlugin)
	}
	if len(emitted) != 0 {
		t.Fatalf("emitted=%q, want no payload before websocket-like stream error", emitted)
	}
}

func TestHandleExecutorExecuteStreamClosesPluginOnChunkErrorAfterPendingPrefix(t *testing.T) {
	setLoadedConfigForTest(Config{CodexResponsesRules: "gpt-*-openai-compact=>gpt-$1"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "gpt-none-openai-compact",
			Format:          "openai-response",
			SourceFormat:    "openai-response",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"gpt-none-openai-compact","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-error-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte("event")},
		{Error: "model not found"},
	}
	emitted, closedHost, closedPlugin, _, err := runExecutorStreamTest(req, reads)
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	if !closedHost || !closedPlugin {
		t.Fatalf("closedHost=%v closedPlugin=%v", closedHost, closedPlugin)
	}
	if strings.Join(emitted, "") != "event" {
		t.Fatalf("emitted=%q, want pending bytes flushed before error close", emitted)
	}
}

func runExecutorStreamTest(req rpcExecutorRequest, reads []pluginapi.HostModelStreamReadResponse) ([]string, bool, bool, []byte, error) {
	return runExecutorStreamTestWithHostContentTypeAndForwarded(req, reads, "text/event-stream", nil)
}

func runExecutorStreamTestWithForwarded(req rpcExecutorRequest, reads []pluginapi.HostModelStreamReadResponse, forwarded *pluginapi.HostModelExecutionRequest) ([]string, bool, bool, []byte, error) {
	return runExecutorStreamTestWithHostContentTypeAndForwarded(req, reads, "text/event-stream", forwarded)
}

func runExecutorStreamTestWithHostContentType(req rpcExecutorRequest, reads []pluginapi.HostModelStreamReadResponse, contentType string) ([]string, bool, bool, []byte, error) {
	return runExecutorStreamTestWithHostContentTypeAndForwarded(req, reads, contentType, nil)
}

func runExecutorStreamTestWithHostContentTypeAndForwarded(req rpcExecutorRequest, reads []pluginapi.HostModelStreamReadResponse, contentType string, forwarded *pluginapi.HostModelExecutionRequest) ([]string, bool, bool, []byte, error) {
	var emitted []string
	closedHost := false
	closedPlugin := false
	done := make(chan struct{})
	fakeHost := func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostModelExecuteStream:
			if forwarded != nil {
				raw, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(raw, forwarded); err != nil {
					return nil, err
				}
			}
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, Headers: http.Header{"Content-Type": []string{contentType}}, StreamID: "host-stream-1"})
		case pluginabi.MethodHostModelStreamRead:
			if len(reads) == 0 {
				return nil, fmt.Errorf("unexpected extra stream read")
			}
			next := reads[0]
			reads = reads[1:]
			return json.Marshal(next)
		case pluginabi.MethodHostStreamEmit:
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var emit struct {
				StreamID string `json:"stream_id"`
				Payload  []byte `json:"payload"`
				Error    string `json:"error"`
			}
			if err := json.Unmarshal(raw, &emit); err != nil {
				return nil, err
			}
			emitted = append(emitted, string(emit.Payload))
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostModelStreamClose:
			closedHost = true
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostStreamClose:
			closedPlugin = true
			close(done)
			return json.Marshal(map[string]any{})
		default:
			return nil, fmt.Errorf("unexpected method %q", method)
		}
	}
	rawReq, err := json.Marshal(req)
	if err != nil {
		return nil, false, false, nil, err
	}
	respRaw, err := handleExecutorExecuteStream(rawReq, fakeHost)
	if err != nil {
		return nil, false, false, nil, err
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		return nil, false, false, nil, fmt.Errorf("stream forwarder did not close plugin stream")
	}
	return emitted, closedHost, closedPlugin, respRaw, nil
}

func TestWrapEnvelopeAvoidsPayloadSizedIntermediate(t *testing.T) {
	payload := []byte(`{"data":"` + strings.Repeat("x", 256<<10) + `"}`)
	production := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := wrapEnvelope(payload, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	reference := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := json.Marshal(pluginabi.Envelope{OK: true, Result: json.RawMessage(payload)}); err != nil {
				b.Fatal(err)
			}
		}
	})
	if got, limit := production.AllocedBytesPerOp(), reference.AllocedBytesPerOp()+int64(len(payload))/4; got > limit {
		t.Fatalf("wrapEnvelope bytes/op=%d, direct envelope bytes/op=%d", got, reference.AllocedBytesPerOp())
	}

	empty, err := wrapEnvelope(nil, nil)
	if err != nil {
		t.Fatalf("wrap empty envelope: %v", err)
	}
	if string(empty) != `{"ok":true,"result":null}` {
		t.Fatalf("empty envelope=%s, want explicit null result", empty)
	}
}

func TestCallHostReturnsDecoderOwnedResult(t *testing.T) {
	resultPayload := json.RawMessage(`{"data":"` + strings.Repeat("x", 256<<10) + `"}`)
	responseBytes, err := json.Marshal(pluginabi.Envelope{OK: true, Result: resultPayload})
	if err != nil {
		t.Fatalf("marshal host response: %v", err)
	}
	callback := func(string, []byte) ([]byte, error) {
		return responseBytes, nil
	}
	setHostCallbackForTest(callback)
	defer setHostCallbackForTest(nil)
	requestPayload := map[string]string{"request": "x"}

	production := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := callHost("test.method", requestPayload); err != nil {
				b.Fatal(err)
			}
		}
	})
	reference := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := json.Marshal(requestPayload); err != nil {
				b.Fatal(err)
			}
			var env pluginabi.Envelope
			if err := json.Unmarshal(responseBytes, &env); err != nil {
				b.Fatal(err)
			}
		}
	})
	if got, limit := production.AllocedBytesPerOp(), reference.AllocedBytesPerOp()+int64(len(resultPayload))/4; got > limit {
		t.Fatalf("callHost bytes/op=%d, single-decode reference bytes/op=%d", got, reference.AllocedBytesPerOp())
	}
}

func TestHandleMethodDispatchesRegisterReconfigureAndUnknown(t *testing.T) {
	registerRaw, err := handleMethod(pluginabi.MethodPluginRegister, nil)
	if err != nil {
		t.Fatalf("handle register error = %v", err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(registerRaw, &env); err != nil {
		t.Fatalf("decode register envelope: %v", err)
	}
	if !env.OK || len(env.Result) == 0 {
		t.Fatalf("register envelope=%#v", env)
	}
	var reg registration
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatalf("decode register result: %v", err)
	}
	if reg.Metadata.Name != "model-mapper" {
		t.Fatalf("registration=%#v", reg)
	}

	identifierRaw, err := handleMethod(pluginabi.MethodExecutorIdentifier, nil)
	if err != nil {
		t.Fatalf("handle identifier error = %v", err)
	}
	var identifierEnv pluginabi.Envelope
	if err := json.Unmarshal(identifierRaw, &identifierEnv); err != nil {
		t.Fatalf("decode identifier env: %v", err)
	}
	if !identifierEnv.OK || !bytes.Contains(identifierEnv.Result, []byte(`"identifier":"model-mapper"`)) {
		t.Fatalf("identifier env=%s", identifierRaw)
	}

	reconfigureRaw, err := handleMethod(pluginabi.MethodPluginReconfigure, []byte(`{"config_yaml":"ZW5hYmxlZDogdHJ1ZQpnbG9iYWxfcnVsZXM6IGE9PmIK"}`))
	if err != nil {
		t.Fatalf("handle reconfigure error = %v", err)
	}
	if err := json.Unmarshal(reconfigureRaw, &env); err != nil {
		t.Fatalf("decode reconfigure envelope: %v", err)
	}
	if !env.OK || len(env.Result) == 0 {
		t.Fatalf("reconfigure envelope=%#v", env)
	}
	decision, err := routeModel(loadedConfig(), "openai", "a", "", "")
	if err != nil {
		t.Fatalf("route after reconfigure: %v", err)
	}
	if !decision.Handled || decision.UpstreamModel != "b" {
		t.Fatalf("decision after reconfigure=%#v", decision)
	}

	unknownRaw, err := handleMethod("unknown.method", nil)
	if err != nil {
		t.Fatalf("handle unknown returned Go error = %v", err)
	}
	if err := json.Unmarshal(unknownRaw, &env); err != nil {
		t.Fatalf("decode unknown envelope: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code != "unknown_method" {
		t.Fatalf("unknown envelope=%#v", env)
	}
}

func TestHandleMethodCountTokensUnsupportedWithoutPanic(t *testing.T) {
	raw, err := handleMethod(pluginabi.MethodExecutorCountTokens, nil)
	if err != nil {
		t.Fatalf("count tokens Go error = %v", err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode count tokens envelope: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code != "unsupported" {
		t.Fatalf("count tokens envelope=%#v", env)
	}
}
