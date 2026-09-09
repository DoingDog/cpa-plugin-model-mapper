package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	sawDone       bool
}

type streamChunkRewriter struct {
	originalModel     string
	format            string
	frameRawJSONAsSSE bool
	framedRawJSON     bool
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

func (r *sseRewriter) restoreResponseModelCandidate(body []byte) ([]byte, bool, bool, error) {
	if r.encodedModel == nil {
		var err error
		r.encodedModel, err = json.Marshal(r.originalModel)
		if err != nil {
			return nil, false, false, err
		}
	}
	return rewriteResponseModelFieldsWithReplacementChecked(body, r.originalModel, r.encodedModel)
}

func (r *sseRewriter) restoreResponseModel(body []byte) ([]byte, bool, error) {
	if !mightContainResponseModelField(body) {
		return bytes.Clone(body), false, nil
	}
	out, changed, _, err := r.restoreResponseModelCandidate(body)
	return out, changed, err
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
	hasMarker := false
	var markerScanner responseModelMarkerScanner
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
			if !hasMarker {
				hasMarker = markerScanner.feed('\n')
			}
		}
		joinedLen += len(value)
		if !hasMarker && mightContainResponseModelField(value) {
			hasMarker = true
		}
		if !hasMarker {
			for _, b := range value {
				if markerScanner.feed(b) {
					hasMarker = true
					break
				}
			}
		}
		dataFields++
	}
	if !hasMarker {
		return appendUnchangedSSEEvent(out, event), nil
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
	restored, changed, _, err := r.restoreResponseModelCandidate(joined)
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
			if len(value) == 0 {
				out = append(out, append(append([]byte(nil), line...), lineBreak...))
				continue
			}
			if bytes.Equal(bytes.TrimSpace(value), []byte("[DONE]")) {
				r.sawDone = true
				out = append(out, append(append([]byte(nil), line...), lineBreak...))
				continue
			}
			if !mightContainResponseModelField(value) {
				out = append(out, append(append([]byte(nil), line...), lineBreak...))
				continue
			}
			restored, changed, _, err := r.restoreResponseModelCandidate(value)
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

func completeSSEEvents(p []byte) bool {
	found := false
	for len(p) > 0 {
		eventLen, delimiterLen, _ := findSSEEventDelimiter(p, 0, true)
		if delimiterLen == 0 {
			return false
		}
		consumed := eventLen + delimiterLen
		if consumed <= 0 || consumed > len(p) {
			return false
		}
		p = p[consumed:]
		found = true
	}
	return found
}

func hasSSEDoneEvent(p []byte) bool {
	for len(p) > 0 {
		eventLen, delimiterLen, _ := findSSEEventDelimiter(p, 0, true)
		if delimiterLen == 0 {
			return false
		}
		for event := p[:eventLen]; len(event) > 0; {
			line, _, remaining := splitSSELine(event)
			event = remaining
			if bytes.HasPrefix(line, []byte("data:")) && bytes.Equal(bytes.TrimSpace(sseFieldValue(line)), []byte("[DONE]")) {
				return true
			}
		}
		p = p[eventLen+delimiterLen:]
	}
	return false
}

func (r *streamChunkRewriter) Write(p []byte) ([][]byte, error) {
	if !r.sse.bomDone {
		p = r.sse.consumeLeadingBOM(p)
		if len(p) == 0 && !r.sse.bomDone {
			return nil, nil
		}
	}
	owned := false
	if len(r.pending) > 0 {
		p = append(r.pending, p...)
		r.pending = nil
		owned = true
	}
	if len(r.sse.buf) > 0 {
		return r.sse.Write(p)
	}
	if r.frameRawJSONAsSSE && !couldStartJSONValue(p) && completeSSEEvents(p) && !mightContainResponseModelField(p) {
		r.sse.sawDone = r.sse.sawDone || hasSSEDoneEvent(p)
		return [][]byte{bytes.Clone(p)}, nil
	}
	if r.frameRawJSONAsSSE && isColonlessSSEChunk(p) {
		return r.sse.Write(p)
	}
	trimmed := bytes.TrimSpace(p)
	if r.frameRawJSONAsSSE && len(trimmed) > 0 && trimmed[0] != '{' && trimmed[0] != '[' && couldStartJSONValue(p) && !isSSEChunk(p) {
		if owned {
			r.pending = p
		} else {
			r.pending = append(r.pending, p...)
		}
		return nil, nil
	}
	if couldStartJSONValue(p) {
		chunks, consumed, ok, incomplete, err := r.tryRawJSONChunks(p)
		if err != nil {
			return nil, err
		}
		if ok {
			if incomplete {
				r.pending = append([]byte(nil), p[consumed:]...)
			}
			return chunks, nil
		}
		if incomplete {
			if owned {
				r.pending = p
			} else {
				r.pending = append(r.pending, p...)
			}
			return nil, nil
		}
	}
	if isSSEChunk(p) {
		return r.sse.Write(p)
	}
	if isIncompleteSSEPrefix(p) {
		r.pending = append(r.pending, p...)
		return nil, nil
	}
	if !r.frameRawJSONAsSSE && len(p) > 0 && len(bytes.Trim(p, " \t\r\n")) == 0 {
		if owned {
			r.pending = p
		} else {
			r.pending = append(r.pending, p...)
		}
		return nil, nil
	}
	return r.rawJSONChunks(p)
}

func (r *streamChunkRewriter) rawJSONChunks(p []byte) ([][]byte, error) {
	chunks, _, ok, incomplete, err := r.tryRawJSONChunks(p)
	if err != nil {
		return nil, err
	}
	if !ok && !incomplete {
		return [][]byte{bytes.Clone(p)}, nil
	}
	if incomplete {
		fallback := bytes.Clone(p)
		if r.frameRawJSONAsSSE {
			fallback = frameSSEData(fallback)
		}
		return [][]byte{fallback}, nil
	}
	return chunks, nil
}

func (r *streamChunkRewriter) tryRawJSONChunks(p []byte) ([][]byte, int, bool, bool, error) {
	start := skipTopLevelModelJSONSpace(p, 0)
	end := len(p)
	for end > start {
		switch p[end-1] {
		case ' ', '\t', '\r', '\n':
			end--
		default:
			goto trimmed
		}
	}

trimmed:
	value := p[start:end]
	if len(value) > 0 {
		switch value[0] {
		case '{':
			if bytes.IndexByte(value, '}') < 0 {
				return nil, 0, false, true, nil
			}
		case '[':
			if bytes.IndexByte(value, ']') < 0 {
				return nil, 0, false, true, nil
			}
		}
	}
	couldBeComplete := len(value) > 0
	if couldBeComplete {
		switch value[0] {
		case '{':
			couldBeComplete = value[len(value)-1] == '}'
		case '[':
			couldBeComplete = value[len(value)-1] == ']'
		case '"':
			couldBeComplete = value[len(value)-1] == '"'
		}
	}
	if couldBeComplete {
		var restored []byte
		changed := false
		valid := false
		if mightContainResponseModelField(value) {
			var err error
			restored, changed, valid, err = r.sse.restoreResponseModelCandidate(value)
			if err != nil {
				return nil, 0, false, false, err
			}
		} else if json.Valid(value) {
			restored = bytes.Clone(value)
			valid = true
		}
		if valid {
			if r.frameRawJSONAsSSE {
				return [][]byte{r.frameRawJSON(restored)}, len(p), true, false, nil
			}
			if !changed {
				return [][]byte{bytes.Clone(p)}, len(p), true, false, nil
			}
			if start == 0 && end == len(p) {
				return [][]byte{restored}, len(p), true, false, nil
			}
			out := make([]byte, 0, len(p)-end+start+len(restored))
			out = append(out, p[:start]...)
			out = append(out, restored...)
			out = append(out, p[end:]...)
			return [][]byte{out}, len(p), true, false, nil
		}
	}
	values, consumed, ok, incomplete := splitJSONValues(p)
	if !ok {
		return nil, 0, false, incomplete, nil
	}
	if len(values) == 0 {
		return nil, consumed, true, incomplete, nil
	}
	out := make([][]byte, 0, len(values))
	cursor := 0
	for i, value := range values {
		start := skipTopLevelModelJSONSpace(p, cursor)
		end := start + len(value)
		restored, _, err := r.sse.restoreResponseModel(value)
		if err != nil {
			return nil, 0, false, false, err
		}
		if r.frameRawJSONAsSSE {
			out = append(out, r.frameRawJSON(restored))
		} else {
			chunk := make([]byte, 0, start-cursor+len(restored))
			chunk = append(chunk, p[cursor:start]...)
			chunk = append(chunk, restored...)
			if i == len(values)-1 {
				chunk = append(chunk, p[end:consumed]...)
			}
			out = append(out, chunk)
		}
		cursor = end
	}
	return out, consumed, true, incomplete, nil
}

func splitJSONValues(p []byte) ([][]byte, int, bool, bool) {
	if len(bytes.TrimSpace(p)) == 0 {
		return nil, len(p), true, false
	}
	dec := json.NewDecoder(bytes.NewReader(p))
	values := make([][]byte, 0, 1)
	consumed := 0
	for {
		var raw json.RawMessage
		err := dec.Decode(&raw)
		if err == io.EOF {
			return values, len(p), len(values) > 0, false
		}
		if err != nil {
			if err == io.ErrUnexpectedEOF {
				return values, consumed, len(values) > 0, true
			}
			return nil, 0, false, false
		}
		values = append(values, raw)
		consumed = int(dec.InputOffset())
	}
}

func (r *streamChunkRewriter) Flush() ([][]byte, error) {
	if len(r.pending) > 0 {
		pending := append([]byte(nil), r.pending...)
		r.pending = nil
		if couldStartJSONValue(pending) {
			chunks, err := r.rawJSONChunks(pending)
			if err != nil {
				return nil, err
			}
			flushed, err := r.sse.Flush()
			if err != nil {
				return nil, err
			}
			return append(chunks, flushed...), nil
		}
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

func (r *streamChunkRewriter) Finish() ([][]byte, error) {
	chunks, err := r.Flush()
	if err != nil {
		return nil, err
	}
	if r.format == "openai" && r.frameRawJSONAsSSE && r.framedRawJSON && !r.sse.sawDone {
		chunks = append(chunks, []byte("data: [DONE]\n\n"))
	}
	return chunks, nil
}

func (r *streamChunkRewriter) frameRawJSON(p []byte) []byte {
	r.framedRawJSON = true
	eventType := ""
	if r.format == "openai-response" || r.format == "claude" {
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(p, &event) == nil {
			eventType = event.Type
		}
	}
	return frameSSEEvent(p, eventType)
}

func frameSSEData(p []byte) []byte {
	return frameSSEEvent(p, "")
}

func frameSSEEvent(p []byte, eventType string) []byte {
	var out bytes.Buffer
	if eventType != "" {
		out.WriteString("event: ")
		out.WriteString(eventType)
		out.WriteByte('\n')
	}
	start := 0
	for {
		position, length, _ := sseLineEnding(p, start, true)
		out.WriteString("data: ")
		if length == 0 {
			out.Write(p[start:])
			out.WriteByte('\n')
			break
		}
		out.Write(p[start:position])
		out.WriteByte('\n')
		start = position + length
		if start == len(p) {
			out.WriteString("data: \n")
			break
		}
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

const (
	rulesStackModeOff           = "off"
	rulesStackModeSpecificFirst = "specific_first"
	rulesStackModeGlobalFirst   = "global_first"
)

type Config struct {
	GlobalRules            string `json:"global_rules"`
	ClaudeMessagesRules    string `json:"claude_messages_rules"`
	CodexResponsesRules    string `json:"codex_responses_rules"`
	OpenAICompletionsRules string `json:"openai_completions_rules"`
	RulesStackMode         string `json:"rules_stack_mode"`

	compiled               bool
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

func decodeConfig(raw json.RawMessage) (Config, error) {
	cfg := defaultConfig()
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("{}")) {
		return compileConfig(cfg)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Config{}, err
	}
	if mode, ok := fields["rules_stack_mode"]; ok {
		mode = bytes.TrimSpace(mode)
		if len(mode) == 0 || mode[0] != '"' {
			return Config{}, fmt.Errorf("rules_stack_mode must be a string")
		}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	return compileConfig(cfg)
}

func compileConfig(cfg Config) (Config, error) {
	if cfg.RulesStackMode == "" {
		cfg.RulesStackMode = rulesStackModeOff
	}
	switch cfg.RulesStackMode {
	case rulesStackModeOff, rulesStackModeSpecificFirst, rulesStackModeGlobalFirst:
	default:
		return Config{}, fmt.Errorf("invalid rules_stack_mode %q", cfg.RulesStackMode)
	}

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
	cfg.compiled = true
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

const callerPatternCacheGenerationSize = 16 << 10

type callerPatternCacheState struct {
	current  map[callerPatternCacheKey]bool
	previous map[callerPatternCacheKey]bool
}

var (
	loadedConfigMu sync.RWMutex
	loadedCfg      = defaultConfig()

	// ponytail: two fixed generations cap cross-RPC caller state; remove this cache when CPA carries route decisions into executor calls.
	callerPatternCacheMu sync.RWMutex
	callerPatternCache   = callerPatternCacheState{current: make(map[callerPatternCacheKey]bool, callerPatternCacheGenerationSize)}

	hostAPIMu      sync.RWMutex
	hostCallbackFn hostCallback
)

type hostCallback func(method string, request []byte) ([]byte, error)

func loadedConfig() Config {
	loadedConfigMu.RLock()
	defer loadedConfigMu.RUnlock()
	return loadedCfg
}

func resetCallerPatternCache() {
	callerPatternCacheMu.Lock()
	callerPatternCache = callerPatternCacheState{current: make(map[callerPatternCacheKey]bool, callerPatternCacheGenerationSize)}
	callerPatternCacheMu.Unlock()
}

func publishLoadedConfig(cfg Config) {
	loadedConfigMu.Lock()
	loadedCfg = cfg
	loadedConfigMu.Unlock()
}

func setLoadedConfigForTest(cfg Config) {
	compiled, err := compileConfig(cfg)
	if err != nil {
		panic(err)
	}
	resetCallerPatternCache()
	publishLoadedConfig(compiled)
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
	publishLoadedConfig(cfg)
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

type ruleSelection struct {
	first  []rule
	second []rule
}

func selectRules(cfg Config, format string) ruleSelection {
	var specific []rule
	switch format {
	case "claude":
		specific = cfg.claudeMessagesRules
	case "openai-response":
		specific = cfg.codexResponsesRules
	case "openai":
		specific = cfg.openAICompletionsRules
	case "gemini", "interactions":
		return ruleSelection{first: cfg.globalRules}
	default:
		return ruleSelection{}
	}
	if len(specific) == 0 {
		return ruleSelection{first: cfg.globalRules}
	}

	selection := ruleSelection{first: specific}
	switch cfg.RulesStackMode {
	case rulesStackModeSpecificFirst:
		selection.second = cfg.globalRules
	case rulesStackModeGlobalFirst:
		selection.first = cfg.globalRules
		selection.second = specific
	}
	if len(selection.first) == 0 {
		selection.first = selection.second
		selection.second = nil
	}
	return selection
}

func callerAPIKeyForSelectedRules(cfg Config, format string, headers http.Header, query url.Values, scope string) string {
	selection := selectRules(cfg, format)
	for i := range selection.first {
		if len(selection.first[i].callerPattern) > 0 {
			return callerAPIKey(headers, query, scope)
		}
	}
	for i := range selection.second {
		if len(selection.second[i].callerPattern) > 0 {
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

func canonicalizeHeaders(headers http.Header) {
	for key, values := range headers {
		canonical := http.CanonicalHeaderKey(key)
		if canonical == key {
			continue
		}
		headers[canonical] = append(headers[canonical], values...)
		delete(headers, key)
	}
}

func handleModelRoute(raw []byte) ([]byte, error) {
	var req modelRouteRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	canonicalizeHeaders(req.Headers)
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
	if !cfg.compiled {
		var err error
		cfg, err = compileConfig(cfg)
		if err != nil {
			return routeDecision{}, err
		}
	}
	selection := selectRules(cfg, format)
	if len(selection.first) == 0 {
		return routeDecision{}, nil
	}
	mapped, matched, err := applyRules(model, scope, key, selection.first)
	if err != nil {
		return routeDecision{}, err
	}
	if len(selection.second) > 0 {
		var secondMatched bool
		mapped, secondMatched, err = applyRules(mapped, scope, key, selection.second)
		if err != nil {
			return routeDecision{}, err
		}
		matched = matched || secondMatched
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

func releaseExecutorStreamSetup(req *executorRPCRequest) {
	if req == nil {
		return
	}
	req.OriginalRequest = nil
	req.Headers = nil
	req.Query = nil
	req.Metadata = nil
}

func emitRewritten(chunks [][]byte, batch bool, emit func([]byte) error) error {
	if !batch {
		for _, chunk := range chunks {
			if len(chunk) > 0 {
				if err := emit(chunk); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if len(chunks) == 1 {
		if len(chunks[0]) == 0 {
			return nil
		}
		return emit(chunks[0])
	}
	total := 0
	for _, chunk := range chunks {
		total += len(chunk)
	}
	if total == 0 {
		return nil
	}
	batchPayload := make([]byte, 0, total)
	for _, chunk := range chunks {
		batchPayload = append(batchPayload, chunk...)
	}
	return emit(batchPayload)
}

func handleExecutorExecuteStream(raw []byte, call hostCaller) ([]byte, error) {
	var req executorRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	canonicalizeHeaders(req.Headers)
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

type executorStream struct {
	pluginStreamID    string
	hostStreamID      string
	originalModel     string
	format            string
	frameRawJSONAsSSE bool
	call              hostCaller
	closeHostOnce     sync.Once
	closeHostErr      error
}

var executorStreamLifecycle = struct {
	mu        sync.Mutex
	stopping  bool
	preparing int
	shutdowns int
	active    map[*executorStream]struct{}
	wg        sync.WaitGroup
}{active: make(map[*executorStream]struct{})}

func resetExecutorStreamLifecycle() {
	executorStreamLifecycle.mu.Lock()
	defer executorStreamLifecycle.mu.Unlock()
	if len(executorStreamLifecycle.active) != 0 || executorStreamLifecycle.preparing != 0 || executorStreamLifecycle.shutdowns != 0 {
		return
	}
	executorStreamLifecycle.stopping = false
	executorStreamLifecycle.active = make(map[*executorStream]struct{})
}

func beginExecutorStreamPreparation() bool {
	executorStreamLifecycle.mu.Lock()
	defer executorStreamLifecycle.mu.Unlock()
	if executorStreamLifecycle.stopping {
		return false
	}
	executorStreamLifecycle.preparing++
	executorStreamLifecycle.wg.Add(1)
	return true
}

func finishExecutorStreamPreparation() {
	executorStreamLifecycle.mu.Lock()
	executorStreamLifecycle.preparing--
	executorStreamLifecycle.mu.Unlock()
	executorStreamLifecycle.wg.Done()
}

func admitExecutorStream(stream *executorStream) bool {
	executorStreamLifecycle.mu.Lock()
	defer executorStreamLifecycle.mu.Unlock()
	if executorStreamLifecycle.stopping {
		return false
	}
	executorStreamLifecycle.preparing--
	executorStreamLifecycle.active[stream] = struct{}{}
	return true
}

func unregisterExecutorStream(stream *executorStream) {
	executorStreamLifecycle.mu.Lock()
	delete(executorStreamLifecycle.active, stream)
	executorStreamLifecycle.mu.Unlock()
	executorStreamLifecycle.wg.Done()
}

func shutdownExecutorStreams() {
	executorStreamLifecycle.mu.Lock()
	executorStreamLifecycle.stopping = true
	executorStreamLifecycle.shutdowns++
	streams := make([]*executorStream, 0, len(executorStreamLifecycle.active))
	for stream := range executorStreamLifecycle.active {
		streams = append(streams, stream)
	}
	executorStreamLifecycle.mu.Unlock()
	for _, stream := range streams {
		_ = stream.closeHost()
	}
	executorStreamLifecycle.wg.Wait()
	executorStreamLifecycle.mu.Lock()
	executorStreamLifecycle.shutdowns--
	executorStreamLifecycle.mu.Unlock()
}

func (s *executorStream) closeHost() error {
	s.closeHostOnce.Do(func() {
		_, s.closeHostErr = s.call(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: s.hostStreamID})
	})
	return s.closeHostErr
}

func (s *executorStream) closePlugin(errText string) error {
	_, err := s.call(pluginabi.MethodHostStreamClose, struct {
		StreamID string `json:"stream_id"`
		Error    string `json:"error,omitempty"`
	}{StreamID: s.pluginStreamID, Error: errText})
	return err
}

func (s *executorStream) emit(payload []byte) error {
	_, err := s.call(pluginabi.MethodHostStreamEmit, struct {
		StreamID string `json:"stream_id"`
		Payload  []byte `json:"payload"`
	}{StreamID: s.pluginStreamID, Payload: payload})
	return err
}

func prepareExecutorStream(req *executorRPCRequest, call hostCaller) (*executorStream, http.Header, error) {
	scope := callerScopeFromMetadata(req.Metadata)
	cfg := loadedConfig()
	decision, err := routeModel(cfg, req.SourceFormat, req.Model, scope, callerAPIKeyForSelectedRules(cfg, req.SourceFormat, req.Headers, req.Query, scope))
	if err != nil {
		return nil, nil, fmt.Errorf("route stream: %w", err)
	}
	if !decision.Handled {
		return nil, nil, fmt.Errorf("route stream: unhandled model route for %q", req.Model)
	}
	body, changed, err := rewriteRequestModel(req.OriginalRequest, decision.UpstreamModel)
	if err != nil {
		return nil, nil, fmt.Errorf("rewrite stream request: %w", err)
	}
	if changed {
		req.Headers.Del("Content-Length")
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
	releaseExecutorStreamSetup(req)
	if err != nil {
		return nil, nil, fmt.Errorf("execute stream: %w", err)
	}
	var partial struct {
		StreamID string `json:"stream_id"`
	}
	_ = json.Unmarshal(hostRaw, &partial)
	var hostResp struct {
		pluginapi.HostModelStreamResponse
		Body []byte `json:"body"`
	}
	if err := json.Unmarshal(hostRaw, &hostResp); err != nil {
		decodeErr := fmt.Errorf("decode host stream response: %w", err)
		if partial.StreamID == "" {
			return nil, nil, decodeErr
		}
		_, closeErr := call(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: partial.StreamID})
		if closeErr != nil {
			return nil, nil, errors.Join(decodeErr, fmt.Errorf("close host stream: %w", closeErr))
		}
		return nil, nil, decodeErr
	}
	stream := &executorStream{
		pluginStreamID: req.StreamID,
		hostStreamID:   hostResp.StreamID,
		originalModel:  decision.OriginalModel,
		format:         req.Format,
		call:           call,
	}
	if hostResp.StatusCode >= http.StatusBadRequest {
		statusErr := fmt.Errorf("execute stream status %d: %s", hostResp.StatusCode, string(hostResp.Body))
		if stream.hostStreamID != "" {
			if closeErr := stream.closeHost(); closeErr != nil {
				return nil, nil, errors.Join(statusErr, fmt.Errorf("close host stream: %w", closeErr))
			}
		}
		return nil, nil, statusErr
	}
	if stream.hostStreamID == "" {
		return nil, nil, fmt.Errorf("missing host stream id")
	}
	headers := hostResp.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	canonicalizeHeaders(headers)
	headers.Del("Content-Length")
	headers.Del("Transfer-Encoding")
	if headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", "text/event-stream")
	}
	stream.frameRawJSONAsSSE = isEventStreamContentType(headers.Get("Content-Type"))
	return stream, headers, nil
}

func startExecutorStream(req executorRPCRequest, call hostCaller, closeStream func(string, string) error) ([]byte, error) {
	if !beginExecutorStreamPreparation() {
		return nil, fmt.Errorf("executor stream lifecycle is stopping")
	}
	stream, headers, err := prepareExecutorStream(&req, call)
	if err != nil {
		finishExecutorStreamPreparation()
		return nil, err
	}
	setup := pluginapi.ExecutorStreamResponse{Headers: headers}
	response, err := json.Marshal(struct {
		Headers http.Header `json:"headers"`
	}{Headers: setup.Headers})
	if err != nil {
		_ = stream.closeHost()
		finishExecutorStreamPreparation()
		return nil, err
	}
	if !admitExecutorStream(stream) {
		lifecycleErr := fmt.Errorf("executor stream lifecycle is stopping")
		if closeErr := stream.closeHost(); closeErr != nil {
			lifecycleErr = errors.Join(lifecycleErr, fmt.Errorf("close host stream: %w", closeErr))
		}
		finishExecutorStreamPreparation()
		return nil, lifecycleErr
	}
	go func() {
		defer unregisterExecutorStream(stream)
		if err := runStreamForward(stream); err != nil {
			_ = closeStream(stream.pluginStreamID, err.Error())
		}
	}()
	return response, nil
}

func joinStreamErrors(primary error, cleanup ...error) error {
	errs := make([]error, 0, len(cleanup)+1)
	if primary != nil {
		errs = append(errs, primary)
	}
	for _, err := range cleanup {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *executorStream) processPayload(rewriter *streamChunkRewriter, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	chunks, err := rewriter.Write(payload)
	if err != nil {
		return fmt.Errorf("rewrite stream chunk: %w", err)
	}
	if err := emitRewritten(chunks, rewriter.frameRawJSONAsSSE, s.emit); err != nil {
		return fmt.Errorf("emit stream chunk: %w", err)
	}
	return nil
}

func (s *executorStream) flushAndEmit(rewriter *streamChunkRewriter, cleanCompletion bool) error {
	var (
		flushed [][]byte
		err     error
	)
	if cleanCompletion {
		flushed, err = rewriter.Finish()
	} else {
		flushed, err = rewriter.Flush()
	}
	if err != nil {
		return fmt.Errorf("flush stream rewriter: %w", err)
	}
	if err := emitRewritten(flushed, rewriter.frameRawJSONAsSSE, s.emit); err != nil {
		return fmt.Errorf("emit flushed stream chunk: %w", err)
	}
	return nil
}

func (s *executorStream) finish(rewriter *streamChunkRewriter, primary error, payloadErr error, closePlugin bool, cleanCompletion bool) error {
	cleanup := make([]error, 0, 2)
	if payloadErr != nil {
		cleanup = append(cleanup, payloadErr)
	}
	if err := s.flushAndEmit(rewriter, cleanCompletion); err != nil {
		cleanup = append(cleanup, err)
	}
	if err := s.closeHost(); err != nil {
		cleanup = append(cleanup, fmt.Errorf("close host stream: %w", err))
	}
	firstErr := joinStreamErrors(primary, cleanup...)
	if !closePlugin {
		return firstErr
	}
	errText := ""
	if firstErr != nil {
		errText = firstErr.Error()
	}
	if err := s.closePlugin(errText); err != nil {
		return joinStreamErrors(firstErr, fmt.Errorf("close plugin stream: %w", err))
	}
	return nil
}

func runStreamForward(stream *executorStream) error {
	rewriter := newStreamChunkRewriter(stream.originalModel)
	rewriter.format = stream.format
	rewriter.frameRawJSONAsSSE = stream.frameRawJSONAsSSE
	for {
		readRaw, err := stream.call(pluginabi.MethodHostModelStreamRead, pluginapi.HostModelStreamReadRequest{StreamID: stream.hostStreamID})
		if err != nil {
			return stream.finish(rewriter, fmt.Errorf("read host stream: %w", err), nil, false, false)
		}
		var chunk pluginapi.HostModelStreamReadResponse
		if err := json.Unmarshal(readRaw, &chunk); err != nil {
			return stream.finish(rewriter, fmt.Errorf("decode host stream chunk: %w", err), nil, false, false)
		}
		payloadErr := stream.processPayload(rewriter, chunk.Payload)
		if chunk.Error != "" {
			return stream.finish(rewriter, errors.New(chunk.Error), payloadErr, true, false)
		}
		if payloadErr != nil || chunk.Done {
			return stream.finish(rewriter, payloadErr, nil, true, chunk.Done && payloadErr == nil)
		}
	}
}

func handleExecutorExecute(raw []byte, call hostCaller) ([]byte, error) {
	var req executorRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	canonicalizeHeaders(req.Headers)
	scope := callerScopeFromMetadata(req.Metadata)
	cfg := loadedConfig()
	decision, err := routeModel(cfg, req.SourceFormat, req.Model, scope, callerAPIKeyForSelectedRules(cfg, req.SourceFormat, req.Headers, req.Query, scope))
	if err != nil {
		return nil, err
	}
	if !decision.Handled {
		return nil, fmt.Errorf("unhandled model route for %q", req.Model)
	}
	body, changed, err := rewriteRequestModel(req.OriginalRequest, decision.UpstreamModel)
	if err != nil {
		return nil, err
	}
	if changed {
		req.Headers.Del("Content-Length")
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
	canonicalizeHeaders(hostResp.Headers)
	if hostResp.StatusCode >= 400 {
		return nil, fmt.Errorf("host.model.execute status %d: %s", hostResp.StatusCode, string(hostResp.Body))
	}
	payload, changed, err := restoreResponseModel(hostResp.Body, decision.OriginalModel)
	if err != nil {
		return nil, err
	}
	if changed {
		hostResp.Headers.Del("Content-Length")
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
	GlobalRules            string    `yaml:"global_rules"`
	ClaudeMessagesRules    string    `yaml:"claude_messages_rules"`
	CodexResponsesRules    string    `yaml:"codex_responses_rules"`
	OpenAICompletionsRules string    `yaml:"openai_completions_rules"`
	RulesStackMode         yaml.Node `yaml:"rules_stack_mode"`
}

func decodeLifecycleConfig(raw []byte) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	var lifecycle map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &lifecycle); err != nil {
		return append(json.RawMessage(nil), trimmed...), false, nil
	}
	encoded, ok := lifecycle["config_yaml"]
	if !ok {
		return append(json.RawMessage(nil), trimmed...), false, nil
	}
	var configYAML *string
	if err := json.Unmarshal(encoded, &configYAML); err != nil || configYAML == nil {
		if err != nil {
			return nil, true, fmt.Errorf("config_yaml must be a string: %w", err)
		}
		return nil, true, fmt.Errorf("config_yaml must be a string")
	}
	decoded, err := base64.StdEncoding.DecodeString(*configYAML)
	if err != nil {
		return nil, true, err
	}
	var yamlConfig lifecycleYAMLConfig
	decoder := yaml.NewDecoder(bytes.NewReader(decoded))
	if err := decoder.Decode(&yamlConfig); err != nil && err != io.EOF {
		return nil, true, err
	} else if err == nil {
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			if err == nil {
				return nil, true, fmt.Errorf("config_yaml must contain one YAML document")
			}
			return nil, true, fmt.Errorf("trailing YAML: %w", err)
		}
	}
	rulesStackMode := ""
	if node := yamlConfig.RulesStackMode; node.Kind != 0 {
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
			return nil, true, fmt.Errorf("rules_stack_mode must be a string")
		}
		rulesStackMode = node.Value
	}
	cfgRaw, err := json.Marshal(Config{
		GlobalRules:            yamlConfig.GlobalRules,
		ClaudeMessagesRules:    yamlConfig.ClaudeMessagesRules,
		CodexResponsesRules:    yamlConfig.CodexResponsesRules,
		OpenAICompletionsRules: yamlConfig.OpenAICompletionsRules,
		RulesStackMode:         rulesStackMode,
	})
	if err != nil {
		return nil, true, err
	}
	return cfgRaw, true, nil
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
	if !json.Valid(body) {
		return bytes.Clone(body), false, nil
	}
	start, end, found, duplicate := findTopLevelModelValue(body)
	if !found {
		return bytes.Clone(body), false, nil
	}
	if duplicate {
		return rewriteTopLevelModelCanonical(body, model)
	}
	rawValue := body[start:end]
	if len(rawValue) < 2 || rawValue[0] != '"' || rawValue[len(rawValue)-1] != '"' {
		return bytes.Clone(body), false, nil
	}
	var current string
	if err := json.Unmarshal(rawValue, &current); err != nil || current == model {
		return bytes.Clone(body), false, nil
	}
	replacement, err := json.Marshal(model)
	if err != nil {
		return nil, false, err
	}
	out := make([]byte, len(body)-end+start+len(replacement))
	n := copy(out, body[:start])
	n += copy(out[n:], replacement)
	copy(out[n:], body[end:])
	return out, true, nil
}

func rewriteTopLevelModelCanonical(body []byte, model string) ([]byte, bool, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return bytes.Clone(body), false, nil
	}
	rawValue, ok := doc["model"]
	if !ok || len(rawValue) < 2 || rawValue[0] != '"' || rawValue[len(rawValue)-1] != '"' {
		return bytes.Clone(body), false, nil
	}
	var current string
	if err := json.Unmarshal(rawValue, &current); err != nil {
		return bytes.Clone(body), false, nil
	}
	replacement, err := json.Marshal(model)
	if err != nil {
		return nil, false, err
	}
	doc["model"] = replacement
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func findTopLevelModelValue(body []byte) (int, int, bool, bool) {
	i := skipTopLevelModelJSONSpace(body, 0)
	if i == len(body) || body[i] != '{' {
		return 0, 0, false, false
	}
	i++
	start, end, found, duplicate := 0, 0, false, false
	for {
		i = skipTopLevelModelJSONSpace(body, i)
		if i == len(body) || body[i] == '}' {
			return start, end, found, duplicate
		}
		keyStart := i
		i = skipTopLevelModelJSONString(body, i)
		keyMatches := topLevelModelKey(body[keyStart:i])
		i = skipTopLevelModelJSONSpace(body, i)
		if i == len(body) || body[i] != ':' {
			return 0, 0, false, false
		}
		i = skipTopLevelModelJSONSpace(body, i+1)
		if i == len(body) {
			return 0, 0, false, false
		}
		valueStart := i
		i = skipTopLevelModelJSONValue(body, i)
		if keyMatches {
			duplicate = duplicate || found
			start, end, found = valueStart, i, true
		}
		i = skipTopLevelModelJSONSpace(body, i)
		if i == len(body) || body[i] == '}' {
			return start, end, found, duplicate
		}
		if body[i] != ',' {
			return 0, 0, false, false
		}
		i++
	}
}

func topLevelModelKey(raw []byte) bool {
	if bytes.Equal(raw, []byte(`"model"`)) {
		return true
	}
	if bytes.IndexByte(raw, '\\') < 0 {
		return false
	}
	var key string
	return json.Unmarshal(raw, &key) == nil && key == "model"
}

func skipTopLevelModelJSONSpace(body []byte, i int) int {
	for i < len(body) {
		switch body[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

func skipTopLevelModelJSONString(body []byte, i int) int {
	escaped := false
	for i++; i < len(body); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch body[i] {
		case '\\':
			escaped = true
		case '"':
			return i + 1
		}
	}
	return i
}

func skipTopLevelModelJSONValue(body []byte, i int) int {
	switch body[i] {
	case '"':
		return skipTopLevelModelJSONString(body, i)
	case '{', '[':
		depth := 0
		inString, escaped := false, false
		for ; i < len(body); i++ {
			switch {
			case inString && escaped:
				escaped = false
			case inString && body[i] == '\\':
				escaped = true
			case inString && body[i] == '"':
				inString = false
			case !inString && body[i] == '"':
				inString = true
			case !inString && (body[i] == '{' || body[i] == '['):
				depth++
			case !inString && (body[i] == '}' || body[i] == ']'):
				depth--
				if depth == 0 {
					return i + 1
				}
			}
		}
		return i
	default:
		for i < len(body) && body[i] != ',' && body[i] != '}' && body[i] != ']' && body[i] != ' ' && body[i] != '\t' && body[i] != '\r' && body[i] != '\n' {
			i++
		}
		return i
	}
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func escapedResponseModelKey(raw []byte) bool {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return false
	}
	model := "model"
	modelVersion := "modelVersion"
	decoded := 0
	for i := 1; i < len(raw)-1; i++ {
		c := raw[i]
		if c == '\\' {
			i++
			if i >= len(raw)-1 {
				return false
			}
			switch raw[i] {
			case '"', '\\', '/':
				c = raw[i]
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'u':
				if i+4 >= len(raw) {
					return false
				}
				value := uint16(0)
				for j := 1; j <= 4; j++ {
					nibble, ok := hexNibble(raw[i+j])
					if !ok {
						return false
					}
					value = value<<4 | uint16(nibble)
				}
				i += 4
				if value > 0x7f {
					return false
				}
				c = byte(value)
			default:
				return false
			}
		}
		if decoded >= len(modelVersion) {
			return false
		}
		matchesModel := decoded < len(model) && model[decoded] == c
		matchesVersion := modelVersion[decoded] == c
		if !matchesModel && !matchesVersion {
			return false
		}
		decoded++
	}
	return decoded == len(model) || decoded == len(modelVersion)
}

type responseModelMarkerScanner struct {
	inString    bool
	escaped     bool
	hadEscape   bool
	afterString bool
	key         [80]byte
	keyLen      int
	keyOverflow bool
}

func (s *responseModelMarkerScanner) feed(b byte) bool {
	if s.afterString {
		switch b {
		case ' ', '\t', '\r', '\n':
			return false
		case ':':
			s.afterString = false
			if !s.hadEscape || s.keyOverflow {
				return false
			}
			return escapedResponseModelKey(s.key[:s.keyLen])
		default:
			s.afterString = false
		}
	}

	if !s.inString {
		if b != '"' {
			return false
		}
		s.inString = true
		s.escaped = false
		s.hadEscape = false
		s.keyLen = 0
		s.keyOverflow = false
	}
	if s.keyLen < len(s.key) {
		s.key[s.keyLen] = b
		s.keyLen++
	} else {
		s.keyOverflow = true
	}
	if s.escaped {
		s.escaped = false
		return false
	}
	if b == '\\' {
		s.escaped = true
		s.hadEscape = true
		return false
	}
	if b == '"' && s.keyLen > 1 {
		s.inString = false
		s.afterString = true
	}
	return false
}

func mightContainResponseModelField(body []byte) bool {
	if bytes.Contains(body, []byte(`"model"`)) || bytes.Contains(body, []byte(`"modelVersion"`)) {
		return true
	}
	if !bytes.Contains(body, []byte{'\\'}) {
		return false
	}
	var scanner responseModelMarkerScanner
	for _, b := range body {
		if scanner.feed(b) {
			return true
		}
	}
	return false
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
	out, changed, _, err := rewriteResponseModelFieldsWithReplacementChecked(body, model, replacement)
	return out, changed, err
}

func rewriteResponseModelFieldsWithReplacementChecked(body []byte, model string, replacement json.RawMessage) ([]byte, bool, bool, error) {
	start := skipTopLevelModelJSONSpace(body, 0)
	if start == len(body) {
		return bytes.Clone(body), false, false, nil
	}
	switch body[start] {
	case '{':
		return rewriteResponseModelObjectWithReplacementChecked(body, model, replacement)
	case '[':
		return rewriteResponseModelArrayWithReplacementChecked(body, model, replacement)
	default:
		if !json.Valid(body) {
			return bytes.Clone(body), false, false, nil
		}
		return bytes.Clone(body), false, true, nil
	}
}

func rewriteResponseModelObjectWithReplacementChecked(body []byte, model string, replacement json.RawMessage) ([]byte, bool, bool, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return bytes.Clone(body), false, false, nil
	}
	changed := rewriteRawStringField(doc, "model", model, replacement)
	changed = rewriteRawStringField(doc, "modelVersion", model, replacement) || changed
	messageChanged, err := rewriteNestedRawStringFields(doc, "message", model, replacement, "model")
	if err != nil {
		return nil, false, true, err
	}
	changed = messageChanged || changed
	responseChanged, err := rewriteNestedRawStringFields(doc, "response", model, replacement, "model", "modelVersion")
	if err != nil {
		return nil, false, true, err
	}
	changed = responseChanged || changed
	interactionChanged, err := rewriteNestedRawStringFields(doc, "interaction", model, replacement, "model")
	if err != nil {
		return nil, false, true, err
	}
	changed = interactionChanged || changed
	if !changed {
		return bytes.Clone(body), false, true, nil
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false, true, err
	}
	return out, true, true, nil
}

func rewriteResponseModelArrayWithReplacementChecked(body []byte, model string, replacement json.RawMessage) ([]byte, bool, bool, error) {
	if !json.Valid(body) {
		return bytes.Clone(body), false, false, nil
	}
	cursor := skipTopLevelModelJSONSpace(body, 0) + 1
	copyFrom := 0
	changed := false
	var out []byte
	for {
		start := skipTopLevelModelJSONSpace(body, cursor)
		if body[start] == ']' {
			break
		}
		end := skipTopLevelModelJSONValue(body, start)
		if body[start] == '{' {
			restored, elementChanged, _, err := rewriteResponseModelObjectWithReplacementChecked(body[start:end], model, replacement)
			if err != nil {
				return nil, false, true, err
			}
			if elementChanged {
				if !changed {
					out = make([]byte, 0, len(body)-end+start+len(restored))
				}
				out = append(out, body[copyFrom:start]...)
				out = append(out, restored...)
				copyFrom = end
				changed = true
			}
		}
		next := skipTopLevelModelJSONSpace(body, end)
		if body[next] == ']' {
			break
		}
		cursor = next + 1
	}
	if !changed {
		return bytes.Clone(body), false, true, nil
	}
	out = append(out, body[copyFrom:]...)
	return out, true, true, nil
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
	matchesScope := func(candidate string) string {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && callerScope(candidate) == scope {
			return candidate
		}
		return ""
	}
	for _, authorization := range headers.Values("Authorization") {
		parts := strings.SplitN(authorization, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			authorization = parts[1]
		}
		if candidate := matchesScope(authorization); candidate != "" {
			return candidate
		}
	}
	for _, values := range [][]string{
		headers.Values("X-Goog-Api-Key"),
		headers.Values("X-Api-Key"),
		query["key"],
		query["auth_token"],
	} {
		for _, candidate := range values {
			if candidate := matchesScope(candidate); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func defaultConfig() Config {
	return Config{RulesStackMode: rulesStackModeOff, compiled: true}
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

func callerPatternMatch(r *rule, scope, key string) (bool, bool) {
	cacheKey := callerPatternCacheKey{scope: scope, pattern: r.callerPatternText}
	callerPatternCacheMu.RLock()
	matched, ok := callerPatternCache.current[cacheKey]
	if !ok {
		matched, ok = callerPatternCache.previous[cacheKey]
	}
	callerPatternCacheMu.RUnlock()
	if ok {
		return matched, true
	}
	if key == "" || callerScope(key) != scope {
		return false, false
	}
	_, matched = matchTokens(key, r.callerPattern)
	callerPatternCacheMu.Lock()
	if cached, exists := callerPatternCache.current[cacheKey]; exists {
		matched = cached
	} else if cached, exists := callerPatternCache.previous[cacheKey]; exists {
		matched = cached
	} else {
		if len(callerPatternCache.current) >= callerPatternCacheGenerationSize {
			callerPatternCache.previous = callerPatternCache.current
			callerPatternCache.current = make(map[callerPatternCacheKey]bool, callerPatternCacheGenerationSize)
		}
		callerPatternCache.current[cacheKey] = matched
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
