package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const functionalResponsesRawPretty = `{
"type":"response.completed",
"response":{"id":"resp_pretty","object":"response","model":"grok-4.7","status":"completed","output":[]}
}`
const functionalResponsesRawRestored = `{"type":"response.completed","response":{"id":"resp_pretty","object":"response","model":"grok-4.6","status":"completed","output":[]}}`

func TestFunctionalResponsesRawJSONFraming(t *testing.T) {
	if !json.Valid([]byte(functionalResponsesRawPretty)) || strings.Count(functionalResponsesRawPretty, "\n") != 3 {
		t.Fatal("fixture must contain three actual LF bytes in a complete JSON value")
	}
	wantCompletion := "event: response.completed\ndata: " + functionalResponsesRawRestored + "\n\n"
	for _, tc := range []struct{ name, input, want string }{
		{"LF", functionalResponsesRawPretty, wantCompletion},
		{"CRLF", strings.ReplaceAll(functionalResponsesRawPretty, "\n", "\r\n"), wantCompletion},
		{"CR", strings.ReplaceAll(functionalResponsesRawPretty, "\n", "\r"), wantCompletion},
		{"blank-lines-tabs", " \t\r\n{\n\n\t\"type\" : \"response.completed\",\n\t\"response\" : {\"id\":\"resp_pretty\",\"object\":\"response\",\"model\":\"grok-4.7\",\"status\":\"completed\",\"output\":[]}\n}\r\n\t", wantCompletion},
		{"lexical-and-whitelist", `{
 "type" : "response.completed", "model" : "upstream", "model" : "duplicate", "modelVersion" : "upstream",
 "response" : {"model":"upstream", "modelVersion":"upstream", "opaque":{"model":"upstream"}},
 "message" : {"model":"upstream", "modelVersion":"opaque"}, "interaction" : {"model":"upstream"},
 "mo\u0064el":"upstream", "dup":1, "dup":2, "numbers":[1.00,1e+09,9007199254740993],
 "text":"opaque grok-4.7 中文 a \/ \\ \" \n \r \t", "\u0061b":"\u4e2d", "tool":{"model":"upstream"}
}`, "event: response.completed\ndata: " + `{"type":"response.completed","model":"grok-4.6","model":"grok-4.6","modelVersion":"grok-4.6","response":{"model":"grok-4.6","modelVersion":"grok-4.6","opaque":{"model":"upstream"}},"message":{"model":"grok-4.6","modelVersion":"opaque"},"interaction":{"model":"grok-4.6"},"mo\u0064el":"grok-4.6","dup":1,"dup":2,"numbers":[1.00,1e+09,9007199254740993],"text":"opaque grok-4.7 中文 a \/ \\ \" \n \r \t","\u0061b":"\u4e2d","tool":{"model":"upstream"}}` + "\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !json.Valid([]byte(tc.input)) {
				t.Fatal("invalid test fixture")
			}
			r := newStreamChunkRewriter("grok-4.6")
			r.format, r.frameRawJSONAsSSE = "openai-response", true
			chunks, err := r.Write([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			finished, err := r.Finish()
			if got := string(bytes.Join(append(chunks, finished...), nil)); err != nil || got != tc.want {
				t.Fatalf("wire=%q want=%q error=%v", got, tc.want, err)
			}
		})
	}

	t.Run("partitions-and-order", func(t *testing.T) {
		suffix := "id: tail\nevent: response.completed\ndata: " + functionalResponsesRawRestored + "\n\n"
		for _, tc := range []struct{ name, input, suffix, want string }{
			{"single", functionalResponsesRawPretty, "", wantCompletion},
			{"sequence", functionalResponsesRawPretty + "\n\t" + functionalResponsesRawPretty, "", wantCompletion + wantCompletion},
			{"real-SSE-suffix", functionalResponsesRawPretty, "\n\n" + suffix, wantCompletion + suffix},
		} {
			for split := -1; split <= len(tc.input); split++ {
				t.Run(fmt.Sprintf("%s/split=%d", tc.name, split), func(t *testing.T) {
					parts := [][]byte{[]byte(tc.input)}
					if split >= 0 {
						parts = [][]byte{[]byte(tc.input[:split]), []byte(tc.input[split:])}
					} else {
						parts = nil
						for i := range tc.input {
							parts = append(parts, []byte(tc.input[i:i+1]))
						}
					}
					parts[len(parts)-1] = append(parts[len(parts)-1], tc.suffix...)
					r := newStreamChunkRewriter("grok-4.6")
					r.format, r.frameRawJSONAsSSE = "openai-response", true
					var out [][]byte
					for _, part := range parts {
						chunks, err := r.Write(part)
						if err != nil {
							t.Fatal(err)
						}
						out = append(out, chunks...)
					}
					finished, err := r.Finish()
					if got := string(bytes.Join(append(out, finished...), nil)); err != nil || got != tc.want {
						t.Fatalf("wire=%q want=%q error=%v", got, tc.want, err)
					}
				})
			}
		}
	})

	t.Run("unchanged-paths", func(t *testing.T) {
		input := "{\n\"type\":\"response.completed\",\n\"model\":\"upstream\"\n}"
		restored := "{\n\"type\":\"response.completed\",\n\"model\":\"grok-4.6\"\n}"
		generic := "data: {\ndata: \"type\":\"response.completed\",\ndata: \"model\":\"grok-4.6\"\ndata: }\n\n"
		for _, format := range []string{"", "openai", "claude", "gemini", "interactions", "openai-response"} {
			for _, framed := range []bool{false, true} {
				if format == "openai-response" && framed {
					continue
				}
				t.Run(fmt.Sprintf("%s/framed=%v", format, framed), func(t *testing.T) {
					r := newStreamChunkRewriter("grok-4.6")
					r.format, r.frameRawJSONAsSSE = format, framed
					chunks, err := r.Write([]byte(input))
					if err != nil {
						t.Fatal(err)
					}
					finished, err := r.Finish()
					want := restored
					if framed {
						want = generic
						if format == "claude" {
							want = "event: response.completed\n" + want
						}
						if format == "openai" {
							want += "data: [DONE]\n\n"
						}
					}
					if got := string(bytes.Join(append(chunks, finished...), nil)); err != nil || got != want {
						t.Fatalf("wire=%q want=%q error=%v", got, want, err)
					}
				})
			}
		}
		native := ": keep\r\nid: native\r\nevent: response.completed\r\ndata: {\r\ndata: \"model\":\"grok-4.6\"\r\ndata: }\r\n\r\n"
		r := newStreamChunkRewriter("grok-4.6")
		r.format, r.frameRawJSONAsSSE = "openai-response", true
		chunks, err := r.Write([]byte(native))
		if err != nil {
			t.Fatal(err)
		}
		finished, err := r.Finish()
		if got := string(bytes.Join(append(chunks, finished...), nil)); err != nil || got != native {
			t.Fatalf("native wire=%q want=%q error=%v", got, native, err)
		}
	})

	t.Run("ownership-and-complete-prefix", func(t *testing.T) {
		r := newStreamChunkRewriter("grok-4.6")
		r.format, r.frameRawJSONAsSSE = "openai-response", true
		first, second := []byte(functionalResponsesRawPretty[:20]), []byte(functionalResponsesRawPretty[20:])
		chunks, err := r.Write(first)
		if err != nil || len(chunks) != 0 {
			t.Fatalf("pending write=(%q,%v)", chunks, err)
		}
		for i := range first {
			first[i] = 'x'
		}
		chunks, err = r.Write(second)
		if err != nil || string(bytes.Join(chunks, nil)) != wantCompletion {
			t.Fatalf("completed write=(%q,%v)", chunks, err)
		}
		for i := range second {
			second[i] = 'x'
		}
		if got := string(bytes.Join(chunks, nil)); got != wantCompletion {
			t.Fatalf("input mutation changed output=%q", got)
		}
		snapshot := bytes.Clone(bytes.Join(chunks, nil))
		prefix, err := r.Write([]byte(functionalResponsesRawPretty + " {"))
		if err != nil || string(bytes.Join(prefix, nil)) != wantCompletion {
			t.Fatalf("prefix=(%q,%v)", prefix, err)
		}
		finished, err := r.Finish()
		if err == nil || !strings.Contains(err.Error(), "incomplete raw JSON stream") || len(finished) != 0 || !bytes.Equal(bytes.Join(chunks, nil), snapshot) {
			t.Fatalf("Finish=(%q,%v), previous output=%q", finished, err, chunks)
		}
	})

	t.Run("private-framing-error", func(t *testing.T) {
		r := newStreamChunkRewriter("grok-4.6")
		r.format, r.frameRawJSONAsSSE = "openai-response", true
		input := []byte(`{"type":`)
		framed, err := r.frameRawJSON(input)
		if err == nil || len(framed) != 0 || r.framedRawJSON || r.rawJSONBoundary || string(input) != `{"type":` {
			t.Fatalf("invalid framing=(%q,%v), state=%+v input=%q", framed, err, r, input)
		}
	})
}

func TestFunctionalResponsesRawJSONForwarder(t *testing.T) {
	for _, contentType := range []string{"", "text/event-stream", "application/json"} {
		t.Run("content-type="+contentType, func(t *testing.T) {
			setLoadedConfigForTest(Config{GlobalRules: "grok-4.6=>grok-4.7"})
			request := rpcExecutorRequest{
				ExecutorRequest: pluginapi.ExecutorRequest{
					Model: "grok-4.6", Format: "openai-response", SourceFormat: "openai-response", Stream: true,
					OriginalRequest: []byte(`{"model":"grok-4.6","stream":true,"opaque":{"model":"grok-4.6"}}`),
				},
				HostCallbackID: "pretty-callback", StreamID: "functional-pretty",
			}
			var forwarded pluginapi.HostModelExecutionRequest
			emitted, closedHost, closedPlugin, setup, err := runExecutorStreamTestWithHostContentTypeAndForwarded(request, []pluginapi.HostModelStreamReadResponse{{Payload: []byte(functionalResponsesRawPretty)}, {Done: true}}, contentType, &forwarded)
			if err != nil || !closedHost || !closedPlugin {
				t.Fatalf("callback error=%v closedHost=%v closedPlugin=%v", err, closedHost, closedPlugin)
			}
			var response struct{ Headers http.Header }
			if err := json.Unmarshal(setup, &response); err != nil {
				t.Fatal(err)
			}
			wantType := contentType
			if wantType == "" {
				wantType = "text/event-stream"
			}
			if response.Headers.Get("Content-Type") != wantType || forwarded.Model != "grok-4.7" || string(forwarded.Body) != `{"model":"grok-4.7","stream":true,"opaque":{"model":"grok-4.6"}}` || forwarded.EntryProtocol != "openai-response" || forwarded.ExitProtocol != "openai-response" || !forwarded.Stream {
				t.Fatalf("setup=%s forwarded=%+v", setup, forwarded)
			}
			want := "event: response.completed\ndata: " + functionalResponsesRawRestored + "\n\n"
			if contentType == "application/json" {
				want = "{\n\"type\":\"response.completed\",\n\"response\":{\"id\":\"resp_pretty\",\"object\":\"response\",\"model\":\"grok-4.6\",\"status\":\"completed\",\"output\":[]}\n}"
			}
			if got := strings.Join(emitted, ""); got != want {
				t.Fatalf("emitted=%q want=%q", got, want)
			}
		})
	}
}
