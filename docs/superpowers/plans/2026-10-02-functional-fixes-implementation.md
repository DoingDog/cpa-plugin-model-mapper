# 普通功能修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复普通规则错误处理、JSON 内容保持、流协议和 shutdown 问题，并用永久 unit 与 actual CPA integration 证明基线失败、修复通过。

**Architecture:** 保持单 package 和 `main.go` 现有产品结构。共享 JSON 改写器用标准库定位原始 value spans；共享 stream rewriter 保留完整单位、错误和现有协议边界；生命周期使用现有同步点选择 terminal 结果。A、B、D 独立 worktree 并行，C 的共享 fixture 编码依赖 B。固定 C 最终审查提交 -> 本输入层级规格修正独立审查通过 -> G 有限接口核查、必要完整 TDD／审查／提交 -> E 全部永久 actual 验收 -> F 发布。本输入层级规格修正须独立审查通过后启动 G；G/E 永久验收未完成时 E/F 不放行。

**Tech Stack:** Go `1.26.0`，`encoding/json`，现有 cgo ABI，CPA `v7.2.152`，现有 Go tests、Makefile 和 GitHub Actions。

**Spec:** `docs/superpowers/specs/2026-10-02-functional-fixes-design.md`。

## Global Constraints

- Go 最低版本 `1.26.0`；CPA 固定 `v7.2.152`，integration revision 固定 `c76dfd4e0edabab9000628b1560ab8ab379eadb8`。
- 不增加产品依赖、配置项或功能；不修改 CPA 源码、已安装 module cache、旧 `.claude` 或其他 worktree 的现有内容。
- 沿用 `main.go` 的产品结构；新增回归按 A/B/C/D/G 使用独立测试文件。旧 fixture 只改变明确过时的字节、framing 或 emission 预期，其他断言保留。
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
| G | 修改 C 最终 HEAD 上共享 `streamChunkRewriter.Write/Flush/Finish`、`sseRewriter.Write/drain`、delimiterless scanner/reset/classification 的 F15 必要部分；创建 `stream_native_fields_regression_test.go`；必要协议／性能 fixture。 | 等 C 连续完成独立审查、修正、复审与提交，产品 reviewBASE 固定 `51b006438c1a0edd4263020297747f99a174c47c`；执行起点由协调者交付包含全部已整合修复的 HEAD，规格修正独立审查通过后有限核查 G2。只消费 C 的 `batchSSEOutput`、framing/batching 和 chunks+error，不改 C executor helper 或 A/B/D。同一实际入口出现可靠相反要求时停止；其余连续完成必要 TDD、全部测试／性能、审查、修正、复审和提交。 |
| E | 修改 `.github/scripts/smoke-local_test.go`；创建唯一的 `.github/scripts/testdata/cpa-functional-regression_test.go`；仅在实际入口变化时改 `Makefile`、`.github/workflows/build.yml`。 | 依赖已合并 A/B/C/D/G，在单独 worktree 完成实际 CPA RED/GREEN、全部终验和独立覆盖审查。F15 baseline 固定 `6c7f060`，F01..F14 原 baseline 保持。 |

B 的已知保序 fixture 包括 `TestRestoreResponseModelFastPathPreservesEscapedSemantics`、`TestStreamChunkRewriterFramesRawJSONBeforeSSEDoneInSameWrite`、`TestStreamChunkRewriterFramesRawJSONBeforeSSEDoneAcrossPartitions`、`BenchmarkSSEMarkerGuardRestore`、`BenchmarkSSEMarkerGuardCandidate`（`main_test.go:2332-2369`），以及全量 suite 显示仅因原 map 排序或 escaped key 归一化而过时的 exact-byte 测试。B 对这些 fixture 保持同样输入、同样 framing/opaque/ownership 断言，只更改新的精确字节表示。两个 benchmark 不随 root tests 运行，B 必须独立运行其 byte-exact preflight 并达到 GREEN 后交给 C/E。

C 的职责 fixture 包括 `TestHandleExecutorExecuteStreamReturnsPreparedHostHeaders`、`TestRunStreamForwardTerminatesReframedOpenAIChat`、`TestRunStreamForwardProcessesTerminalPayload`、`TestRunStreamForwardBatchesOnlySSEOutput`。B 若必须修改这些测试中的 JSON 字节，先完成保序部分并提交，C 从该提交开始修改 framing/emit 部分。任何同一测试的双重修改都按此依赖执行；函数边界不代替 fixture 依赖。

G 的共享函数修改从协调者主动交付的完整整合 HEAD 开始，确认它包含固定 C 最终审查提交 `51b006438c1a0edd4263020297747f99a174c47c` 及全部已整合修复；产品 reviewBASE 固定该 C SHA，最终 review 覆盖 C..G 最终 HEAD 全部累计范围并明确文档范围。不读取或轮询其他在修改 worktree。只有限核查交付 HEAD 的 Write 分发、scanner/reset、drain 候选提交、complete/incomplete 限额和 batching/chunks+error。G2 按现行 output-unit 消费语义核查实际入口；规格修正未独立审查通过，或同一实际入口出现可靠相反要求时停止产品编码。不能以全部积存到 EOF 或启发式取得产品 GREEN。

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
- [ ] PR7 复用 C/F04 的同一 TDD。在上述 core/header fixture 覆盖缺少 `Content-Type`、`text/event-stream`、`text/event-stream; charset=utf-8`、`application/json`，以及最后一个 raw payload 与 Done 同回调的 ABI 控制；OpenAI 输出始终是逐个可解码的 raw JSON、恢复 client model、零插件 framing/DONE。保留 Gemini、Responses、Claude、Interactions 的原职责对照；旧 header、raw-chat、done-payload、batching fixture 只修正相应 framing/emission 预期，不改 D 的生命周期函数。源探测单 payload 的内容为 `hello`，上述双 payload fixture 仍为 `onetwo`，均精确检查完整内容。执行以下 focused 命令，目标 RED/GREEN 仍属于 F04。

```bash
go test . -run 'TestFunctionalOpenAIRawCoreChunks|TestHandleExecutorExecuteStreamReturnsPreparedHostHeaders|TestRunStreamForward(TerminatesReframedOpenAIChat|ProcessesTerminalPayload|BatchesOnlySSEOutput)|TestPrepareExecutorStreamGeminiKeepsCoreChunksRaw' -count=1 -v
```

- [ ] 公共报告补充约束：PR7 原始 JSON 已核实 `user.login=leolmq`，候选 head 为 `9fe917c031f0a7ed8b43897220a1eea237acc88b`。后续相关 C 产品修复、复审修正、E 永久回归和文档修正提交均注明 #7／@leolmq。C5 的既有 `git commit` 命令必须追加 `-m $'Related-PR: #7\nPR-Author: @leolmq'`，每次提交后执行 `git log -1 --format='%H%n%B'` 核对实际 body；E3 和相关文档提交同样执行。Release／最终 PR 评论注明该作者贡献，不编造姓名、email 或 `Co-authored-by`，不改写已经完成的历史。

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

- [ ] issue8 复用既有 C terminal 控制和 E2 的同一完整 Responses fixture，规则为 `grok-4.6=>grok-4.7`，`Format/SourceFormat=openai-response`。覆盖 `Payload+Done`、在第 17 字节拆分后末段携带 Done、`Payload+Error+Done`（原错误 `probe upstream error`），以及 payload 后独立 Done 的控制；检查上游请求 `Model` 和顶层 `model=grok-4.7`、下游 `response.model=grok-4.6`、完整 output 原样、emit -> host-close -> plugin-close 与成功 close 次数。保留 D 的 terminal/error 断言和函数所有权。执行 `go test . -run 'TestRunStreamForward(ProcessesTerminalPayload|FlushesPendingBytesOnReadError|PreservesInBandErrorAcrossCleanupFailures)' -count=1 -v`。早期独立 fake callback 探测的完整 SSE terminal 控制已通过，按控制记录，不要求人为制造 RED；该结果不覆盖 E2 两个无 LF field chunks，也不证明补充调查中的 mapped 502 已解决。
- [ ] E2 引用的 `issue8-alternative-verification.json` 已正式确认 `6c7f060` 的无 LF 字段边界缺陷和 native/HTTP mapped 502。以 C 原范围最终已验证 HEAD 为前置依赖，由 Task G 在 `stream_native_fields_regression_test.go` 的 `TestFunctionalNativeSSEField` 范围内原样检查 E2 的两个无 LF event/data payload、18 字段九事件、单 terminal data-only 和九个独立 data-only payload；不预拼接、不补 LF，保留 XAI core 空 chunk 和正常独立 Done。单 terminal 对照保持一帧、模型恢复和完整 opaque output；九个 data-only payload 保持九个有序有效帧、完整 delta/output 和 `response.completed`。保存实际 source HEAD、原始输入和 baseline/fixed 结果；G 必须先完成 G2 同前缀／迟到 delimiter 的有限接口语义核查。缺可靠区分信息时停止产品编码并报告冲突，不能把全部积存到 EOF 或候选提前 emit 作为修复。仍为目标 RED 且 G2 条件满足时，顺序完成 focused TDD、共享 scanner 修正、完整验证、独立规格／质量复审、必要修正及提交；已有 GREEN 时记录 C 修复归属并保存永久回归，不新增猜测性 workaround 或共享函数的并行实现。运行 `go test . -run '^TestFunctional|TestRunStreamForward(ProcessesTerminalPayload|FlushesPendingBytesOnReadError|PreservesInBandErrorAcrossCleanupFailures)' -count=1 -v`，既有完整 SSE、合法字节分片、metadata、incomplete 上限和 D 的 terminal/error 条件不变。E 等该后续任务最终提交通过并整合后完成唯一入口的真实 producer/native/framer/HTTP 验收。
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

## Task G：native SSE field boundary，F15

> For agentic workers：沿用既定 workflow 分工。产品任务在独立分配 worktree 连续完成测试、实现、审查、修正、复审和提交。本输入层级规格修正须独立审查通过后启动 G。G1/G2 按固定 CPA 正常无 mapper 消费链核查 callback 单位、顺序和时机，再连续完成必要完整 TDD 与验收；同一实际入口出现可靠相反要求时停止产品编码。产品尚未修复。

**Goal：**覆盖并修复 F15 的 field-pair 与多事件 data-only 共用边界问题，同时保留单 terminal data-only、普通 SSE 分片和完整内容。

**Architecture：**使用共享 rewriter/scanner、现有增量状态、SSE helpers 与 encoding/json。按既有 format 和调用入口保持 Responses output-unit 语义，generic 连续 wire 保持合法字节分片和标准 delimiter。完整值验证、候选记录与派发分别明确时点；G2 核对原 validator 及对应 HTTP/WS 消费者。同一实际入口出现可靠相反要求时停止并交回具体冲突。

**使用的技术：**Go >=1.26.0，现有 encoding/json，固定 CPA v7.2.152 与 c-shared 插件。没有新产品依赖或配置。

**Spec：**`docs/superpowers/specs/2026-10-02-functional-fixes-design.md` 中 G/F15 的绑定规格。完整草稿来源为 `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/.superpowers/sdd/2026-10-02-functional-fixes-implementation/task-G-native-fields-design-1.json`，SHA-256 `4cc5cdb512e520af2fcdc2486af640fa00e5c510acda28e5ac6e5b2fd2922b72`；独立复审为同目录 `task-G-native-fields-plan-review-1.json`（approved=true、findings=[]），批准范围仅为草稿。当前输入层级与派发要求依据同一协调目录的 `task-G-consumer-semantic-verification.json`（verified=true）；原草稿和复审保留为历史依据，本次完整更正草稿与实际命令结果单独记录。

### Global Constraints

- 保留 F01..F14；F13 是 helper 回归，F14 是合法 ABI 层级；F15 包含 field-pair 和 data-only，不单列 provider 修复。
- CPA v7.2.152，module Sum `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`，integration binary revision `c76dfd4e0edabab9000628b1560ab8ab379eadb8`。
- maxPendingStreamBytes 保持 16 << 20；完整单位和完整累计流量不计入 incomplete 上限；超限未完成单位清空并报错。
- 使用 encoding/json 和现有 SSE helpers/state，不新增手写 parser、配置、产品依赖或 ABI 字段。
- 保留 B 的 JSON/opaque/ownership、C 的 framing/batching 与 chunks+error、D 的终止和关闭；A/B/D 不修改。
- 只在分配 worktree 或本任务 OS 临时副本执行；不读取 C 在修改 worktree、不轮询、不修改 CPA/module cache。
- E 保留唯一 TestCPAPluginIntegration smoke 入口与唯一 cpa-functional-regression_test.go CPA fixture。
- pluginVersion 默认 0.0.0-dev；issue8 用 Refs；PR7 相关新提交注明 #7 与 @leolmq。发版、评论、推送和关闭不在本任务内。

### Review Focus

1. field-pair 与 data-only 两/九事件必须同时通过；单 terminal data-only 正常不能代替多事件。G1 的 Sequence 和 DataOnlyTerminalControl 分别断言。
2. G1 GenericCanonicalControls 在 generic 连续 wire 入口检查 canonical 拼接 event/data field 的 whole、全部双分片、单字节及迟到 delimiter 原文。Responses LateDelimiter 按四 callback 累计 `0、1、1、2` 派发，LateDataDelimiter/Dispatch 检查下一独立 data 派发前值、Flush 派发末值；whole event-only metadata 后接真正 completed 的正常控制保留。
3. header 候选已记录后不得重扫整个 JSON。Continuation/ContinuationAllocations 与 2 MiB、8 MiB、8 KiB benchmark 检查模型、opaque、输出长度、ownership 与扫描成本。
4. 完整大单位和 overflow tail 分开检查；Limit 的每个 complete/incomplete subtest 独立运行，不因首个 RED 跳过控制。EOF、错误和 close 由 Forwarder 及 C/D 既有控制覆盖。
5. 根 callback mock、真实 core/host producer、native DLL、实际 Responses HTTP framer 分层记录。G4 的完整 CPA 代码和 E 唯一入口验证两种 builtin、两种形状、全部 route 与非流，不用 callback mock 代替实际链路。

### 文件与接口

G 的源文件位置以其分配的绝对 worktree 根目录为准。当前固定设计源为 `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/main.go`、`main_test.go`、`performance_regression_test.go`。

- 创建 `stream_native_fields_regression_test.go`，内容为 G1 的完整 Go 文件。
- 仅修改 main.go 共用 streamChunkRewriter/sseRewriter/scanner 的 F15 必要部分；必要协议 fixture 修改限于 main_test.go，不改 B 的保序期望或 D 的 lifecycle fixture。
- 新 native continuation benchmark 可随新回归文件保存；只有共用检查确需调整才改 performance_regression_test.go，门槛和旧 preflight 不放宽。
- E 将 G4 的完整 Go 代码合入其唯一 `.github/scripts/testdata/cpa-functional-regression_test.go`，imports 与已有函数合并；不覆盖其他 F01..F14 fixture，不另建第二文件。

保留接口：`(*streamChunkRewriter).Write([]byte) ([][]byte,error)`、Flush/Finish 同返回类型；`emitRewritten(chunks [][]byte, batch bool, emit func([]byte) error) error`；`(*executorStream).processPayload(*streamChunkRewriter, []byte) error`；`(*executorStream).flushAndEmit(*streamChunkRewriter, bool) error`。非空 chunks 可与 error 同时存在；由 C 的既有调用者发送并合并错误。

### G1：固定起点、完整 focused 回归与归属

- [ ] 等待本输入层级规格修正独立审查通过及协调者主动交付完整整合 HEAD。由既定 workflow 分配 G 的独立 worktree，核查根目录、branch、clean 和实际 HEAD 等于交付 SHA，确认固定 C=`51b006438c1a0edd4263020297747f99a174c47c` 为 ancestor。产品 reviewBASE 固定该 C SHA，起点 SHA 另记，最终 review 使用固定 C..G 全部最终 HEAD，明确文档范围。不读取其他在修改目录。

以下命令在 G 分配 worktree 执行，Git Bash 变量仅在同一调用有效；多次调用重新给 G/reviewBASE/startHEAD 赋最初记录的相同值，startHEAD 不随产品修改更新。

```bash
G=$(git rev-parse --show-toplevel)
reviewBASE=51b006438c1a0edd4263020297747f99a174c47c
startHEAD=$(git -C "$G" rev-parse HEAD)
git -C "$G" merge-base --is-ancestor "$reviewBASE" "$startHEAD"
git -C "$G" status --short
git -C "$G" log -1 --format='%H %s'
go -C "$G" version
```

- [ ] 在完整整合起点重新核对 C 交付接口的有限位置：Write 向 sse.Write 分发及格式设置；scanner header/data/typed-complete 状态及 reset；drain 的标准 delimiter/logical 候选选择与提交；complete/incomplete 限额；batchSSEOutput 和 processPayload/flushAndEmit 的 chunks+error 消费。固定设计源对应 main.go:32..1214、1656..1685、1982..2013。不复制未交付 C 的函数，不重新全仓审计。
- [ ] 加入下列完整、已编译的 root Go 回归文件。现有 requireValidResponsesSSE、splitSSELine、sseFieldValue、newStreamChunkRewriter、runStreamForward 均为固定源码真实符号，其他 gNative helpers 在本文件定义。C 如已有同输入、同断言的回归，复用并补足缺失矩阵，不重复定义。

```go
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
				b.ReportAllocs()
				b.SetBytes(int64(len(want)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					chunks, err = gNativeContinue(parts)
					if err != nil || len(chunks) == 0 {
						b.Fatalf("native continuation=(%d,%v)", len(chunks), err)
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
```

- [ ] 运行新回归和所有控制。先确认实际编译成功，再把缺少内部 LF/派发边界、模型未恢复或完整大单位误限长记录为 RED；编译/启动/缺依赖错误不能充当 RED。

```bash
go -C "$G" test -mod=readonly -count=1 -v . -run '^TestFunctionalNativeSSEField'
```

历史 6c7f060 的 Boundary、Sequence 两类两个/九个事件、Forwarder、Discriminator 正例、field-pair Continuation 与 InputOwnership 为目标 RED；完整单位限额及控制结果保留在原草稿报告。本次更正后的 Dispatch、LateDelimiter/LateDataDelimiter 按正常消费者时机断言，GenericCanonicalControls 使用 generic 连续 wire 入口。实际编译、目标 RED、正常控制、仅编译 native DLL mapped 草稿和未运行项目分别记录在本次 JSON；不将旧零提前输出断言的 GREEN 作为新目标结果。native benchmark 必须先通过 byte-exact preflight，错误输出不能用于计时。

只有两类目标、单 terminal 控制、generic wire 及 Responses callback/迟到 delimiter 的各自控制、格式/opaque/ownership、限额与错误控制均通过，才可判定 C 已覆盖新函数层验收。已 GREEN 时不撤销 C 取得 RED、不重复改产品，记录 C 修复 commit，仍补缺失永久 producer/native/HTTP 回归并完成 G2 的语义审查、G3..G5。

### G2：输入层级、派发时点和最小共用修复检查点

- [ ] 确认本输入层级规格修正已经独立审查通过。有限读取综合报告的结论、requiredSpecCorrection、实际证据与限制；以固定 CPA 的正常无 mapper 消费链定义 Responses output-unit 语义，不把本报告作为产品 GREEN 或发布放行。
- [ ] 核对实际入口：ResponseFormat、StreamChunk/output-unit、translator、validator、host bridge 和现有 format/Write 分发。HostModelStreamReadResponse 仍只有 Payload/Error/Done，接口不增加区分标志。HTTP 网络 Write/Flush 先经过真实 XAI/Codex Scanner，再用捕获的 host Payload 验收；不能把网络四次 Write 直接作为四 callback fixture。
- [ ] 使用 G1 同一 canonical 四 callback，逐次比较独立预期的完整模型恢复 bytes 与 data 事件，累计为 `0、1、1、2`。第 2 次 created、第 4 次 completed；迟到 LF/CRLF 不改变这两个事件。data-only 的两/九事件在下一独立 data chunk 到达时派发前值，Flush 派发末值。不能以 emit 次数代替 data 事件，也不能全流积存到 EOF。
- [ ] GenericCanonicalControls/WireControls 在 generic 连续 wire 入口保留 whole、全部双分片、单字节、LF/CR/CRLF/BOM 和标准 delimiter 前零派发。ResponsesMetadataControl 保留 whole event-only metadata 后接真正 completed data 的正常输入。单个拼接 data field 保留 generic 原文及 Responses validator 的 `invalid SSE data JSON` 错误；孤立 event-only 流的无 mapper 失败不认定为 mapper 缺陷。F14 等合法 callback 分片按原 validator 和对应 HTTP/WS 消费者核验，分别记录其支持和限制，不删除 unknown/id/retry/comment metadata。
- [ ] 在共享 Write/scanner/drain 保持现有 format 与调用入口，复用增量 header/JSON/complete-end 状态、SSE helpers、rewriteEvent 和 B 的恢复器。完整值验证、候选记录与不可逆派发分别说明时点；generic wire 等待标准 delimiter，Responses 按原消费者语义生成必要 LF/blank delimiter。type 验证使用 encoding/json，闲置字段和 opaque 原文保持，data-only 不要求 event header。内部已有 LF 的 logical-event 支持、reset、完整前缀及原错误保持。不新增 mode/config/ABI/依赖/parser/provider 推断、timeout、lookahead 个数或 substring 决策。
- [ ] header 路径先检查 format、是否仍需记录 header 和相关 field 前缀，复用 C 的增量游标。记录后不从 0 重扫增长的 header/JSON；两类 2 MiB/8 MiB、8 KiB continuation 保持 byte-exact、模型/opaque/ownership 和增量成本验证。
- [ ] 保持 16 MiB 规则：本次调用补成的完整单位可超限；此前超限 incomplete prefix 保持原 error/清空。完整前缀与尾部错误可共存，C 的 chunks+error 调用者先发送有效 prefix 再合并错误，不提高常量或删除错误。
- [ ] 若后续发现同一实际入口确实必须同时保证两种相反解释的可靠证据，停止产品编码，返回准确入口、输入数组、消费者和相反时机的具体冲突。重跑完整 F15 和全部既有控制，成功后进入 G3；未达到要求不提交产品修复。

```bash
go -C "$G" test -mod=readonly -count=1 -v . -run '^TestFunctionalNativeSSEField'
go -C "$G" test -mod=readonly -count=1 -v . -run '^(TestRunStreamForward(ProcessesTerminalPayload|SeparatesDelimiterlessKimiResponsesLifecycle|FlushesPendingBytesOnReadError|BatchesOnlySSEOutput|PreservesInBandErrorAcrossCleanupFailures)|TestStreamChunkRewriter(DelimiterlessResponsesPartitionInvariant|DoesNotEndOrdinarySSEAtReadBoundary|DoesNotTreatReadBoundaryAsLineEnding|BOMPartitionInvariant|FramesRawJSONByFormat|RawJSONArrayPartitions|PreservesUnframedRawJSONSeparators)|TestSSERewriter(DoesNotInventLineBreakAtChunkBoundary|OutputOwnershipAcrossFutureWrites))$'
```

检查完整输出后再核对原错误与 close；callback mock 仅证明函数调用路径。

### G3：完整 suite、race、allocation 和增量性能

- [ ] 在相同 G worktree 运行所有 root tests、vet 和 race，不留下 C framing、B 保序或 D lifecycle 失败。

```bash
go -C "$G" test -mod=readonly -count=1 ./...
go -C "$G" vet -mod=readonly ./...
go -C "$G" test -mod=readonly -race -count=1 ./...
go -C "$G" test -mod=readonly -race -count=3 . -run '^TestFunctionalNativeSSEField'
```

- [ ] 运行全部相关既有 allocation 与新增 native continuation 检查，门槛保持。no-model response <=1 clone，complete markerless/escaped SSE <=6，fragmented raw JSON <100，delimiterless 2 MiB/8 KiB <=200；新的两类 native 2 MiB/8 KiB 也先检查完整输出再测 <=200，不能只跑正常单 data 控制。

```bash
go -C "$G" test -mod=readonly -count=1 -v . -run '^(TestRestoreResponseWithoutModelUsesCloneOnly|TestStreamChunkRewriter(FastPathsCompleteSSEBatchWithoutModelMarker|FastPathsEscapedSSEBatchWithoutModelMarker|ScansFragmentedDelimiterlessResponsesEventLinearly|LargeFragmentedRawJSONAllocations|RawJSONUsesOneRestorePass)|TestSSERewriter(SingleDataFastPathAllocations|MultiEventBatchAvoidsPerEventChunkSliceAllocation)|TestFunctionalNativeSSEFieldContinuationAllocations)$'
```

- [ ] 在同一机器、同 Go、同参数下顺序测完整整合起点 startHEAD 与 G，产品 reviewBASE 始终保持固定 C。通过自身 Git objects 把 startHEAD 导出到 OS temp，禁止 checkout/reset 当前分支。G 的必要产品变化只在 main.go，旧测试/性能变化只在 main_test.go、performance_regression_test.go，三个文件的起点 overlay 须经 diff 确认覆盖 G 的全部源码差异；不能用固定 C 的旧树替换已整合 A/B/D 修复。

准备 BASE overlay 的可执行步骤如下，普通 Git 操作分开执行。临时 Python 文件是另一工具的输入，任务结束删除；不提交。

```bash
BASE_TEMP=$(mktemp -d)
git -C "$G" archive --format=tar --output="$BASE_TEMP/base.tar" "$startHEAD" main.go main_test.go performance_regression_test.go
tar -xf "$BASE_TEMP/base.tar" -C "$BASE_TEMP"
```

把以下完整 Python 保存到系统临时目录的 build_base_overlay.py，执行时传入 G 和 BASE_TEMP 的 Windows绝对路径。

```python
import json
import sys
from pathlib import Path

root = Path(sys.argv[1]).resolve()
base = Path(sys.argv[2]).resolve()
files = ("main.go", "main_test.go", "performance_regression_test.go")
for name in files:
    if not (root / name).is_file() or not (base / name).is_file():
        raise RuntimeError(f"missing source: {name}")
replace = {str(root / name): str(base / name) for name in files}
(base / "overlay.json").write_text(json.dumps({"Replace": replace}), encoding="utf-8")
```

```bash
python "$BASE_TEMP/build_base_overlay.py" "$G" "$(cygpath -m "$BASE_TEMP")"
BASE_OVERLAY="$BASE_TEMP/overlay.json"
go -C "$G" test -mod=readonly -overlay "$BASE_OVERLAY" -run '^$' -bench '^BenchmarkSSEMarkerGuard(Restore|Candidate)$' -benchtime=1x -count=1 -benchmem .
go -C "$G" test -mod=readonly -run '^$' -bench '^BenchmarkSSEMarkerGuard(Restore|Candidate)$' -benchtime=1x -count=1 -benchmem .
go -C "$G" test -mod=readonly -overlay "$BASE_OVERLAY" -run '^$' -bench '^Benchmark(RewriteTopLevelModel|RestoreResponseModel|RestoreResponseWithoutModel|ResponseModelMarkerScan|SSEMarkerGuard(Restore|Candidate)|StreamChunkRewriter(FragmentedRawJSON|SingleJSON|CompleteSSEBatch|EscapedSSEBatch|UnicodeEscapedSSEBatch)|EmitRewrittenBatch)$' -benchmem -count=5 .
go -C "$G" test -mod=readonly -run '^$' -bench '^Benchmark(RewriteTopLevelModel|RestoreResponseModel|RestoreResponseWithoutModel|ResponseModelMarkerScan|SSEMarkerGuard(Restore|Candidate)|StreamChunkRewriter(FragmentedRawJSON|SingleJSON|CompleteSSEBatch|EscapedSSEBatch|UnicodeEscapedSSEBatch)|EmitRewrittenBatch)$' -benchmem -count=5 .
```

- [ ] 新 native benchmark 同时覆盖 field-pair/data-only、2 MiB/8 KiB 和8 MiB/8 KiB。保留 byte-exact preflight、模型、opaque 1.00/Unicode、全部长度和 ownership。只在 preflight通过时记录耗时；起点的错误输出不作为正确输出的性能基准，正常 data-only 控制可以单独比较。

```bash
go -C "$G" test -mod=readonly -run '^$' -bench '^BenchmarkStreamChunkRewriterNativeFieldContinuation$' -benchtime=1x -count=1 -benchmem .
go -C "$G" test -mod=readonly -run '^$' -bench '^BenchmarkStreamChunkRewriterNativeFieldContinuation$' -benchmem -count=5 .
go -C "$G" test -mod=readonly -overlay "$BASE_OVERLAY" -run '^$' -bench '^BenchmarkStreamChunkRewriterNativeFieldContinuation/dataOnly=true/' -benchmem -count=5 .
```

检查增量游标和相关 helper 调用：候选记录后每次续写只推进新 bytes；不得重新搜索完整 pending header/JSON。结合 2/8 MiB ns/op、B/op、allocs/op检查增长；本轮旧示例的约16倍增长是已核实的问题，不把 <=200 allocation当线性证据。发现稳定新增扫描或复制成本，由 G 修正并重跑全部相关检查，不设未经测量的百分比门槛。

### G4：交 E 的唯一永久 producer/native/HTTP fixture

- [ ] E 把下面完整、已编译的 Go 代码合入其唯一 CPA fixture。保留 package pluginhost 和所需 imports，重复 import 合并；已有完全相同 fixture/helper可复用，保留本文件的两类输入和全部断言。TestModelMapperFunctional 前缀由 E 原 runner自动选中，不增加 smoke入口或独立 CPA fixture。
- [ ] 新代码的四层分别为真实 core/host producer、真实 native DLL 映射、真实 Responses HTTP handler/framer 的 enabled mapped/unmatched/disabled 与非流，以及无 mapper的纯 producer/HTTP控制。只有外部 httptest upstream 使用固定正常内容。

```go
package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	openaihandlers "github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers/openai"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"gopkg.in/yaml.v3"
)

const gCPAOutput = `[{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}]`
const gCPACompleted = `{"type":"response.completed","response":{"id":"resp-issue8","object":"response","status":"completed","model":"grok-4.7","output":` + gCPAOutput + `}}`

func gCPAPayloads(model string, count int) []string {
	values := []string{
		`{"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`,
		`{"type":"response.in_progress","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"ordinary grok-4.7 opaque 中文 output"}`,
		`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"ordinary grok-4.7 opaque 中文 output"}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}}`,
		gCPACompleted,
	}
	for i := range values {
		values[i] = strings.Replace(values[i], `"model":"grok-4.7"`, `"model":"`+model+`"`, 1)
	}
	if count == 1 {
		return values[8:]
	}
	return values
}

type gCPAFieldsFixture struct {
	name, eol, contentType string
	step                   int
	dataOnly               bool
	count                  int
}

func gCPAFieldsFixtures() []gCPAFieldsFixture {
	var out []gCPAFieldsFixture
	for _, dataOnly := range []bool{false, true} {
		for _, count := range []int{1, 9} {
			for _, transport := range []gCPAFieldsFixture{
				{name: "LF-whole", eol: "\n", contentType: "text/event-stream"},
				{name: "CRLF-whole", eol: "\r\n", contentType: "text/event-stream"},
				{name: "LF-7bytes", eol: "\n", contentType: "text/event-stream", step: 7},
				{name: "CRLF-bytewise", eol: "\r\n", contentType: "text/event-stream", step: 1},
				{name: "LF-charset", eol: "\n", contentType: "text/event-stream; charset=utf-8", step: 11},
				{name: "CRLF-charset", eol: "\r\n", contentType: "text/event-stream; charset=utf-8", step: 7},
			} {
				transport.dataOnly, transport.count = dataOnly, count
				transport.name = fmt.Sprintf("dataOnly=%v/count=%d/%s", dataOnly, count, transport.name)
				out = append(out, transport)
			}
		}
	}
	return out
}

func gCPAFields(f gCPAFieldsFixture, model string) ([]string, []byte) {
	var fields []string
	var wire bytes.Buffer
	for _, payload := range gCPAPayloads(model, f.count) {
		var event struct{ Type string }
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			panic(err)
		}
		if !f.dataOnly {
			fields = append(fields, "event: "+event.Type)
			fmt.Fprintf(&wire, "event: %s%s", event.Type, f.eol)
		}
		fields = append(fields, "data: "+payload)
		fmt.Fprintf(&wire, "data: %s%s%s", payload, f.eol, f.eol)
	}
	return fields, wire.Bytes()
}

type gCPAUpstreamRecord struct {
	body, response []byte
	model, path    string
}

type gCPALocalProducer struct {
	mu       sync.Mutex
	fixture  gCPAFieldsFixture
	records  []gCPAUpstreamRecord
	provider coreauth.ProviderExecutor
	auth     *coreauth.Auth
	base     *handlers.BaseAPIHandler
	cfg      *config.Config
}

func gCPANewLocalProducer(t *testing.T, name string) *gCPALocalProducer {
	t.Helper()
	p := &gCPALocalProducer{cfg: &config.Config{}}
	p.provider = runtimeexecutor.NewXAIExecutor(p.cfg)
	if name == "codex" {
		p.provider = runtimeexecutor.NewCodexExecutor(p.cfg)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		var request struct {
			Model  string
			Stream bool
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		if r.URL.Path != "/v1/responses" || !request.Stream || (request.Model != "grok-4.6" && request.Model != "grok-4.7") {
			t.Errorf("upstream path=%s request=%+v", r.URL.Path, request)
		}
		p.mu.Lock()
		f := p.fixture
		p.mu.Unlock()
		_, wire := gCPAFields(f, request.Model)
		p.mu.Lock()
		p.records = append(p.records, gCPAUpstreamRecord{body: bytes.Clone(body), response: bytes.Clone(wire), model: request.Model, path: r.URL.Path})
		p.mu.Unlock()
		w.Header().Set("Content-Type", f.contentType)
		step := f.step
		if step == 0 {
			step = len(wire)
		}
		for start := 0; start < len(wire); start += step {
			if _, err := w.Write(wire[start:min(start+step, len(wire))]); err != nil {
				t.Error(err)
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(server.Close)
	p.auth = &coreauth.Auth{ID: "g-fields-" + name, Provider: name, Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "fake-upstream-key", "base_url": server.URL + "/v1", "proxy_url": "direct"}}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.SetConfig(p.cfg)
	manager.RegisterExecutor(p.provider)
	if _, err := manager.Register(context.Background(), p.auth); err != nil {
		t.Fatal(err)
	}
	registry.GetGlobalRegistry().RegisterClient(p.auth.ID, p.auth.Provider, []*registry.ModelInfo{{ID: "grok-4.6"}, {ID: "grok-4.7"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(p.auth.ID) })
	p.base = handlers.NewBaseAPIHandlers(&p.cfg.SDKConfig, manager)
	return p
}

func (p *gCPALocalProducer) setFixture(f gCPAFieldsFixture) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fixture, p.records = f, nil
}

func gCPARequest(model string, stream bool) []byte {
	return []byte(fmt.Sprintf(`{"model":%q,"input":"say ok","stream":%v,"prompt_cache_key":"task-g-local"}`, model, stream))
}

func gCPARequireFields(t *testing.T, parts []string, f gCPAFieldsFixture, model string) {
	t.Helper()
	want, _ := gCPAFields(f, model)
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("producer fields=%q, want=%q", parts, want)
	}
	for _, part := range parts {
		if strings.ContainsAny(part, "\r\n") {
			t.Fatalf("logical field unexpectedly contains line ending: %q", part)
		}
	}
}

func gCPACoreFields(t *testing.T, p *gCPALocalProducer, ctx context.Context) []string {
	t.Helper()
	body := gCPARequest("grok-4.7", true)
	stream, err := p.provider.ExecuteStream(ctx, p.auth, coreexecutor.Request{Model: "grok-4.7", Payload: body}, coreexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: body, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		if len(chunk.Payload) > 0 {
			parts = append(parts, string(chunk.Payload))
		}
	}
	return parts
}

func gCPAHostFields(t *testing.T, p *gCPALocalProducer, ctx context.Context) []string {
	t.Helper()
	result, errMsg := p.base.ExecuteModelStream(ctx, handlers.ModelExecutionRequest{EntryProtocol: "openai-response", ExitProtocol: "openai-response", Model: "grok-4.7", Stream: true, Body: gCPARequest("grok-4.7", true)})
	if errMsg != nil {
		t.Fatal(errMsg.Error)
	}
	var parts []string
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		if len(chunk.Payload) > 0 {
			parts = append(parts, string(chunk.Payload))
		}
	}
	return parts
}

func gCPARequireResponse(t *testing.T, raw []byte, model string) {
	t.Helper()
	var response struct {
		Model, Status string
		Output        json.RawMessage
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Model != model || response.Status != "completed" || string(response.Output) != gCPAOutput {
		t.Fatalf("response model=%q status=%q output=%s", response.Model, response.Status, response.Output)
	}
}

func gCPARequireEvents(t *testing.T, raw []byte, f gCPAFieldsFixture, model string) {
	t.Helper()
	// 现有 sse.Decode 只识别 LF；仅为该 parser 统一已知 CRLF 行结束，原始 bytes 继续保留。
	events, err := sse.Decode(bytes.NewReader(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))))
	if err != nil {
		t.Fatal(err)
	}
	want := gCPAPayloads(model, f.count)
	var payloads []string
	for _, event := range events {
		payload, ok := event.Data.(string)
		if !ok || payload == "" {
			continue
		}
		if payload == "[DONE]" {
			t.Fatal("Responses must not acquire DONE")
		}
		var value struct {
			Type, Delta string
			Response    json.RawMessage
		}
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			t.Fatal(err)
		}
		if !f.dataOnly && event.Event != value.Type {
			t.Fatalf("event=%q type=%q", event.Event, value.Type)
		}
		if f.dataOnly && event.Event != "message" {
			t.Fatalf("data-only decoder event=%q, want default message", event.Event)
		}
		if value.Type == "response.output_text.delta" && value.Delta != "ordinary grok-4.7 opaque 中文 output" {
			t.Fatalf("delta=%q", value.Delta)
		}
		if value.Type == "response.completed" {
			gCPARequireResponse(t, value.Response, model)
		}
		payloads = append(payloads, payload)
	}
	if !reflect.DeepEqual(payloads, want) {
		t.Fatalf("data events=%q want=%q", payloads, want)
	}
}

func gCPALoadNative(t *testing.T, p *gCPALocalProducer, enabled bool) *Host {
	t.Helper()
	plugin := os.Getenv("CPA_SMOKE_PLUGIN")
	if plugin == "" {
		t.Fatal("CPA_SMOKE_PLUGIN is required")
	}
	library, err := os.ReadFile(plugin)
	if err != nil {
		t.Fatal(err)
	}
	pluginDir := filepath.Join(t.TempDir(), "plugins")
	target := filepath.Join(pluginDir, runtime.GOOS, runtime.GOARCH, "model-mapper"+filepath.Ext(plugin))
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, library, 0600); err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(target)
	if err != nil || sha256.Sum256(copied) != sha256.Sum256(library) {
		t.Fatalf("DLL copy error=%v", err)
	}
	data := fmt.Sprintf("plugins:\n  enabled: %v\n  dir: %q\n  configs:\n    model-mapper:\n      enabled: true\n      priority: 1\n      global_rules: 'grok-4.6=>grok-4.7'\n", enabled, filepath.ToSlash(pluginDir))
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatal(err)
	}
	host := New()
	host.SetModelExecutor(p.base)
	host.ApplyConfig(context.Background(), &cfg)
	active := host.activeRecords()
	if enabled {
		if len(active) != 1 || active[0].id != "model-mapper" || active[0].plugin.Capabilities.Executor == nil {
			t.Fatal("native executor is not active")
		}
		t.Cleanup(func() {
			if !host.UnloadPlugin("model-mapper") {
				t.Error("native unload failed")
			}
		})
	} else if len(active) != 0 {
		t.Fatal("disabled control unexpectedly has an active plugin")
	}
	t.Logf("native enabled=%v source=%s copied=%s SHA256=%x", enabled, plugin, target, sha256.Sum256(library))
	return host
}

func TestModelMapperFunctionalNativeProducerFields(t *testing.T) {
	for _, name := range []string{"xai", "codex"} {
		t.Run(name, func(t *testing.T) {
			p := gCPANewLocalProducer(t, name)
			for _, f := range gCPAFieldsFixtures() {
				t.Run(f.name, func(t *testing.T) {
					p.setFixture(f)
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					gCPARequireFields(t, gCPACoreFields(t, p, ctx), f, "grok-4.7")
					gCPARequireFields(t, gCPAHostFields(t, p, ctx), f, "grok-4.7")
				})
			}
		})
	}
}

func TestModelMapperFunctionalNativeSSEFields(t *testing.T) {
	for _, name := range []string{"xai", "codex"} {
		t.Run(name, func(t *testing.T) {
			p := gCPANewLocalProducer(t, name)
			host := gCPALoadNative(t, p, true)
			for _, f := range gCPAFieldsFixtures() {
				t.Run(f.name, func(t *testing.T) {
					p.setFixture(f)
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					gCPARequireFields(t, gCPACoreFields(t, p, ctx), f, "grok-4.7")
					gCPARequireFields(t, gCPAHostFields(t, p, ctx), f, "grok-4.7")
					body := gCPARequest("grok-4.6", true)
					result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, pluginapi.ExecutorRequest{Model: "grok-4.6", Format: "openai-response", SourceFormat: "openai-response", Stream: true, Payload: body, OriginalRequest: body})
					if err != nil {
						t.Fatal(err)
					}
					var raw bytes.Buffer
					var parts []string
					for chunk := range result.Chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
						raw.Write(chunk.Payload)
						parts = append(parts, string(chunk.Payload))
					}
					t.Logf("native field input shape=%s output chunks=%q", f.name, parts)
					gCPARequireEvents(t, raw.Bytes(), f, "grok-4.6")
				})
			}
		})
	}
}

func TestModelMapperFunctionalNativeFieldsHTTP(t *testing.T) {
	for _, name := range []string{"xai", "codex"} {
		for _, enabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/enabled=%v", name, enabled), func(t *testing.T) {
				p := gCPANewLocalProducer(t, name)
				host := gCPALoadNative(t, p, enabled)
				p.base.SetPluginHost(host)
				p.base.SetModelRouterHost(host)
				router := gin.New()
				router.POST("/v1/responses", openaihandlers.NewOpenAIResponsesAPIHandler(p.base).Responses)
				server := httptest.NewServer(router)
				defer server.Close()
				for _, f := range gCPAFieldsFixtures() {
					for _, stream := range []bool{true, false} {
						var mapped *gCPAUpstreamRecord
						for _, model := range []string{"grok-4.6", "grok-4.7"} {
							t.Run(fmt.Sprintf("%s/stream=%v/model=%s", f.name, stream, model), func(t *testing.T) {
								p.setFixture(f)
								ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
								defer cancel()
								request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", bytes.NewReader(gCPARequest(model, stream)))
								if err != nil {
									t.Fatal(err)
								}
								request.Header.Set("Content-Type", "application/json")
								response, err := http.DefaultClient.Do(request)
								if err != nil {
									t.Fatal(err)
								}
								raw, err := io.ReadAll(response.Body)
								closeErr := response.Body.Close()
								if err != nil || closeErr != nil {
									t.Fatalf("read=%v close=%v", err, closeErr)
								}
								p.mu.Lock()
								records := append([]gCPAUpstreamRecord(nil), p.records...)
								p.mu.Unlock()
								wantUpstream := model
								if enabled && model == "grok-4.6" {
									wantUpstream = "grok-4.7"
								}
								if len(records) != 1 || records[0].model != wantUpstream || records[0].path != "/v1/responses" {
									t.Fatalf("upstream calls/route=%+v", records)
								}
								if enabled {
									if model == "grok-4.6" {
										snapshot := records[0]
										mapped = &snapshot
									} else if mapped == nil || !bytes.Equal(mapped.body, records[0].body) || !bytes.Equal(mapped.response, records[0].response) {
										t.Fatal("mapped/direct upstream bytes differ")
									}
								}
								t.Logf("HTTP %s stream=%v client=%s upstream=%s status=%d body=%s", f.name, stream, model, wantUpstream, response.StatusCode, raw)
								if response.StatusCode != http.StatusOK {
									t.Fatalf("native HTTP status=%d body=%s", response.StatusCode, raw)
								}
								if stream {
									if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
										t.Fatalf("Content-Type=%q", response.Header.Get("Content-Type"))
									}
									gCPARequireEvents(t, raw, f, model)
								} else {
									gCPARequireResponse(t, raw, model)
								}
							})
						}
					}
				}
			})
		}
	}
}

func TestModelMapperFunctionalNativeFieldsHTTPUnmappedControl(t *testing.T) {
	for _, name := range []string{"xai", "codex"} {
		t.Run(name, func(t *testing.T) {
			p := gCPANewLocalProducer(t, name)
			router := gin.New()
			router.POST("/v1/responses", openaihandlers.NewOpenAIResponsesAPIHandler(p.base).Responses)
			server := httptest.NewServer(router)
			defer server.Close()
			for _, f := range gCPAFieldsFixtures() {
				for _, stream := range []bool{true, false} {
					t.Run(fmt.Sprintf("%s/stream=%v", f.name, stream), func(t *testing.T) {
						p.setFixture(f)
						ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
						defer cancel()
						request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", bytes.NewReader(gCPARequest("grok-4.7", stream)))
						if err != nil {
							t.Fatal(err)
						}
						request.Header.Set("Content-Type", "application/json")
						response, err := http.DefaultClient.Do(request)
						if err != nil {
							t.Fatal(err)
						}
						raw, err := io.ReadAll(response.Body)
						closeErr := response.Body.Close()
						if err != nil || closeErr != nil {
							t.Fatalf("read=%v close=%v", err, closeErr)
						}
						if response.StatusCode != http.StatusOK {
							t.Fatalf("HTTP status=%d body=%s", response.StatusCode, raw)
						}
						if stream {
							gCPARequireEvents(t, raw, f, "grok-4.7")
						} else {
							gCPARequireResponse(t, raw, "grok-4.7")
						}
						p.mu.Lock()
						records := append([]gCPAUpstreamRecord(nil), p.records...)
						p.mu.Unlock()
						if len(records) != 1 || records[0].model != "grok-4.7" || records[0].path != "/v1/responses" {
							t.Fatalf("upstream=%+v", records)
						}
					})
				}
			}
		})
	}
}
```

- [ ] E 复用现有 prepareFunctionalCPAOverlay/runFunctionalCPAOverlay：先 go list 核对 CPA version/Sum，把原模块完整复制到 t.TempDir 可写副本，overlay target 位于副本 internal/pluginhost。源 module cache 保持只读。普通 import 直接使用该模块已有 gin、sse、yaml 与 SDK，不改根 go.mod。
CPA_MODULE_COPY/OVERLAY_JSON 由 E 既有 prepareFunctionalCPAOverlay 的实际返回值设置。先完成下方两组DLL构建与身份核验，再运行同一永久fixture及唯一smoke命令；不能把 -run '^$' 的编译结果记成native GREEN。

- [ ] 在唯一 TestCPAPluginIntegration 入口保存 actual binary 的相同矩阵，复用 smokeEnv、prepareDirs、copyFile、buildConfig、startCPA、waitReady、stopCPA。当前 G4 的 HTTP 函数在真正 CPA handler/framer 上运行；binary smoke继续核对真实进程、版本、配置、management注册和shadow hash，不用该函数结果替代 binary状态检查。

配置使用实际 `xai-api-key`/`codex-api-key`，本地 base-url 和 fake-upstream-key，移除 native组的openai-compatibility；模型列表为 `name=alias=grok-4.6` 与 `name=alias=grok-4.7`，不使用disabled alias。plugins.enabled=true时规则固定grok-4.6=>grok-4.7；false时仍保存同一DLL/规则配置并确认没有active native executor。请求为 G4 的gCPARequest，prompt_cache_key固定task-g-local；nonstream只改stream=false。upstream response.model来自实际request model，opaque output始终保持完整常量。

| 输入 | producer/host要求 | native/HTTP验收 |
| --- | --- | --- |
| completed-only field-pair | 两个无LF fields，core空chunk可记录，host非空字段恰好2个。 | 1条completed data事件，完整output/model，HTTP200。 |
| 九事件 field-pair | 18个字段，按G4全部JSON与名称顺序。 | 9条有序data事件，完整delta/done/part/item/completed，HTTP200。 |
| completed-only data-only | 1个data field，保持既有正常控制。 | 实际Responses framer派发1条，JSON type=completed，opaque/model完整，HTTP200；不因ABI缺末尾空行报错。 |
| 九事件 data-only | 9个独立data fields。 | 9条有序data事件，decoder默认message、JSON type序列正确，全部payload/output/opaque/model完整，HTTP200。 |
| 全部非流 | builtin实际上游可仍为stream=true。 | HTTP200、completed对象、完整output与route期望model。 |

上述两类1/9事件都使用G4的六种transport：LF/CRLF whole、LF7bytes、CRLF bytewise、LF/CRLF charset，分别运行mapped、direct/unmatched、disabled .6/.7。网络Write/Flush经builtin重新形成logical fields，必须记录实际core/host边界，不声称逐网络fragment穿透ABI。保留完整SSE/terminal、Payload+Done及旧原错误控制，不制造正常bridge不存在的非空Payload+Done状态冒充生产捕获。

- [ ] F15 baseline使用固定6c7f060，来源独立于E的旧F01..F14 baseline。只从E自己worktree的Git object读取固定源码，OS temp做构建输入；不回退G/E，不访问C工作区。以下完整命令构建Windows本地两组资产。

```bash
E=$(git rev-parse --show-toplevel)
F15_BASE_DIR=$(mktemp -d)
git -C "$E" archive --format=tar --output="$F15_BASE_DIR/base.tar" 6c7f060f4da5bb33e7b2ecd74c44499c9676a93c main.go abi_cgo.go go.mod go.sum
tar -xf "$F15_BASE_DIR/base.tar" -C "$F15_BASE_DIR"
mkdir -p "$E/dist/native-field-baseline" "$E/dist/native-field-fixed"
F15_BASE_DLL="$E/dist/native-field-baseline/model-mapper.dll"
F15_FIXED_DLL="$E/dist/native-field-fixed/model-mapper.dll"
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go -C "$F15_BASE_DIR" build -mod=readonly -buildvcs=false -trimpath -buildmode=c-shared -o "$F15_BASE_DLL" .
GIT_DIR="$(git -C "$E" rev-parse --absolute-git-dir)" GIT_WORK_TREE="$E" CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go -C "$E" build -mod=readonly -trimpath -buildmode=c-shared -o "$F15_FIXED_DLL" .
sha256sum "$F15_BASE_DIR/main.go" "$F15_BASE_DIR/abi_cgo.go" "$F15_BASE_DLL" "$F15_FIXED_DLL"
go version -m "$F15_BASE_DLL"
go version -m "$F15_FIXED_DLL"
go version -m 'C:/Users/user/Downloads/cpa-plugin/dist/integration/cpa.exe'
```

baseline明确关闭VCS stamping，真实sourceHead来自Git archive6c7f060和源文件hash，不把临时目录上层仓库metadata当来源。fixed记录实际E整合HEAD和vcs.modified状态，不把未提交测试阶段声称为clean Release。开发版本不注入正式版本；baseline/fixed构建参数差别与源码hash如实记录。确认binary固定revision、DLL与实际shadow内容hash、注册与effective_enabled后执行。

- [ ] 使用同一永久fixture运行F15 baseline RED与最终C/G GREEN。producer与无mapper HTTP控制两边都正常；field-pair与九事件data-only在baseline应出现实际目标失败，单terminal data-only保持正常。原始capture/控制成功不替代mapped正确性。

```bash
CPA_SMOKE_PLUGIN="$F15_BASE_DLL" go -C "$CPA_MODULE_COPY" test -mod=readonly -overlay "$OVERLAY_JSON" -count=1 -v ./internal/pluginhost -run '^TestModelMapperFunctionalNative' -timeout 180s
CPA_SMOKE_PLUGIN="$F15_FIXED_DLL" go -C "$CPA_MODULE_COPY" test -mod=readonly -overlay "$OVERLAY_JSON" -count=1 -v ./internal/pluginhost -run '^TestModelMapperFunctionalNative' -timeout 180s
```

- [ ] 从唯一smoke入口运行相同baseline/fixed完整binary matrix，保留原有其他F01..F14验收。

```bash
CPA_SMOKE_INTEGRATION=1 CPA_SMOKE_CPA_BIN='C:/Users/user/Downloads/cpa-plugin/dist/integration/cpa.exe' CPA_SMOKE_PLUGIN="$F15_BASE_DLL" go -C "$E" test -mod=readonly -count=1 -v "$E/.github/scripts/smoke-local.go" "$E/.github/scripts/smoke-local_test.go" -run '^TestCPAPluginIntegration$' -timeout 600s
CPA_SMOKE_INTEGRATION=1 CPA_SMOKE_CPA_BIN='C:/Users/user/Downloads/cpa-plugin/dist/integration/cpa.exe' CPA_SMOKE_PLUGIN="$F15_FIXED_DLL" go -C "$E" test -mod=readonly -count=1 -v "$E/.github/scripts/smoke-local.go" "$E/.github/scripts/smoke-local_test.go" -run '^TestCPAPluginIntegration$' -timeout 600s
```

RED只认可真实mapped结果和内容失败；编译、启动、DLL加载或TempDir清理错误应单独修正，不能当RED。真实native不关闭checkptr或race；Windows loader的已知限制按E既有要求如实报告，root生命周期另跑race。

### G5：自审、独立审查、修正复审与任务提交

- [ ] 自审逐项对应 F15 绑定规格：两类输入和单terminal控制、generic wire 标准 delimiter 和 Responses callback/迟到 delimiter 的各自时机、固定 C reviewBASE 与完整整合起点、16MiB、JSON/opaque/metadata/ownership、错误与batching、真实层级、性能扫描与allocation、署名和发布边界。本规格修正未独立审查通过，或同一实际入口出现可靠相反要求时，按 G2 停止，不提交产品 fix。
- [ ] 检查diff仅涉及G-owned范围，没有新增parser/provider/config/dependency，没有改A/B/D或CPA/module cache；确认测试expected独立于产品输出。

```bash
git -C "$G" diff --check
git -C "$G" diff "$startHEAD" -- main.go main_test.go stream_native_fields_regression_test.go performance_regression_test.go
```

- [ ] 向独立reviewer交付固定 reviewBASE=`51b006438c1a0edd4263020297747f99a174c47c`..全部最终 HEAD 的累计完整 diff、完整 spec/plan、单独标明的文档范围及真实命令结果；修正后仍覆盖同一 BASE..全部 HEAD。reviewer必须核对所有共享Write/Flush/Finish调用者、field-pair与data-only、候选/派发时点、generic wire 与 Responses output-unit 的实际入口及消费者语义、增量游标/reset、C的batch flag与chunks+error、真实E层级及署名。按既定workflow要求选择可用[1m]型号，完成后主动回报；草稿设计／文档整合阶段不派 agent；产品 G 的独立审查仍由既定 workflow 分配。
- [ ] 每项确认finding由G在同一worktree修正，重跑受影响focused/control，再跑root/vet/race；性能相关修正重跑allocation与bench。独立复审通过后才能提交。若审查发现信息冲突，回到G2停止条件，不通过降规格或省略控制取得GREEN。
- [ ] 产品仍RED且语义已解决时，最小共用修复与必要回归一起提交；C已覆盖时只提交缺失test，记录C的产品fix归属。下列subject按实际分支二选一，不产生未修改文件变更。

```bash
git -C "$G" add -- main.go main_test.go stream_native_fields_regression_test.go performance_regression_test.go
git -C "$G" commit -m "fix: preserve native Responses SSE field boundaries" -m 'Refs: #8
Related-PR: #7
PR-Author: @leolmq'
```

C已修复、G无产品diff时subject为 `test: cover native Responses SSE field boundaries`，body相同。不编造Co-authored-by姓名/email，不改写旧历史。

```bash
git -C "$G" show --stat --oneline HEAD
git -C "$G" status --short
```

- [ ] G主动回报commit、reviewBASE、复审结果、所有测试/性能结果与E接收内容；E在自己的worktree提交唯一入口/fixture及完成永久baseline RED/fixed GREEN。只提交本任务相关文件，不push、不评论、不Release、不提前关闭issue。实际Release成功与资产核验后由协调者填最终comment/版本。
- [ ] 删除本任务创建且不需保留的OS临时输入/模块副本，保留需要复查的JSON和diff在已忽略的专用目录，不删除已有文件。

### 历史草稿核验与本次检查范围

原补充草稿通过 Git archive 固定源码，产品和永久 tests 只读；原编译、真实 RED、root/vet 和正常控制 race 记录保留在原报告。

本次文档任务在固定 `6c88bcf4a92854a999d1d878d47f1b0ab16dcb5c` 的未修改产品上编译完整更正 root/CPA 草稿。新 generic wire、whole metadata 后 completed、单 terminal data-only、格式控制及其 race 通过，原 root suite 和新草稿 vet 通过。完整 F15 仍为真实内容和派发时机 RED，包含两类两/九事件、field-pair 边界、模型恢复和 complete 限额；field-pair native benchmark 的 byte-exact preflight 失败，data-only 单大单位仅为正常性能控制。实际命令、exitCode、原消费者对照、hash 和临时副本清理记录位于文档任务自有 ignored 目录的 JSON。

真实CPA草稿编译exit0。两种builtin的core/host producer控制和无mapper的HTTP handler/framer控制实际运行exit0；确认两类1/9事件、六种transport、完整payload/output及模型。映射native DLL/HTTP新草稿本轮没有运行；已保存producer trace/归档HTTP与framer证据提供原实际RED，新永久baseline/fixed由E执行。

旧候选实验仅在固定源码测试实例设置现有scanner状态，没有改产品；第3次Write提前输出125 bytes。旧全buffer header helper扫描probe，2MiB约90ms、8MiB约1.44s且零allocation。新增native续写benchmark的field-pair preflight为目标RED，data-only正常单单位计时约9.05ms/37.97ms，B/op与allocs/op已保留为控制，未将其称为多事件修复。

固定 C 最终审查提交为 `51b006438c1a0edd4263020297747f99a174c47c`；本草稿不含产品实现，原 Linux/Docker 现场唯一根因未确认。现行输入层级以综合消费者核验为依据，本次文档修正须独立审查通过；G 从含全部已整合修复的交付 HEAD 执行有限核查与必要完整 TDD，最终 review 覆盖固定 C..G 全部 HEAD。

## Task E：永久 actual CPA integration 和完整终验

**Files:** 修改 `.github/scripts/smoke-local_test.go`，创建唯一永久 fixture `.github/scripts/testdata/cpa-functional-regression_test.go`；仅实际需要时修改 `Makefile`、`.github/workflows/build.yml`。

**Interfaces:** 主入口保持 `TestCPAPluginIntegration` 和现有 `CPA_SMOKE_INTEGRATION`、`CPA_SMOKE_CPA_BIN`、`CPA_SMOKE_PLUGIN`。复用 `smokeEnv`、`prepareDirs`、`copyFile`、`buildConfig`、`startCPA`、`waitReady`、`stopCPA`；不引入新的 live service 或真实 key。

### E1：保存 baseline，并证明新增实际回归 RED

- [ ] 在 A/B/C/D/G 完成并合并后的独立 E worktree 建立 `dist/functional-baseline/`、`dist/functional-baseline-f15/` 和 `dist/functional-fixed/`，这些目录已被 `dist/` ignore 覆盖。从 E 自身 Git objects 导出固定完整 SHA 的 Git archive，用标准库 archive parser 提取到本任务的 OS 临时源码副本，构建 DLL 后保存到 E 自有的上述 ignored 目录，运行结束清理临时副本，不回退或修改当前 E worktree。F01..F14 baseline 为 `7855e55904f9a208ef915aa7878b77db8577a294`；F15 baseline 为 `6c7f060f4da5bb33e7b2ecd74c44499c9676a93c`，分别记录 source hash、构建参数和资产身份，不混用两个 baseline。

```bash
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -trimpath -buildmode=c-shared -o dist/functional-baseline/model-mapper.dll .
go version -m dist/functional-baseline/model-mapper.dll
go version -m C:/Users/user/Downloads/cpa-plugin/dist/integration/cpa.exe
```

baseline build 命令在对应固定 SHA 的 OS 临时源码副本执行；把产物保存到 E 的对应 ignored 目录。逐个核对 archive 源码与自身固定 Git object，记录源码 commit 与构建参数，不能仅靠 DLL 的外层 VCS metadata 推断源码位置。

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
| `pr7-core-headers-terminal` | 同一 native fixture 的正常 OpenAI raw core、四类 host headers、最后一个 payload 后独立 Done；单 payload 的完整内容为 `hello`，mapped/unmapped 模型正确，零插件 framing/DONE。HTTP wire 的验收沿用 `chat`、`completions`。 |
| `responses-terminal-model-restoration` | 原规则 `grok-4.6=>grok-4.7` 的完整 `response.completed`；上游请求 Model/顶层 model、下游 response.model、完整 output/opaque 文本、mapped/unmapped/禁用 plugin HTTP 200 和可派发 terminal；正常 native producer 与 C 的组合 terminal flags 分层核对。 |
| `responses-builtin-field-chunks` | 真实 `NewXAIExecutor`／`NewCodexExecutor` 的两个无 LF field chunks、18 字段九事件、XAI core 空 chunk、正常独立 Done；同一 fixture 增加单 terminal data-only 一帧和九个独立无 LF data-only payload 九帧的对照。mapped/unmapped/禁用 plugin 的 producer/native/framer/HTTP 分层核对完整 delta/output、模型、opaque、`response.completed`、原错误及既有分片。 |
| `responses-request-body-controls` | 每组独立 CPA/native DLL 进程，禁用 plugin 的 `grok-4.6`、启用原规则的 `grok-4.6`、相同配置直接 unmatched `grok-4.7`；非流／流的完整 raw/parsed body、非 model member 顺序／字节、opaque、headers 与 HTTP 结果对应。 |
| `responses-http` | OpenAI-compatible 的九条正常 Responses lifecycle，HTTP SSE 可派发、created/completed 模型正确；markerless delta 保留。 |
| `responses-truncated-prefix` | actual native Codex 的有效 created 后 e/ev/eve/even/event，终止时保留有效事件和原错误；完整 `event:` 对照。 |
| `responses-large-complete` | 257 个 64 KiB 普通文本 delta，使真实 translator 生成已测 16,842,937 字节 done；mapped/unmapped 完整文本及 completed；不以降低输出长度绕过。 |
| `claude` | mapped/unmapped messages 非流/流；标准 events、message.model、opaque/tool 文本；合法 colonless unknown fields 保持事件数。 |
| `gemini` | mapped/unmapped 非流、SSE 与 raw JSON array 流；immediate modelVersion 恢复、nested content 原样、无二次 framing。 |
| `interactions` | 无 agent 的 OpenAI-compatible 六条 JSON logical event 加 done；event_type/name、interaction.model、markerless step 内容；native proper SSE 对照。 |
| `interactions-agent` | 非空 agent 的 native 非流/流，路由未处理，行为与未启用映射的控制相同。 |
| `count-tokens`、`reconfigure-reload` | 现有 Claude guard，正常注册、reconfigure、reload 和 clean stream 对照；不新增 Gemini countTokens 辨别能力。 |

- [ ] 公共报告验收依赖 C 最终已验证提交以及 G 完成有限接口语义核查、必要 TDD／审查／复审后的最终提交；G2 未满足时 E/F 不放行。新增缺陷的正式交叉核验已完成，证据见下文。全部新增永久 integration 由本任务的唯一入口和唯一 CPA fixture 管理。PR7 的 HTTP matrix 在 E1 的同一 fake upstream 分别使用缺少 `Content-Type`、`text/event-stream`、`text/event-stream; charset=utf-8`、`application/json`，记录真实 translator/host 后实际到达插件的 headers/core bytes。两个 HTTP endpoint 的 mapped/unmapped 流均检查 200、完整 `onetwo`、对应 model、可派发 JSON SSE、`data: data:` 为零、DONE 恰好一次。native core/header 对照用真实 loader、ABI callback 和 host bridge，给正常可控 executor 的 raw JSON `delta.content=hello` 和四类 headers，精确检查 `hello`、对应模型和零插件 SSE/DONE；最后一个有效 payload 后关闭 producer，记录真实 host read 的非空 Payload/Done=false，再记录空 Payload/Done=true。复用 CPA parser 和现有 validators，HTTP 侧的 header 归一化据实际路径报告，不将上游缺失 header 等同于插件侧缺失。后续 PR7 相关 E 永久回归／复审修正提交按 C1 约束在 E3 的既有 `git commit` 命令追加 `-m $'Related-PR: #7\nPR-Author: @leolmq'`，提交后用 `git log -1 --format='%H%n%B'` 核对实际正文。
- [ ] issue8 在临时 CPA 与同一 mock upstream 的正常 provider 配置注册 `grok-4.6`／`grok-4.7`，对照禁用 plugin 请求 `grok-4.6`、启用原规则 `grok-4.6=>grok-4.7` 请求 `grok-4.6`、相同启用配置直接请求 unmatched `grok-4.7`。每组独立 CPA/native DLL 进程、相同请求序列，保留非流／流控制；避免会话 reasoning replay 影响比较。使用下列正常完整 terminal fixture，末尾包含派发空行；native 对照的 `Format/SourceFormat` 均为 `openai-response`。

```plaintext
event: response.completed
data: {"type":"response.completed","response":{"model":"grok-4.7","output":[{"type":"message","content":[{"type":"output_text","text":"grok-4.7"}]}]}}

```

启用 plugin 的 mapped/unmatched 上游请求 `Model` 和顶层 JSON `model` 均为 `grok-4.7`，每次请求调用 upstream 一次；fixed 三组正常 HTTP 均须为 200，JSON data 与 `response.completed` 均有效可派发，mapped `response.model=grok-4.6`、unmatched `response.model=grok-4.7`。禁用组不执行映射，按实际请求模型参数化 terminal fixture 的 `response.model`，其余 output 保持同样内容并独立记录模型预期。逐项精确比较完整 `response.output`，包括 message/content 类型、数组顺序和文本 `grok-4.7`，opaque 文本不改。C5 的三个组合 terminal flags 属于可控 ABI 接受对照；E 在真实 native fixture 核对正常 payload 后独立 Done，并在真实 bridge 可达的错误路径保留 `probe upstream error` 和已接受的完整 payload，报告实际 read/terminal 序列及 error 通道。mock、native、完整 HTTP 的结果分别记录，fake callbacks 不作为 Linux/Docker 现场复现。上述用例随 E1/E3 的既有 baseline/fixed integration 命令执行；PR7 必须得到目标 RED/GREEN，C5 的完整 SSE terminal 控制在两者均保持 GREEN，issue8 的 native/HTTP 基线结果据实记录。

- [ ] 完整读取已返回的正式报告 `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/.superpowers/sdd/2026-10-02-functional-fixes-implementation/issue8-alternative-verification.json`。报告记录 `originalIssueReproduced=true`、`commentAllowed=false`，独立重建并重跑 `6c7f060` Windows DLL／固定 CPA，确认真实 XAI/Codex 的无 LF 字段边界缺陷和原文 mapped HTTP 502。官方 `v0.5.8`／`v0.5.11` Windows DLL 与 Debian WSL2 Linux `.so` 的结果已依据完整结构化证据、保存的 transcript 和原始工具输出交叉核对；原发布资产及原始 HTTP JSON 已清理，交叉核验没有重跑 Linux matrix 或报告者 Docker，Linux `.so` 的 dirty build metadata 不证明源码字节等同于 clean tag。该受测 HEAD 的缺陷已正式确认，C 后续最终修复 HEAD／首个 Release 与报告者部署的唯一根因仍未确定。无 LF 输入与上面的完整 SSE 分开检查，连续两次 host read 的 payload 原样如下，随后为正常空 Payload／独立 Done；XAI core 的额外空 chunk 与实际 host 层过滤分别记录并保留控制：

```go
reads := []pluginapi.HostModelStreamReadResponse{
    {Payload: []byte("event: response.completed")},
    {Payload: []byte(`data: {"type":"response.completed","response":{"model":"grok-4.7","output":[{"type":"message","content":[{"type":"output_text","text":"grok-4.7"}]}]}}`)},
    {Done: true},
}
```

- [ ] 复用同一 CPA fixture，把本地 mock upstream 接入真实 `NewXAIExecutor`／`NewCodexExecutor`，经实际 Manager/BaseAPIHandler、validator、native loader/ABI/host bridge 和 `/v1/responses`，保存真实 producer payload、host read 与插件输出，不能以 fake callback 输入代替 builtin producer 验收。completed-only 两字段与既有正常九事件 lifecycle 的 18 字段各自精确比较完整 output、模型和 opaque；mapped/unmatched、禁用 plugin 的 direct 与 same-input alias（同一 `grok-4.7` upstream）分别保留正常非流、HTTP 状态、有效 data/terminal 与原错误控制。LF/CRLF、七字节／单字节网络 writes 和 charset 对照沿用已有调查，网络分片与实际 host chunk 分别记录；既有 C 分片、metadata 和 incomplete 条件不变，不把任意 host read 当 event delimiter。缺陷正式核验已完成；在 C 最终已验证 HEAD 顺序完成后续回归／必要修正，再用 E1/E3 同一 baseline/fixed 命令证明真实 producer 的目标 RED/GREEN；仍 RED 时由共享 rewriter 所有者连续修正及复审，E/F 不以旧 terminal 控制 GREEN 放行，也不新建第二永久入口。
- [ ] `responses-builtin-field-chunks` 的同一 fixture 增加两个 data-only 控制，不向输入补 LF 或预合并。单 terminal 仅提供一个 `data: ` 后接上面完整 terminal JSON 的 payload，随后独立 Done，必须保持一个有效可派发帧、mapped `response.model=grok-4.6`、unmatched `grok-4.7`，完整 output 与 opaque 文本原样。九事件控制复用同一固定 lifecycle 的九个完整 JSON，各以独立且无 LF 的 `data: ` payload 输入，顺序精确为 `response.created`、`response.in_progress`、`response.output_item.added`、`response.content_part.added`、`response.output_text.delta`、`response.output_text.done`、`response.content_part.done`、`response.output_item.done`、`response.completed`；必须得到九个有序、JSON 有效且可派发的数据帧，逐项精确检查完整 delta/output、所有允许恢复的模型字段和 opaque 内容，不用 emit 次数代替帧数。保留的 `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/.test-cpa/issue8-crosscheck/published-final-native.output:727-759` 显示两版发布 DLL 的单 terminal data-only 为一帧且模型恢复，九个原生 data-only payload 为九帧、经 mapper 后为零帧；这是 native／真实 Responses framer 证据。E 分别保存真实 producer、native bridge、Responses framer 和实际 HTTP 的 baseline/fixed 输入与结果，framer 结果不能填写为未经运行的 HTTP 结果。两组随 E1/E3 的唯一 integration 入口和既有 CPA overlay 命令执行；单 terminal 正常对照保留 GREEN，九事件目标须证明实际 RED/GREEN，完整 SSE 和合法分片控制不变。
- [ ] 请求体检查复用已保存的临时 CPA/mock upstream 调查和本入口三组控制，捕获真正到达 upstream 的完整 raw/parsed body、非 `model` member 顺序／字节表示、opaque、相关 request/response headers、URI、HTTP 状态和完整错误，按请求对应比较；解析用现有 CPA parser／`encoding/json`，原始字节必须另保留。请求体独立核验已完成，正式报告 `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/.superpowers/sdd/2026-10-02-functional-fixes-implementation/issue8-request-body-independent.json` 记录 `originalIssueReproduced=true`：独立重跑真实 Windows DLL／固定 CPA／HTTP 后，xAI mapped 流返回 HTTP 502 `upstream stream closed before first payload`，direct／disabled 流及非流对照正常；正常 minimal-message 的 mapped/direct-unmatched 上游 raw body 和正常响应逐字节相同，捕获 headers/URI 一致，未确认额外请求体改写缺陷。两种 native 方法的整体交叉核验已由上述正式报告确认无 LF 字段边界缺陷；报告者 Linux/Docker 的唯一根因尚未匹配，最终相关修复 commit／首个 Release 尚未确定。当前跳过无法复现评论；E 仍按本入口和 E1/E3 的既有命令完成永久 baseline/fixed 验收，据实记录各组实际结果及差异，不把差异本身判为缺陷或原 502 根因，不重新分派重复调查／产品任务。临时进程和 OS 临时目录按既有 helper 清理，所需证据保存到 E 自有 ignored workspace。

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

- [ ] F15 的完整永久输入、Go fixture、两组 DLL 身份核验和 baseline/fixed 命令沿用 G4，合入本任务的唯一 fixture／入口。原公共报告的 disabled same-input alias 控制和历史结果继续保留；G4 新矩阵注册实际 `grok-4.6`／`grok-4.7`，disabled 按实际请求 model 验收，不用 alias 替换其 upstream model。两种真正 builtin 的 field-pair/data-only 各含 1/9 事件和六种 transport，mapped/direct-unmatched/disabled `.6/.7` 及全部非流逐项检查完整 JSON、事件顺序、delta/output、opaque、model、HTTP 200。core/host 与无 mapper HTTP 控制 GREEN、新 native DLL 草稿仅编译、新永久 baseline RED／fixed GREEN分别记录。

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

- [ ] F15 同时重跑 G3 的两类 native 2 MiB/8 KiB allocation（<=200）及 2 MiB、8 MiB／8 KiB continuation benchmark。先通过 byte-exact preflight 和 ownership，再比较同环境 ns/op、B/op、allocs/op，并核查候选记录后的增量游标；BASE 的错误 field-pair 输出不参与正确输出计时，不能用正常单 data-only 控制代替两类新目标。

- [ ] 实际 Windows/Linux 构建打包和 compatibility。Linux 使用已有可用 Zig compiler，保持 GLIBC target，打包器在 host Go 环境运行。

```bash
make package VERSION=0.5.12 GOOS=windows GOARCH=amd64
make package VERSION=0.5.12 GOOS=linux GOARCH=amd64 BUILD_CC="zig cc -target x86_64-linux-gnu.2.17"
```

版本 `0.5.12` 为候选构建标签；发布前再核对是否被占用。核对 DLL/.so 的 metadata、sidecar、zip 根目录、LICENSE 和 sha256 checksum，不修改开发默认版本。

- [ ] 独立 reviewer 逐项检查规格表 F01..F15 的归属和控制条件，确认 F13 无独立生产任务、F14 无生产网络分片误述、F15 已满足 G2 语义条件且两类 native 永久矩阵完整、每个 layer 的实际结果据实记录。完整核对 G4 的两类 1/9 事件、六种 transport、全部 route／非流、两个 baseline 来源，以及编译、core/host／无 mapper HTTP 控制和新 native DLL GREEN 的区别。覆盖不足由 E 在自己的 worktree 补查和复审，不交给主会话代写。
- [ ] `git diff --check` 后仅提交 E 实际修改的 integration/CI 文件，永久 fixture 必须随同入口提交：`git add .github/scripts/smoke-local_test.go .github/scripts/testdata/cpa-functional-regression_test.go && git commit -m "test: cover model mapper functional paths in CPA"`。Makefile/CI 只有实际必要修改才加入。

## G/E 后续任务：普通 metadata output units

本节落实 spec 的普通 metadata output units 要求，补充 G/E 的完整验收。原 A..G、E1..E3 和 F 的要求继续适用；当前 `stream_native_fields_regression_test.go`、唯一 CPA fixture 和 smoke 入口已经存在，按本节精确追加或修改，不重复创建。编码须等待本次两文档的累计独立审查通过。文档任务仅验证草稿与未修复 START，不修改产品、tracked tests 或永久 fixture。

### 固定输入、所有权与执行顺序

- metadata baseline 固定为 `3dce8db7373d2da6cfd792a30e2c323e0525bdc5`。F01..F14 baseline=`7855e55904f9a208ef915aa7878b77db8577a294`、原 F15 baseline=`6c7f060f4da5bb33e7b2ecd74c44499c9676a93c` 继续保留。G 原累计产品 reviewBASE=`51b006438c1a0edd4263020297747f99a174c47c` 不变。
- G-M 只拥有 `main.go` 的共享 Responses rewriter/scanner 必要修改、`stream_native_fields_regression_test.go` 和确实受影响的协议／性能 fixture。不改 A/B 的路由／JSON helper、C 的 executor emit/flush helper、D 的 lifecycle/close 或 CPA。
- E-M 只拥有 `.github/scripts/testdata/cpa-functional-regression_test.go` 与 `.github/scripts/smoke-local_test.go`。复用当前 producer、helpers、loader、capture generator 和唯一 `TestCPAPluginIntegration`，不增加第二入口、fixture、parser 或依赖。Makefile/CI 的原显式文件命令无需因扩大同一 matrix 改变。
- G-M 与 E-M 各在新的 native 允许 worktree 执行。E-M 可并行准备独立完整草稿及 START DLL 的 baseline RED；最终 GREEN、独立审查和任务提交依赖 G-M 的已验证产品及全部原整合修复。协调者只接收完成且范围无冲突的提交，不代写剩余步骤。产品任务的独立 reviewer 由既定 workflow 安排，使用可用 `[1m]` 型号并主动回报完成。
- 各自开始时记录完整固定 `taskBASE` 和执行起点 SHA，之后保持不变。G-M 的额外 metadata review 至少覆盖固定 START..全部最终 HEAD，并保留固定 C..全部最终 HEAD 的原产品审查；E-M 的 taskBASE 为协调者实际交付的、包含已完成 G-M 的完整整合起点。独立草稿移入该起点后完整重跑。禁止用 `HEAD~1` 或最后一个修正提交代替累计范围。

### G-M1：根目标 RED 与原正常控制

- [ ] 核查分配目录、clean、branch、完整交付 SHA 和固定 START/C ancestry，读取 spec 与本节。使用自身 Git objects 的固定 START archive 在 OS 临时目录构建 baseline；使用成熟 archive parser，不执行 Git 回退，不修改其他 worktree 或 module cache。稳定证据保存自己已忽略的 `dist/` 专用目录。
- [ ] 在现有 `stream_native_fields_regression_test.go` 追加下列完整 Go 内容。草稿的 package/import 声明用于独立编译；合入时合并已有 imports，只追加函数。复用 `gNativeFields`、`gNativeParts`、`gRequireNativeFrames`、`gNativeLargeFixture`、`gNativeContinue`，不再复制这些 helper。它们检查精确字段边界、全部 payload、单个／连续 metadata、三个位置、两种形状、1/9 事件、Flush/Finish、ownership、真实 forwarder 错误／close 及 continuation。只有单 terminal data-only 容许已有的最后 blank delimiter 差别，其余精确比较完整输出。

```go
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
```

- [ ] 在任何产品 Edit 之前先 gofmt 和编译，再运行相同 focused 命令。当前 START 的目标输出是 metadata/event/data 被拼成一行、真实 dataFrames 为零且 JSON model 未恢复；Forwarder 的有效输出缺失会同时使 emit-error 控制未触发。记录具体内容差异，不把编译、启动、缺环境或不适用的旧序列化期望计为 RED。none、Wire 和原正常控制分别记录。

```bash
G=$(git rev-parse --show-toplevel)
metadataBASE=3dce8db7373d2da6cfd792a30e2c323e0525bdc5
startHEAD=$(git -C "$G" rev-parse HEAD)
git -C "$G" merge-base --is-ancestor "$metadataBASE" "$startHEAD"
gofmt -w "$G/stream_native_fields_regression_test.go"
go -C "$G" test -mod=readonly . -run '^$'
go -C "$G" test -mod=readonly -count=2 -v . -run '^TestFunctionalNativeSSEFieldOrdinaryMetadata' -timeout 240s
go -C "$G" test -mod=readonly -count=1 -v . -run '^TestFunctionalNativeSSEField(Boundary|Sequence|DataOnlyTerminalControl|Dispatch|LateDelimiter|LateDataDelimiter|WireControls|GenericCanonicalControls|ResponsesMetadataControl|MetadataOutputUnits|MetadataForwarder|FormatIsolation|Discriminator|DataOnlyTypeControl|Limit|Continuation|ContinuationAllocations|InputOwnership)$' -timeout 300s
```

### G-M2：共享修正、GREEN、性能与累计审查

- [ ] 有限核查 `(*streamChunkRewriter).Write/Flush/Finish`、`(*sseRewriter).Write/drain`、`findDelimiterlessResponsesEventEnd`、scanner/reset，以及实际调用者 `processPayload`、`flushAndEmit`、`runStreamForward`。当前 `sseRewriter.Write` 仅在新 `data:` 与已记录 response header/data 候选之间恢复边界，metadata 起首使 scanner disabled，后续字段直接 append。在同一共享 output-unit 状态中保留独立 metadata 的原 bytes 和真实字段末尾，使真正 event/data 继续走已有 discriminator、增量定位及 `rewriteEvent`。完整值定位、验证、候选保存与派发分别保持明确时点；已记录候选不从零重复扫描整个 JSON。
- [ ] 修正必须同时通过 metadata 起首、event 中、事件之间和连续 metadata；metadata 本身不成为 event/type/JSON discriminator。保留原 field-pair `0、1、1、2` 时机、data-only 下一 data/Flush 时机、迟到 delimiter、canonical mismatch、whole event-only opaque metadata、generic wire 的全部 split/bytewise/LF/CR/CRLF/BOM 和标准 delimiter 等待。不要对 generic 任意 read/网络片段补 LF；不添加 provider/substring 推断、ABI flag、配置、parser、timeout、fallback 或 HTTP/WS 各自处理。真实消费者的相反条件仍按 G2 停止并交回具体证据，不降低要求。
- [ ] 保持六项模型白名单、16 MiB incomplete 限额、完整 prefix/chunks+error、当前 batching、输入／输出 ownership、自然 EOF、Payload+Done、in-band/callback/emit/cleanup errors、host close 一次及原 plugin close 策略。root callback mock 只隔离 host 调用，不代替 E-M 的真实链路。
- [ ] 产品修正后重新运行 G-M1 的原样 focused/control 命令，全部 GREEN；然后运行完整根测试、vet、race 和既有注册／schema。root race 使用真实检查参数，普通 native DLL 的集成另外运行，不关闭 checkptr/race。

```bash
go -C "$G" test -mod=readonly -count=1 ./...
go -C "$G" vet -mod=readonly ./...
go -C "$G" test -mod=readonly -race -count=1 ./... -timeout 600s
go -C "$G" test -mod=readonly -count=1 . -run 'TestPluginRegistrationMetadataAndConfigFields|TestPluginRegisterKeepsSchemaOneForNewerHost|TestHandleMethodDispatchesRegisterReconfigureAndUnknown'
go -C "$G" test -mod=readonly . -run '^$' -bench '^Benchmark(StreamChunkRewriter(NativeFieldContinuation|OrdinaryMetadataContinuation)|SSEMarkerGuard(Restore|Candidate))$' -benchtime=1x -count=1 -benchmem
go -C "$G" test -mod=readonly . -run '^$' -bench '^Benchmark(StreamChunkRewriter(NativeFieldContinuation|OrdinaryMetadataContinuation)|RewriteTopLevelModel|RestoreResponseModel|RestoreResponseWithoutModel|ResponseModelMarkerScan|SSEMarkerGuard(Restore|Candidate)|StreamChunkRewriter(FragmentedRawJSON|SingleJSON|CompleteSSEBatch|EscapedSSEBatch|UnicodeEscapedSSEBatch)|EmitRewrittenBatch)$' -count=5 -benchmem
```

- [ ] 受影响 benchmark 先通过 byte-exact preflight，再在同机器、Go、构建参数下计时。metadata 与 none 的两类 2 MiB/8 KiB allocation <=200，2 MiB 与 8 MiB continuation 检查实际 ns/op、B/op、allocs/op 和增量游标。全部既有 allocation 门槛保持。START 的 metadata preflight 已失败，不计时错误输出、不把旧性能报告写成新 HEAD 数据；保留正式性能成本并据实际新增扫描／复制定位稳定回归，再修正复测。
- [ ] 自审逐项对应 spec、G-M1 全部测试和原 G3/G5，确认只修改 G-owned 内容。完成独立规格／质量累计审查、确认 finding 的修正和同 BASE..全部最终 HEAD 复审；每次产品修正重跑受影响 focused/control、root/vet/race，性能相关修正重跑 preflight 与计时。
- [ ] 提交实际 G-owned 修改，body 保留归属；提交后核对完整 message、scope、clean 和最终 HEAD，向 E-M/协调者主动交付实际修复 SHA、固定 BASE、测试／性能日志、累计 diff 和限制。没有产品 diff 时记录真实归属，只提交缺失回归。

```bash
git -C "$G" diff --check
git -C "$G" add -- main.go stream_native_fields_regression_test.go
git -C "$G" commit -m 'fix: preserve ordinary Responses metadata output units' -m 'Refs: #8
Related-PR: #7
PR-Author: @leolmq'
git -C "$G" log -1 --format='%H%n%B'
git -C "$G" diff --binary "$metadataBASE"..HEAD
git -C "$G" diff --binary 51b006438c1a0edd4263020297747f99a174c47c..HEAD
```

必要协议／性能 fixture 有实际修改时仅把对应文件加入同次提交；不 stage 无关内容，不改写旧提交署名。

### E-M1：唯一 fixture、真实消费者与 metadata 矩阵

- [ ] 先在自己的独立草稿和 OS 临时可写 CPA v7.2.152 副本准备测试，保留原模块只读。固定 module Sum、integration revision、真实 `.6/.7` model 注册、source/copy/shadow hash、有效 enabled/disabled 状态沿用当前 loader/process helper。准备阶段可对 START DLL 运行 RED；接收 G-M 已验证产品后，从交付完整起点记录固定 E-M taskBASE，并在自己的 worktree 完成全部 GREEN、自审、修正、独立复审和提交。
- [ ] 在唯一 CPA fixture 用下面完整内容替换 `gCPAFieldsFixture`、`gCPAFieldsFixtures`、`gCPAFields`；现有 helper/测试及全部断言保留，`TestModelMapperFunctionalNativeSSEFields` 的单 terminal data-only 分支按本节精确修改。共有 96 个 fixture，每个 metadata 行在对应 event/data 前生成。固定 six transports 不变，none 保留原完整组合。循环的 labels 同 binary generator 保持逐字一致。

```go
type gCPAFieldsFixture struct {
	name, eol, contentType, metadata string
	step                             int
	dataOnly                         bool
	count                            int
}

func gCPAFieldsFixtures() []gCPAFieldsFixture {
	var out []gCPAFieldsFixture
	for _, dataOnly := range []bool{false, true} {
		for _, count := range []int{1, 9} {
			for _, transport := range []gCPAFieldsFixture{
				{name: "LF-whole", eol: "\n", contentType: "text/event-stream"},
				{name: "CRLF-whole", eol: "\r\n", contentType: "text/event-stream"},
				{name: "LF-7bytes", eol: "\n", contentType: "text/event-stream", step: 7},
				{name: "CRLF-bytewise", eol: "\r\n", contentType: "text/event-stream", step: 1},
				{name: "LF-charset", eol: "\n", contentType: "text/event-stream; charset=utf-8", step: 11},
				{name: "CRLF-charset", eol: "\r\n", contentType: "text/event-stream; charset=utf-8", step: 7},
			} {
				for _, metadata := range []string{"", "id: event-1", "retry: 100", ": heartbeat"} {
					f := transport
					f.dataOnly, f.count, f.metadata = dataOnly, count, metadata
					label := metadata
					if label == "" {
						label = "none"
					}
					f.name = fmt.Sprintf("dataOnly=%v/count=%d/%s/metadata=%s", dataOnly, count, f.name, label)
					out = append(out, f)
				}
			}
		}
	}
	return out
}

func gCPAFields(f gCPAFieldsFixture, model string) ([]string, []byte) {
	var fields []string
	var wire bytes.Buffer
	for _, payload := range gCPAPayloads(model, f.count) {
		var event struct{ Type string }
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			panic(err)
		}
		if f.metadata != "" {
			fields = append(fields, f.metadata)
			fmt.Fprintf(&wire, "%s%s", f.metadata, f.eol)
		}
		if !f.dataOnly {
			fields = append(fields, "event: "+event.Type)
			fmt.Fprintf(&wire, "event: %s%s", event.Type, f.eol)
		}
		fields = append(fields, "data: "+payload)
		fmt.Fprintf(&wire, "data: %s%s%s", payload, f.eol, f.eol)
	}
	return fields, wire.Bytes()
}
```

- [ ] 在 `gCPARequireEvents` 的 `t.Helper()` 后加入下面检查，之后完整保留 `sse.Decode`、event/type、全部 JSON、output/model/opaque 和 DONE 检查。只对成熟 decoder 输入统一已知 CRLF；原始 bytes 另存。

```go
if f.metadata != "" {
    normalized := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
    if got := bytes.Count(append([]byte("\n"), normalized...), []byte("\n"+f.metadata+"\n")); got != f.count {
        t.Fatalf("metadata count=%d want=%d bytes=%q", got, f.count, raw)
    }
}
```

- [ ] 在 `TestModelMapperFunctionalNativeSSEFields` 中，用下列完整代码替换日志之后的原 `if f.dataOnly && f.count == 1` 分支。直接 JSON 断言只处理无 metadata 的单 terminal；有 metadata 时复用已补充原文字节检查的 `gCPARequireEvents` 和现有 `sse.Decode`，核对 metadata 字段边界、真正 data、event/type、client model、全部 completed output 与 opaque。不筛除任何 provider/transport/metadata 组合，不修改正确产品输出迁就测试。none 的无最终 blank delimiter 控制保持；既有 decoder 支持 EOF 派发。

```go
if f.dataOnly && f.count == 1 && f.metadata == "" {
	// 无 metadata 的单 terminal output unit 保留无最终 blank delimiter 控制。
	var event struct {
		Type     string
		Response json.RawMessage
	}
	payload := bytes.TrimSpace(bytes.TrimPrefix(raw.Bytes(), []byte("data: ")))
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "response.completed" {
		t.Fatalf("terminal type=%q", event.Type)
	}
	gCPARequireResponse(t, event.Response, "grok-4.6")
} else {
	gCPARequireEvents(t, raw.Bytes(), f, "grok-4.6")
}
```

- [ ] 同步 ignored 完整 CPA 草稿并编译。文档任务在 OS 临时副本中，从该完整草稿逐字提取上面的断言分支作为验证测试的函数体，复用 `gCPAFieldsFixtures`／`gCPAFields` 生成正确 client-model 输出；核查两个 provider 标签、全部 96 fixtures、保留与移除单 terminal data-only 最终 blank delimiter 的两组输入，以及含 `event:`／`data:`／model 字样的 opaque comment。该检查只证明测试断言接受正确预期输出，单独保存命令和完整日志，不计为产品或集成 GREEN，不新增永久入口／fixture。随后移除临时验证测试，以同一 E-M3 的 Native/OrdinaryMetadata 永久命令对 START DLL 记录真实产品 RED；G-M/E-M 修正后原样执行 GREEN。metadata 拼接、模型未恢复、完整 output／opaque 不符仍须触发原断言；none、direct、disabled 控制继续保留。

- [ ] 在唯一 CPA fixture 追加下列完整测试，合并草稿 package/import 声明所列的已有项，只追加函数，不创建第二文件。真实 core/host、native、direct HTTP/WS 与 mapped HTTP/WS 分为独立 subtest，目标 native 失败不会跳过 HTTP/WS。disabled WS 对两个实际模型独立检查，原 `NativeFieldsHTTP` 继续覆盖 disabled/mapped/direct 非流和流。metadata 位置控制运行真实未加载 mapper 消费链，固定 before-event/in-event/between-events；连续 opaque comment 不解析。G-M 完成后，还须把同位置的 mapped native/HTTP/WS 纳入相同测试，复用已给出的 producer、loader、HTTP/WS helpers 和精确预期，不删 direct 控制。新增 unknown 字段的 callback 支持只有原消费者实际支持证据成立时才增加，不以 generic wire 控制推断。

```go
package pluginhost

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestModelMapperFunctionalOrdinaryMetadata(t *testing.T) {
	for _, provider := range []string{"xai", "codex"} {
		for _, f := range gCPAFieldsFixtures() {
			t.Run(provider+"/"+f.name, func(t *testing.T) {
				p := gCPANewLocalProducer(t, provider)
				p.setFixture(f)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				gCPARequireFields(t, gCPACoreFields(t, p, ctx), f, "grok-4.7")
				gCPARequireFields(t, gCPAHostFields(t, p, ctx), f, "grok-4.7")
				server := eResponsesServer(t, p.base)
				t.Run("direct", func(t *testing.T) {
					status, _, raw := eHTTP(t, server, "/v1/responses", gCPARequest("grok-4.7", true))
					if status != 200 {
						t.Fatalf("direct HTTP=%d body=%q", status, raw)
					}
					gCPARequireEvents(t, raw, f, "grok-4.7")
					ws, err := eWS(t, server, "grok-4.7")
					if err != nil || !reflect.DeepEqual(ws, gCPAPayloads("grok-4.7", f.count)) {
						t.Fatalf("direct WS=%q error=%v", ws, err)
					}
				})
				host := gCPALoadNative(t, p, true)
				canonical := f
				canonical.eol = "\n"
				_, want := gCPAFields(canonical, "grok-4.6")
				t.Run("mapped-native", func(t *testing.T) {
					result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, eNativeRequest("grok-4.6", "openai-response", true))
					if err != nil {
						t.Fatal(err)
					}
					raw, errs := eNativeDrain(t, result.Chunks)
					t.Logf("native=%q errors=%q", raw, errs)
					comparable, expected := raw, want
					if f.dataOnly && f.count == 1 {
						comparable, expected = bytes.TrimSuffix(raw, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))
					}
					if len(errs) != 0 || !bytes.Equal(comparable, expected) {
						t.Errorf("mapped native bytes=%q errors=%q want=%q", raw, errs, expected)
					}
					gCPARequireEvents(t, raw, f, "grok-4.6")
				})
				p.base.SetPluginHost(host)
				p.base.SetModelRouterHost(host)
				t.Run("mapped-http", func(t *testing.T) {
					status, _, raw := eHTTP(t, server, "/v1/responses", gCPARequest("grok-4.6", true))
					t.Logf("mapped HTTP=%d body=%q", status, raw)
					if status != 200 {
						t.Fatalf("mapped HTTP=%d want=200", status)
					}
					gCPARequireEvents(t, raw, f, "grok-4.6")
					if !bytes.Equal(raw, append(bytes.Clone(want), '\n')) {
						t.Fatalf("mapped HTTP bytes=%q want=%q", raw, append(bytes.Clone(want), '\n'))
					}
				})
				t.Run("mapped-ws", func(t *testing.T) {
					ws, err := eWS(t, server, "grok-4.6")
					t.Logf("mapped WS=%q error=%v", ws, err)
					if err != nil || !reflect.DeepEqual(ws, gCPAPayloads("grok-4.6", f.count)) {
						t.Fatalf("mapped WS=%q error=%v", ws, err)
					}
				})
			})
		}
	}
}

func TestModelMapperFunctionalOrdinaryMetadataDisabledWS(t *testing.T) {
	for _, provider := range []string{"xai", "codex"} {
		p := gCPANewLocalProducer(t, provider)
		host := gCPALoadNative(t, p, false)
		p.base.SetPluginHost(host)
		p.base.SetModelRouterHost(host)
		server := eResponsesServer(t, p.base)
		for _, f := range gCPAFieldsFixtures() {
			for _, model := range []string{"grok-4.6", "grok-4.7"} {
				t.Run(provider+"/"+f.name+"/"+model, func(t *testing.T) {
					p.setFixture(f)
					ws, err := eWS(t, server, model)
					if err != nil || !reflect.DeepEqual(ws, gCPAPayloads(model, f.count)) {
						t.Fatalf("disabled WS=%q error=%v", ws, err)
					}
					p.mu.Lock()
					calls := append([]gCPAUpstreamRecord(nil), p.records...)
					p.mu.Unlock()
					if len(calls) != 1 || calls[0].model != model {
						t.Fatalf("disabled calls=%+v", calls)
					}
				})
			}
		}
	}
}

func TestModelMapperFunctionalOrdinaryMetadataConsumerControls(t *testing.T) {
	for _, provider := range []string{"xai", "codex"} {
		for _, dataOnly := range []bool{false, true} {
			for _, count := range []int{1, 9} {
				for _, metadata := range [][]string{{"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}} {
					for _, position := range []string{"before-event", "in-event", "between-events"} {
						t.Run(fmt.Sprintf("%s/dataOnly=%v/count=%d/metadata=%q/%s", provider, dataOnly, count, metadata, position), func(t *testing.T) {
							p := gCPANewLocalProducer(t, provider)
							f := gCPAFieldsFixture{count: count, dataOnly: dataOnly, contentType: "text/event-stream", eol: "\n"}
							p.fixture = f
							var fields []string
							var wire strings.Builder
							original, _ := gCPAFields(gCPAFieldsFixture{count: count, dataOnly: false, eol: "\n"}, "grok-4.7")
							for i, payload := range gCPAPayloads("grok-4.7", count) {
								name := original[i*2]
								add := func(value string) { fields = append(fields, value); wire.WriteString(value + "\n") }
								if position == "before-event" || position == "between-events" && i > 0 {
									for _, field := range metadata {
										add(field)
									}
								}
								if !dataOnly {
									add(name)
								}
								if position == "in-event" {
									for _, field := range metadata {
										add(field)
									}
								}
								add("data: " + payload)
								wire.WriteByte('\n')
							}
							p.wire = []byte(wire.String())
							ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
							defer cancel()
							if got := gCPACoreFields(t, p, ctx); !reflect.DeepEqual(got, fields) {
								t.Fatalf("core=%q want=%q", got, fields)
							}
							if got := gCPAHostFields(t, p, ctx); !reflect.DeepEqual(got, fields) {
								t.Fatalf("host=%q want=%q", got, fields)
							}
							server := eResponsesServer(t, p.base)
							status, _, raw := eHTTP(t, server, "/v1/responses", gCPARequest("grok-4.7", true))
							if status != 200 {
								t.Fatalf("direct HTTP=%d body=%q", status, raw)
							}
							gCPARequireEvents(t, raw, f, "grok-4.7")
							for _, field := range metadata {
								expected := count
								if position == "between-events" {
									expected = count - 1
								}
								if bytes.Count(raw, []byte(field+"\n")) != expected {
									t.Fatalf("metadata field=%q count=%d want=%d bytes=%q", field, bytes.Count(raw, []byte(field+"\n")), expected, raw)
								}
							}
							ws, err := eWS(t, server, "grok-4.7")
							if err != nil || !reflect.DeepEqual(ws, gCPAPayloads("grok-4.7", count)) {
								t.Fatalf("direct WS=%q error=%v", ws, err)
							}
							t.Logf("original consumer position=%s HTTP=%q WS=%q", position, raw, ws)
						})
					}
				}
			}
		}
	}
}
```

- [ ] mapped 位置检查在现有位置控制测试里完成：direct 子测试完成后加载 `gCPALoadNative(t, p, true)`、设置两个 host、调用真实 native/HTTP/WS，预期使用 `gCPAPayloads("grok-4.6", count)`；metadata 的每行出现次数与原控制相同。以下是追加在同一 position subtest 中的完整代码，所需符号来自唯一 fixture。

```go
host := gCPALoadNative(t, p, true)
requireMetadata := func(t *testing.T, raw []byte) {
    t.Helper()
    for _, field := range metadata {
        expected := count
        if position == "between-events" { expected = count-1 }
        if got := bytes.Count(raw, []byte(field+"\n")); got != expected { t.Fatalf("metadata %q count=%d want=%d bytes=%q", field, got, expected, raw) }
    }
}
t.Run("mapped-native", func(t *testing.T) {
    result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, eNativeRequest("grok-4.6", "openai-response", true))
    if err != nil { t.Fatal(err) }
    native, errs := eNativeDrain(t, result.Chunks)
    if len(errs) != 0 { t.Fatalf("native errors=%q", errs) }
    eRequireData(t, native, gCPAPayloads("grok-4.6", count))
    requireMetadata(t, native)
})
p.base.SetPluginHost(host)
p.base.SetModelRouterHost(host)
t.Run("mapped-http", func(t *testing.T) {
    status, _, mapped := eHTTP(t, server, "/v1/responses", gCPARequest("grok-4.6", true))
    if status != 200 { t.Fatalf("mapped position HTTP=%d body=%q", status, mapped) }
    gCPARequireEvents(t, mapped, f, "grok-4.6")
    requireMetadata(t, mapped)
})
t.Run("mapped-ws", func(t *testing.T) {
    wsMapped, err := eWS(t, server, "grok-4.6")
    if err != nil || !reflect.DeepEqual(wsMapped, gCPAPayloads("grok-4.6", count)) { t.Fatalf("mapped position WS=%q error=%v", wsMapped, err) }
})
```

### E-M2：1536 个 actual binary 组合与精确 capture 消费

- [ ] 在现有 `functionalNativeBinaryCaptures` 的局部 `fixture` 加 `metadata string`，局部 `capture` 加 `Metadata string`。仅替换 six transports 循环内原三行 fixture 追加代码为下列内容，不改变 provider、enabled、stream、model 循环和进程分组。每个 metadata label 与 CPA fixture 完全相同。

```go
for _, metadata := range []string{"", "id: event-1", "retry: 100", ": heartbeat"} {
    variant := f
    variant.dataOnly, variant.count, variant.metadata = dataOnly, count, metadata
    label := metadata
    if label == "" { label = "none" }
    variant.name = fmt.Sprintf("dataOnly=%v/count=%d/%s/metadata=%s", dataOnly, count, f.name, label)
    fixtures = append(fixtures, variant)
}
```

- [ ] 在同一 upstream wire 生成循环、现有 `if !f.dataOnly` 前加入 `if f.metadata != "" { fmt.Fprintf(&wire, "%s%s", f.metadata, f.eol) }`。保存 capture 时在现有 `record.Name, record.Fixture, record.ClientModel = ...` 后加入 `record.Metadata = f.metadata`。保留 raw/parsed body、非 model member bytes/order、request/response headers、URI、upstream response、calls/status 及 mapped/direct 比较；disabled `.6` 的现有非 model 比较只归一化模型，真实 upstream 仍 `.6`。
- [ ] 唯一 CPA fixture 的 `TestModelMapperFunctionalBinaryCaptures` 局部 capture 同步加 `Metadata string`。长度断言改为下面数值；原 expected map 的完整笛卡尔积、逐项 delete、重复／未知 key 和最后遗漏检查保留。identity 的原条件前增加 `capture.Metadata != f.metadata ||`。不能只检查总数，不能删任何原 payload/header/URI/upstream bytes 断言。

```go
if len(captures) != 1536 {
    t.Fatalf("binary captures=%d want=1536", len(captures))
}
```

- [ ] 1536 的维度逐项核对：XAI/Codex；field-pair/data-only；1/9；原六种 transport；enabled mapped `.6`、enabled direct `.7`、disabled `.6/.7`；流/非流；none/id/retry/comment。none 对应原完整 384 条。附加连续／位置／WS 用例由 E-M1 的同一永久 fixture 管理，不用 package 层结果代替 actual binary。
- [ ] 修改现有 `runFunctionalCPAOverlay` 的子进程 context 为 `660*time.Second`、Go `-timeout` 为 `600s`；唯一入口外层永久命令为 `-timeout 1800s`。只增加期限，不减少组合、不关闭 race/checkptr、不添加顺序 sleep。真实 HTTP/WS 单次 request 的现有 deadline 保持，必要时按实际耗时增加；deadline 只检测完成。

### E-M3：固定 baseline RED、最终 GREEN、终验与提交

- [ ] 从 E-M 自身 Git objects 导出三个固定 baseline 的 OS 临时源码，用成熟 archive parser 提取。新 metadata DLL 保存自己的 `dist/functional-baseline-metadata/model-mapper.dll`；默认开发版本不注入正式版本。baseline 使用 `-buildvcs=false`，逐个保存 source commit、source/archive SHA256、Go、构建参数、DLL SHA256 与实际 shadow SHA256。`go version -m` 的外层 stamp 不代替源码 bytes。最终 DLL 来自自己已整合 G-M 的实际 HEAD，分别核对 source/copy/shadow 和 registration/effective enabled。禁止修改原 CPA 或 module cache。
- [ ] 在已编译的同一永久 fixture 上对 START DLL 运行下面 RED，对最终 DLL 运行原样 GREEN。`CPA_MODULE_COPY` 和 `OVERLAY_JSON` 使用现有 `prepareFunctionalCPAOverlay` 创建的真实可写副本与 overlay 路径；源 fixture 是 E-M 的唯一 tracked 文件。保存完整日志，具体断言 native model/boundary、HTTP 502 和 WS 缺完整 completed；none/direct/disabled 正常结果保留。

```bash
E=$(git rev-parse --show-toplevel)
CPA_SMOKE_PLUGIN="$E/dist/functional-baseline-metadata/model-mapper.dll" go -C "$CPA_MODULE_COPY" test -mod=readonly -overlay "$OVERLAY_JSON" -count=2 -v ./internal/pluginhost -run '^TestModelMapperFunctional(OrdinaryMetadata|Native)' -timeout 600s
CPA_SMOKE_PLUGIN="$E/dist/functional-fixed/model-mapper.dll" go -C "$CPA_MODULE_COPY" test -mod=readonly -overlay "$OVERLAY_JSON" -count=2 -v ./internal/pluginhost -run '^TestModelMapperFunctional(OrdinaryMetadata|Native)' -timeout 600s
```

- [ ] 编码完成后的实际 binary 永久 RED/GREEN 分别执行下列相同入口和完整矩阵。完整原 F01..F14 与原 F15 baseline 命令也保留，以同一扩大后的入口、1800 秒期限执行并分层记录各自预期目标与正常控制。新 metadata 的目标 RED 绑定 START DLL，编译／启动／缺环境失败不计为产品 RED。

```bash
E=$(git rev-parse --show-toplevel)
CPA_SMOKE_INTEGRATION=1 CPA_SMOKE_CPA_BIN='C:/Users/user/Downloads/cpa-plugin/dist/integration/cpa.exe' CPA_SMOKE_PLUGIN="$E/dist/functional-baseline-metadata/model-mapper.dll" go -C "$E" test -mod=readonly -count=1 -v "$E/.github/scripts/smoke-local.go" "$E/.github/scripts/smoke-local_test.go" -run '^TestCPAPluginIntegration$' -timeout 1800s
CPA_SMOKE_INTEGRATION=1 CPA_SMOKE_CPA_BIN='C:/Users/user/Downloads/cpa-plugin/dist/integration/cpa.exe' CPA_SMOKE_PLUGIN="$E/dist/functional-fixed/model-mapper.dll" go -C "$E" test -mod=readonly -count=1 -v "$E/.github/scripts/smoke-local.go" "$E/.github/scripts/smoke-local_test.go" -run '^TestCPAPluginIntegration$' -timeout 1800s
```

- [ ] GREEN 要求整个唯一入口成功，1536 条 capture 精确匹配完整预期且无重复／遗漏；所有事件、type、完整 output/delta、metadata/opaque、client model、upstream calls/model、HTTP 状态和错误、headers/URI/bytes 全部满足。保留原所有格式流/非流、agent/count_tokens、首次 native unload、17 项 bridge/drain/errors、register/schema/reconfigure/reload 和内容反证。fixed binary、模块真实 producer/host/native/HTTP/WS 分层记录；没有实际运行的平台不填写为通过。
- [ ] 重跑 E3 的全部 root/vet/race、register/schema、三个显式 script tests、受影响完整性能 preflight/bench 和 Windows/Linux 构建打包／compatibility。不要执行 `go test ./.github/scripts`。共享源码或 fixture 修正后重跑受影响命令与累计复审，旧测量仅作历史对照。保留正式性能成本、C 原始 RED 日志缺失、Go VCS stamp 和报告者 Linux/Docker 部署限制，本地 GREEN 不表示其环境已解决。
- [ ] 自审核对 spec 每条 metadata 要求、G/E 所有权、1536 维度、原全部控制、三个 baseline 身份、源码／library／shadow 来源、不同层级结果、deadline 和发布边界。提交仅 E-M 实际两文件修改，完整 body 保留归属。

```bash
git -C "$E" diff --check
git -C "$E" add -- .github/scripts/smoke-local_test.go .github/scripts/testdata/cpa-functional-regression_test.go
git -C "$E" commit -m 'test: cover ordinary Responses metadata in CPA' -m 'Refs: #8
Related-PR: #7
PR-Author: @leolmq'
git -C "$E" log -1 --format='%H%n%B'
```

- [ ] 将开始时记录的固定 E-M taskBASE..全部最终 HEAD 的完整 diff、spec/plan、原始日志和 layer/性能／构建结果交独立 reviewer；所有确认问题由 E-M 在同一 worktree 修正，重跑受影响控制和完整终验，再对相同累计范围复审。完成后主动回报真实 commit/BASE/HEAD/worktree、报告与限制。保留稳定 ignored 证据，清理自己 OS 临时副本和进程，不删除用户已有内容。
- [ ] G-M/E-M 修改经累计独立审查后，根据共享 scanner 与永久 integration 的实际影响重跑全分支必要复审并逐项核对全部范围，确认无覆盖遗漏且规格／质量审查通过后才进入 F。原真实用户发布授权、七平台 CI/Release、下载资产／runtime 来源核验和 issue8/PR7 评论要求保持；本节不执行 push/tag/Release/公开评论，不预填修复版本，不关闭 issue。

## Task H：mixed-shape Responses ordinary metadata

本任务绑定 spec 的 H 节及已完成的 `M1-F15-MIXED-METADATA-01` 交接。固定 review/taskBASE=`9f84a8135982f3c0097aba58e804ccf8fd38b5ba`；原累计 reviewBASE=`7855e55904f9a208ef915aa7878b77db8577a294`，metadata 累计 BASE=`3dce8db7373d2da6cfd792a30e2c323e0525bdc5`。全部步骤在 H 实际 isolation worktree 连续执行。当前唯一 coding owner 完成自审，最终两门独立 reviewer 由控制 workflow 分派。

### H1：事实核对、当前规格与文档提交

- [ ] 普通 Get-Location/pwd、git rev-parse HEAD、branch/status 核对实际 ROOT。HEAD 应为固定9f84；不同时在自身 ROOT 用普通 `git switch -c` 创建唯一 H 分支指向9f84。不 EnterWorktree，不跨工作区 git，不派代理，不改变权限。
- [ ] 读取只读交接、已结束正式报告和相关 stable helper；核对正式 SHA256、固定 CPA binary revision/version/SHA256。只读取插件直接相关 CPA 依赖，不改 CPA/cache、来源或旧报告。
- [ ] 有限追踪 shared Write/drain/scanner/reset、全部调用者、batching/chunks+error。根因假设由真实 units 和 trace 核对：仅下一 data 触发 data-only drain，metadata suffix 遇 event 使 scanner disabled。原 C/G/G-M 行为保持。
- [ ] 在两份 tracked 文档加入完整 H 规格与本计划，逐项自审范围、HTTP限制、XAI WS会话差异、原1536/none384、新mixed expected-map、全部验证和后续 F 边界。`git diff --check` 后单独提交 docs，body 为 `Refs: #8`、`Related-PR: #7`、`PR-Author: @leolmq`。检查提交只含两份文档。

### H2：永久测试和真实9f84 RED

- [ ] 阅读 `writing-good-tests.md`。说明每个新 test 检测的真实缺陷，expected bytes 不调用产品逻辑生成。复用 native fixture/helpers。
- [ ] 在 `stream_native_fields_regression_test.go` 新增 mixed 回归：data-only -> field-pair、反向、多次交替、none/id/retry/comment/连续 opaque、三种位置、Flush/Finish 和逐次派发；精确检查完整 payload/六白名单/opaque。覆盖2MiB/8MiB续写、完整跨16MiB、incomplete超限、完整前缀+error/读错误、累计流量、input/output ownership和游标；复用原边界tests，不删旧断言。
- [ ] 在唯一 CPA fixture 增加真实 producer/host/native/WS 同层验收，保留 mapped/direct/disabled `.6/.7` 对照。fixed binary 交接确需时仅扩展原 smoke入口。新增 mixed matrix单独命名，完整 expected-map 检查 unknown/duplicate/missing；原1536和none384轴/fixture/断言不变。
- [ ] 使用固定9f84 DLL运行新 focused根tests和永久实际CPA，观察目标内容 RED：模型未恢复、无效 native data、mapped WS close1006；none/direct/disabled WS和非流是正常反证。HTTP direct/disabled缺completed单独准确记录，禁止把HTTP200或消费者限制当TDD RED。
- [ ] 保存真实源码、DLL/header、命令/exit、producer/host units、raw请求/headers/URI/response、native/消费者bytes与日志。保留XAI60项独立WS会话字段严格equality失败，检查确实仅该已确认差异；不忽略或伪造 equality。

### H3：最小 shared 修正及 focused GREEN

- [ ] 只在共享 output-unit/scanner 根因位置修改，复用既有 format/state/json恢复器。完整 data-only 遇下一已核验 event或data单位时先派发前值；metadata保持给后续单位，不污染JSON或关闭scanner。不增加provider、mode、parser、配置或fallback。
- [ ] focused新test GREEN后执行原 `TestFunctionalNativeSSEField`、所有`TestFunctional`及协议/forwarder/terminal/allocation/游标控制。generic wire LF/CR/CRLF、BOM、所有splits、四其他formats、raw单值/序列/array和opaque保持。
- [ ] 出现意外失败按实际证据修正，保存失败日志和重验；新增内容精确改动，不降低旧门槛、不跳过旧assertion。完整前缀+error仍由既有调用者发送后报错。

### H4：最终源码完整验证及真实性能

- [ ] `go test ./... -count=1`、`go vet ./...`、`go test ./... -race -count=1 -timeout=1800s`，完整保存 stdout/stderr和实际exit。根平台skip单列。
- [ ] 三个显式入口分别运行 package-release、check-compatibility、smoke-local 的 `.go`与`_test.go`组合，禁止目录级scripts测试。注入 `-X main.pluginVersion=0.5.12` 的注册/schema控制，默认版本仍为dev，不声称发布。
- [ ] 全部原59 benchmark 及原 metadata新增后的60组入口执行 `go test . -run '^$' -bench . -benchtime=1x -count=1 -benchmem`，精确检查原preflight全集和新增mixed。保留全部byte-exact/长度/ownership/allocation条件。
- [ ] 用9f84独立无Git source archive和fixed当前源码，按相同policy顺序foreground运行受影响benchmarks `-count=5 -benchmem`，先各自1x preflight。原样本、中位数、比较和真实成本完整保存。9f84错误mixed无计时基线；新增mixed只测fixed。旧五版本数字不改写、不机械重跑未受影响版本。
- [ ] 构建最终native DLL，唯一 `TestCPAPluginIntegration` 运行原1536/none384、全部原functional/bridge/lifecycle/schema/reconfigure/reload/unload17及完整新增mixed。真实producer/host/native/WS必需GREEN；HTTP既存限制按每项准确payload记录。全部captures与expected-map逐项核对，不用总数量代替。
- [ ] 保留 CPA 源副本时使用 `cpa-` 加16个hex字符的目录名，并保留目录已存在即失败的检查和完整module/source身份核验。Windows 实测完整hash目录下的深层依赖启动 vet 返回 `The directory name is invalid`；新增 focused 路径回归观察 RED 后缩短目录组件，不改变系统设置、不跳过 vet。审计与单独注入版本验收的 Go 输入放在本任务 `.inputs/` 中，避免被根 `./...` 当成额外package；不增加依赖文件。Git archive导出bytes、Git blob/canonical LF和ROOT raw源文件分别核对，CRLF规范化必须由真实Git blob hash验证。

### H5：build/package、最终自审、提交与交接

- [ ] Windows/Linux c-shared使用当前源与注入版本构建；现有compatibility/packager检查通过，Linux GLIBC<=2.17。保存source archive、Git blobs/canonical LF/raw identity manifest、DLL/header、zip/checksum和receipt；linked-worktree GoVCS不作为源码身份。保留Go1.26.5/GCC16.1.0/Zig0.17.0与CI Zig0.16.0、原C RED和历史清理限制；七平台CI/runtime/Release未运行。
- [ ] 单独完整自审核对9f84..当前全部修改与原7855..当前累计内容，核对所有新assertions、旧controls、调用者与所有权，无扩展功能/依赖/配置。发现confirmed/gap连续修正、重验，直到完成条件满足。
- [ ] 普通git提交全部owned产品/tests，body保留三个Refs字段。检查最终HEAD覆盖docs及产品提交，status干净。生成9f84..最终HEAD、7855..最终HEAD、3dce8db..最终HEAD完整累计packages，不能只给最后commit。
- [ ] 所有稳定证据/日志/captures/源归档/DLL/header/manifest/receipts/样本/自审及 `task-H-report.json` 在自身 ROOT/dist/task-H-mixed-metadata。报告列实际root/head/branch/commits/source/hash/owned路径、完整RED-GREEN、commands/exit、matrix/controls/performance、清理和限制。未公开exit=null，guard拒绝processStarted=false，保留真实Exit1/2与修正过程；不覆盖旧artifact，不删除用户文件。
- [ ] 主动SendMessage main报告阶段性事实及最终报告/hash，正式StructuredOutput。owner self-review不代替独立审批，controller完成两门审查/修正/复审/整合及新HEAD共享协议与全范围终审后继续F。H不启动reviewer，不推送/tag/Release/评论/关闭。

## Task F：合并、授权核对和 patch 发布

**Files:** 不新增产品文件。仅合并已完成提交；版本通过现有 build flags 注入。

- [ ] 确认 A/B/C/D/G/E 全部完成，检查 `git show --stat`、函数/fixture 所有权和独立审查结果；F15 的 G2 冲突必须已解决，E 的唯一永久 fixture／入口必须完成两类 native 全矩阵 baseline RED／fixed GREEN。E 固定集成点若后续变动，重跑相关终验；不将未完成任务 cherry-pick 到 main。
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
- [ ] issue8 现场信息请求由既有补充复现／条件评论负责人执行，独立于本任务的发布依赖。E2 的正式交叉核验已记录 `originalIssueReproduced=true`、`commentAllowed=false`，无法复现时的评论阶段已跳过，未评论或关闭。其他有效方法完成并经独立核验仍未复现时，核对真实用户授权后即可评论请求 CPA 失败请求日志、准确插件版本与 CPA revision、部署／endpoint、相关配置、原始 SSE 和 host read 信息，无需等待 E 全部终验或 Release。`ISSUE8_INFO_REQUEST_FILE` 为该负责人自有 ignored workspace 内的实际评论正文，写明已核查的范围与限制，不声称修复；执行以下命令核对返回的评论 URL、实际正文和 issue 仍为 open。正式确认已复现时跳过这项无法复现评论，进度消息不代替正式判定。

```bash
gh issue comment 8 --repo DoingDog/cpa-plugin-model-mapper --body-file "$ISSUE8_INFO_REQUEST_FILE"
gh issue view 8 --repo DoingDog/cpa-plugin-model-mapper --json state,comments
```

- [ ] 发布后的修复说明由 F 在成功发布及 assets 核验后执行，评论和关闭另核对真实用户授权。`C_FIX_COMMIT` 取 C 实际完成并合入的产品修复 commit；结合全部已发布 Release、tag ancestry 和 E 结果确定首个包含它的 `FIX_RELEASE_VERSION`，未知时不填候选。逐项核对后续 PR7 相关产品、复审、永久回归和文档修正提交的实际 `Related-PR: #7`／`PR-Author: @leolmq`，不要求改写已有提交。`PR7_COMMENT_FILE` 是 F 自有 ignored workspace 内的评论正文，写明该 commit、Release 版本、E 的真实 CPA/DLL Chat/Completions 结果与证据层级，以及已核实 #7／@leolmq 的贡献。`RELEASE_NOTES_FILE` 保留实际 Release 现有正文并补充相同作者贡献，读取 Release body 核验；不得把 `9fe917c031f0a7ed8b43897220a1eea237acc88b` 候选或真实仍有问题的 `v0.5.7` 写成已交付修复，不编造姓名、email 或 `Co-authored-by`。变量须有经核验的实际值后执行：

```bash
git show -s --format='%H%n%B' "$C_FIX_COMMIT"
git tag --contains "$C_FIX_COMMIT"
gh api --paginate repos/DoingDog/cpa-plugin-model-mapper/releases --jq '.[] | {tag_name,published_at,draft,prerelease}'
git merge-base --is-ancestor "$C_FIX_COMMIT" "${FIX_RELEASE_VERSION}^{commit}"
gh release view "$FIX_RELEASE_VERSION" --repo DoingDog/cpa-plugin-model-mapper --json tagName,publishedAt,isDraft,isPrerelease,body,url
gh release edit "$FIX_RELEASE_VERSION" --repo DoingDog/cpa-plugin-model-mapper --notes-file "$RELEASE_NOTES_FILE"
gh release view "$FIX_RELEASE_VERSION" --repo DoingDog/cpa-plugin-model-mapper --json body,url
gh pr comment 7 --repo DoingDog/cpa-plugin-model-mapper --body-file "$PR7_COMMENT_FILE"
gh pr view 7 --repo DoingDog/cpa-plugin-model-mapper --json state,comments
```

- [ ] `ISSUE8_COMMENT_FILE` 同样位于 F 自有 ignored workspace，发布后的正文依据正式补充核验及 E 最终 Responses/native/HTTP 实际结果。当前请求体独立核验和 E2 的正式 native 交叉核验已确认本地 mapped 502 及 `6c7f060` 的无 LF 字段边界缺陷，`originalIssueReproduced=true`、`commentAllowed=false`，无法复现时的评论阶段已跳过；C 后续最终修复 HEAD／首个 Release 尚未确定，不沿用早期未复现结论，不填写猜测。只有已证实与报告相关的修复才填写对应 commit/首个 Release；历史 `8b7bd9a0d135251878792b3740bc06a7da7ede00`／`v0.5.2` terminal 修复早于提问，不归因于原 issue。修复发版并完成资产核验后，评论注明已尝试修复及实际 commit/版本，要求提问者更新并在自己的环境测试；本地通过不表示报告者环境已解决。本地复现与报告者 Linux/Docker 的唯一部署根因分开说明；未复现或未证实已发布版本解决报告时，正文说明核查范围及仍缺的部署版本、原始 SSE/host read、CPA revision，保持 open。前述现场信息请求按独立时点执行。执行 `gh issue comment 8 --repo DoingDog/cpa-plugin-model-mapper --body-file "$ISSUE8_COMMENT_FILE"`，再用 `gh issue view 8 --repo DoingDog/cpa-plugin-model-mapper --json state,comments` 核对实际评论 URL／正文与状态。后续已发布版本确实解决报告且获真实用户授权时，按同样的 ancestry、首个 Release 和 assets 检查填写已证实 commit/版本，涉及 PR7 的贡献按 C1 署名，评论后执行 `gh issue close 8 --repo DoingDog/cpa-plugin-model-mapper --reason completed`，再次核对 state/comments；未满足这些条件不得关闭。

## 计划自审与交接完成条件

- [ ] 规格逐项映射：F01->A，F02/F03->B，F04/F07/F08/F09/F10/F11/F12->C，F05/F06->D，F13->C 共用 delimiterless 回归，F14->C 分类回归，F15->G 共享 native field boundary 回归及必要修复；actual integration 和全部控制->E，发布->F。
- [ ] 公共报告映射：PR7->C/F04 统一 TDD、E 实际 HTTP/core/header/native 控制；issue8->C 既有 terminal 控制及最终已验证 HEAD 上的两个无 LF 输入、18 字段九事件、单 terminal data-only 一帧／九个独立 data-only payload 九帧回归，共享 rewriter 已分派后续任务顺序完成必要修正，E 唯一入口的真实 XAI/Codex／三组请求体／producer/native/framer/HTTP 对照；现场信息请求->既有补充复现负责人独立核验后的条件评论，不等待 E 终验或 Release；发布后的实际 commit/版本说明及条件性关闭->F。核对 `hello`/`onetwo`、四类 headers、完整 delta/output/opaque、组合 ABI flags 与正常 producer read 的层级、既有全部分片及唯一永久 fixture。核对请求体独立核验和两种 native 方法的正式交叉核验均已完成、`6c7f060` 字段边界缺陷已确认、`originalIssueReproduced=true`／`commentAllowed=false`，保留 Windows 独立重跑、Windows/WSL2 发布结果来源及资产已清理／Linux 未重跑限制；不把 framer 结果充当未经运行的 HTTP 结果。报告者部署唯一根因与最终修复 HEAD/Release 仍未确定，未证实发布修复时保持 open；后续相关提交／Release／最终 PR 评论保留 #7／@leolmq 署名，不重复实施 A/B/D 或另派共享 scanner 的并行实现。
- [ ] 核对 Go 测试块能够在现有类型/helper 上编译；RED 必须为目标行为失败，不能是缺失符号、fixture 启动或依赖错误。
- [ ] 占位、自相矛盾、未经测量性能承诺、额外配置/功能和共享函数冲突检查完成。C 依赖 B fixture，G 依赖 C 最终完整审查提交，E 依赖 A/B/C/D/G 全部修复；独立任务仍并行。F15 草稿已独立复审，G2 按已核验的 Responses output-unit 消费语义执行，产品尚未修复，文档整合不表示产品已修复；同一实际入口出现可靠相反派发要求时停止产品编码并报告。完整核对 Responses 四 callback 累计 `0、1、1、2`、迟到 LF/CRLF 不改变两个事件、data-only 下一独立 data 派发前值及 Flush 派发末值、generic 连续 wire 的全部分片与原文、whole event-only metadata 后真正 terminal、原 validator 错误、合法 callback 的对应消费者限制、opaque、16 MiB，以及两份完整更正 Go 草稿与本次编译输入一致。
- [ ] 当前文档提交只暂存两份文档，运行 `git diff --check`，不修改或暂存产品代码、旧 `.claude`、build artifact 或中间结果。
