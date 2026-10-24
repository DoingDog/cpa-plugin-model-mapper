package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestRawNeutralWhitespaceLinearAllocations(t *testing.T) {
	for _, size := range []int{8192, 16384, 32768} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			input := bytes.Repeat([]byte(" \t\r\n"), size/4)
			want := string(input) + `{"model":"client"}`
			allocs := testing.AllocsPerRun(3, func() {
				r := newStreamChunkRewriter("client")
				r.format = "openai"
				for i := range input {
					chunks, err := r.Write(input[i : i+1])
					if err != nil || len(chunks) != 0 {
						t.Fatalf("whitespace Write=(%q,%v)", chunks, err)
					}
				}
				chunks, err := r.Write([]byte(`{"model":"upstream"}`))
				if got := string(bytes.Join(chunks, nil)); err != nil || got != want {
					t.Fatalf("immediate output bytes=%d error=%v, want exact %d bytes", len(got), err, len(want))
				}
				if chunks, err := r.Finish(); err != nil || len(chunks) != 0 {
					t.Fatalf("Finish=(%q,%v), want no replay", chunks, err)
				}
			})
			// 空白 callback 复用自有 pending，分配次数仅随 buffer 扩容增长。
			if allocs > 64 {
				t.Fatalf("%d bytewise whitespace bytes: allocations=%v, want <=64", size, allocs)
			}
		})
	}
}

func TestRawNeutralWhitespacePendingLimit(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(fmt.Sprint(overflow), func(t *testing.T) {
			r := newStreamChunkRewriter("client")
			space := bytes.Repeat([]byte{'\n'}, maxPendingStreamBytes-1)
			for _, part := range [][]byte{space, []byte{'\r'}, nil} {
				if chunks, err := r.Write(part); err != nil || len(chunks) != 0 {
					t.Fatalf("boundary Write output bytes=%d error=%v", len(bytes.Join(chunks, nil)), err)
				}
			}
			if overflow {
				chunks, err := r.Write([]byte{'\n'})
				if err == nil || len(chunks) != 0 || len(r.pending) != 0 {
					t.Fatalf("overflow chunks=%q error=%v pending bytes=%d", chunks, err, len(r.pending))
				}
			}
			chunks, err := r.Write([]byte(`{"model":"upstream"}`))
			want := []byte(`{"model":"client"}`)
			if !overflow {
				want = append(append(bytes.Clone(space), '\r'), want...)
			}
			if got := bytes.Join(chunks, nil); err != nil || !bytes.Equal(got, want) {
				t.Fatalf("after boundary output bytes=%d error=%v, want exact %d bytes", len(got), err, len(want))
			}
		})
	}
}

func TestRawNeutralWhitespaceDocument(t *testing.T) {
	input := []byte("\r\n\r\n{\"model\":\"upstream\"}")
	want := "\r\n\r\n{\"model\":\"client\"}"
	if got := rawNumberParts(t, false, input[:3], input[3:]); got != want {
		t.Fatalf("CRLF split output=%q, want %q", got, want)
	}
}

func TestRawNeutralWhitespaceSequence(t *testing.T) {
	want := "{}\n\n1.5 {\"model\":\"client\"}"
	for _, parts := range [][][]byte{
		{[]byte("{}"), []byte("\n\n"), []byte(`1.5 {"model":"upstream"}`)},
		{[]byte("{}"), []byte("\n\n1.5 {\"model\":\"upstream\"}")},
	} {
		if got := rawNumberParts(t, false, parts...); got != want {
			t.Errorf("parts=%q output=%q, want %q", parts, got, want)
		}
	}
}

func TestRawScalarLookingSSEKeepsUnknownObject(t *testing.T) {
	for _, field := range []string{"1.5", "true", "false", "null", `"scalar"`} {
		for _, eol := range []string{"\n", "\r", "\r\n"} {
			for _, framed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/eol=%q/framed=%v", field, eol, framed), func(t *testing.T) {
					input := []byte(field + eol + `{"model":"upstream"}` + eol + `data: {"model":"upstream"}` + eol + eol)
					want := field + eol + `{"model":"upstream"}` + eol + `data: {"model":"client"}` + eol + eol
					if got := rawNumberParts(t, framed, input); got != want {
						t.Fatalf("output=%q, want only data JSON restored %q", got, want)
					}
				})
			}
		}
	}
}

func classificationPartitions(input []byte) [][][]byte {
	parts := [][][]byte{{input}}
	for split := 0; split <= len(input); split++ {
		parts = append(parts, [][]byte{input[:split], input[split:]})
	}
	bytewise := make([][]byte, len(input))
	for i := range input {
		bytewise[i] = input[i : i+1]
	}
	return append(parts, bytewise)
}

func TestRawNeutralWhitespacePartitions(t *testing.T) {
	const object = `{"model":"upstream","modelVersion":"upstream","response":{"model":"upstream","modelVersion":"upstream"},"message":{"model":"upstream"},"interaction":{"model":"upstream"},"opaque":{"model":"upstream"},"text":"upstream 中文 \\/","n":1.00}`
	const restored = `{"model":"client","modelVersion":"client","response":{"model":"client","modelVersion":"client"},"message":{"model":"client"},"interaction":{"model":"client"},"opaque":{"model":"upstream"},"text":"upstream 中文 \\/","n":1.00}`
	spaces := []string{" ", "\t", "\n", "\r", "\r\n", " \t\r\n \t", "\n\n\n"}
	for _, first := range []string{"\n", "\r", "\r\n"} {
		for _, second := range []string{"\n", "\r", "\r\n"} {
			spaces = append(spaces, first+second)
		}
	}
	for _, space := range spaces {
		for _, scalar := range []string{"1", "1.5", "1e+3", "1E-3", "-1.5", "-1e+3", "true", "false", "null", `"a\\\"b"`} {
			for _, prefix := range []string{"", "{}", object, "[" + object + space + "]"} {
				t.Run(fmt.Sprintf("space=%q/scalar=%s/prefix=%d", space, scalar, len(prefix)), func(t *testing.T) {
					input := []byte(prefix + space + scalar + space + object + space)
					want := strings.ReplaceAll(prefix, object, restored) + space + scalar + space + restored + space
					for i, parts := range classificationPartitions(input) {
						if got := rawNumberParts(t, false, parts...); got != want {
							t.Fatalf("partition=%d output=%q, want %q", i, got, want)
						}
					}
				})
			}
		}
		for _, body := range []struct{ input, want string }{{object, restored}, {"[" + space + object + space + "]", "[" + space + restored + space + "]"}, {"", ""}} {
			input := []byte(space + body.input + space)
			for i, parts := range classificationPartitions(input) {
				if got, want := rawNumberParts(t, false, parts...), space+body.want+space; got != want {
					t.Fatalf("space=%q partition=%d output=%q, want %q", space, i, got, want)
				}
			}
		}
	}
}

func TestRawNeutralWhitespaceImmediateOwnership(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"model":"upstream"}`, `{"model":"client"}`},
		{`[{"modelVersion":"upstream"}]`, `[{"modelVersion":"client"}]`},
		{`{"model":"upstream"} {"text":"`, `{"model":"client"}`},
	} {
		r := newStreamChunkRewriter("client")
		prefix := []byte("\r\n\r\n")
		if chunks, err := r.Write(prefix); err != nil || len(chunks) != 0 {
			t.Fatalf("whitespace Write=(%q,%v)", chunks, err)
		}
		prefix[0] = 'x'
		body := []byte(tc.input)
		chunks, err := r.Write(body)
		if got, want := string(bytes.Join(chunks, nil)), "\r\n\r\n"+tc.want; err != nil || got != want {
			t.Fatalf("immediate output=%q error=%v, want %q", got, err, want)
		}
		body[0] = 'x'
		if got := string(bytes.Join(chunks, nil)); got != "\r\n\r\n"+tc.want {
			t.Fatalf("emitted bytes alias input: %q", got)
		}
	}
}

func TestRawNeutralWhitespaceForwarder(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	t.Cleanup(func() {
		shutdownExecutorStreams()
		resetExecutorStreamLifecycle()
		setLoadedConfigForTest(defaultConfig())
	})
	const object = `{"model":"upstream","modelVersion":"upstream","response":{"model":"upstream","modelVersion":"upstream"},"message":{"model":"upstream"},"interaction":{"model":"upstream"},"opaque":{"model":"upstream"},"n":1.00}`
	const restored = `{"model":"client","modelVersion":"client","response":{"model":"client","modelVersion":"client"},"message":{"model":"client"},"interaction":{"model":"client"},"opaque":{"model":"upstream"},"n":1.00}`
	for _, format := range []string{"openai", "openai-response", "claude", "gemini", "interactions"} {
		for _, contentType := range []string{"application/json", "text/event-stream"} {
			if contentType == "text/event-stream" && format != "openai" {
				continue
			}
			for _, witness := range []struct{ input, want string }{
				{"\r\n\r\n" + object, "\r\n\r\n" + restored},
				{"{}\n\n1.5 " + object, "{}\n\n1.5 " + restored},
			} {
				for i, parts := range classificationPartitions([]byte(witness.input)) {
					t.Run(fmt.Sprintf("%s/%s/bytes=%d/partition=%d", format, contentType, len(witness.input), i), func(t *testing.T) {
						reads := make([]pluginapi.HostModelStreamReadResponse, 0, len(parts)+1)
						for _, part := range parts {
							reads = append(reads, pluginapi.HostModelStreamReadResponse{Payload: part})
						}
						reads = append(reads, pluginapi.HostModelStreamReadResponse{Done: true})
						req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "client", Format: format, SourceFormat: format, Stream: true, OriginalRequest: []byte(`{"model":"client","stream":true}`)}, StreamID: "neutral-raw"}
						var forwarded pluginapi.HostModelExecutionRequest
						emitted, hostClosed, pluginClosed, setupRaw, err := runExecutorStreamTestWithHostContentTypeAndForwarded(req, reads, contentType, &forwarded)
						var setup pluginapi.ExecutorStreamResponse
						if err := json.Unmarshal(setupRaw, &setup); err != nil {
							t.Fatal(err)
						}
						if got := strings.Join(emitted, ""); err != nil || got != witness.want || !hostClosed || !pluginClosed || setup.Headers.Get("Content-Type") != contentType || forwarded.Model != "upstream" || string(forwarded.Body) != `{"model":"upstream","stream":true}` {
							t.Fatalf("output=%q error=%v close=%v/%v headers=%v forwarded=%+v, want %q", got, err, hostClosed, pluginClosed, setup.Headers, forwarded, witness.want)
						}
					})
				}
			}
		}
	}
}

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
