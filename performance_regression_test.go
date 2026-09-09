package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unsafe"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func BenchmarkCallerPatternCacheRetention(b *testing.B) {
	rules, err := parseRules("sk-*#client=>target")
	if err != nil {
		b.Fatal(err)
	}
	rule := &rules[0]
	for _, count := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("N=%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				resetCallerPatternCache()
				for j := 0; j < count; j++ {
					key := fmt.Sprintf("sk-%d", j)
					matched, authenticated := callerPatternMatch(rule, callerScope(key), key)
					if !matched || !authenticated {
						b.Fatalf("match=(%v,%v), want (true,true)", matched, authenticated)
					}
				}
				callerPatternCacheMu.RLock()
				retained := len(callerPatternCache.current) + len(callerPatternCache.previous)
				callerPatternCacheMu.RUnlock()
				b.ReportMetric(float64(retained), "retained-entries")
			}
		})
	}
}

func BenchmarkCallerPatternCacheWarmParallel(b *testing.B) {
	rules, err := parseRules("sk-*#client=>target")
	if err != nil {
		b.Fatal(err)
	}
	rule := &rules[0]
	b.Run("hot-key", func(b *testing.B) {
		key := "sk-hot"
		scope := callerScope(key)
		resetCallerPatternCache()
		matched, authenticated := callerPatternMatch(rule, scope, key)
		if !matched || !authenticated {
			b.Fatalf("warm match=(%v,%v), want (true,true)", matched, authenticated)
		}
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				matched, authenticated := callerPatternMatch(rule, scope, key)
				if !matched || !authenticated {
					b.Fatalf("match=(%v,%v), want (true,true)", matched, authenticated)
				}
			}
		})
	})

	keys := make([]string, 1024)
	scopes := make([]string, len(keys))
	for i := range keys {
		keys[i] = fmt.Sprintf("sk-%d", i)
		scopes[i] = callerScope(keys[i])
	}
	b.Run("working-set-1024", func(b *testing.B) {
		resetCallerPatternCache()
		for i, key := range keys {
			matched, authenticated := callerPatternMatch(rule, scopes[i], key)
			if !matched || !authenticated {
				b.Fatalf("warm match=(%v,%v), want (true,true)", matched, authenticated)
			}
		}
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			index := 0
			for pb.Next() {
				matched, authenticated := callerPatternMatch(rule, scopes[index], keys[index])
				if !matched || !authenticated {
					b.Fatalf("match=(%v,%v), want (true,true)", matched, authenticated)
				}
				index = (index + 1) & (len(keys) - 1)
			}
		})
	})
}

var (
	benchmarkRewriteTopLevelModelOutput    []byte
	benchmarkRewriteTopLevelModelChanged   bool
	benchmarkResponseModelMarkerScanResult bool
	benchmarkStreamOutputBytes             int
	benchmarkEmitRewrittenOutputBytes      int
)

func BenchmarkResponseModelMarkerScan(b *testing.B) {
	for _, size := range []struct {
		name  string
		bytes int
	}{
		{name: "4KiB", bytes: 4 << 10},
		{name: "64KiB", bytes: 64 << 10},
		{name: "1MiB", bytes: 1 << 20},
	} {
		for _, payload := range []struct {
			name    string
			marker  bool
			changed bool
		}{
			{name: "no-marker", marker: false, changed: false},
			{name: "literal-marker-near-end", marker: true, changed: true},
			{name: "escaped-key-near-end", marker: true, changed: true},
		} {
			b.Run(size.name+"/"+payload.name, func(b *testing.B) {
				body := responseModelMarkerScanBenchmarkFixture(size.bytes, payload.name)
				if got := mightContainResponseModelField(body); got != payload.marker {
					b.Fatalf("mightContainResponseModelField=%v, want %v", got, payload.marker)
				}
				restored, changed, err := restoreResponseModel(body, "client")
				if err != nil {
					b.Fatal(err)
				}
				if changed != payload.changed {
					b.Fatalf("restoreResponseModel changed=%v, want %v", changed, payload.changed)
				}
				if !changed && !bytes.Equal(restored, body) {
					b.Fatalf("restoreResponseModel=%q, want %q", restored, body)
				}
				if changed && !bytes.Contains(restored, []byte(`"model":"client"`)) {
					b.Fatalf("restoreResponseModel=%q, want restored model", restored)
				}

				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchmarkResponseModelMarkerScanResult = mightContainResponseModelField(body)
				}
			})
		}
	}
}

func responseModelMarkerScanBenchmarkFixture(size int, payload string) []byte {
	prefix := []byte(`{"opaque":"line\nquoted:\"value\"\\`)
	var suffix []byte
	switch payload {
	case "no-marker":
		suffix = []byte(`","id":"response"}`)
	case "literal-marker-near-end":
		suffix = []byte(`","model":"upstream"}`)
	case "escaped-key-near-end":
		suffix = append([]byte{'"', ',', '"', '\\'}, []byte(`u006dodel":"upstream"}`)...)
	}
	body := make([]byte, 0, size)
	body = append(body, prefix...)
	body = append(body, bytes.Repeat([]byte("x"), size-len(prefix)-len(suffix))...)
	return append(body, suffix...)
}

func BenchmarkRewriteTopLevelModel(b *testing.B) {
	for _, benchmark := range []struct {
		name string
		size int
	}{
		{name: "1KiB", size: 1 << 10},
		{name: "64KiB", size: 64 << 10},
		{name: "1MiB", size: 1 << 20},
		{name: "8MiB", size: 8 << 20},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			body := rewriteTopLevelModelBenchmarkFixture(benchmark.size)
			out, changed, err := rewriteTopLevelModel(body, "client")
			if err != nil {
				b.Fatal(err)
			}
			if !changed {
				b.Fatal("changed=false, want true")
			}
			var decoded struct {
				Model  string `json:"model"`
				Opaque []struct {
					Model string `json:"model"`
				} `json:"opaque"`
			}
			if err := json.Unmarshal(out, &decoded); err != nil {
				b.Fatalf("decode output: %v", err)
			}
			if decoded.Model != "client" || len(decoded.Opaque) != 1 || decoded.Opaque[0].Model != "opaque" {
				b.Fatalf("decoded=%#v, want top-level client and opaque nested model", decoded)
			}
			benchmarkRewriteTopLevelModelOutput = out
			benchmarkRewriteTopLevelModelChanged = changed

			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, changed, err = rewriteTopLevelModel(body, "client")
				if err != nil {
					b.Fatal(err)
				}
				benchmarkRewriteTopLevelModelOutput = out
				benchmarkRewriteTopLevelModelChanged = changed
			}
		})
	}
}

type restoreResponseBenchmarkFixture struct {
	name              string
	body              []byte
	changed           bool
	unchanged         bool
	wantModel         string
	wantResponseModel string
	geminiArray       bool
}

func BenchmarkRestoreResponseModel(b *testing.B) {
	for _, size := range []int{4 << 10, 64 << 10, 1 << 20, 8 << 20} {
		for _, fixture := range restoreResponseBenchmarkFixtures(size) {
			b.Run(fmt.Sprintf("%d/%s", size, fixture.name), func(b *testing.B) {
				out, changed, err := restoreResponseModel(fixture.body, "client")
				if err != nil || changed != fixture.changed {
					b.Fatalf("preflight restore=(%d,%v,%v), want changed=%v", len(out), changed, err, fixture.changed)
				}
				fixture.assertRestored(b, out)

				b.SetBytes(int64(len(fixture.body)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchmarkRewriteTopLevelModelOutput, benchmarkRewriteTopLevelModelChanged, err = restoreResponseModel(fixture.body, "client")
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func restoreResponseBenchmarkFixtures(size int) []restoreResponseBenchmarkFixture {
	return []restoreResponseBenchmarkFixture{
		{
			name:      "no-marker",
			body:      restoreResponseBenchmarkFixtureBody(size, `{"opaque":"`, `","id":"response"}`),
			unchanged: true,
		},
		{
			name:      "same-model",
			body:      restoreResponseBenchmarkFixtureBody(size, `{"model":"client","opaque":"`, `","id":"response"}`),
			unchanged: true,
			wantModel: "client",
		},
		{
			name:      "top-level-changed",
			body:      restoreResponseBenchmarkFixtureBody(size, `{"model":"upstream","opaque":"`, `","id":"response"}`),
			changed:   true,
			wantModel: "client",
		},
		{
			name:              "nested-response-changed",
			body:              restoreResponseBenchmarkFixtureBody(size, `{"response":{"model":"upstream"},"opaque":"`, `","id":"response"}`),
			changed:           true,
			wantResponseModel: "client",
		},
		// This preflight would fail against the pre-Task-2 array behavior, which did not restore array elements.
		{
			name:        "gemini-array-changed",
			body:        restoreResponseBenchmarkFixtureBody(size, `[{"modelVersion":"upstream","opaque":{"model":"opaque-model","modelVersion":"opaque-version"},"role":{"model":"role-model"},"payload":"`, `"}]`),
			changed:     true,
			geminiArray: true,
		},
	}
}

func restoreResponseBenchmarkFixtureBody(size int, prefix, suffix string) []byte {
	body := make([]byte, 0, size)
	body = append(body, prefix...)
	body = append(body, bytes.Repeat([]byte("x"), size-len(prefix)-len(suffix))...)
	return append(body, suffix...)
}

func (fixture restoreResponseBenchmarkFixture) assertRestored(b *testing.B, out []byte) {
	if fixture.geminiArray {
		var restored []struct {
			ModelVersion string `json:"modelVersion"`
			Opaque       struct {
				Model        string `json:"model"`
				ModelVersion string `json:"modelVersion"`
			} `json:"opaque"`
			Role struct {
				Model string `json:"model"`
			} `json:"role"`
		}
		if err := json.Unmarshal(out, &restored); err != nil {
			b.Fatalf("preflight decode %q: %v", fixture.name, err)
		}
		if len(restored) != 1 || restored[0].ModelVersion != "client" || restored[0].Opaque.Model != "opaque-model" || restored[0].Opaque.ModelVersion != "opaque-version" || restored[0].Role.Model != "role-model" {
			b.Fatalf("preflight gemini array=%#v, want restored immediate modelVersion and preserved opaque and role models", restored)
		}
		return
	}
	if fixture.unchanged && !bytes.Equal(out, fixture.body) {
		b.Fatalf("preflight restore changed unchanged fixture %q", fixture.name)
	}
	var restored struct {
		Model    string `json:"model"`
		Response struct {
			Model string `json:"model"`
		} `json:"response"`
	}
	if err := json.Unmarshal(out, &restored); err != nil {
		b.Fatalf("preflight decode %q: %v", fixture.name, err)
	}
	if fixture.wantModel != "" && restored.Model != fixture.wantModel {
		b.Fatalf("preflight model=%q, want %q", restored.Model, fixture.wantModel)
	}
	if fixture.wantResponseModel != "" && restored.Response.Model != fixture.wantResponseModel {
		b.Fatalf("preflight response.model=%q, want %q", restored.Response.Model, fixture.wantResponseModel)
	}
}

func rewriteTopLevelModelBenchmarkFixture(size int) []byte {
	prefix := []byte(`{"model":"upstream","opaque":[{"model":"opaque","payload":"`)
	suffix := []byte(`"}]}`)
	body := make([]byte, 0, size)
	body = append(body, prefix...)
	body = append(body, bytes.Repeat([]byte("x"), size-len(prefix)-len(suffix))...)
	return append(body, suffix...)
}

func TestRewriteTopLevelModelPreservesValidatedOpaqueBytes(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		changed bool
	}{
		{
			name: "normal whitespace and nested tool content",
			body: []byte(`{
  "model" : "upstream",
  "opaque" : {"model":"opaque","tool":{"model":"tool-opaque"},"content":[{"model":"content-opaque"}]}
}`),
			changed: true,
		},
		{
			name:    "escaped top-level key",
			body:    []byte(fmt.Sprintf(`{"%cu006dodel":"upstream","opaque":{"model":"opaque","tool":{"model":"tool-opaque"},"content":[{"model":"content-opaque"}]}}`, 92)),
			changed: true,
		},
		{
			name:    "escaped current model string",
			body:    []byte(fmt.Sprintf(`{"model":"up%cu0073tream","opaque":{"model":"opaque","tool":{"model":"tool-opaque"},"content":[{"model":"content-opaque"}]}}`, 92)),
			changed: true,
		},
		{
			name:    "last duplicate model wins",
			body:    []byte(`{"model":"earlier","opaque":{"model":"opaque","tool":{"model":"tool-opaque"},"content":[{"model":"content-opaque"}]},"model":"upstream"}`),
			changed: true,
		},
		{
			name:    "last model is non-string",
			body:    []byte(`{"model":"upstream","opaque":{"model":"opaque","tool":{"model":"tool-opaque"},"content":[{"model":"content-opaque"}]},"model":123}`),
			changed: false,
		},
		{
			name:    "missing model",
			body:    []byte(`{"opaque":{"model":"opaque","tool":{"model":"tool-opaque"},"content":[{"model":"content-opaque"}]}}`),
			changed: false,
		},
		{
			name:    "invalid JSON after valid prefix",
			body:    []byte(`{"model":"upstream","opaque":{"model":"opaque","tool":{"model":"tool-opaque"},"content":[{"model":"content-opaque"}]},"broken":`),
			changed: false,
		},
		{
			name:    "requested model already equal",
			body:    []byte(fmt.Sprintf(`{"model":"cl%cu0069ent","opaque":{"model":"opaque","tool":{"model":"tool-opaque"},"content":[{"model":"content-opaque"}]}}`, 92)),
			changed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := rewriteTopLevelModel(tt.body, "client")
			if err != nil {
				t.Fatalf("rewriteTopLevelModel error = %v", err)
			}
			if changed != tt.changed {
				t.Fatalf("changed=%v, want %v", changed, tt.changed)
			}
			if !tt.changed {
				if !bytes.Equal(got, tt.body) {
					t.Fatalf("body=%q, want byte-identical clone %q", got, tt.body)
				}
				return
			}

			var decoded struct {
				Model  string `json:"model"`
				Opaque struct {
					Model string `json:"model"`
					Tool  struct {
						Model string `json:"model"`
					} `json:"tool"`
					Content []struct {
						Model string `json:"model"`
					} `json:"content"`
				} `json:"opaque"`
			}
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatalf("decode output: %v", err)
			}
			if decoded.Model != "client" || decoded.Opaque.Model != "opaque" || decoded.Opaque.Tool.Model != "tool-opaque" || len(decoded.Opaque.Content) != 1 || decoded.Opaque.Content[0].Model != "content-opaque" {
				t.Fatalf("rewritten document=%s, opaque values changed", got)
			}
		})
	}
}

func TestRewriteTopLevelModelLeavesNullModelUnchanged(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "top-level model null",
			body: []byte(`{"model":null,"opaque":{"model":"opaque"}}`),
		},
		{
			name: "last duplicate model null",
			body: []byte(`{"model":"upstream","opaque":{"model":"opaque"},"model":null}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := rewriteTopLevelModel(tt.body, "client")
			if err != nil {
				t.Fatalf("rewriteTopLevelModel error = %v", err)
			}
			if changed {
				t.Fatal("changed=true, want false")
			}
			if !bytes.Equal(got, tt.body) {
				t.Fatalf("body=%q, want byte-identical clone %q", got, tt.body)
			}
			if len(got) > 0 && &got[0] == &tt.body[0] {
				t.Fatal("body aliases input, want clone")
			}
		})
	}
}

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

func BenchmarkEmitRewrittenBatch(b *testing.B) {
	for _, totalSize := range []int{64 << 10, 1 << 20} {
		for _, chunkCount := range []int{1, 2, 32, 128} {
			b.Run(fmt.Sprintf("%d/chunks=%d", totalSize, chunkCount), func(b *testing.B) {
				chunks := emitRewrittenBatchBenchmarkFixture(totalSize, chunkCount)
				want := bytes.Join(chunks, nil)
				calls := 0
				var emitted []byte
				if err := emitRewritten(chunks, true, func(p []byte) error {
					calls++
					emitted = append(emitted[:0], p...)
					return nil
				}); err != nil {
					b.Fatal(err)
				}
				if calls != 1 || !bytes.Equal(emitted, want) {
					b.Fatalf("preflight calls=%d emitted=%d, want one %d-byte ordered batch", calls, len(emitted), len(want))
				}

				emit := func(p []byte) error {
					benchmarkEmitRewrittenOutputBytes = len(p)
					return nil
				}
				b.SetBytes(int64(totalSize))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if err := emitRewritten(chunks, true, emit); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func emitRewrittenBatchBenchmarkFixture(totalSize, chunkCount int) [][]byte {
	chunks := make([][]byte, chunkCount)
	for i := range chunks {
		size := totalSize / chunkCount
		if i < totalSize%chunkCount {
			size++
		}
		chunks[i] = bytes.Repeat([]byte("x"), size)
	}
	return chunks
}

func BenchmarkStreamChunkRewriterFragmentedRawJSON(b *testing.B) {
	opaqueID := string(bytes.Repeat([]byte("x"), 64<<10))
	payload := append([]byte(`{"model":"upstream","id":"`), opaqueID...)
	payload = append(payload, `"}`...)
	const fragments = 32
	chunkSize := (len(payload) + fragments - 1) / fragments
	chunks := make([][]byte, 0, fragments)
	for start := 0; start < len(payload); start += chunkSize {
		end := start + chunkSize
		if end > len(payload) {
			end = len(payload)
		}
		chunks = append(chunks, payload[start:end])
	}

	r := newStreamChunkRewriter("client")
	var output []byte
	for _, chunk := range chunks {
		out, err := r.Write(chunk)
		if err != nil {
			b.Fatal(err)
		}
		output = append(output, bytes.Join(out, nil)...)
	}
	out, err := r.Flush()
	if err != nil {
		b.Fatal(err)
	}
	output = append(output, bytes.Join(out, nil)...)
	var restored struct {
		Model string `json:"model"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(output, &restored); err != nil {
		b.Fatalf("preflight output is not JSON: %v", err)
	}
	if restored.Model != "client" || restored.ID != opaqueID {
		b.Fatalf("preflight restored=%#v, want client model and intact ID", restored)
	}

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := newStreamChunkRewriter("client")
		outputBytes := 0
		for _, chunk := range chunks {
			out, err := r.Write(chunk)
			if err != nil {
				b.Fatal(err)
			}
			for _, p := range out {
				outputBytes += len(p)
			}
		}
		out, err := r.Flush()
		if err != nil {
			b.Fatal(err)
		}
		for _, p := range out {
			outputBytes += len(p)
		}
		benchmarkStreamOutputBytes = outputBytes
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
	stream, _, err := prepareExecutorStream(req, call)
	if err != nil {
		t.Fatalf("prepareExecutorStream error = %v", err)
	}
	if req.OriginalRequest != nil || req.Headers != nil || req.Query != nil || req.Metadata != nil {
		t.Fatalf("setup fields retained after prepare: %#v", req)
	}
	if err := runStreamForward(stream); err != nil {
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
	r := newStreamChunkRewriter("client")
	r.frameRawJSONAsSSE = true
	chunks, err := r.Write(payload)
	if err != nil || len(chunks) != 1 || !bytes.Equal(chunks[0], payload) {
		b.Fatalf("preflight Write=(%q,%v), want unchanged batch", chunks, err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
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
