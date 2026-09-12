# Stream Safety and Error Propagation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 delimiterless OpenAI Responses 事件拼接、单 stream incomplete state 无界增长和 host HTTP error metadata 丢失，并发布 v0.5.6。

**Architecture:** 在现有 `streamChunkRewriter`/`sseRewriter` 内按 JSON decoder 消费位置恢复 Kimi Responses 的逻辑事件边界，不依赖 `host.model.stream_read` 调用边界；同一层限制每个 stream 当前未完成单元为 16 MiB。使用内部 typed error 将 host `pluginabi.Error` 或 status-only execution error 保留到 `wrapEnvelope`，不改变公开 ABI。

**Tech Stack:** Go 1.26.0、Go stdlib `encoding/json`/`errors`/`net/http`、CLIProxyAPI v7.2.152 plugin ABI、现有 Go test suite、Git/GitHub Actions。

**Spec:** `docs/superpowers/specs/2026-09-12-stream-safety-and-error-propagation-design.md`

## Global Constraints

- 只修改本插件仓库，不修改或 vendoring CPA 源码。
- 保持 `github.com/router-for-me/CLIProxyAPI/v7@v7.2.152`，不增加 dependency。
- delimiterless boundary recovery 只对 `format == "openai-response"` 生效，并依据完整 Responses `event:` + JSON `data:` 内容，不依据 host read 边界。
- 标准 SSE 的 LF、CRLF、CR delimiter 和字段顺序保持 byte-compatible。
- `maxPendingStreamBytes` 固定为 `16 << 20`，只限制当前 retained incomplete unit，不限制累计 stream bytes 或包含多个完整单元的单次 `Write`。
- error envelope 只传播已有字段；status-only error 不编造 upstream code、retryability 或 response headers。
- 不实现 `executor.count_tokens` workaround，不实现 route/reconfigure queue/cache workaround。
- 不读取或修改其他旧 spec、plan 或 verification 文档。
- 每项 production change 前必须先运行 focused failing test，确认 RED 后再实现。

---

### Task 1: 建立分支并保存本次设计基线

**Files:**
- Add: `docs/superpowers/specs/2026-09-12-stream-safety-and-error-propagation-design.md`
- Add: `docs/superpowers/plans/2026-09-12-stream-safety-and-error-propagation-implementation.md`

**Interfaces:**
- Consumes: 当前 `main` at `334a7a8170637ffed5e0566213544788bd8a816d`，tag `v0.5.5`。
- Produces: feature branch `fix/v0.5.6-stream-safety` 和可独立审查的设计 commit。

- [ ] **Step 1: 确认只存在初始 `.claude/` 与本次两份文档**

Run:

```powershell
git status --short --branch
git diff --check
```

Expected: branch 仍为 `main`；untracked 包含初始 `.claude/` 与本次 `docs/superpowers/...` 文件；无 production diff，`git diff --check` exit 0。

- [ ] **Step 2: 创建 feature branch**

Run:

```powershell
git switch -c fix/v0.5.6-stream-safety
```

Expected: 新 branch 基于 `334a7a8`，现有 untracked 文档随 working tree 保留。

- [ ] **Step 3: 提交本次 spec 与 plan**

Run:

```powershell
git add docs/superpowers/specs/2026-09-12-stream-safety-and-error-propagation-design.md docs/superpowers/plans/2026-09-12-stream-safety-and-error-propagation-implementation.md
git commit -m "docs: specify v0.5.6 stream safety fixes"
```

Expected: commit 成功；`.claude/` 仍未跟踪且未加入 commit。

---

### Task 2: 以内容边界恢复 delimiterless Responses SSE events

**Files:**
- Modify: `main.go:30-187`，`main.go:461-557`
- Test: `main_test.go:4278-4451`，`main_test.go:5850-6057`，`main_test.go:6387-6444`

**Interfaces:**
- Consumes: `sseRewriter.Write([]byte) ([][]byte, error)`、`sseRewriter.drain(bool) ([][]byte, error)`、`streamChunkRewriter.format`、`rewriteEvent`。
- Produces: `sseRewriter.recoverResponsesEvents bool` 和 `findDelimiterlessResponsesEventEnd([]byte, bool) (int, bool)`；返回值中的 `int` 是 event 结束位置，`bool` 表示是否确认安全边界。

- [ ] **Step 1: 添加精确 Kimi 两 payload 的 RED test**

在 `main_test.go` 的 stream rewriter tests 旁加入 helper 和 test。helper 只验证本测试生成的 canonical LF SSE：

```go
func requireValidResponsesSSE(t *testing.T, stream []byte, wantEvents int) {
	t.Helper()
	frames := bytes.Split(bytes.TrimSuffix(stream, []byte("\n\n")), []byte("\n\n"))
	if len(frames) != wantEvents {
		t.Fatalf("SSE frames=%d, want %d: %q", len(frames), wantEvents, stream)
	}
	for _, frame := range frames {
		var data [][]byte
		for _, line := range bytes.Split(frame, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data:")) {
				data = append(data, bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" ")))
			}
		}
		payload := bytes.Join(data, []byte("\n"))
		if !json.Valid(payload) {
			t.Fatalf("invalid SSE data JSON %q in frame %q", payload, frame)
		}
	}
}

func TestStreamChunkRewriterSeparatesDelimiterlessKimiResponsesEvents(t *testing.T) {
	parts := [][]byte{
		[]byte("event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":1,\"response\":{\"status\":\"in_progress\",\"model\":\"kimi-k3\"}}"),
		[]byte("event: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"sequence_number\":2,\"response\":{\"status\":\"in_progress\",\"model\":\"kimi-k3\"}}"),
	}
	r := newStreamChunkRewriter("claude-opus-5")
	r.format = "openai-response"
	r.frameRawJSONAsSSE = true
	var chunks [][]byte
	for _, part := range parts {
		written, err := r.Write(part)
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, written...)
	}
	finished, err := r.Finish()
	if err != nil {
		t.Fatal(err)
	}
	got := bytes.Join(append(chunks, finished...), nil)
	requireValidResponsesSSE(t, got, 2)
	if bytes.Contains(got, []byte("kimi-k3")) || bytes.Count(got, []byte(`"model":"claude-opus-5"`)) != 2 || bytes.Contains(got, []byte("}event:")) {
		t.Fatalf("rewritten stream=%q", got)
	}
}
```

Run:

```powershell
go test . -run '^TestStreamChunkRewriterSeparatesDelimiterlessKimiResponsesEvents$' -count=1 -v
```

Expected: FAIL because current output contains `}event:` and only one invalid frame。

- [ ] **Step 2: 添加 segmentation invariance 和标准 SSE guard tests，保持 RED**

添加：

```go
func rewriteResponsesParts(t *testing.T, parts ...[]byte) []byte {
	t.Helper()
	r := newStreamChunkRewriter("claude-opus-5")
	r.format = "openai-response"
	r.frameRawJSONAsSSE = true
	var chunks [][]byte
	for _, part := range parts {
		written, err := r.Write(part)
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, written...)
	}
	finished, err := r.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Join(append(chunks, finished...), nil)
}

func TestStreamChunkRewriterDelimiterlessResponsesPartitionInvariant(t *testing.T) {
	input := []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"kimi-k3\"}}event: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"response\":{\"model\":\"kimi-k3\"}}")
	want := rewriteResponsesParts(t, input)
	requireValidResponsesSSE(t, want, 2)
	for split := 0; split <= len(input); split++ {
		got := rewriteResponsesParts(t, input[:split], input[split:])
		if !bytes.Equal(got, want) {
			t.Fatalf("split=%d output=%q, want %q", split, got, want)
		}
	}
	parts := make([][]byte, len(input))
	for i := range input {
		parts[i] = input[i : i+1]
	}
	if got := rewriteResponsesParts(t, parts...); !bytes.Equal(got, want) {
		t.Fatalf("one-byte output=%q, want %q", got, want)
	}
}

func TestStreamChunkRewriterDoesNotEndOrdinarySSEAtReadBoundary(t *testing.T) {
	r := newStreamChunkRewriter("client")
	r.format = "openai-response"
	r.frameRawJSONAsSSE = true
	first := []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"upstream\"}}")
	if out, err := r.Write(first); err != nil || len(out) != 0 {
		t.Fatalf("first Write=(%q,%v), want buffered", out, err)
	}
	second := []byte("\nid: event-1\n\n")
	out, err := r.Write(second)
	if err != nil {
		t.Fatal(err)
	}
	got := bytes.Join(out, nil)
	if !bytes.Contains(got, []byte("id: event-1")) || !bytes.Contains(got, []byte(`"model":"client"`)) {
		t.Fatalf("output=%q", got)
	}
}
```

Run:

```powershell
go test . -run 'TestStreamChunkRewriter(DelimiterlessResponsesPartitionInvariant|DoesNotEndOrdinarySSEAtReadBoundary)' -count=1 -v
```

Expected: partition test FAIL；ordinary SSE guard 继续 PASS。

- [ ] **Step 3: 实现最小 content-based boundary detector**

在 `sseRewriter` 增加：

```go
recoverResponsesEvents bool
```

在 `streamChunkRewriter.Write` 设置：

```go
r.sse.recoverResponsesEvents = r.format == "openai-response"
```

在 SSE helpers 旁实现 detector。实现必须：解析首行 `event:`；查找下一行 `data:`；用 `json.Decoder.InputOffset()` 找 JSON value 结束；解码 top-level `type` 并要求它等于 `event:` value且以 `response.` 开头；只在 suffix 以完整 `event:` 开始或 `e`/`ev`/`eve`/`even`/`event`/`event:` 形式等待下一 prefix 时保留；仅在 `eof` 且 suffix 为空时结束最终 event。

```go
func findDelimiterlessResponsesEventEnd(buf []byte, eof bool) (int, bool) {
	lineEnd, lineBreakLen, _ := sseLineEnding(buf, 0, false)
	if lineBreakLen == 0 || !bytes.HasPrefix(buf[:lineEnd], []byte("event:")) {
		return 0, false
	}
	eventType := strings.TrimSpace(string(buf[len("event:"):lineEnd]))
	if !strings.HasPrefix(eventType, "response.") {
		return 0, false
	}
	dataStart := lineEnd + lineBreakLen
	dataLine := buf[dataStart:]
	if !bytes.HasPrefix(dataLine, []byte("data:")) {
		return 0, false
	}
	valueStart := dataStart + len("data:")
	if valueStart < len(buf) && buf[valueStart] == ' ' {
		valueStart++
	}
	dec := json.NewDecoder(bytes.NewReader(buf[valueStart:]))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return 0, false
	}
	var typed struct{ Type string `json:"type"` }
	if json.Unmarshal(raw, &typed) != nil || typed.Type != eventType {
		return 0, false
	}
	end := valueStart + int(dec.InputOffset())
	suffix := buf[end:]
	if len(suffix) == 0 {
		return end, eof
	}
	if bytes.HasPrefix(suffix, []byte("event:")) {
		return end, true
	}
	if bytes.HasPrefix([]byte("event:"), suffix) {
		return 0, false
	}
	return 0, false
}
```

将 `sseRewriter.drain` 的循环调整为同时计算标准 delimiter 和 synthetic Responses boundary，并选择较早者。synthetic branch 对 `buf[:logicalEnd]` 调用现有 `rewriteEvent`，随后 append `[]byte("\n\n")`，从 `logicalEnd` 继续；标准 delimiter branch 保持原实现。clean EOF 的完整 delimiterless Responses event 也走 synthetic branch。不要对普通 EOF buffer 合成 delimiter。

核心选择逻辑：

```go
logicalEnd, logical := 0, false
if r.recoverResponsesEvents {
	logicalEnd, logical = findDelimiterlessResponsesEventEnd(r.buf, eof)
}
standardEnd, standardLen, next := findSSEEventDelimiter(r.buf, r.scanFrom, eof)
if logical && (standardLen == 0 || logicalEnd < standardEnd) {
	// rewrite buf[:logicalEnd], consume logicalEnd, append canonical \n\n
	continue
}
// existing standard delimiter and eof fallback
```

若实现时发现标准 delimiter 的 `standardEnd` 与 JSON end 相等，优先标准 branch，以保留原始 LF/CRLF/CR delimiter。

- [ ] **Step 4: 运行 focused tests，确认 GREEN**

Run:

```powershell
go test . -run 'TestStreamChunkRewriter(SeparatesDelimiterlessKimiResponsesEvents|DelimiterlessResponsesPartitionInvariant|DoesNotEndOrdinarySSEAtReadBoundary|DoesNotTreatReadBoundaryAsLineEnding|PreservesFramedResponsesEventBytes)' -count=1 -v
```

Expected: PASS。

- [ ] **Step 5: 添加完整 9-event 和 forwarding integration regression**

构造 9 个 payload，顺序固定为：

```go
[]string{
	"response.created",
	"response.in_progress",
	"response.output_item.added",
	"response.content_part.added",
	"response.output_text.delta",
	"response.output_text.done",
	"response.content_part.done",
	"response.output_item.done",
	"response.completed",
}
```

每个 JSON 至少含匹配的 `type`；`response.created`、`response.in_progress`、`response.completed` 的 `response.model` 为 `kimi-k3`，其他 event 放置各自最小合法 `item`/`part`/`delta` 字段。用 `runExecutorStreamTest` 提供 9 个独立 `HostModelStreamReadResponse.Payload`，并让最后一个 read 同时 `Done: true`。断言：

```go
joined := []byte(strings.Join(emitted, ""))
requireValidResponsesSSE(t, joined, 9)
if bytes.Contains(joined, []byte("kimi-k3")) || bytes.Count(joined, []byte(`"model":"claude-opus-5"`)) != 3 {
	t.Fatalf("emitted=%q", joined)
}
```

再增加一个 Error-with-payload subtest，最后 payload 同时 `Error: "upstream failed"`，断言 payload 在 plugin close 前 emit、host/plugin stream 都关闭、close error 含 upstream failure。

Run:

```powershell
go test . -run '^TestRunStreamForwardSeparatesDelimiterlessKimiResponsesLifecycle$' -count=1 -v
```

Expected: PASS；该 integration test 在 production implementation 缺失时必须曾表现为 invalid frame 或错误 frame count。

- [ ] **Step 6: 运行 stream regression set 并提交**

Run:

```powershell
go test . -run 'SSE|StreamChunkRewriter|RunStreamForward|HandleExecutorExecuteStream' -count=1
git diff --check
git add main.go main_test.go
git commit -m "fix: preserve Responses stream event boundaries"
```

Expected: tests PASS，commit 只含 Task 2 改动。

---

### Task 3: 限制 incomplete stream retained state

**Files:**
- Modify: `main.go:28-49`，`main.go:126-187`，`main.go:461-640`
- Test: `main_test.go:4890-5350`，`main_test.go:6446-6499`

**Interfaces:**
- Consumes: `sseRewriter.buf`、`streamChunkRewriter.pending`、`Write`/`Flush`/`Finish` error path。
- Produces: `const maxPendingStreamBytes = 16 << 20`；`streamChunkRewriter.retainPending([]byte) error`；统一 error text `stream pending data exceeds 16777216 bytes`。

- [ ] **Step 1: 添加 SSE/raw JSON overflow RED test**

加入 table-driven test。不要直接断言 private slice capacity；只观察 API output/error：

```go
func TestStreamChunkRewriterRejectsOversizedIncompleteUnit(t *testing.T) {
	tests := []struct {
		name, prefix string
		configure    func(*streamChunkRewriter)
	}{
		{name: "sse", prefix: "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"", configure: func(r *streamChunkRewriter) {
			r.format = "openai-response"
			r.frameRawJSONAsSSE = true
		}},
		{name: "raw json", prefix: "{\"type\":\"response.output_text.delta\",\"delta\":\"", configure: func(r *streamChunkRewriter) {
			r.format = "openai-response"
			r.frameRawJSONAsSSE = true
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newStreamChunkRewriter("client")
			tt.configure(r)
			var emitted [][]byte
			prefix := []byte(tt.prefix)
			out, err := r.Write(prefix)
			if err != nil {
				t.Fatalf("prefix Write: %v", err)
			}
			emitted = append(emitted, out...)
			remaining := maxPendingStreamBytes - len(prefix)
			for remaining > 0 {
				n := min(1<<20, remaining)
				out, err = r.Write(bytes.Repeat([]byte("x"), n))
				if err != nil {
					t.Fatalf("early overflow with %d bytes remaining: %v", remaining, err)
				}
				emitted = append(emitted, out...)
				remaining -= n
			}
			out, err = r.Write([]byte("x"))
			if err == nil || !strings.Contains(err.Error(), "stream pending data exceeds") {
				t.Fatalf("overflow Write=(%q,%v)", out, err)
			}
			if len(out) != 0 || len(emitted) != 0 {
				t.Fatalf("emitted partial oversized unit: %q", append(emitted, out...))
			}
			if flushed, flushErr := r.Flush(); flushErr != nil || len(flushed) != 0 {
				t.Fatalf("Flush after overflow=(%q,%v), want cleared", flushed, flushErr)
			}
		})
	}
}
```

该测试先写一次 prefix，再用 1 MiB fragments 填满 retained limit；关键断言是 limit 前无 error，limit+1 后 error 且 Flush 无 output。

Run:

```powershell
go test . -run '^TestStreamChunkRewriterRejectsOversizedIncompleteUnit$' -count=1 -v
```

Expected: compile FAIL because `maxPendingStreamBytes` 尚不存在，或者 current code 持续接受超限 input。

- [ ] **Step 2: 添加 compatibility RED test**

加入：

```go
func TestStreamChunkRewriterPendingLimitAllowsLargeCompleteTraffic(t *testing.T) {
	largeText := strings.Repeat("x", 1<<20)
	for _, framed := range []bool{false, true} {
		r := newStreamChunkRewriter("client")
		r.format = "openai-response"
		r.frameRawJSONAsSSE = framed
		payload := []byte(`{"type":"response.output_text.delta","delta":"` + largeText + `"}`)
		out, err := r.Write(payload)
		if err != nil || len(out) == 0 {
			t.Fatalf("framed=%v large complete Write=(%d chunks,%v)", framed, len(out), err)
		}
	}

	unit := []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"" + strings.Repeat("x", 64<<10) + "\"}\n\n")
	r := newStreamChunkRewriter("client")
	r.format = "openai-response"
	r.frameRawJSONAsSSE = true
	var total int
	for total <= maxPendingStreamBytes {
		out, err := r.Write(unit)
		if err != nil || len(out) == 0 {
			t.Fatalf("total=%d Write=(%d chunks,%v)", total, len(out), err)
		}
		for _, chunk := range out {
			total += len(chunk)
		}
	}
}
```

Run:

```powershell
go test . -run '^TestStreamChunkRewriterPendingLimitAllowsLargeCompleteTraffic$' -count=1 -v
```

Expected: compile FAIL until constant exists；最终必须 PASS，且不能以累计 counter 拒绝。

- [ ] **Step 3: 实现 retained-tail limit**

在 package constants 旁加入：

```go
const maxPendingStreamBytes = 16 << 20
```

在 `sseRewriter.Write` 调用 `drain(false)` 后检查仅剩 `r.buf`：

```go
out, err := r.drain(false)
if err != nil {
	return nil, err
}
if len(r.buf) > maxPendingStreamBytes {
	r.buf = nil
	r.scanFrom = 0
	return nil, fmt.Errorf("stream pending data exceeds %d bytes", maxPendingStreamBytes)
}
return out, nil
```

在 `streamChunkRewriter` 增加统一 retained-tail helper：

```go
func (r *streamChunkRewriter) retainPending(p []byte) error {
	if len(p) > maxPendingStreamBytes {
		r.pending = nil
		return fmt.Errorf("stream pending data exceeds %d bytes", maxPendingStreamBytes)
	}
	r.pending = bytes.Clone(p)
	return nil
}
```

将 `Write` 与 `writeRawJSONArray` 中所有 `r.pending = ...` 和 `append(r.pending, ...)` 的 retained-tail assignment 改为调用 `retainPending`。不要用 caller-owned large slice 作为 backing array；helper 总是 clone tail，保证短 tail 不会保留一个远大于 16 MiB 的输入 allocation。组合旧 `pending` 与新 bytes 后，先解析完成 JSON/events，再只把 incomplete suffix 交给 helper。

若 overflow 前同一次 `Write` 已解析出 complete chunks，当前 API 不能同时可靠返回 chunks 和 terminal error；返回 error 且不返回该次 chunks，之前调用已成功 emit 的 chunks保持不变。

- [ ] **Step 4: 运行 focused limit tests，确认 GREEN**

Run:

```powershell
go test . -run 'TestStreamChunkRewriter(RejectsOversizedIncompleteUnit|PendingLimitAllowsLargeCompleteTraffic|EmitsCompletedRawJSONArrayElements|RawJSONArrayPartitions|BuffersSplitSSEPrefix)' -count=1 -v
```

Expected: PASS。

- [ ] **Step 5: 添加 forwarding overflow integration**

使用 custom `hostCaller`，每次 `host.model.stream_read` 返回 1 MiB 的同一未终止 Responses `data:` JSON fragment，直到超过 limit。记录 `host.stream.emit`、`host.model.stream_close` 和 `host.stream.close` payload。断言：

```go
if emitCalls != 0 {
	t.Fatalf("emit calls=%d, want 0", emitCalls)
}
if hostCloseCalls != 1 || pluginCloseCalls != 1 {
	t.Fatalf("close calls host=%d plugin=%d", hostCloseCalls, pluginCloseCalls)
}
if !strings.Contains(pluginCloseError, "stream pending data exceeds 16777216 bytes") {
	t.Fatalf("plugin close error=%q", pluginCloseError)
}
```

使用 channel 等待 async stream close，timeout 保持现有 2 秒测试惯例。测试结束后确认第二条正常 stream 用独立 rewriter 仍可完整执行，证明 overflow state 不跨 stream。

Run:

```powershell
go test . -run '^TestRunStreamForwardClosesOnlyOversizedIncompleteStream$' -count=1 -v
```

Expected: PASS；production implementation 前应 FAIL 或 timeout/OOM 风险，因此先以较小 internal test seam 验证 RED，再运行 16 MiB integration。

- [ ] **Step 6: 运行 stream tests 并提交**

Run:

```powershell
go test . -run 'SSE|StreamChunkRewriter|RunStreamForward|HandleExecutorExecuteStream' -count=1
git diff --check
git add main.go main_test.go
git commit -m "fix: bound incomplete stream state"
```

Expected: PASS，commit 只含 retained-state limit 与 tests。

---

### Task 4: 保留 host structured error 与 HTTP status

**Files:**
- Modify: `main.go:1319-1337`，`main.go:1505-1592`，`main.go:1724-1799`，`main.go:1916-1942`
- Test: `main_test.go:4000-4040`，`main_test.go:6322-6386`，`main_test.go:6989-7056`

**Interfaces:**
- Consumes: `pluginabi.Error`、`wrapEnvelope([]byte, error)`、`callHost`、mapped nonstream/stream host responses。
- Produces: internal `structuredPluginError` implementing `error` and optional `Unwrap() error`；`structuredError(code, message string, retryable bool, httpStatus int, cause error) error`。

- [ ] **Step 1: 添加 nested host envelope propagation RED test**

在 `callHost` tests 附近加入：

```go
func TestCallHostPreservesStructuredErrorEnvelope(t *testing.T) {
	want := pluginabi.Error{Code: "rate_limit_error", Message: "retry later", Retryable: true, HTTPStatus: http.StatusTooManyRequests}
	response, err := json.Marshal(pluginabi.Envelope{OK: false, Error: &want})
	if err != nil {
		t.Fatal(err)
	}
	setHostCallbackForTest(func(string, []byte) ([]byte, error) { return response, nil })
	t.Cleanup(func() { setHostCallbackForTest(nil) })
	_, hostErr := callHost("host.model.execute", map[string]any{})
	if hostErr == nil {
		t.Fatal("callHost error=nil")
	}
	raw, err := wrapEnvelope(nil, fmt.Errorf("execute: %w", hostErr))
	if err != nil {
		t.Fatal(err)
	}
	var got pluginabi.Envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Error == nil || *got.Error != want {
		t.Fatalf("envelope=%s, want %#v", raw, want)
	}
}
```

Run:

```powershell
go test . -run '^TestCallHostPreservesStructuredErrorEnvelope$' -count=1 -v
```

Expected: FAIL，current output 是 `code=plugin_error`，message 带 plain wrapper，缺少 retryable/http_status。

- [ ] **Step 2: 添加 status-only nonstream/stream table RED tests**

对 400、429、503 table，每项分别调用 `handleExecutorExecute` 和 `prepareExecutorStream`，再传给 `wrapEnvelope` 并 decode：

```go
for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
	t.Run(strconv.Itoa(status), func(t *testing.T) {
		// host returns HostModelExecutionResponse or HostModelStreamResponse + Body
		// decode wrapEnvelope result into pluginabi.Envelope
		if got.Error.Code != "plugin_error" || got.Error.HTTPStatus != status {
			t.Fatalf("error=%#v", got.Error)
		}
		if !strings.Contains(got.Error.Message, strconv.Itoa(status)) || !strings.Contains(got.Error.Message, "upstream failed") {
			t.Fatalf("message=%q", got.Error.Message)
		}
	})
}
```

stream case 保留现有 live-host-stream close assertion；增加一个 close failure subtest并继续断言 `errors.Is(err, closeErr)`，envelope status 仍为原始 400/429/503。

增加 generic guard：

```go
raw, _ := wrapEnvelope(nil, errors.New("internal failure"))
// assert code=plugin_error, message="internal failure", HTTPStatus=0, Retryable=false
```

Run:

```powershell
go test . -run 'Test(CallHostPreservesStructuredErrorEnvelope|HandleExecutorExecutePropagatesHostHTTPStatus|PrepareExecutorStreamPropagatesHostHTTPStatus|WrapEnvelopeKeepsGenericErrorsStatusless)' -count=1 -v
```

Expected: structured/status tests FAIL；generic guard PASS。

- [ ] **Step 3: 实现 typed error 和 envelope encoding**

在 `hostCaller` 附近加入：

```go
type structuredPluginError struct {
	detail pluginabi.Error
	cause  error
}

func (e *structuredPluginError) Error() string { return e.detail.Message }
func (e *structuredPluginError) Unwrap() error { return e.cause }

func newStructuredPluginError(detail pluginabi.Error, cause error) error {
	return &structuredPluginError{detail: detail, cause: cause}
}
```

在 `callHost` 的 `!env.OK && env.Error != nil` branch 复制 value，避免保留 decoder owner 之外的 pointer：

```go
detail := *env.Error
return nil, newStructuredPluginError(detail, nil)
```

在 nonstream status branch 返回：

```go
message := fmt.Sprintf("host.model.execute status %d: %s", hostResp.StatusCode, string(hostResp.Body))
return nil, newStructuredPluginError(pluginabi.Error{
	Code:       "plugin_error",
	Message:    message,
	HTTPStatus: hostResp.StatusCode,
}, nil)
```

在 stream status branch 先构造现有 status message并关闭可能存在的 host stream。close 成功则 typed error 的 cause 为 nil；close 失败则将 cleanup error合并进 `detail.Message`，并把原始 `closeErr` 放入 `cause`，使现有 `errors.Is` assertion 保持有效：

```go
message := fmt.Sprintf("execute stream status %d: %s", hostResp.StatusCode, string(hostResp.Body))
var cause error
if stream.hostStreamID != "" {
	if closeErr := stream.closeHost(); closeErr != nil {
		cause = closeErr
		message = errors.Join(errors.New(message), fmt.Errorf("close host stream: %w", closeErr)).Error()
	}
}
return nil, nil, newStructuredPluginError(pluginabi.Error{
	Code:       "plugin_error",
	Message:    message,
	HTTPStatus: hostResp.StatusCode,
}, cause)
```

将 error envelope 编码集中为：

```go
func marshalErrorEnvelope(detail pluginabi.Error) []byte {
	raw, err := json.Marshal(pluginabi.Envelope{OK: false, Error: &detail})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"plugin_error","message":"failed to encode error envelope"}}`)
	}
	return raw
}

func wrapEnvelope(payload []byte, err error) ([]byte, error) {
	if err != nil {
		var structured *structuredPluginError
		if errors.As(err, &structured) {
			return marshalErrorEnvelope(structured.detail), nil
		}
		return errorEnvelope("plugin_error", err.Error()), nil
	}
	// existing success path
}

func errorEnvelope(code, message string) []byte {
	return marshalErrorEnvelope(pluginabi.Error{Code: code, Message: message})
}
```

nested host error 使用其原始 `detail.Message`，不把内部 callback wrapper 文本写回 client message；status-only errors 使用上述完整 diagnostic message。

- [ ] **Step 4: 运行 focused error tests，确认 GREEN**

Run:

```powershell
go test . -run 'Test(CallHostPreservesStructuredErrorEnvelope|HandleExecutorExecuteReturnsErrorForHostHTTPStatus|HandleExecutorExecutePropagatesHostHTTPStatus|RunStreamForwardClosesHostStreamOnHTTPError|PrepareExecutorStreamPropagatesHostHTTPStatus|WrapEnvelopeKeepsGenericErrorsStatusless|WrapEnvelopeAvoidsPayloadSizedIntermediate|CallHostReturnsDecoderOwnedResult)' -count=1 -v
```

Expected: PASS。

- [ ] **Step 5: 运行 executor/error regression 并提交**

Run:

```powershell
go test . -run 'Executor|CallHost|WrapEnvelope|HostHTTPStatus' -count=1
git diff --check
git add main.go main_test.go
git commit -m "fix: propagate host error status"
```

Expected: PASS，nested `retryable=true` 只在插件 ABI envelope assertion 中验证，不声称 CPA v7.2.152 outer decoder保留它。

---

### Task 5: 更新公开行为与 v0.5.6 release examples

**Files:**
- Modify: `README.md:180-223`

**Interfaces:**
- Consumes: Task 2 的 delimiterless Responses recovery、Task 3 的 16 MiB limit、Task 4 的 error propagation。
- Produces: 用户可见的准确边界说明和 `VERSION=0.5.6` examples。

- [ ] **Step 1: 更新 README rewrite boundaries**

在 `README.md` 的 stream paragraphs 中加入以下事实，不增加新的 config surface：

```markdown
Delimiterless OpenAI Responses logical events produced by CPA's provider translators are separated by their complete JSON `data:` values before model restoration; host stream-read boundaries alone are never treated as SSE delimiters.

The plugin retains at most 16 MiB for one incomplete SSE event or raw JSON value per stream. This limit does not apply to cumulative stream traffic or batches of complete events. Exceeding it closes only that stream with an error and emits no partial oversized unit.

Structured host callback errors preserve their plugin ABI `code`, `message`, `retryable`, and `http_status`. Status-only mapped execution failures preserve the HTTP status with `plugin_error`; the ABI has no error-header field, so response headers such as `Retry-After` are not synthesized.
```

确保文本不声称 CPA v7.2.152 在插件外继续保留 `retryable`；可改写为明确的 plugin ABI boundary。

- [ ] **Step 2: bump README package examples**

将三处 `VERSION=0.5.5` 改为 `VERSION=0.5.6`。仓库没有 source version file；release version 由 tag/Makefile ldflags 注入，因此不要改 `pluginVersion = "0.0.0-dev"`。

- [ ] **Step 3: 检查文档和提交**

Run:

```powershell
git diff --check
git diff -- README.md
git add README.md
git commit -m "docs: document stream safety boundaries"
```

Expected: diff 只含三项行为说明和 v0.5.6 command examples。

---

### Task 6: 全量验证和计划完成检查

**Files:**
- Modify only if verification identifies a defect: `main.go`、`main_test.go`、`README.md`、本次 spec/plan。
- Do not create or modify old verification documents。

**Interfaces:**
- Consumes: Tasks 1-5 的 commits。
- Produces: clean verified feature branch，供 local main fast-forward merge。

- [ ] **Step 1: 运行 focused reproduction**

Run:

```powershell
go test . -run 'TestStreamChunkRewriterSeparatesDelimiterlessKimiResponsesEvents|TestStreamChunkRewriterDelimiterlessResponsesPartitionInvariant|TestRunStreamForwardSeparatesDelimiterlessKimiResponsesLifecycle|TestStreamChunkRewriterRejectsOversizedIncompleteUnit|TestRunStreamForwardClosesOnlyOversizedIncompleteStream|TestCallHostPreservesStructuredErrorEnvelope|TestHandleExecutorExecutePropagatesHostHTTPStatus|TestPrepareExecutorStreamPropagatesHostHTTPStatus' -count=1 -v
```

Expected: all PASS；输出中没有 malformed `}event:`。

- [ ] **Step 2: 并行运行互不写文件的基础验证**

Run as independent processes：

```powershell
go test -count=1 ./...
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

Expected: all exit 0。不要运行 `go test ./.github/scripts`。

- [ ] **Step 3: 运行 race test**

Run:

```powershell
go test -race -count=1 ./...
```

Expected: PASS，无 race report。

- [ ] **Step 4: 验证 release version 和本机 package path**

Run:

```powershell
go run .github/scripts/package-release.go -validate-only -version 0.5.6
make package VERSION=0.5.6 GOOS=windows GOARCH=amd64
```

Expected: version validation PASS；Windows amd64 c-shared build/package compatibility inspection PASS，archive 名为 `model-mapper_0.5.6_windows_amd64.zip`。生成的 `dist/` 保持 ignored，不加入 commit。

若本机缺少 C compiler 或 make，记录确切 command/error，继续完成所有其他 deterministic checks；GitHub Actions 是 cross-platform authoritative build。

- [ ] **Step 5: 检查 diff scope 与计划完成度**

Run:

```powershell
git status --short --branch
git diff --check
git diff main...HEAD --stat
git diff main...HEAD -- main.go main_test.go README.md docs/superpowers/specs/2026-09-12-stream-safety-and-error-propagation-design.md docs/superpowers/plans/2026-09-12-stream-safety-and-error-propagation-implementation.md
git log --oneline --decorate main..HEAD
```

Expected: tracked files只有 `main.go`、`main_test.go`、`README.md` 和本次 spec/plan；`.claude/` 仍未跟踪；没有 CPA source、dependency 或旧文档改动；所有 plan tasks有对应 commit/test evidence。

- [ ] **Step 6: 使用 Opus 5 对三个互不重叠 finding 的最终 diff 做一次逐段 review**

Review partition：

1. Responses boundary code/tests，仅检查分片不变量、JSON-aware boundary、SSE regression。
2. retained-state limit code/tests，仅检查 current incomplete unit、allocation retention、overflow cleanup。
3. structured error code/tests，仅检查 code/message/status/retryable boundary和 generic fallback。

同一 reviewer 不重复另一个 partition；发现问题时先写或强化 focused failing test，再改 production code并重跑该 partition和全量 suite。不要调用会对同一文件启动重复 angle agents 的 code-review skill。

- [ ] **Step 7: 提交 verification 后产生的必要修正**

仅当 Step 1-6 实际产生修正时：

```powershell
git add main.go main_test.go README.md docs/superpowers/specs/2026-09-12-stream-safety-and-error-propagation-design.md docs/superpowers/plans/2026-09-12-stream-safety-and-error-propagation-implementation.md
git commit -m "test: complete v0.5.6 verification"
```

Expected: 无修正时跳过 commit，不创建空 commit。

---

### Task 7: 合并 local main、tag、push 并检查 GitHub Actions

**Files:**
- No source edits expected。
- Generated ignored artifacts: `dist/`，不得提交。

**Interfaces:**
- Consumes: verified `fix/v0.5.6-stream-safety` HEAD。
- Produces: local `main` fast-forward、remote `origin/main`、annotated tag `v0.5.6`、GitHub release workflow run。

- [ ] **Step 1: 在 shipping 前重新运行 completion gate**

Run:

```powershell
git status --short --branch
go test -count=1 ./...
go vet ./...
```

Expected: tests/vet PASS；除初始 `.claude/` 和 ignored artifacts 外无未提交改动。

- [ ] **Step 2: fast-forward merge local main**

Run:

```powershell
git switch main
git merge --ff-only fix/v0.5.6-stream-safety
```

Expected: local `main` fast-forward到 verified feature HEAD，无 merge conflict或 merge commit。

- [ ] **Step 3: 创建 annotated patch tag**

先确认 tag 不存在：

```powershell
git tag --list v0.5.6
```

Expected: 无输出。随后：

```powershell
git tag -a v0.5.6 -m "v0.5.6"
```

Expected: annotated tag 指向 local `main` HEAD。

- [ ] **Step 4: push main 和 tag**

原任务已经明确授权此次 external shipping action。Run:

```powershell
git push origin main
git push origin v0.5.6
```

Expected: 两次 push 成功；tag push 触发 `.github/workflows/build.yml` release path。

- [ ] **Step 5: 检查 GitHub workflow**

Run:

```powershell
gh run list --workflow build.yml --branch v0.5.6 --limit 1
gh run watch --exit-status $(gh run list --workflow build.yml --branch v0.5.6 --limit 1 --json databaseId --jq '.[0].databaseId')
```

PowerShell 中若命令替换或 `gh` 版本不接受该形式，先把 run ID 存入 `$runId` 后调用 `gh run watch $runId --exit-status`。Expected: tag 对应 workflow conclusion 为 `success`，release job 发布 v0.5.6 artifacts。

若 workflow 失败，读取失败 job log，仅修复本仓库能解决的原因；按 TDD/verification 重做相应 task，删除未发布或错误 tag前必须先确认当前 remote release状态。原任务授权修复并继续 push，但不授权覆盖他人随后更新的 remote branch，push 前必须 fetch并检查 divergence。

- [ ] **Step 6: 最终状态确认与临时文件清理**

Run:

```powershell
git status --short --branch
git log -1 --oneline --decorate
git tag --points-at HEAD
git ls-remote --heads --tags origin main v0.5.6
```

Expected: local `main` 与 `origin/main` 同步，HEAD 带 `v0.5.6`，初始 `.claude/` 保留。

删除仓库外 reproduction 目录 `C:\Users\user\AppData\Local\Temp\cpa-plugin-repro-20260912` 和临时任务记忆 `C:\Users\user\.claude\projects\C--Users-user-Downloads-cpa-plugin\audit-task-temporary.md`，但只在 GitHub workflow 成功且本次最终报告所需证据已经整理后执行。不要删除仓库内初始 `.claude/`。
