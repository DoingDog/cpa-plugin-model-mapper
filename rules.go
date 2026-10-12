package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type token struct {
	literal string
	capture int
}

type caseOperation uint8

const (
	caseOperationNone caseOperation = iota
	caseOperationLower
	caseOperationUpper
)

type rule struct {
	callerScope       string
	callerPattern     []token
	callerPatternText string
	excludeCaller     bool
	patternTokens     []token
	replacementTokens []token
	captureCount      int
	caseOperation     caseOperation
}

func parseRules(raw string) ([]rule, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty rules")
	}
	parts := splitRuleEntries(raw)
	out := make([]rule, 0, len(parts))
	for _, part := range parts {
		if hasUnescapedCommentMarker(part) {
			continue
		}
		if part == "" {
			return nil, fmt.Errorf("invalid rules")
		}
		for _, r := range part {
			if unicode.IsSpace(r) || r == '"' || r == '\'' {
				return nil, fmt.Errorf("invalid character")
			}
		}
		excludeCaller := strings.HasPrefix(part, "#")
		if excludeCaller {
			part = part[1:]
		}
		scopeParts, err := splitEscaped(part, '#')
		if err != nil || len(scopeParts) > 2 || excludeCaller && len(scopeParts) != 2 {
			return nil, fmt.Errorf("invalid rule")
		}
		body := scopeParts[0]
		scope := ""
		var scopePattern []token
		scopePatternText := ""
		if len(scopeParts) == 2 {
			scopeTokens, wildcards, err := parseFind(scopeParts[0])
			if err != nil || len(scopeTokens) == 0 {
				return nil, fmt.Errorf("invalid API key scope")
			}
			if wildcards == 0 {
				scope = callerScope(scopeTokens[0].literal)
			} else {
				scopePattern = scopeTokens
				scopePatternText = scopeParts[0]
			}
			body = scopeParts[1]
		}

		scopeRule := rule{callerScope: scope, callerPattern: scopePattern, callerPatternText: scopePatternText, excludeCaller: excludeCaller}
		switch body {
		case `\a`:
			scopeRule.caseOperation = caseOperationLower
			out = append(out, scopeRule)
			continue
		case `\A`:
			scopeRule.caseOperation = caseOperationUpper
			out = append(out, scopeRule)
			continue
		}
		sep, ok := findRuleSeparator(body)
		if !ok {
			return nil, fmt.Errorf("invalid rule")
		}
		find, replace := body[:sep], body[sep+2:]
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
		scopeRule.patternTokens = pt
		scopeRule.replacementTokens = rt
		scopeRule.captureCount = captures
		out = append(out, scopeRule)
	}
	return out, nil
}

func splitRuleEntries(raw string) []string {
	entries := make([]string, 0, 1+strings.Count(raw, ";"))
	start := 0
	backslashes := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' {
			backslashes++
			continue
		}
		if raw[i] == ';' && backslashes%2 == 0 {
			entries = append(entries, raw[start:i])
			start = i + 1
		}
		backslashes = 0
	}
	return append(entries, raw[start:])
}

func hasUnescapedCommentMarker(entry string) bool {
	backslashes := 0
	for i := 0; i < len(entry); i++ {
		if entry[i] == '\\' {
			backslashes++
			continue
		}
		if entry[i] == '!' && backslashes%2 == 0 {
			return true
		}
		backslashes = 0
	}
	return false
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
			case '*', ';', '$', '#', '!', '\\':
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
			if i+1 < len(s) && (s[i+1] == '#' || s[i+1] == '!') {
				lit.WriteByte(s[i+1])
				i++
				continue
			}
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
		n, err := strconv.Atoi(s[i+1 : j])
		if err != nil || n < 1 || n > captures {
			return nil, fmt.Errorf("invalid reference")
		}
		flush()
		tokens = append(tokens, token{capture: n})
		i = j - 1
	}
	flush()
	return tokens, nil
}

func applyASCIIModelCase(model string, operation caseOperation) string {
	needsChange := false
	for i := 0; i < len(model); i++ {
		c := model[i]
		if operation == caseOperationLower && c >= 'A' && c <= 'Z' ||
			operation == caseOperationUpper && c >= 'a' && c <= 'z' {
			needsChange = true
			break
		}
	}
	if !needsChange {
		return model
	}

	converted := []byte(model)
	for i, c := range converted {
		switch operation {
		case caseOperationLower:
			if c >= 'A' && c <= 'Z' {
				converted[i] = c + ('a' - 'A')
			}
		case caseOperationUpper:
			if c >= 'a' && c <= 'z' {
				converted[i] = c - ('a' - 'A')
			}
		}
	}
	return string(converted)
}

func callerMatchesRule(r *rule, scope, key string) bool {
	if r.callerScope == "" && len(r.callerPattern) == 0 {
		return true
	}
	if scope == "" {
		return false
	}
	matched := r.callerScope == scope
	if len(r.callerPattern) > 0 {
		var ok bool
		matched, ok = callerPatternMatch(r, scope, key)
		if !ok {
			return false
		}
	}
	return matched != r.excludeCaller
}

func applyRules(model, scope, key string, rules []rule) (string, bool, error) {
	current := model
	matchedAny := false
	for i := range rules {
		r := &rules[i]
		if !callerMatchesRule(r, scope, key) {
			continue
		}
		if r.caseOperation != caseOperationNone {
			current = applyASCIIModelCase(current, r.caseOperation)
			matchedAny = true
		} else {
			captures, ok := matchTokens(current, r.patternTokens)
			if !ok {
				continue
			}
			current = buildReplacement(r.replacementTokens, captures)
			matchedAny = true
		}
		if current == "" {
			return "", true, fmt.Errorf("empty mapped model")
		}
	}
	return current, matchedAny, nil
}

func matchTokens(s string, tokens []token) ([]string, bool) {
	var captures []string
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
		if captures == nil {
			captures = make([]string, 0, len(tokens))
		}
		captures = append(captures, s[pos:end])
		pos = end
	}
	return captures, pos == len(s)
}

func buildReplacement(tokens []token, captures []string) string {
	if len(tokens) == 1 && tokens[0].literal != "" {
		return tokens[0].literal
	}
	size := 0
	for _, tok := range tokens {
		if tok.literal != "" {
			size += len(tok.literal)
		} else {
			size += len(captures[tok.capture-1])
		}
	}
	var b strings.Builder
	b.Grow(size)
	for _, tok := range tokens {
		if tok.literal != "" {
			b.WriteString(tok.literal)
			continue
		}
		b.WriteString(captures[tok.capture-1])
	}
	return b.String()
}
