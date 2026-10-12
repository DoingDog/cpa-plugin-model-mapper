package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

type sseRewriter struct {
	originalModel      string
	encodedModel       json.RawMessage
	buf                []byte
	scanFrom           int
	bomPrefix          []byte
	bomDone            bool
	started            bool
	trackDone          bool
	sawDone            bool
	logicalEventFormat string
	rawJSONPrefix      int
	delimiterless      delimiterlessResponsesEventScanner
}

type delimiterlessResponsesEventScanner struct {
	disabled            bool
	headerPrefixChecked bool
	headerComplete      bool
	headerStart         int
	headerScan          int
	nativeHeader        bool
	metadataEnd         int
	eventType           string
	dataStart           int
	dataPrefixChecked   bool
	valueReady          bool
	valueStart          int
	jsonScan            int
	jsonStarted         bool
	jsonDepth           int
	inString            bool
	escaped             bool
	completeEnd         int
}

func newSSERewriter(originalModel string) *sseRewriter {
	return &sseRewriter{originalModel: originalModel}
}

func (r *sseRewriter) resetDelimiterlessResponsesEventScanner() {
	r.delimiterless = delimiterlessResponsesEventScanner{}
	r.rawJSONPrefix = 0
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

func (r *sseRewriter) Write(p []byte) ([][]byte, error) {
	p = r.consumeLeadingBOM(p)
	if len(p) == 0 && !r.bomDone && len(r.bomPrefix) > 0 {
		return nil, nil
	}
	if len(p) > 0 {
		r.started = true
	}
	var out [][]byte
	if r.logicalEventFormat == "openai-response" && isResponsesMetadataField(p) && bytes.IndexAny(p, "\r\n") < 0 {
		s := &r.delimiterless
		fieldBoundary := len(r.buf) == 0 || s.metadataEnd == len(r.buf) || s.headerPrefixChecked &&
			(!s.headerComplete && s.headerScan == len(r.buf) || s.headerComplete && (s.dataStart == len(r.buf) || s.completeEnd == len(r.buf)))
		if !s.disabled && fieldBoundary {
			if s.headerPrefixChecked && !s.headerComplete && s.headerScan == len(r.buf) {
				// 独立 metadata 确认前一 header 的字段末尾，JSON 仍由原 scanner 验证。
				r.buf = append(r.buf, '\n')
				s.nativeHeader = true
			}
			r.buf = append(r.buf, p...)
			r.buf = append(r.buf, '\n')
			s.metadataEnd = len(r.buf)
			p = nil
		}
	}
	if r.logicalEventFormat == "openai-response" && (bytes.HasPrefix(p, []byte("data:")) || bytes.HasPrefix(p, []byte("event:")) ||
		isResponsesMetadataField(p) && completeSSEEvents(p)) {
		s := &r.delimiterless
		if !s.disabled && s.completeEnd > 0 && (s.completeEnd == len(r.buf) || s.metadataEnd == len(r.buf)) && s.dataStart == s.headerStart {
			// 下一独立 event/data 或完整 metadata 单位确认前值边界，metadata 保留给下一事件。
			nextFields := r.buf[s.completeEnd:]
			r.buf = r.buf[:s.completeEnd]
			var err error
			out, err = r.drain(true)
			if err != nil {
				return out, err
			}
			r.buf = nextFields
			r.delimiterless.metadataEnd = len(nextFields)
		} else if bytes.HasPrefix(p, []byte("data:")) && !s.disabled && s.headerPrefixChecked && !s.headerComplete && s.headerScan == len(r.buf) {
			eventType := strings.TrimSpace(string(r.buf[s.headerStart+len("event:") : s.headerScan]))
			if strings.HasPrefix(eventType, "response.") || eventType == "error" || eventType == "keepalive" {
				// dataStart == headerScan 记录 callback 间缺少的 LF；验证后才补写。
				s.eventType, s.dataStart, s.headerComplete = eventType, s.headerScan, true
				if strings.IndexFunc(eventType, unicode.IsSpace) >= 0 {
					// opaque metadata 不是单个 type token；独立 data 单位单独验证。
					s.eventType = ""
				}
			}
		}
	}
	r.buf = append(r.buf, p...)
	chunks, err := r.drain(false)
	if len(out) == 0 {
		out = chunks
	} else {
		out = append(out, chunks...)
	}
	if err != nil {
		return out, err
	}
	if len(r.buf)-max(r.delimiterless.completeEnd, r.delimiterless.metadataEnd, r.rawJSONPrefix) > maxPendingStreamBytes {
		// 末尾 CR 可能属于 CRLF，完整 event 等待下一字节或 EOF。
		if _, delimiterLen, _ := findSSEEventDelimiter(r.buf, r.scanFrom, true); delimiterLen > 0 {
			return out, nil
		}
		r.buf = nil
		r.scanFrom = 0
		r.resetDelimiterlessResponsesEventScanner()
		return out, fmt.Errorf("stream pending data exceeds %d bytes", maxPendingStreamBytes)
	}
	return out, nil
}

func (r *sseRewriter) Flush() ([][]byte, error) {
	r.flushLeadingBOM()
	if len(r.buf) == 0 {
		r.resetDelimiterlessResponsesEventScanner()
		return nil, nil
	}
	return r.drain(true)
}

func (r *sseRewriter) drain(eof bool) ([][]byte, error) {
	bufferCap := cap(r.buf)
	var out [][]byte
	consumed := false
	for {
		logicalEnd, logical := 0, false
		if r.logicalEventFormat != "" {
			logicalEnd, logical = r.findDelimiterlessResponsesEventEnd(eof)
		}
		standardEnd, n, next := findSSEEventDelimiter(r.buf, r.scanFrom, eof)
		if logical && (n == 0 || logicalEnd < standardEnd) {
			event := r.buf[:logicalEnd]
			r.buf = r.buf[logicalEnd:]
			r.scanFrom = 0
			consumed = true
			var err error
			out, err = r.rewritePendingEvent(out, event)
			r.resetDelimiterlessResponsesEventScanner()
			if err != nil {
				return out, err
			}
			out = append(out, []byte("\n\n"))
			if eof && len(r.buf) < len("event:") && bytes.HasPrefix([]byte("event:"), r.buf) {
				r.buf = nil
			}
			continue
		}
		if n == 0 {
			r.scanFrom = next
			break
		}
		event := r.buf[:standardEnd]
		delimiter := r.buf[standardEnd : standardEnd+n : standardEnd+n]
		if cap(r.buf) >= 1<<20 {
			delimiter = bytes.Clone(delimiter)
		}
		r.buf = r.buf[standardEnd+n:]
		r.scanFrom = 0
		consumed = true
		var err error
		out, err = r.rewritePendingEvent(out, event)
		r.resetDelimiterlessResponsesEventScanner()
		if err != nil {
			return out, err
		}
		out = append(out, delimiter)
	}
	if eof && len(r.buf) > 0 {
		event := r.buf
		r.buf = nil
		r.scanFrom = 0
		var err error
		out, err = r.rewritePendingEvent(out, event)
		r.resetDelimiterlessResponsesEventScanner()
		if err != nil {
			return out, err
		}
	}
	if len(r.buf) == 0 {
		r.buf = nil
		r.scanFrom = 0
		r.resetDelimiterlessResponsesEventScanner()
	} else if bufferCap > maxPendingStreamBytes && len(r.buf) <= maxPendingStreamBytes || (consumed && bufferCap > 2*len(r.buf)) {
		r.buf = bytes.Clone(r.buf)
	}
	return out, nil
}

func (r *sseRewriter) rewritePendingEvent(out [][]byte, event []byte) ([][]byte, error) {
	s := &r.delimiterless
	if s.completeEnd == 0 || s.dataStart == 0 || s.dataStart != s.headerScan {
		return r.rewriteEvent(out, event)
	}
	// 完整值已验证，仅补写 header 的 LF，不复制整个 JSON。
	originalOutLen := len(out)
	header := make([]byte, s.dataStart+1)
	copy(header, event[:s.dataStart])
	header[s.dataStart] = '\n'
	out = append(out, header)
	out, err := r.rewriteEvent(out, event[s.dataStart:])
	if err != nil {
		return out[:originalOutLen], err
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
		return out, err
	}
	if !changed {
		return appendUnchangedSSEEvent(out, event), nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, restored); err != nil {
		return out, err
	}
	restored = compact.Bytes()

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
			if r.trackDone && bytes.Equal(bytes.TrimSpace(value), []byte("[DONE]")) {
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
				return out[:originalOutLen], err
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

func isResponsesMetadataField(p []byte) bool {
	return bytes.HasPrefix(p, []byte("id:")) || bytes.HasPrefix(p, []byte("retry:")) || bytes.HasPrefix(p, []byte(":"))
}

func (r *sseRewriter) findDelimiterlessResponsesEventEnd(eof bool) (int, bool) {
	buf := r.buf
	s := &r.delimiterless
	if s.disabled {
		return 0, false
	}
	if !s.headerPrefixChecked {
		if r.logicalEventFormat == "openai-response" {
			for isResponsesMetadataField(buf[s.headerScan:]) {
				end, n, _ := sseLineEnding(buf, s.headerScan, eof)
				if n == 0 {
					return 0, false
				}
				s.headerScan = end + n
			}
		}
		s.headerStart = s.headerScan
		field := "event:"
		if r.logicalEventFormat == "openai-response" && len(buf) > s.headerStart && buf[s.headerStart] == 'd' {
			field = "data:"
		}
		if len(buf)-s.headerStart < len(field) {
			if !bytes.Equal(buf[s.headerStart:], []byte(field[:len(buf)-s.headerStart])) {
				s.disabled = true
			}
			return 0, false
		}
		if !bytes.HasPrefix(buf[s.headerStart:], []byte(field)) {
			s.disabled = true
			return 0, false
		}
		s.headerPrefixChecked = true
		s.headerScan += len(field)
		s.headerComplete = field == "data:"
		if s.headerComplete {
			s.dataStart = s.headerStart
		}
	}
	if !s.headerComplete {
		for s.headerScan < len(buf) {
			switch buf[s.headerScan] {
			case '\n':
				s.dataStart = s.headerScan + 1
			case '\r':
				if s.headerScan+1 == len(buf) {
					return 0, false
				}
				s.dataStart = s.headerScan + 1
				if buf[s.headerScan+1] == '\n' {
					s.dataStart++
				}
			default:
				s.headerScan++
				continue
			}
			s.eventType = strings.TrimSpace(string(buf[s.headerStart+len("event:") : s.headerScan]))
			switch r.logicalEventFormat {
			case "openai-response":
				if !strings.HasPrefix(s.eventType, "response.") && s.eventType != "error" && s.eventType != "keepalive" {
					s.disabled = true
					return 0, false
				}
			case "interactions":
				switch s.eventType {
				case "interaction.created", "interaction.status_update", "step.start", "step.delta", "step.stop", "interaction.completed", "done":
				default:
					s.disabled = true
					return 0, false
				}
			}
			s.headerComplete = true
			break
		}
		if !s.headerComplete {
			return 0, false
		}
	}
	if !s.dataPrefixChecked {
		if r.logicalEventFormat == "openai-response" {
			for isResponsesMetadataField(buf[s.dataStart:]) {
				end, n, _ := sseLineEnding(buf, s.dataStart, eof)
				if n == 0 {
					return 0, false
				}
				s.dataStart = end + n
			}
		}
		const dataField = "data:"
		if len(buf) < s.dataStart+len(dataField) {
			return 0, false
		}
		if !bytes.HasPrefix(buf[s.dataStart:], []byte(dataField)) {
			s.disabled = true
			return 0, false
		}
		s.dataPrefixChecked = true
	}
	if !s.valueReady {
		s.valueStart = s.dataStart + len("data:")
		if s.valueStart == len(buf) {
			return 0, false
		}
		if buf[s.valueStart] == ' ' {
			s.valueStart++
		}
		s.jsonScan = s.valueStart
		s.valueReady = true
	}
	if s.completeEnd == 0 {
		for s.jsonScan < len(buf) {
			b := buf[s.jsonScan]
			if !s.jsonStarted {
				if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
					s.jsonScan++
					continue
				}
				if b != '{' && !(r.logicalEventFormat == "interactions" && s.eventType == "done" && b == '[') {
					s.disabled = true
					return 0, false
				}
				s.jsonStarted = true
				s.jsonDepth = 1
				s.jsonScan++
				continue
			}
			if s.inString {
				if s.escaped {
					s.escaped = false
				} else if b == '\\' {
					s.escaped = true
				} else if b == '"' {
					s.inString = false
				}
				s.jsonScan++
				continue
			}
			switch b {
			case '"':
				s.inString = true
			case '{', '[':
				s.jsonDepth++
			case '}', ']':
				s.jsonDepth--
				if s.jsonDepth == 0 {
					s.completeEnd = s.jsonScan + 1
				}
			}
			s.jsonScan++
			if s.completeEnd != 0 {
				break
			}
		}
		if s.completeEnd == 0 {
			return 0, false
		}
		value := buf[s.valueStart:s.completeEnd]
		matched := false
		switch r.logicalEventFormat {
		case "openai-response":
			var typed struct {
				Type string `json:"type"`
			}
			matched = json.Unmarshal(value, &typed) == nil && (s.eventType != "" && typed.Type == s.eventType ||
				s.eventType == "response.done" && typed.Type == "response.completed" ||
				s.eventType == "" && (strings.HasPrefix(typed.Type, "response.") ||
					s.dataStart == s.headerStart && (typed.Type == "error" || typed.Type == "keepalive")))
		case "interactions":
			if s.eventType == "done" {
				matched = bytes.Equal(bytes.TrimSpace(value), []byte("[DONE]"))
			} else {
				var typed struct {
					EventType string `json:"event_type"`
				}
				matched = json.Unmarshal(value, &typed) == nil && typed.EventType == s.eventType
			}
		}
		if !matched {
			s.completeEnd = 0
			s.disabled = true
			return 0, false
		}
	}
	suffix := buf[s.completeEnd:]
	if len(suffix) == 0 {
		return s.completeEnd, eof || s.nativeHeader || s.dataStart > 0 && s.dataStart == s.headerScan
	}
	if s.metadataEnd == len(buf) && s.metadataEnd > s.completeEnd {
		// data-only 后的 metadata 留给下一事件，不提前派发前值。
		return s.completeEnd, eof
	}
	if bytes.HasPrefix(suffix, []byte("event:")) {
		return s.completeEnd, true
	}
	if bytes.HasPrefix([]byte("event:"), suffix) {
		return s.completeEnd, eof
	}
	s.disabled = true
	return 0, false
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

func sseContainsEscapedModelField(p []byte) bool {
	if !bytes.Contains(p, []byte(`\u00`)) {
		return false
	}
	var scanner responseModelMarkerScanner
	for len(p) > 0 {
		line, _, next := splitSSELine(p)
		p = next
		if len(line) == 0 {
			scanner = responseModelMarkerScanner{}
		} else if bytes.HasPrefix(line, []byte("data:")) {
			for _, b := range sseFieldValue(line) {
				if scanner.feed(b) {
					return true
				}
			}
			scanner.feed('\n')
		}
	}
	return false
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

func frameSSEData(p []byte) []byte {
	return frameSSEEvent(p, "")
}

func frameSSEEvent(p []byte, eventType string) []byte {
	var out bytes.Buffer
	if eventType != "" && !strings.ContainsAny(eventType, "\r\n") {
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
