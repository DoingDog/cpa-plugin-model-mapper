package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const maxPendingStreamBytes = 16 << 20

type streamChunkRewriter struct {
	originalModel     string
	format            string
	frameRawJSONAsSSE bool
	batchSSEOutput    bool
	emitFailed        bool
	framedRawJSON     bool
	rawJSONBoundary   bool
	sse               *sseRewriter
	pending           []byte
	rawJSONArray      bool
	rawScan           rawJSONScan
}

type rawJSONScan struct {
	active   bool
	scanFrom int
	depth    int
	inString bool
	escaped  bool
}

func (s *rawJSONScan) advance(p []byte) bool {
	for i := s.scanFrom; i < len(p); i++ {
		b := p[i]
		switch {
		case s.inString && s.escaped:
			s.escaped = false
		case s.inString && b == '\\':
			s.escaped = true
		case s.inString && b == '"':
			s.inString = false
			if s.depth == 0 {
				return true
			}
		case !s.inString && b == '"':
			s.inString = true
		case !s.inString && (b == '{' || b == '['):
			s.depth++
		case !s.inString && (b == '}' || b == ']'):
			s.depth--
			if s.depth == 0 {
				return true
			}
		}
	}
	s.scanFrom = len(p)
	return false
}

func newStreamChunkRewriter(originalModel string) *streamChunkRewriter {
	return &streamChunkRewriter{
		originalModel: originalModel,
		sse:           newSSERewriter(originalModel),
	}
}

func (r *streamChunkRewriter) retainPending(p []byte) error {
	r.rawScan = rawJSONScan{}
	incompleteBytes := len(p)
	if incompleteBytes > maxPendingStreamBytes && r.frameRawJSONAsSSE && couldStartJSONValue(p) {
		_, consumed, ok, _ := splitJSONValues(p, false, false)
		if ok {
			// 完整单位等待 SSE 分类或 EOF，只限制未完成 suffix。
			incompleteBytes = len(p) - consumed
		}
	}
	if incompleteBytes > maxPendingStreamBytes {
		r.pending = nil
		return fmt.Errorf("stream pending data exceeds %d bytes", maxPendingStreamBytes)
	}
	r.pending = bytes.Clone(p)
	start := skipTopLevelModelJSONSpace(r.pending, 0)
	if start < len(r.pending) && (r.pending[start] == '{' || r.pending[start] == '[' || r.pending[start] == '"') {
		scan := rawJSONScan{active: true, scanFrom: start}
		if !scan.advance(r.pending) {
			r.rawScan = scan
		}
	}
	return nil
}

func (r *streamChunkRewriter) Write(p []byte) ([][]byte, error) {
	r.batchSSEOutput = r.frameRawJSONAsSSE
	r.sse.trackDone = r.format == "openai"
	switch r.format {
	case "openai-response", "interactions":
		r.sse.logicalEventFormat = r.format
	default:
		r.sse.logicalEventFormat = ""
	}
	if !r.sse.bomDone {
		p = r.sse.consumeLeadingBOM(p)
		if len(p) == 0 && !r.sse.bomDone {
			return nil, nil
		}
	}
	if r.rawScan.active {
		r.pending = append(r.pending, p...)
		if !r.rawScan.advance(r.pending) {
			if len(r.pending) > maxPendingStreamBytes {
				r.pending = nil
				r.rawScan = rawJSONScan{}
				return nil, fmt.Errorf("stream pending data exceeds %d bytes", maxPendingStreamBytes)
			}
			return nil, nil
		}
		p, r.pending = r.pending, nil
		r.rawScan = rawJSONScan{}
	} else if len(r.pending) > 0 {
		p = append(r.pending, p...)
		r.pending = nil
	}
	if len(r.sse.buf) > 0 {
		r.batchSSEOutput = true
		return r.sse.Write(p)
	}
	if r.rawJSONBoundary {
		p = bytes.TrimLeft(p, " \t\r\n")
		if len(p) == 0 {
			return nil, nil
		}
		r.rawJSONBoundary = false
	}
	if r.rawJSONArray {
		return r.writeRawJSONArray(p, false)
	}
	start := skipTopLevelModelJSONSpace(p, 0)
	if !r.frameRawJSONAsSSE && start < len(p) && p[start] == '[' {
		return r.writeRawJSONArray(p, true)
	}
	if r.frameRawJSONAsSSE && !couldStartJSONValue(p) && (r.sse.logicalEventFormat == "" || !bytes.Contains(p, []byte("event:"))) && completeSSEEvents(p) && !mightContainResponseModelField(p) && !sseContainsEscapedModelField(p) {
		r.sse.started = true
		if r.sse.trackDone {
			r.sse.sawDone = r.sse.sawDone || hasSSEDoneEvent(p)
		}
		return [][]byte{bytes.Clone(p)}, nil
	}
	if r.frameRawJSONAsSSE && isColonlessSSEChunk(p) {
		return r.sse.Write(p)
	}
	if r.format == "openai-response" && isResponsesExtensionField(p) {
		r.batchSSEOutput = true
		return r.sse.Write(p)
	}
	trimmed := bytes.TrimSpace(p)
	if len(trimmed) > 0 && trimmed[0] != '{' && trimmed[0] != '[' && couldStartJSONValue(p) {
		if r.sse.started || isSSEChunk(p) {
			if r.frameRawJSONAsSSE && len(p) > maxPendingStreamBytes {
				// 完整 scalar 转为 SSE unknown field 后仍不计入未完成 suffix 限额。
				_, r.sse.rawJSONPrefix, _, _ = splitJSONValues(p, false, false)
			}
			r.batchSSEOutput = true
			return r.sse.Write(p)
		}
		if r.frameRawJSONAsSSE {
			if err := r.retainPending(p); err != nil {
				chunks, _, _, _, rewriteErr := r.tryRawJSONChunks(p, true)
				return chunks, joinStreamErrors(rewriteErr, err)
			}
			return nil, nil
		}
	}
	if couldStartJSONValue(p) {
		chunks, consumed, ok, incomplete, err := r.tryRawJSONChunks(p, false)
		if err != nil {
			return chunks, err
		}
		if ok {
			if incomplete {
				if err := r.retainPending(p[consumed:]); err != nil {
					completed, _, completedOK, _, rewriteErr := r.tryRawJSONChunks(p, true)
					if completedOK {
						chunks = completed
					}
					return chunks, joinStreamErrors(rewriteErr, err)
				}
			} else if consumed < len(p) {
				sseChunks, err := r.writeSSESuffix(p[consumed:])
				return append(chunks, sseChunks...), err
			}
			return chunks, nil
		}
		if incomplete {
			if err := r.retainPending(p); err != nil {
				return nil, err
			}
			return nil, nil
		}
	}
	if isSSEChunk(p) {
		r.batchSSEOutput = true
		return r.sse.Write(p)
	}
	if isIncompleteSSEPrefix(p) {
		if err := r.retainPending(p); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if len(p) > 0 && len(bytes.Trim(p, " \t\r\n")) == 0 {
		if err := r.retainPending(p); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if r.frameRawJSONAsSSE || r.format != "" {
		r.batchSSEOutput = true
		return r.sse.Write(p)
	}
	return r.rawJSONChunks(p)
}

func (r *streamChunkRewriter) writeRawJSONArray(p []byte, opening bool) ([][]byte, error) {
	var out [][]byte
	cursor := 0
	if opening {
		start := skipTopLevelModelJSONSpace(p, 0)
		out = append(out, bytes.Clone(p[:start+1]))
		cursor = start + 1
		r.rawJSONArray = true
	}
	for cursor < len(p) {
		start := skipTopLevelModelJSONSpace(p, cursor)
		if start == len(p) {
			out = append(out, bytes.Clone(p[cursor:start]))
			return out, nil
		}
		if p[start] == ',' {
			out = append(out, bytes.Clone(p[cursor:start+1]))
			cursor = start + 1
			continue
		}
		if p[start] == ']' {
			out = append(out, bytes.Clone(p[cursor:start+1]))
			r.rawJSONArray = false
			if start+1 < len(p) {
				suffix, err := r.Write(p[start+1:])
				r.batchSSEOutput = false
				return append(out, suffix...), err
			}
			return out, nil
		}

		end := skipTopLevelModelJSONValue(p, start)
		next := skipTopLevelModelJSONSpace(p, end)
		complete := next < len(p)
		if !complete && (p[start] == '{' || p[start] == '[' || p[start] == '"') {
			complete = json.Valid(p[start:end])
		}
		if !complete {
			if err := r.retainPending(p[cursor:]); err != nil {
				return out, err
			}
			return out, nil
		}
		if !json.Valid(p[start:end]) || next < len(p) && p[next] != ',' && p[next] != ']' {
			out = append(out, bytes.Clone(p[cursor:]))
			r.rawJSONArray = false
			return out, nil
		}

		restored := bytes.Clone(p[start:end])
		if p[start] == '{' {
			var err error
			restored, _, err = r.sse.restoreResponseModel(p[start:end])
			if err != nil {
				return out, err
			}
		}
		capacity := start - cursor + len(restored) + next - end
		if next < len(p) {
			capacity++
		}
		chunk := make([]byte, 0, capacity)
		chunk = append(chunk, p[cursor:start]...)
		chunk = append(chunk, restored...)
		chunk = append(chunk, p[end:next]...)
		cursor = next
		if cursor < len(p) {
			chunk = append(chunk, p[cursor])
			cursor++
		}
		out = append(out, chunk)
		if next < len(p) && p[next] == ']' {
			r.rawJSONArray = false
			if cursor < len(p) {
				suffix, err := r.Write(p[cursor:])
				r.batchSSEOutput = false
				return append(out, suffix...), err
			}
			return out, nil
		}
	}
	return out, nil
}

func (r *streamChunkRewriter) writeSSESuffix(p []byte) ([][]byte, error) {
	if r.frameRawJSONAsSSE {
		p = bytes.TrimLeft(p, " \t\r\n")
	}
	r.rawJSONBoundary = false
	return r.sse.Write(p)
}

func (r *streamChunkRewriter) rawJSONChunks(p []byte) ([][]byte, error) {
	chunks, consumed, ok, incomplete, err := r.tryRawJSONChunks(p, true)
	if err != nil {
		return chunks, err
	}
	if !ok && !incomplete {
		return [][]byte{bytes.Clone(p)}, nil
	}
	if incomplete {
		if r.frameRawJSONAsSSE {
			return chunks, fmt.Errorf("incomplete raw JSON stream")
		}
		return [][]byte{bytes.Clone(p)}, nil
	}
	if consumed < len(p) {
		sseChunks, err := r.writeSSESuffix(p[consumed:])
		return append(chunks, sseChunks...), err
	}
	return chunks, nil
}

func (r *streamChunkRewriter) tryRawJSONChunks(p []byte, eof bool) ([][]byte, int, bool, bool, error) {
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
				framed, err := r.frameRawJSON(restored)
				if err != nil {
					return nil, 0, true, false, err
				}
				return [][]byte{framed}, len(p), true, false, nil
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
	values, consumed, ok, incomplete := splitJSONValues(p, r.frameRawJSONAsSSE, eof)
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
			return out, cursor, true, false, err
		}
		if r.frameRawJSONAsSSE {
			framed, err := r.frameRawJSON(restored)
			if err != nil {
				return out, cursor, true, false, err
			}
			out = append(out, framed)
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

func splitJSONValues(p []byte, framed, eof bool) ([][]byte, int, bool, bool) {
	if len(bytes.TrimSpace(p)) == 0 {
		return nil, len(p), true, false
	}
	dec := json.NewDecoder(bytes.NewReader(p))
	values := make([][]byte, 0, 1)
	consumed := 0
	scalarStart, scalarOffset := 0, 0
	splitBeforeSSESuffix := func(start int) ([][]byte, int, bool, bool) {
		// unknown field 内未分隔的 scalar tokens 不属于完整 raw 前缀。
		if start == 0 && scalarStart < len(values) {
			return values[:scalarStart], scalarOffset, scalarStart > 0, false
		}
		return values, consumed, len(values) > 0, false
	}
	for {
		suffix := p[consumed:]
		start := skipTopLevelModelJSONSpace(suffix, 0)
		if len(values) > 0 && start < len(suffix) && suffix[start] != '{' && suffix[start] != '[' {
			if (framed || !couldStartJSONValue(suffix)) && (isSSEChunk(suffix) || isIncompleteSSEPrefix(suffix)) {
				return splitBeforeSSESuffix(start)
			}
			// scalar 与 unknown field 的分界需要后续字节或 EOF，已完成的 raw 前缀仍可发送。
			if !eof && (framed || !couldStartJSONValue(suffix)) {
				return values, consumed, true, true
			}
		}
		var raw json.RawMessage
		err := dec.Decode(&raw)
		if err == io.EOF {
			return values, len(p), len(values) > 0, false
		}
		if err != nil {
			if err == io.ErrUnexpectedEOF {
				return values, consumed, len(values) > 0, true
			}
			if len(values) > 0 && start < len(suffix) && suffix[start] != '{' && suffix[start] != '[' && (isSSEChunk(suffix) || isIncompleteSSEPrefix(suffix)) {
				return splitBeforeSSESuffix(start)
			}
			return nil, 0, false, false
		}
		end := int(dec.InputOffset())
		if raw[0] == '{' || raw[0] == '[' {
			scalarStart, scalarOffset = len(values)+1, end
		} else if start > 0 {
			scalarStart, scalarOffset = len(values), consumed
		}
		values = append(values, raw)
		consumed = end
	}
}

func (r *streamChunkRewriter) Flush() ([][]byte, error) {
	r.batchSSEOutput = r.frameRawJSONAsSSE
	r.rawScan = rawJSONScan{}
	if r.rawJSONArray {
		r.rawJSONArray = false
		pending := bytes.Clone(r.pending)
		r.pending = nil
		flushed, err := r.sse.Flush()
		if len(pending) > 0 {
			return append([][]byte{pending}, flushed...), err
		}
		return flushed, err
	}
	if len(r.pending) > 0 {
		pending := append([]byte(nil), r.pending...)
		r.pending = nil
		start := skipTopLevelModelJSONSpace(pending, 0)
		if couldStartJSONValue(pending) && (pending[start] == '{' || pending[start] == '[' || !isSSEChunk(pending)) {
			chunks, err := r.rawJSONChunks(pending)
			flushed, flushErr := r.sse.Flush()
			return append(chunks, flushed...), joinStreamErrors(err, flushErr)
		}
		r.batchSSEOutput = true
		chunks, err := r.sse.Write(pending)
		flushed, flushErr := r.sse.Flush()
		return append(chunks, flushed...), joinStreamErrors(err, flushErr)
	}
	r.batchSSEOutput = true
	return r.sse.Flush()
}

func (r *streamChunkRewriter) Finish() ([][]byte, error) {
	chunks, err := r.Flush()
	if err != nil {
		return chunks, err
	}
	r.sse.resetDelimiterlessResponsesEventScanner()
	if r.format == "openai" && r.frameRawJSONAsSSE && r.framedRawJSON && !r.sse.sawDone {
		chunks = append(chunks, []byte("data: [DONE]\n\n"))
	}
	return chunks, nil
}

func (r *streamChunkRewriter) frameRawJSON(p []byte) ([]byte, error) {
	if r.format == "openai-response" {
		var compact bytes.Buffer
		if err := json.Compact(&compact, p); err != nil {
			return nil, err
		}
		p = compact.Bytes()
	}
	r.framedRawJSON = true
	r.rawJSONBoundary = true
	eventType := ""
	if r.format == "openai-response" || r.format == "claude" {
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(p, &event) == nil {
			eventType = event.Type
		}
	}
	return frameSSEEvent(p, eventType), nil
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
	for remaining := p; len(remaining) > 0; {
		line, _, next := splitSSELine(remaining)
		remaining = next
		if !couldStartJSONValue(line) && hasSSEFieldPrefix(line) {
			return true
		}
	}
	_, delimiterLength, _ := findSSEEventDelimiter(p, 0, true)
	trimmed := bytes.TrimLeft(p, " \t\r\n")
	return delimiterLength > 0 || len(trimmed) > 0 && !couldStartJSONValue(trimmed) && bytes.ContainsAny(trimmed, "\r\n")
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
