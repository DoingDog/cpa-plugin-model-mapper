package main

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

func rewriteRequestModel(body []byte, upstreamModel string) ([]byte, bool, error) {
	return rewriteTopLevelModel(body, upstreamModel)
}

func restoreResponseModel(body []byte, originalModel string) ([]byte, bool, error) {
	return rewriteResponseModelFields(body, originalModel)
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

type jsonByteEdit struct {
	start, end  int
	replacement json.RawMessage
}

func applyJSONByteEdits(body []byte, edits []jsonByteEdit) []byte {
	if len(edits) == 0 {
		return bytes.Clone(body)
	}
	size := len(body)
	for _, edit := range edits {
		size += len(edit.replacement) - (edit.end - edit.start)
	}
	out := make([]byte, size)
	written, copyFrom := 0, 0
	for _, edit := range edits {
		written += copy(out[written:], body[copyFrom:edit.start])
		written += copy(out[written:], edit.replacement)
		copyFrom = edit.end
	}
	copy(out[written:], body[copyFrom:])
	return out
}

func rewriteTopLevelModelCanonical(body []byte, model string) ([]byte, bool, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	if _, err := d.Token(); err != nil {
		return nil, false, err
	}
	var edits []jsonByteEdit
	var lastModel json.RawMessage
	for d.More() {
		memberStart := skipTopLevelModelJSONSpace(body, int(d.InputOffset()))
		key, err := d.Token()
		if err != nil {
			return nil, false, err
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return nil, false, err
		}
		if key != "model" {
			continue
		}
		end := int(d.InputOffset())
		start := end - len(raw)
		if len(edits) > 0 {
			// 后续 model member 连同前置逗号删除，其他 member 保留原字节。
			start = memberStart
		}
		edits = append(edits, jsonByteEdit{start: start, end: end})
		lastModel = raw
	}
	if _, err := d.Token(); err != nil {
		return nil, false, err
	}
	if len(lastModel) == 0 || lastModel[0] != '"' {
		return bytes.Clone(body), false, nil
	}
	replacement, err := json.Marshal(model)
	if err != nil {
		return nil, false, err
	}
	edits[0].replacement = replacement
	return applyJSONByteEdits(body, edits), true, nil
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
	d := json.NewDecoder(bytes.NewReader(body))
	var edits []jsonByteEdit
	var raw json.RawMessage
	var err error
	switch body[start] {
	case '{':
		err = collectResponseModelEdits(d, body, model, replacement, "", &edits)
	case '[':
		_, err = d.Token()
		for err == nil && d.More() {
			err = collectResponseModelEdits(d, body, model, replacement, "", &edits)
		}
		if err == nil {
			_, err = d.Token()
		}
	default:
		err = d.Decode(&raw)
	}
	if err == nil {
		err = d.Decode(&raw)
		if err == io.EOF {
			return applyJSONByteEdits(body, edits), len(edits) > 0, true, nil
		}
	}
	if !json.Valid(body) {
		return bytes.Clone(body), false, false, nil
	}
	return nil, false, true, err
}

func collectResponseModelEdits(d *json.Decoder, body []byte, model string, replacement json.RawMessage, parent string, edits *[]jsonByteEdit) error {
	start := skipTopLevelModelJSONSpace(body, int(d.InputOffset()))
	if start < len(body) && (body[start] == ':' || body[start] == ',') {
		start = skipTopLevelModelJSONSpace(body, start+1)
	}
	var raw json.RawMessage
	// 此处只选择读取方式，JSON 结构和分隔符仍由 Decoder 验证。
	if start == len(body) || body[start] != '{' {
		return d.Decode(&raw)
	}
	if _, err := d.Token(); err != nil {
		return err
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return err
		}
		if parent == "" && (key == "response" || key == "message" || key == "interaction") {
			if err := collectResponseModelEdits(d, body, model, replacement, key.(string), edits); err != nil {
				return err
			}
			continue
		}
		if err := d.Decode(&raw); err != nil {
			return err
		}
		end := int(d.InputOffset())
		start := end - len(raw)
		if key == "model" || key == "modelVersion" && (parent == "" || parent == "response") {
			if raw[0] != '"' {
				continue
			}
			equal, err := responseModelStringEqual(raw, model)
			if err != nil {
				return err
			}
			if !equal {
				*edits = append(*edits, jsonByteEdit{start: start, end: end, replacement: replacement})
			}
		}
	}
	_, err := d.Token()
	return err
}

func responseModelStringEqual(raw []byte, model string) (bool, error) {
	if bytes.IndexByte(raw, '\\') < 0 && utf8.Valid(raw[1:len(raw)-1]) {
		return len(raw) == len(model)+2 && bytes.Equal(raw[1:len(raw)-1], []byte(model)), nil
	}
	var current string
	if err := json.Unmarshal(raw, &current); err != nil {
		return false, err
	}
	return current == model, nil
}
