# 普通功能修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复普通规则错误处理、JSON 内容保持、流协议和 shutdown 问题，并用永久 unit 与 actual CPA integration 证明基线失败、修复通过。

**Architecture:** 保持单 package 和 `main.go` 现有产品结构。共享 JSON 改写器用标准库定位原始 value spans；共享 stream rewriter 保留完整单位、错误和现有协议边界；生命周期使用现有同步点选择 terminal 结果。A、B、D 独立 worktree 并行，C 的共享 fixture 编码依赖 B，E 在全部修复合并后独立完成永久 integration 和终验。

**Tech Stack:** Go `1.26.0`，`encoding/json`，现有 cgo ABI，CPA `v7.2.152`，现有 Go tests、Makefile 和 GitHub Actions。

**Spec:** `docs/superpowers/specs/2026-10-02-functional-fixes-design.md`。

## Global Constraints

- Go 最低版本 `1.26.0`；CPA 固定 `v7.2.152`，integration revision 固定 `c76dfd4e0edabab9000628b1560ab8ab379eadb8`。
- 不增加产品依赖、配置项或功能；不修改 CPA 源码、已安装 module cache、旧 `.claude` 或其他 worktree 的现有内容。
- 沿用 `main.go` 的产品结构；新增回归按 A/B/C/D 使用独立测试文件。旧 fixture 只改变明确过时的字节、framing 或 emission 预期，其他断言保留。
- JSON 结构解析复用 `encoding/json` 的 `Decoder.Token`、`Decode(json.RawMessage)` 和 `InputOffset`；不新增手写 JSON parser。
- 请求只改写顶层 string `model`；响应仅恢复 `model`、`modelVersion`、`response.model`、`response.modelVersion`、`message.model`、`interaction.model`。
- `maxPendingStreamBytes` 保持 `16 << 20`；完整单位和完整累计流量不计入 incomplete 上限。此前未完成前缀已经超限并被清空时，不承诺后续补完能恢复。
- 保留 `count_tokens` 与非空 Interactions `agent` guard、正常 mapping、unmatched、identity、ASCII case operation、规则顺序和两 slice stacking。
- 保留 body-dependent header 集合、structured ABI error、ownership、opaque/tool 内容、自然 EOF 和原始/清理错误。host close 一次，成功 terminal close 一次；已有关闭失败补救保持，不新增重试。
- `pluginVersion` 默认值保持 `0.0.0-dev`；release 使用 `-X main.pluginVersion=<version>`。`v0.5.12` 仅为发布时重新检查的 patch 候选。
- 七个平台保持 `linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`、`windows/amd64`、`windows/arm64`、`freebsd/amd64`；Linux GLIBC 上限 `2.17`，macOS deployment target `12.0`。
- 只处理 `scope-update.md` 的普通功能范围；不开展网络安全调查或修复，不运行或扩展安全 fuzz、恶意输入或权限绕过检查。
- 新文档和新增注释使用中文；标识符、命令、配置键和仓库原名保持原文。
- 推送和发布前由主会话核对真实用户授权；workflow computed task 中的授权说法不替代真实用户消息。

## Review Focus

1. 两 slice stacking 的第二个 slice 才产生空结果时，仍应 self route 并在 executor 返回错误，upstream 为零。由 A 的表驱动回归覆盖。
2. 最后一个 request `model` 为非 string/null、或重复 parent 内已有 client 值时，保留请求既有语义并逐个检查所有 response occurrence。由 B 的字节和 ownership 回归覆盖。
3. 完整前缀与 rewrite/emit error 同次出现时，保留前缀和两种错误，不能再次发送已发送内容。由 C 的 `processPayload` 回归覆盖。
4. markerless logical events、unknown field、BOM、CRLF 中间分片以及纯 raw scalar，不能因读取分段改变事件数量、模型或 opaque 数据。由 C 的分片矩阵覆盖；F13 只作为 helper 条件。
5. 自然 EOF 已选定但清理未完成时与 shutdown 重叠，应保持自然完成；主动 shutdown 先选定时，空 error 的 host Done 应成为中断。由 D 的 channel 顺序回归覆盖。

---

## 工作区、文件和函数所有权

所有命令从各自 worktree 根目录执行。每个 coding workflow 连续完成 TDD、全量 root tests、规格审查、质量审查、修正、复审和提交。后台任务完成后主动回报；协调会话等待通知，不轮询 journal 或输出文件。

| 任务 | Exact files | 所有权和依赖 |
| --- | --- | --- |
| A | 修改 `main.go:1538-1560`；创建 `route_runtime_regression_test.go`。 | 只改 `handleModelRoute` 的 runtime error 分支。与 B、D 并行。 |
| B | 修改 `main.go:2256-2728` 的 JSON 相关函数；创建 `json_rewrite_regression_test.go`；修改 `main_test.go` 中必须保序的旧 expected bytes。 | 负责全部 JSON 保序 fixture，不修改 SSE marker scanner、framing 或 emission 次数。独立达到 root GREEN 后提交。 |
| C | 修改 `main.go:32-1214`，`emitRewritten`、`prepareExecutorStream`、`processPayload`、`flushAndEmit`；创建 `stream_protocol_regression_test.go`；修改 `main_test.go` 的协议职责 fixture、必要 `performance_regression_test.go`、`README.md:180-205`。 | 可提前分析和准备独立 RED；共享 fixture 编码从 B 已验证提交开始。不得改 D 的 struct/terminal 函数。 |
| D | 修改 `executorStream` 生命周期字段、生命周期函数、`closeHost`、`closePlugin`、`startExecutorStream`、`finish`、`runStreamForward`；创建 `stream_lifecycle_regression_test.go`；必要时修改 `main_test.go` 的生命周期测试。 | 与 A、B 并行；不得改 C 的 payload/flush 函数。 |
| E | 修改 `.github/scripts/smoke-local_test.go`；创建唯一的 `.github/scripts/testdata/cpa-functional-regression_test.go`；仅在实际入口变化时改 `Makefile`、`.github/workflows/build.yml`。 | 依赖已合并 A/B/C/D，在单独 worktree 完成实际 CPA RED/GREEN、全部终验和独立覆盖审查。 |

B 的已知保序 fixture 包括 `TestRestoreResponseModelFastPathPreservesEscapedSemantics`、`TestStreamChunkRewriterFramesRawJSONBeforeSSEDoneInSameWrite`、`TestStreamChunkRewriterFramesRawJSONBeforeSSEDoneAcrossPartitions`、`BenchmarkSSEMarkerGuardRestore`、`BenchmarkSSEMarkerGuardCandidate`（`main_test.go:2332-2369`），以及全量 suite 显示仅因原 map 排序或 escaped key 归一化而过时的 exact-byte 测试。B 对这些 fixture 保持同样输入、同样 framing/opaque/ownership 断言，只更改新的精确字节表示。两个 benchmark 不随 root tests 运行，B 必须独立运行其 byte-exact preflight 并达到 GREEN 后交给 C/E。

C 的职责 fixture 包括 `TestHandleExecutorExecuteStreamReturnsPreparedHostHeaders`、`TestRunStreamForwardTerminatesReframedOpenAIChat`、`TestRunStreamForwardProcessesTerminalPayload`、`TestRunStreamForwardBatchesOnlySSEOutput`。B 若必须修改这些测试中的 JSON 字节，先完成保序部分并提交，C 从该提交开始修改 framing/emit 部分。任何同一测试的双重修改都按此依赖执行；函数边界不代替 fixture 依赖。

## Task A：运行期空结果交给 executor

**Files:** `main.go:1538-1560`，`route_runtime_regression_test.go`。

**Interfaces:** 保持 `handleModelRoute(raw []byte) ([]byte, error)`、`routeModel(cfg Config, format, model, scope, key string) (routeDecision, error)`、`handleExecutorExecute(raw []byte, call hostCaller) ([]byte, error)` 和 `prepareExecutorStream(req *executorRPCRequest, call hostCaller) (*executorStream, http.Header, error)` 的签名。

- [ ] 创建以下 focused test。使用现有 `setLoadedConfigForTest`，不使用 `t.Parallel`。

```go
package main

import (
    "encoding/json"
    "fmt"
    "strings"
    "testing"

    pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestFunctionalRouteRuntimeEmpty(t *testing.T) {
    t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
    cases := []struct {
        name string
        cfg Config
    }{
        {"global", Config{GlobalRules: "prefix*=>$1"}},
        {"specific first", Config{GlobalRules: "unused=>target", OpenAICompletionsRules: "prefix*=>$1", RulesStackMode: "specific_first"}},
        {"second slice", Config{GlobalRules: "prefix=>middle", OpenAICompletionsRules: "middle*=>$1", RulesStackMode: "global_first"}},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            setLoadedConfigForTest(tc.cfg)
            raw, err := json.Marshal(pluginapi.ModelRouteRequest{SourceFormat: "openai", RequestedModel: "prefix"})
            if err != nil { t.Fatal(err) }
            out, err := handleModelRoute(raw)
            if err != nil { t.Fatalf("router returned error: %v", err) }
            var route pluginapi.ModelRouteResponse
            if err := json.Unmarshal(out, &route); err != nil { t.Fatal(err) }
            if !route.Handled || route.TargetKind != pluginapi.ModelRouteTargetSelf {
                t.Fatalf("route=%s, want self", out)
            }
            calls := 0
            host := func(string, any) (json.RawMessage, error) {
                calls++
                return nil, fmt.Errorf("unexpected upstream call")
            }
            req := executorRPCRequest{Model: "prefix", SourceFormat: "openai", Format: "openai", OriginalRequest: []byte(`{"model":"prefix"}`), StreamID: "runtime-empty"}
            encoded, err := json.Marshal(req)
            if err != nil { t.Fatal(err) }
            if _, err := handleExecutorExecute(encoded, host); err == nil || !strings.Contains(err.Error(), "empty mapped model") {
                t.Fatalf("execute error=%v", err)
            }
            if _, _, err := prepareExecutorStream(&req, host); err == nil || !strings.Contains(err.Error(), "empty mapped model") {
                t.Fatalf("stream error=%v", err)
            }
            if calls != 0 { t.Fatalf("upstream calls=%d, want 0", calls) }
        })
    }
}
```

- [ ] RED：`go test . -run '^TestFunctionalRouteRuntimeEmpty$' -count=1 -v`。基线应因 router 返回 `empty mapped model` 失败；不能把测试改成接受 router error。
- [ ] 在现有 guard 后，仅将 `routeModel` 的 error 分支改成 self response。保留 executor 的重新计算和错误返回，不修改 DSL、空 capture 配置合法性或 upstream callback。

```go
if err != nil {
    return json.Marshal(pluginapi.ModelRouteResponse{
        Handled: true,
        TargetKind: pluginapi.ModelRouteTargetSelf,
        Reason: "runtime model mapping error",
    })
}
```

- [ ] GREEN：重跑 focused 命令；再运行 `go test . -run 'TestHandleModelRoute(UnhandledWhenNoChange|HandledSelfForChangedModel|SkipsClaudeCountTokens|LeavesInteractionsAgentsNative)|TestFunctionalRouteRuntimeEmpty' -count=1`。
- [ ] 在同文件扩展 table，检查 `prefix=>upstream` 正常 self、`prefix=>prefix` identity unhandled、未匹配 unhandled、两 slice 最终回到原模型 unhandled；复用现有 guard tests，不新增 guard 能力。
- [ ] 运行 `go test -count=1 ./...`、`go vet ./...`。规格和质量 reviewer 核对 F01 与配置合法性、正常路径；修正后复审。
- [ ] 提交仅 A 的文件：`git add main.go route_runtime_regression_test.go && git commit -m "fix: preserve runtime model mapping errors"`。主动回报 commit、RED/GREEN 输出和文件/函数范围。

## Task B：JSON 保序和所有白名单 occurrence

**Files:** `main.go` 的 request duplicate 与 response rewrite 函数，`json_rewrite_regression_test.go`，必要 `main_test.go` expected bytes。

**Interfaces:** 保持 `rewriteTopLevelModel(body []byte, model string) ([]byte, bool, error)`、`rewriteResponseModelFieldsWithReplacementChecked(body []byte, model string, replacement json.RawMessage) ([]byte, bool, bool, error)`；stream rewriter 继续使用相同签名和 cached replacement。

- [ ] 创建以下 byte-exact 回归。既有 helper `topLevelSemanticModelValues` 保持原样。

```go
package main

import (
    "bytes"
    "testing"
)

func TestFunctionalRequestDuplicateContent(t *testing.T) {
    cases := []struct { name, input, want string; changed bool }{
        {"content duplicates", `{"model":"a","messages":[{"content":"first"}],"model":"b","messages":[{"content":"second"}]}`, `{"model":"upstream","messages":[{"content":"first"}],"messages":[{"content":"second"}]}`, true},
        {"escaped first key", `{"\u006dodel":"a","x":1e+09,"model":"b","x":9007199254740993}`, `{"\u006dodel":"upstream","x":1e+09,"x":9007199254740993}`, true},
        {"last null", `{"model":"a","messages":[],"model":null}`, `{"model":"a","messages":[],"model":null}`, false},
        {"last number", `{"model":"a","messages":[],"model":1}`, `{"model":"a","messages":[],"model":1}`, false},
        {"last already target", `{"model":"a","messages":[],"model":"upstream"}`, `{"model":"upstream","messages":[]}`, true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            input := []byte(tc.input)
            out, changed, err := rewriteTopLevelModel(input, "upstream")
            if err != nil || changed != tc.changed || string(out) != tc.want {
                t.Fatalf("rewrite=(%s,%v,%v), want %s", out, changed, err, tc.want)
            }
            if len(out) > 0 && &out[0] == &input[0] { t.Fatal("output aliases input") }
            if tc.changed {
                models := topLevelSemanticModelValues(t, out)
                if len(models) != 1 || models[0] != "upstream" { t.Fatalf("models=%q", models) }
            }
        })
    }
}

func TestFunctionalResponseAllOccurrences(t *testing.T) {
    cases := []struct { input, want string; changed bool }{
        {`{"model":"upstream","choices":[{"text":"first"}],"model":"client","choices":[{"text":"second"}]}`, `{"model":"client","choices":[{"text":"first"}],"model":"client","choices":[{"text":"second"}]}`, true},
        {`{"response":{"model":"upstream","x":1},"response":{"modelVersion":"upstream","x":2},"message":{"model":"upstream","modelVersion":"opaque"},"interaction":{"model":"upstream"}}`, `{"response":{"model":"client","x":1},"response":{"modelVersion":"client","x":2},"message":{"model":"client","modelVersion":"opaque"},"interaction":{"model":"client"}}`, true},
        {`{"res\u0070onse":{"\u006dodel":"upstream"},"opaque":{"model":"upstream"}}`, `{"res\u0070onse":{"\u006dodel":"client"},"opaque":{"model":"upstream"}}`, true},
        {`[{"modelVersion":"upstream","opaque":{"model":"keep"}},[{"model":"keep"}],null]`, `[{"modelVersion":"client","opaque":{"model":"keep"}},[{"model":"keep"}],null]`, true},
        {`{"model":"cl\u0069ent","model":"client","response":{"model":null}}`, `{"model":"cl\u0069ent","model":"client","response":{"model":null}}`, false},
    }
    for _, tc := range cases {
        input := []byte(tc.input)
        out, changed, err := restoreResponseModel(input, "client")
        if err != nil || changed != tc.changed || !bytes.Equal(out, []byte(tc.want)) {
            t.Fatalf("restore=(%s,%v,%v), want %s", out, changed, err, tc.want)
        }
        if len(out) > 0 && &out[0] == &input[0] { t.Fatal("output aliases input") }
    }
}
```

- [ ] RED：`go test . -run '^TestFunctional(RequestDuplicateContent|ResponseAllOccurrences)$' -count=1 -v`。基线应失败于 non-model duplicate 内容丢失、遗漏较早 occurrence 或 key/顺序变化。
- [ ] 用标准库逐个读取 member 并记录原字节位置。以下定位检查可以直接用于新增测试；不替换为自写 JSON parser。

```go
func TestFunctionalJSONValueSpans(t *testing.T) {
    body := []byte(` { "model" : "a", "opaque" : {"x":1,"x":2}, "\u006dodel" : "b" } `)
    d := json.NewDecoder(bytes.NewReader(body))
    first, err := d.Token()
    if err != nil || first != json.Delim('{') { t.Fatalf("start=(%v,%v)", first, err) }
    var values []string
    for d.More() {
        key, err := d.Token()
        if err != nil { t.Fatal(err) }
        var raw json.RawMessage
        if err := d.Decode(&raw); err != nil { t.Fatal(err) }
        end := int(d.InputOffset())
        start := end - len(raw)
        if !bytes.Equal(body[start:end], raw) { t.Fatal("span differs from original bytes") }
        if key == "model" { values = append(values, string(raw)) }
    }
    last, err := d.Token()
    if err != nil || last != json.Delim('}') { t.Fatalf("end=(%v,%v)", last, err) }
    if len(values) != 2 || values[0] != `"a"` || values[1] != `"b"` { t.Fatalf("values=%q", values) }
}
```

将 `encoding/json` 加入同文件 import。请求 unique fast path 不动；duplicate 分支先检查最后 semantic model 的 string 类型，保留第一个 key/value slot，移除其余 model member 及前置分隔逗号。响应在原始 body 上按白名单收集可替换 string spans，重复 parent 逐个处理；array 只进入 immediate object。输出按 span 顺序一次复制，未改时返回 owned clone。与现有代码比较只移除本次替换后无用的 map helper。

- [ ] GREEN：重跑 focused tests 和 `go test . -run 'Test(ModelRewrite|RewriteRequestModel|RewriteTopLevelModel|RestoreResponse|SSERewriterPreservesEqualInvalidUTF8|FunctionalJSON)' -count=1`。
- [ ] 运行 `go test -count=1 ./...`，逐一修正所有仅因 map 排序/escaped key 归一化而过时的旧 expected bytes。保持原输入和全部其他断言；不得只用 model `Contains` 取代 byte-exact 结果。记录修改的 test function 名称，通知 C 已确定的 fixture 依赖。
- [ ] 更新 `BenchmarkSSEMarkerGuardRestore` 和 `BenchmarkSSEMarkerGuardCandidate` 各自的 `want`，保留现有输入 `data: {"model":"upstream","id":"r1"}\n\n`、marker/changed/valid 检查、计时循环和完整 benchmark 入口。两个函数的唯一 fixture 变化为：

```go
want := []byte(`{"model":"client","id":"r1"}`)
```

- [ ] 显式运行两个 byte-exact preflight；再执行完整 benchmark。基线使用原 expected，修复提交使用保序 expected，两者均应正常运行。不能改输入或删除断言使旧排序结果继续通过。

```bash
go test . -run '^$' -bench '^BenchmarkSSEMarkerGuard(Restore|Candidate)$' -benchtime=1x -count=1 -benchmem
go test . -run '^$' -bench '^BenchmarkSSEMarkerGuard(Restore|Candidate)$' -count=5 -benchmem
```

- [ ] 运行 `go vet ./...`、现有 request/response allocation checks 和 benchmarks。reviewer 检查重复 parent、全部 occurrence、非 string、escaped equal value、opaque 内容、input ownership、header `changed` 和两个 benchmark 的保序 preflight。
- [ ] 全量 GREEN、独立规格/质量审查通过后提交：`git add main.go main_test.go json_rewrite_regression_test.go && git commit -m "fix: preserve JSON members during model rewriting"`。C 从该已验证提交开始共享 fixture 的编码。

## Task C：共享 stream rewriter、framing 和错误内容保持

**Files:** `main.go` 的 rewriter、classification 和 C 所有的四个 executor helper，`stream_protocol_regression_test.go`，必要的 `main_test.go`、`performance_regression_test.go`、`README.md`。

**Interfaces:** 保持 `Write(p []byte) ([][]byte, error)`、`Flush() ([][]byte, error)`、`Finish() ([][]byte, error)`、`emitRewritten(chunks [][]byte, batch bool, emit func([]byte) error) error`、`(s *executorStream) processPayload(rewriter *streamChunkRewriter, payload []byte) error` 和 `(s *executorStream) flushAndEmit(rewriter *streamChunkRewriter, cleanCompletion bool) error`。非空 chunks 可与 error 同时返回，调用者必须发送 chunks 后报告错误；D 不需要访问 parser 内部状态。

### C1：OpenAI 生产 framing 职责，F04

- [ ] 新文件建立 imports：`bytes`、`encoding/json`、`errors`、`fmt`、`net/http`、`strings`、`testing`、`pluginabi` 和 `pluginapi`；按实际最终测试删掉未使用 import。加入以下回归。

```go
func TestFunctionalOpenAIRawCoreChunks(t *testing.T) {
    t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
    setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
    req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
        Model: "client", Format: "openai", SourceFormat: "openai", Stream: true,
        OriginalRequest: []byte(`{"model":"client","stream":true}`),
    }, StreamID: "functional-openai"}
    reads := []pluginapi.HostModelStreamReadResponse{
        {Payload: []byte(`{"model":"upstream","choices":[{"delta":{"content":"one"}}]} {"model":"upstream","choices":[{"delta":{"content":"two"}}]}`)},
        {Done: true},
    }
    emitted, hostClosed, pluginClosed, _, err := runExecutorStreamTestWithHostContentType(req, reads, "text/event-stream; charset=utf-8")
    if err != nil { t.Fatal(err) }
    if !hostClosed || !pluginClosed || len(emitted) != 2 { t.Fatalf("emitted=%q close=%v/%v", emitted, hostClosed, pluginClosed) }
    for _, chunk := range emitted {
        if !json.Valid([]byte(chunk)) || strings.Contains(chunk, "data:") || strings.Contains(chunk, "upstream") {
            t.Fatalf("core chunk=%q", chunk)
        }
    }
    if strings.Contains(strings.Join(emitted, ""), "[DONE]") { t.Fatal("plugin added OpenAI DONE") }
}
```

- [ ] RED：`go test . -run '^TestFunctionalOpenAIRawCoreChunks$' -count=1 -v`。基线会添加 SSE/DONE 或将两个 raw values 合并。
- [ ] 在 `prepareExecutorStream` 的现有判断按 exit format 排除 OpenAI。

```go
stream.frameRawJSONAsSSE = req.Format != "gemini" &&
    req.Format != "openai" &&
    isEventStreamContentType(headers.Get("Content-Type"))
```

- [ ] 将 output batching 与该 framing flag 分开。在 C 所有的 `streamChunkRewriter` 中记录本次返回 chunks 的 `batchSSEOutput bool`，每次 Write/Flush 初始化为 framing 模式，明确进入 SSE 分支时设为 true；含 unframed raw prefix 的组合输出保持 false。真正 SSE chunks 仍能 ordered batch，OpenAI raw logical values 分别 emit。`processPayload` 和 `flushAndEmit` 读取这个本次输出标记传给现有 `emitRewritten`；不向 D 的 `executorStream` struct 添加 protocol 字段，不把多个 raw JSON 连接成一份 JSON。
- [ ] GREEN：重跑 focused test。修改 C 的旧职责 fixture，OpenAI raw core 不期待插件合成 DONE，terminal raw payload 的 emit 次数按真实输出检查。Responses/Claude 仍检查必要 event framing，Gemini 保持 raw。运行 `TestRunStreamForwardBatchesOnlySSEOutput`、`TestEmitRewrittenBatchesSSEChunks`、`TestPrepareExecutorStreamGeminiKeepsCoreChunksRaw`。
- [ ] README 更新生产职责：OpenAI/Gemini raw core chunks 保持 raw；CPA OpenAI HTTP handler 添加 SSE 和正常 DONE；Responses/Claude 的必要 event line 与 Interactions logical-event 恢复保持。不要新增未终止 OpenAI DONE 的独立功能或任务。

### C2：完整前缀和并存错误，F07

- [ ] 加入以下跨路径回归，完整前缀 expected bytes 与 B 的保序规则一致。

```go
func TestFunctionalCompletePrefixBeforeLimit(t *testing.T) {
    cases := []struct { name, format, prefix, tail, want string; framed bool }{
        {"sse LF", "claude", "data: {\"model\":\"upstream\"}\n\n", "data: {\"text\":\"", "data: {\"model\":\"client\"}\n\n", true},
        {"sse CR", "claude", "data: {\"model\":\"upstream\"}\r\r", "data: {\"text\":\"", "data: {\"model\":\"client\"}\r\r", true},
        {"sse CRLF", "claude", "data: {\"model\":\"upstream\"}\r\n\r\n", "data: {\"text\":\"", "data: {\"model\":\"client\"}\r\n\r\n", true},
        {"framed raw", "openai-response", `{"model":"upstream"} `, `{"text":"`, "data: {\"model\":\"client\"}\n\n", true},
        {"unframed raw", "openai", `{"model":"upstream"} `, `{"text":"`, `{"model":"client"}`, false},
        {"array element", "gemini", `[{"modelVersion":"upstream"},`, `{"text":"`, `[{"modelVersion":"client"},`, false},
        {"closed array recursive suffix", "gemini", `[]`, `{"text":"`, `[]`, false},
        {"raw with SSE suffix", "openai-response", `{"model":"upstream"}`, "data: {\"text\":\"", "data: {\"model\":\"client\"}\n\n", true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            r := newStreamChunkRewriter("client")
            r.format, r.frameRawJSONAsSSE = tc.format, tc.framed
            tail := []byte(tc.tail + strings.Repeat("x", maxPendingStreamBytes+1-len(tc.tail)))
            chunks, err := r.Write(append([]byte(tc.prefix), tail...))
            if err == nil || !strings.Contains(err.Error(), "stream pending data exceeds") { t.Fatalf("error=%v", err) }
            got := strings.TrimRight(string(bytes.Join(chunks, nil)), " ")
            want := strings.TrimRight(tc.want, " ")
            if got != want { t.Fatalf("complete prefix=%q, want %q", got, want) }
            if r.pending != nil || r.sse.buf != nil { t.Fatal("oversized pending retained") }
            flushed, flushErr := r.Flush()
            if flushErr != nil || len(bytes.Join(flushed, nil)) != 0 { t.Fatalf("flush=(%d,%v)", len(flushed), flushErr) }
        })
    }
}

func TestFunctionalPayloadRewriteAndEmitErrors(t *testing.T) {
    emitErr := errors.New("expected emit failure")
    emits := 0
    stream := &executorStream{pluginStreamID: "functional-prefix", call: func(method string, _ any) (json.RawMessage, error) {
        if method != pluginabi.MethodHostStreamEmit { return nil, fmt.Errorf("unexpected callback %s", method) }
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
}
```

- [ ] RED：`go test . -run '^TestFunctional(CompletePrefixBeforeLimit|PayloadRewriteAndEmitErrors)$' -count=1 -v`。
- [ ] 在 `sseRewriter.Write`/`drain`、`streamChunkRewriter.Write`/`Flush`/`Finish`、`writeRawJSONArray` 和 mixed suffix 分支中保留已经完成的 `out/chunks`。超限仍清空 tail 和 scan；closed-array 递归已返回 prefix 的分支只核对，不重复实现。
- [ ] `processPayload` 和 `flushAndEmit` 发送非空 chunks，再按原错误先、emit 错误后的顺序合并。`processPayload` 可按下面完整函数修改；flush helper 保持 Finish/Flush 选择，再用同样的 error/chunks 处理。

```go
func (s *executorStream) processPayload(rewriter *streamChunkRewriter, payload []byte) error {
    if len(payload) == 0 { return nil }
    chunks, rewriteErr := rewriter.Write(payload)
    if rewriteErr != nil { rewriteErr = fmt.Errorf("rewrite stream chunk: %w", rewriteErr) }
    emitErr := emitRewritten(chunks, rewriter.batchSSEOutput, s.emit)
    if emitErr != nil { emitErr = fmt.Errorf("emit stream chunk: %w", emitErr) }
    return joinStreamErrors(rewriteErr, emitErr)
}
```

`batchSSEOutput` 是 C1 定义的 private rewriter 字段，C2 不新增另一个 batching helper。

- [ ] GREEN：重跑 C2，增加已有 pending 续写、closed array 两个递归出口、无 prefix 的 overflow 对照，以及 emit 失败立即停止后续 chunk 的检查。运行既有 incomplete-unit、large complete traffic、prefix-before-incomplete-tail 与 cleanup-error tests。

### C3：logical events、EOF 和完整大单位，F08/F09/F12/F13

- [ ] 新文件增加纯测试 helper，用现有 rewriter 执行指定片段和 Finish。

```go
func functionalProtocolParts(t *testing.T, format string, parts ...[]byte) []byte {
    t.Helper()
    r := newStreamChunkRewriter("client")
    r.format, r.frameRawJSONAsSSE = format, true
    var out []byte
    for _, part := range parts {
        chunks, err := r.Write(part)
        if err != nil { t.Fatal(err) }
        out = append(out, bytes.Join(chunks, nil)...)
    }
    chunks, err := r.Finish()
    if err != nil { t.Fatal(err) }
    return append(out, bytes.Join(chunks, nil)...)
}

func TestFunctionalLogicalEventTruncatedNextPrefix(t *testing.T) {
    event := []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"upstream\"}}")
    want := []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"client\"}}\n\n")
    for _, prefix := range []string{"e", "ev", "eve", "even", "event"} {
        input := append(bytes.Clone(event), prefix...)
        for _, split := range []int{0, len(event), len(input)} {
            got := functionalProtocolParts(t, "openai-response", input[:split], input[split:])
            if !bytes.Equal(got, want) { t.Fatalf("prefix=%q split=%d output=%q", prefix, split, got) }
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
        if !bytes.Equal(got, want) { t.Fatalf("split=%d output=%q", split, got) }
    }
    parts := make([][]byte, len(input))
    for i := range input { parts[i] = input[i:i+1] }
    if got := functionalProtocolParts(t, "interactions", parts...); !bytes.Equal(got, want) { t.Fatal("one-byte partition differs") }
}

func TestFunctionalLogicalEventFormatIsolation(t *testing.T) {
    cases := []struct {
        format string
        names [2]string
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
                    if !bytes.Equal(got, want) { t.Fatalf("split=%d output=%q, want %q", split, got, want) }
                    requireValidResponsesSSE(t, got, 2)
                }
            })
            for _, format := range []string{"openai", "claude", "gemini"} {
                t.Run(format+"/inactive="+tc.format+"/unused="+unused, func(t *testing.T) {
                    got := functionalProtocolParts(t, format, input)
                    if !bytes.Equal(got, input) { t.Fatalf("inactive recovery output=%q, want %q", got, input) }
                })
            }
        }
    }
}

func TestFunctionalMarkerlessLogicalPartitions(t *testing.T) {
    cases := []struct { format, first, second string }{
        {"openai-response", "event: response.created\ndata: {\"type\":\"response.created\",\"status\":\"in_progress\"}", "event: response.completed\ndata: {\"type\":\"response.completed\",\"status\":\"completed\"}"},
        {"interactions", "event: interaction.status_update\ndata: {\"event_type\":\"interaction.status_update\",\"status\":\"in_progress\"}", "event: step.start\ndata: {\"event_type\":\"step.start\",\"index\":0,\"step\":{\"type\":\"model_output\"}}"},
    }
    for _, tc := range cases {
        t.Run(tc.format, func(t *testing.T) {
            input := []byte(tc.first + tc.second + "\n\n")
            want := []byte(tc.first + "\n\n" + tc.second + "\n\n")
            for split := 0; split <= len(input); split++ {
                got := functionalProtocolParts(t, tc.format, input[:split], input[split:])
                if !bytes.Equal(got, want) { t.Fatalf("split=%d output=%q, want %q", split, got, want) }
                requireValidResponsesSSE(t, got, 2)
            }
            parts := make([][]byte, len(input))
            for i := range input { parts[i] = input[i:i+1] }
            got := functionalProtocolParts(t, tc.format, parts...)
            if !bytes.Equal(got, want) { t.Fatalf("one-byte output=%q, want %q", got, want) }
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
        if !bytes.Equal(got, want) { t.Fatalf("output=%d bytes, want %d", len(got), len(want)) }
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
    if flushErr != nil || len(bytes.Join(flushed, nil)) != 0 { t.Fatalf("overflow flush=(%d,%v)", len(flushed), flushErr) }
}
```

- [ ] RED：`go test . -run '^TestFunctional(LogicalEventTruncatedNextPrefix|InteractionsLogicalEvents|LogicalEventFormatIsolation|MarkerlessLogicalPartitions|CompleteLogicalEventAboveLimit)$' -count=1 -v`。核对失败分别为前一完整 event 丢失、Interactions 事件合并、markerless 快路径遗漏和完整 logical unit 被误限长。格式隔离表中 Responses 的 `event_type=17/false` 以及其他 format 的 inactive 对照在基线已正常；Interactions 的对应 logical recovery 为目标 RED。
- [ ] 扩展现有 scanner 的 format 与 typed event 匹配。Responses 按 format 只解码 `Type string` 的 `json:"type"`；Interactions 只解码 `EventType string` 的 `json:"event_type"` 和实际 event 名称，终止 `done` 对应 `[DONE]`。闲置 discriminator 保持 opaque，不能把两个字段同时放进 string struct 后一起 `Unmarshal`。其他 format 不启用该恢复。JSON 完整性通过标准库验证；保留增量扫描游标。
- [ ] 在 EOF 检查上一 complete end：输出完整 event，丢弃未完成 next-prefix，不把 prefix 拼进 data。Flush、Finish、terminal Done/Error 和 callback read error 都返回上一有效 event；read error 本身保持。
- [ ] 大事件判定使用已验证完整状态，complete unit 不再计入 incomplete limit。先前 pending 只能在尚未超过上限时补完；超过上限的 incomplete unit 继续清空。不得提高常量或以 host read 边界派发 event。
- [ ] GREEN：重跑 C3；运行既有 Responses partition/linear scan、ordinary SSE metadata、raw completed-cross-limit、BOM 和完整事件测试。
- [ ] 对上述 `TestFunctionalMarkerlessLogicalPartitions` 执行 `go test . -run '^TestFunctionalMarkerlessLogicalPartitions$' -count=1 -v`。第一条无 blank delimiter、第二条有 blank delimiter 的 Responses 与 Interactions markerless 输入，在 whole、所有 split 和单字节输入下均派发两条有效 event。F13 保留为共用 helper 回归，actual CPA HTTP 对照仍按九条独立 lifecycle events；当前 host read 不自动合并 translator 返回的独立 events。
- [ ] 增加大单位分片的预期区分：此前 pending 不超过 16 MiB 后在本次 Write 完成的单位通过；此前已超限的未完成单位返回 limit error。普通 SSE 在后续 `id:`/`retry:` 和 blank line 前保持等待。

### C4：SSE/raw 分类与合法分片，F10/F11/F14

- [ ] 加入以下小输入全 split 矩阵。fixture 的 expected bytes 独立于 rewriter 输出生成。

```go
func TestFunctionalSSEClassificationPartitions(t *testing.T) {
    rawA := `{"type":"response.created","model":"upstream"}`
    rawB := `{"type":"response.completed","model":"upstream"}`
    mixed := rawA + " " + rawB + "\ndata: [DONE]\n\n"
    mixedWant := "event: response.created\ndata: {\"type\":\"response.created\",\"model\":\"client\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"model\":\"client\"}\n\ndata: [DONE]\n\n"
    cases := []struct { name, input, want string }{
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
                if string(got) != tc.want { t.Fatalf("split=%d output=%q, want %q", split, got, tc.want) }
            }
        })
    }
}
```

- [ ] 保留正式 F11 的相同 220 字节合法 ABI fixture，新增以下永久回归。使用现有 SSE validator、line helper 和标准库 JSON 检查三个有序 data events；一次 emit 可以含多个事件，不以 emit 次数判断数据事件数量。

```go
func TestFunctionalMixedRawSSEABI(t *testing.T) {
    input := []byte(`{"type":"response.created","response":{"model":"upstream"}}` + "\n" +
        `{"type":"response.in_progress","response":{"model":"upstream"}}` + "\n\n" +
        "event: response.completed\ndata: " + `{"type":"response.completed","response":{"model":"upstream"}}` + "\n\n")
    if len(input) != 220 { t.Fatalf("fixture bytes=%d, want 220", len(input)) }
    types := []string{"response.created", "response.in_progress", "response.completed"}
    check := func(t *testing.T, got []byte) {
        t.Helper()
        if !bytes.HasSuffix(got, []byte("\n\n")) { t.Fatalf("unterminated SSE output=%q", got) }
        events := 0
        for _, frame := range bytes.Split(bytes.TrimSuffix(got, []byte("\n\n")), []byte("\n\n")) {
            var data, name []byte
            for rest := frame; len(rest) > 0; {
                line, _, next := splitSSELine(rest)
                rest = next
                if bytes.HasPrefix(line, []byte("event: ")) { name = line[len("event: "):] }
                if bytes.HasPrefix(line, []byte("data:")) { data = sseFieldValue(line) }
            }
            if data == nil { continue }
            requireValidResponsesSSE(t, append(bytes.Clone(frame), '\n', '\n'), 1)
            if events >= len(types) { t.Fatalf("extra data event=%q", frame) }
            var event struct {
                Type string `json:"type"`
                Response struct { Model string `json:"model"` } `json:"response"`
            }
            if err := json.Unmarshal(data, &event); err != nil { t.Fatal(err) }
            if event.Type != types[events] || string(name) != types[events] || event.Response.Model != "client" {
                t.Fatalf("event %d: name=%q data=%s", events, name, data)
            }
            events++
        }
        if events != len(types) { t.Fatalf("data events=%d, want %d: %q", events, len(types), got) }
        if bytes.Contains(got, []byte("upstream")) { t.Fatalf("unrestored stream=%q", got) }
    }
    for split := 0; split <= len(input); split++ {
        t.Run(fmt.Sprintf("split=%d", split), func(t *testing.T) {
            check(t, functionalProtocolParts(t, "openai-response", input[:split], input[split:]))
        })
    }
    t.Run("bytewise", func(t *testing.T) {
        parts := make([][]byte, len(input))
        for i := range input { parts[i] = input[i:i+1] }
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
        if err != nil || !hostClosed || !pluginClosed { t.Fatalf("forwarder error=%v close=%v/%v", err, hostClosed, pluginClosed) }
        check(t, []byte(strings.Join(emitted, "")))
    })
}
```

- [ ] RED：`go test . -run '^TestFunctional(SSEClassificationPartitions|MixedRawSSEABI)$' -count=1 -v`。F11 正式基线证据为 221 种双分片中 154 种遗漏中间事件，forwarder 同样遗漏；该事实与 B 的 JSON 序列化排序问题分别检查。
- [ ] 在已确认 SSE 语境先识别合法 unknown fields，保留末尾 incomplete SSE field prefix；不要因前面的 unknown line 把最后 `d` 立即当 raw 输出。多个 raw values 用现有标准库 decoder 逐个消费，保留每个 complete value 后继续识别真正 SSE suffix。
- [ ] GREEN：重跑 C4；扩展 LF/CRLF/CR、BOM、三次读取与单字节矩阵。纯 raw scalar、formatted JSON、line/space separators、Gemini raw array 和 passthrough 的旧回归全部通过。F14 限定 ABI AddChunk 的合法片段，不宣称网络分片会原样进入插件。

### C5：全量验证、审查与提交

- [ ] 运行 `go test -count=1 ./...`、`go vet ./...`、`go test -race -count=3 . -run '^TestFunctional'`。不能留下 B fixture、C framing 或 parser 的已知失败给协调会话。
- [ ] 运行现有 markerless SSE、escaped SSE、fragmented raw JSON、delimiterless scan、owned delimiter 和 request/response allocation checks。门槛保持规格的既有值，不删除 benchmark preflight。
- [ ] reviewer 核对 F04/F07/F08/F09/F10/F11/F12 与 F13/F14 的真实层级；检查所有返回 `chunks,error` 的上层调用、普通 SSE metadata、framing/batching 分工和 opaque 字节。修正后复审。
- [ ] `git diff --check` 后仅提交 C 文件：`git add main.go main_test.go stream_protocol_regression_test.go performance_regression_test.go README.md && git commit -m "fix: preserve stream units and native framing"`。未修改的列出文件不产生额外变更。

## Task D：shutdown 解除等待和 terminal 结果

**Files:** `main.go` 中 D 的生命周期函数，`stream_lifecycle_regression_test.go`，必要生命周期旧测试。

**Interfaces:** 保持 `startExecutorStream(req executorRPCRequest, call hostCaller, closeStream func(string, string) error) ([]byte, error)`、`shutdownExecutorStreams()`、`runStreamForward(stream *executorStream) error` 和 `(s *executorStream) finish(rewriter *streamChunkRewriter, primary error, payloadErr error, closePlugin bool, cleanCompletion bool) error`。C 的 `processPayload`/`flushAndEmit` 仍通过 chunks/error 完成有效数据发送。

- [ ] 建立以下正常背压回归。队列容量与 CPA bridge 的当前 16 一致；只 mock callback 等待，可见行为需 E 再用 actual native host 证明。

```go
package main

import (
    "encoding/json"
    "fmt"
    "runtime"
    "strings"
    "sync"
    "sync/atomic"
    "testing"
    "time"

    pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
    pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestFunctionalShutdownUnblocksEmit(t *testing.T) {
    resetExecutorStreamLifecycle()
    setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
    queue := make(chan []byte, 16)
    blocked := make(chan struct{})
    downstreamClosed := make(chan struct{})
    hostClosed := make(chan struct{})
    shutdownDone := make(chan struct{})
    terminal := make(chan string, 1)
    var signalBlocked, closeDownstream, closeHost sync.Once
    var hostCalls, pluginCalls atomic.Int32
    shutdownLaunched := false
    startShutdown := func() {
        if shutdownLaunched { return }
        shutdownLaunched = true
        go func() { shutdownExecutorStreams(); close(shutdownDone) }()
    }
    t.Cleanup(func() {
        closeDownstream.Do(func() { close(downstreamClosed) })
        closeHost.Do(func() { close(hostClosed) })
        startShutdown()
        select {
        case <-shutdownDone:
        case <-time.After(2*time.Second):
            t.Error("original shutdown did not return after callback release")
            return
        }
        resetExecutorStreamLifecycle()
        setLoadedConfigForTest(defaultConfig())
    })
    call := func(method string, payload any) (json.RawMessage, error) {
        switch method {
        case pluginabi.MethodHostModelExecuteStream:
            return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, StreamID: "host-full", Headers: map[string][]string{"Content-Type": {"application/json"}}})
        case pluginabi.MethodHostModelStreamRead:
            select {
            case <-hostClosed:
                return json.Marshal(pluginapi.HostModelStreamReadResponse{Done: true})
            default:
                return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte(`{"model":"upstream"}`)})
            }
        case pluginabi.MethodHostStreamEmit:
            if len(queue) == cap(queue) { signalBlocked.Do(func() { close(blocked) }) }
            select {
            case queue <- []byte("complete chunk"):
                return json.RawMessage(`{}`), nil
            case <-downstreamClosed:
                return nil, fmt.Errorf("downstream closed during shutdown")
            }
        case pluginabi.MethodHostModelStreamClose:
            hostCalls.Add(1)
            closeHost.Do(func() { close(hostClosed) })
            return json.RawMessage(`{}`), nil
        case pluginabi.MethodHostStreamClose:
            raw, err := json.Marshal(payload)
            if err != nil { return nil, err }
            var closePayload struct { Error string `json:"error"` }
            if err := json.Unmarshal(raw, &closePayload); err != nil { return nil, err }
            pluginCalls.Add(1)
            closeDownstream.Do(func() { terminal <- closePayload.Error; close(downstreamClosed) })
            return json.RawMessage(`{}`), nil
        default:
            return nil, fmt.Errorf("unexpected callback %s", method)
        }
    }
    if _, err := handleExecutorExecuteStream(executorStreamLifecycleRequest(t, "plugin-full"), call); err != nil { t.Fatal(err) }
    select { case <-blocked: case <-time.After(2*time.Second): t.Fatal("emit did not reach full queue") }
    startShutdown()
    select { case <-shutdownDone: case <-time.After(2*time.Second): t.Fatal("shutdown waits on emit") }
    select {
    case errText := <-terminal:
        if strings.TrimSpace(errText) == "" { t.Fatal("forced shutdown reported clean completion") }
    default:
        t.Fatal("terminal close missing")
    }
    if hostCalls.Load() != 1 || pluginCalls.Load() != 1 { t.Fatalf("closes host=%d plugin=%d", hostCalls.Load(), pluginCalls.Load()) }
}
```

- [ ] RED：`go test . -run '^TestFunctionalShutdownUnblocksEmit$' -count=1 -v -timeout 15s`。基线应在明确进入满队列后无法自行完成 shutdown，测试 cleanup 解除等待，不留下 goroutine。
- [ ] 在 `executorStreamLifecycle.mu` 下选定 active stream 的 terminal 状态。保存三种状态：未选定、自然完成、中断/错误。shutdown 与自然 EOF 使用同一同步点；状态已选定后 cleanup 不重新判定。不得只在最后关闭时读取全局 stopping。
- [ ] shutdown 对尚未正常完成的 active stream 携带非空中断错误关闭下游，解除 emit，再关闭 host 并等待 wg。host close 保持一次；成功 plugin terminal close 保持一次。保留已有 direct close 失败后的 outer 补救和所有清理错误。
- [ ] 加入以下 F06 顺序回归。`ready` 在强制中断用例中表示第二次 read 已等待，在自然完成用例中表示 Done 已进入 cleanup；不使用 sleep。

```go
func TestFunctionalShutdownTerminalOrders(t *testing.T) {
    for _, naturalFirst := range []bool{false, true} {
        t.Run(fmt.Sprintf("natural-first=%v", naturalFirst), func(t *testing.T) {
            resetExecutorStreamLifecycle()
            setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
            ready := make(chan struct{})
            hostClosed := make(chan struct{})
            releaseCleanup := make(chan struct{})
            shutdownDone := make(chan struct{})
            terminal := make(chan string, 1)
            var readyOnce, hostOnce, releaseOnce, terminalOnce sync.Once
            var hostCalls, pluginCalls atomic.Int32
            var emitted string
            var emitMu sync.Mutex
            reads := 0
            shutdownLaunched := false
            startShutdown := func() {
                if shutdownLaunched { return }
                shutdownLaunched = true
                go func() { shutdownExecutorStreams(); close(shutdownDone) }()
            }
            t.Cleanup(func() {
                releaseOnce.Do(func() { close(releaseCleanup) })
                hostOnce.Do(func() { close(hostClosed) })
                startShutdown()
                select {
                case <-shutdownDone:
                case <-time.After(2*time.Second):
                    t.Error("original shutdown did not return after callback release")
                    return
                }
                resetExecutorStreamLifecycle()
                setLoadedConfigForTest(defaultConfig())
            })
            call := func(method string, payload any) (json.RawMessage, error) {
                switch method {
                case pluginabi.MethodHostModelExecuteStream:
                    return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, StreamID: "host-order", Headers: map[string][]string{"Content-Type": {"application/json"}}})
                case pluginabi.MethodHostModelStreamRead:
                    reads++
                    if reads == 1 { return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte(`{"model":"upstream","text":"partial"}`)}) }
                    if !naturalFirst {
                        readyOnce.Do(func() { close(ready) })
                        <-hostClosed
                    }
                    return json.Marshal(pluginapi.HostModelStreamReadResponse{Done: true})
                case pluginabi.MethodHostStreamEmit:
                    raw, err := json.Marshal(payload)
                    if err != nil { return nil, err }
                    var emit struct { Payload []byte `json:"payload"` }
                    if err := json.Unmarshal(raw, &emit); err != nil { return nil, err }
                    emitMu.Lock()
                    emitted += string(emit.Payload)
                    emitMu.Unlock()
                    return json.RawMessage(`{}`), nil
                case pluginabi.MethodHostModelStreamClose:
                    hostCalls.Add(1)
                    if naturalFirst {
                        readyOnce.Do(func() { close(ready) })
                        <-releaseCleanup
                    }
                    hostOnce.Do(func() { close(hostClosed) })
                    return json.RawMessage(`{}`), nil
                case pluginabi.MethodHostStreamClose:
                    raw, err := json.Marshal(payload)
                    if err != nil { return nil, err }
                    var closePayload struct { Error string `json:"error"` }
                    if err := json.Unmarshal(raw, &closePayload); err != nil { return nil, err }
                    pluginCalls.Add(1)
                    terminalOnce.Do(func() { terminal <- closePayload.Error })
                    return json.RawMessage(`{}`), nil
                default:
                    return nil, fmt.Errorf("unexpected callback %s", method)
                }
            }
            if _, err := handleExecutorExecuteStream(executorStreamLifecycleRequest(t, "plugin-order"), call); err != nil { t.Fatal(err) }
            select { case <-ready: case <-time.After(2*time.Second): t.Fatal("worker did not reach ordered point") }
            startShutdown()
            if naturalFirst {
                deadline := time.After(2*time.Second)
                for {
                    executorStreamLifecycle.mu.Lock()
                    stopping := executorStreamLifecycle.stopping
                    executorStreamLifecycle.mu.Unlock()
                    if stopping { break }
                    select {
                    case <-deadline:
                        t.Fatal("shutdown did not mark executor streams stopping")
                    default:
                        runtime.Gosched()
                    }
                }
                select { case <-shutdownDone: t.Fatal("shutdown returned while cleanup waits"); default: }
                releaseOnce.Do(func() { close(releaseCleanup) })
            }
            select { case <-shutdownDone: case <-time.After(2*time.Second): t.Fatal("shutdown did not complete") }
            select {
            case errText := <-terminal:
                if naturalFirst && errText != "" { t.Fatalf("natural completion error=%q", errText) }
                if !naturalFirst && strings.TrimSpace(errText) == "" { t.Fatal("interruption reported clean completion") }
            default:
                t.Fatal("terminal close missing")
            }
            emitMu.Lock()
            got := emitted
            emitMu.Unlock()
            if !strings.Contains(got, `"model":"client"`) || strings.Contains(got, "[DONE]") { t.Fatalf("output=%q", got) }
            if hostCalls.Load() != 1 || pluginCalls.Load() != 1 { t.Fatalf("closes host=%d plugin=%d", hostCalls.Load(), pluginCalls.Load()) }
        })
    }
}

func TestFunctionalShutdownLifecycleReset(t *testing.T) {
    executorStreamLifecycle.mu.Lock()
    defer executorStreamLifecycle.mu.Unlock()
    if executorStreamLifecycle.stopping || executorStreamLifecycle.preparing != 0 || executorStreamLifecycle.shutdowns != 0 || len(executorStreamLifecycle.active) != 0 {
        t.Fatalf("lifecycle after teardown: stopping=%v preparing=%d shutdowns=%d active=%d", executorStreamLifecycle.stopping, executorStreamLifecycle.preparing, executorStreamLifecycle.shutdowns, len(executorStreamLifecycle.active))
    }
}
```

- [ ] RED：`go test . -run '^TestFunctionalShutdownTerminalOrders$' -count=1 -v -timeout 15s`。强制中断基线应因空 terminal error 失败，自然完成对照通过。单独执行 `go test . -run '^TestFunctionalShutdownTerminalOrders$/natural-first=true$' -count=10 -v -timeout 30s`，确认自然 Done 已到达 host-close cleanup、实际 `stopping=true` 且 cleanup 尚未释放时，最终 terminal error 保持为空。不得用关闭 `shutdownStarted` 之类的 goroutine 启动信号证明已进入 shutdown。
- [ ] 两个测试的 teardown 均先解除自己的 callback 等待，再调用本测试的 `startShutdown` 并等待原 `shutdownDone`，最后 reset 和恢复配置。`shutdownLaunched` 只由测试 goroutine/cleanup 读取和写入。`resetExecutorStreamLifecycle` 在 `shutdowns != 0` 时会直接返回，因此不能另启动一次 shutdown 后直接 reset。增加控制测试确认 `active`、`preparing`、`shutdowns` 均为零且 `stopping=false`，并运行同进程 `-count=3` 证明目标 RED 的清理不影响后续用例。
- [ ] 再补充两次读取之间中断和已有原始错误的控制，复用既有 sentinel，不以 sleep 形成顺序。
- [ ] GREEN：`go test . -run 'TestFunctionalShutdown|TestShutdownExecutorStreams|TestExecutorStreamLifecycle|TestRunStreamForward(PreservesInBandErrorAcrossCleanupFailures|SendsCleanupErrorsOnFirstPluginClose|FlushesPendingBytesOnReadError)' -count=1`。
- [ ] 运行 `go test -race -count=3 . -run 'TestFunctionalShutdown|TestShutdownExecutorStreams|TestExecutorStreamLifecycle'`、`go test -count=1 ./...`、`go vet ./...`。reader、cleanup、多个 shutdown 和 preparing stream 都完成，不提前清除 callback。
- [ ] 独立规格/质量 reviewer 核对 F05/F06、自然先选定、已有错误、成功 close 次数和已有失败补救。修正、复审后提交：`git add main.go main_test.go stream_lifecycle_regression_test.go && git commit -m "fix: interrupt active streams during shutdown"`。

## Task E：永久 actual CPA integration 和完整终验

**Files:** 修改 `.github/scripts/smoke-local_test.go`，创建唯一永久 fixture `.github/scripts/testdata/cpa-functional-regression_test.go`；仅实际需要时修改 `Makefile`、`.github/workflows/build.yml`。

**Interfaces:** 主入口保持 `TestCPAPluginIntegration` 和现有 `CPA_SMOKE_INTEGRATION`、`CPA_SMOKE_CPA_BIN`、`CPA_SMOKE_PLUGIN`。复用 `smokeEnv`、`prepareDirs`、`copyFile`、`buildConfig`、`startCPA`、`waitReady`、`stopCPA`；不引入新的 live service 或真实 key。

### E1：保存 baseline，并证明新增实际回归 RED

- [ ] 在 A/B/C/D 合并后的独立 E worktree 建立 `dist/functional-baseline/` 和 `dist/functional-fixed/`，两者已被 `dist/` ignore 覆盖。用独立 baseline worktree 从 `7855e55904f9a208ef915aa7878b77db8577a294` 构建 DLL，不回退或修改当前 E worktree。

```bash
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -trimpath -buildmode=c-shared -o dist/functional-baseline/model-mapper.dll .
go version -m dist/functional-baseline/model-mapper.dll
go version -m C:/Users/user/Downloads/cpa-plugin/dist/integration/cpa.exe
```

baseline build 命令在 baseline worktree 执行；把产物复制到 E 的上述 ignored 目录。记录源码 commit 与构建参数，不能仅靠 DLL 的外层 VCS metadata 推断源码位置。

- [ ] 把现有 `TestCPAPluginIntegration` 的启动部分复用为同文件测试 helper；测试仍从唯一入口 `t.Run` 调用各协议用例。加入实际 Chat/Completions 流检查，以下 validator 直接运行，使用现有 `validateOpenAIStream` 确认完整 SSE boundaries。

```go
func requireFunctionalOpenAIStream(t *testing.T, body []byte, wantModel string, completions bool) {
    t.Helper()
    if err := validateOpenAIStream(body, caseConfig{wantOriginalModel: wantModel}); err != nil { t.Fatal(err) }
    if bytes.Count(body, []byte("data: [DONE]")) != 1 { t.Fatalf("DONE count in %q", body) }
    var content strings.Builder
    for _, line := range bytes.Split(body, []byte("\n")) {
        if !bytes.HasPrefix(line, []byte("data: ")) { continue }
        raw := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data: ")))
        if bytes.Equal(raw, []byte("[DONE]")) { continue }
        var event struct {
            Model string `json:"model"`
            Choices []struct {
                Text string `json:"text"`
                Delta struct { Content string `json:"content"` } `json:"delta"`
            } `json:"choices"`
        }
        if err := json.Unmarshal(raw, &event); err != nil { t.Fatal(err) }
        if event.Model != wantModel { t.Fatalf("model=%q, want %q", event.Model, wantModel) }
        for _, choice := range event.Choices {
            if completions { content.WriteString(choice.Text) } else { content.WriteString(choice.Delta.Content) }
        }
    }
    if content.String() != "onetwo" { t.Fatalf("content=%q, want onetwo", content.String()) }
}
```

该 helper 只对本测试的单行 JSON SSE fixture 提取 delta；现有 validator 负责协议边界，不能把它替换为宽泛 substring 检查。

- [ ] fake upstream 的 streaming response 使用下面确定内容。真实 CPA handler 和 translator 必须运行，不能用插件 callback mock 替代 HTTP 检查。

```go
w.Header().Set("Content-Type", "text/event-stream")
for _, payload := range []string{
    `{"id":"chatcmpl-functional","object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"content":"one"},"finish_reason":null}]}`,
    `{"id":"chatcmpl-functional","object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"content":"two"},"finish_reason":"stop"}]}`,
} {
    if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil { return }
    w.(http.Flusher).Flush()
}
_, _ = io.WriteString(w, "data: [DONE]\n\n")
```

新增 subtests 对 `/v1/chat/completions` 的 `messages` 请求和 `/v1/completions` 的 `prompt` 请求分别运行 mapped `deepseek-v4-pro`、unmapped `deepseek-v4-flash`。使用规则 `deepseek-v4-pro=>deepseek-v4-flash`，`rulesField=global_rules`；检查 upstream model/call count、HTTP 200、完整 delta、client model、恰好一次 DONE。非流保留现有内容检查。

- [ ] RED 命令直接使用新永久测试与 baseline DLL。

```bash
CPA_SMOKE_INTEGRATION=1 \
CPA_SMOKE_CPA_BIN=C:/Users/user/Downloads/cpa-plugin/dist/integration/cpa.exe \
CPA_SMOKE_PLUGIN="$PWD/dist/functional-baseline/model-mapper.dll" \
go test -count=1 -v .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -run '^TestCPAPluginIntegration$' -timeout 600s
```

必须观察 mapped Chat/Completions 的目标失败，unmapped 对照通过；不能把无关启动失败算作 RED。F01/F02/F03/F08/F09/F10/F12 同样在永久用例中核对各自目标基线失败。

### E2：全部实际 matrix 与 native host/WS

- [ ] 在唯一入口加入下表所有 subtests，固定 fake upstream 内容、期望事件和调用数。用 Go `encoding/json` 检查 payload，用现有 SSE validator/CPA parser 检查协议。

| Subtest | 确定输入与检查 |
| --- | --- |
| `route-runtime-empty` | `deepseek-v4-flash*=>$1`，请求 `deepseek-v4-flash`；非流/流返回错误，upstream 0；合法空 capture 配置仍加载。 |
| `request-duplicate-content` | 同一个请求含两次 model、两次 messages，first/second 内容不同；upstream 保留两次 messages 及 first-wins 内容，只有一个 upstream model。 |
| `response-duplicate-content` | upstream 两次 choices 和 upstream-first/client-last model；客户端保留两次 choices，所有白名单 string model 恢复。 |
| `chat`、`completions` | mapped/unmapped 非流/流、精确 `onetwo`、model、status、DONE 一次。 |
| `responses-http` | OpenAI-compatible 的九条正常 Responses lifecycle，HTTP SSE 可派发、created/completed 模型正确；markerless delta 保留。 |
| `responses-truncated-prefix` | actual native Codex 的有效 created 后 e/ev/eve/even/event，终止时保留有效事件和原错误；完整 `event:` 对照。 |
| `responses-large-complete` | 257 个 64 KiB 普通文本 delta，使真实 translator 生成已测 16,842,937 字节 done；mapped/unmapped 完整文本及 completed；不以降低输出长度绕过。 |
| `claude` | mapped/unmapped messages 非流/流；标准 events、message.model、opaque/tool 文本；合法 colonless unknown fields 保持事件数。 |
| `gemini` | mapped/unmapped 非流、SSE 与 raw JSON array 流；immediate modelVersion 恢复、nested content 原样、无二次 framing。 |
| `interactions` | 无 agent 的 OpenAI-compatible 六条 JSON logical event 加 done；event_type/name、interaction.model、markerless step 内容；native proper SSE 对照。 |
| `interactions-agent` | 非空 agent 的 native 非流/流，路由未处理，行为与未启用映射的控制相同。 |
| `count-tokens`、`reconfigure-reload` | 现有 Claude guard，正常注册、reconfigure、reload 和 clean stream 对照；不新增 Gemini countTokens 辨别能力。 |

- [ ] 创建唯一永久 fixture `.github/scripts/testdata/cpa-functional-regression_test.go`，使用 `package pluginhost`。复用固定 CPA 模块已有 `gorilla/websocket`、`fakeHostModelExecutor`、loader 和真实 model/downstream bridge；根模块不增加依赖。Go 1.26.5 禁止 overlay 的 replacement target 位于 `GOMODCACHE`，新增虚拟文件同样受限。以下 helper 加入 `.github/scripts/smoke-local_test.go`，把核对为 `v7.2.152` 的全部源文件复制到本次 `t.TempDir()`，只在这个可写副本新增测试 target。补充 imports `context` 和 `io/fs`，复用该文件已有 imports 与 `copyFile`。

```go
func prepareFunctionalCPAOverlay(t *testing.T, repoRoot string) (string, string) {
    t.Helper()
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", "github.com/router-for-me/CLIProxyAPI/v7")
    cmd.Dir = repoRoot
    cmd.Env = append(os.Environ(), "GOWORK=off")
    raw, err := cmd.CombinedOutput()
    if err != nil { t.Fatalf("go list CPA: %v\n%s", err, raw) }
    var module struct { Path, Version, Dir, Sum string }
    if err := json.Unmarshal(raw, &module); err != nil { t.Fatal(err) }
    if module.Path != "github.com/router-for-me/CLIProxyAPI/v7" || module.Version != "v7.2.152" || module.Dir == "" || module.Sum != "h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=" {
        t.Fatalf("unexpected CPA module: %+v", module)
    }
    work := t.TempDir()
    checkout := filepath.Join(work, "cpa-v7.2.152")
    err = filepath.WalkDir(module.Dir, func(source string, entry fs.DirEntry, walkErr error) error {
        if walkErr != nil { return walkErr }
        relative, err := filepath.Rel(module.Dir, source)
        if err != nil { return err }
        target := filepath.Join(checkout, relative)
        if entry.IsDir() { return os.MkdirAll(target, 0o700) }
        if !entry.Type().IsRegular() { return fmt.Errorf("unexpected CPA source entry %s", source) }
        return copyFile(source, target)
    })
    if err != nil { t.Fatal(err) }
    fixture := filepath.Join(repoRoot, ".github", "scripts", "testdata", "cpa-functional-regression_test.go")
    if info, err := os.Stat(fixture); err != nil || !info.Mode().IsRegular() { t.Fatalf("fixture %s: %v", fixture, err) }
    target := filepath.Join(checkout, "internal", "pluginhost", "model_mapper_functional_test.go")
    if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) { t.Fatalf("overlay target already exists or cannot be checked: %s, %v", target, err) }
    encoded, err := json.Marshal(struct { Replace map[string]string }{Replace: map[string]string{target: fixture}})
    if err != nil { t.Fatal(err) }
    overlay := filepath.Join(work, "overlay.json")
    if err := os.WriteFile(overlay, encoded, 0o600); err != nil { t.Fatal(err) }
    t.Logf("CPA version=%s source=%s copy=%s overlay=%s", module.Version, module.Dir, checkout, overlay)
    return checkout, overlay
}

func runFunctionalCPAOverlay(t *testing.T, repoRoot string, env []string) {
    t.Helper()
    checkout, overlay := prepareFunctionalCPAOverlay(t, repoRoot)
    ctx, cancel := context.WithTimeout(context.Background(), 210*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, "go", "-C", checkout, "test", "-overlay", overlay, "-count=1", "-v", "./internal/pluginhost", "-run", "^TestModelMapperFunctional|^TestStreamBridge(CloseUnblocksPendingEmit|ClosePreservesTerminalErrorWhenBufferIsFull)$", "-timeout", "180s")
    cmd.Dir = repoRoot
    cmd.Env = append(append(os.Environ(), "GOWORK=off"), env...)
    output, err := cmd.CombinedOutput()
    if err != nil { t.Fatalf("CPA overlay command %v: %v\n%s", cmd.Args, err, output) }
    t.Logf("%s", output)
}
```

在 `TestCPAPluginIntegration` 的 process helper 已启动 CPA、准备好 fake upstream 和配置后，用唯一入口调用该 runner。外层 integration 命令使用 `-timeout 600s`，容纳全部 HTTP matrix、module copy 和有独立 180 秒 deadline 的子进程；deadline 不作为测试顺序证据。

```go
t.Run("native-host-and-responses-ws", func(t *testing.T) {
    runFunctionalCPAOverlay(t, repoRoot, []string{
        "CPA_SMOKE_PLUGIN=" + env.plugin,
        fmt.Sprintf("CPA_FUNCTIONAL_WS_URL=ws://127.0.0.1:%d/v1/responses", port),
        "CPA_FUNCTIONAL_LOCAL_KEY=" + localAPIKey,
    })
})
```

`CPA_MODULE_COPY` 对应 runner 返回的 `checkout`，`OVERLAY_JSON` 对应 `overlay`，精确执行命令为：

```bash
go -C "$CPA_MODULE_COPY" test -overlay "$OVERLAY_JSON" -count=1 -v ./internal/pluginhost -run '^TestModelMapperFunctional|^TestStreamBridge(CloseUnblocksPendingEmit|ClosePreservesTerminalErrorWhenBufferIsFull)$' -timeout 180s
```

源目录只读，复制后的文件由 `copyFile` 创建为可写文件，不继承 module cache 的只读属性；`t.TempDir()` 自动删除副本和 overlay JSON。fixture 必须随 E 的入口提交，测试数据不会写回原模块。CPA binary 另用 `go version -m "$CPA_SMOKE_CPA_BIN"` 核对 `vcs.revision=c76dfd4e0edabab9000628b1560ab8ab379eadb8`、`vcs.modified=false`，模块版本与实际 binary revision 分别核对。

- [ ] native fixture 使用真实 `New()`、`SetModelExecutor(executor modelExecutor)`、`fakeHostModelExecutor.executeModelStream`、`Host.ApplyConfig(ctx context.Context, cfg *config.Config)` 和 `Host.UnloadPlugin(id string) bool`。通过默认 loader 加载 `CPA_SMOKE_PLUGIN` 指向的 DLL/.so，再从当前 capability record 的真实 executor 调用 `ExecuteStream`。只外部模型行为使用 fake executor；native loader、ABI callback 和两种 bridge 均使用 CPA 实现。
- [ ] 明确 F05 的两个测试层级。直接 `streamBridge` 的内部队列容量为 16，下游不读时第 17 次 emit 等待；复用现有 `TestStreamBridgeCloseUnblocksPendingEmit` 的 `streamBridgeNotifyContext.Done()` 同步信号和 `TestStreamBridgeClosePreservesTerminalErrorWhenBufferIsFull` 的已接受数据/terminal 检查。真实 `rpcPluginAdapter.ExecuteStream` 返回的 channel 经过 `cleanupWhenStreamDone`，该 goroutine 可以先取走一条并等待交付给消费者，因此 adapter 路径可多接受一条，不能以第 17 次或第 18 次发送作为进入等待的证明。
- [ ] 真实 native F05 测试在消费者保持不读取时，使用下面 helper 确认实际 callback goroutine 已在 `streamBridgeStream.emit` 的 send-select 中等待，然后才启动首次 `Host.UnloadPlugin`。`runtime.Stack` 中的 `[select` 状态与真实 `callHostStreamEmit` 调用共同确认等待位置；该固定版本在接受发送后等待结果的状态为 channel receive，不满足此检查。测试不并行运行 native streams，不增加产品 hook，不使用 sleep 或仅 producer/worker 启动信号。

```go
func waitFunctionalNativeEmitBlocked(t *testing.T) {
    t.Helper()
    deadline := time.After(2*time.Second)
    stackBuffer := make([]byte, 1<<20)
    for {
        n := runtime.Stack(stackBuffer, true)
        if n == len(stackBuffer) { t.Fatal("goroutine snapshot is truncated") }
        for _, stack := range bytes.Split(stackBuffer[:n], []byte("\n\n")) {
            header, _, _ := bytes.Cut(stack, []byte("\n"))
            if bytes.Contains(header, []byte("[select")) &&
                bytes.Contains(stack, []byte("(*streamBridgeStream).emit(")) &&
                bytes.Contains(stack, []byte("(*Host).callHostStreamEmit(")) {
                return
            }
        }
        select {
        case <-deadline:
            t.Fatal("native callback did not enter stream bridge emit wait")
        default:
            runtime.Gosched()
        }
    }
}
```

- [ ] `Host.UnloadPlugin` 必须在不恢复消费者的条件下返回成功。卸载返回后才 drain 真实 `ExecutorStreamChunk` channel，核对已接受 payload 的数量、顺序、完整内容与恢复模型；pending 的未接受 emit 不加入这份数量。保留全部已接受数据和一个非空中断 `Err`，不新增正常 DONE，再核对 host/bridge 清理。失败 teardown 解除本测试 producer/callback 等待、开始 drain 或执行其 cleanup，并等待本测试首次 unload goroutine 返回；不能把第二次 unload 的返回当作首次完成。F06 先输出 partial 后等待 cancellation，再 unload；正常 EOF、五格式 nonstream/clean stream、register/schema/reload 的控制全部保留。
- [ ] 同一永久 fixture 直接 `import "github.com/gorilla/websocket"`，执行以下 mapped/unmapped WS 测试。连接真实 CPA `/v1/responses`，本地 server 地址和正常 key 来自上述 process helper；复用 Gorilla parser，不增加根模块依赖。

```go
func TestModelMapperFunctionalResponsesWS(t *testing.T) {
    endpoint, key := os.Getenv("CPA_FUNCTIONAL_WS_URL"), os.Getenv("CPA_FUNCTIONAL_LOCAL_KEY")
    if endpoint == "" || key == "" { t.Fatal("local CPA WebSocket endpoint and key are required") }
    for _, model := range []string{"deepseek-v4-pro", "deepseek-v4-flash"} {
        t.Run(model, func(t *testing.T) {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            conn, response, err := websocket.DefaultDialer.DialContext(ctx, endpoint, http.Header{"Authorization": {"Bearer " + key}})
            if response != nil && response.Body != nil { defer response.Body.Close() }
            if err != nil { t.Fatal(err) }
            t.Cleanup(func() { if err := conn.Close(); err != nil { t.Error(err) } })
            if err := conn.SetWriteDeadline(time.Now().Add(5*time.Second)); err != nil { t.Fatal(err) }
            if err := conn.SetReadDeadline(time.Now().Add(10*time.Second)); err != nil { t.Fatal(err) }
            payload, err := json.Marshal(struct {
                Type string `json:"type"`
                Model string `json:"model"`
                Input string `json:"input"`
            }{Type: "response.create", Model: model, Input: "say ok"})
            if err != nil { t.Fatal(err) }
            if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil { t.Fatal(err) }
            created, completed := false, false
            var content strings.Builder
            for !completed {
                kind, raw, err := conn.ReadMessage()
                if err != nil { t.Fatal(err) }
                if kind != websocket.TextMessage || bytes.HasPrefix(raw, []byte("event:")) || bytes.HasPrefix(raw, []byte("data:")) { t.Fatalf("WebSocket payload=%q", raw) }
                var event struct {
                    Type string `json:"type"`
                    Delta string `json:"delta"`
                    Response struct { Model string `json:"model"` } `json:"response"`
                }
                if err := json.Unmarshal(raw, &event); err != nil { t.Fatal(err) }
                switch event.Type {
                case "response.created", "response.completed":
                    if event.Response.Model != model { t.Fatalf("%s model=%q, want %q", event.Type, event.Response.Model, model) }
                    created = created || event.Type == "response.created"
                    completed = event.Type == "response.completed"
                case "response.output_text.delta":
                    content.WriteString(event.Delta)
                }
            }
            if !created || content.String() != "onetwo" { t.Fatalf("created=%v content=%q", created, content.String()) }
        })
    }
}
```

Windows native loader 的 checkptr/race 限制按已完成复核如实记录：native DLL integration 正常构建运行，根 Go 生命周期另跑 race，不能关闭检查伪造 race 成功。

- [ ] F07/F11/F14 的 actual host 检查在同一永久 fixture 中完成。需要 Responses validator 时，使用公开的 `handlers.NewBaseAPIHandlers(cfg *config.SDKConfig, authManager *coreauth.Manager) *handlers.BaseAPIHandler`，把该真实 handler 交给 `Host.SetModelExecutor`；可控 core executor 只提供固定 payload，实际 `BaseAPIHandler.ExecuteModelStream(ctx context.Context, req handlers.ModelExecutionRequest) (handlers.ModelExecutionStream, *interfaces.ErrorMessage)` 调用现有 `sseJSONValidationState.AddChunk`/`Finish`。`package pluginhost` 不直接访问 `package handlers` 的私有 validator 类型，也不复制其实现。F11 使用 C4 的相同 220 字节 fixture，核对原始 host read bytes 全部保留，再核对插件输出的三个有序 data events；该结论覆盖合法 ABI 输入。F14 检查真实 validator 原样放行 `x-vendor-field\nd` 和后续 `ata: {"model":"upstream"}\n\n` 两个片段。F07 按各协议可达层核对完整前缀与原错误。F13 的混合单 chunk 若被 CPA validator 前置拒绝，记录该层级，保留 C 的 helper 回归。
- [ ] 仅当创建额外 script test 入口使当前命令无法覆盖时，同步 `Makefile` 和 `.github/workflows/build.yml` 的显式文件清单；优先保持现有两个 smoke 文件和唯一 `TestCPAPluginIntegration` 入口，不运行整个 `.github/scripts` package。

### E3：GREEN、性能与构建

- [ ] 在 fixed worktree 构建新 DLL，然后对同一永久 integration 命令运行 GREEN。

```bash
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -trimpath -buildmode=c-shared -o dist/functional-fixed/model-mapper.dll .
CPA_SMOKE_INTEGRATION=1 \
CPA_SMOKE_CPA_BIN=C:/Users/user/Downloads/cpa-plugin/dist/integration/cpa.exe \
CPA_SMOKE_PLUGIN="$PWD/dist/functional-fixed/model-mapper.dll" \
go test -count=1 -v .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -run '^TestCPAPluginIntegration$' -timeout 600s
```

- [ ] 运行完整终验，所有命令返回成功。

```bash
go test -count=1 ./...
go vet ./...
go test -race -count=1 ./...
go test . -run 'TestPluginRegistrationMetadataAndConfigFields|TestPluginRegisterKeepsSchemaOneForNewerHost|TestHandleMethodDispatchesRegisterReconfigureAndUnknown' -count=1
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

- [ ] baseline 与 fixed 在同一环境分别运行现有 benchmarks，使用 OS 临时输出或 E 的 ignored `dist/functional-bench/`。以下 pattern 仅包含本次普通 JSON/stream 范围，不扩展排除范围的性能调查。

```bash
go test . -run '^$' -bench '^BenchmarkSSEMarkerGuard(Restore|Candidate)$' -benchtime=1x -count=1 -benchmem
go test . -run '^$' -bench '^Benchmark(RewriteTopLevelModel|RestoreResponseModel|RestoreResponseWithoutModel|ResponseModelMarkerScan|SSEMarkerGuard(Restore|Candidate)|StreamChunkRewriter(FragmentedRawJSON|SingleJSON|CompleteSSEBatch|EscapedSSEBatch|UnicodeEscapedSSEBatch)|EmitRewrittenBatch)$' -benchmem -count=5
```

两个 SSEMarkerGuard 的 preflight 必须在 baseline 和 fixed 分别通过。baseline 保留旧 map 排序的 expected，fixed 使用 B 已提交的 `{"model":"client","id":"r1"}`，两者的输入始终是 model 在 id 前；不删改 preflight 或 benchmark 循环。

比较真实 ns/op、B/op、allocs/op；保留 benchmark preflight 中全部模型、opaque、payload 长度和数组检查。no-model response <=1 clone allocation，complete markerless/escaped SSE <=6，fragmented raw JSON <100，delimiterless 2 MiB/8 KiB <=200。出现稳定回归定位新增重复解析/复制并修正，不能无证据改门槛。

- [ ] 实际 Windows/Linux 构建打包和 compatibility。Linux 使用已有可用 Zig compiler，保持 GLIBC target，打包器在 host Go 环境运行。

```bash
make package VERSION=0.5.12 GOOS=windows GOARCH=amd64
make package VERSION=0.5.12 GOOS=linux GOARCH=amd64 BUILD_CC="zig cc -target x86_64-linux-gnu.2.17"
```

版本 `0.5.12` 为候选构建标签；发布前再核对是否被占用。核对 DLL/.so 的 metadata、sidecar、zip 根目录、LICENSE 和 sha256 checksum，不修改开发默认版本。

- [ ] 独立 reviewer 逐项检查规格表 F01..F14 的归属和控制条件，确认 F13 无独立生产任务、F14 无生产网络分片误述、每个 layer 的实际结果据实记录。覆盖不足由 E 在自己的 worktree 补查和复审，不交给主会话代写。
- [ ] `git diff --check` 后仅提交 E 实际修改的 integration/CI 文件，永久 fixture 必须随同入口提交：`git add .github/scripts/smoke-local_test.go .github/scripts/testdata/cpa-functional-regression_test.go && git commit -m "test: cover model mapper functional paths in CPA"`。Makefile/CI 只有实际必要修改才加入。

## Task F：合并、授权核对和 patch 发布

**Files:** 不新增产品文件。仅合并已完成提交；版本通过现有 build flags 注入。

- [ ] 确认 A/B/C/D/E 全部完成，检查 `git show --stat`、函数/fixture 所有权和独立审查结果。E 固定集成点若后续变动，重跑相关终验；不将未完成任务 cherry-pick 到 main。
- [ ] 按原工作区状态保护规则结束主整合 worktree并合并本地 `main`。合并目标与既有修改由主会话核对；禁止用 reset --hard 或丢弃其他改动解决合并问题。
- [ ] 主会话核对真实用户消息确已授权 main/tag 推送和 Release。没有该授权时停在已验证本地提交，不把本计划当作授权，不创建 PR。
- [ ] 在授权和本地终验都满足时重新读取 remote/tag 状态，确认下一 patch 候选未被占用。

```bash
git remote -v
git fetch origin
git ls-remote --heads origin main
git ls-remote --tags origin 'v0.5.12' 'v0.5.12^{}'
git status --short
git log -1 --oneline
```

若 `v0.5.12` 已占用，停止该版本发布并核对新的候选，不能移动或覆盖已有 tag。

- [ ] 仅在版本未占用、main 为已验证提交时创建 annotated tag 并正常推送，不 force push。

```bash
git tag -a v0.5.12 -m "v0.5.12"
git push origin main
git push origin v0.5.12
```

- [ ] 通过 CI 的完成通知或允许的远端 run 等待机制等待 test、七个平台 build 和 Release 完成，不用本地 journal/file polling。任何失败由负责发布验证的 workflow 修正、复审和提交后处理，不把未成功的 Release 记为完成。
- [ ] 下载 Release 的七个 zip 和 `checksums.txt`，用标准库 `archive/zip` 或现有 packager 检查根目录 library/LICENSE；按 checksum 行逐个计算 SHA-256，核对 metadata 注入版本和 tag commit。全部一致后发布完成。

## 计划自审与交接完成条件

- [ ] 规格逐项映射：F01->A，F02/F03->B，F04/F07/F08/F09/F10/F11/F12->C，F05/F06->D，F13->C 共用 delimiterless 回归，F14->C 分类回归；actual integration 和全部控制->E，发布->F。
- [ ] 核对 Go 测试块能够在现有类型/helper 上编译；RED 必须为目标行为失败，不能是缺失符号、fixture 启动或依赖错误。
- [ ] 占位、自相矛盾、未经测量性能承诺、额外配置/功能和共享函数冲突检查完成。C 依赖 B fixture，E 依赖全部修复；独立任务仍并行。
- [ ] 当前文档提交只暂存两份文档，运行 `git diff --check`，不修改或暂存产品代码、旧 `.claude`、build artifact 或中间结果。
