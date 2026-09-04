# Model Mapper Complete Fixes And Performance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在只修改 model-mapper 插件仓库的前提下，用 TDD 连续完成所有已验证且插件可独立修复的功能问题、性能优化、ABI 防护和发布工具修复。

**Architecture:** 保留 `sseRewriter` 对任意 raw bytes 的字节语义，在 `streamChunkRewriter` 增加针对已复现 CPA Responses chunk 形态的保守兼容层。配置解析、模型重写和 host callback 的现有边界不变，性能优化只减少不必要的复制、扫描、分配和 emit，不改变 raw JSON/WebSocket 的既有 chunk boundary。

**Tech Stack:** Go 1.26、标准库 `encoding/json`、`mime`、`net`、`regexp`、cgo C ABI、`gopkg.in/yaml.v3`、Go test、race detector、checkptr、GitHub Actions。

**Spec:** `.claude/plan/tender-yawning-shamir.md`

## Global Constraints

- 只修改 `C:\Users\user\Downloads\cpa-plugin`，不修改 CLIProxyAPI 源码、`go/pkg/mod`、外部工作树或 CPA release。
- 每个行为修改先写最小失败 Go test，再修改生产代码，再运行 focused test。
- 请求只改顶层 JSON `model` 字段；response 只恢复既有白名单字段。
- 不对任意 raw byte continuation 推断逻辑 SSE boundary；插件侧边界修复只覆盖已复现的完整 field 形态，并保留 `data: one` 加 `event: two` 的现有行为。
- `text/event-stream` 判断必须基于解析后的 media type，而不是字符串 substring。
- SSE field value 只按规范移除 colon 后最多一个 ASCII SPACE。
- malformed JSON、未知 SSE field、普通文本和 opaque content 必须保持既有 passthrough 语义。
- 性能优化必须保留 output ownership、字节顺序、SSE delimiter、raw JSON/WebSocket emit boundary 和 `-race` 正确性。
- 不实施已被历史 workflow refute 或判定为 cleanup 的候选：caller cache cap/TTL、规则预扫描、helper/switch 合并、SSE buffer 几何增长、跨 payload nested raw JSON 生产错误和测试可读性整理。
- CPA pluginhost lease/unload、route/reconfigure snapshot、count-token host callback 和 host-owned `caller_scope` 只在最终报告中列为外部阻塞，不添加插件伪修复。

---

### Task 1: Freeze Baseline And Add Stream Red Tests

**Files:**
- Modify: `main_test.go`，在现有 SSE 和 stream rewriter 测试附近增加回归测试
- Read-only reference: `main.go:28-501`，确认现有 parser 和 helper 边界

**Interfaces:**
- Consumes: `newStreamChunkRewriter`、`sseRewriter.Write`、`streamChunkRewriter.Flush`、`flattenChunks`
- Produces: 覆盖已复现 EOF 场景的失败测试，后续 Task 2 至 Task 4 必须保持这些测试

- [x] **Step 1: 运行基线测试**

Run:

```text
go test ./...
go vet ./...
go test -race -count=1 ./...
go test -gcflags=all=-d=checkptr=2 ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
```

Expected: 当前历史基线通过。`CPA_SMOKE_API_KEY` 或 `CPA_SMOKE_CPA_BIN` 未设置时不运行 live smoke。

- [x] **Step 2: Write the failing tests**

增加以下测试行为，测试名称可按现有命名风格使用：

```go
r := newStreamChunkRewriter("client")
r.frameRawJSONAsSSE = true
first, err := r.Write([]byte("event: response.completed"))
if err != nil || len(first) != 0 {
    t.Fatalf("first write = (%q, %v), want buffered", first, err)
}
second, err := r.Write([]byte(`data: {"response":{"model":"upstream"}}`))
if err != nil {
    t.Fatal(err)
}
second = append(second, mustFlush(t, r)...)
if got := flattenChunks(second); !strings.Contains(got, "event: response.completed\n") || !strings.Contains(got, `"model":"client"`) {
    t.Fatalf("output = %q, want separate event/data lines and restored model", got)
}
```

同时增加表格测试，确认以下输入输出不回归：

```go
// Existing raw continuation semantics must remain byte-preserving.
input := []string{"data: one", "event: two\n\n"}
want := "data: oneevent: two\n\n"
```

覆盖连续完整 JSON `data:` fields、JSON 内部 split、LF、CR、CRLF、split prefix、split delimiter、flush、exact Content-Type、colonless scalar、BOM 和 data value 控制字节。

- [x] **Step 3: Run only the new stream tests**

Run:

```text
go test . -run 'Test(StreamChunk|SSERewriter|ContentType|BOM|Colonless)' -count=1
```

Expected: 新增的 event/data boundary、Content-Type、BOM、colonless scalar 或 field-value 测试至少有一项失败，现有 continuation 测试仍通过。记录失败输出后进入 Task 2。

- [x] **Step 4: Commit the red-test checkpoint**

```text
git add main_test.go
git commit -m "test: cover verified stream response regressions"
```

---

### Task 2: Fix Known SSE Logical Boundary And Media Type Handling

**Files:**
- Modify: `main.go:35-50, 313-335, 425-450, 482-500, 857-859`
- Test: `main_test.go`，使用 Task 1 的 stream tests

**Interfaces:**
- Consumes: `streamChunkRewriter.Write`、`sseRewriter.Write`、`isSSEChunk`、`isIncompleteSSEPrefix`
- Produces: `streamChunkRewriter` 的保守 boundary compatibility；`frameRawJSONAsSSE` 只由精确 media type 设置

- [x] **Step 1: Implement the smallest boundary helper**

增加一个只处理已确认形态的 helper。其规则必须是：

```go
// Separate a known complete SSE field from a following field prefix.
// Do not separate arbitrary data continuation.
func knownSSELogicalBoundary(pending, next []byte) bool
```

返回 true 的条件：pending 没有 CR/LF，pending 是完整 `event:` field 且 next 以 `data:` 或其他 SSE field prefix 开始；或者 pending 是完整且 `json.Valid` 的 `data:` value 且 next 以 SSE field prefix 开始。`data: one` 不满足完整 JSON 条件，必须返回 false。

在 `streamChunkRewriter.Write` 进入现有 append 或 `r.sse.Write` 前调用该判断，必要时只向 pending 末尾加入一个 LF。未知 raw continuation 不加入 LF。该 helper 不修改 `sseRewriter.Write` 的通用语义。

- [x] **Step 2: Run focused stream tests**

```text
go test . -run 'Test(StreamChunk|SSERewriter)' -count=1
```

Expected: event/data EOF 回归和现有 `data: one` continuation 测试通过。若普通 SSE field 或 split JSON 失败，先修正 Task 2 的 boundary 条件，不进入后续任务。

- [x] **Step 3: Replace Content-Type substring check**

导入 `mime`，增加精确解析逻辑：

```go
mediaType, _, err := mime.ParseMediaType(hostResp.Headers.Get("Content-Type"))
rewriter.frameRawJSONAsSSE = err == nil && strings.EqualFold(mediaType, "text/event-stream")
```

`application/json; profile="text/event-stream"` 必须保持 raw JSON，不得被 framing 为 `data:`。

- [x] **Step 4: Verify focused tests and ownership**

```text
go test . -run 'Test(StreamChunk|SSERewriter|ContentType)' -count=10
```

Expected: 全部通过，输出字节顺序和 existing flush behavior 不变。

- [x] **Step 5: Commit the stream boundary fix**

```text
git add main.go main_test.go
git commit -m "fix: preserve known SSE field boundaries"
```

---

### Task 3: Fix SSE Protocol Edge Cases Without Changing Raw Semantics

**Files:**
- Modify: `main.go:28-47, 153-223, 226-257, 313-335`
- Test: `main_test.go`

**Interfaces:**
- Consumes: `splitSSELine`、`rewriteEvent`、`rewriteMultiDataEvent`、`tryRawJSONChunks`
- Produces: `sseFieldValue(line []byte) []byte` 和 stream-start BOM state

- [x] **Step 1: Write the failing field-value and BOM tests**

测试以下输入必须保持规定语义：

```go
input := "data:\v{\"model\":\"upstream\"}\f\n\n"
// VT and FF are data bytes. They must not be removed or decoded as JSON.

for split := 0; split <= 3; split++ {
    // EF BB BF + data field, split at every BOM boundary.
    // Joined output and restored model must be identical for every split.
}
```

增加 colonless `null\n\n`、`true\n\n`、数字和 JSON string 的 event-count 测试，确认 unknown field 不变成 `data:` event。

- [x] **Step 2: Run red tests**

```text
go test . -run 'Test(SSE.*Field|SSE.*BOM|Colonless)' -count=1
```

Expected: 当前 `bytes.TrimSpace`、raw JSON fast path 或 BOM partition 至少使一个测试失败。

- [x] **Step 3: Add the exact field-value helper**

实现：

```go
func sseFieldValue(line []byte) []byte {
    value := line[len("data:"):]
    if len(value) > 0 && value[0] == ' ' {
        value = value[1:]
    }
    return value
}
```

用该 helper 替换单 data 和 multi-data 路径中的 `bytes.TrimSpace`。解析 JSON 时让 `encoding/json` 处理 JSON 自身允许的 whitespace。

- [x] **Step 4: Add one-shot BOM state**

在 `sseRewriter` 增加 stream-start BOM state。仅在流开头缓存 `EF`、`EF BB` 或 `EF BB BF`，识别完整 BOM 后从 parser view 移除恰好一个 BOM。BOM 和 `data:` 的所有 chunk split 必须得到相同输出和 model restoration。流中部 U+FEFF 不作 BOM 处理。

- [x] **Step 5: Reorder colonless SSE classification**

在 `streamChunkRewriter.Write` 的 raw JSON fast path 前，识别含完整 event delimiter 且没有 colon 的 SSE field。该路径交给 `sseRewriter`，不得调用 `frameSSEData`。带 object/array 的 raw JSON 仍先使用现有完整 JSON path。

- [x] **Step 6: Run protocol tests**

```text
go test . -run 'Test(SSE|StreamChunk|Colonless)' -count=20
```

Expected: 所有新增和既有 SSE 测试通过，malformed passthrough 与 `[DONE]` 保持原样。

- [x] **Step 7: Commit protocol corrections**

```text
git add main.go main_test.go
git commit -m "fix: preserve SSE field bytes and stream-start BOM"
```

---

### Task 4: Fix Initial Configuration Loading And Gemini Declaration

**Files:**
- Modify: `main.go:20-22, 521-552, 625-640, 1015-1073`
- Modify: `go.mod`, `go.sum`
- Test: `main_test.go`

**Interfaces:**
- Consumes: `decodeLifecycleConfig`、`decodeConfig`、`compileConfig`、`pluginRegistration`
- Produces: shared lifecycle config apply path and registration containing `gemini`

- [x] **Step 1: Add the YAML dependency declaration**

Add the direct dependency used by production code:

```text
go get gopkg.in/yaml.v3
```

Do not add any other dependency. `go.mod` must identify the YAML module as direct after `go mod tidy`.

- [x] **Step 2: Write failing configuration tests**

Add tests that encode lifecycle `ConfigYAML` and assert:

```go
raw := lifecyclePayloadWithYAML(t, "global_rules: a=>b # comment\n")
if _, err := handlePluginRegister(raw); err != nil {
    t.Fatal(err)
}
route := routeForModel(t, "a")
if !route.Handled {
    t.Fatal("initial register did not publish global_rules")
}
```

Cover plain scalar, single quote, double quote, inline comments, bad DSL, empty config, `enabled`, `priority`, unknown fields and one register without a second reconfigure. Assert `pluginRegistration().Capabilities.ExecutorInputFormats` contains `gemini`.

- [x] **Step 3: Run configuration tests to verify red state**

```text
go test . -run 'Test(PluginRegister|LifecycleConfig|YAML|PluginRegistration|Gemini)' -count=1
```

Expected: initial register test fails because `handlePluginRegister` ignores raw input, inline comment test fails with the hand-written parser, and Gemini assertion fails.

- [x] **Step 4: Replace hand-written YAML scalar parsing**

Define a YAML-only input struct with exactly these fields and tags:

```go
type lifecycleYAMLConfig struct {
    GlobalRules            string `yaml:"global_rules"`
    ClaudeMessagesRules    string `yaml:"claude_messages_rules"`
    CodexResponsesRules    string `yaml:"codex_responses_rules"`
    OpenAICompletionsRules string `yaml:"openai_completions_rules"`
}
```

After base64 decoding `ConfigYAML`, call `yaml.Unmarshal`, marshal the four fields into the existing `Config` JSON shape, and pass it through `decodeConfig`. Remove `Scanner`, `strings.Cut` and `unquoteYAMLScalar` from the lifecycle YAML path. Preserve the existing JSON config path.

- [x] **Step 5: Share register and reconfigure publish logic**

Create one private helper that decodes lifecycle config, decodes plugin config, compiles all rule sets and calls `setLoadedConfigForTest`. Call it from both `handlePluginRegister` and `handlePluginReconfigure` before returning registration. Invalid initial config must return an error and must not publish a bad snapshot.

- [x] **Step 6: Add Gemini input format**

Change only the registration input list:

```go
ExecutorInputFormats: []string{"openai", "claude", "openai-response", "gemini"},
```

Keep existing output formats and response whitelist unchanged.

- [x] **Step 7: Run focused and dependency checks**

```text
go test . -run 'Test(PluginRegister|LifecycleConfig|YAML|PluginRegistration|Gemini)' -count=20
go mod tidy
go test ./...
```

- [x] **Step 8: Commit configuration fixes**

```text
git add main.go main_test.go go.mod go.sum
git commit -m "fix: load initial config and declare Gemini input"
```

---

### Task 5: Add C ABI Length Guards

**Files:**
- Modify: `abi_cgo.go:74-121`
- Create: `abi_cgo_test.go` with `//go:build cgo`

**Interfaces:**
- Consumes: exported callback functions `cliproxy_plugin_init` and `cliproxyPluginCall`
- Produces: checked conversion helpers that return errors instead of panicking on oversized C lengths

- [x] **Step 1: Write boundary tests**

Add tests around pure Go-checkable helpers for:

```go
if _, ok := cIntLength(uint64(maxCInt) + 1); ok {
    t.Fatal("oversized C length accepted")
}
if _, ok := pluginResponseLength(maxPluginResponseBytes + 1); ok {
    t.Fatal("oversized plugin response accepted")
}
```

Also cover zero length, nil pointers and the largest accepted values without allocating multi-gigabyte buffers.

- [x] **Step 2: Run ABI tests in red state**

```text
go test . -run 'Test(ABI|CInt|PluginResponseLength)' -count=1
```

Expected: helper symbols or guards are absent and the new tests fail to compile or fail their assertions.

- [x] **Step 3: Implement checked conversions**

Before `C.GoBytes`, reject `response.len` above the maximum representable `C.int`. Before copying plugin output into `*[1 << 30]byte`, reject output above the fixed safe response limit. Return callback error or C return code `1`; do not call `C.GoBytes` with a wrapped negative length and do not let the copy expression panic.

Keep normal callback free, empty response and successful result behavior unchanged.

- [x] **Step 4: Verify ABI and normal tests**

```text
go test . -run 'Test(ABI|CInt|PluginResponseLength)' -count=20
go test -race ./...
go test -gcflags=all=-d=checkptr=2 ./...
```

- [x] **Step 5: Commit ABI guards**

```text
git add abi_cgo.go abi_cgo_test.go
git commit -m "fix: reject oversized C ABI buffers safely"
```

---

### Task 6: Release Setup Memory After Stream Execute

**Files:**
- Modify: `main.go:748-923`
- Test: `main_test.go`，使用现有 stream test helpers 增加 heap retention test

**Interfaces:**
- Consumes: `executorRPCRequest`、`startExecutorStream`、`runStreamForward`
- Produces: pointer-based short-lived request setup and explicit release of unused large fields

- [x] **Step 1: Write the failing retention test**

Create a blocked host stream callback. Pass an approximately `32 MiB` `OriginalRequest`, headers, query and metadata, block after `host.model.execute_stream` returns, and measure heap retention until stream close. The baseline assertion must observe the request body remains reachable.

- [x] **Step 2: Run the memory test**

```text
go test . -run 'Test.*OriginalRequest.*Retained' -count=3 -v
```

Expected: baseline retains the large request while `host.model.stream_read` is blocked.

- [x] **Step 3: Separate setup and read-loop lifetimes**

Change `runStreamForward` to receive a pointer to a request copy owned by the goroutine:

```go
reqCopy := req
go func() {
    if err := runStreamForward(&reqCopy, call); err != nil {
        _ = closeStream(reqCopy.StreamID, err.Error())
    }
}()
```

Use a release function after request rewrite and the execute-stream callback completes:

```go
releaseSetup := func() {
    req.OriginalRequest = nil
    req.Headers = nil
    req.Query = nil
    req.Metadata = nil
    body = nil
}
```

Keep stream ID, callback ID, format, original model and mapped model values needed by close/read/emit. Release setup fields on both callback success and callback error.

- [x] **Step 4: Run focused memory and stream tests**

```text
go test . -run 'Test(.*OriginalRequest.*Retained|HandleExecutorExecuteStream|RunStreamForward)' -count=10
go test -race ./...
```

Expected: retained bytes fall to measurement noise after setup while all close and host error tests pass.

- [x] **Step 5: Commit memory lifetime fix**

```text
git add main.go main_test.go
git commit -m "perf: release stream setup request memory early"
```

---

### Task 7: Reduce Response Restore Scans And Multi-Data Allocations

**Files:**
- Modify: `main.go:153-223, 348-403, 1122-1164`
- Test: `main_test.go`

**Interfaces:**
- Consumes: `mightContainResponseModelField`、`restoreResponseModel`、`rewriteResponseModelFieldsWithReplacement`、`tryRawJSONChunks`
- Produces: virtual marker scanner, validity-aware restore helper and escaped-key-aware prefilter

- [x] **Step 1: Write failing behavior and allocation tests**

Add tests for:

```go
// A marker can cross the virtual newline inserted between data fields.
event := []byte("data: {\"respo\n\ndata: nse\":{\"model\":\"upstream\"}}")
// The logical joined scan must still enter restore when the marker is possible.
```

Add an `AllocsPerRun` test for a large no-model multi-data event. Add a test proving ordinary escaped text containing `\u` remains clone-only, while escaped keys for `model`, `modelVersion`, `response` and `message` still restore.

- [x] **Step 2: Run red tests and baseline benchmark**

```text
go test . -run 'Test(MultiData|MightContain|RestoreResponse)' -count=1
go test . -run '^$' -bench 'Benchmark(RestoreResponse|StreamChunkRewriter)' -benchmem -benchtime=500ms -count=5
```

Expected: marker-before-join allocation test demonstrates current joined allocation; escaped ordinary text demonstrates current broad `\\u` prefilter cost; behavior tests expose any missing cross-field marker handling.

- [x] **Step 3: Implement marker scan over virtual joined bytes**

Add a scanner that iterates each `data:` value with `sseFieldValue`, inserts a virtual LF between values, and retains a suffix of the longest marker. It must detect markers spanning two fields without constructing `joined`. Only after a marker is found may `rewriteMultiDataEvent` allocate and build the joined payload. If no marker is found, return `appendUnchangedSSEEvent` directly.

Do not scan each field independently. The virtual stream must include the same LF that the later join operation uses.

- [x] **Step 4: Make raw restore validity-aware**

Add a private helper with a stable signature:

```go
func (r *sseRewriter) restoreResponseModelChecked(body []byte) (out []byte, changed bool, valid bool, err error)
```

When the response marker prefilter is positive, let `json.Unmarshal` provide the only validity scan and return `valid`. In `tryRawJSONChunks`, use that result instead of calling `json.Valid` first. When the prefilter is negative, retain the single existing validity check and clone fast path. Invalid input must continue to pass through byte-for-byte.

- [x] **Step 5: Implement conservative escaped-key prefilter**

Replace broad `bytes.Contains(body, []byte(` + "`\\u`" + `))` behavior with a lexical scan of JSON string tokens. Only inspect a string as a possible key when its closing quote is followed by JSON whitespace and `:`. Decode a small escaped key candidate and compare against `model`, `modelVersion`, `response` and `message`. If the scanner cannot prove the token is ordinary text, return true and preserve correctness by entering the existing JSON decode path.

- [x] **Step 6: Verify behavior and allocation limits**

```text
go test . -run 'Test(MultiData|MightContain|RestoreResponse)' -count=20
go test -race ./...
go test . -run '^$' -bench 'Benchmark(RestoreResponse|StreamChunkRewriter)' -benchmem -benchtime=500ms -count=7
```

Expected: no-model multi-data path avoids joined allocation; cross-field marker behavior remains correct; ordinary escaped text remains clone-only; raw model events no longer execute duplicate validity scans.

- [x] **Step 7: Commit parser hot-path optimizations**

```text
git add main.go main_test.go
git commit -m "perf: avoid unnecessary response restore scans"
```

---

### Task 8: Batch SSE Emits Without Changing Raw Chunk Boundaries

**Files:**
- Modify: `main.go:850-921`
- Test: `main_test.go`，扩展 host stream test helper

**Interfaces:**
- Consumes: `runStreamForward`、`streamChunkRewriter.Write`、`streamChunkRewriter.Flush`
- Produces: SSE-only `emitRewritten` helper that emits one owned batch per host read

- [x] **Step 1: Write failing emit-count tests**

Use a host response with exact `Content-Type: text/event-stream` and one host read containing many complete tiny SSE events. Count `MethodHostStreamEmit` callbacks and assert the baseline emits one callback per rewritten fragment. Add a raw `application/json` control case that records each existing raw payload boundary.

- [x] **Step 2: Run emit tests in red state**

```text
go test . -run 'Test.*Stream.*Emit.*Batch' -count=1
```

Expected: SSE case reports multiple emit callbacks before the batching implementation.

- [x] **Step 3: Add SSE-only batch helper**

Implement:

```go
func emitRewritten(chunks [][]byte, batch bool, emit func([]byte) error) error
```

When `batch` is false, emit each chunk in order. When true, concatenate non-empty chunks into one owned byte slice and emit once. Set `batch` only when `mime.ParseMediaType` returned `text/event-stream`. Use the helper for normal writes and flushes. Do not batch raw JSON, WebSocket messages or malformed passthrough paths.

- [x] **Step 4: Verify ordering, ownership and allocation**

```text
go test . -run 'Test.*Stream.*Emit' -count=20
go test -race ./...
go test . -run '^$' -bench 'Benchmark.*Stream.*Emit' -benchmem -benchtime=500ms -count=7
```

Expected: SSE output bytes are identical and ordered, emit calls drop for tiny SSE events, raw JSON control boundaries remain unchanged, and returned batches remain valid after later writes.

- [x] **Step 5: Commit SSE batching**

```text
git add main.go main_test.go
git commit -m "perf: batch rewritten SSE emits per host read"
```

---

### Task 9: Harden Release Version And Platform ABI Gates

**Files:**
- Modify: `.github/scripts/package-release.go`
- Modify: `.github/scripts/package-release_test.go`
- Create: `.github/scripts/check-release-compatibility.go`
- Test: `.github/scripts/check-release-compatibility_test.go`
- Modify: `.github/workflows/build.yml`
- Modify: `Makefile` only when needed to pass explicit Linux compiler and deployment target

**Interfaces:**
- Consumes: `resolveVersion`、`normalizeReleaseVersion`、existing artifact matrix
- Produces: validated release versions and post-build platform ABI checks

- [x] **Step 1: Write failing packager tests**

Add table tests with expected acceptance:

```go
cases := []struct {
    input string
    ok    bool
}{
    {"v1.2.3", true},
    {"1.2.3", true},
    {"0.0.0-dev", true},
    {"v1.2.3-rc.1", true},
    {"vbeta", false},
    {"beta", false},
    {"v1", false},
}
```

- [x] **Step 2: Run packager tests in red state**

```text
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
```

Expected: invalid versions currently pass normalization.

- [x] **Step 3: Validate normalized versions**

Make `resolveVersion` strip one leading `v`, validate the complete normalized version against the accepted numeric `major.minor.patch` plus optional prerelease/build grammar, and return an error for `beta` or `vbeta`. Keep archive naming and checksum format unchanged.

- [x] **Step 4: Add workflow version gate**

In every release metadata step, reject a tag that begins with `v` but does not match the same release grammar before setting `VERSION` or building artifacts. Keep non-tag builds at `0.0.0-dev`.

- [x] **Step 5: Add Linux and macOS ABI checks**

Use a pinned Linux cgo toolchain or sysroot that produces GLIBC symbols no newer than `GLIBC_2.17`, and fail the job with `readelf --version-info` when a newer GLIBC symbol appears. Set the macOS build environment explicitly to deployment target `12.0`, then fail the job when `otool -l` reports a higher minimum.

The check must run against each Linux and macOS library before packaging. It must not alter CPA source or dependencies.

- [x] **Step 6: Run packaging tests and build checks**

```text
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test -race .github/scripts/package-release.go .github/scripts/package-release_test.go
```

For available local targets, run the existing c-shared build and inspect the generated artifact with the platform-native symbol tool. CI-only checks remain in workflow and must fail closed.

- [x] **Step 7: Commit release gates**

```text
git add .github/scripts/package-release.go .github/scripts/package-release_test.go .github/workflows/build.yml Makefile
git commit -m "fix: validate release versions and platform ABI baselines"
```

---

### Task 10: Make Local Smoke Readiness Belong To Its Process

**Files:**
- Modify: `.github/scripts/smoke-local.go`
- Create: `.github/scripts/smoke-local_test.go`

**Interfaces:**
- Consumes: `startCPA`、`waitReady`、`stopCPA`、`runCase`
- Produces: port preflight, process-aware readiness, error-returning shutdown and condition-based waits

- [x] **Step 1: Write failing helper tests**

Test pure helpers for:

```go
if err := checkPortAvailable("127.0.0.1:0"); err != nil {
    t.Fatal(err)
}
// A readiness poll must return an early process-exit error instead of waiting for timeout.
// An unsupported graceful signal must trigger the documented kill fallback.
```

Use a local test listener and a short-lived test process. Do not start CPA or contact an external endpoint from unit tests.

- [x] **Step 2: Run smoke helper tests in red state**

```text
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
```

Expected: helper symbols or current process-aware behavior are absent, and the current readiness path cannot observe a supplied process exit.

- [x] **Step 3: Add port preflight and process-aware readiness**

Before `startCPA`, bind and release `127.0.0.1:<port>` to reject an already occupied port. Pass `*cpaProcess` to readiness polling. Each poll must first select on `proc.waitDone`; if CPA exited, return `earlyExitError` immediately. Only accept a successful `/v1/models` response after this process has started.

- [x] **Step 4: Replace fixed startup and shutdown waits**

Return from `startCPA` immediately after successful `cmd.Start`; let `waitReady` provide the readiness condition. Change `stopCPA` to return an error, propagate it from `runCase`, treat unsupported `os.Interrupt` or `SIGTERM` as explicit fallback conditions, and wait on `waitDone` instead of sleeping a fixed duration when the process has exited.

- [x] **Step 5: Run helper tests and compile the script**

```text
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=20
go vet .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
go run .github/scripts/smoke-local.go
```

Expected: the last command fails only when the required live smoke environment variables are absent, with the existing explicit error messages. No live endpoint is contacted by unit tests.

- [x] **Step 6: Commit smoke tooling**

```text
git add .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
git commit -m "fix: make smoke readiness process-aware"
```

---

### Task 11: Full TDD Verification And Scope Audit

**Files:**
- Test all modified files
- Do not modify CPA source or module cache

**Interfaces:**
- Consumes: all Task 1 through Task 10 changes
- Produces: verified plugin tree, benchmark evidence and explicit external-blocker report

- [x] **Step 1: Run all focused tests**

```text
go test . -run 'Test(StreamChunk|SSERewriter|ContentType|Colonless|BOM|PluginRegister|LifecycleConfig|YAML|PluginRegistration|Gemini|ABI|CInt|PluginResponseLength|MultiData|MightContain|RestoreResponse|.*OriginalRequest.*Retained|RunStreamForward|.*Stream.*Emit)' -count=20
go test .github/scripts/package-release.go .github/scripts/package-release_test.go .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=20
```

- [x] **Step 2: Run repository correctness gates**

```text
go test ./...
go vet ./...
go test -race -count=1 ./...
go test -count=10 ./...
go test -gcflags=all=-d=checkptr=2 ./...
```

Expected: all pass. Any failure must be fixed before performance results are accepted.

- [x] **Step 3: Run packaging and build gates**

```text
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test -race .github/scripts/package-release.go .github/scripts/package-release_test.go
make build-windows-amd64
```

Run available Linux cross-build and artifact inspection commands from `CLAUDE.md`. Do not run `go test ./.github/scripts` because that package layout is explicitly unsupported.

- [x] **Step 4: Re-run performance measurements**

Run the same stream memory, SSE emit, response restore and escaped-text benchmarks used before the fixes. Record `ns/op`, `B/op`, `allocs/op`, retained heap bytes and emit call counts. Confirm no benchmark improvement comes from dropping bytes or changing output boundaries.

- [x] **Step 5: Run live smoke only when credentials exist**

```text
make smoke-local
```

Run this only when both `CPA_SMOKE_API_KEY` and `CPA_SMOKE_CPA_BIN` are set. Otherwise record it as skipped with the exact missing variable, not as a pass.

- [x] **Step 6: Audit the final diff**

```text
git diff --check
git status --short
git diff --name-only
```

Confirm only plugin repository files changed. Specifically verify no path under the CLIProxyAPI module cache, no CPA source checkout, and no unrelated source or documentation changes were edited.

- [x] **Step 7: Final report**

Report completed plugin fixes, measured performance changes, tests that passed, live smoke status, and these unmodified external blockers:

- async plugin stream lease and unload ordering;
- route decision handoff across reconfigure;
- count-token method routing or host callback;
- host-owned `caller_scope` generation and propagation;
- universal raw chunk boundary metadata.

Do not describe an external blocker as fixed by this plugin-only change set.

## Execution Record

- Tasks 1 through 10 were implemented with focused RED tests, minimal production changes, and passing focused regressions.
- Task 11 focused tests ran 20 rounds and passed.
- Repository tests, vet, race, repeated tests, and checkptr passed.
- Performance measurements preserved the existing stream improvements: raw JSON restore stayed at 27 allocations per operation, and complete SSE batches stayed at 3 allocations per operation.
- Windows amd64 and Linux amd64 c-shared artifacts were built locally. Linux required only `GLIBC_2.2.5` and `GLIBC_2.3.2`, below the `GLIBC_2.17` baseline. Both local `0.4.4` archives contain only the library and `LICENSE`, with valid sha256sum-format checksums.
- Live smoke was skipped because `CPA_SMOKE_API_KEY` and `CPA_SMOKE_CPA_BIN` were not set.
- The async plugin lease and unload ordering, route decision handoff across reconfigure, count-token routing or callback, host-owned `caller_scope` propagation, and universal raw chunk boundary metadata remain external CPA blockers and were not modified.
