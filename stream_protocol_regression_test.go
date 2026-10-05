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

func TestFunctionalOpenAIRawCoreChunks(t *testing.T) {
	t.Cleanup(func() {
		shutdownExecutorStreams()
		resetExecutorStreamLifecycle()
		setLoadedConfigForTest(defaultConfig())
	})
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	for _, contentType := range []string{"", "text/event-stream", "text/event-stream; charset=utf-8", "application/json"} {
		for _, terminalPayload := range []bool{false, true} {
			for _, tc := range []struct {
				name, input string
				want        []string
			}{
				{"two values", `{"model":"upstream","choices":[{"delta":{"content":"one"}}]} {"model":"upstream","choices":[{"delta":{"content":"two"}}]}`, []string{`{"model":"client","choices":[{"delta":{"content":"one"}}]}`, ` {"model":"client","choices":[{"delta":{"content":"two"}}]}`}},
				{"single hello", `{"model":"upstream","choices":[{"delta":{"content":"hello"}}]}`, []string{`{"model":"client","choices":[{"delta":{"content":"hello"}}]}`}},
			} {
				t.Run(fmt.Sprintf("%s/header=%s/terminal=%v", tc.name, contentType, terminalPayload), func(t *testing.T) {
					req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
						Model: "client", Format: "openai", SourceFormat: "openai", Stream: true,
						OriginalRequest: []byte(`{"model":"client","stream":true}`),
					}, StreamID: "functional-openai"}
					reads := []pluginapi.HostModelStreamReadResponse{{Payload: []byte(tc.input), Done: terminalPayload}}
					if !terminalPayload {
						reads = append(reads, pluginapi.HostModelStreamReadResponse{Done: true})
					}
					emitted, hostClosed, pluginClosed, _, err := runExecutorStreamTestWithHostContentType(req, reads, contentType)
					if err != nil || !hostClosed || !pluginClosed || len(emitted) != len(tc.want) {
						t.Fatalf("emitted=%q close=%v/%v error=%v", emitted, hostClosed, pluginClosed, err)
					}
					for i, chunk := range emitted {
						if !json.Valid([]byte(chunk)) || chunk != tc.want[i] || strings.Contains(chunk, "data:") || strings.Contains(chunk, "upstream") {
							t.Fatalf("core chunk=%q, want %q", chunk, tc.want[i])
						}
					}
					if strings.Contains(strings.Join(emitted, ""), "[DONE]") {
						t.Fatal("plugin added OpenAI DONE")
					}
				})
			}
		}
	}
}

func TestFunctionalIssue8TerminalPayload(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "grok-4.6=>grok-4.7"})
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
	const output = `[{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}]`
	const payload = `{"type":"response.completed","response":{"id":"resp-issue8","object":"response","status":"completed","model":"grok-4.7","output":` + output + `}}`
	const restored = `{"type":"response.completed","response":{"id":"resp-issue8","object":"response","status":"completed","model":"grok-4.6","output":` + output + `}}`
	input := []byte("event: response.completed\ndata: " + payload + "\n\n")
	want := []byte("event: response.completed\ndata: " + restored + "\n\n")
	for _, tc := range []struct {
		name, wantError string
		reads           []pluginapi.HostModelStreamReadResponse
	}{
		{"Payload+Done", "", []pluginapi.HostModelStreamReadResponse{{Payload: input, Done: true}}},
		{"split terminal", "", []pluginapi.HostModelStreamReadResponse{{Payload: input[:17]}, {Payload: input[17:], Done: true}}},
		{"Payload+Error+Done", "probe upstream error", []pluginapi.HostModelStreamReadResponse{{Payload: input, Error: "probe upstream error", Done: true}}},
		{"independent Done", "", []pluginapi.HostModelStreamReadResponse{{Payload: input}, {Done: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := tc.reads
			var emitted []byte
			var closeText string
			var order []string
			hostCloses, pluginCloses := 0, 0
			call := func(method string, value any) (json.RawMessage, error) {
				switch method {
				case pluginabi.MethodHostModelExecuteStream:
					raw, err := json.Marshal(value)
					if err != nil {
						return nil, err
					}
					var forwarded pluginapi.HostModelExecutionRequest
					if err := json.Unmarshal(raw, &forwarded); err != nil {
						return nil, err
					}
					if forwarded.Model != "grok-4.7" || string(forwarded.Body) != `{"model":"grok-4.7","stream":true}` || forwarded.EntryProtocol != "openai-response" || forwarded.ExitProtocol != "openai-response" {
						return nil, fmt.Errorf("unexpected forwarded request=%+v", forwarded)
					}
					return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, StreamID: "issue8-host", Headers: map[string][]string{"Content-Type": {"text/event-stream"}}})
				case pluginabi.MethodHostModelStreamRead:
					if len(reads) == 0 {
						return nil, errors.New("unexpected extra read")
					}
					read := reads[0]
					reads = reads[1:]
					return json.Marshal(read)
				case pluginabi.MethodHostStreamEmit:
					raw, err := json.Marshal(value)
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
					order = append(order, "emit")
				case pluginabi.MethodHostModelStreamClose:
					hostCloses++
					order = append(order, "host-close")
				case pluginabi.MethodHostStreamClose:
					pluginCloses++
					order = append(order, "plugin-close")
					raw, err := json.Marshal(value)
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
			stream, _, err := prepareExecutorStream(&executorRPCRequest{Model: "grok-4.6", Format: "openai-response", SourceFormat: "openai-response", OriginalRequest: []byte(`{"model":"grok-4.6","stream":true}`), StreamID: "issue8-plugin"}, call)
			if err != nil {
				t.Fatal(err)
			}
			if err := runStreamForward(stream); err != nil || !bytes.Equal(emitted, want) || closeText != tc.wantError || hostCloses != 1 || pluginCloses != 1 || len(reads) != 0 || strings.Join(order, ",") != "emit,host-close,plugin-close" {
				t.Fatalf("output=%q terminal=%q closes=%d/%d remaining=%d order=%v error=%v", emitted, closeText, hostCloses, pluginCloses, len(reads), order, err)
			}
			requireValidResponsesSSE(t, emitted, 1)
		})
	}
}

func TestFunctionalCompletePrefixBeforeLimit(t *testing.T) {
	cases := []struct {
		name, format, prefix, tail, want string
		framed                           bool
	}{
		{"sse LF", "claude", "data: {\"model\":\"upstream\"}\n\n", "data: {\"text\":\"", "data: {\"model\":\"client\"}\n\n", true},
		{"sse CR", "claude", "data: {\"model\":\"upstream\"}\r\r", "data: {\"text\":\"", "data: {\"model\":\"client\"}\r\r", true},
		{"sse CRLF", "claude", "data: {\"model\":\"upstream\"}\r\n\r\n", "data: {\"text\":\"", "data: {\"model\":\"client\"}\r\n\r\n", true},
		{"framed raw", "openai-response", `{"model":"upstream"} `, `{"text":"`, "data: {\"model\":\"client\"}\n\n", true},
		{"unframed raw", "openai", `{"model":"upstream"} `, `{"text":"`, `{"model":"client"}`, false},
		{"array element", "gemini", `[{"modelVersion":"upstream"},`, `{"text":"`, `[{"modelVersion":"client"},`, false},
		{"closed array recursive suffix", "gemini", `[]`, `{"text":"`, `[]`, false},
		{"closed array element recursive suffix", "gemini", `[{"modelVersion":"upstream"}]`, `{"text":"`, `[{"modelVersion":"client"}]`, false},
		{"raw with SSE suffix", "openai-response", `{"model":"upstream"}`, "data: {\"text\":\"", "data: {\"model\":\"client\"}\n\n", true},
	}
	for _, tc := range cases {
		for _, continued := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/continued=%v", tc.name, continued), func(t *testing.T) {
				r := newStreamChunkRewriter("client")
				r.format, r.frameRawJSONAsSSE = tc.format, tc.framed
				tail := []byte(tc.tail + strings.Repeat("x", maxPendingStreamBytes+1-len(tc.tail)))
				input := append([]byte(tc.prefix), tail...)
				var out [][]byte
				if continued {
					chunks, err := r.Write(input[:len(tc.prefix)+len(tc.tail)])
					if err != nil {
						t.Fatal(err)
					}
					out = append(out, chunks...)
					input = input[len(tc.prefix)+len(tc.tail):]
				}
				chunks, err := r.Write(input)
				if err == nil || !strings.Contains(err.Error(), "stream pending data exceeds") {
					t.Fatalf("error=%v", err)
				}
				out = append(out, chunks...)
				got := strings.TrimRight(string(bytes.Join(out, nil)), " ")
				want := strings.TrimRight(tc.want, " ")
				if got != want {
					t.Fatalf("complete prefix=%q, want %q", got, want)
				}
				if r.pending != nil || r.sse.buf != nil {
					t.Fatal("oversized pending retained")
				}
				flushed, flushErr := r.Flush()
				if flushErr != nil || len(bytes.Join(flushed, nil)) != 0 {
					t.Fatalf("flush=(%d,%v)", len(flushed), flushErr)
				}
			})
		}
	}
}

func TestFunctionalPayloadRewriteAndEmitErrors(t *testing.T) {
	emitErr := errors.New("expected emit failure")
	emits := 0
	stream := &executorStream{pluginStreamID: "functional-prefix", call: func(method string, _ any) (json.RawMessage, error) {
		if method != pluginabi.MethodHostStreamEmit {
			return nil, fmt.Errorf("unexpected callback %s", method)
		}
		emits++
		return nil, emitErr
	}}
	r := newStreamChunkRewriter("client")
	r.frameRawJSONAsSSE = true
	payload := []byte("data: {\"model\":\"upstream\"}\n\ndata: {\"text\":\"" + strings.Repeat("x", maxPendingStreamBytes+1))
	err := stream.processPayload(r, payload)
	if emits != 1 || !errors.Is(err, emitErr) || !strings.Contains(err.Error(), "stream pending data exceeds") {
		t.Fatalf("emits=%d error=%v", emits, err)
	}
	if strings.Index(err.Error(), "rewrite stream chunk") > strings.Index(err.Error(), "emit stream chunk") {
		t.Fatalf("error order=%v", err)
	}
	if err := stream.flushAndEmit(r, false); err != nil || emits != 1 {
		t.Fatalf("flush error=%v emits=%d, want no replay", err, emits)
	}
}

func TestFunctionalEmitFailureStopsRawChunks(t *testing.T) {
	emitErr := errors.New("expected first emit failure")
	var emitted []byte
	emits := 0
	stream := &executorStream{pluginStreamID: "functional-emit", call: func(method string, payload any) (json.RawMessage, error) {
		if method != pluginabi.MethodHostStreamEmit {
			return nil, fmt.Errorf("unexpected callback %s", method)
		}
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
		emitted = bytes.Clone(emit.Payload)
		emits++
		return nil, emitErr
	}}
	r := newStreamChunkRewriter("client")
	if err := stream.processPayload(r, []byte(`{"model":"upstream","id":1} {"model":"upstream","id":2}`)); !errors.Is(err, emitErr) {
		t.Fatalf("error=%v", err)
	}
	if emits != 1 || string(emitted) != `{"model":"client","id":1}` {
		t.Fatalf("emits=%d output=%q", emits, emitted)
	}
	if err := stream.flushAndEmit(r, true); err != nil || emits != 1 {
		t.Fatalf("flush error=%v emits=%d, want no replay", err, emits)
	}
	if err := stream.processPayload(r, []byte(`{"model":"upstream","id":3}`)); err != nil || emits != 1 {
		t.Fatalf("later payload error=%v emits=%d, want no further emission", err, emits)
	}
}

func TestFunctionalEmitFailureStopsPendingUnits(t *testing.T) {
	for _, tc := range []struct{ name, format, input, wantFirst string }{
		{"SSE LF", "claude", "data: {\"model\":\"upstream\",\"id\":1}\n\ndata: {\"model\":\"upstream\",\"id\":2}", "data: {\"model\":\"client\",\"id\":1}\n\n"},
		{"SSE CRLF", "claude", "data: {\"model\":\"upstream\",\"id\":1}\r\n\r\ndata: {\"model\":\"upstream\",\"id\":2}", "data: {\"model\":\"client\",\"id\":1}\r\n\r\n"},
		{"SSE CR", "claude", "data: {\"model\":\"upstream\",\"id\":1}\r\rdata: {\"model\":\"upstream\",\"id\":2}", "data: {\"model\":\"client\",\"id\":1}\r\r"},
		{"deferred scalar", "openai-response", `{"model":"upstream","id":1} true`, "data: {\"model\":\"client\",\"id\":1}\n\n"},
	} {
		for _, terminal := range []string{"continuing", "done", "error"} {
			t.Run(tc.name+"/"+terminal, func(t *testing.T) {
				emitErr := errors.New("expected first emit failure")
				var attempted [][]byte
				var order []string
				var closeText string
				reads, hostCloses, pluginCloses := 0, 0, 0
				stream := &executorStream{originalModel: "client", format: tc.format, frameRawJSONAsSSE: true, hostStreamID: "host-pending", pluginStreamID: "plugin-pending"}
				stream.call = func(method string, payload any) (json.RawMessage, error) {
					switch method {
					case pluginabi.MethodHostModelStreamRead:
						reads++
						order = append(order, "read")
						if reads != 1 || payload.(pluginapi.HostModelStreamReadRequest).StreamID != "host-pending" {
							return nil, fmt.Errorf("unexpected read %d: %+v", reads, payload)
						}
						chunk := pluginapi.HostModelStreamReadResponse{Payload: []byte(tc.input), Done: terminal != "continuing"}
						if terminal == "error" {
							chunk.Error = "expected upstream failure"
						}
						return json.Marshal(chunk)
					case pluginabi.MethodHostStreamEmit:
						order = append(order, "emit")
						raw, err := json.Marshal(payload)
						if err != nil {
							return nil, err
						}
						var emit struct {
							StreamID string `json:"stream_id"`
							Payload  []byte `json:"payload"`
						}
						if err := json.Unmarshal(raw, &emit); err != nil {
							return nil, err
						}
						if emit.StreamID != "plugin-pending" {
							return nil, fmt.Errorf("emit stream id=%q", emit.StreamID)
						}
						attempted = append(attempted, bytes.Clone(emit.Payload))
						return nil, emitErr
					case pluginabi.MethodHostModelStreamClose:
						hostCloses++
						order = append(order, "host-close")
						if payload.(pluginapi.HostModelStreamCloseRequest).StreamID != "host-pending" {
							return nil, fmt.Errorf("host close payload=%+v", payload)
						}
					case pluginabi.MethodHostStreamClose:
						pluginCloses++
						order = append(order, "plugin-close")
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
				if err != nil || reads != 1 || hostCloses != 1 || pluginCloses != 1 {
					t.Fatalf("error=%v reads=%d closes=%d/%d", err, reads, hostCloses, pluginCloses)
				}
				if len(attempted) != 1 || string(attempted[0]) != tc.wantFirst {
					t.Errorf("emit attempts=%q, want only %q", attempted, tc.wantFirst)
				}
				if strings.Join(order, ",") != "read,emit,host-close,plugin-close" || !strings.Contains(closeText, emitErr.Error()) {
					t.Errorf("order=%v terminal=%q", order, closeText)
				}
				if terminal == "error" && (!strings.Contains(closeText, "expected upstream failure") || strings.Index(closeText, "expected upstream failure") > strings.Index(closeText, emitErr.Error())) {
					t.Errorf("upstream error order=%q", closeText)
				}
			})
		}
	}
}

func functionalProtocolParts(t *testing.T, format string, parts ...[]byte) []byte {
	t.Helper()
	r := newStreamChunkRewriter("client")
	r.format, r.frameRawJSONAsSSE = format, true
	var out []byte
	for _, part := range parts {
		chunks, err := r.Write(part)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, bytes.Join(chunks, nil)...)
	}
	chunks, err := r.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return append(out, bytes.Join(chunks, nil)...)
}

func TestFunctionalLogicalEventTruncatedNextPrefix(t *testing.T) {
	event := []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"upstream\"}}")
	want := []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"client\"}}\n\n")
	for _, prefix := range []string{"e", "ev", "eve", "even", "event"} {
		input := append(bytes.Clone(event), prefix...)
		for _, split := range []int{0, len(event), len(input)} {
			got := functionalProtocolParts(t, "openai-response", input[:split], input[split:])
			if !bytes.Equal(got, want) {
				t.Fatalf("prefix=%q split=%d output=%q", prefix, split, got)
			}
		}
	}
}

func TestFunctionalInteractionsLogicalEvents(t *testing.T) {
	names := []string{"interaction.created", "interaction.status_update", "step.start", "step.delta", "step.stop", "interaction.completed"}
	payloads := []string{
		`{"event_type":"interaction.created","interaction":{"model":"upstream"}}`,
		`{"event_type":"interaction.status_update","status":"in_progress"}`,
		`{"event_type":"step.start","index":0,"step":{"type":"model_output"}}`,
		`{"event_type":"step.delta","index":0,"delta":{"text":"answer"}}`,
		`{"event_type":"step.stop","index":0}`,
		`{"event_type":"interaction.completed","interaction":{"model":"upstream","status":"completed"}}`,
	}
	var input, want []byte
	for i, name := range names {
		event := "event: " + name + "\ndata: " + payloads[i]
		input = append(input, event...)
		want = append(want, strings.ReplaceAll(event, `"model":"upstream"`, `"model":"client"`)...)
		want = append(want, '\n', '\n')
	}
	input = append(input, "event: done\ndata: [DONE]"...)
	want = append(want, "event: done\ndata: [DONE]\n\n"...)
	for split := 0; split <= len(input); split++ {
		got := functionalProtocolParts(t, "interactions", input[:split], input[split:])
		if !bytes.Equal(got, want) {
			t.Fatalf("split=%d output=%q", split, got)
		}
	}
	parts := make([][]byte, len(input))
	for i := range input {
		parts[i] = input[i : i+1]
	}
	if got := functionalProtocolParts(t, "interactions", parts...); !bytes.Equal(got, want) {
		t.Fatal("one-byte partition differs")
	}
}

func TestFunctionalLogicalEventFormatIsolation(t *testing.T) {
	cases := []struct {
		format  string
		names   [2]string
		payload string
	}{
		{"openai-response", [2]string{"response.created", "response.completed"}, `{"event_type":%s,"response":{"model":"upstream"},"type":%q}`},
		{"interactions", [2]string{"interaction.created", "interaction.completed"}, `{"event_type":%[2]q,"interaction":{"model":"upstream"},"type":%[1]s}`},
	}
	for _, tc := range cases {
		for _, unused := range []string{"17", "false"} {
			var input, want []byte
			for _, name := range tc.names {
				event := "event: " + name + "\ndata: " + fmt.Sprintf(tc.payload, unused, name)
				input = append(input, event...)
				want = append(want, strings.ReplaceAll(event, `"model":"upstream"`, `"model":"client"`)...)
				want = append(want, '\n', '\n')
			}
			t.Run(tc.format+"/unused="+unused, func(t *testing.T) {
				for split := 0; split <= len(input); split++ {
					got := functionalProtocolParts(t, tc.format, input[:split], input[split:])
					if !bytes.Equal(got, want) {
						t.Fatalf("split=%d output=%q, want %q", split, got, want)
					}
					requireValidResponsesSSE(t, got, 2)
				}
			})
			for _, format := range []string{"openai", "claude", "gemini"} {
				t.Run(format+"/inactive="+tc.format+"/unused="+unused, func(t *testing.T) {
					got := functionalProtocolParts(t, format, input)
					if !bytes.Equal(got, input) {
						t.Fatalf("inactive recovery output=%q, want %q", got, input)
					}
				})
			}
		}
	}
}

func TestFunctionalMarkerlessLogicalPartitions(t *testing.T) {
	cases := []struct{ format, first, second string }{
		{"openai-response", "event: response.created\ndata: {\"type\":\"response.created\",\"status\":\"in_progress\"}", "event: response.completed\ndata: {\"type\":\"response.completed\",\"status\":\"completed\"}"},
		{"interactions", "event: interaction.status_update\ndata: {\"event_type\":\"interaction.status_update\",\"status\":\"in_progress\"}", "event: step.start\ndata: {\"event_type\":\"step.start\",\"index\":0,\"step\":{\"type\":\"model_output\"}}"},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			input := []byte(tc.first + tc.second + "\n\n")
			want := []byte(tc.first + "\n\n" + tc.second + "\n\n")
			for split := 0; split <= len(input); split++ {
				got := functionalProtocolParts(t, tc.format, input[:split], input[split:])
				if !bytes.Equal(got, want) {
					t.Fatalf("split=%d output=%q, want %q", split, got, want)
				}
				requireValidResponsesSSE(t, got, 2)
			}
			parts := make([][]byte, len(input))
			for i := range input {
				parts[i] = input[i : i+1]
			}
			got := functionalProtocolParts(t, tc.format, parts...)
			if !bytes.Equal(got, want) {
				t.Fatalf("one-byte output=%q, want %q", got, want)
			}
		})
	}
}

func TestFunctionalCompleteLogicalEventAboveLimit(t *testing.T) {
	input := []byte("event: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"text\":\"" + strings.Repeat("x", maxPendingStreamBytes) + "\"}")
	want := append(bytes.Clone(input), '\n', '\n')
	for _, parts := range [][][]byte{
		{input},
		{input[:maxPendingStreamBytes], input[maxPendingStreamBytes:]},
		{input, []byte("ev")},
	} {
		got := functionalProtocolParts(t, "openai-response", parts...)
		if !bytes.Equal(got, want) {
			t.Fatalf("output=%d bytes, want %d", len(got), len(want))
		}
	}
	two := append(bytes.Clone(input), input...)
	if got := functionalProtocolParts(t, "openai-response", two); !bytes.Equal(got, append(bytes.Clone(want), want...)) {
		t.Fatal("two complete large logical units differ")
	}
	r := newStreamChunkRewriter("client")
	r.format, r.frameRawJSONAsSSE = "openai-response", true
	chunks, err := r.Write(input[:maxPendingStreamBytes+1])
	if err == nil || !strings.Contains(err.Error(), "stream pending data exceeds") || len(chunks) != 0 || r.pending != nil || r.sse.buf != nil {
		t.Fatalf("incomplete overflow=(%d,%v)", len(chunks), err)
	}
	flushed, flushErr := r.Flush()
	if flushErr != nil || len(bytes.Join(flushed, nil)) != 0 {
		t.Fatalf("overflow flush=(%d,%v)", len(flushed), flushErr)
	}
}

func TestFunctionalSSEClassificationPartitions(t *testing.T) {
	rawA := `{"type":"response.created","model":"upstream"}`
	rawB := `{"type":"response.completed","model":"upstream"}`
	mixed := rawA + " " + rawB + "\ndata: [DONE]\n\n"
	mixedWant := "event: response.created\ndata: {\"type\":\"response.created\",\"model\":\"client\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"model\":\"client\"}\n\ndata: [DONE]\n\n"
	cases := []struct{ name, input, want string }{
		{"unknown true", "true\ndata: {\"model\":\"upstream\"}\n\n", "true\ndata: {\"model\":\"client\"}\n\n"},
		{"unknown false", "false\ndata: {\"model\":\"upstream\"}\n\n", "false\ndata: {\"model\":\"client\"}\n\n"},
		{"unknown null", "null\ndata: {\"model\":\"upstream\"}\n\n", "null\ndata: {\"model\":\"client\"}\n\n"},
		{"unknown number", "123\ndata: {\"model\":\"upstream\"}\n\n", "123\ndata: {\"model\":\"client\"}\n\n"},
		{"extension then split data", "x-vendor-field\ndata: {\"model\":\"upstream\"}\n\n", "x-vendor-field\ndata: {\"model\":\"client\"}\n\n"},
		{"multiple raw then SSE", mixed, mixedWant},
		{"raw scalar", "true", "data: true\n\n"},
		{"colonless scalar event", "true\n\n", "true\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for split := 0; split <= len(tc.input); split++ {
				got := functionalProtocolParts(t, "openai-response", []byte(tc.input[:split]), []byte(tc.input[split:]))
				if string(got) != tc.want {
					t.Fatalf("split=%d output=%q, want %q", split, got, tc.want)
				}
			}
		})
	}
}

func TestFunctionalMixedRawSSEABI(t *testing.T) {
	input := []byte(`{"type":"response.created","response":{"model":"upstream"}}` + "\n" +
		`{"type":"response.in_progress","response":{"model":"upstream"}}` + "\n\n" +
		"event: response.completed\ndata: " + `{"type":"response.completed","response":{"model":"upstream"}}` + "\n\n")
	if len(input) != 220 {
		t.Fatalf("fixture bytes=%d, want 220", len(input))
	}
	types := []string{"response.created", "response.in_progress", "response.completed"}
	check := func(t *testing.T, got []byte) {
		t.Helper()
		if !bytes.HasSuffix(got, []byte("\n\n")) {
			t.Fatalf("unterminated SSE output=%q", got)
		}
		events := 0
		for _, frame := range bytes.Split(bytes.TrimSuffix(got, []byte("\n\n")), []byte("\n\n")) {
			var data, name []byte
			for rest := frame; len(rest) > 0; {
				line, _, next := splitSSELine(rest)
				rest = next
				if bytes.HasPrefix(line, []byte("event: ")) {
					name = line[len("event: "):]
				}
				if bytes.HasPrefix(line, []byte("data:")) {
					data = sseFieldValue(line)
				}
			}
			if data == nil {
				continue
			}
			requireValidResponsesSSE(t, append(bytes.Clone(frame), '\n', '\n'), 1)
			if events >= len(types) {
				t.Fatalf("extra data event=%q", frame)
			}
			var event struct {
				Type     string `json:"type"`
				Response struct {
					Model string `json:"model"`
				} `json:"response"`
			}
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			if event.Type != types[events] || string(name) != types[events] || event.Response.Model != "client" {
				t.Fatalf("event %d: name=%q data=%s output=%q", events, name, data, got)
			}
			events++
		}
		if events != len(types) {
			t.Fatalf("data events=%d, want %d: %q", events, len(types), got)
		}
		if bytes.Contains(got, []byte("upstream")) {
			t.Fatalf("unrestored stream=%q", got)
		}
	}
	for split := 0; split <= len(input); split++ {
		t.Run(fmt.Sprintf("split=%d", split), func(t *testing.T) {
			check(t, functionalProtocolParts(t, "openai-response", input[:split], input[split:]))
		})
	}
	t.Run("bytewise", func(t *testing.T) {
		parts := make([][]byte, len(input))
		for i := range input {
			parts[i] = input[i : i+1]
		}
		check(t, functionalProtocolParts(t, "openai-response", parts...))
	})
	t.Run("forwarder", func(t *testing.T) {
		setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
		t.Cleanup(func() {
			shutdownExecutorStreams()
			resetExecutorStreamLifecycle()
			setLoadedConfigForTest(defaultConfig())
		})
		req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
			Model: "client", Format: "openai-response", SourceFormat: "openai-response", Stream: true,
			OriginalRequest: []byte(`{"model":"client","stream":true}`),
		}, StreamID: "functional-mixed"}
		reads := []pluginapi.HostModelStreamReadResponse{{Payload: input}, {Done: true}}
		emitted, hostClosed, pluginClosed, _, err := runExecutorStreamTestWithForwarded(req, reads, nil)
		if err != nil || !hostClosed || !pluginClosed {
			t.Fatalf("forwarder error=%v close=%v/%v", err, hostClosed, pluginClosed)
		}
		check(t, []byte(strings.Join(emitted, "")))
	})
}

func TestFunctionalFlushRewriteAndEmitErrors(t *testing.T) {
	for _, clean := range []bool{false, true} {
		t.Run(fmt.Sprintf("clean=%v", clean), func(t *testing.T) {
			r := newStreamChunkRewriter("client")
			r.frameRawJSONAsSSE = true
			chunks, err := r.Write([]byte("1 2 ["))
			if err != nil || len(chunks) != 0 {
				t.Fatalf("Write=(%q,%v), want buffered ambiguous scalar sequence", chunks, err)
			}
			emitErr := errors.New("expected flush emit failure")
			var emitted []byte
			emits := 0
			stream := &executorStream{call: func(method string, payload any) (json.RawMessage, error) {
				if method != pluginabi.MethodHostStreamEmit {
					return nil, fmt.Errorf("unexpected callback %s", method)
				}
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
				emits++
				emitted = bytes.Clone(emit.Payload)
				return nil, emitErr
			}}
			err = stream.flushAndEmit(r, clean)
			if emits != 1 || string(emitted) != "data: 1\n\ndata: 2\n\n" || !errors.Is(err, emitErr) || !strings.Contains(err.Error(), "incomplete raw JSON stream") {
				t.Fatalf("emits=%d output=%q error=%v", emits, emitted, err)
			}
			if strings.Index(err.Error(), "flush stream rewriter") > strings.Index(err.Error(), "emit flushed stream chunk") {
				t.Fatalf("error order=%v", err)
			}
			if err := stream.flushAndEmit(r, clean); err != nil || emits != 1 {
				t.Fatalf("second flush error=%v emits=%d, want no replay", err, emits)
			}
		})
	}
}

func TestFunctionalUnframedArraySuffixBatching(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"[]data: {}\n\n", "[]data: {}\n\n"},
		{"[{\"modelVersion\":\"upstream\"}]data: {}\n\n", "[{\"modelVersion\":\"client\"}]data: {}\n\n"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			r := newStreamChunkRewriter("client")
			r.format = "gemini"
			chunks, err := r.Write([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			var emitted [][]byte
			if err := emitRewritten(chunks, r.batchSSEOutput, func(p []byte) error {
				emitted = append(emitted, bytes.Clone(p))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(emitted) < 2 || string(emitted[0]) != "[" || string(bytes.Join(emitted, nil)) != tc.want {
				t.Fatalf("emitted=%q, want unbatched array prefix and unchanged SSE suffix", emitted)
			}
		})
	}
}

func TestFunctionalLogicalEOFPaths(t *testing.T) {
	readErr := errors.New("expected callback read failure")
	for _, tc := range []struct{ format, event, want string }{
		{"openai-response", "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"upstream\"}}", "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"client\"}}\n\n"},
		{"interactions", "event: interaction.created\ndata: {\"event_type\":\"interaction.created\",\"interaction\":{\"model\":\"upstream\"}}", "event: interaction.created\ndata: {\"event_type\":\"interaction.created\",\"interaction\":{\"model\":\"client\"}}\n\n"},
	} {
		for _, prefix := range []string{"e", "ev", "eve", "even", "event"} {
			for _, ending := range []string{"Flush", "Finish", "Done", "Error", "callback error"} {
				t.Run(tc.format+"/"+prefix+"/"+ending, func(t *testing.T) {
					input := []byte(tc.event + prefix)
					if ending == "Flush" || ending == "Finish" {
						r := newStreamChunkRewriter("client")
						r.format, r.frameRawJSONAsSSE = tc.format, true
						chunks, err := r.Write(input)
						if err != nil || len(chunks) != 0 {
							t.Fatalf("Write=(%q,%v), want pending logical boundary", chunks, err)
						}
						if ending == "Flush" {
							chunks, err = r.Flush()
						} else {
							chunks, err = r.Finish()
						}
						if err != nil || string(bytes.Join(chunks, nil)) != tc.want {
							t.Fatalf("%s=(%q,%v), want %q", ending, chunks, err, tc.want)
						}
						return
					}
					var emitted []byte
					var closeError string
					reads, hostCloses, pluginCloses := 0, 0, 0
					stream := &executorStream{
						originalModel: "client", format: tc.format, frameRawJSONAsSSE: true,
						pluginStreamID: "functional-plugin", hostStreamID: "functional-host",
						call: func(method string, payload any) (json.RawMessage, error) {
							switch method {
							case pluginabi.MethodHostModelStreamRead:
								reads++
								if ending == "callback error" && reads == 2 {
									return nil, readErr
								}
								if reads != 1 {
									return nil, fmt.Errorf("unexpected read %d", reads)
								}
								chunk := pluginapi.HostModelStreamReadResponse{Payload: input}
								if ending == "Done" {
									chunk.Done = true
								}
								if ending == "Error" {
									chunk.Done, chunk.Error = true, "expected terminal failure"
								}
								return json.Marshal(chunk)
							case pluginabi.MethodHostStreamEmit:
								raw, err := json.Marshal(payload)
								if err != nil {
									return nil, err
								}
								var emit struct {
									StreamID string `json:"stream_id"`
									Payload  []byte `json:"payload"`
								}
								if err := json.Unmarshal(raw, &emit); err != nil {
									return nil, err
								}
								if emit.StreamID != "functional-plugin" {
									return nil, fmt.Errorf("emit id=%q", emit.StreamID)
								}
								emitted = append(emitted, emit.Payload...)
							case pluginabi.MethodHostModelStreamClose:
								hostCloses++
								if payload.(pluginapi.HostModelStreamCloseRequest).StreamID != "functional-host" {
									return nil, errors.New("wrong host stream id")
								}
							case pluginabi.MethodHostStreamClose:
								pluginCloses++
								raw, err := json.Marshal(payload)
								if err != nil {
									return nil, err
								}
								var close struct {
									Error string `json:"error"`
								}
								if err := json.Unmarshal(raw, &close); err != nil {
									return nil, err
								}
								closeError = close.Error
							default:
								return nil, fmt.Errorf("unexpected callback %s", method)
							}
							return json.RawMessage(`{}`), nil
						},
					}
					err := runStreamForward(stream)
					if string(emitted) != tc.want || hostCloses != 1 {
						t.Fatalf("output=%q host closes=%d error=%v", emitted, hostCloses, err)
					}
					if ending == "callback error" {
						if !errors.Is(err, readErr) || pluginCloses != 0 {
							t.Fatalf("error=%v plugin closes=%d", err, pluginCloses)
						}
					} else {
						if err != nil || pluginCloses != 1 {
							t.Fatalf("error=%v plugin closes=%d", err, pluginCloses)
						}
						if ending == "Error" && !strings.Contains(closeError, "expected terminal failure") {
							t.Fatalf("close error=%q", closeError)
						}
						if ending == "Done" && closeError != "" {
							t.Fatalf("close error=%q", closeError)
						}
					}
				})
			}
		}
	}
}

func TestFunctionalLargeLogicalMetadataWait(t *testing.T) {
	for _, format := range []string{"openai-response", "interactions"} {
		for _, lineEnd := range []string{"\n", "\r", "\r\n"} {
			t.Run(fmt.Sprintf("%s/line-end=%q", format, lineEnd), func(t *testing.T) {
				name, discriminator, modelField := "response.created", "type", "response"
				if format == "interactions" {
					name, discriminator, modelField = "interaction.created", "event_type", "interaction"
				}
				input := []byte("event: " + name + lineEnd + "data: {\"" + discriminator + "\":\"" + name + "\",\"" + modelField + "\":{\"model\":\"upstream\"},\"text\":\"" + strings.Repeat("x", maxPendingStreamBytes) + "\"}")
				r := newStreamChunkRewriter("client")
				r.format, r.frameRawJSONAsSSE = format, true
				for _, part := range [][]byte{input[:maxPendingStreamBytes], input[maxPendingStreamBytes:], []byte(lineEnd), []byte("id: event-1" + lineEnd), []byte("retry: 1000")} {
					chunks, err := r.Write(part)
					if err != nil || len(chunks) != 0 {
						t.Fatalf("Write=(%d,%v), want waiting for metadata delimiter", len(chunks), err)
					}
				}
				chunks, err := r.Write([]byte(lineEnd + lineEnd))
				flushed, flushErr := r.Finish()
				want := append(bytes.Replace(input, []byte(`"model":"upstream"`), []byte(`"model":"client"`), 1), []byte(lineEnd+"id: event-1"+lineEnd+"retry: 1000"+lineEnd+lineEnd)...)
				if err != nil || flushErr != nil || !bytes.Equal(bytes.Join(append(chunks, flushed...), nil), want) {
					t.Fatalf("output=%d/%d errors=%v/%v", len(bytes.Join(append(chunks, flushed...), nil)), len(want), err, flushErr)
				}
			})
		}
	}
}

func TestFunctionalMixedRawSSELineEndings(t *testing.T) {
	for _, lineEnd := range []string{"\n", "\r\n", "\r"} {
		for _, bom := range []bool{false, true} {
			t.Run(fmt.Sprintf("line-end=%q/bom=%v", lineEnd, bom), func(t *testing.T) {
				input := []byte(`{"type":"response.created","response":{"model":"upstream"}}` + lineEnd +
					`{"type":"response.in_progress","response":{"model":"upstream"},"opaque":{"model":"upstream"}}` + lineEnd + lineEnd +
					"event: response.completed" + lineEnd + "data: " + `{"type":"response.completed","response":{"model":"upstream"}}` + lineEnd + lineEnd)
				want := []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"client\"}}\n\n" +
					"event: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"response\":{\"model\":\"client\"},\"opaque\":{\"model\":\"upstream\"}}\n\n" +
					"event: response.completed" + lineEnd + "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"client\"}}" + lineEnd + lineEnd)
				if bom {
					input = append([]byte{0xef, 0xbb, 0xbf}, input...)
				}
				for split := 0; split <= len(input); split++ {
					next := min(split+1, len(input))
					got := functionalProtocolParts(t, "openai-response", input[:split], input[split:next], input[next:])
					if !bytes.Equal(got, want) {
						t.Fatalf("split=%d output=%q, want %q", split, got, want)
					}
				}
				parts := make([][]byte, len(input))
				for i := range input {
					parts[i] = input[i : i+1]
				}
				if got := functionalProtocolParts(t, "openai-response", parts...); !bytes.Equal(got, want) {
					t.Fatalf("bytewise output=%q, want %q", got, want)
				}
			})
		}
	}
}

func TestFunctionalExitFormatFraming(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	t.Cleanup(func() {
		shutdownExecutorStreams()
		resetExecutorStreamLifecycle()
		setLoadedConfigForTest(defaultConfig())
	})
	for _, tc := range []struct{ exit, source, contentType, input, want string }{
		{"openai", "claude", "text/event-stream; charset=utf-8", `{"model":"upstream","choices":[]}`, `{"model":"client","choices":[]}`},
		{"gemini", "openai", "text/event-stream", `{"modelVersion":"upstream"}`, `{"modelVersion":"client"}`},
		{"openai-response", "openai", "text/event-stream", `{"type":"response.completed","response":{"model":"upstream"}}`, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"client\"}}\n\n"},
		{"claude", "openai", "text/event-stream", `{"type":"message_start","message":{"model":"upstream"}}`, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"client\"}}\n\n"},
		{"interactions", "openai", "text/event-stream", `{"event_type":"interaction.created","interaction":{"model":"upstream"}}`, "data: {\"event_type\":\"interaction.created\",\"interaction\":{\"model\":\"client\"}}\n\n"},
		{"openai-response", "claude", `application/json; profile="text/event-stream"`, `{"response":{"model":"upstream"}}`, `{"response":{"model":"client"}}`},
	} {
		t.Run(tc.exit+"/"+tc.contentType, func(t *testing.T) {
			req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
				Model: "client", Format: tc.exit, SourceFormat: tc.source, Stream: true,
				OriginalRequest: []byte(`{"model":"client","stream":true}`),
			}, StreamID: "functional-format"}
			var forwarded pluginapi.HostModelExecutionRequest
			reads := []pluginapi.HostModelStreamReadResponse{{Payload: []byte(tc.input)}, {Done: true}}
			emitted, hostClosed, pluginClosed, rawSetup, err := runExecutorStreamTestWithHostContentTypeAndForwarded(req, reads, tc.contentType, &forwarded)
			if err != nil || !hostClosed || !pluginClosed || strings.Join(emitted, "") != tc.want {
				t.Fatalf("output=%q error=%v close=%v/%v, want %q", emitted, err, hostClosed, pluginClosed, tc.want)
			}
			var setup pluginapi.ExecutorStreamResponse
			if err := json.Unmarshal(rawSetup, &setup); err != nil {
				t.Fatal(err)
			}
			if setup.Headers.Get("Content-Type") != tc.contentType || forwarded.EntryProtocol != tc.source || forwarded.ExitProtocol != tc.exit || forwarded.Model != "upstream" || string(forwarded.Body) != `{"model":"upstream","stream":true}` {
				t.Fatalf("headers=%v forwarded=%#v", setup.Headers, forwarded)
			}
		})
	}
}

func TestFunctionalCompleteSSEAboveLimit(t *testing.T) {
	for _, lineEnd := range []string{"\n", "\r\n", "\r"} {
		input := []byte("event: message_start" + lineEnd + "data: {\"type\":\"message_start\",\"message\":{\"model\":\"upstream\"},\"text\":\"" + strings.Repeat("x", maxPendingStreamBytes) + "\"}" + lineEnd + lineEnd)
		want := bytes.Replace(input, []byte(`"model":"upstream"`), []byte(`"model":"client"`), 1)
		for _, split := range []int{0, maxPendingStreamBytes} {
			t.Run(fmt.Sprintf("line-end=%q/split=%d", lineEnd, split), func(t *testing.T) {
				got := functionalProtocolParts(t, "claude", input[:split], input[split:])
				if !bytes.Equal(got, want) {
					t.Fatalf("output=%d bytes, want %d", len(got), len(want))
				}
			})
		}
	}
}

func TestFunctionalSSEClassificationLineEndings(t *testing.T) {
	for _, format := range []string{"openai", "openai-response", "claude", "gemini", "interactions"} {
		for _, lineEnd := range []string{"\n", "\r\n", "\r"} {
			for _, bom := range []bool{false, true} {
				for _, field := range []string{"true", "false", "null", "123", "x-vendor-field"} {
					t.Run(fmt.Sprintf("%s/%q/bom=%v/%s", format, lineEnd, bom, field), func(t *testing.T) {
						input := []byte(field + lineEnd + "data: {\"model\":\"upstream\",\"opaque\":{\"model\":\"upstream\"},\"text\":\"event: literal data: literal\"}" + lineEnd + lineEnd)
						want := []byte(field + lineEnd + "data: {\"model\":\"client\",\"opaque\":{\"model\":\"upstream\"},\"text\":\"event: literal data: literal\"}" + lineEnd + lineEnd)
						if bom {
							input = append([]byte{0xef, 0xbb, 0xbf}, input...)
						}
						for split := 0; split <= len(input); split++ {
							next := min(split+1, len(input))
							got := functionalProtocolParts(t, format, input[:split], input[split:next], input[next:])
							if !bytes.Equal(got, want) {
								t.Fatalf("split=%d output=%q, want %q", split, got, want)
							}
						}
						parts := make([][]byte, len(input))
						for i := range input {
							parts[i] = input[i : i+1]
						}
						if got := functionalProtocolParts(t, format, parts...); !bytes.Equal(got, want) {
							t.Fatalf("bytewise output=%q, want %q", got, want)
						}
					})
				}
			}
		}
	}
}

func functionalForwardProtocolParts(t *testing.T, format string, parts ...[]byte) []byte {
	t.Helper()
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	t.Cleanup(func() {
		shutdownExecutorStreams()
		resetExecutorStreamLifecycle()
		setLoadedConfigForTest(defaultConfig())
	})
	req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
		Model: "client", Format: format, SourceFormat: format, Stream: true,
		OriginalRequest: []byte(`{"model":"client","stream":true}`),
	}, StreamID: "functional-unknown-suffix"}
	reads := make([]pluginapi.HostModelStreamReadResponse, 0, len(parts)+1)
	for _, part := range parts {
		reads = append(reads, pluginapi.HostModelStreamReadResponse{Payload: part})
	}
	reads = append(reads, pluginapi.HostModelStreamReadResponse{Done: true})
	emitted, hostClosed, pluginClosed, _, err := runExecutorStreamTestWithForwarded(req, reads, nil)
	if err != nil || !hostClosed || !pluginClosed {
		t.Fatalf("forwarder error=%v close=%v/%v", err, hostClosed, pluginClosed)
	}
	return []byte(strings.Join(emitted, ""))
}

func TestFunctionalRawPrefixUnknownSSESuffix(t *testing.T) {
	for _, format := range []string{"openai-response", "claude", "interactions"} {
		for _, field := range []string{"x-vendor-field", "true", "false", "null", "123", `"scalar"`, "-1"} {
			for _, lineEnd := range []string{"\n", "\r\n", "\r"} {
				for _, bom := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%q/bom=%v", format, field, lineEnd, bom), func(t *testing.T) {
						input := []byte("{\"model\":\"upstream\"}" + lineEnd + field + lineEnd + "data: {\"model\":\"upstream\",\"opaque\":{\"model\":\"upstream\"}}" + lineEnd + lineEnd)
						want := []byte("data: {\"model\":\"client\"}\n\n" + field + lineEnd + "data: {\"model\":\"client\",\"opaque\":{\"model\":\"upstream\"}}" + lineEnd + lineEnd)
						if bom {
							input = append([]byte{0xef, 0xbb, 0xbf}, input...)
						}
						for split := 0; split <= len(input); split++ {
							for _, next := range []int{split, min(split+1, len(input))} {
								got := functionalProtocolParts(t, format, input[:split], input[split:next], input[next:])
								if !bytes.Equal(got, want) {
									t.Fatalf("splits=%d,%d output=%q, want %q", split, next, got, want)
								}
								normalized := bytes.ReplaceAll(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
								requireValidResponsesSSE(t, normalized, 2)
							}
						}
						parts := make([][]byte, len(input))
						for i := range input {
							parts[i] = input[i : i+1]
						}
						if got := functionalProtocolParts(t, format, parts...); !bytes.Equal(got, want) {
							t.Fatalf("bytewise output=%q, want %q", got, want)
						}
					})
				}
			}
			t.Run(format+"/"+field+"/forwarder", func(t *testing.T) {
				input := []byte("{\"model\":\"upstream\"}\n" + field + "\ndata: {\"model\":\"upstream\"}\n\n")
				want := []byte("data: {\"model\":\"client\"}\n\n" + field + "\ndata: {\"model\":\"client\"}\n\n")
				for _, split := range []int{0, 22, len(input)} {
					if got := functionalForwardProtocolParts(t, format, input[:split], input[split:]); !bytes.Equal(got, want) {
						t.Fatalf("split=%d output=%q, want %q", split, got, want)
					}
				}
				parts := make([][]byte, len(input))
				for i := range input {
					parts[i] = input[i : i+1]
				}
				if got := functionalForwardProtocolParts(t, format, parts...); !bytes.Equal(got, want) {
					t.Fatalf("bytewise forwarder output=%q, want %q", got, want)
				}
			})
		}
	}
}

func TestFunctionalUnknownSSEMetadataEOF(t *testing.T) {
	for _, format := range []string{"openai-response", "claude", "interactions"} {
		for _, metadata := range []string{"id: cursor", "retry: 1000", ": keep", "x-vendor-field: keep"} {
			for _, field := range []string{"true", "false", "null", "123", `"scalar"`, "-1"} {
				for _, lineEnd := range []string{"\n", "\r\n", "\r"} {
					for _, delimiter := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/%s/%s/%q/delimiter=%v", format, metadata, field, lineEnd, delimiter), func(t *testing.T) {
							input := []byte("data: {\"model\":\"upstream\"}" + lineEnd + lineEnd + field + lineEnd + metadata + lineEnd)
							want := []byte("data: {\"model\":\"client\"}" + lineEnd + lineEnd + field + lineEnd + metadata + lineEnd)
							if delimiter {
								input = append(input, lineEnd...)
								want = append(want, lineEnd...)
							}
							for split := 0; split <= len(input); split++ {
								for _, next := range []int{split, min(split+1, len(input))} {
									if got := functionalProtocolParts(t, format, input[:split], input[split:next], input[next:]); !bytes.Equal(got, want) {
										t.Fatalf("splits=%d,%d output=%q, want %q", split, next, got, want)
									}
								}
							}
							parts := make([][]byte, len(input))
							for i := range input {
								parts[i] = input[i : i+1]
							}
							if got := functionalProtocolParts(t, format, parts...); !bytes.Equal(got, want) {
								t.Fatalf("bytewise output=%q, want %q", got, want)
							}
						})
					}
				}
			}
			t.Run(format+"/"+metadata+"/forwarder", func(t *testing.T) {
				input := []byte("data: {\"model\":\"upstream\"}\n\ntrue\n" + metadata + "\n")
				want := []byte("data: {\"model\":\"client\"}\n\ntrue\n" + metadata + "\n")
				for _, split := range []int{0, 28, len(input)} {
					if got := functionalForwardProtocolParts(t, format, input[:split], input[split:]); !bytes.Equal(got, want) {
						t.Fatalf("split=%d output=%q, want %q", split, got, want)
					}
				}
				parts := make([][]byte, len(input))
				for i := range input {
					parts[i] = input[i : i+1]
				}
				if got := functionalForwardProtocolParts(t, format, parts...); !bytes.Equal(got, want) {
					t.Fatalf("bytewise forwarder output=%q, want %q", got, want)
				}
			})
		}
	}
}

func TestFunctionalRawJSONFlushRetainsMetadataSuffix(t *testing.T) {
	for _, format := range []string{"openai-response", "claude", "interactions"} {
		for _, suffix := range []string{"true\nid: cursor\n", "false\nretry: 1000\n", "null\n: keep\n"} {
			t.Run(format+"/"+suffix, func(t *testing.T) {
				r := newStreamChunkRewriter("client")
				r.format, r.frameRawJSONAsSSE = format, true
				chunks, err := r.rawJSONChunks([]byte("{\"model\":\"upstream\"}\n" + suffix))
				if err != nil {
					t.Fatal(err)
				}
				flushed, flushErr := r.Flush()
				want := "data: {\"model\":\"client\"}\n\n" + suffix
				if got := string(bytes.Join(append(chunks, flushed...), nil)); flushErr != nil || got != want {
					t.Fatalf("output=%q error=%v, want %q", got, flushErr, want)
				}
			})
		}
	}
}

func TestFunctionalCompleteRawScalarAboveLimit(t *testing.T) {
	input, err := json.Marshal(strings.Repeat("x", maxPendingStreamBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte("data: "), input...), '\n', '\n')
	for _, format := range []string{"openai-response", "claude", "interactions"} {
		for _, tc := range []struct {
			name  string
			parts [][]byte
		}{
			{"whole", [][]byte{input}},
			{"pending at limit", [][]byte{input[:maxPendingStreamBytes], input[maxPendingStreamBytes:]}},
			{"continued pending at limit", [][]byte{input[:1], input[1:maxPendingStreamBytes], input[maxPendingStreamBytes:]}},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				got := functionalProtocolParts(t, format, tc.parts...)
				if !bytes.Equal(got, want) {
					t.Fatalf("output=%d bytes, want %d", len(got), len(want))
				}
			})
		}
	}
}

func TestFunctionalIncompleteRawScalarAboveLimit(t *testing.T) {
	input := append([]byte{'"'}, bytes.Repeat([]byte{'x'}, maxPendingStreamBytes)...)
	for _, continued := range []bool{false, true} {
		t.Run(fmt.Sprintf("continued=%v", continued), func(t *testing.T) {
			r := newStreamChunkRewriter("client")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			part := input
			if continued {
				chunks, err := r.Write(input[:maxPendingStreamBytes])
				if err != nil || len(chunks) != 0 {
					t.Fatalf("pending Write=(%d,%v)", len(chunks), err)
				}
				part = input[maxPendingStreamBytes:]
			}
			chunks, err := r.Write(part)
			if err == nil || !strings.Contains(err.Error(), "stream pending data exceeds") || len(chunks) != 0 {
				t.Fatalf("overflow=(%d,%v)", len(chunks), err)
			}
			if r.pending != nil || r.sse.buf != nil || r.rawScan.active {
				t.Fatal("oversized incomplete scalar retained")
			}
			flushed, err := r.Finish()
			if err != nil || len(flushed) != 0 {
				t.Fatalf("Finish=(%d,%v), want no incomplete output", len(flushed), err)
			}
		})
	}
}

func TestFunctionalCompleteRawScalarBatchAboveLimit(t *testing.T) {
	value, err := json.Marshal(strings.Repeat("x", maxPendingStreamBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	input := append(bytes.Clone(value), []byte(" true")...)
	want := append(append([]byte("data: "), value...), []byte("\n\ndata: true\n\n")...)
	for _, split := range []int{0, maxPendingStreamBytes, len(value)} {
		t.Run(fmt.Sprintf("split=%d", split), func(t *testing.T) {
			got := functionalProtocolParts(t, "openai-response", input[:split], input[split:])
			if !bytes.Equal(got, want) {
				t.Fatalf("output=%d bytes, want %d", len(got), len(want))
			}
		})
	}
}

func TestFunctionalRawScalarCompletePrefixBeforeLimit(t *testing.T) {
	value, err := json.Marshal(strings.Repeat("x", maxPendingStreamBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte("data: "), value...), '\n', '\n')
	for _, oversized := range []bool{false, true} {
		for _, split := range []int{0, maxPendingStreamBytes, len(value)} {
			t.Run(fmt.Sprintf("oversized=%v/split=%d", oversized, split), func(t *testing.T) {
				tail := []byte(` {"text":"`)
				if oversized {
					tail = append(tail, bytes.Repeat([]byte{'x'}, maxPendingStreamBytes+1-len(tail))...)
				}
				input := append(bytes.Clone(value), tail...)
				r := newStreamChunkRewriter("client")
				r.format, r.frameRawJSONAsSSE = "openai-response", true
				chunks, err := r.Write(input[:split])
				if err != nil || len(chunks) != 0 {
					t.Fatalf("first Write=(%d,%v)", len(chunks), err)
				}
				chunks, err = r.Write(input[split:])
				if oversized {
					if err == nil || !strings.Contains(err.Error(), "stream pending data exceeds") || !bytes.Equal(bytes.Join(chunks, nil), want) {
						t.Fatalf("overflow=(%d,%v), want complete prefix", len(bytes.Join(chunks, nil)), err)
					}
					if r.pending != nil || r.sse.buf != nil || r.rawScan.active {
						t.Fatal("oversized tail retained")
					}
					flushed, err := r.Finish()
					if err != nil || len(flushed) != 0 {
						t.Fatalf("Finish=(%d,%v), want no replay", len(flushed), err)
					}
					return
				}
				if err != nil || len(chunks) != 0 {
					t.Fatalf("Write=(%d,%v), want pending EOF classification", len(chunks), err)
				}
				chunks, err = r.Finish()
				if err == nil || !strings.Contains(err.Error(), "incomplete raw JSON stream") || !bytes.Equal(bytes.Join(chunks, nil), want) {
					t.Fatalf("Finish=(%d,%v), want complete prefix and incomplete error", len(bytes.Join(chunks, nil)), err)
				}
			})
		}
	}
}

func TestFunctionalCompleteRawScalarUnknownSSEPartitions(t *testing.T) {
	value, err := json.Marshal(strings.Repeat("x", maxPendingStreamBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	input := append(bytes.Clone(value), []byte("\nx-vendor-field\ndata: {\"model\":\"upstream\"}\n\n")...)
	want := append(bytes.Clone(value), []byte("\nx-vendor-field\ndata: {\"model\":\"client\"}\n\n")...)
	boundary := len(value) + len("\nx-vendor-field\nd")
	type partitionCase struct {
		name  string
		parts [][]byte
	}
	cases := []partitionCase{
		{"whole", [][]byte{input}},
		{"pending at limit", [][]byte{input[:maxPendingStreamBytes], input[maxPendingStreamBytes:]}},
		{"unknown field before split data", [][]byte{input[:len(value)], input[len(value):boundary], input[boundary:]}},
	}
	for i := 0; i <= len("\nx-vendor-field"); i++ {
		cut := len(value) + i
		cases = append(cases,
			partitionCase{fmt.Sprintf("unknown prefix=%d/two reads", i), [][]byte{input[:cut], input[cut:]}},
			partitionCase{fmt.Sprintf("unknown prefix=%d/three reads", i), [][]byte{value, input[len(value):cut], input[cut:]}},
		)
	}
	parts := [][]byte{value}
	for i := len(value); i < len(input); i++ {
		parts = append(parts, input[i:i+1])
	}
	cases = append(cases, partitionCase{"bytewise suffix", parts})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newStreamChunkRewriter("client")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			var out [][]byte
			for i, part := range tc.parts {
				chunks, err := r.Write(part)
				if err != nil {
					t.Fatalf("Write %d=(%d,%v)", i, len(chunks), err)
				}
				if i < len(tc.parts)-1 && len(chunks) != 0 {
					t.Fatalf("Write %d prematurely framed ambiguous scalar: %d bytes", i, len(bytes.Join(chunks, nil)))
				}
				out = append(out, chunks...)
			}
			chunks, err := r.Finish()
			got := bytes.Join(append(out, chunks...), nil)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("output=%d bytes, want %d, error=%v; scalar unknown field must remain unchanged", len(got), len(want), err)
			}
		})
	}
}

func TestFunctionalRawScalarSSEIncompleteLimit(t *testing.T) {
	value, err := json.Marshal(strings.Repeat("x", maxPendingStreamBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprintf("previous event completed=%v", completed), func(t *testing.T) {
			r := newStreamChunkRewriter("client")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			input := append(bytes.Clone(value), []byte("\nx-vendor-field\ndata:")...)
			if completed {
				input = append(input, []byte(" {\"model\":\"upstream\"}\n\n")...)
			}
			chunks, err := r.Write(input)
			if err != nil {
				t.Fatal(err)
			}
			if completed {
				want := append(bytes.Clone(value), []byte("\nx-vendor-field\ndata: {\"model\":\"client\"}\n\n")...)
				if !bytes.Equal(bytes.Join(chunks, nil), want) {
					t.Fatal("completed SSE scalar prefix changed")
				}
			} else if len(chunks) != 0 {
				t.Fatal("incomplete SSE event emitted")
			}
			tail := []byte(` {"text":"`)
			if completed {
				tail = append([]byte("data:"), tail...)
			}
			tail = append(tail, bytes.Repeat([]byte{'x'}, maxPendingStreamBytes+1-len(tail))...)
			chunks, err = r.Write(tail)
			if err == nil || !strings.Contains(err.Error(), "stream pending data exceeds") || len(chunks) != 0 {
				t.Fatalf("incomplete suffix=(%d,%v)", len(chunks), err)
			}
			if r.pending != nil || r.sse.buf != nil || r.rawScan.active {
				t.Fatal("oversized incomplete SSE suffix retained")
			}
			chunks, err = r.Finish()
			if err != nil || len(chunks) != 0 {
				t.Fatalf("cleared suffix Finish=(%d,%v)", len(chunks), err)
			}
		})
	}
}

func TestFunctionalCompleteRawScalarAfterRawPrefix(t *testing.T) {
	value, err := json.Marshal(strings.Repeat("x", maxPendingStreamBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte(`{"model":"upstream"} `)
	want := append(append([]byte("data: {\"model\":\"client\"}\n\ndata: "), value...), '\n', '\n')
	for _, ending := range []string{"complete", "incomplete", "overflow"} {
		for _, split := range []int{0, maxPendingStreamBytes} {
			t.Run(fmt.Sprintf("%s/split=%d", ending, split), func(t *testing.T) {
				input := append(bytes.Clone(prefix), value...)
				if ending != "complete" {
					tail := []byte(` {"text":"`)
					if ending == "overflow" {
						tail = append(tail, bytes.Repeat([]byte{'x'}, maxPendingStreamBytes+1-len(tail))...)
					}
					input = append(input, tail...)
				}
				r := newStreamChunkRewriter("client")
				r.format, r.frameRawJSONAsSSE = "openai-response", true
				first, err := r.Write(input[:split])
				if err != nil {
					t.Fatal(err)
				}
				chunks, err := r.Write(input[split:])
				out := append(first, chunks...)
				if ending == "overflow" {
					if err == nil || !strings.Contains(err.Error(), "stream pending data exceeds") || !bytes.Equal(bytes.Join(out, nil), want) {
						t.Fatalf("overflow=(%d,%v), want both complete units", len(bytes.Join(out, nil)), err)
					}
					if r.pending != nil || r.sse.buf != nil || r.rawScan.active {
						t.Fatal("oversized scalar suffix retained")
					}
					flushed, err := r.Finish()
					if err != nil || len(flushed) != 0 {
						t.Fatalf("Finish=(%d,%v), want no replay", len(flushed), err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				flushed, err := r.Finish()
				out = append(out, flushed...)
				if ending == "complete" && err != nil || ending == "incomplete" && (err == nil || !strings.Contains(err.Error(), "incomplete raw JSON stream")) || !bytes.Equal(bytes.Join(out, nil), want) {
					t.Fatalf("Finish=(%d,%v), want both complete units", len(bytes.Join(out, nil)), err)
				}
			})
		}
	}
}

func TestFunctionalRawScalarEOFPartitions(t *testing.T) {
	cases := []struct{ input, want string }{
		{"true", "data: true\n\n"},
		{"false", "data: false\n\n"},
		{"null", "data: null\n\n"},
		{"123", "data: 123\n\n"},
		{"-1", "data: -1\n\n"},
		{"1.5", "data: 1.5\n\n"},
		{`"scalar"`, "data: \"scalar\"\n\n"},
		{`"opaque: data: event: upstream"`, "data: \"opaque: data: event: upstream\"\n\n"},
		{`{"model":"upstream"} true`, "data: {\"model\":\"client\"}\n\ndata: true\n\n"},
		{`{"model":"upstream"} "scalar"`, "data: {\"model\":\"client\"}\n\ndata: \"scalar\"\n\n"},
		{`{"model":"upstream"} 123 {"model":"upstream"}`, "data: {\"model\":\"client\"}\n\ndata: 123\n\ndata: {\"model\":\"client\"}\n\n"},
	}
	for _, format := range []string{"openai-response", "claude", "interactions"} {
		for _, tc := range cases {
			t.Run(format+"/"+tc.input, func(t *testing.T) {
				input := []byte(tc.input)
				for split := 0; split <= len(input); split++ {
					if got := string(functionalProtocolParts(t, format, input[:split], input[split:])); got != tc.want {
						t.Fatalf("split=%d output=%q, want %q", split, got, tc.want)
					}
				}
				parts := make([][]byte, len(input))
				for i := range input {
					parts[i] = input[i : i+1]
				}
				if got := string(functionalProtocolParts(t, format, parts...)); got != tc.want {
					t.Fatalf("bytewise output=%q, want %q", got, tc.want)
				}
			})
		}
	}
}
