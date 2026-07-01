package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

func main() {}

type Config struct {
	Enabled                bool   `json:"enabled"`
	GlobalRules            string `json:"global_rules"`
	ClaudeMessagesRules    string `json:"claude_messages_rules"`
	CodexResponsesRules    string `json:"codex_responses_rules"`
	OpenAICompletionsRules string `json:"openai_completions_rules"`
}

type routeDecision struct {
	Handled       bool
	OriginalModel string
	UpstreamModel string
}

func selectRules(cfg Config, format string) (string, bool) {
	switch format {
	case "claude":
		if cfg.ClaudeMessagesRules != "" {
			return cfg.ClaudeMessagesRules, true
		}
	case "openai-response":
		if cfg.CodexResponsesRules != "" {
			return cfg.CodexResponsesRules, true
		}
	case "openai":
		if cfg.OpenAICompletionsRules != "" {
			return cfg.OpenAICompletionsRules, true
		}
	}
	if cfg.GlobalRules != "" {
		return cfg.GlobalRules, true
	}
	return "", false
}

func routeModel(cfg Config, format string, model string) (routeDecision, error) {
	if !cfg.Enabled {
		return routeDecision{}, nil
	}
	raw, ok := selectRules(cfg, format)
	if !ok {
		return routeDecision{}, nil
	}
	rules, err := parseRules(raw)
	if err != nil {
		return routeDecision{}, err
	}
	mapped, matched, err := applyRules(model, rules)
	if err != nil {
		return routeDecision{}, err
	}
	if !matched || mapped == model {
		return routeDecision{}, nil
	}
	return routeDecision{Handled: true, OriginalModel: model, UpstreamModel: mapped}, nil
}

func rewriteRequestModel(body []byte, upstreamModel string) ([]byte, bool, error) {
	return rewriteTopLevelModel(body, upstreamModel)
}

func restoreResponseModel(body []byte, originalModel string) ([]byte, bool, error) {
	return rewriteTopLevelModel(body, originalModel)
}

func rewriteTopLevelModel(body []byte, model string) ([]byte, bool, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return append([]byte(nil), body...), false, nil
	}
	current, ok := doc["model"]
	if !ok {
		return append([]byte(nil), body...), false, nil
	}
	if _, ok := current.(string); !ok {
		return append([]byte(nil), body...), false, nil
	}
	doc["model"] = model
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

type token struct {
	literal string
	capture int
}

type rule struct {
	patternTokens     []token
	replacementTokens []token
	captureCount      int
}

func defaultConfig() Config {
	return Config{Enabled: true}
}

func parseRules(raw string) ([]rule, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty rules")
	}
	for _, r := range raw {
		if unicode.IsSpace(r) || r == '"' || r == '\'' {
			return nil, fmt.Errorf("invalid character")
		}
	}

	parts, err := splitEscaped(raw, ';')
	if err != nil || len(parts) == 0 {
		return nil, fmt.Errorf("invalid rules")
	}
	out := make([]rule, 0, len(parts))
	for _, part := range parts {
		sep, ok := findRuleSeparator(part)
		if !ok {
			return nil, fmt.Errorf("invalid rule")
		}
		find, replace := part[:sep], part[sep+2:]
		if find == "" || replace == "" {
			return nil, fmt.Errorf("invalid rule")
		}
		pt, captures, err := parseFind(find)
		if err != nil {
			return nil, err
		}
		rt, err := parseReplace(replace, captures)
		if err != nil {
			return nil, err
		}
		out = append(out, rule{patternTokens: pt, replacementTokens: rt, captureCount: captures})
	}
	return out, nil
}

func findRuleSeparator(s string) (int, bool) {
	escaped := false
	sep := -1
	for i := 0; i+1 < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '=' && s[i+1] == '>' {
			if sep >= 0 {
				return -1, false
			}
			sep = i
		}
	}
	return sep, sep >= 0
}

func splitEscaped(s string, sep byte) ([]string, error) {
	var parts []string
	start := 0
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == sep {
			if i == start {
				return nil, fmt.Errorf("empty segment")
			}
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	if escaped {
		return nil, fmt.Errorf("dangling escape")
	}
	if start >= len(s) {
		return nil, fmt.Errorf("empty segment")
	}
	parts = append(parts, s[start:])
	return parts, nil
}

func parseFind(s string) ([]token, int, error) {
	var tokens []token
	lit := strings.Builder{}
	captures := 0
	flush := func() {
		if lit.Len() > 0 {
			tokens = append(tokens, token{literal: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			if i+1 >= len(s) {
				return nil, 0, fmt.Errorf("dangling escape")
			}
			n := s[i+1]
			switch n {
			case '*', ';', '$', '\\':
				lit.WriteByte(n)
				i++
			case '=':
				if i+2 < len(s) && s[i+2] == '>' {
					lit.WriteString("=>")
					i += 2
				} else {
					return nil, 0, fmt.Errorf("invalid escape")
				}
			default:
				return nil, 0, fmt.Errorf("invalid escape")
			}
			continue
		}
		if c == '*' {
			flush()
			captures++
			tokens = append(tokens, token{capture: captures})
			continue
		}
		lit.WriteByte(c)
	}
	flush()
	return tokens, captures, nil
}

func parseReplace(s string, captures int) ([]token, error) {
	var tokens []token
	lit := strings.Builder{}
	flush := func() {
		if lit.Len() > 0 {
			tokens = append(tokens, token{literal: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			if i+2 < len(s) && s[i+1] == '=' && s[i+2] == '>' {
				lit.WriteString("=>")
				i += 2
				continue
			}
			return nil, fmt.Errorf("invalid escape")
		}
		if c != '$' {
			lit.WriteByte(c)
			continue
		}
		if i+1 >= len(s) || s[i+1] < '1' || s[i+1] > '9' {
			return nil, fmt.Errorf("invalid reference")
		}
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		var n int
		for k := i + 1; k < j; k++ {
			n = n*10 + int(s[k]-'0')
		}
		if n == 0 || n > captures {
			return nil, fmt.Errorf("invalid reference")
		}
		flush()
		tokens = append(tokens, token{capture: n})
		i = j - 1
	}
	flush()
	return tokens, nil
}

func applyRules(model string, rules []rule) (string, bool, error) {
	current := model
	matchedAny := false
	for _, r := range rules {
		captures, ok := matchTokens(current, r.patternTokens)
		if !ok {
			continue
		}
		current = buildReplacement(r.replacementTokens, captures)
		matchedAny = true
		if current == "" {
			return "", true, fmt.Errorf("empty mapped model")
		}
	}
	return current, matchedAny, nil
}

func matchTokens(s string, tokens []token) ([]string, bool) {
	captures := make([]string, 0, len(tokens))
	pos := 0
	for i, tok := range tokens {
		if tok.literal != "" {
			if !strings.HasPrefix(s[pos:], tok.literal) {
				return nil, false
			}
			pos += len(tok.literal)
			continue
		}
		nextLit := ""
		for j := i + 1; j < len(tokens); j++ {
			if tokens[j].literal != "" {
				nextLit = tokens[j].literal
				break
			}
		}
		end := len(s)
		if nextLit != "" {
			idx := strings.Index(s[pos:], nextLit)
			if idx < 0 {
				return nil, false
			}
			end = pos + idx
		}
		captures = append(captures, s[pos:end])
		pos = end
	}
	return captures, pos == len(s)
}

func buildReplacement(tokens []token, captures []string) string {
	var b strings.Builder
	for _, tok := range tokens {
		if tok.literal != "" {
			b.WriteString(tok.literal)
			continue
		}
		b.WriteString(captures[tok.capture-1])
	}
	return b.String()
}
