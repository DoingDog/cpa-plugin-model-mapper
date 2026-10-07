package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const gNativeOutput = `[{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}]`
const gNativeCompleted = `{"type":"response.completed","response":{"id":"resp-issue8","object":"response","status":"completed","model":"grok-4.7","output":` + gNativeOutput + `}}`
const gNativeCompletedWant = `{"type":"response.completed","response":{"id":"resp-issue8","object":"response","status":"completed","model":"grok-4.6","output":` + gNativeOutput + `}}`

func gNativeParts(t *testing.T, format string, finish bool, parts ...[]byte) []byte {
	t.Helper()
	r := newStreamChunkRewriter("grok-4.6")
	r.format, r.frameRawJSONAsSSE = format, true
	var out []byte
	for _, part := range parts {
		chunks, err := r.Write(part)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, bytes.Join(chunks, nil)...)
	}
	var chunks [][]byte
	var err error
	if finish {
		chunks, err = r.Finish()
	} else {
		chunks, err = r.Flush()
	}
	if err != nil {
		t.Fatal(err)
	}
	return append(out, bytes.Join(chunks, nil)...)
}

func TestFunctionalNativeSSEFieldBoundary(t *testing.T) {
	for _, finish := range []bool{false, true} {
		t.Run(fmt.Sprintf("finish=%v", finish), func(t *testing.T) {
			got := gNativeParts(t, "openai-response", finish,
				[]byte("event: response.completed"), []byte("data: "+gNativeCompleted))
			want := []byte("event: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
			if !bytes.Equal(got, want) {
				t.Fatalf("native fields output=%q, want %q", got, want)
			}
			requireValidResponsesSSE(t, got, 1)
			var event struct {
				Response struct {
					Model  string
					Output json.RawMessage
				}
			}
			_, _, rest := splitSSELine(got)
			data, _, _ := splitSSELine(rest)
			if err := json.Unmarshal(sseFieldValue(data), &event); err != nil {
				t.Fatal(err)
			}
			if event.Response.Model != "grok-4.6" || string(event.Response.Output) != gNativeOutput {
				t.Fatalf("response=%+v", event.Response)
			}
		})
	}
}

func gNativeSequence() ([]string, []string) {
	names := []string{"response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.done", "response.content_part.done", "response.output_item.done", "response.completed"}
	payloads := []string{
		`{"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`,
		`{"type":"response.in_progress","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"ordinary grok-4.7 opaque 中文 output"}`,
		`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"ordinary grok-4.7 opaque 中文 output"}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}}`,
		gNativeCompleted,
	}
	return names, payloads
}

func TestFunctionalNativeSSEFieldForwarder(t *testing.T) {
	for _, terminal := range []string{"natural", "in-band-error", "callback-error"} {
		t.Run(terminal, func(t *testing.T) {
			reads := []pluginapi.HostModelStreamReadResponse{
				{Payload: []byte("event: response.completed")},
				{Payload: []byte("data: " + gNativeCompleted)},
				{Done: true},
			}
			if terminal == "in-band-error" {
				reads[1].Error, reads[1].Done = "controlled upstream read error", true
			}
			var emitted []byte
			var closeText string
			hostCloses, pluginCloses := 0, 0
			readErr := errors.New("controlled callback read error")
			stream := &executorStream{pluginStreamID: "g-native", hostStreamID: "g-host", originalModel: "grok-4.6", format: "openai-response", frameRawJSONAsSSE: true}
			stream.call = func(method string, payload any) (json.RawMessage, error) {
				switch method {
				case pluginabi.MethodHostModelStreamRead:
					if terminal == "callback-error" && len(reads) == 1 {
						return nil, readErr
					}
					if len(reads) == 0 {
						return nil, errors.New("unexpected extra host read")
					}
					read := reads[0]
					reads = reads[1:]
					return json.Marshal(read)
				case pluginabi.MethodHostStreamEmit:
					raw, err := json.Marshal(payload)
					if err != nil {
						return nil, err
					}
					var emit struct {
						Payload []byte `json:"payload"`
					}
					if err := json.Unmarshal(raw, &emit); err != nil {
						return nil, err
					}
					emitted = append(emitted, emit.Payload...)
				case pluginabi.MethodHostModelStreamClose:
					hostCloses++
				case pluginabi.MethodHostStreamClose:
					pluginCloses++
					raw, err := json.Marshal(payload)
					if err != nil {
						return nil, err
					}
					var closed struct {
						Error string `json:"error"`
					}
					if err := json.Unmarshal(raw, &closed); err != nil {
						return nil, err
					}
					closeText = closed.Error
				default:
					return nil, fmt.Errorf("unexpected callback %s", method)
				}
				return json.RawMessage(`{}`), nil
			}
			err := runStreamForward(stream)
			want := []byte("event: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
			if !bytes.Equal(emitted, want) {
				t.Fatalf("forwarded native fields=%q, want %q", emitted, want)
			}
			if hostCloses != 1 {
				t.Fatalf("host closes=%d", hostCloses)
			}
			if terminal == "callback-error" {
				if !errors.Is(err, readErr) || pluginCloses != 0 {
					t.Fatalf("error=%v plugin closes=%d", err, pluginCloses)
				}
			} else {
				if err != nil || pluginCloses != 1 {
					t.Fatalf("error=%v plugin closes=%d", err, pluginCloses)
				}
				if terminal == "natural" && closeText != "" {
					t.Fatal(closeText)
				}
				if terminal == "in-band-error" && closeText != "controlled upstream read error" {
					t.Fatalf("terminal error=%q", closeText)
				}
			}
		})
	}
}

func TestFunctionalNativeSSEFieldWireControls(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n", "\r"} {
		input := []byte("event: response.completed" + eol + "data: " + gNativeCompleted + eol + "id: event-1" + eol + "retry: 100" + eol + "x-vendor-field: grok-4.7" + eol + ": opaque grok-4.7" + eol + eol)
		want := []byte("event: response.completed" + eol + "data: " + gNativeCompletedWant + eol + "id: event-1" + eol + "retry: 100" + eol + "x-vendor-field: grok-4.7" + eol + ": opaque grok-4.7" + eol + eol)
		for split := 0; split <= len(input); split++ {
			got := gNativeParts(t, "claude", true, input[:split], input[split:])
			if !bytes.Equal(got, want) {
				t.Fatalf("eol=%q split=%d output=%q, want %q", eol, split, got, want)
			}
		}
		parts := make([][]byte, len(input))
		for i := range input {
			parts[i] = input[i : i+1]
		}
		if got := gNativeParts(t, "claude", true, parts...); !bytes.Equal(got, want) {
			t.Fatalf("eol=%q bytewise output differs", eol)
		}
	}
	literal := []byte("event: response.completeddata: " + gNativeCompleted + "\n\n")
	for split := 0; split <= len(literal); split++ {
		if got := gNativeParts(t, "claude", true, literal[:split], literal[split:]); !bytes.Equal(got, literal) {
			t.Fatalf("literal event value split=%d changed to %q", split, got)
		}
	}
	r := newStreamChunkRewriter("grok-4.6")
	r.format, r.frameRawJSONAsSSE = "claude", true
	if chunks, err := r.Write([]byte("event: response.completed\ndata: " + gNativeCompleted)); err != nil || len(chunks) != 0 {
		t.Fatalf("ordinary pre-metadata output=%q error=%v", chunks, err)
	}
	chunks, err := r.Write([]byte("\nid: event-1\n\n"))
	want := []byte("event: response.completed\ndata: " + gNativeCompletedWant + "\nid: event-1\n\n")
	if err != nil || !bytes.Equal(bytes.Join(chunks, nil), want) {
		t.Fatalf("metadata output=%q error=%v", chunks, err)
	}
}

func TestFunctionalNativeSSEFieldFormatIsolation(t *testing.T) {
	for _, format := range []string{"openai", "claude", "gemini", "interactions"} {
		parts := [][]byte{[]byte("event: response.completed"), []byte("data: " + gNativeCompleted)}
		got := gNativeParts(t, format, true, parts...)
		if !bytes.Equal(got, bytes.Join(parts, nil)) {
			t.Fatalf("format=%s inactive native recovery=%q", format, got)
		}
	}
}

func TestFunctionalNativeSSEFieldDiscriminator(t *testing.T) {
	for _, unused := range []string{"17", "false"} {
		t.Run("unused="+unused, func(t *testing.T) {
			data := `{"type":"response.completed","event_type":` + unused + `,"response":{"model":"grok-4.7"},"opaque":{"text":"event: response.completed data: grok-4.7","n":1.00}}`
			wantData := `{"type":"response.completed","event_type":` + unused + `,"response":{"model":"grok-4.6"},"opaque":{"text":"event: response.completed data: grok-4.7","n":1.00}}`
			got := gNativeParts(t, "openai-response", true, []byte("event: response.completed"), []byte("data: "+data))
			want := []byte("event: response.completed\ndata: " + wantData + "\n\n")
			if !bytes.Equal(got, want) {
				t.Fatalf("unused=%s output=%q, want %q", unused, got, want)
			}
		})
	}
	t.Run("mismatch-control", func(t *testing.T) {
		parts := [][]byte{[]byte("event: response.completed"), []byte(`data: {"type":"response.created","response":{"model":"grok-4.7"}}`)}
		if got := gNativeParts(t, "openai-response", true, parts...); !bytes.Equal(got, bytes.Join(parts, nil)) {
			t.Fatalf("mismatched discriminator changed to %q", got)
		}
	})
}

func TestFunctionalNativeSSEFieldDataOnlyTypeControl(t *testing.T) {
	for _, value := range []string{`{}`, `{"type":""}`, `{"type":null}`, `{"type":17}`, `{"type":false}`, `{"type":"other"}`} {
		t.Run(value, func(t *testing.T) {
			input := []byte("data: " + value)
			if got := gNativeParts(t, "openai-response", true, input); !bytes.Equal(got, input) {
				t.Fatalf("unrecognized type output=%q want=%q", got, input)
			}
		})
	}
}

func TestFunctionalNativeSSEFieldNativeDelimiter(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n", "\r"} {
		t.Run(fmt.Sprintf("eol=%q", eol), func(t *testing.T) {
			data := []byte("data: " + gNativeCompleted + eol + eol)
			want := []byte("event: response.completed\ndata: " + gNativeCompletedWant + eol + eol)
			if got := gNativeParts(t, "openai-response", true, []byte("event: response.completed"), data); !bytes.Equal(got, want) {
				t.Fatalf("native delimiter output=%q want=%q", got, want)
			}
		})
	}
}

func TestFunctionalNativeSSEFieldLimit(t *testing.T) {
	if maxPendingStreamBytes != 16<<20 {
		t.Fatal("pending limit changed")
	}
	for _, dataOnly := range []bool{false, true} {
		header := []byte("event: response.output_text.done")
		if dataOnly {
			header = nil
		}
		value := `{"type":"response.output_text.done","text":"` + strings.Repeat("x", maxPendingStreamBytes) + `"}`
		data := []byte("data: " + value)
		unitWant := []byte("data: " + value + "\n\n")
		if !dataOnly {
			unitWant = append([]byte("event: response.output_text.done\n"), unitWant...)
		}
		cut := maxPendingStreamBytes - len(header)
		for name, parts := range map[string][][]byte{
			"single-complete":                {header, data},
			"two-complete":                   {header, data, header, data},
			"pending-at-limit-then-complete": {header, data[:cut], data[cut:], header, data},
		} {
			t.Run(fmt.Sprintf("dataOnly=%v/%s", dataOnly, name), func(t *testing.T) {
				want := bytes.Clone(unitWant)
				if name != "single-complete" {
					want = append(want, unitWant...)
				}
				got := gNativeParts(t, "openai-response", true, parts...)
				if !bytes.Equal(bytes.TrimSuffix(got, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					t.Fatalf("complete native bytes=%d want=%d", len(got), len(want))
				}
			})
		}
		t.Run(fmt.Sprintf("dataOnly=%v/incomplete-control", dataOnly), func(t *testing.T) {
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			if _, err := r.Write(header); err != nil {
				t.Fatal(err)
			}
			chunks, err := r.Write([]byte(`data: {"type":"response.output_text.done","text":"` + strings.Repeat("x", maxPendingStreamBytes)))
			if err == nil || !strings.Contains(err.Error(), "stream pending data exceeds") || len(chunks) != 0 {
				t.Fatalf("incomplete native=(%d chunks,%v)", len(chunks), err)
			}
			chunks, err = r.Flush()
			if err != nil || len(bytes.Join(chunks, nil)) != 0 {
				t.Fatalf("cleared incomplete flush=(%d chunks,%v)", len(chunks), err)
			}
		})
	}
}

func gNativeFields(dataOnly bool, count int) ([][]byte, []byte, []string) {
	names, payloads := gNativeSequence()
	indices := []int{8}
	if count == 2 {
		indices = []int{0, 8}
	} else if count == 9 {
		indices = []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
	}
	var parts [][]byte
	var want bytes.Buffer
	var types []string
	for _, i := range indices {
		if !dataOnly {
			parts = append(parts, []byte("event: "+names[i]))
			fmt.Fprintf(&want, "event: %s\n", names[i])
		}
		parts = append(parts, []byte("data: "+payloads[i]))
		restored := payloads[i]
		if i < 2 || i == 8 {
			restored = strings.Replace(restored, `"model":"grok-4.7"`, `"model":"grok-4.6"`, 1)
		}
		fmt.Fprintf(&want, "data: %s\n\n", restored)
		types = append(types, names[i])
	}
	return parts, want.Bytes(), types
}

func gRequireNativeFrames(t *testing.T, got []byte, types []string, dataOnly bool) {
	t.Helper()
	requireValidResponsesSSE(t, got, len(types))
	frames := bytes.Split(bytes.TrimSuffix(got, []byte("\n\n")), []byte("\n\n"))
	for i, frame := range frames {
		var name string
		var data []byte
		for rest := frame; len(rest) > 0; {
			line, _, next := splitSSELine(rest)
			rest = next
			if bytes.HasPrefix(line, []byte("event: ")) {
				name = string(line[len("event: "):])
			}
			if bytes.HasPrefix(line, []byte("data:")) {
				data = sseFieldValue(line)
			}
		}
		var value struct {
			Type, Delta, Text string
			Response          struct {
				Model  string
				Output json.RawMessage
			}
		}
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		if value.Type != types[i] || (!dataOnly && name != types[i]) || (dataOnly && name != "") {
			t.Fatalf("frame %d name=%q type=%q want=%q", i, name, value.Type, types[i])
		}
		switch value.Type {
		case "response.created", "response.in_progress", "response.completed":
			if value.Response.Model != "grok-4.6" {
				t.Fatalf("frame %d model=%q", i, value.Response.Model)
			}
		}
		if value.Type == "response.completed" && string(value.Response.Output) != gNativeOutput {
			t.Fatalf("completed output=%s", value.Response.Output)
		}
		if value.Type == "response.output_text.delta" && value.Delta != "ordinary grok-4.7 opaque 中文 output" {
			t.Fatalf("delta=%q", value.Delta)
		}
		if value.Type == "response.output_text.done" && value.Text != "ordinary grok-4.7 opaque 中文 output" {
			t.Fatalf("done text=%q", value.Text)
		}
	}
}

func TestFunctionalNativeSSEFieldSequence(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		for _, count := range []int{2, 9} {
			for _, finish := range []bool{false, true} {
				t.Run(fmt.Sprintf("dataOnly=%v/count=%d/finish=%v", dataOnly, count, finish), func(t *testing.T) {
					parts, want, types := gNativeFields(dataOnly, count)
					got := gNativeParts(t, "openai-response", finish, parts...)
					if !bytes.Equal(got, want) {
						t.Fatalf("native sequence bytes=%d want=%d, prefix=%q", len(got), len(want), got[:min(len(got), 250)])
					}
					gRequireNativeFrames(t, got, types, dataOnly)
				})
			}
		}
	}
}

func TestFunctionalNativeSSEFieldDataOnlyTerminalControl(t *testing.T) {
	for _, finish := range []bool{false, true} {
		parts, _, types := gNativeFields(true, 1)
		got := gNativeParts(t, "openai-response", finish, parts...)
		if string(bytes.TrimSuffix(got, []byte("\n\n"))) != "data: "+gNativeCompletedWant {
			t.Fatalf("single data-only control finish=%v output=%q", finish, got)
		}
		gRequireNativeFrames(t, got, types, true)
	}
}

func TestFunctionalNativeSSEFieldGenericCanonicalControls(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		parts, _, _ := gNativeFields(dataOnly, 2)
		for _, eol := range []string{"\n", "\r\n", "\r"} {
			t.Run(fmt.Sprintf("dataOnly=%v/eol=%q", dataOnly, eol), func(t *testing.T) {
				literal := append(bytes.Join(parts, nil), []byte(eol+eol)...)
				if got := gNativeParts(t, "claude", true, literal); !bytes.Equal(got, literal) {
					t.Fatalf("whole generic field changed: %q", got)
				}
				for split := 0; split <= len(literal); split++ {
					if got := gNativeParts(t, "claude", true, literal[:split], literal[split:]); !bytes.Equal(got, literal) {
						t.Fatalf("split=%d generic field changed: %q", split, got)
					}
				}
				bytewise := make([][]byte, len(literal))
				for i := range literal {
					bytewise[i] = literal[i : i+1]
				}
				if got := gNativeParts(t, "claude", true, bytewise...); !bytes.Equal(got, literal) {
					t.Fatalf("bytewise generic field changed: %q", got)
				}
				r := newStreamChunkRewriter("grok-4.6")
				r.format, r.frameRawJSONAsSSE = "claude", true
				for i, part := range parts {
					if chunks, err := r.Write(part); err != nil || len(chunks) != 0 {
						t.Fatalf("generic write=%d chunks=%q error=%v", i, chunks, err)
					}
				}
				chunks, err := r.Write([]byte(eol + eol))
				if err != nil {
					t.Fatal(err)
				}
				// 末尾 CR 仍可能组成 CRLF，Finish 确认该行结束符。
				flushed, err := r.Finish()
				got := bytes.Join(append(chunks, flushed...), nil)
				if err != nil || !bytes.Equal(got, literal) {
					t.Fatalf("generic delayed delimiter output=%q error=%v want=%q", got, err, literal)
				}
				if !dataOnly {
					line, _, remaining := splitSSELine(literal)
					if !bytes.HasPrefix(line, []byte("event:")) || hasSSEDataField(line) || hasSSEDataField(remaining) {
						t.Fatal("generic control must be one event field with zero data fields")
					}
				}
			})
		}
	}
}

func TestFunctionalNativeSSEFieldDispatch(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		for _, count := range []int{2, 9} {
			t.Run(fmt.Sprintf("dataOnly=%v/count=%d", dataOnly, count), func(t *testing.T) {
				parts, want, types := gNativeFields(dataOnly, count)
				units := bytes.SplitAfter(want, []byte("\n\n"))[:count]
				r := newStreamChunkRewriter("grok-4.6")
				r.format, r.frameRawJSONAsSSE = "openai-response", true
				var got []byte
				for i, part := range parts {
					chunks, err := r.Write(part)
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, bytes.Join(chunks, nil)...)
					frames := (i + 1) / 2
					if dataOnly {
						frames = i
					}
					if expected := bytes.Join(units[:frames], nil); !bytes.Equal(got, expected) {
						t.Fatalf("write=%d want dataFrames=%d output=%q want=%q", i+1, frames, got, expected)
					}
				}
				chunks, err := r.Flush()
				got = append(got, bytes.Join(chunks, nil)...)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("flush output=%q error=%v want=%q", got, err, want)
				}
				gRequireNativeFrames(t, got, types, dataOnly)
			})
		}
	}
}

func TestFunctionalNativeSSEFieldLateDelimiter(t *testing.T) {
	parts, want, types := gNativeFields(false, 2)
	units := bytes.SplitAfter(want, []byte("\n\n"))[:2]
	for _, eol := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("eol=%q", eol), func(t *testing.T) {
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			var got []byte
			for i, part := range parts {
				chunks, err := r.Write(part)
				got = append(got, bytes.Join(chunks, nil)...)
				if expected := bytes.Join(units[:(i+1)/2], nil); err != nil || !bytes.Equal(got, expected) {
					t.Fatalf("callback=%d cumulative dataFrames=%d output=%q error=%v want=%q", i+1, (i+1)/2, got, err, expected)
				}
			}
			chunks, err := r.Write([]byte(eol + eol))
			if err != nil || len(bytes.TrimSpace(bytes.Join(chunks, nil))) != 0 {
				t.Fatalf("late delimiter added output: chunks=%q error=%v", chunks, err)
			}
			flushed, err := r.Finish()
			if err != nil || len(bytes.TrimSpace(bytes.Join(flushed, nil))) != 0 || !bytes.Equal(got, want) {
				t.Fatalf("late delimiter finish=(%q,%v) data=%q", flushed, err, got)
			}
			gRequireNativeFrames(t, got, types, false)
		})
	}
}

func TestFunctionalNativeSSEFieldLateDataDelimiter(t *testing.T) {
	parts, want, types := gNativeFields(true, 2)
	first := bytes.SplitAfter(want, []byte("\n\n"))[0]
	for _, eol := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("eol=%q", eol), func(t *testing.T) {
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			chunks, err := r.Write(parts[0])
			if err != nil || len(chunks) != 0 {
				t.Fatalf("first data=(%q,%v)", chunks, err)
			}
			chunks, err = r.Write(parts[1])
			got := bytes.Join(chunks, nil)
			if err != nil || !bytes.Equal(got, first) {
				t.Fatalf("next data dispatched=%q error=%v want=%q", got, err, first)
			}
			chunks, err = r.Write([]byte(eol + eol))
			got = append(got, bytes.Join(chunks, nil)...)
			flushed, flushErr := r.Flush()
			got = append(got, bytes.Join(flushed, nil)...)
			// delimiter 的行结束符可以保留；每个 data 单位及模型必须完整。
			normalized := bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))
			if err != nil || flushErr != nil || !bytes.Equal(normalized, want) {
				t.Fatalf("late data delimiter output=%q write=%v flush=%v want=%q", got, err, flushErr, want)
			}
			gRequireNativeFrames(t, normalized, types, true)
		})
	}
}

func TestFunctionalNativeSSEFieldResponsesMetadataControl(t *testing.T) {
	parts, _, _ := gNativeFields(false, 2)
	for _, eol := range []string{"\n", "\r\n"} {
		literal := append(bytes.Join(parts, nil), []byte(eol+eol)...)
		if got := gNativeParts(t, "openai-response", true, literal); !bytes.Equal(got, literal) {
			t.Fatalf("whole event-only metadata changed: %q", got)
		}
		data := []byte("data: " + gNativeCompleted + eol + eol)
		want := append(bytes.Clone(literal), []byte("data: "+gNativeCompletedWant+eol+eol)...)
		if got := gNativeParts(t, "openai-response", true, literal, data); !bytes.Equal(got, want) {
			t.Fatalf("metadata then real terminal output=%q want=%q", got, want)
		}
	}
}

func gNativeMetadataCases() []struct {
	name                   string
	header, terminal, want []byte
} {
	parts, _, _ := gNativeFields(false, 2)
	joined := bytes.Join(parts, nil)
	resegmented := bytes.Join(parts[:3], nil)
	return []struct {
		name                   string
		header, terminal, want []byte
	}{
		{"host-no-LF", joined, bytes.Clone(parts[3]), append(bytes.Clone(joined), []byte("\ndata: "+gNativeCompletedWant+"\n\n")...)},
		{"resegmented", resegmented, append(bytes.Clone(parts[3]), '\n', '\n'), append(bytes.Clone(resegmented), []byte("\ndata: "+gNativeCompletedWant+"\n\n")...)},
		{"delimited-control", append(bytes.Clone(joined), '\n', '\n'), append(bytes.Clone(parts[3]), '\n', '\n'), append(bytes.Clone(joined), []byte("\n\ndata: "+gNativeCompletedWant+"\n\n")...)},
	}
}

func gRequireMetadataTerminal(t *testing.T, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Fatalf("metadata/terminal output=%q want=%q", got, want)
	}
	// metadata 内嵌的 event/data/model 保持原文，仅检查真正的 data 行。
	var data []byte
	for rest := got; len(rest) > 0; {
		line, _, next := splitSSELine(rest)
		rest = next
		if bytes.HasPrefix(line, []byte("data:")) {
			if data != nil {
				t.Fatal("unexpected extra data field")
			}
			data = append(bytes.Clone(line), '\n', '\n')
		}
	}
	gRequireNativeFrames(t, data, []string{"response.completed"}, true)
}

func TestFunctionalNativeSSEFieldMetadataOutputUnits(t *testing.T) {
	for _, tc := range gNativeMetadataCases() {
		for _, finish := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/finish=%v", tc.name, finish), func(t *testing.T) {
				r := newStreamChunkRewriter("grok-4.6")
				r.format, r.frameRawJSONAsSSE = "openai-response", true
				var out [][]byte
				for _, input := range [][]byte{tc.header, tc.terminal} {
					part := bytes.Clone(input)
					chunks, err := r.Write(part)
					if err != nil {
						t.Fatal(err)
					}
					out = append(out, chunks...)
					for i := range part {
						part[i] = 'z'
					}
				}
				var chunks [][]byte
				var err error
				if finish {
					chunks, err = r.Finish()
				} else {
					chunks, err = r.Flush()
				}
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, chunks...)
				gRequireMetadataTerminal(t, bytes.Join(out, nil), tc.want)
				if _, err := r.Write([]byte("data: {}\n\n")); err != nil {
					t.Fatal(err)
				}
				gRequireMetadataTerminal(t, bytes.Join(out, nil), tc.want)
			})
		}
	}
}

func TestFunctionalNativeSSEFieldMetadataForwarder(t *testing.T) {
	for _, tc := range gNativeMetadataCases() {
		for _, terminal := range []string{"natural", "done-payload", "in-band-error", "callback-error", "cleanup-errors", "emit-error"} {
			t.Run(tc.name+"/"+terminal, func(t *testing.T) {
				reads := []pluginapi.HostModelStreamReadResponse{{Payload: tc.header}, {Payload: tc.terminal}, {Done: true}}
				if terminal == "done-payload" {
					reads[1].Done = true
				}
				if terminal == "in-band-error" || terminal == "cleanup-errors" {
					reads[1].Error, reads[1].Done = "controlled upstream read error", true
				}
				readErr := errors.New("controlled callback read error")
				hostErr := errors.New("controlled host close error")
				pluginErr := errors.New("controlled plugin close error")
				emitErr := errors.New("controlled emit error")
				var emitted []byte
				var closeText string
				var order []string
				readCount, hostCloses, pluginCloses := 0, 0, 0
				stream := &executorStream{pluginStreamID: "g-metadata", hostStreamID: "g-host", originalModel: "grok-4.6", format: "openai-response", frameRawJSONAsSSE: true}
				stream.call = func(method string, payload any) (json.RawMessage, error) {
					switch method {
					case pluginabi.MethodHostModelStreamRead:
						readCount++
						if terminal == "callback-error" && readCount == 3 {
							return nil, readErr
						}
						if len(reads) == 0 {
							return nil, errors.New("unexpected extra host read")
						}
						read := reads[0]
						reads = reads[1:]
						return json.Marshal(read)
					case pluginabi.MethodHostStreamEmit:
						order = append(order, "emit")
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, err
						}
						var emit struct{ Payload []byte }
						if err := json.Unmarshal(raw, &emit); err != nil {
							return nil, err
						}
						emitted = append(emitted, emit.Payload...)
						if terminal == "emit-error" && hasSSEDataField(emit.Payload) {
							return nil, emitErr
						}
					case pluginabi.MethodHostModelStreamClose:
						hostCloses++
						order = append(order, "host-close")
						if terminal == "cleanup-errors" {
							return nil, hostErr
						}
					case pluginabi.MethodHostStreamClose:
						pluginCloses++
						order = append(order, "plugin-close")
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, err
						}
						var closed struct{ Error string }
						if err := json.Unmarshal(raw, &closed); err != nil {
							return nil, err
						}
						closeText = closed.Error
						if terminal == "cleanup-errors" {
							return nil, pluginErr
						}
					default:
						return nil, fmt.Errorf("unexpected callback %s", method)
					}
					return json.RawMessage(`{}`), nil
				}
				err := runStreamForward(stream)
				gRequireMetadataTerminal(t, emitted, tc.want)
				wantOrder := "emit,host-close,plugin-close"
				if tc.name == "delimited-control" {
					wantOrder = "emit," + wantOrder
				}
				if terminal == "callback-error" {
					wantOrder = strings.TrimSuffix(wantOrder, ",plugin-close")
				}
				if strings.Join(order, ",") != wantOrder || hostCloses != 1 {
					t.Fatalf("order=%v host closes=%d want=%s", order, hostCloses, wantOrder)
				}
				switch terminal {
				case "callback-error":
					if !errors.Is(err, readErr) || pluginCloses != 0 || readCount != 3 {
						t.Fatalf("error=%v plugin closes=%d reads=%d", err, pluginCloses, readCount)
					}
				case "cleanup-errors":
					if !errors.Is(err, hostErr) || !errors.Is(err, pluginErr) || !strings.Contains(closeText, "controlled upstream read error") || !strings.Contains(closeText, hostErr.Error()) || pluginCloses != 1 || readCount != 2 {
						t.Fatalf("error=%v close=%q plugin closes=%d reads=%d", err, closeText, pluginCloses, readCount)
					}
				default:
					wantError, wantReads := "", 2
					if terminal == "natural" {
						wantReads = 3
					} else if terminal == "in-band-error" {
						wantError = "controlled upstream read error"
					} else if terminal == "emit-error" {
						wantError = "emit stream chunk: " + emitErr.Error()
					}
					if err != nil || closeText != wantError || pluginCloses != 1 || readCount != wantReads {
						t.Fatalf("error=%v close=%q plugin closes=%d reads=%d", err, closeText, pluginCloses, readCount)
					}
				}
			})
		}
	}
}

func gNativeLargeFixture(size int, dataOnly bool) ([][]byte, []byte) {
	value := `{"type":"response.output_text.done","response":{"model":"grok-4.7"},"text":"` + strings.Repeat("x", size) + `","opaque":{"text":"grok-4.7 中文","n":1.00}}`
	wantValue := `{"type":"response.output_text.done","response":{"model":"grok-4.6"},"text":"` + strings.Repeat("x", size) + `","opaque":{"text":"grok-4.7 中文","n":1.00}}`
	data := []byte("data: " + value)
	var parts [][]byte
	want := "data: " + wantValue + "\n\n"
	if !dataOnly {
		parts = append(parts, []byte("event: response.output_text.done"))
		want = "event: response.output_text.done\n" + want
	}
	for start := 0; start < len(data); start += 8 << 10 {
		parts = append(parts, data[start:min(start+(8<<10), len(data))])
	}
	return parts, []byte(want)
}

func gNativeContinue(parts [][]byte) ([][]byte, error) {
	r := newStreamChunkRewriter("grok-4.6")
	r.format, r.frameRawJSONAsSSE = "openai-response", true
	var chunks [][]byte
	for _, part := range parts {
		out, err := r.Write(part)
		chunks = append(chunks, out...)
		if err != nil {
			return chunks, err
		}
	}
	out, err := r.Finish()
	return append(chunks, out...), err
}

func TestFunctionalNativeSSEFieldContinuation(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		for _, size := range []int{2 << 20, 8 << 20} {
			t.Run(fmt.Sprintf("dataOnly=%v/bytes=%d", dataOnly, size), func(t *testing.T) {
				parts, want := gNativeLargeFixture(size, dataOnly)
				chunks, err := gNativeContinue(parts)
				got := bytes.Join(chunks, nil)
				if err != nil || !bytes.Equal(bytes.TrimSuffix(got, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					t.Fatalf("native continuation error=%v bytes=%d want=%d", err, len(got), len(want))
				}
				for _, part := range parts {
					for i := range part {
						part[i] = 'z'
					}
				}
				if !bytes.Equal(bytes.TrimSuffix(bytes.Join(chunks, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					t.Fatal("native chunks alias input bytes")
				}
				frozen := bytes.Clone(got)
				r := newStreamChunkRewriter("grok-4.6")
				r.format, r.frameRawJSONAsSSE = "openai-response", true
				var previous [][]byte
				fresh, _ := gNativeLargeFixture(size, dataOnly)
				for _, part := range fresh {
					out, writeErr := r.Write(part)
					if writeErr != nil {
						t.Fatal(writeErr)
					}
					previous = append(previous, out...)
				}
				out, finishErr := r.Finish()
				if finishErr != nil {
					t.Fatal(finishErr)
				}
				previous = append(previous, out...)
				if _, writeErr := r.Write([]byte("data: {}\n\n")); writeErr != nil {
					t.Fatal(writeErr)
				}
				if !bytes.Equal(bytes.Join(previous, nil), frozen) {
					t.Fatal("native chunks changed after future write")
				}
			})
		}
	}
}

func BenchmarkStreamChunkRewriterNativeFieldContinuation(b *testing.B) {
	for _, dataOnly := range []bool{false, true} {
		for _, size := range []int{2 << 20, 8 << 20} {
			b.Run(fmt.Sprintf("dataOnly=%v/bytes=%d/fragment=8192", dataOnly, size), func(b *testing.B) {
				parts, want := gNativeLargeFixture(size, dataOnly)
				chunks, err := gNativeContinue(parts)
				got := bytes.Join(chunks, nil)
				if err != nil || !bytes.Equal(bytes.TrimSuffix(got, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					b.Fatalf("byte-exact native preflight error=%v bytes=%d want=%d", err, len(got), len(want))
				}
				input := bytes.Join(parts, nil)
				for _, part := range parts {
					for i := range part {
						part[i] = 'z'
					}
				}
				if !bytes.Equal(bytes.Join(chunks, nil), got) {
					b.Fatal("native preflight output aliases input")
				}
				for offset, i := 0, 0; i < len(parts); i++ {
					copy(parts[i], input[offset:offset+len(parts[i])])
					offset += len(parts[i])
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(want)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					chunks, err = gNativeContinue(parts)
					outputBytes := 0
					for _, chunk := range chunks {
						outputBytes += len(chunk)
					}
					if err != nil || outputBytes != len(got) {
						b.Fatalf("native continuation=(%d bytes,%v), want %d bytes", outputBytes, err, len(got))
					}
				}
			})
		}
	}
}

func TestFunctionalNativeSSEFieldContinuationAllocations(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("dataOnly=%v", dataOnly), func(t *testing.T) {
			parts, want := gNativeLargeFixture(2<<20, dataOnly)
			chunks, err := gNativeContinue(parts)
			if err != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(chunks, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
				t.Fatalf("native allocation preflight=(%d,%v)", len(chunks), err)
			}
			allocations := testing.AllocsPerRun(1, func() {
				out, err := gNativeContinue(parts)
				if err != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(out, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					panic(fmt.Sprintf("native allocation output=(%d,%v)", len(out), err))
				}
			})
			if allocations > 200 {
				t.Fatalf("native continuation allocations=%v want <=200", allocations)
			}
		})
	}
}

func TestFunctionalNativeSSEFieldInputOwnership(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("dataOnly=%v", dataOnly), func(t *testing.T) {
			parts, want, _ := gNativeFields(dataOnly, 2)
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			var chunks [][]byte
			for _, part := range parts {
				out, err := r.Write(part)
				if err != nil {
					t.Fatal(err)
				}
				chunks = append(chunks, out...)
				for i := range part {
					part[i] = 'z'
				}
			}
			out, err := r.Finish()
			if err != nil {
				t.Fatal(err)
			}
			chunks = append(chunks, out...)
			if got := bytes.Join(chunks, nil); !bytes.Equal(got, want) {
				t.Fatalf("native input ownership output=%q want=%q", got, want)
			}
			frozen := bytes.Join(chunks, nil)
			if _, err := r.Write([]byte("data: {}\n\n")); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bytes.Join(chunks, nil), frozen) {
				t.Fatal("previous native output changed after future Write")
			}
		})
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataOutputUnits(t *testing.T) {
	for _, metadata := range [][]string{nil, {"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}} {
		for _, dataOnly := range []bool{false, true} {
			for _, count := range []int{1, 9} {
				for _, finish := range []bool{false, true} {
					t.Run(fmt.Sprintf("metadata=%q/dataOnly=%v/count=%d/finish=%v", metadata, dataOnly, count, finish), func(t *testing.T) {
						parts, want, types := gNativeFields(dataOnly, count)
						var prefix []byte
						var inputs [][]byte
						for _, field := range metadata {
							inputs = append(inputs, []byte(field))
							prefix = append(prefix, []byte(field+"\n")...)
						}
						inputs = append(inputs, parts...)
						want = append(prefix, want...)
						r := newStreamChunkRewriter("grok-4.6")
						r.format, r.frameRawJSONAsSSE = "openai-response", true
						var chunks [][]byte
						for _, field := range inputs {
							out, err := r.Write(field)
							if err != nil {
								t.Fatal(err)
							}
							chunks = append(chunks, out...)
							for i := range field {
								field[i] = 'z'
							}
						}
						var out [][]byte
						var err error
						if finish {
							out, err = r.Finish()
						} else {
							out, err = r.Flush()
						}
						if err != nil {
							t.Fatal(err)
						}
						chunks = append(chunks, out...)
						got := bytes.Join(chunks, nil)
						comparable := got
						if dataOnly && count == 1 {
							comparable, want = bytes.TrimSuffix(got, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))
						}
						if !bytes.Equal(comparable, want) {
							t.Fatalf("metadata output=%q want=%q", got, want)
						}
						gRequireNativeFrames(t, got, types, dataOnly)
						frozen := bytes.Clone(got)
						if _, err := r.Write([]byte("data: {}\n\n")); err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(bytes.Join(chunks, nil), frozen) {
							t.Fatal("previous metadata output changed after future Write")
						}
					})
				}
			}
		}
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataPositions(t *testing.T) {
	for _, metadata := range [][]string{{"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}} {
		for _, dataOnly := range []bool{false, true} {
			for _, count := range []int{1, 9} {
				for _, position := range []string{"before-event", "in-event", "between-events"} {
					t.Run(fmt.Sprintf("metadata=%q/dataOnly=%v/count=%d/%s", metadata, dataOnly, count, position), func(t *testing.T) {
						parts, wire, types := gNativeFields(dataOnly, count)
						units := bytes.SplitAfter(wire, []byte("\n\n"))[:count]
						var input [][]byte
						var want []byte
						stride := 2
						if dataOnly {
							stride = 1
						}
						for i, unit := range units {
							prefix := []byte(strings.Join(metadata, "\n") + "\n")
							if position == "between-events" && i == 0 {
								prefix = nil
							}
							fields := parts[i*stride : (i+1)*stride]
							if position == "in-event" && !dataOnly {
								input = append(input, fields[0])
								for _, field := range metadata {
									input = append(input, []byte(field))
								}
								input = append(input, fields[1])
								line, rest, ok := bytes.Cut(unit, []byte("\n"))
								if !ok {
									t.Fatal("fixture has no event line")
								}
								want = append(want, line...)
								want = append(want, '\n')
								want = append(want, prefix...)
								want = append(want, rest...)
							} else {
								if len(prefix) > 0 {
									for _, field := range metadata {
										input = append(input, []byte(field))
									}
								}
								input = append(input, fields...)
								want = append(want, prefix...)
								want = append(want, unit...)
							}
						}
						got := gNativeParts(t, "openai-response", true, input...)
						comparable := got
						if dataOnly && count == 1 {
							comparable, want = bytes.TrimSuffix(got, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))
						}
						if !bytes.Equal(comparable, want) {
							t.Fatalf("position output=%q want=%q", got, want)
						}
						gRequireNativeFrames(t, got, types, dataOnly)
					})
				}
			}
		}
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataWire(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		wire := []byte("id: event-1" + eol + "retry: 100" + eol + `: opaque event: response.created data: {"model":"grok-4.7"} 中文` + eol + "event: response.completed" + eol + "data: " + gNativeCompleted + eol + eol)
		want := bytes.Replace(wire, []byte("data: "+gNativeCompleted), []byte("data: "+gNativeCompletedWant), 1)
		for split := 0; split <= len(wire); split++ {
			if got := gNativeParts(t, "claude", true, wire[:split], wire[split:]); !bytes.Equal(got, want) {
				t.Fatalf("eol=%q split=%d got=%q want=%q", eol, split, got, want)
			}
		}
		parts := make([][]byte, len(wire))
		for i := range wire {
			parts[i] = wire[i : i+1]
		}
		if got := gNativeParts(t, "claude", true, parts...); !bytes.Equal(got, want) {
			t.Fatalf("eol=%q bytewise output=%q want=%q", eol, got, want)
		}
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataForwarder(t *testing.T) {
	for _, metadata := range []string{"id: event-1", "retry: 100", ": heartbeat"} {
		for _, terminal := range []string{"natural", "done-payload", "in-band-error", "callback-error", "cleanup-errors", "emit-error"} {
			t.Run(metadata+"/"+terminal, func(t *testing.T) {
				reads := []pluginapi.HostModelStreamReadResponse{{Payload: []byte(metadata)}, {Payload: []byte("event: response.completed")}, {Payload: []byte("data: " + gNativeCompleted)}, {Done: true}}
				if terminal == "done-payload" {
					reads[2].Done = true
				}
				if terminal == "in-band-error" || terminal == "cleanup-errors" {
					reads[2].Error, reads[2].Done = "controlled upstream read error", true
				}
				readErr, hostErr, pluginErr, emitErr := errors.New("controlled callback read error"), errors.New("controlled host close error"), errors.New("controlled plugin close error"), errors.New("controlled emit error")
				var output []byte
				var order []string
				var closeText string
				readCount, hostCloses, pluginCloses := 0, 0, 0
				stream := &executorStream{pluginStreamID: "metadata-draft", hostStreamID: "metadata-host", originalModel: "grok-4.6", format: "openai-response", frameRawJSONAsSSE: true}
				stream.call = func(method string, payload any) (json.RawMessage, error) {
					switch method {
					case pluginabi.MethodHostModelStreamRead:
						readCount++
						if terminal == "callback-error" && readCount == 4 {
							return nil, readErr
						}
						if len(reads) == 0 {
							return nil, errors.New("unexpected extra read")
						}
						next := reads[0]
						reads = reads[1:]
						return json.Marshal(next)
					case pluginabi.MethodHostStreamEmit:
						order = append(order, "emit")
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, err
						}
						var emit struct{ Payload []byte }
						if err := json.Unmarshal(raw, &emit); err != nil {
							return nil, err
						}
						output = append(output, emit.Payload...)
						if terminal == "emit-error" && hasSSEDataField(emit.Payload) {
							return nil, emitErr
						}
					case pluginabi.MethodHostModelStreamClose:
						hostCloses++
						order = append(order, "host-close")
						if terminal == "cleanup-errors" {
							return nil, hostErr
						}
					case pluginabi.MethodHostStreamClose:
						pluginCloses++
						order = append(order, "plugin-close")
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, err
						}
						var closed struct{ Error string }
						if err := json.Unmarshal(raw, &closed); err != nil {
							return nil, err
						}
						closeText = closed.Error
						if terminal == "cleanup-errors" {
							return nil, pluginErr
						}
					default:
						return nil, fmt.Errorf("unexpected method %s", method)
					}
					return json.RawMessage(`{}`), nil
				}
				err := runStreamForward(stream)
				want := []byte(metadata + "\nevent: response.completed\ndata: " + gNativeCompletedWant + "\n\n")
				if !bytes.Equal(output, want) {
					t.Errorf("metadata forwarder=%q want=%q", output, want)
				}
				if hostCloses != 1 || len(order) == 0 {
					t.Fatalf("order=%v host closes=%d", order, hostCloses)
				}
				wantCloses := "host-close,plugin-close"
				if terminal == "callback-error" {
					wantCloses = "host-close"
				}
				var closes []string
				for _, action := range order {
					if action != "emit" {
						closes = append(closes, action)
					}
				}
				if strings.Join(closes, ",") != wantCloses || order[0] != "emit" {
					t.Errorf("order=%v want emit before %s", order, wantCloses)
				}
				switch terminal {
				case "callback-error":
					if !errors.Is(err, readErr) || pluginCloses != 0 || readCount != 4 {
						t.Errorf("error=%v plugin closes=%d reads=%d", err, pluginCloses, readCount)
					}
				case "cleanup-errors":
					if !errors.Is(err, hostErr) || !errors.Is(err, pluginErr) || !strings.Contains(closeText, "controlled upstream read error") || !strings.Contains(closeText, hostErr.Error()) || pluginCloses != 1 || readCount != 3 {
						t.Errorf("error=%v close=%q plugin closes=%d reads=%d", err, closeText, pluginCloses, readCount)
					}
				default:
					wantError, wantReads := "", 3
					if terminal == "natural" {
						wantReads = 4
					} else if terminal == "in-band-error" {
						wantError = "controlled upstream read error"
					} else if terminal == "emit-error" {
						wantError = "emit stream chunk: " + emitErr.Error()
					}
					if err != nil || closeText != wantError || pluginCloses != 1 || readCount != wantReads {
						t.Errorf("error=%v close=%q plugin closes=%d reads=%d", err, closeText, pluginCloses, readCount)
					}
				}
			})
		}
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataDispatch(t *testing.T) {
	for _, metadata := range [][]string{{"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}} {
		for _, dataOnly := range []bool{false, true} {
			for _, eol := range []string{"\n", "\r\n"} {
				t.Run(fmt.Sprintf("metadata=%q/dataOnly=%v/eol=%q", metadata, dataOnly, eol), func(t *testing.T) {
					r := newStreamChunkRewriter("grok-4.6")
					r.format, r.frameRawJSONAsSSE = "openai-response", true
					parts, want, types := gNativeFields(dataOnly, 2)
					want = append([]byte(strings.Join(metadata, "\n")+"\n"), want...)
					var got []byte
					for _, field := range metadata {
						out, err := r.Write([]byte(field))
						got = append(got, bytes.Join(out, nil)...)
						if err != nil || bytes.Contains(got, []byte("\ndata:")) {
							t.Fatalf("metadata dispatched data=%q error=%v", got, err)
						}
					}
					for i, part := range parts {
						out, err := r.Write(part)
						got = append(got, bytes.Join(out, nil)...)
						frames := (i + 1) / 2
						if dataOnly {
							frames = i
						}
						if err != nil || bytes.Count(got, []byte("\ndata: ")) != frames {
							t.Fatalf("callback=%d dataFrames=%d want=%d output=%q error=%v", i+1, bytes.Count(got, []byte("\ndata: ")), frames, got, err)
						}
					}
					out, err := r.Write([]byte(eol + eol))
					if err != nil {
						t.Fatal(err)
					}
					if dataOnly {
						got = append(got, bytes.Join(out, nil)...)
					} else if len(bytes.TrimSpace(bytes.Join(out, nil))) != 0 {
						t.Fatalf("late delimiter added event=%q", out)
					}
					out, err = r.Finish()
					if dataOnly {
						got = append(got, bytes.Join(out, nil)...)
					} else if len(bytes.TrimSpace(bytes.Join(out, nil))) != 0 {
						t.Fatalf("Finish added event=%q", out)
					}
					if err != nil || !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
						t.Fatalf("late delimiter output=%q error=%v want=%q", got, err, want)
					}
					gRequireNativeFrames(t, bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), types, dataOnly)
				})
			}
		}
	}
}

func gNativeOrdinaryMetadataLargeFixture(size int, dataOnly bool) ([][]byte, []byte) {
	parts, want := gNativeLargeFixture(size, dataOnly)
	metadata := [][]byte{[]byte("id: event-1"), []byte("retry: 100"), []byte(`: opaque event: response.created data: {"model":"grok-4.7"} 中文`)}
	prefix := append(bytes.Join(metadata, []byte("\n")), '\n')
	return append(metadata, parts...), append(prefix, want...)
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataContinuation(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		for _, size := range []int{2 << 20, 8 << 20} {
			t.Run(fmt.Sprintf("dataOnly=%v/bytes=%d", dataOnly, size), func(t *testing.T) {
				parts, want := gNativeOrdinaryMetadataLargeFixture(size, dataOnly)
				chunks, err := gNativeContinue(parts)
				if err != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(chunks, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					t.Fatalf("continuation error=%v", err)
				}
				if size == 2<<20 {
					allocs := testing.AllocsPerRun(1, func() {
						out, err := gNativeContinue(parts)
						if err != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(out, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
							panic(fmt.Sprintf("allocation preflight error=%v", err))
						}
					})
					if allocs > 200 {
						t.Fatalf("allocations=%v want<=200", allocs)
					}
				}
				for _, part := range parts {
					for i := range part {
						part[i] = 'z'
					}
				}
				if !bytes.Equal(bytes.TrimSuffix(bytes.Join(chunks, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					t.Fatal("metadata continuation aliases input")
				}
			})
		}
	}
}

func TestFunctionalNativeSSEFieldOrdinaryMetadataPendingLimit(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("dataOnly=%v", dataOnly), func(t *testing.T) {
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			metadata := []byte("id: event-1")
			if _, err := r.Write(metadata); err != nil {
				t.Fatal(err)
			}
			var header []byte
			if !dataOnly {
				header = []byte("event: response.output_text.done")
				if _, err := r.Write(header); err != nil {
					t.Fatal(err)
				}
			}
			data := []byte(`data: {"type":"response.output_text.done","text":"` + strings.Repeat("x", maxPendingStreamBytes) + `"}`)
			cut := maxPendingStreamBytes - len(header)
			if chunks, err := r.Write(data[:cut]); err != nil || len(chunks) != 0 {
				t.Fatalf("metadata must not count against incomplete data: chunks=%d error=%v", len(chunks), err)
			}
			chunks, err := r.Write(data[cut:])
			flushed, flushErr := r.Finish()
			want := append(bytes.Clone(metadata), '\n')
			if !dataOnly {
				want = append(want, header...)
				want = append(want, '\n')
			}
			want = append(want, data...)
			if err != nil || flushErr != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(append(chunks, flushed...), nil), []byte("\n\n")), want) {
				t.Fatalf("complete continuation error=%v flush=%v", err, flushErr)
			}
		})
	}
}

func BenchmarkStreamChunkRewriterOrdinaryMetadataContinuation(b *testing.B) {
	for _, dataOnly := range []bool{false, true} {
		for _, size := range []int{2 << 20, 8 << 20} {
			b.Run(fmt.Sprintf("dataOnly=%v/bytes=%d/fragment=8192", dataOnly, size), func(b *testing.B) {
				parts, want := gNativeOrdinaryMetadataLargeFixture(size, dataOnly)
				chunks, err := gNativeContinue(parts)
				if err != nil || !bytes.Equal(bytes.TrimSuffix(bytes.Join(chunks, nil), []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))) {
					b.Fatalf("byte-exact metadata preflight error=%v", err)
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(want)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					chunks, err = gNativeContinue(parts)
					if err != nil || len(chunks) == 0 {
						b.Fatalf("continuation=(%d,%v)", len(chunks), err)
					}
				}
			})
		}
	}
}
