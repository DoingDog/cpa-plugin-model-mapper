package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unsafe"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestMightContainResponseModelFieldIgnoresEscapedTextMarker(t *testing.T) {
	body := append([]byte(`{"text":"`), bytes.Repeat([]byte{0x5c, 'u', '0', '0', '6', '1'}, 4096)...)
	body = append(body, []byte(`"}`)...)
	if mightContainResponseModelField(body) {
		t.Fatal("ordinary escaped text was classified as a response model field")
	}
}

func TestSSERewriterLargeDelimiterDoesNotRetainBackingArray(t *testing.T) {
	input := []byte("event: " + strings.Repeat("x", 2<<20) + "\n\n")
	backing := bytes.Clone(input)
	start := uintptr(unsafe.Pointer(unsafe.SliceData(backing)))
	end := start + uintptr(cap(backing))
	r := newSSERewriter("client")
	r.buf = backing
	chunks, err := r.Write(nil)
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	for _, chunk := range chunks {
		if !bytes.Equal(chunk, []byte("\n\n")) {
			continue
		}
		ptr := uintptr(unsafe.Pointer(unsafe.SliceData(chunk)))
		if ptr >= start && ptr < end {
			t.Fatal("large event delimiter still aliases the input backing array")
		}
		return
	}
	t.Fatalf("chunks=%q, missing event delimiter", chunks)
}

func TestEmitRewrittenBatchesSSEChunks(t *testing.T) {
	calls := 0
	var emitted []byte
	err := emitRewritten([][]byte{[]byte("data: one\n\n"), []byte("data: two\n\n")}, true, func(p []byte) error {
		calls++
		emitted = append(emitted, p...)
		return nil
	})
	if err != nil {
		t.Fatalf("emitRewritten error = %v", err)
	}
	if calls != 1 || string(emitted) != "data: one\n\ndata: two\n\n" {
		t.Fatalf("calls=%d emitted=%q, want one ordered batch", calls, emitted)
	}
}

func TestReleaseExecutorStreamSetupClearsLargeFields(t *testing.T) {
	req := &executorRPCRequest{
		OriginalRequest: []byte("request"),
		Headers:         map[string][]string{"X-Test": {"value"}},
		Query:           map[string][]string{"q": {"value"}},
		Metadata:        map[string]any{"key": "value"},
	}
	releaseExecutorStreamSetup(req)
	if req.OriginalRequest != nil || req.Headers != nil || req.Query != nil || req.Metadata != nil {
		t.Fatalf("request setup fields were not released: %#v", req)
	}
}

func TestRunStreamForwardReleasesSetupBeforeRead(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	req := &executorRPCRequest{
		Model:           "client",
		Format:          "openai",
		SourceFormat:    "openai",
		OriginalRequest: []byte(`{"model":"client"}`),
		Headers:         http.Header{"X-Test": {"value"}},
		Metadata:        map[string]any{"key": "value"},
		StreamID:        "plugin-stream",
	}
	call := func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostModelExecuteStream:
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, StreamID: "host-stream"})
		case pluginabi.MethodHostModelStreamRead:
			if req.OriginalRequest != nil || req.Headers != nil || req.Query != nil || req.Metadata != nil {
				t.Fatalf("setup fields retained when read loop started: %#v", req)
			}
			return json.Marshal(pluginapi.HostModelStreamReadResponse{Done: true})
		case pluginabi.MethodHostModelStreamClose, pluginabi.MethodHostStreamClose:
			return json.Marshal(map[string]any{})
		default:
			t.Fatalf("unexpected host method %q", method)
			return nil, nil
		}
	}
	if err := runStreamForward(req, call); err != nil {
		t.Fatalf("runStreamForward error = %v", err)
	}
}

func TestStreamChunkRewriterFastPathsCompleteSSEBatchWithoutModelMarker(t *testing.T) {
	payload := bytes.Repeat([]byte("data:x\n\n"), 8192)
	allocs := testing.AllocsPerRun(20, func() {
		r := newStreamChunkRewriter("client")
		r.frameRawJSONAsSSE = true
		chunks, err := r.Write(payload)
		if err != nil || len(chunks) != 1 || !bytes.Equal(chunks[0], payload) {
			panic("complete SSE batch was not preserved as one owned chunk")
		}
	})
	if allocs > 6 {
		t.Fatalf("complete SSE batch allocations=%v, want <=6", allocs)
	}
}

func BenchmarkStreamChunkRewriterCompleteSSEBatch(b *testing.B) {
	payload := bytes.Repeat([]byte("data:x\n\n"), 8192)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		r := newStreamChunkRewriter("client")
		r.frameRawJSONAsSSE = true
		chunks, err := r.Write(payload)
		if err != nil || len(chunks) != 1 {
			b.Fatalf("Write=(%d,%v), want one batch", len(chunks), err)
		}
	}
}

func TestRunStreamForwardBatchesOnlySSEOutput(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
		Model: "client", Format: "openai", SourceFormat: "openai",
		OriginalRequest: []byte(`{"model":"client"}`), Stream: true,
	}, StreamID: "plugin-stream"}

	sseReads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte("data: one\n\ndata: two\n\n")},
		{Done: true},
	}
	emitted, _, _, _, err := runExecutorStreamTestWithHostContentType(req, sseReads, "text/event-stream; charset=utf-8")
	if err != nil {
		t.Fatalf("SSE stream error = %v", err)
	}
	if len(emitted) != 1 || emitted[0] != "data: one\n\ndata: two\n\n" {
		t.Fatalf("SSE emitted=%q, want one ordered batch", emitted)
	}

	rawReads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte(`{"type":"one"}`)},
		{Payload: []byte(`{"type":"two"}`)},
		{Done: true},
	}
	emitted, _, _, _, err = runExecutorStreamTestWithHostContentType(req, rawReads, "application/json")
	if err != nil {
		t.Fatalf("raw stream error = %v", err)
	}
	if len(emitted) != 2 {
		t.Fatalf("raw emitted=%q, want original per-read boundaries", emitted)
	}
}

func TestRunStreamForwardDoesNotFrameProfileParameter(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
		Model: "client", Format: "openai", SourceFormat: "openai",
		OriginalRequest: []byte(`{"model":"client"}`), Stream: true,
	}, StreamID: "plugin-stream"}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte(`{"model":"upstream"}`)},
		{Done: true},
	}
	emitted, _, _, _, err := runExecutorStreamTestWithHostContentType(req, reads, `application/json; profile="text/event-stream"`)
	if err != nil {
		t.Fatalf("stream error = %v", err)
	}
	if len(emitted) != 1 || strings.HasPrefix(emitted[0], "data: ") || !strings.Contains(emitted[0], `"model":"client"`) {
		t.Fatalf("emitted=%q, want unframed restored JSON", emitted)
	}
}
