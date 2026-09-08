# cpa-plugin-model-mapper 0.5.2 Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复0.5.2审计确认的全部runtime、stream、配置、cache和release问题，只保留通过预设benchmark门槛的性能实现，并发布verified `v0.5.2`。

**Architecture:** 保持单package和现有RPC类型，不引入dependency。Runtime将host stream setup同步完成，再把已准备的stream交给异步worker；worker的raw JSON framing与同步返回的最终 `Content-Type` 使用同一个判断结果。Release build用与ldflags相同的version sidecar记录binary版本，aggregate packager先验证全部输入，再写任何release文件。

**Tech Stack:** Go、cgo、Go stdlib `encoding/json`/`net/http`/`sync`、`gopkg.in/yaml.v3`、GNU Make、GitHub Actions、PowerShell与Git Bash。

**Spec:** `docs/superpowers/specs/2026-09-08-cpa-model-mapper-0.5.2-audit-design.md`

## Global Constraints

- 只修改插件仓库，不修改 `upstream/CLIProxyAPI` 或任何CPA本体文件。
- 五种注册format固定为 `openai`、`openai-response`、`claude`、`gemini`、`interactions`；未知format必须unhandled。
- response model白名单固定为顶层 `model`、顶层 `modelVersion`、`response.model`、`response.modelVersion`、`message.model`、`interaction.model`。
- request rewrite只允许修改顶层 `model`；opaque content、tool text和任意其他nested字段保持原值。
- SSE line ending必须支持CRLF、LF和bare CR；transport read boundary不能生成wire bytes。
- host明确返回非SSE `Content-Type` 时保持该类型和raw JSON分块；header缺失时使用 `text/event-stream` fallback并frame raw JSON。
- body bytes变化后删除stale `Content-Length`；stream setup始终删除host `Content-Length` 和 `Transfer-Encoding`。
- caller identity只信任authenticated `caller_scope`；不得信任未绑定的client header。最低支持scoped rules的CPA版本为 `v7.2.101`。
- caller match cache每代最多16,384 entries，总上限32,768；eviction只能fail closed。
- aggregate release binary version必须与normalized release version完全相同；sidecar路径固定为 `binaryPath + ".version"`。
- `VERSION=v0.5.2` 与 `VERSION=0.5.2` 都生成embedded `0.5.2` 和无前导v的archive名；`vv0.5.2` 必须失败。
- 不增加第三方dependency，不改变wildcard non-backtracking语义，不改变non-SSE multi-value separator语义。
- 每项production改动先有聚焦的失败Go test；修改smoke case本身属于test oracle更新，不为此增加仅供测试的production abstraction。
- 性能candidate不满足spec门槛时，恢复candidate实现，保留有语义preflight的benchmark。
- 不读取或修改其他旧spec/plan，不读取或修改用户原有 `.claude/plan/tender-yawning-shamir.md`。
- Go注释和命名匹配现有英文风格；仅已知容量简化使用一条 `ponytail:` comment。

---

## File Ownership and Parallel Execution

计划commit后记录基点：

```powershell
$PlanBase = git rev-parse HEAD
```

从同一个 `$PlanBase` 同时启动两个Sonnet 1M xhigh agent，各自在isolated worktree工作并提交。两个agent不得读取或修改对方文件，也不得读取整个旧plan/spec目录。

**Runtime lane唯一可写文件：**

- `main.go`：config、format、headers、caller cache、response rewrite、stream parser、stream setup/worker/lifecycle。
- `main_test.go`：runtime correctness和stream lifecycle regression tests。
- `performance_regression_test.go`：三个benchmark矩阵与allocation checks。
- `abi_cgo.go`：init/shutdown与stream lifecycle连接。
- `abi_cgo_test.go`：仅在现有cgo helper确实需要直接coverage时修改；默认不改。
- `CLAUDE.md`：只更新实际response model白名单。

**Release lane唯一可写文件：**

- `Makefile`：normalized build version与sidecar。
- `.github/scripts/package-release.go`：aggregate version preflight。
- `.github/scripts/package-release_test.go`：sidecar regression tests。
- `.github/scripts/check-release-compatibility_test.go`：Make integration tests和fixture。
- `.github/scripts/smoke-local.go`：两个wildcard credential smoke cases。
- `.github/scripts/smoke-local_test.go`：仅在现有helper行为改变时修改；默认不改。
- `README.md`：build命令、最低CPA版本和response白名单。

两个lane完成后，主会话按commit顺序cherry-pick。若agent因API断开，使用原agent ID和 `SendMessage` 恢复，不启动重复任务。

## Runtime Lane

### Task 1: Strict lifecycle config and registered format boundary

**Files:**
- Modify: `main.go:912-941`
- Modify: `main.go:1352-1389`
- Test: `main_test.go:66-336`
- Test: `main_test.go:1149-1170`

**Interfaces:**
- Consumes: `decodeLifecycleConfig(raw []byte) (json.RawMessage, bool, error)`、`applyLifecycleConfig(raw []byte) error`、`selectRules(cfg Config, format string) ruleSelection`。
- Produces: 相同public/internal signatures；`config_yaml` presence由top-level raw map检测；unknown format返回空 `ruleSelection`。

- [ ] **Step 1: Add failing lifecycle type and atomic-publication tests**

在 `main_test.go` 增加 `TestApplyLifecycleConfigRejectsNonStringConfigYAMLAtomically`。table inputs固定为：

```go
[]string{
    `{"config_yaml":123}`,
    `{"config_yaml":null}`,
    `{"config_yaml":{}}`,
    `{"config_yaml":[]}`,
}
```

每个subtest先发布 `Config{GlobalRules: "old=>target"}`，调用 `applyLifecycleConfig`，要求error包含 `config_yaml must be a string`，随后调用 `routeModel(loadedConfig(), "openai", "old", "", "")` 并确认仍路由到 `target`。

在同一组附近增加 `TestDecodeLifecycleConfigDistinguishesDirectAndEmptyLifecycleConfig`：

- direct `{"global_rules":"a=>b"}` 返回 `lifecycle=false`，随后decode得到该rule。
- lifecycle `{"config_yaml":""}` 返回 `lifecycle=true`，随后decode得到default empty config。

- [ ] **Step 2: Add failing unknown-format test**

增加 `TestRuleSelectionRejectsUnknownFormat`，用 `Config{GlobalRules: "client=>global"}` 调用 `routeModel`，format固定为 `unknown`，要求 `Handled=false`。现有 `TestRuleSelectionGlobalOnlyFormats` 继续断言 `gemini` 和 `interactions` 使用global rules。

- [ ] **Step 3: Run focused tests and confirm RED**

```powershell
go test . -run 'Test(ApplyLifecycleConfigRejectsNonStringConfigYAMLAtomically|DecodeLifecycleConfigDistinguishesDirectAndEmptyLifecycleConfig|RuleSelectionRejectsUnknownFormat)$'
```

Expected：非法 `config_yaml` 至少一个case无error或覆盖旧配置，unknown format错误地Handled。

- [ ] **Step 4: Implement strict presence detection**

把 `decodeLifecycleConfig` 的匿名string struct改为raw top-level map：

```go
var lifecycle map[string]json.RawMessage
if err := json.Unmarshal(trimmed, &lifecycle); err != nil {
    return append(json.RawMessage(nil), trimmed...), false, nil
}
encoded, ok := lifecycle["config_yaml"]
if !ok {
    return append(json.RawMessage(nil), trimmed...), false, nil
}
var configYAML string
if err := json.Unmarshal(encoded, &configYAML); err != nil {
    return nil, true, fmt.Errorf("config_yaml must be a string: %w", err)
}
```

随后对 `configYAML` 执行现有base64/YAML/config marshal流程。空string必须走该流程并生成empty config，不回退到direct config。

- [ ] **Step 5: Restrict `selectRules` default**

switch必须显式包含：

```go
case "gemini", "interactions":
    return ruleSelection{first: cfg.globalRules}
default:
    return ruleSelection{}
```

`claude`、`openai-response`、`openai` 的stack逻辑不变。

- [ ] **Step 6: Run focused and adjacent config tests**

```powershell
go test . -run 'Test(DecodeLifecycleConfig|ApplyLifecycleConfig|HandlePluginRegister|DecodeConfig|RuleSelection|ReconfigureRulesStackMode)'
```

Expected：PASS。

- [ ] **Step 7: Commit Task 1**

```powershell
git add -- main.go main_test.go
git commit -m "fix: validate lifecycle config and formats"
```

### Task 2: HTTP header canonicalization at RPC boundaries

**Files:**
- Modify: `main.go:958-981`
- Modify: `main.go:1082-1097`
- Modify: `main.go:1248-1300`
- Test: `main_test.go:1506-1550`
- Test: `main_test.go:2454-2640`
- Test: `main_test.go:3078-3263`

**Interfaces:**
- Produces: `canonicalizeHeaders(headers http.Header)`，in-place canonicalize和merge，不clone map。
- Consumers: `handleModelRoute`、`handleExecutorExecuteStream`、`handleExecutorExecute`，以及Task 7中的host stream setup decode。

- [ ] **Step 1: Add failing canonicalization unit test**

增加 `TestCanonicalizeHeadersMergesCaseVariants`：

```go
headers := http.Header{
    "content-length": {"10"},
    "Content-Length": {"11"},
    "x-test":         {"lower"},
    "X-Test":         {"canonical"},
}
canonicalizeHeaders(headers)
```

要求map只保留 `Content-Length` 和 `X-Test` 两个canonical keys，每个key保留两个value。value顺序不作断言，因为JSON object和Go map不保序。

- [ ] **Step 2: Add failing route and nonstream boundary tests**

1. 在 `TestHandleModelRouteUsesCallerScope` 增加lowercase `authorization` wildcard case，metadata digest与credential绑定，要求Handled。
2. 增加 `TestHandleExecutorExecuteCanonicalizesRequestAndResponseHeaders`：request raw JSON手写lowercase `content-length`，host callback检查forwarded headers没有任意casing的Content-Length；host response返回lowercase `content-length` 和 `x-request-id`，body恢复发生后要求ExecutorResponse没有Content-Length且有canonical `X-Request-Id`。
3. 另加unchanged response case，lowercase `content-length` 应被canonicalize并保留，证明删除仍只发生在body改变时。

- [ ] **Step 3: Confirm RED**

```powershell
go test . -run 'Test(CanonicalizeHeadersMergesCaseVariants|HandleModelRouteUsesCallerScope|HandleExecutorExecuteCanonicalizesRequestAndResponseHeaders)$'
```

Expected：lowercase Authorization未命中，lowercase Content-Length未删除，或helper未定义。

- [ ] **Step 4: Implement the in-place helper**

在RPC request types附近增加：

```go
func canonicalizeHeaders(headers http.Header) {
    for key, values := range headers {
        canonical := http.CanonicalHeaderKey(key)
        if canonical == key {
            continue
        }
        headers[canonical] = append(headers[canonical], values...)
        delete(headers, key)
    }
}
```

不为nil map分配。多个casing被遍历时最终合并到canonical key。

- [ ] **Step 5: Call the helper immediately after successful JSON decode**

在以下边界调用：

```go
canonicalizeHeaders(req.Headers)
```

- `handleModelRoute`
- `handleExecutorExecuteStream`
- `handleExecutorExecute`

在nonstream host response decode成功后调用：

```go
canonicalizeHeaders(hostResp.Headers)
```

Task 7负责stream host response的对应调用。

- [ ] **Step 6: Run focused executor tests**

```powershell
go test . -run 'Test(HandleModelRoute|HandleExecutorExecute|CanonicalizeHeaders)'
```

Expected：PASS。

- [ ] **Step 7: Commit Task 2**

```powershell
git add -- main.go main_test.go
git commit -m "fix: canonicalize RPC headers"
```

### Task 3: Bounded caller wildcard decision cache across reconfigure

**Files:**
- Modify: `main.go:827-878`
- Modify: `main.go:2045-2065`
- Test: `main_test.go:2687-2925`
- Test: `performance_regression_test.go`

**Interfaces:**
- Produces: two-generation cache with constants `callerPatternCacheGenerationSize = 16 << 10` and total bound32,768。
- Produces test-visible internal helpers: `resetCallerPatternCache()`；entry count可由same-package test在cache lock下读取，不增加exported API。
- Preserves: `callerPatternMatch(r *rule, scope, key string) (matched, authenticated bool)`。

- [ ] **Step 1: Add benchmark harness before changing the cache**

在 `performance_regression_test.go` 增加 `BenchmarkCallerPatternCacheRetention`，subbenchmarks固定为 `N=1000`、`N=10000`、`N=100000`。每次iteration reset cache，使用rule `sk-*#client=>target` 的compiled caller pattern，生成unique `sk-%d` credential与对应 `callerScope`，调用 `callerPatternMatch`，最后报告：

```go
b.ReportMetric(float64(retained), "retained-entries")
```

语义preflight必须确认每次返回 `matched=true` 和 `authenticated=true`。100,000 case用 `-benchtime=1x` 采集，输出追加到：

`C:\Users\user\AppData\Local\Temp\claude-cpa-plugin-0.5.2-20260908-benchmarks.md`

Expected baseline：`retained-entries` 随N增长到100,000附近。

- [ ] **Step 2: Replace old reset expectations with failing required behavior**

删除或改写：

- `TestSetLoadedConfigPublishesWithCallerCacheReset`
- `TestReconfigureClearsCallerPatternCache`

新test名为 `TestEquivalentReconfigurePreservesCallerPatternDecisionForExecutor`，步骤固定为：

1. `setLoadedConfigForTest` 发布 `sk-kimi-*#client-model=>wildcard-target`。
2. `handleModelRoute` 使用bound `Authorization: Bearer sk-kimi-team` 和caller scope，确认Handled并warm cache。
3. `handlePluginReconfigure` 发布等价rules。
4. `handleExecutorExecute` 只保留caller scope，header改为interceptor replacement或删除。
5. fake host捕获request，要求 `Model == "wildcard-target"`。

保留 `TestHandleModelRouteWarmCallerPatternHitDoesNotRequireConfigWriteLock`，证明cache hit不需要config write lock。

- [ ] **Step 3: Add failing capacity test**

增加 `TestCallerPatternCacheIsBounded`，插入100,000个unique scope后，在cache read lock下统计current+previous entries，要求 `<= 2*callerPatternCacheGenerationSize`。再验证一个已淘汰entry在没有raw key时返回 `(false, false)`，即fail closed。

- [ ] **Step 4: Confirm RED**

```powershell
go test . -run 'Test(EquivalentReconfigurePreservesCallerPatternDecisionForExecutor|CallerPatternCacheIsBounded|HandleModelRouteWarmCallerPatternHitDoesNotRequireConfigWriteLock)$'
```

Expected：reconfigure后executor无法复用，或cache超过bound。

- [ ] **Step 5: Implement two generations**

用一个small state struct替代单map：

```go
const callerPatternCacheGenerationSize = 16 << 10

type callerPatternCacheState struct {
    current  map[callerPatternCacheKey]bool
    previous map[callerPatternCacheKey]bool
}
```

lookup在一个 `RLock` 内先查current，再查previous。insert在 `Lock` 内重新check两代，current达到16,384前先执行：

```go
callerPatternCache.previous = callerPatternCache.current
callerPatternCache.current = make(map[callerPatternCacheKey]bool, callerPatternCacheGenerationSize)
```

只写一条comment：

```go
// ponytail: two fixed generations cap cross-RPC caller state; remove this cache when CPA carries route decisions into executor calls.
```

- [ ] **Step 6: Separate production reconfigure from test/lifecycle reset**

`publishLoadedConfig` 只持有 `loadedConfigMu` 并替换compiled config，不获取cache lock。`setLoadedConfigForTest` 在publish前调用 `resetCallerPatternCache` 保持test isolation。Task 8会在native init/shutdown调用相同reset helper。

- [ ] **Step 7: Run correctness, race and post-change retention benchmark**

```powershell
go test . -run 'Test(Caller|ExecutorReuses|EquivalentReconfigure|ReconfigureRulesStackMode)'
go test -race . -run 'Test(Caller|EquivalentReconfigure|HandleModelRouteWarmCallerPatternHit)'
go test . -run '^$' -bench '^BenchmarkCallerPatternCacheRetention$' -benchmem -benchtime=1x -count=3
```

Expected：tests PASS；100,000 case `retained-entries <= 32768`；warm hit仍只使用RLock。

- [ ] **Step 8: Append before/after cache evidence and commit**

在临时benchmark日志记录exact command、Go/CPU和retained values。随后：

```powershell
git add -- main.go main_test.go performance_regression_test.go
git commit -m "fix: bound caller pattern cache"
```

### Task 4: Restore `interaction.model` without broadening recursive rewrite

**Files:**
- Modify: `main.go:1610-1635`
- Test: `main_test.go:1552-1601`
- Test: `main_test.go:3011-3046`
- Test: `main_test.go:4228-4258`

**Interfaces:**
- Extends only `rewriteResponseModelFieldsWithReplacementChecked`。
- Produces no new exported function。

- [ ] **Step 1: Add failing direct and executor tests**

在现有response whitelist fixtures加：

```json
"interaction":{"model":"upstream","output":[{"model":"keep-output"}]}
```

要求 `interaction.model == "client"`，`interaction.output[0].model == "keep-output"`。在nonstream known fields test和SSE known fields test也加入该对象并断言恢复。

- [ ] **Step 2: Confirm RED**

```powershell
go test . -run 'Test(ModelRewriteTreatsOpaqueContentAsRawJSON|HandleExecutorExecuteRestoresKnownResponseModelFields|HandleExecutorExecuteStreamRestoresKnownSSEModelFields)$'
```

Expected：`interaction.model` 仍为upstream。

- [ ] **Step 3: Add exactly one nested whitelist call**

在message和response处理旁加入：

```go
interactionChanged, err := rewriteNestedRawStringFields(doc, "interaction", model, replacement, "model")
if err != nil {
    return nil, false, true, err
}
changed = interactionChanged || changed
```

不新增递归walker，不把 `interaction.modelVersion` 加入白名单。

- [ ] **Step 4: Run response and all-format tests**

```powershell
go test . -run 'Test(RestoreResponse|ModelRewrite|HandleExecutorExecute(AllFormats|RestoresKnownResponseModelFields)|HandleExecutorExecuteStream(AllFormats|RestoresKnownSSEModelFields))'
```

Expected：PASS。

- [ ] **Step 5: Commit Task 4**

```powershell
git add -- main.go main_test.go
git commit -m "fix: restore interactions response model"
```

### Task 5: Make SSE parsing independent of host read boundaries and support all line endings

**Files:**
- Modify: `main.go:333-423`
- Modify: `main.go:462-509`
- Modify: `main.go:640-649`
- Test: `main_test.go:2167-2205`
- Test: `main_test.go:3319-3339`
- Test: `main_test.go:3597-3771`

**Interfaces:**
- Deletes: `isSSEFieldStart`、`lastSSELine`、`knownSSELogicalBoundary`。
- Preserves: `sseLineEnding`、`findSSEEventDelimiter`、`streamChunkRewriter.Write` signatures。
- Changes: `frameSSEData` recognizesCRLF、LF和CR。

- [ ] **Step 1: Replace the test that encodes an invented boundary**

`TestStreamChunkRewriterSeparatesKnownResponseEventAndDataChunks` currently omits the wire newline between `event:` and `data:` and expectsthe chunk boundary to create one。Replace it with `TestStreamChunkRewriterDoesNotTreatReadBoundaryAsLineEnding`，using valid single-line SSE field values that contain field-like text：

```go
inputs := []string{
    "event: responsedata: continuation\n\n",
    "data: {\"model\":\"upstream\"}data: continuation\n\n",
}
```

For every split offset from0 throughlen(input)，feed two writes plusFlush and require output equal to the split=0 output byte-for-byte。The second input isnon-JSON data and must not gain a newline。

- [ ] **Step 2: Add failing `frameSSEData` line-ending table**

Add `TestFrameSSEDataSupportsAllSSELineEndings`：

```go
cases := []struct{ input, want string }{
    {`{"a":1}` + "\n" + `{"b":2}`, "data: {\"a\":1}\ndata: {\"b\":2}\n\n"},
    {`{"a":1}` + "\r\n" + `{"b":2}`, "data: {\"a\":1}\ndata: {\"b\":2}\n\n"},
    {`{"a":1}` + "\r" + `{"b":2}`, "data: {\"a\":1}\ndata: {\"b\":2}\n\n"},
}
```

Also add a trailing line-ending case and assert the generated event remains parseable as SSE data without any unprefixed line。

- [ ] **Step 3: Confirm RED**

```powershell
go test . -run 'Test(StreamChunkRewriterDoesNotTreatReadBoundaryAsLineEnding|FrameSSEDataSupportsAllSSELineEndings)$'
```

Expected：current heuristic changes at least one partition；bare CR case contains unprefixedbytes。

- [ ] **Step 4: Remove boundary inference**

In `streamChunkRewriter.Write` replace：

```go
if len(r.sse.buf) > 0 {
    if knownSSELogicalBoundary(r.sse.buf, p) {
        r.sse.buf = append(r.sse.buf, '\n')
    }
    return r.sse.Write(p)
}
```

with：

```go
if len(r.sse.buf) > 0 {
    return r.sse.Write(p)
}
```

Delete the three now-unused helpers。

- [ ] **Step 5: Rewrite `frameSSEData` with existing `sseLineEnding`**

Iterate fromoffset0。For each line ending，append `data: ` plus the logical line and oneLF。After last line append oneadditionalLF to dispatch。Do not use `bufio.Scanner` or another parser。

- [ ] **Step 6: Run complete SSE parser regression group**

```powershell
go test . -run 'Test(SSERewriter|StreamChunkRewriter|FrameSSEData)'
```

Expected：PASS。If an old test requires a transport boundary to act as a newline，rewrite that test to include the missing wire line ending；do not restore the heuristic。

- [ ] **Step 7: Commit Task 5**

```powershell
git add -- main.go main_test.go
git commit -m "fix: preserve SSE bytes across reads"
```

### Task 6: Emit complete raw JSON prefixes while buffering only incomplete tails

**Files:**
- Modify: `main.go:462-638`
- Test: `main_test.go:3698-3751`
- Test: `main_test.go:3833-4047`

**Interfaces:**
- Changes internal `splitJSONValues` to returncomplete values plus last complete offset。
- Changes internal `tryRawJSONChunks` as needed to returnchunks and unconsumed tail state。
- Preserves external `streamChunkRewriter.Write` and `Flush` signatures。

- [ ] **Step 1: Add failing complete-prefix tests**

Add `TestStreamChunkRewriterEmitsCompleteJSONPrefixBeforeIncompleteTail` forboth `frameRawJSONAsSSE=false` andtrue。First write：

```plaintext
{"model":"upstream","id":1}
{"model":"up
```

Requirements afterfirst write：

- exactly one output value。
- first value hasmodel `client`。
- framed=true output starts `data: ` and ends blank line。
- the incomplete second value is not emitted。

Second write is `stream","id":2}` and must emit exactly one second restoredvalue without repeatingid1。

- [ ] **Step 2: Add failing terminal-tail framing test**

Add `TestStreamChunkRewriterFlushFramesIncompleteJSONTailWhenSSE`。Set `frameRawJSONAsSSE=true`，write `{"model":"upstream"`，thenFlush。Require the original incomplete bytes appear only insideone or more `data:` fields and no bare unprefixed CR/LF line is emitted。

- [ ] **Step 3: Confirm RED**

```powershell
go test . -run 'Test(StreamChunkRewriterEmitsCompleteJSONPrefixBeforeIncompleteTail|StreamChunkRewriterFlushFramesIncompleteJSONTailWhenSSE)$'
```

Expected：first complete value is withheld andFlush emits the combinedbuffer incorrectly。

- [ ] **Step 4: Return last complete decoder offset**

Use `json.Decoder.InputOffset()` immediately after each successful `Decode(&raw)`。Required behavior：

- `io.EOF` afterone or more values：complete sequence。
- `io.ErrUnexpectedEOF` afterone or more values：return thosevalues，`consumed=lastCompleteOffset`，`incomplete=true`。
- `io.ErrUnexpectedEOF` withno complete values：no output，wholeinput tail。
- any deterministic syntax error：return `ok=false` and preserve current whole-payload fallback。

Do not emit a successfulprefix before a deterministic syntax error，because fallback would otherwise duplicate bytes。

- [ ] **Step 5: Store only `p[consumed:]` as pending**

Update `tryRawJSONChunks`/`Write` so `ok=true` and `incomplete=true` can coexist。Rewrite/frame complete values immediately；clone or retain only theunconsumed tail according to existing ownership rules。When pending completes on a latercall，do not include previously emitted bytes。

- [ ] **Step 6: Make Flush honor selected outer framing**

For a pending candidate that remainsincomplete atEOF，the fallback chunk must pass through `frameSSEData` when `frameRawJSONAsSSE` istrue。For false，preserve the existingowned raw clone。

- [ ] **Step 7: Run raw JSON and ownership tests**

```powershell
go test . -run 'Test(StreamChunkRewriter|RawJSON|SplitJSONValues)'
```

Expected：PASS，including existing input ownership/allocation checks andmultiple-value behavior。

- [ ] **Step 8: Commit Task 6**

```powershell
git add -- main.go main_test.go
git commit -m "fix: stream complete JSON prefixes"
```

### Task 7: Synchronize stream setup, return host headers, and preserve terminal payload/error order

**Files:**
- Modify: `main.go:1021-1246`
- Test: `main_test.go:3078-3317`
- Test: `main_test.go:3399-3595`
- Test: `main_test.go:4049-4419`
- Test: `performance_regression_test.go:118-161`

**Interfaces:**
- Produces an internal preparedstream value containing `pluginStreamID`、`hostStreamID`、`originalModel`、`frameRawJSONAsSSE`、`call` andidempotent host close state。
- Produces `prepareExecutorStream(req *executorRPCRequest, call hostCaller) (*executorStream, http.Header, error)` or an equivalently minimal signature。
- Changes `runStreamForward` to consume onlyprepared state and perform read/rewrite/emit/close。
- Preserves `handleExecutorExecuteStream(raw []byte, call hostCaller) ([]byte, error)`。

- [ ] **Step 1: Add failing synchronous setup/header test**

Add `TestHandleExecutorExecuteStreamReturnsPreparedHostHeaders`。Fake `host.model.execute_stream` returns：

```go
pluginapi.HostModelStreamResponse{
    StatusCode: 200,
    StreamID:   "host-stream",
    Headers: http.Header{
        "content-type":      {"application/json"},
        "content-length":    {"999"},
        "transfer-encoding": {"chunked"},
        "x-request-id":      {"request-1"},
    },
}
```

Workerread returnsone complete raw JSON value thenDone。Require setup response headers：

- `Content-Type == application/json`
- `X-Request-Id == request-1`
- no casing ofContent-Length orTransfer-Encoding

Require emitted body remainsraw JSON，not `data:` framed。

A table in the same test covers：

- missinghost Content-Type -> response `text/event-stream` and raw JSON framed。
- host `text/event-stream; charset=utf-8` -> same media type and raw JSON framed。
- host `application/json; profile="text/event-stream"` -> preserve media type and do notframe。

- [ ] **Step 2: Prove handler waits for host setup but not stream reads**

Add `TestHandleExecutorExecuteStreamCompletesSetupBeforeReturning`。Block the fake setup callback on a channel and callhandler in goroutine；verify handler hasnot returned。Release setup，then blockfirst read；verify handlerreturns setup headers while workerread remainsblocked。This encodes the required sync/async boundary and protects the16-slot CPA bridge constraint。

- [ ] **Step 3: Add failing partial setup cleanup test**

Add `TestPrepareExecutorStreamClosesPartialStreamIDOnDecodeError`。Return raw response：

```json
{"stream_id":"host-partial","status_code":"not-a-number"}
```

Count `host.model.stream_close` calls and requireone call for `host-partial`，with returned error containing `decode host stream response`。

- [ ] **Step 4: Add terminal payload order tests**

Add table `TestRunStreamForwardProcessesTerminalPayload`：

1. `{Payload: validJSON, Done: true}` emitsrestored payload，thenhost close，thenplugin close。
2. `{Payload: validJSON, Error: "upstream failed", Done: true}` emitsrestored payload，thenclosesplugin witherror text containing `upstream failed`。
3. `{Payload: split SSE completion, Error: "upstream failed"}` processespayload beforeFlush。

Assert event order explicitly as `emit,host-close,plugin-close`。

- [ ] **Step 5: Add cleanup aggregation test**

Add `TestRunStreamForwardPreservesInBandErrorAcrossCleanupFailures`。Fake one terminalchunk with `Error: "primary upstream error"`，thenmakeFlush emit、host close andfirstplugin close return distinctsentinel errors。Require returned error text containsall errors and starts/unwraps withthe primary error。Require allcleanup callbacks wereattempted even after earlier failure。Theouter wrapper may retryplugin close；second attempt succeeds and still receives text containingthe primary error。

- [ ] **Step 6: Confirm RED for the pipeline group**

```powershell
go test . -run 'Test(HandleExecutorExecuteStreamReturnsPreparedHostHeaders|HandleExecutorExecuteStreamCompletesSetupBeforeReturning|PrepareExecutorStreamClosesPartialStreamIDOnDecodeError|RunStreamForwardProcessesTerminalPayload|RunStreamForwardPreservesInBandErrorAcrossCleanupFailures)$'
```

Expected：setup returns beforehost setup，headers arefixedSSE，partial stream leaks，terminal payload isdropped，orcleanup hidesprimary error。

- [ ] **Step 7: Extract the synchronous prepare phase**

Move these existing operations into `prepareExecutorStream`：

1. caller scope andrule selection。
2. request model rewrite andconditional request `Content-Length` deletion。
3. `host.model.execute_stream` call。
4. release oflarge request setup fields afterthe call。
5. partial decode cleanup、status validation andmissingstream-id validation。
6. host response header canonicalization。
7. clone/finalize downstream headers，deleteContent-Length/Transfer-Encoding，setfallbackContent-Type onlyifmissing。
8. parse finalContent-Type once into `frameRawJSONAsSSE` andstore it inprepared state。

Use `pluginapi.ExecutorStreamResponse{Headers: headers}` forsetup JSON instead of anuntyped map。

- [ ] **Step 8: Give prepared host close idempotent state**

Preparedstream owns `sync.Once` andstoredclose error：

```go
func (s *executorStream) closeHost() error {
    s.closeHostOnce.Do(func() {
        _, s.closeHostErr = s.call(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: s.hostStreamID})
    })
    return s.closeHostErr
}
```

This is required forTask 8 shutdown racing normalworker cleanup。

- [ ] **Step 9: Start only the read worker asynchronously**

`startExecutorStream` must：

1. prepare synchronously。
2. create setup response bytes。
3. start onegoroutine that calls `runStreamForward(prepared)`。
4. return setup response without waiting forread。

Ifprepare fails，do notstartworker。Iflater admission fails inTask 8，closeprepared host stream before returningerror。

- [ ] **Step 10: Reorder each read result**

For every decodedchunk：

1. process nonemptyPayload。
2. retain anyrewrite/emit error instead of returning beforeexamining an accompanying `Error`。
3. ifError is nonempty，make itthe primary error，thenFlush，closehost，closeplugin，andjoincleanup errors with `errors.Join`。
4. otherwise ifpayload processing failed，performcleanup andreturn that error。
5. otherwise ifDone，finish normalFlush/close flow。

Transportread anddecode errors also attemptFlush andidempotent host close before returning。Do not callplugin close inside `runStreamForward` twice；the `startExecutorStream` wrapper handles returnederrors once，except anin-band firstclose failure where retry is intentional。

- [ ] **Step 11: Update existing content-type expectations without weakening semantics**

现有 `TestHandleExecutorExecuteStreamRestoresLineDelimitedRawJSONForWebSocket` 和 `TestHandleExecutorExecuteStreamRestoresSpaceDelimitedRawJSONForWebSocket` 传入 `application/json`，它们必须继续断言unframed restored JSON，并新增setup response `Content-Type: application/json` 断言。Tests usingempty or `text/event-stream` must expect `data:` framing。Do not change low-level `frameRawJSONAsSSE=false` behavior。

- [ ] **Step 12: Adapt setup-release allocation test**

Update `TestRunStreamForwardReleasesSetupBeforeRead` so it checks requestfields arecleared afterprepare and beforefirstread。The test must still ensurethe original request/body/header/query/metadata are notreferenced bythe worker。

- [ ] **Step 13: Run full stream group and race probe**

```powershell
go test . -run 'Test(HandleExecutorExecuteStream|RunStreamForward|StreamChunkRewriter|ReleaseExecutorStreamSetup)'
go test -race . -run 'Test(HandleExecutorExecuteStream|RunStreamForward)'
```

Expected：PASS，no goroutine leak orrace report。

- [ ] **Step 14: Commit Task 7**

```powershell
git add -- main.go main_test.go performance_regression_test.go
git commit -m "fix: prepare stream before forwarding"
```

### Task 8: Drain asynchronous stream workers before native shutdown

**Files:**
- Modify: `main.go` near stream types and `startExecutorStream`
- Modify: `abi_cgo.go:79-114`
- Modify: `abi_cgo.go:159-162`
- Test: `main_test.go` near stream worker tests
- Test: `abi_cgo_test.go` only if a non-C helper needs direct coverage

**Interfaces:**
- Produces: `resetExecutorStreamLifecycle()` and `shutdownExecutorStreams()`。
- Internal lifecycle state:mutex、stopping bool、active set、WaitGroup。
- `cliproxy_plugin_init` resets lifecycle beforepublishinghost callback。
- `cliproxyPluginShutdown` calls `shutdownExecutorStreams()`，then `resetCallerPatternCache()`，then `setHostCallback(nil)`。

- [ ] **Step 1: Add failing shutdown drain test**

Add `TestShutdownExecutorStreamsClosesAndWaitsForWorkers`。Use a fakehost wherefirst `stream_read` blocks until `host.model.stream_close` closes achannel。Start astream through `handleExecutorExecuteStream`，wait untilread started，then call `shutdownExecutorStreams` in anothergoroutine。Require：

- shutdown invokeshost close exactlyonce。
- shutdown doesnot return untilread worker exits andplugin close isattempted。
- aftershutdown returns，host callback count remainsunchanged after a shortcondition-based synchronization step，not an arbitrary longsleep。

- [ ] **Step 2: Add failing admission and re-init tests**

Add `TestExecutorStreamLifecycleRejectsDuringShutdownAndResets`：

1. mark lifecycle stopping througha test-controlled shutdown withoneblocked worker。
2. attempt anew prepared stream，requireerror andits host stream closed。
3. unblock oldworker andwait shutdown。
4. call `resetExecutorStreamLifecycle`。
5. start a newstream andconfirmnormal completion。

No `WaitGroup.Add` may happen concurrently aftershutdown hasbegun `Wait`。

- [ ] **Step 3: Confirm RED**

```powershell
go test . -run 'Test(ShutdownExecutorStreamsClosesAndWaitsForWorkers|ExecutorStreamLifecycleRejectsDuringShutdownAndResets)$'
```

Expected：lifecycle functions missing orshutdown returns whileworker isactive。

- [ ] **Step 4: Implement registry admission under one lock**

The admission helper must holdthe lifecycle mutex while checking `stopping`，addingtoactive set andcalling `wg.Add(1)`。Workerdefer removes itself underlock，then calls `wg.Done()`。Shutdown：

1. lock，set `stopping=true`，snapshot active pointers，unlock。
2. callidempotent `closeHost` for eachsnapshot item without holdingglobal lock。
3. `wg.Wait()`。

Reset only runswhen noworker is active andsets `stopping=false` withafresh active map。

- [ ] **Step 5: Connect registry to `startExecutorStream`**

Register aftersuccessfulprepare andbeforegoroutine launch。On rejection，closehost andreturnerror withoutworker。Worker always `defer unregisterExecutorStream(stream)`，includingpanic-free error paths。

- [ ] **Step 6: Connect ABI init/shutdown in the required order**

init参数和ABI检查成功后，先调用 `resetExecutorStreamLifecycle()` 和 `resetCallerPatternCache()`，再通过现有host callback closure调用 `setHostCallback`。Atshutdown：

```go
func cliproxyPluginShutdown() {
    shutdownExecutorStreams()
    resetCallerPatternCache()
    setHostCallback(nil)
}
```

Do not changeABI version、host pointercapture、`free_buffer` orplugin call response allocation。

- [ ] **Step 7: Run lifecycle tests with race detector**

```powershell
go test . -run 'Test(ShutdownExecutorStreams|ExecutorStreamLifecycle|HandleExecutorExecuteStream)'
go test -race . -run 'Test(ShutdownExecutorStreams|ExecutorStreamLifecycle|HandleExecutorExecuteStream)'
```

Expected：PASS andno race。

- [ ] **Step 8: Commit Task 8**

```powershell
git add -- main.go main_test.go abi_cgo.go abi_cgo_test.go
git commit -m "fix: drain streams during plugin shutdown"
```

If `abi_cgo_test.go` remainedunchanged，omit itfrom `git add`。

### Task 9: Benchmark-gated top-level request rewrite

**Files:**
- Modify: `performance_regression_test.go`
- Candidate modify: `main.go:1419-1436`
- Candidate test changes: exact-output assertions in `main_test.go` only ifcandidate iskept

**Interfaces:**
- Preserves: `rewriteTopLevelModel(body []byte, model string) ([]byte, bool, error)`。
- Candidate may addunexported scanners forvalidated top-level JSON only。

- [ ] **Step 1: Add the benchmark matrix with semantic preflight**

Add `BenchmarkRewriteTopLevelModel` withsizes1 KiB、64 KiB、1 MiB、8 MiB。Fixture isone validtop-level object with `model:"upstream"` near thefront andone largeopaque array/string payload。Before `b.ResetTimer()`：

- callrewrite once。
- decode output andrequiretop-level model `client`。
- requirea nested `model:"opaque"` unchanged。
- consume output throughpackage-level sinks soempty output cannotappear faster。

Every subbenchmark calls `b.ReportAllocs()` and `b.SetBytes(int64(len(body)))`。

- [ ] **Step 2: Add candidate semantics tests before implementation**

Add `TestRewriteTopLevelModelPreservesValidatedOpaqueBytes` covering：

- normalobject with whitespace andnested/tool content。
- escaped top-level key `"\u006dodel"`。
- escaped current model string。
- duplicate top-level model，whereonlythe last member determines JSON object semantics。
- last model non-string。
- missingmodel。
- invalidJSON aftera validprefix。
- requested model alreadyequal，requiring `changed=false` andbyte-identical clone。

For changed duplicate keys，assertdecoded finalmodel andunchanged opaque values，not canonical mapbyte order。For unchangedcases，assertbyte equality。

- [ ] **Step 3: Run semantic tests against current implementation**

```powershell
go test . -run 'Test(RewriteRequestModel|RewriteTopLevelModel|ModelRewriteTreatsOpaqueContentAsRawJSON)'
```

Expected：现有invariants全部通过。新增tests只断言top-level model、opaque values、invalid/equal input和duplicate-key的decoder-visible语义，不要求candidate特有的byte layout。

- [ ] **Step 4: Capture a seven-run baseline**

```powershell
go test . -run '^$' -bench '^BenchmarkRewriteTopLevelModel$' -benchmem -benchtime=500ms -count=7
```

Save raw output andmedian ns/op、B/op、allocs/op for eachsize in thetemporary benchmark log。Do not includecompilation time。

- [ ] **Step 5: Implement one minimal candidate**

Candidate algorithm：

1. call `json.Valid(body)`；invalid input returnsone `bytes.Clone` and `changed=false`。
2. scan onlythe validated top-level object，trackingJSON strings、escapes andnested `{}`/`[]` depth。
3. locate thelast top-level key whose decoded name isexactly `model`。Fast path comparesunescaped key bytes；onlyescaped candidate keys use `strconv.Unquote` or `json.Unmarshal`。
4. inspect onlythe selected rawvalue as aJSON string。Ifmissing、non-string orsemantically equalto target，returnclone/false。
5. marshal targetonce，allocateexact finalsize，copy prefix/replacement/suffix once。

Do not parse orcopy everyopaque value。Do not recursively searchnested objects。Keepingearlier duplicate keys isallowed because thelast key stilldefines the same decoder-visible model；tests must document this exact choice。

- [ ] **Step 6: Replace exact map-order assertions only ifcandidate remains**

Existing all-format executor tests compareforwarded request bytes tocanonical `json.Marshal(map)` order。Change these todecode top-level fields andcompareopaque nested `json.RawMessage` values。Do not weaken checks forEntryProtocol、ExitProtocol、model、content ortool text。

- [ ] **Step 7: Run semantics and full root tests**

```powershell
go test . -run 'Test(RewriteRequestModel|RewriteTopLevelModel|ModelRewrite|HandleExecutorExecute(AllFormats|Forwards)|HandleExecutorExecuteStreamAllFormats)'
go test .
```

Expected：PASS。

- [ ] **Step 8: Capture seven-run candidate results**

Run theexact baseline command again andrecordraw output。Keep onlyif：

- median ns/op for1 MiB and8 MiB improvesat least20%。
- B/op for1 MiB and8 MiB improvesat least30%。
- 1 KiB median ns/op doesnot regressmore than10%。
- allsemantic tests pass。

- [ ] **Step 9: Keep or revert only the candidate**

Ifgate passes，commit implementation、semantic tests andany exact-output test adjustments：

```powershell
git add -- main.go main_test.go performance_regression_test.go
git commit -m "perf: rewrite request model in place"
```

Ifgate fails，restoreonlycandidate code andcandidate-only assertion changes，keep the benchmark harness，runroot tests，thencommit：

```powershell
git add -- performance_regression_test.go
git commit -m "test: benchmark request model rewrite"
```

Record `kept` or `reverted` withall gate values in thetemporary benchmark log。

### Task 10: Benchmark-gated response marker scan

**Files:**
- Modify: `performance_regression_test.go`
- Candidate modify: `main.go:214-264`
- Candidate modify: `main.go:300-326`
- Candidate modify: `main.go:1451-1607`

**Interfaces:**
- Preserves: `mightContainResponseModelField`、response rewrite signatures。
- Candidate may retain `responseModelMarkerScanner` onlyfor escaped-key fallback；literal search uses `bytes.Contains`。

- [ ] **Step 1: Add marker benchmark matrix**

Add `BenchmarkResponseModelMarkerScan` for4 KiB、64 KiB、1 MiB andthree payloads：

- no marker：opaque text，includingordinary escapes butno model key。
- literal marker：`"model"` near theend。
- escaped key：`"\u006dodel"` near theend。

Preflight verifies `mightContainResponseModelField` expectedbool and `restoreResponseModel` exactchanged semantics。Usepackage sinks，`b.ReportAllocs()` and `b.SetBytes()`。

- [ ] **Step 2: Capture seven-run baseline**

```powershell
go test . -run '^$' -bench '^BenchmarkResponseModelMarkerScan$' -benchmem -benchtime=500ms -count=7
```

Save raw output andmedians totemporary benchmark log。

- [ ] **Step 3: Implement one literal fast path and remove confirmed duplicate scans**

Fullbody marker check：

```go
if bytes.Contains(body, []byte(`"model"`)) || bytes.Contains(body, []byte(`"modelVersion"`)) {
    return true
}
if !bytes.Contains(body, []byte{'\\'}) {
    return false
}
```

Onlythen runescaped-key state scanning。ForSSE `data:` lines，call theoptimized marker check perlogical value；a validJSON key cannotspanraw SSE line endings。When `rewriteEvent` or `rewriteMultiDataEvent` alreadyconfirmed a marker，call `restoreResponseModelCandidate` directly，not `restoreResponseModel`。Preserve `[DONE]` andnon-JSON paths。

- [ ] **Step 4: Run marker, escaped-key, SSE andallocation tests**

```powershell
go test . -run 'Test(MightContainResponseModelField|RestoreResponse|SSERewriter|StreamChunkRewriter)'
go test .
```

Expected：PASS；escaped keys stillrestore；ordinary escapedtext stillusesoneclone allocation path。

- [ ] **Step 5: Capture candidate results and apply gate**

Run theexact baseline command again。Keep onlyif64 KiB and1 MiB no-marker orliteral-marker median ns/op improvesat least15%，B/op doesnot increase，andescaped-key correctness passes。

- [ ] **Step 6: Keep or revert and commit**

Ifkept：

```powershell
git add -- main.go performance_regression_test.go
git commit -m "perf: reduce response marker scans"
```

Ifreverted：

```powershell
git add -- performance_regression_test.go
git commit -m "test: benchmark response marker scans"
```

Recordexact gate result in thetemporary benchmark log。

### Task 11: Runtime documentation and lane verification

**Files:**
- Modify: `CLAUDE.md:34-38`
- Verify only: allruntime-owned files

**Interfaces:**
- Documentation must matchactual code afterperformance keep/revert decisions。

- [ ] **Step 1: Update the response whitelist**

Add `interaction.model` to theexisting whitelist sentence。Do not alter unrelatedarchitecture text。

- [ ] **Step 2: Run formatting and runtime verification**

```powershell
gofmt -w main.go main_test.go performance_regression_test.go abi_cgo.go abi_cgo_test.go
go test .
go vet .
go test -race .
go test . -run '^$' -bench '^(BenchmarkRewriteTopLevelModel|BenchmarkResponseModelMarkerScan|BenchmarkCallerPatternCacheRetention)$' -benchmem -benchtime=200ms -count=3
```

Expected：allcommands PASS；benchmark outputs remainsemantically preflighted。

- [ ] **Step 3: Check lane diff and commit docs/formatting only ifneeded**

```powershell
git diff --check
git status --short
git add -- CLAUDE.md main.go main_test.go performance_regression_test.go abi_cgo.go abi_cgo_test.go
git commit -m "docs: document interactions model restoration"
```

Ifonly `CLAUDE.md` changed，stageonlythat file。Reportallruntime commit hashes andthe benchmark log path to theparent。

## Release Lane

### Task 12: Validate aggregate binary sidecars before writing release files

**Files:**
- Modify: `.github/scripts/package-release.go:68-93`
- Test: `.github/scripts/package-release_test.go:147-189`

**Interfaces:**
- Consumes sidecar: `artifact.binaryPath(distDir) + ".version"`。
- Preserves single-library mode used byGitHub matrix jobs。
- `packageExistingArtifacts(version, distDir, outDir string) error` stillreceivesnormalized version from `resolveVersion`。

- [ ] **Step 1: Update the success fixture withmatching sidecars**

For eachdummy binary in `TestPackageExistingArtifactsUsesSha256sumFormat`，write adjacent `.version` containing `0.1.0\n`。This keeps theexisting success oracle valid afterthe boundary isenforced。

- [ ] **Step 2: Add failing preflight tests**

Add table `TestPackageExistingArtifactsRejectsUnverifiedVersionsBeforeWriting` withcases：

- sidecar missing。
- sidecar empty。
- sidecar `0.0.0-dev` whiletarget `0.5.2`。
- sidecar `0.5.1` whiletarget `0.5.2`。

Each case createsat leasttwo binaries，onevalid andoneinvalid，calls `packageExistingArtifacts("0.5.2", dist, out)`，requireserror mentioningoffending artifact andexpected version，thenassertsout directory eitherdoes notexist orcontains nofiles。This detects a partial zip fromthe earlier valid artifact。

- [ ] **Step 3: Confirm RED**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -run 'TestPackage(ExistingArtifactsRejectsUnverifiedVersionsBeforeWriting|ExistingArtifactsUsesSha256sumFormat)$'
```

Expected：missing/mismatched sidecars areaccepted andarchives appear。

- [ ] **Step 4: Implement complete preflight before `MkdirAll`**

Firstcollect existing supported artifacts intoa small slice containingbinary path andfuturezip path。For eachfound binary：

```go
versionPath := binaryPath + ".version"
rawVersion, err := os.ReadFile(versionPath)
if err != nil {
    return fmt.Errorf("read artifact version %s: %w", filepath.ToSlash(versionPath), err)
}
builtVersion := strings.TrimSpace(string(rawVersion))
if builtVersion != version {
    return fmt.Errorf("artifact %s was built as %q, want %q", filepath.ToSlash(binaryPath), builtVersion, version)
}
```

Ifno supported binary，returnthe existingerror。Onlyafterall discovered binaries pass，call `os.MkdirAll(outDir)` andpackage them。Do not addsidecar tozip。

- [ ] **Step 5: Run all packager tests**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
```

Expected：PASS。

- [ ] **Step 6: Commit Task 12**

```powershell
git add -- .github/scripts/package-release.go .github/scripts/package-release_test.go
git commit -m "fix: verify aggregate artifact versions"
```

### Task 13: Normalize Make release versions and write build sidecars

**Files:**
- Modify: `Makefile:7-10`
- Modify: `Makefile:27-69`
- Test: `.github/scripts/check-release-compatibility_test.go:42-96`
- Test: `.github/scripts/package-release_test.go:24-54`

**Interfaces:**
- Produces Make variables `RELEASE_VERSION` and `BUILD_VERSION`。
- `BUILD_VERSION` is `0.0.0-dev` whenVERSION empty，otherwiseone-leading-v normalized。
- `build-platform` writes `$out.version` onlyaftera successfulbuild。

- [ ] **Step 1: Extend version grammar test**

Add `vv1.2.3` and `vv0.5.2` asinvalid rows to `TestResolveVersionValidatesReleaseGrammar`。Run it andconfirmexistingparser alreadypasses thispart。

- [ ] **Step 2: Add failing Make dry-run normalization test**

In `check-release-compatibility_test.go` add `TestBuildPlatformNormalizesLeadingVInMetadataAndSidecar`。Locate repo root usingthe existing pattern，run：

```go
exec.Command(makePath, "--no-print-directory", "-n", "build-platform",
    "VERSION=v0.5.2", "GOOS=windows", "GOARCH=amd64",
    "DIST_DIR="+filepath.ToSlash(t.TempDir()))
```

Require output contains `-X main.pluginVersion=0.5.2` andwrites `0.5.2` to `model-mapper.dll.version`。Require itdoes notcontain `-X main.pluginVersion=v0.5.2`。

- [ ] **Step 3: Add failing package archive-name integration test**

增加 `TestPackagePlatformNormalizesLeadingVInArchiveName`。在 `distDir := t.TempDir()` 下创建dummy `windows_amd64/model-mapper.dll` 和匹配的 `model-mapper.dll.version`，通过 `exec.Command` 运行 `make package-platform VERSION=v0.5.2 GOOS=windows GOARCH=amd64 MAKE=true DIST_DIR=` 加 `filepath.ToSlash(distDir)`。要求：

- `model-mapper_0.5.2_windows_amd64.zip` exists。
- `model-mapper_v0.5.2_windows_amd64.zip` doesnotexist。

- [ ] **Step 4: Confirm RED**

```powershell
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -run 'Test(BuildPlatformNormalizesLeadingVInMetadataAndSidecar|PackagePlatformNormalizesLeadingVInArchiveName)$'
```

Expected：ldflags orarchive containsleadingv，or sidecar command ismissing。

- [ ] **Step 5: Define normalized version variables once**

AtMakefile top：

```make
RELEASE_VERSION := $(patsubst v%,%,$(VERSION))
BUILD_VERSION := $(if $(RELEASE_VERSION),$(RELEASE_VERSION),0.0.0-dev)
VERSION_LDFLAGS := $(if $(VERSION),-X main.pluginVersion=$(RELEASE_VERSION),)
```

Raw `VERSION` stillgoes to `package-release.go -validate-only` beforepackaging，so `vv0.5.2` remainsinvalid even thoughMake string expansion removesonev。

- [ ] **Step 6: Write sidecar only after successful build**

Extend theone-shell `build-platform` recipe so thebuild command isfollowed by：

```sh
&& printf '%s\n' "$(BUILD_VERSION)" > "$$out.version"
```

Do not use `; printf`，which couldwritea freshsidecar afterfailedbuild。

- [ ] **Step 7: Use normalized version in archive paths and recursive make**

Use `$(RELEASE_VERSION)` forarchive basename。Remove theexplicit recursive override `VERSION_LDFLAGS="$(VERSION_LDFLAGS)"` fromthe `package` target，allowingsubmake toderive itfromrawVERSION。Do not changeGitHub workflow'salready normalizedVERSION path。

- [ ] **Step 8: Keep compatibility failure fixture valid**

Write `model-mapper.so.version` containing `0.5.1` in `TestPackagePlatformStopsAfterCompatibilityFailure`。The test must stillfailonGLIBC beforepackaging andleave noarchive。

- [ ] **Step 9: Run Make/release tests**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
```

Expected：PASS。

- [ ] **Step 10: Commit Task 13**

```powershell
git add -- Makefile .github/scripts/package-release_test.go .github/scripts/check-release-compatibility_test.go
git commit -m "fix: normalize release build versions"
```

### Task 14: Exercise raw credential recovery in live smoke and correct README

**Files:**
- Modify: `.github/scripts/smoke-local.go:120-137`
- Modify: `README.md:64`
- Modify: `README.md:184-192`
- Modify: `README.md:212-218`

**Interfaces:**
- No newhelper orcase type。
- ExistingOpenAI requests send `Authorization`；existingClaude requests send `X-Api-Key`。

- [ ] **Step 1: Change exactly two smoke rule prefixes**

In `openai-completions-rules-scoped-hit` andits pairedfallback rule string，replaceexact scope `local-smoke-key#` withwildcard `local-smoke-*#`。Do the same for `claude-messages-rules-scoped-hit` andits pairedfallback。Do not changeglobal/codex/streaming cases。

Thehit key `local-smoke-key` matches；`fallback-local-smoke-key` doesnotstart with `local-smoke-`，so thefallback case remainsa negative oracle。

- [ ] **Step 2: Compile the smoke helper**

```powershell
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

Expected：PASS。The case table itself isthe live regression oracle；do notextracta productionhelper solely forunit inspection。

- [ ] **Step 3: Correct README minimum CPA version and whitelist**

- Replace `v7.2.145` with `v7.2.101` in thecaller-scope requirement。
- Add `interaction.model` to theresponse restoration list。
- Keep thefail-closed explanation forolder/missingmetadata behavior。

- [ ] **Step 4: Correct README build/package sequence**

Useone explicitversion throughout：

```powershell
make test
make vet
make build-windows-amd64 VERSION=0.5.2
make build-linux-amd64 VERSION=0.5.2 LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"
make package VERSION=0.5.2
```

State concisely thataggregate packaging verifies each adjacent `.version` sidecar andfailsifbuilt version differs。

- [ ] **Step 5: Check docs and helper diff**

```powershell
git diff --check
git diff -- .github/scripts/smoke-local.go README.md
```

Expected：onlytwo case rule pairs andthree documented facts change。

- [ ] **Step 6: Commit Task 14**

```powershell
git add -- .github/scripts/smoke-local.go README.md
git commit -m "test: smoke wildcard caller credentials"
```

### Task 15: Release lane verification

**Files:**
- Verify only: release-owned files

- [ ] **Step 1: Format Go helpers**

```powershell
gofmt -w .github/scripts/package-release.go .github/scripts/package-release_test.go .github/scripts/check-release-compatibility_test.go .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

- [ ] **Step 2: Run all script test binaries explicitly**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

Expected：PASS。Never run `go test ./.github/scripts`。

- [ ] **Step 3: Run dry-run assertions manually**

```powershell
make -n build-platform VERSION=v0.5.2 GOOS=windows GOARCH=amd64
make -n package-platform VERSION=v0.5.2 GOOS=windows GOARCH=amd64
```

Expected：ldflags andsidecar contentuse `0.5.2`；archive is `model-mapper_0.5.2_windows_amd64.zip`。

- [ ] **Step 4: Check lane diff and report commits**

```powershell
git diff --check
git status --short
```

Commit anygofmt-only changes withthe task whosefiles theybelong to，not a broadcleanup commit。Reportrelease commit hashes toparent。

## Integration and Review

### Task 16: Integrate both isolated lanes

**Files:**
- Integrate only; do notedit user `.claude/` state

- [ ] **Step 1: Confirm parent branch state**

```powershell
git branch --show-current
git status --short
git log -3 --oneline
```

Expected：branch `fix/v0.5.2-audit`；onlyuser's pre-existing `.claude/` remainsuntracked beforecherry-picks。

- [ ] **Step 2: Cherry-pick Runtime lane commits in reported order**

按runtime agent报告的先后顺序执行一次 `git cherry-pick`，参数使用其返回的全部exact commit hashes。先核对第一个commit的parent为 `$PlanBase`，不要使用branch范围猜测commit顺序。

- [ ] **Step 3: Cherry-pick Release lane commits in reported order**

按release agent报告的先后顺序执行一次 `git cherry-pick`，参数使用其返回的全部exact commit hashes。先核对第一个commit的parent为 `$PlanBase`。

Becausefile ownership isdisjoint，no conflict isexpected。Ifaconflict occurs，stopthat cherry-pick，inspect onlythe conflicting assignedfile andpreserve bothspec-required behaviors。

- [ ] **Step 4: Run integration smoke tests**

```powershell
go test .
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

Expected：PASS。

### Task 17: Non-overlapping Opus 1M xhigh review

**Files:**
- Runtime reviewer only: `main.go`、`main_test.go`、`performance_regression_test.go`、`abi_cgo.go`、`abi_cgo_test.go`、`CLAUDE.md`
- Release reviewer only: `Makefile`、`.github/scripts/package-release.go`、`.github/scripts/package-release_test.go`、`.github/scripts/check-release-compatibility_test.go`、`.github/scripts/smoke-local.go`、`.github/scripts/smoke-local_test.go`、`README.md`

- [ ] **Step 1: Launch both read-only reviews in one parallel tool batch**

每个reviewer的prompt直接包含其lane requirements和exact base/HEAD range，因此都不需要读取另一个lane或任何旧plan。Prompt requirements：

- UseOpus 1M xhigh。
- Review onlyassigned paths。
- Verifycorrectness、data loss、deadlock/race、header/framing consistency、test oracle strength andscope discipline。
- Report onlyconfirmed orhigh-confidence findings withconcrete input/state -> wrong result andfile:line。
- Do notedit。
- Runtime reviewer additionally checksbenchmark methodology andwhetheronlygated candidates survived。
- Release reviewer additionally checksnooutput iswritten beforeall sidecars validate andleading-v normalization reachesldflags/sidecar/archive。

- [ ] **Step 2: Verify every reported finding before editing**

For eachfinding，reproduce withthe smallesttargeted test。Discard findings thatconflict withthe explicitnon-backtracking、non-SSE separator orCPA trust-boundary decisions。Do not sendthe samefile toanother reviewer forduplicate analysis。

- [ ] **Step 3: Fix confirmed findings through the original lane agent**

恢复对应的Sonnet agent，只发送已验证finding并复用原worktree context。要求先得到RED test，再做minimal fix、确认GREEN并创建新commit。随后cherry-pick该commit。Ifagent API isunavailable，main session applies the sameTDD steps directly。

- [ ] **Step 4: Run targeted verification after eachfix**

Run thenamed regression test plusits subsystem group。Do not rerunanother broadreview ofalready-cleared files。

## Final Verification

### Task 18: Run complete source, race, benchmark and package verification

**Files:**
- Verify working tree andgenerated ignored artifacts only

- [ ] **Step 1: Inspect before cleaning generated output**

```powershell
git status --short
git diff --check
```

List `dist/` ifitexists，confirm itisignored/generated，thenrun：

```powershell
make clean
```

Do not touch `.test-cpa/` or `.claude/`。

- [ ] **Step 2: Run all source verification commands**

```powershell
go test ./...
go vet ./...
go test -race ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

Expected：allPASS。Record exact outputs orcommands in final report；do not claima skipped command passed。

- [ ] **Step 3: Rerun retained performance matrices**

```powershell
go test . -run '^$' -bench '^(BenchmarkRewriteTopLevelModel|BenchmarkResponseModelMarkerScan|BenchmarkCallerPatternCacheRetention)$' -benchmem -benchtime=500ms -count=5
```

Expected：semantic preflight passes；retained cache <=32,768；kept candidates stillmeettheir recorded direction。Append finalvalues tothetemporary benchmark log。

- [ ] **Step 4: Build Windows amd64 withrelease version**

```powershell
make build-windows-amd64 VERSION=0.5.2
```

Expected：`dist/windows_amd64/model-mapper.dll` andadjacent `.version` exist；sidecar contentexactly `0.5.2`。

- [ ] **Step 5: Build Linux amd64 whenZig isavailable**

Check `Get-Command zig`。Ifavailable：

```powershell
make build-linux-amd64 VERSION=0.5.2 LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"
```

Expected：library andsidecar content `0.5.2`。IfZig isunavailable，recordthat Linux localbuild wasskipped；GitHub matrix remainsrequired beforecompletion。

- [ ] **Step 6: Package onlycleanly built artifacts**

```powershell
make package VERSION=0.5.2
```

Expected：aggregate preflight succeeds，everyarchive nameomitsleadingv，`checksums.txt` hasone line perlocally built supported artifact。Openarchives andconfirmonlyrootlibrary plusoptionalrootLICENSE，no `.version` sidecar。

- [ ] **Step 7: Verify binary/version evidence**

For eachlocal binary，confirm：

- sidecar exact `0.5.2`。
- build dry-run uses `-X main.pluginVersion=0.5.2`。
- binary byte strings contain `0.5.2`。

Sidecar和经过测试的build recipe是aggregate边界的权威证据；binary string scan只作为附加release证据，因为linker可以保留不再生效的默认string literal。

- [ ] **Step 8: Run live smoke ifboth required values areavailable**

If `CPA_SMOKE_API_KEY` isnonempty and `CPA_SMOKE_CPA_BIN` namesan existing executable：

```powershell
make smoke-local VERSION=0.5.2
```

Requireall cases，includingOpenAI Authorization wildcard andClaude X-Api-Key wildcard，printsuccess。Otherwise recordexactlywhich prerequisite ismissing andmarkonlythe live external smoke asskipped。

- [ ] **Step 9: Verify final diff scope**

```powershell
git status --short
git diff 81efc90b7996e71d202372d006c4d7ecae6931c5..HEAD --check
git diff --stat 81efc90b7996e71d202372d006c4d7ecae6931c5..HEAD
git log --oneline 81efc90b7996e71d202372d006c4d7ecae6931c5..HEAD
```

Expected：onlyspec、plan anddeclared runtime/release fileschanged；`.claude/plan/tender-yawning-shamir.md` remainsuntracked anduntouched；`dist/` remainsignored。

## Merge and Release

### Task 19: Merge verified branch into local `main`

**Files:**
- Git refs only

- [ ] **Step 1: Ensure implementation branch is clean except preserved user state**

```powershell
git status --short
git rev-parse HEAD
```

Ifcode/doc changes areuncommitted，commit them bytheir owningtask beforecontinuing。Never stage `.claude/`。

- [ ] **Step 2: Switch to main and fast-forward merge**

```powershell
git switch main
git merge --ff-only fix/v0.5.2-audit
```

Expected：local `main` 从 `81efc90b7996e71d202372d006c4d7ecae6931c5` fast-forward到verified branch HEAD，无merge conflict。

- [ ] **Step 3: Rerun release-critical verification on `main`**

```powershell
go test ./...
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

Expected：PASS。

### Task 20: Tag, push, monitor workflow and validate release assets

**Files:**
- GitHub refs、Actions run和release assets

- [ ] **Step 1: Confirm remote baseline and tag absence**

```powershell
git fetch origin main --tags
git status --short
git rev-parse main
git rev-parse origin/main
git tag --list v0.5.2
git ls-remote --tags origin refs/tags/v0.5.2
```

Expected beforepublish：localmain containsverified commits；remote `v0.5.2` absent。Ifremote main advanced independently，do notrewrite it；integrate onlyifchanges arecompatible andrerunverification。

- [ ] **Step 2: Create annotated release tag**

```powershell
git tag -a v0.5.2 -m "model-mapper v0.5.2"
```

- [ ] **Step 3: Push main and tag**

```powershell
git push origin main
git push origin v0.5.2
```

This isexplicitly authorized bythe task。Do notforce-pushmain。

- [ ] **Step 4: Locate and watch the tag-triggered Build workflow**

Use `gh run list --workflow build.yml --event push --limit 20 --json databaseId,headBranch,headSha,status,conclusion,url` andselectthe run whoseheadBranch/tag is `v0.5.2` andheadSha equalstag target。Then：

```powershell
$TagSHA = git rev-list -n 1 v0.5.2
$Runs = gh run list --workflow build.yml --event push --limit 20 --json databaseId,headSha | ConvertFrom-Json
$RunID = ($Runs | Where-Object { $_.headSha -eq $TagSHA } | Select-Object -First 1).databaseId
if (-not $RunID) { throw "No Build workflow found for $TagSHA" }
gh run watch $RunID --exit-status
```

Requiretest、build matrix、Windows ARM64、FreeBSD andRelease jobs success。Ifafailure istransient，rerunfailed jobs andwatch。Ifsource-related，reproduce locally，applyTDD fix on `fix/v0.5.2-audit` or anewrelease-fix branch，mergeandreverify beforemovingan unpublishedfailed tag；neveroverwritean already publishedsuccessful release。

- [ ] **Step 5: Inspect release metadata and asset matrix**

```powershell
gh release view v0.5.2 --json tagName,targetCommitish,url,assets
```

Requirethese eightassets：

```plaintext
model-mapper_0.5.2_linux_amd64.zip
model-mapper_0.5.2_linux_arm64.zip
model-mapper_0.5.2_darwin_amd64.zip
model-mapper_0.5.2_darwin_arm64.zip
model-mapper_0.5.2_windows_amd64.zip
model-mapper_0.5.2_windows_arm64.zip
model-mapper_0.5.2_freebsd_amd64.zip
checksums.txt
```

Noarchive maycontain `v0.5.2` inits filename。

- [ ] **Step 6: Download and verify every release asset**

创建唯一临时目录并下载release assets：

```powershell
$ReleaseDir = Join-Path ([IO.Path]::GetTempPath()) ("cpa-plugin-v0.5.2-release-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $ReleaseDir | Out-Null
gh release download v0.5.2 --dir $ReleaseDir
```

验证：

- `checksums.txt` hasexactlyseven sha256sum-format lines usingarchive basenames only。
- eachcomputed SHA-256 matchesits line。
- eachzip haslibrary atzip root andoptional `LICENSE` only。
- nozip containsa `.version` sidecar ornested directory。
- 每个library都包含build string `0.5.2`；不以binary中是否残留未生效的default literal判定版本。

- [ ] **Step 7: Record final repository and release state**

```powershell
git status --short
git log -1 --oneline
git show --no-patch --decorate v0.5.2
```

Final report must state：

- confirmed fixes completed。
- performance candidateskept orreverted withmeasured medians。
- exact verification commands andany unavailablelive-smoke prerequisite。
- main commit、tag、workflow URL、release URL andasset count。
- preserved `.claude/plan/tender-yawning-shamir.md` remainsuntouched。
