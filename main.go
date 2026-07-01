package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func main() {}

type sseRewriter struct {
	originalModel string
	buf           []byte
}

func newSSERewriter(originalModel string) *sseRewriter {
	return &sseRewriter{originalModel: originalModel}
}

func (r *sseRewriter) Write(p []byte) ([][]byte, error) {
	r.buf = append(r.buf, p...)
	var out [][]byte
	for {
		delim, n := sseEventDelimiter(r.buf)
		if n == 0 {
			break
		}
		event := append([]byte(nil), r.buf[:delim]...)
		r.buf = r.buf[delim+n:]
		rewritten, err := r.rewriteEvent(event)
		if err != nil {
			return nil, err
		}
		out = append(out, rewritten...)
		out = append(out, r.delimiterBytes(n))
	}
	return out, nil
}

func (r *sseRewriter) Flush() ([][]byte, error) {
	if len(r.buf) == 0 {
		return nil, nil
	}
	out := [][]byte{append([]byte(nil), r.buf...)}
	r.buf = nil
	return out, nil
}

func (r *sseRewriter) rewriteEvent(event []byte) ([][]byte, error) {
	var out [][]byte
	for len(event) > 0 {
		lineEnd := bytes.IndexByte(event, '\n')
		line := event
		lineBreak := []byte(nil)
		if lineEnd >= 0 {
			line = event[:lineEnd]
			lineBreak = []byte("\n")
			event = event[lineEnd+1:]
		} else {
			event = nil
		}
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
			if len(lineBreak) > 0 {
				lineBreak = []byte("\r\n")
			}
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			value := bytes.TrimSpace(line[len("data:"):])
			if len(value) == 0 || bytes.Equal(value, []byte("[DONE]")) {
				out = append(out, append(append([]byte(nil), line...), lineBreak...))
				continue
			}
			restored, changed, err := restoreResponseModel(value, r.originalModel)
			if err != nil {
				return nil, err
			}
			if changed {
				out = append(out, append(append([]byte("data: "), restored...), lineBreak...))
				continue
			}
		}
		out = append(out, append(append([]byte(nil), line...), lineBreak...))
	}
	return out, nil
}

func sseEventDelimiter(buf []byte) (eventLen, delimLen int) {
	lf := bytes.Index(buf, []byte("\n\n"))
	crlf := bytes.Index(buf, []byte("\r\n\r\n"))
	if lf < 0 && crlf < 0 {
		return 0, 0
	}
	if lf >= 0 && (crlf < 0 || lf < crlf) {
		return lf, 2
	}
	return crlf, 4
}

func (r *sseRewriter) delimiterBytes(n int) []byte {
	if n == 4 {
		return []byte("\r\n\r\n")
	}
	return []byte("\n\n")
}

type Config struct {
	Enabled                bool   `json:"enabled"`
	GlobalRules            string `json:"global_rules"`
	ClaudeMessagesRules    string `json:"claude_messages_rules"`
	CodexResponsesRules    string `json:"codex_responses_rules"`
	OpenAICompletionsRules string `json:"openai_completions_rules"`
}

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
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name: "model-mapper",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "enabled", Type: pluginapi.ConfigFieldTypeBoolean},
				{Name: "global_rules", Type: pluginapi.ConfigFieldTypeString},
				{Name: "claude_messages_rules", Type: pluginapi.ConfigFieldTypeString},
				{Name: "codex_responses_rules", Type: pluginapi.ConfigFieldTypeString},
				{Name: "openai_completions_rules", Type: pluginapi.ConfigFieldTypeString},
			},
		},
		Capabilities: registrationCapabilities{
			ModelRouter:        true,
			Executor:           true,
			ExecutorModelScope: string(pluginapi.ExecutorModelScopeStatic),
		},
	}
}

func decodeConfig(raw json.RawMessage) (Config, error) {
	cfg := defaultConfig()
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("{}")) {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	for _, rules := range []string{cfg.GlobalRules, cfg.ClaudeMessagesRules, cfg.CodexResponsesRules, cfg.OpenAICompletionsRules} {
		if rules == "" {
			continue
		}
		if _, err := parseRules(rules); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

var (
	loadedConfigMu sync.RWMutex
	loadedCfg      = defaultConfig()
)

func loadedConfig() Config {
	loadedConfigMu.RLock()
	defer loadedConfigMu.RUnlock()
	return loadedCfg
}

func setLoadedConfigForTest(cfg Config) {
	loadedConfigMu.Lock()
	loadedCfg = cfg
	loadedConfigMu.Unlock()
}

func handlePluginRegister(raw []byte) ([]byte, error) {
	return json.Marshal(pluginRegistration())
}

func handlePluginReconfigure(raw []byte) ([]byte, error) {
	cfg, err := decodeConfig(raw)
	if err != nil {
		return nil, err
	}
	setLoadedConfigForTest(cfg)
	return json.Marshal(map[string]any{"ok": true, "enabled": cfg.Enabled})
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

func handleModelRoute(raw []byte) ([]byte, error) {
	var req pluginapi.ModelRouteRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	decision, err := routeModel(loadedConfig(), req.SourceFormat, req.RequestedModel)
	if err != nil {
		return nil, err
	}
	if !decision.Handled {
		return json.Marshal(pluginapi.ModelRouteResponse{Handled: false})
	}
	return json.Marshal(pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetSelf, Reason: "model mapped by model-mapper"})
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

type hostCaller func(method string, payload any) (json.RawMessage, error)

type executorRPCRequest struct {
	pluginapi.ExecutorRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
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
	decision, err := routeModel(loadedConfig(), req.SourceFormat, req.Model)
	if err != nil {
		return nil, err
	}
	if !decision.Handled {
		return nil, fmt.Errorf("unhandled model route for %q", req.Model)
	}
	body, _, err := rewriteRequestModel(req.OriginalRequest, decision.UpstreamModel)
	if err != nil {
		return nil, err
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
	if hostResp.StatusCode >= 400 {
		return nil, fmt.Errorf("host.model.execute status %d: %s", hostResp.StatusCode, string(hostResp.Body))
	}
	payload, _, err := restoreResponseModel(hostResp.Body, decision.OriginalModel)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pluginapi.ExecutorResponse{Payload: payload, Headers: hostResp.Headers})
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
