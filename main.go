package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

func main() {}

var pluginVersion = "0.0.0-dev"

type sseRewriter struct {
	originalModel string
	encodedModel  json.RawMessage
	buf           []byte
	scanFrom      int
	bomPrefix     []byte
	bomDone       bool
}

type streamChunkRewriter struct {
	originalModel     string
	frameRawJSONAsSSE bool
	sse               *sseRewriter
	pending           []byte
}

func newSSERewriter(originalModel string) *sseRewriter {
	return &sseRewriter{originalModel: originalModel}
}

var utf8SSEBOM = [...]byte{0xef, 0xbb, 0xbf}

func (r *sseRewriter) consumeLeadingBOM(p []byte) []byte {
	if r.bomDone {
		return p
	}
	for len(p) > 0 && len(r.bomPrefix) < len(utf8SSEBOM) {
		if p[0] != utf8SSEBOM[len(r.bomPrefix)] {
			r.bomDone = true
			if len(r.bomPrefix) == 0 {
				return p
			}
			restored := make([]byte, 0, len(r.bomPrefix)+len(p))
			restored = append(restored, r.bomPrefix...)
			restored = append(restored, p...)
			r.bomPrefix = nil
			return restored
		}
		r.bomPrefix = append(r.bomPrefix, p[0])
		p = p[1:]
	}
	if len(r.bomPrefix) == len(utf8SSEBOM) {
		r.bomPrefix = nil
		r.bomDone = true
	}
	return p
}

func (r *sseRewriter) flushLeadingBOM() {
	if r.bomDone || len(r.bomPrefix) == 0 {
		return
	}
	r.buf = append(r.bomPrefix, r.buf...)
	r.bomPrefix = nil
	r.bomDone = true
}

func sseFieldValue(line []byte) []byte {
	value := line[len("data:"):]
	if len(value) > 0 && value[0] == ' ' {
		return value[1:]
	}
	return value
}

func (r *sseRewriter) restoreResponseModel(body []byte) ([]byte, bool, error) {
	if !mightContainResponseModelField(body) {
		return bytes.Clone(body), false, nil
	}
	if r.encodedModel == nil {
		var err error
		r.encodedModel, err = json.Marshal(r.originalModel)
		if err != nil {
			return nil, false, err
		}
	}
	return rewriteResponseModelFieldsWithReplacement(body, r.originalModel, r.encodedModel)
}

func newStreamChunkRewriter(originalModel string) *streamChunkRewriter {
	return &streamChunkRewriter{
		originalModel: originalModel,
		sse:           newSSERewriter(originalModel),
	}
}

func (r *sseRewriter) Write(p []byte) ([][]byte, error) {
	p = r.consumeLeadingBOM(p)
	if len(p) == 0 && !r.bomDone && len(r.bomPrefix) > 0 {
		return nil, nil
	}
	r.buf = append(r.buf, p...)
	return r.drain(false)
}

func (r *sseRewriter) Flush() ([][]byte, error) {
	r.flushLeadingBOM()
	if len(r.buf) == 0 {
		return nil, nil
	}
	return r.drain(true)
}

func (r *sseRewriter) drain(eof bool) ([][]byte, error) {
	bufferCap := cap(r.buf)
	var out [][]byte
	consumed := false
	for {
		delim, n, next := findSSEEventDelimiter(r.buf, r.scanFrom, eof)
		if n == 0 {
			r.scanFrom = next
			break
		}
		event := r.buf[:delim]
		delimiter := r.buf[delim : delim+n : delim+n]
		if cap(r.buf) >= 1<<20 {
			delimiter = bytes.Clone(delimiter)
		}
		r.buf = r.buf[delim+n:]
		r.scanFrom = 0
		consumed = true
		var err error
		out, err = r.rewriteEvent(out, event)
		if err != nil {
			return nil, err
		}
		out = append(out, delimiter)
	}
	if eof && len(r.buf) > 0 {
		event := r.buf
		r.buf = nil
		r.scanFrom = 0
		var err error
		out, err = r.rewriteEvent(out, event)
		if err != nil {
			return nil, err
		}
	}
	if consumed {
		if len(r.buf) == 0 {
			r.buf = nil
			r.scanFrom = 0
		} else if bufferCap > 2*len(r.buf) {
			r.buf = bytes.Clone(r.buf)
		}
	}
	return out, nil
}

func splitSSELine(event []byte) (line, lineBreak, remaining []byte) {
	lineEnd, lineBreakLen, _ := sseLineEnding(event, 0, true)
	if lineBreakLen == 0 {
		return event, nil, nil
	}
	return event[:lineEnd], event[lineEnd : lineEnd+lineBreakLen], event[lineEnd+lineBreakLen:]
}

func hasSSEDataField(event []byte) bool {
	for len(event) > 0 {
		line, _, remaining := splitSSELine(event)
		event = remaining
		if bytes.HasPrefix(line, []byte("data:")) {
			return true
		}
	}
	return false
}

func appendUnchangedSSEEvent(out [][]byte, event []byte) [][]byte {
	for len(event) > 0 {
		line, lineBreak, remaining := splitSSELine(event)
		event = remaining
		chunk := make([]byte, 0, len(line)+len(lineBreak))
		chunk = append(chunk, line...)
		chunk = append(chunk, lineBreak...)
		out = append(out, chunk)
	}
	return out
}

func (r *sseRewriter) rewriteMultiDataEvent(out [][]byte, event []byte) ([][]byte, error) {
	joinedLen := 0
	dataFields := 0
	nonDataFields := 0
	for remaining := event; len(remaining) > 0; {
		line, _, next := splitSSELine(remaining)
		remaining = next
		if !bytes.HasPrefix(line, []byte("data:")) {
			nonDataFields++
			continue
		}
		value := sseFieldValue(line)
		if dataFields > 0 {
			joinedLen++
		}
		joinedLen += len(value)
		dataFields++
	}

	joined := make([]byte, 0, joinedLen)
	seenData := 0
	for remaining := event; len(remaining) > 0; {
		line, _, next := splitSSELine(remaining)
		remaining = next
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		if seenData > 0 {
			joined = append(joined, 10)
		}
		joined = append(joined, sseFieldValue(line)...)
		seenData++
	}
	if !mightContainResponseModelField(joined) {
		return appendUnchangedSSEEvent(out, event), nil
	}
	restored, changed, err := r.restoreResponseModel(joined)
	if err != nil {
		return nil, err
	}
	if !changed {
		return appendUnchangedSSEEvent(out, event), nil
	}

	retainedFields := nonDataFields + 1
	retained := 0
	wroteData := false
	for len(event) > 0 {
		line, lineBreak, remaining := splitSSELine(event)
		event = remaining
		isData := bytes.HasPrefix(line, []byte("data:"))
		if isData && wroteData {
			continue
		}
		var chunk []byte
		if isData {
			wroteData = true
			chunk = make([]byte, 0, len("data: ")+len(restored)+len(lineBreak))
			chunk = append(chunk, "data: "...)
			chunk = append(chunk, restored...)
		} else {
			chunk = make([]byte, 0, len(line)+len(lineBreak))
			chunk = append(chunk, line...)
		}
		retained++
		if retained < retainedFields {
			chunk = append(chunk, lineBreak...)
		}
		out = append(out, chunk)
	}
	return out, nil
}

func (r *sseRewriter) rewriteEvent(out [][]byte, event []byte) ([][]byte, error) {
	originalEvent := event
	originalOutLen := len(out)
	for len(event) > 0 {
		line, lineBreak, remaining := splitSSELine(event)
		event = remaining
		if bytes.HasPrefix(line, []byte("data:")) {
			if hasSSEDataField(remaining) {
				return r.rewriteMultiDataEvent(out[:originalOutLen], originalEvent)
			}
			value := sseFieldValue(line)
			if len(value) == 0 || bytes.Equal(value, []byte("[DONE]")) {
				out = append(out, append(append([]byte(nil), line...), lineBreak...))
				continue
			}
			if !mightContainResponseModelField(value) {
				out = append(out, append(append([]byte(nil), line...), lineBreak...))
				continue
			}
			restored, changed, err := r.restoreResponseModel(value)
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

func sseLineEnding(buf []byte, start int, eof bool) (position, length, next int) {
	start = max(0, min(start, len(buf)))
	for i := start; i < len(buf); i++ {
		switch buf[i] {
		case 10:
			return i, 1, 0
		case 13:
			if i+1 < len(buf) {
				if buf[i+1] == 10 {
					return i, 2, 0
				}
				return i, 1, 0
			}
			if eof {
				return i, 1, 0
			}
			return 0, 0, i
		}
	}
	return 0, 0, len(buf)
}

func findSSEEventDelimiter(buf []byte, start int, eof bool) (eventLen, delimLen, next int) {
	start = max(0, min(start, len(buf)))
	search := start
	previousPosition := -1
	previousEnd := -1
	for {
		position, length, resume := sseLineEnding(buf, search, eof)
		if length == 0 {
			if resume < len(buf) {
				if previousPosition >= 0 && previousEnd == resume {
					return 0, 0, previousPosition
				}
				return 0, 0, resume
			}
			if previousPosition >= 0 && previousEnd == len(buf) {
				return 0, 0, previousPosition
			}
			return 0, 0, len(buf)
		}
		if previousPosition >= 0 && previousEnd == position {
			return previousPosition, position + length - previousPosition, 0
		}
		previousPosition = position
		previousEnd = position + length
		search = previousEnd
	}
}

func sseEventDelimiter(buf []byte, start int) (eventLen, delimLen, next int) {
	return findSSEEventDelimiter(buf, start, false)
}

func isSSEFieldStart(p []byte) bool {
	trimmed := bytes.TrimLeft(p, " \t\r\n")
	for _, prefix := range [][]byte{[]byte("data:"), []byte("event:"), []byte("id:"), []byte("retry:"), []byte(":")} {
		if bytes.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func lastSSELine(p []byte) []byte {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '\n' || p[i] == '\r' {
			return p[i+1:]
		}
	}
	return p
}

func knownSSELogicalBoundary(pending, next []byte) bool {
	if len(pending) == 0 || bytes.HasSuffix(pending, []byte("\n")) || bytes.HasSuffix(pending, []byte("\r")) {
		return false
	}
	if !isSSEFieldStart(next) {
		return false
	}
	line := lastSSELine(pending)
	switch {
	case bytes.HasPrefix(line, []byte("event:")):
		return len(bytes.TrimSpace(line[len("event:"):])) > 0
	case bytes.HasPrefix(line, []byte("data:")):
		value := sseFieldValue(line)
		return len(value) > 0 && json.Valid(bytes.TrimSpace(value))
	default:
		return false
	}
}

func isColonlessSSEChunk(p []byte) bool {
	trimmed := bytes.TrimLeft(p, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] == '{' || trimmed[0] == '[' {
		return false
	}
	eventLen, delimiterLength, _ := findSSEEventDelimiter(p, 0, true)
	if delimiterLength == 0 {
		return false
	}
	event := p[:eventLen]
	for len(event) > 0 {
		line, _, remaining := splitSSELine(event)
		event = remaining
		if bytes.IndexByte(line, ':') >= 0 {
			return false
		}
	}
	return true
}

func (r *streamChunkRewriter) Write(p []byte) ([][]byte, error) {
	if !r.sse.bomDone {
		p = r.sse.consumeLeadingBOM(p)
		if len(p) == 0 && !r.sse.bomDone {
			return nil, nil
		}
	}
	if len(r.pending) > 0 {
		r.pending = append(r.pending, p...)
		p = append([]byte(nil), r.pending...)
		r.pending = nil
	}
	if len(r.sse.buf) > 0 {
		if knownSSELogicalBoundary(r.sse.buf, p) {
			r.sse.buf = append(r.sse.buf, '\n')
		}
		return r.sse.Write(p)
	}
	if r.frameRawJSONAsSSE && isColonlessSSEChunk(p) {
		return r.sse.Write(p)
	}
	if couldStartJSONValue(p) {
		if chunks, ok, err := r.tryRawJSONChunks(p); ok || err != nil {
			return chunks, err
		}
	}
	if isSSEChunk(p) {
		return r.sse.Write(p)
	}
	if isIncompleteSSEPrefix(p) {
		r.pending = append(r.pending, p...)
		return nil, nil
	}
	return r.rawJSONChunks(p)
}

func (r *streamChunkRewriter) rawJSONChunks(p []byte) ([][]byte, error) {
	chunks, ok, err := r.tryRawJSONChunks(p)
	if err != nil {
		return nil, err
	}
	if !ok {
		return [][]byte{bytes.Clone(p)}, nil
	}
	return chunks, nil
}

func (r *streamChunkRewriter) tryRawJSONChunks(p []byte) ([][]byte, bool, error) {
	trimmed := bytes.Trim(p, " \t\r\n")
	if len(trimmed) > 0 {
		switch trimmed[0] {
		case '{':
			if bytes.IndexByte(trimmed, '}') < 0 {
				return [][]byte{bytes.Clone(p)}, true, nil
			}
		case '[':
			if bytes.IndexByte(trimmed, ']') < 0 {
				return [][]byte{bytes.Clone(p)}, true, nil
			}
		}
	}
	couldBeComplete := len(trimmed) > 0
	if couldBeComplete {
		switch trimmed[0] {
		case '{':
			couldBeComplete = trimmed[len(trimmed)-1] == '}'
		case '[':
			couldBeComplete = trimmed[len(trimmed)-1] == ']'
		case '"':
			couldBeComplete = trimmed[len(trimmed)-1] == '"'
		}
	}
	if couldBeComplete && json.Valid(trimmed) {
		restored, _, err := r.sse.restoreResponseModel(trimmed)
		if err != nil {
			return nil, false, err
		}
		if r.frameRawJSONAsSSE {
			return [][]byte{frameSSEData(restored)}, true, nil
		}
		return [][]byte{restored}, true, nil
	}
	values, ok := splitJSONValues(p)
	if !ok {
		return nil, false, nil
	}
	if len(values) == 0 {
		return nil, true, nil
	}
	out := make([][]byte, 0, len(values))
	for _, value := range values {
		restored, _, err := r.sse.restoreResponseModel(value)
		if err != nil {
			return nil, false, err
		}
		if r.frameRawJSONAsSSE {
			out = append(out, frameSSEData(restored))
			continue
		}
		out = append(out, restored)
	}
	return out, true, nil
}

func splitJSONValues(p []byte) ([][]byte, bool) {
	if len(bytes.TrimSpace(p)) == 0 {
		return nil, true
	}
	dec := json.NewDecoder(bytes.NewReader(p))
	values := make([][]byte, 0, 1)
	for {
		var raw json.RawMessage
		err := dec.Decode(&raw)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		values = append(values, raw)
	}
	return values, len(values) > 0
}

func (r *streamChunkRewriter) Flush() ([][]byte, error) {
	if len(r.pending) > 0 {
		pending := append([]byte(nil), r.pending...)
		r.pending = nil
		chunks, err := r.sse.Write(pending)
		if err != nil {
			return nil, err
		}
		flushed, err := r.sse.Flush()
		if err != nil {
			return nil, err
		}
		return append(chunks, flushed...), nil
	}
	return r.sse.Flush()
}

func frameSSEData(p []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(p, []byte("\n")) {
		out.WriteString("data: ")
		out.Write(line)
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	return out.Bytes()
}

func isEventStreamContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "text/event-stream")
}

func hasSSEFieldPrefix(p []byte) bool {
	trimmed := bytes.TrimLeft(p, " \t\r\n")
	lineEnd := len(trimmed)
	if i := bytes.IndexByte(trimmed, 13); i >= 0 {
		lineEnd = i
	}
	if i := bytes.IndexByte(trimmed[:lineEnd], 10); i >= 0 {
		lineEnd = i
	}
	line := trimmed[:lineEnd]
	return bytes.IndexByte(line, ':') >= 0 ||
		bytes.Equal(line, []byte("data")) ||
		bytes.Equal(line, []byte("event")) ||
		bytes.Equal(line, []byte("id")) ||
		bytes.Equal(line, []byte("retry"))
}

func couldStartJSONValue(p []byte) bool {
	trimmed := bytes.TrimSpace(p)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case '{', '[', '"', '-', 't', 'f', 'n':
		return true
	default:
		return trimmed[0] >= '0' && trimmed[0] <= '9'
	}
}
func isSSEChunk(p []byte) bool {
	if hasSSEFieldPrefix(p) {
		return true
	}
	_, delimiterLength, _ := findSSEEventDelimiter(p, 0, true)
	return delimiterLength > 0 || hasSSEDataField(p)
}

func isIncompleteSSEPrefix(p []byte) bool {
	trimmed := bytes.TrimLeft(p, " \t\r\n")
	if len(trimmed) == 0 {
		return false
	}
	for _, field := range [][]byte{[]byte("data:"), []byte("event:"), []byte("id:"), []byte("retry:"), []byte(":")} {
		if bytes.HasPrefix(field, trimmed) && len(trimmed) < len(field) {
			return true
		}
	}
	return false
}

type Config struct {
	GlobalRules            string `json:"global_rules"`
	ClaudeMessagesRules    string `json:"claude_messages_rules"`
	CodexResponsesRules    string `json:"codex_responses_rules"`
	OpenAICompletionsRules string `json:"openai_completions_rules"`

	globalRules            []rule
	claudeMessagesRules    []rule
	codexResponsesRules    []rule
	openAICompletionsRules []rule
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
			Name:             "model-mapper",
			Version:          pluginVersion,
			Author:           "DoingDog",
			GitHubRepository: "https://github.com/DoingDog/cpa-plugin-model-mapper",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "global_rules", Type: pluginapi.ConfigFieldTypeString, Description: "Fallback rules used when an endpoint-specific ruleset is empty."},
				{Name: "claude_messages_rules", Type: pluginapi.ConfigFieldTypeString, Description: "Rules for Claude Messages-compatible requests."},
				{Name: "codex_responses_rules", Type: pluginapi.ConfigFieldTypeString, Description: "Rules for OpenAI Responses/Codex-compatible requests."},
				{Name: "openai_completions_rules", Type: pluginapi.ConfigFieldTypeString, Description: "Rules for OpenAI Completions and Chat Completions requests."},
			},
		},
		Capabilities: registrationCapabilities{
			ModelRouter:           true,
			Executor:              true,
			ExecutorModelScope:    string(pluginapi.ExecutorModelScopeStatic),
			ExecutorInputFormats:  []string{"openai", "claude", "openai-response", "gemini"},
			ExecutorOutputFormats: []string{"openai", "claude", "openai-response"},
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
	return compileConfig(cfg)
}

func compileConfig(cfg Config) (Config, error) {
	var err error
	if cfg.globalRules, err = compileRuleSet(cfg.GlobalRules); err != nil {
		return Config{}, err
	}
	if cfg.claudeMessagesRules, err = compileRuleSet(cfg.ClaudeMessagesRules); err != nil {
		return Config{}, err
	}
	if cfg.codexResponsesRules, err = compileRuleSet(cfg.CodexResponsesRules); err != nil {
		return Config{}, err
	}
	if cfg.openAICompletionsRules, err = compileRuleSet(cfg.OpenAICompletionsRules); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func compileRuleSet(raw string) ([]rule, error) {
	if raw == "" {
		return nil, nil
	}
	return parseRules(raw)
}

type callerPatternCacheKey struct {
	scope   string
	pattern string
}

var (
	loadedConfigMu sync.RWMutex
	loadedCfg      = defaultConfig()

	callerPatternCacheMu sync.RWMutex
	callerPatternCache   = make(map[callerPatternCacheKey]bool)

	hostAPIMu      sync.RWMutex
	hostCallbackFn hostCallback
)

type hostCallback func(method string, request []byte) ([]byte, error)

func loadedConfig() Config {
	loadedConfigMu.RLock()
	defer loadedConfigMu.RUnlock()
	return loadedCfg
}

func setLoadedConfigForTest(cfg Config) {
	if compiled, err := compileConfig(cfg); err == nil {
		cfg = compiled
	}
	loadedConfigMu.Lock()
	callerPatternCacheMu.Lock()
	loadedCfg = cfg
	callerPatternCache = make(map[callerPatternCacheKey]bool)
	callerPatternCacheMu.Unlock()
	loadedConfigMu.Unlock()
}

func applyLifecycleConfig(raw []byte) error {
	cfgRaw, _, err := decodeLifecycleConfig(raw)
	if err != nil {
		return err
	}
	cfg, err := decodeConfig(cfgRaw)
	if err != nil {
		return err
	}
	setLoadedConfigForTest(cfg)
	return nil
}

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

type routeDecision struct {
	Handled       bool
	OriginalModel string
	UpstreamModel string
}

func selectRules(cfg Config, format string) (string, []rule, bool) {
	switch format {
	case "claude":
		if cfg.ClaudeMessagesRules != "" {
			return cfg.ClaudeMessagesRules, cfg.claudeMessagesRules, true
		}
	case "openai-response":
		if cfg.CodexResponsesRules != "" {
			return cfg.CodexResponsesRules, cfg.codexResponsesRules, true
		}
	case "openai":
		if cfg.OpenAICompletionsRules != "" {
			return cfg.OpenAICompletionsRules, cfg.openAICompletionsRules, true
		}
	}
	if cfg.GlobalRules != "" {
		return cfg.GlobalRules, cfg.globalRules, true
	}
	return "", nil, false
}

func callerAPIKeyForSelectedRules(cfg Config, format string, headers http.Header, query url.Values, scope string) string {
	_, rules, ok := selectRules(cfg, format)
	if !ok {
		return ""
	}
	if rules == nil {
		return callerAPIKey(headers, query, scope)
	}
	for i := range rules {
		if len(rules[i].callerPattern) > 0 {
			return callerAPIKey(headers, query, scope)
		}
	}
	return ""
}

type modelRouteRPCRequest struct {
	SourceFormat   string
	RequestedModel string
	Headers        http.Header
	Query          url.Values
	Metadata       map[string]any
}

func handleModelRoute(raw []byte) ([]byte, error) {
	var req modelRouteRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	scope := callerScopeFromMetadata(req.Metadata)
	cfg := loadedConfig()
	decision, err := routeModel(cfg, req.SourceFormat, req.RequestedModel, scope, callerAPIKeyForSelectedRules(cfg, req.SourceFormat, req.Headers, req.Query, scope))
	if err != nil {
		return nil, err
	}
	if !decision.Handled {
		return json.Marshal(pluginapi.ModelRouteResponse{Handled: false})
	}
	return json.Marshal(pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetSelf, Reason: "model mapped by model-mapper"})
}

func routeModel(cfg Config, format, model, scope, key string) (routeDecision, error) {
	raw, rules, ok := selectRules(cfg, format)
	if !ok {
		return routeDecision{}, nil
	}
	if rules == nil {
		var err error
		rules, err = parseRules(raw)
		if err != nil {
			return routeDecision{}, err
		}
	}
	mapped, matched, err := applyRules(model, scope, key, rules)
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
	return rewriteResponseModelFields(body, originalModel)
}

type hostCaller func(method string, payload any) (json.RawMessage, error)

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

func handleExecutorExecuteStream(raw []byte, call hostCaller) ([]byte, error) {
	var req executorRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if req.StreamID == "" {
		return nil, fmt.Errorf("missing plugin stream id")
	}
	return startExecutorStream(req, call, func(streamID, errText string) error {
		_, err := call(pluginabi.MethodHostStreamClose, struct {
			StreamID string `json:"stream_id"`
			Error    string `json:"error,omitempty"`
		}{StreamID: streamID, Error: errText})
		return err
	})
}

func startExecutorStream(req executorRPCRequest, call hostCaller, closeStream func(string, string) error) ([]byte, error) {
	go func() {
		if err := runStreamForward(req, call); err != nil {
			_ = closeStream(req.StreamID, err.Error())
		}
	}()
	return json.Marshal(map[string]any{"headers": http.Header{"Content-Type": []string{"text/event-stream"}}})
}

func runStreamForward(req executorRPCRequest, call hostCaller) error {
	scope := callerScopeFromMetadata(req.Metadata)
	cfg := loadedConfig()
	decision, err := routeModel(cfg, req.SourceFormat, req.Model, scope, callerAPIKeyForSelectedRules(cfg, req.SourceFormat, req.Headers, req.Query, scope))
	if err != nil {
		return fmt.Errorf("route stream: %w", err)
	}
	if !decision.Handled {
		return fmt.Errorf("route stream: unhandled model route for %q", req.Model)
	}
	body, _, err := rewriteRequestModel(req.OriginalRequest, decision.UpstreamModel)
	if err != nil {
		return fmt.Errorf("rewrite stream request: %w", err)
	}
	hostRaw, err := call(pluginabi.MethodHostModelExecuteStream, hostModelExecutePayload{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: req.SourceFormat,
			ExitProtocol:  req.Format,
			Model:         decision.UpstreamModel,
			Stream:        true,
			Body:          body,
			Headers:       req.Headers,
			Query:         req.Query,
			Alt:           req.Alt,
		},
		HostCallbackID: req.HostCallbackID,
	})
	if err != nil {
		return fmt.Errorf("execute stream: %w", err)
	}
	var hostResp struct {
		pluginapi.HostModelStreamResponse
		Body []byte `json:"body"`
	}
	if err := json.Unmarshal(hostRaw, &hostResp); err != nil {
		return fmt.Errorf("decode host stream response: %w", err)
	}
	if hostResp.StatusCode >= 400 {
		if hostResp.StreamID != "" {
			_, _ = call(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: hostResp.StreamID})
		}
		return fmt.Errorf("execute stream status %d: %s", hostResp.StatusCode, string(hostResp.Body))
	}
	if hostResp.StreamID == "" {
		return fmt.Errorf("missing host stream id")
	}
	hostStreamID := hostResp.StreamID
	closeHost := func() error {
		_, err := call(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: hostStreamID})
		return err
	}
	closePlugin := func(errText string) error {
		_, err := call(pluginabi.MethodHostStreamClose, struct {
			StreamID string `json:"stream_id"`
			Error    string `json:"error,omitempty"`
		}{StreamID: req.StreamID, Error: errText})
		return err
	}
	emit := func(payload []byte) error {
		_, err := call(pluginabi.MethodHostStreamEmit, struct {
			StreamID string `json:"stream_id"`
			Payload  []byte `json:"payload"`
		}{StreamID: req.StreamID, Payload: payload})
		return err
	}
	rewriter := newStreamChunkRewriter(decision.OriginalModel)
	rewriter.frameRawJSONAsSSE = isEventStreamContentType(hostResp.Headers.Get("Content-Type"))
	for {
		readRaw, err := call(pluginabi.MethodHostModelStreamRead, pluginapi.HostModelStreamReadRequest{StreamID: hostStreamID})
		if err != nil {
			_ = closeHost()
			return fmt.Errorf("read host stream: %w", err)
		}
		var chunk pluginapi.HostModelStreamReadResponse
		if err := json.Unmarshal(readRaw, &chunk); err != nil {
			_ = closeHost()
			return fmt.Errorf("decode host stream chunk: %w", err)
		}
		if chunk.Error != "" {
			flushed, flushErr := rewriter.Flush()
			if flushErr != nil {
				_ = closeHost()
				return fmt.Errorf("flush stream rewriter before error close: %w", flushErr)
			}
			for _, out := range flushed {
				if err := emit(out); err != nil {
					_ = closeHost()
					return fmt.Errorf("emit flushed stream chunk before error close: %w", err)
				}
			}
			if err := closeHost(); err != nil {
				return fmt.Errorf("close host stream: %w", err)
			}
			if err := closePlugin(chunk.Error); err != nil {
				return fmt.Errorf("close plugin stream: %w", err)
			}
			return nil
		}
		if chunk.Done {
			break
		}
		chunks, err := rewriter.Write(chunk.Payload)
		if err != nil {
			_ = closeHost()
			return fmt.Errorf("rewrite stream chunk: %w", err)
		}
		for _, out := range chunks {
			if err := emit(out); err != nil {
				_ = closeHost()
				return fmt.Errorf("emit stream chunk: %w", err)
			}
		}
	}
	flushed, err := rewriter.Flush()
	if err != nil {
		_ = closeHost()
		return fmt.Errorf("flush stream rewriter: %w", err)
	}
	for _, out := range flushed {
		if err := emit(out); err != nil {
			_ = closeHost()
			return fmt.Errorf("emit flushed stream chunk: %w", err)
		}
	}
	if err := closeHost(); err != nil {
		return fmt.Errorf("close host stream: %w", err)
	}
	if err := closePlugin(""); err != nil {
		return fmt.Errorf("close plugin stream: %w", err)
	}
	return nil
}

func handleExecutorExecute(raw []byte, call hostCaller) ([]byte, error) {
	var req executorRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	scope := callerScopeFromMetadata(req.Metadata)
	cfg := loadedConfig()
	decision, err := routeModel(cfg, req.SourceFormat, req.Model, scope, callerAPIKeyForSelectedRules(cfg, req.SourceFormat, req.Headers, req.Query, scope))
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

func wrapEnvelope(payload []byte, err error) ([]byte, error) {
	if err != nil {
		return errorEnvelope("plugin_error", err.Error()), nil
	}
	if len(payload) == 0 {
		payload = []byte("null")
	}
	return json.Marshal(pluginabi.Envelope{OK: true, Result: json.RawMessage(payload)})
}

func errorEnvelope(code, message string) []byte {
	raw, err := json.Marshal(pluginabi.Envelope{
		OK:    false,
		Error: &pluginabi.Error{Code: code, Message: message},
	})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"plugin_error","message":"failed to encode error envelope"}}`)
	}
	return raw
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

type lifecycleYAMLConfig struct {
	GlobalRules            string `yaml:"global_rules"`
	ClaudeMessagesRules    string `yaml:"claude_messages_rules"`
	CodexResponsesRules    string `yaml:"codex_responses_rules"`
	OpenAICompletionsRules string `yaml:"openai_completions_rules"`
}

func decodeLifecycleConfig(raw []byte) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	var lifecycle struct {
		ConfigYAML string `json:"config_yaml"`
	}
	if err := json.Unmarshal(trimmed, &lifecycle); err == nil && lifecycle.ConfigYAML != "" {
		decoded, err := base64.StdEncoding.DecodeString(lifecycle.ConfigYAML)
		if err != nil {
			return nil, true, err
		}
		var yamlConfig lifecycleYAMLConfig
		if err := yaml.Unmarshal(decoded, &yamlConfig); err != nil {
			return nil, true, err
		}
		cfgRaw, err := json.Marshal(Config{
			GlobalRules:            yamlConfig.GlobalRules,
			ClaudeMessagesRules:    yamlConfig.ClaudeMessagesRules,
			CodexResponsesRules:    yamlConfig.CodexResponsesRules,
			OpenAICompletionsRules: yamlConfig.OpenAICompletionsRules,
		})
		if err != nil {
			return nil, true, err
		}
		return cfgRaw, true, nil
	}
	return append(json.RawMessage(nil), trimmed...), false, nil
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
		return nil, fmt.Errorf("host callback %s failed: %s", method, env.Error.Message)
	}
	return env.Result, nil
}

func rewriteTopLevelModel(body []byte, model string) ([]byte, bool, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return bytes.Clone(body), false, nil
	}
	replacement, err := json.Marshal(model)
	if err != nil {
		return nil, false, err
	}
	if !rewriteRawStringField(doc, "model", model, replacement) {
		return bytes.Clone(body), false, nil
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func mightContainResponseModelField(body []byte) bool {
	return bytes.Contains(body, []byte(`"model"`)) ||
		bytes.Contains(body, []byte(`"modelVersion"`)) ||
		bytes.Contains(body, []byte(`\u`))
}

func rewriteResponseModelFields(body []byte, model string) ([]byte, bool, error) {
	if !mightContainResponseModelField(body) {
		return bytes.Clone(body), false, nil
	}
	replacement, err := json.Marshal(model)
	if err != nil {
		return nil, false, err
	}
	return rewriteResponseModelFieldsWithReplacement(body, model, replacement)
}

func rewriteResponseModelFieldsWithReplacement(body []byte, model string, replacement json.RawMessage) ([]byte, bool, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return bytes.Clone(body), false, nil
	}
	changed := rewriteRawStringField(doc, "model", model, replacement)
	changed = rewriteRawStringField(doc, "modelVersion", model, replacement) || changed
	messageChanged, err := rewriteNestedRawStringFields(doc, "message", model, replacement, "model")
	if err != nil {
		return nil, false, err
	}
	changed = messageChanged || changed
	responseChanged, err := rewriteNestedRawStringFields(doc, "response", model, replacement, "model", "modelVersion")
	if err != nil {
		return nil, false, err
	}
	changed = responseChanged || changed
	if !changed {
		return bytes.Clone(body), false, nil
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func rewriteNestedRawStringFields(doc map[string]json.RawMessage, key, model string, replacement json.RawMessage, fields ...string) (bool, error) {
	raw, ok := doc[key]
	if !ok {
		return false, nil
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return false, nil
	}
	changed := false
	for _, field := range fields {
		changed = rewriteRawStringField(nested, field, model, replacement) || changed
	}
	if !changed {
		return false, nil
	}
	out, err := json.Marshal(nested)
	if err != nil {
		return false, err
	}
	doc[key] = out
	return true, nil
}

func rewriteRawStringField(doc map[string]json.RawMessage, key, model string, replacement json.RawMessage) bool {
	raw, ok := doc[key]
	if !ok {
		return false
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return false
	}
	if bytes.IndexByte(trimmed, '\\') < 0 && utf8.Valid(trimmed[1:len(trimmed)-1]) {
		if len(trimmed) == len(model)+2 && bytes.Equal(trimmed[1:len(trimmed)-1], []byte(model)) {
			return false
		}
		doc[key] = replacement
		return true
	}
	var current string
	if err := json.Unmarshal(trimmed, &current); err != nil || current == model {
		return false
	}
	doc[key] = replacement
	return true
}

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

const (
	callerScopeMetadataKey = "caller_scope"
	callerScopeDomain      = "cli-proxy-api:caller-scope:v1\x00"
)

func callerScope(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(callerScopeDomain + apiKey))
	return hex.EncodeToString(sum[:])
}

func callerScopeFromMetadata(metadata map[string]any) string {
	scope, _ := metadata[callerScopeMetadataKey].(string)
	return scope
}

func callerAPIKey(headers http.Header, query url.Values, scope string) string {
	if scope == "" {
		return ""
	}
	authorization := headers.Get("Authorization")
	parts := strings.SplitN(authorization, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		authorization = strings.TrimSpace(parts[1])
	}
	for _, candidate := range []string{
		authorization,
		headers.Get("X-Goog-Api-Key"),
		headers.Get("X-Api-Key"),
		query.Get("key"),
		query.Get("auth_token"),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && callerScope(candidate) == scope {
			return candidate
		}
	}
	return ""
}

func defaultConfig() Config {
	return Config{}
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
			case '*', ';', '$', '#', '\\':
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
			if i+1 < len(s) && s[i+1] == '#' {
				lit.WriteByte('#')
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

func callerPatternMatch(r *rule, scope, key string) (bool, bool) {
	cacheKey := callerPatternCacheKey{scope: scope, pattern: r.callerPatternText}
	callerPatternCacheMu.RLock()
	matched, ok := callerPatternCache[cacheKey]
	callerPatternCacheMu.RUnlock()
	if ok {
		return matched, true
	}
	if key == "" || callerScope(key) != scope {
		return false, false
	}
	_, matched = matchTokens(key, r.callerPattern)
	callerPatternCacheMu.Lock()
	if cached, exists := callerPatternCache[cacheKey]; exists {
		matched = cached
	} else {
		callerPatternCache[cacheKey] = matched
	}
	callerPatternCacheMu.Unlock()
	return matched, true
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

func setHostCallbackForTest(cb hostCallback) {
	hostAPIMu.Lock()
	hostCallbackFn = cb
	hostAPIMu.Unlock()
}

func setHostCallback(cb hostCallback) {
	setHostCallbackForTest(cb)
}
