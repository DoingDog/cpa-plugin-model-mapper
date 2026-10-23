package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestRawNumberContinuationPartitions(t *testing.T) {
	const object = `{"model":"upstream","model":"duplicate","mo\u0064el":"upstream","modelVersion":"upstream","response":{"model":"upstream","modelVersion":"upstream"},"message":{"model":"upstream","modelVersion":"opaque"},"interaction":{"model":"upstream"},"opaque":{"model":"upstream"},"text":"upstream 中文 \\/","numbers":[1.00,1e+09,9007199254740993]}`
	const restored = `{"model":"client","model":"client","mo\u0064el":"client","modelVersion":"client","response":{"model":"client","modelVersion":"client"},"message":{"model":"client","modelVersion":"opaque"},"interaction":{"model":"client"},"opaque":{"model":"upstream"},"text":"upstream 中文 \\/","numbers":[1.00,1e+09,9007199254740993]}`
	for _, number := range []string{"1.5", "1e+3", "1E-3", "-1.5", "-1e+3", "12.5", "1", "-1"} {
		for _, prefix := range []string{"", object + " \t"} {
			for _, framed := range []bool{false, true} {
				t.Run(fmt.Sprintf("number=%s/prefix=%v/framed=%v", number, prefix != "", framed), func(t *testing.T) {
					input := []byte(prefix + number + " " + object)
					want := strings.Replace(prefix, object, restored, 1) + number + " " + restored
					if framed {
						want = "data: " + number + "\n\ndata: " + restored + "\n\n"
						if prefix != "" {
							want = "data: " + restored + "\n\n" + want
						}
					}
					if got := rawNumberParts(t, framed, input); got != want {
						t.Fatalf("complete output=%q, want %q", got, want)
					}
					var failed []int
					for split := 0; split <= len(input); split++ {
						got := rawNumberParts(t, framed, input[:split], input[split:])
						if got != want {
							if len(failed) == 0 {
								t.Errorf("split=%d output=%q, want %q", split, got, want)
							}
							failed = append(failed, split)
						}
					}
					if len(failed) > 0 {
						t.Logf("failed two-way splits=%v of %d", failed, len(input)+1)
					}
					parts := make([][]byte, len(input))
					for i := range input {
						parts[i] = input[i : i+1]
					}
					if got := rawNumberParts(t, framed, parts...); got != want {
						t.Errorf("bytewise output=%q, want %q", got, want)
					}
				})
			}
		}
	}
}

func rawNumberParts(t *testing.T, framed bool, parts ...[]byte) string {
	t.Helper()
	r := newStreamChunkRewriter("client")
	r.format, r.frameRawJSONAsSSE = "openai-response", framed
	var out [][]byte
	for _, part := range parts {
		chunks, err := r.Write(part)
		if err != nil {
			t.Fatalf("Write(%q): %v", part, err)
		}
		out = append(out, chunks...)
	}
	chunks, err := r.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return string(bytes.Join(append(out, chunks...), nil))
}

func TestRawNumberContinuationPreservesSeparators(t *testing.T) {
	const object = `{"model":"upstream"}`
	const restored = `{"model":"client"}`
	for _, number := range []string{"1.5", "1e+3", "1E-3", "-1.5", "-1e+3", "12.5", "1", "-1"} {
		for _, prefix := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prefix=%v", number, prefix), func(t *testing.T) {
				input := []byte(" \t" + number + "\t\r\n" + object + "\r\n")
				want := " \t" + number + "\t\r\n" + restored + "\r\n"
				if prefix {
					input = append([]byte(" \t"+object+"\r\n"), input...)
					want = " \t" + restored + "\r\n" + want
				}
				if got := rawNumberParts(t, false, input); got != want {
					t.Fatalf("whole output=%q, want %q", got, want)
				}
				for split := 0; split <= len(input); split++ {
					if got := rawNumberParts(t, false, input[:split], input[split:]); got != want {
						t.Errorf("split=%d output=%q, want %q", split, got, want)
					}
				}
				parts := make([][]byte, len(input))
				for i := range input {
					parts[i] = input[i : i+1]
				}
				if got := rawNumberParts(t, false, parts...); got != want {
					t.Errorf("bytewise output=%q, want %q", got, want)
				}
			})
		}
	}
}

func TestRawNumberContinuationTrace(t *testing.T) {
	t.Run("minimal", func(t *testing.T) {
		r := newStreamChunkRewriter("client")
		r.format = "openai-response"
		var out [][]byte
		for _, part := range []string{"1", `.5 {"model":"upstream"}`} {
			chunks, err := r.Write([]byte(part))
			t.Logf("Write(%q) chunks=%q pending=%q rawScan=%v sse.started=%v sse.buf=%q error=%v", part, chunks, r.pending, r.rawScan.active, r.sse.started, r.sse.buf, err)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, chunks...)
		}
		tail, err := r.Finish()
		got := string(bytes.Join(append(out, tail...), nil))
		if want := `1.5 {"model":"client"}`; err != nil || got != want {
			t.Fatalf("Finish output=%q error=%v, want %q", got, err, want)
		}
	})

	for _, number := range []string{"1", "-1", "1.5", "1e+3", "1E-3", "1.", "1e", "1e+"} {
		for _, prefix := range []string{"", `{"model":"upstream"}`} {
			for _, framed := range []bool{false, true} {
				for _, finish := range []bool{false, true} {
					t.Run(fmt.Sprintf("EOF/%s/prefix=%v/framed=%v/finish=%v", number, prefix != "", framed, finish), func(t *testing.T) {
						r := newStreamChunkRewriter("client")
						r.frameRawJSONAsSSE = framed
						input := []byte(prefix + " \t" + number)
						before := bytes.Clone(input)
						chunks, err := r.Write(input)
						if err != nil || !bytes.Equal(input, before) {
							t.Fatalf("Write=%q error=%v input=%q", chunks, err, input)
						}
						wantPrefix := strings.Replace(prefix, "upstream", "client", 1)
						if framed && prefix != "" {
							wantPrefix = "data: " + wantPrefix + "\n\n"
						}
						if got := string(bytes.Join(chunks, nil)); got != wantPrefix {
							t.Fatalf("Write output=%q, want completed prefix %q only", got, wantPrefix)
						}
						input[len(input)-1] = 'x'
						var tail [][]byte
						if finish {
							tail, err = r.Finish()
						} else {
							tail, err = r.Flush()
						}
						want := wantPrefix + " \t" + number
						incomplete := !json.Valid([]byte(number))
						if framed {
							want = wantPrefix
							if !incomplete {
								want += "data: " + number + "\n\n"
							}
						}
						if got := string(bytes.Join(append(chunks, tail...), nil)); got != want || (err != nil) != (framed && incomplete) {
							t.Fatalf("final output=%q error=%v, want %q", got, err, want)
						}
						if tail, err := r.Finish(); err != nil || len(tail) != 0 {
							t.Fatalf("replayed tail=%q error=%v", tail, err)
						}
					})
				}
			}
		}
	}

	for _, tc := range []struct{ first, next, want string }{
		{"1", " \t", "1 \t"},
		{"1", `{"model":"upstream"}`, `1{"model":"client"}`},
		{"1.", `5 {"model":"upstream"}`, `1.5 {"model":"client"}`},
		{"1e", `+3 {"model":"upstream"}`, `1e+3 {"model":"client"}`},
		{"1e+", `3 {"model":"upstream"}`, `1e+3 {"model":"client"}`},
	} {
		t.Run("boundary/"+tc.first+"/"+tc.next, func(t *testing.T) {
			r := newStreamChunkRewriter("client")
			chunks, err := r.Write([]byte(tc.first))
			if err != nil || len(chunks) != 0 {
				t.Fatalf("unresolved Write=%q error=%v", chunks, err)
			}
			input := []byte(tc.next)
			chunks, err = r.Write(input)
			if got := string(bytes.Join(chunks, nil)); err != nil || got != tc.want || len(r.pending) != 0 {
				t.Fatalf("boundary output=%q error=%v pending=%q, want immediate %q", got, err, r.pending, tc.want)
			}
			input[0] = 'x'
			if got := string(bytes.Join(chunks, nil)); got != tc.want {
				t.Fatalf("output aliases input: %q", got)
			}
		})
	}
}

func TestRawNumberContinuationKeepsImmediateContainers(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"model":"upstream"}`, `{"model":"client"}`},
		{`[{"model":"upstream"}]`, `[{"model":"client"}]`},
	} {
		r := newStreamChunkRewriter("client")
		chunks, err := r.Write([]byte(tc.input))
		if got := string(bytes.Join(chunks, nil)); err != nil || got != tc.want || len(r.pending) != 0 {
			t.Fatalf("Write(%q)=(%q,%v) pending=%q, want immediate %q", tc.input, got, err, r.pending, tc.want)
		}
	}
}

func TestRawNumberContinuationForwarder(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	t.Cleanup(func() {
		shutdownExecutorStreams()
		resetExecutorStreamLifecycle()
		setLoadedConfigForTest(defaultConfig())
	})
	const object = `{"id":"resp-number","object":"response","status":"completed","model":"upstream","output":[],"opaque":{"model":"upstream"}}`
	const restored = `{"id":"resp-number","object":"response","status":"completed","model":"client","output":[],"opaque":{"model":"upstream"}}`
	for _, format := range []string{"openai", "openai-response", "claude", "gemini", "interactions"} {
		for _, number := range []string{"1", "1.", "1e", "1e+"} {
			t.Run(format+"/error-flush/"+number, func(t *testing.T) {
				req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "client", Format: format, SourceFormat: format, Stream: true, OriginalRequest: []byte(`{"model":"client","stream":true}`)}, StreamID: "raw-number-flush"}
				reads := []pluginapi.HostModelStreamReadResponse{{Payload: []byte(object + " \t" + number)}, {Error: "primary upstream error"}}
				emitted, hostClosed, pluginClosed, _, err := runExecutorStreamTestWithHostContentTypeAndForwarded(req, reads, "application/json", nil)
				if got, want := strings.Join(emitted, ""), restored+" \t"+number; err != nil || !hostClosed || !pluginClosed || got != want {
					t.Fatalf("error-flush output=%q close=%v/%v error=%v, want %q", got, hostClosed, pluginClosed, err, want)
				}
			})
		}
		for _, number := range []string{"1.5", "1e+3", "-1.5"} {
			for _, split := range []int{0, 1} {
				t.Run(fmt.Sprintf("%s/%s/split=%d", format, number, split), func(t *testing.T) {
					input := number + " " + object
					reads := []pluginapi.HostModelStreamReadResponse{{Payload: []byte(input), Done: true}}
					if split != 0 {
						// 负数在完整数字前缀后切分。
						cut := split
						if number[0] == '-' {
							cut++
						}
						reads = []pluginapi.HostModelStreamReadResponse{{Payload: []byte(input[:cut])}, {Payload: []byte(input[cut:]), Done: true}}
					}
					req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
						Model: "client", Format: format, SourceFormat: format, Stream: true,
						OriginalRequest: []byte(`{"model":"client","stream":true}`),
					}, StreamID: "raw-number"}
					var forwarded pluginapi.HostModelExecutionRequest
					emitted, hostClosed, pluginClosed, setupRaw, err := runExecutorStreamTestWithHostContentTypeAndForwarded(req, reads, "application/json", &forwarded)
					var setup pluginapi.ExecutorStreamResponse
					if decodeErr := json.Unmarshal(setupRaw, &setup); decodeErr != nil {
						t.Fatal(decodeErr)
					}
					if err != nil || !hostClosed || !pluginClosed || setup.Headers.Get("Content-Type") != "application/json" || forwarded.Model != "upstream" || string(forwarded.Body) != `{"model":"upstream","stream":true}` {
						t.Fatalf("setup=%s forwarded=%+v close=%v/%v error=%v", setupRaw, forwarded, hostClosed, pluginClosed, err)
					}
					if got, want := strings.Join(emitted, ""), number+" "+restored; got != want {
						t.Errorf("emitted=%q output=%q, want %q", emitted, got, want)
					}
				})
			}
		}
	}
}
