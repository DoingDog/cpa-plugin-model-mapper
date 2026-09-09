# CPA Model Mapper 0.5.3 Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复0.5.3审计确认的插件、ABI、release和live smoke缺陷，以可重复benchmark评估高并发和长payload路径，并发布经过验证的`v0.5.3`。

**Architecture:** 保持单package和现有CPA ABI/RPC边界。core修改集中在`main.go`的共享JSON/stream/config/caller helper；独立脚本只在各自文件中修复输入验证和测试判定。production性能候选与正确性修复分开测量，未通过gate即回退。

**Tech Stack:** Go 1.26.5、cgo `c-shared`、`encoding/json`、`net/http`、`gopkg.in/yaml.v3`、Go benchmark/race/checkptr/cgocheck2、GNU Make、Zig 0.16.0、GitHub Actions/GitHub CLI。

**Spec:** `docs/superpowers/specs/2026-09-09-cpa-model-mapper-0.5.3-audit-design.md`

## Global Constraints

- 只修改`cpa-plugin-model-mapper`，不修改CPA源码。
- 基线commit为`599737af7077d43bb7dcb9149c801427ff076543`，目标tag为`v0.5.3`。
- 每个功能修复先得到聚焦RED，再修改production代码得到GREEN。
- 不改变规则DSL的first-occurrence、non-backtracking wildcard语义。
- 响应恢复只允许`model`、`modelVersion`、`response.model`、`response.modelVersion`、`message.model`和`interaction.model`。
- 不增加dependency，不做无关重构，不读取本次任务以外的旧spec或plan。
- 性能production改动必须使目标case的median `ns/op`至少改善10%，`B/op`与`allocs/op`不恶化，并通过完整语义验证。
- 保留原checkout的`.claude/plan/tender-yawning-shamir.md`。
- live smoke仅在`CPA_SMOKE_API_KEY`和`CPA_SMOKE_CPA_BIN`均存在时运行；否则记录未运行。

## File Map and Parallel Ownership

- Core owner，顺序执行Tasks 2至5：`main.go`、`main_test.go`、`README.md`。其他worker不得读取或修改这三个文件。
- Benchmark owner，先执行Task 1；Task 10必须在Core owner完成后接管`performance_regression_test.go`，不得同时修改`main.go`。
- ABI owner：`abi_cgo.go`、`abi_cgo_test.go`。
- Compatibility owner：`.github/scripts/check-release-compatibility.go`、`.github/scripts/check-release-compatibility_test.go`。
- Package owner：`.github/scripts/package-release.go`、`.github/scripts/package-release_test.go`。
- Smoke owner：`.github/scripts/smoke-local.go`、`.github/scripts/smoke-local_test.go`。
- Coordinator只整合、审查、运行跨域verification和发布，不在worker执行期间重复扫描相同文件。

独立owner可以并行执行。Tasks 2至5共享`main.go`和`main_test.go`，必须由同一个Core owner依次完成。Task 10依赖Task 1的baseline和所有production修改，最后执行。

---

### Task 1: 修正并扩展性能benchmark，记录production基线

**Files:**
- Modify: `performance_regression_test.go:43-161,341-394,463-475`
- Baseline output outside repository: `C:\Users\user\AppData\Local\Temp\cpa-plugin-0.5.3-bench-before.txt`

**Interfaces:**
- Consumes: `restoreResponseModel([]byte,string) ([]byte,bool,error)`、`callerPatternMatch(*rule,string,string) (bool,bool)`、`emitRewritten([][]byte,bool,func([]byte) error) error`、`newStreamChunkRewriter(string) *streamChunkRewriter`。
- Produces: `BenchmarkRestoreResponseModel`、`BenchmarkCallerPatternCacheWarmParallel`、`BenchmarkEmitRewrittenBatch`以及可信的现有stream benchmark。

- [ ] **Step 1: 修正complete-SSE计时边界**

在fixture和一次untimed semantic check后开始计时：

```go
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
		chunks, err := r.Write(payload)
		if err != nil || len(chunks) != 1 {
			b.Fatalf("Write=(%d,%v), want one batch", len(chunks), err)
		}
	}
}
```

- [ ] **Step 2: 为fragmented raw JSON加入untimed内容校验和sink**

先运行一次32 fragments，拼接所有output和Flush结果，要求valid JSON、`model == "client"`、64 KiB opaque ID完整。计时循环中把总output byte count写入package级`benchmarkStreamOutputBytes int`，不能只检查slice数量。

- [ ] **Step 3: 增加完整response restoration benchmark**

加入4 KiB、64 KiB、1 MiB、8 MiB fixtures和这些case：`no-marker`、`same-model`、`top-level-changed`、`nested-response-changed`、`gemini-array-changed`。每个case在`ResetTimer`前调用一次`restoreResponseModel`并断言`changed`和目标字段；计时循环调用完整restore并把返回值写入package sinks。

核心形态：

```go
func BenchmarkRestoreResponseModel(b *testing.B) {
	for _, size := range []int{4 << 10, 64 << 10, 1 << 20, 8 << 20} {
		for _, fixture := range restoreResponseBenchmarkFixtures(size) {
			b.Run(fmt.Sprintf("%d/%s", size, fixture.name), func(b *testing.B) {
				out, changed, err := restoreResponseModel(fixture.body, "client")
				if err != nil || changed != fixture.changed {
					b.Fatalf("preflight restore=(%d,%v,%v)", len(out), changed, err)
				}
				b.SetBytes(int64(len(fixture.body)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchmarkRewriteTopLevelModelOutput, benchmarkRewriteTopLevelModelChanged, err = restoreResponseModel(fixture.body, "client")
					if err != nil { b.Fatal(err) }
				}
			})
		}
	}
}
```

Fixture必须把padding放在opaque string中，且Gemini array至少有两个immediate response object。

- [ ] **Step 4: 增加warm caller cache并发benchmark**

预先compile一条`sk-*#client=>target`并warm cache。增加`hot-key`和`working-set-1024`两个`RunParallel` subbenchmark。每个worker使用自己的local index遍历预计算scope/key，避免额外atomic成为主要测量对象；每次要求`matched && authenticated`。

- [ ] **Step 5: 增加multi-chunk emit benchmark**

对总大小64 KiB和1 MiB、chunk count 1/2/32/128构造fixture；untimed preflight断言一次emit且总字节一致。计时内把emitted length写入sink。

- [ ] **Step 6: 运行benchmark测试确保新基准有效**

Run：

```powershell
go test . -run '^$' -bench 'Benchmark(RestoreResponseModel|CallerPatternCacheWarmParallel|EmitRewrittenBatch|StreamChunkRewriterCompleteSSEBatch|StreamChunkRewriterFragmentedRawJSON)$' -benchmem -benchtime=200ms -count=1
```

Expected：所有benchmark运行；没有semantic preflight failure。

- [ ] **Step 7: 记录未修改production代码的基线**

Run：

```powershell
go test . -run '^$' -bench 'Benchmark(RestoreResponseModel|CallerPatternCacheWarmParallel|EmitRewrittenBatch|SSERewriterSplitLargeEvent|SSERewriterMultiEventBatches|StreamChunkRewriterCompleteSSEBatch|StreamChunkRewriterFragmentedRawJSON)$' -benchmem -benchtime=500ms -count=5 -cpu=1,8,32 | Tee-Object -FilePath 'C:\Users\user\AppData\Local\Temp\cpa-plugin-0.5.3-bench-before.txt'
```

Expected：exit 0，output文件包含每个case的5次结果。

- [ ] **Step 8: Commit benchmark-only修改**

```powershell
git add performance_regression_test.go
git commit -m "test: strengthen performance audit benchmarks"
```

---

### Task 2: 保留raw JSON separator并恢复Gemini streamed arrays

**Files:**
- Modify: `main.go:489-595,1935-1981`
- Modify: `main_test.go:1858-1989,3823-3923,4053-4106,4188-4283`

**Interfaces:**
- Consumes: `skipTopLevelModelJSONSpace`、`skipTopLevelModelJSONValue`和现有object response whitelist helper。
- Produces: `rewriteResponseModelFieldsWithReplacementChecked`支持object或array root；unframed raw JSON output保留所有value外部whitespace。

- [ ] **Step 1: 写raw separator失败测试**

增加`TestStreamChunkRewriterPreservesUnframedRawJSONSeparators`，覆盖：

```go
cases := []struct{ name, input, want string }{
	{"single trailing LF", "{\"model\":\"upstream\"}\n", "{\"model\":\"client\"}\n"},
	{"leading and trailing", " \t{\"model\":\"upstream\"}\r\n", " \t{\"model\":\"client\"}\r\n"},
	{"blank-line objects", "{\"model\":\"upstream\"}\n\n{\"model\":\"upstream\"}\n", "{\"model\":\"client\"}\n\n{\"model\":\"client\"}\n"},
	{"scalar boundary", "1 2\n", "1 2\n"},
}
```

对每个case执行all-in-one和每个single split point，调用Write后Flush，`bytes.Join(chunks,nil)`必须等于want。

- [ ] **Step 2: 运行separator测试确认RED**

Run：

```powershell
go test . -run '^TestStreamChunkRewriterPreservesUnframedRawJSONSeparators$' -count=1
```

Expected：FAIL，至少trailing LF丢失，`1 2`变成`12`。

- [ ] **Step 3: 写Gemini array失败测试**

增加`TestRestoreResponseModelRestoresGeminiStreamArray`。输入array含两个object，分别有顶层`modelVersion:"upstream"`；第一个还含：

```json
{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"args":{"model":"opaque","modelVersion":"opaque"}}}]}}]}
```

断言每个immediate object的`modelVersion`变成`client`，nested args与`role`不变，outer `\n,\n` separator仍存在。另测`[]`、`[null,1,{"id":"x"}]`为byte-identical clone且`changed=false`。

- [ ] **Step 4: 运行Gemini测试确认RED**

Run：

```powershell
go test . -run '^TestRestoreResponseModelRestoresGeminiStreamArray$' -count=1
```

Expected：FAIL，array中`modelVersion`仍为`upstream`。

- [ ] **Step 5: 保留单值和多值separator**

在single-value fast path计算trimmed value的`start/end`。当unframed且未修改时直接`bytes.Clone(p)`；修改时构造`p[:start] + restored + p[end:]`。

把`splitJSONValues`成功到EOF时的`consumed`设为`len(p)`。在多值output循环中从前一value end开始找到当前raw value start：

```go
start := skipTopLevelModelJSONSpace(p, cursor)
end := start + len(value)
```

unframed chunk包含`p[cursor:start]`和restored value；完整输入的最后一个chunk再包含`p[end:consumed]`。有incomplete tail时`consumed`仍停在最后一个完整value end，让separator随tail进入`pending`。framed path继续只frame JSON value。

- [ ] **Step 6: 用最小array dispatcher扩展response restoration**

把现有object逻辑移到`rewriteResponseModelObjectWithReplacementChecked`。dispatcher先trim并按root token选择object或array。array helper先用`json.Valid`确认完整文档，再用`skipTopLevelModelJSONSpace`和`skipTopLevelModelJSONValue`定位每个immediate element；只把object element送给object helper。首次change时构造output并复制原outer bytes和separator；没有change则返回clone。

签名保持：

```go
func rewriteResponseModelFieldsWithReplacementChecked(body []byte, model string, replacement json.RawMessage) ([]byte, bool, bool, error)
```

`valid=true`表示完整合法object/array/scalar JSON；只有object或object elements能change。

- [ ] **Step 7: 增加Gemini executor stream integration测试**

用`Format: "gemini"`、`SourceFormat: "gemini"`、没有body model的Gemini request和`Content-Type: application/json`。host reads把JSON array分成至少3个任意boundary；最终assert output仍是完整array、每个顶层`modelVersion`为client、没有`data:`。

- [ ] **Step 8: 运行core聚焦测试得到GREEN**

```powershell
go test . -run '^(TestStreamChunkRewriterPreservesUnframedRawJSONSeparators|TestRestoreResponseModelRestoresGeminiStreamArray|TestHandleExecutorExecuteStreamRestoresGeminiJSONStreamArray|TestStreamChunkRewriter.*RawJSON.*|TestRawJSON.*|TestRestoreResponseModel.*)$' -count=1
```

Expected：PASS。

- [ ] **Step 9: Commit**

```powershell
git add main.go main_test.go
git commit -m "fix: preserve streamed JSON framing"
```

---

### Task 3: 恢复同一credential source中的authenticated后续值

**Files:**
- Modify: `main.go:2074-2095`
- Modify: `main_test.go:524-549,3207-3288`

**Interfaces:**
- Produces: `callerAPIKey(http.Header,url.Values,string) string`维持签名和source优先级，但遍历所有presented values。

- [ ] **Step 1: 扩展callerAPIKey table为失败测试**

加入：

```go
{name: "later google header", headers: http.Header{"X-Goog-Api-Key": {"spoofed", "sk-google"}}, scope: callerScope("sk-google"), want: "sk-google"},
{name: "later anthropic header", headers: http.Header{"X-Api-Key": {"spoofed", "sk-anthropic"}}, scope: callerScope("sk-anthropic"), want: "sk-anthropic"},
{name: "later query key", query: url.Values{"key": {"spoofed", "sk-query"}}, scope: callerScope("sk-query"), want: "sk-query"},
{name: "later bearer", headers: http.Header{"Authorization": {"Bearer spoofed", "Bearer sk-auth"}}, scope: callerScope("sk-auth"), want: "sk-auth"},
{name: "no digest match", headers: http.Header{"X-Api-Key": {"one", "two"}}, scope: callerScope("three")},
```

再加route-level wildcard case，later value匹配`sk-prod-*#client=>target`。

- [ ] **Step 2: 运行确认RED**

```powershell
go test . -run '^(TestCallerAPIKeyUsesOnlyAuthenticatedCredential|TestHandleExecutorExecuteUsesCallerScopeAcrossRuleSets)$' -count=1
```

Expected：later-value cases FAIL。

- [ ] **Step 3: 按source和value顺序恢复credential**

使用`headers.Values("Authorization")`等slice和`query["key"]`/`query["auth_token"]`。每个Authorization value独立执行当前Bearer解析；每个candidate trim后立即比较`callerScope(candidate) == scope`。不要缓存或返回digest不匹配值。

- [ ] **Step 4: 运行聚焦caller测试**

```powershell
go test . -run 'Caller(APIKey|Pattern|Scope)|Scoped' -count=1
```

Expected：PASS。

- [ ] **Step 5: Commit**

```powershell
git add main.go main_test.go
git commit -m "fix: inspect all authenticated credential values"
```

---

### Task 4: 生成format-specific raw JSON SSE framing

**Files:**
- Modify: `main.go:30-44,304-335,489-647,1124-1132,1230-1312,1388-1432`
- Modify: `main_test.go:3634-3652,3769-3950`及stream test helper

**Interfaces:**
- `streamChunkRewriter`新增`format string`、`framedRawJSON bool`。
- `sseRewriter`新增`sawDone bool`。
- `executorStream`新增`format string`。
- 新增`frameSSEEvent(p []byte, eventType string) []byte`；`frameSSEData(p)`保留为data-only wrapper。
- 新增`(*streamChunkRewriter).Finish() ([][]byte,error)`，只供clean host completion使用。

- [ ] **Step 1: 写Responses、Claude和Gemini framing失败测试**

对`frameRawJSONAsSSE=true`，设置format并写一个完整raw JSON object：

```go
cases := []struct{ format, payload, wantPrefix string }{
	{"openai-response", `{"type":"response.completed","response":{"model":"upstream"}}`, "event: response.completed\ndata: "},
	{"claude", `{"type":"message_start","message":{"model":"upstream"}}`, "event: message_start\ndata: "},
	{"gemini", `{"modelVersion":"upstream"}`, "data: "},
	{"interactions", `{"type":"interaction.completed","interaction":{"model":"upstream"}}`, "data: "},
}
```

断言Responses/Claude event line与top-level type一致；Gemini/Interactions没有`event:`；所有target model恢复。

- [ ] **Step 2: 写OpenAI clean/error termination失败测试**

通过`runStreamForward`测试：

1. format=`openai`、SSE content type、一个raw Chat chunk、host `Done=true` -> joined output以且只含一次`data: [DONE]\n\n`。
2. raw chunk后host read error -> flush pending bytes，但不含`[DONE]`。
3. raw chunk后已经framed的`data: [DONE]\n\n` -> clean completion不重复terminal。
4. format为Responses、Claude或Gemini -> clean completion不添加`[DONE]`。

- [ ] **Step 3: 运行确认RED**

```powershell
go test . -run '^(TestStreamChunkRewriterFramesRawJSONByFormat|TestRunStreamForwardTerminatesReframedOpenAIChat|TestRunStreamForwardDoesNotFabricateDoneOnError)$' -count=1
```

Expected：缺少event line或`[DONE]`而FAIL。

- [ ] **Step 4: 实现event framing**

`frameSSEEvent`在`eventType != ""`时先写`event: `、type和LF，再复用当前多line `data:`写法。`streamChunkRewriter.frameRawJSON`仅在format为`openai-response`或`claude`时用一个小struct解析顶层`type`；解析失败或type为空时data-only。每次真正frame raw JSON时设置`framedRawJSON=true`。

所有`tryRawJSONChunks`中的`frameSSEData(restored)`改为`r.frameRawJSON(restored)`；incomplete fallback仍只能data-frame原始不完整bytes，不能从invalid JSON猜event type。

- [ ] **Step 5: 记录和生成Chat terminal**

`sseRewriter.rewriteEvent`遇到trim后的`data:` value为`[DONE]`时设置`sawDone=true`并原样输出。

`streamChunkRewriter.Finish`先调用`Flush`，然后仅在以下条件全部成立时append `[]byte("data: [DONE]\n\n")`：

```go
r.format == "openai" && r.frameRawJSONAsSSE && r.framedRawJSON && !r.sse.sawDone
```

给`executorStream`保存`req.Format`。让`finish`接收`cleanCompletion bool`；normal `chunk.Done`且没有payload/host error时使用`Finish`，所有error path继续使用`Flush`。

- [ ] **Step 6: 验证already-framed和application/json不变**

运行现有BOM、CR/LF/CRLF、多data、split prefix、raw WebSocket和header tests。新增断言：already-framed Responses event的`event:`、spacing、line ending和delimiter byte-identical；`application/json`不出现`event:`或`data:`。

- [ ] **Step 7: 运行stream测试**

```powershell
go test . -run '^(Test(SSE|Stream|Frame|HandleExecutorExecuteStream|RunStreamForward).*)$' -count=1
```

Expected：PASS。

- [ ] **Step 8: Commit**

```powershell
git add main.go main_test.go
git commit -m "fix: preserve provider stream protocols"
```

---

### Task 5: 拒绝额外lifecycle YAML document并更新0.5.3文档

**Files:**
- Modify: `main.go:1541-1584`
- Modify: `main_test.go:67-340`
- Modify: `README.md:180-195,211-221,252-272`

**Interfaces:**
- `decodeLifecycleConfig`签名不变。

- [ ] **Step 1: 写YAML多document atomic rejection测试**

构造base64 lifecycle payload，cases：

```go
[]string{
	"global_rules: new=>target\n---\nglobal_rules: hidden=>target\n",
	"global_rules: new=>target\n---\n",
	"global_rules: new=>target\n...\ntrailing: [\n",
}
```

先发布`old=>target`，对每个payload调用`applyLifecycleConfig`必须error；随后`routeModel`仍处理old且不处理new/hidden。单document加尾随comments作为success control。

- [ ] **Step 2: 运行确认RED**

```powershell
go test . -run '^TestApplyLifecycleConfigRejectsAdditionalYAMLDocumentsAtomically$' -count=1
```

Expected：前两个case至少一个被接受而FAIL。

- [ ] **Step 3: 改用yaml.Decoder并要求EOF**

空decoded YAML继续产生zero config。非空时第一次`Decode(&yamlConfig)`必须成功，随后第二次`Decode(&extra)`必须返回`io.EOF`；nil error表示额外document，其他error作为trailing YAML error返回。不要调用`KnownFields(true)`。

- [ ] **Step 4: 运行lifecycle测试**

```powershell
go test . -run 'Lifecycle|DecodeConfig|Reconfigure' -count=1
```

Expected：PASS。

- [ ] **Step 5: 更新README**

将build/package示例版本改成`0.5.3`。在Rewrite boundaries中写清：

- unframed raw JSON保留value separator。
- Gemini streamed JSON array逐element恢复顶层`modelVersion`。
- raw JSON转SSE时Responses/Claude生成`event:`、Chat clean completion生成`[DONE]`，Gemini无`[DONE]`。
- live smoke config在每个case结束后删除。

不新增Interactions未证实协议声明。

- [ ] **Step 6: 运行core全测试**

```powershell
go test . -count=1
```

Expected：PASS。

- [ ] **Step 7: Commit**

```powershell
git add main.go main_test.go README.md
git commit -m "fix: validate lifecycle configuration input"
```

---

### Task 6: 拒绝ABI `NULL`与非零request length

**Files:**
- Modify: `abi_cgo.go:63-76,118-150`
- Modify: `abi_cgo_test.go:1-42`

**Interfaces:**
- 新增`copyPluginRequest(unsafe.Pointer,uint64) ([]byte,bool)`，供ABI入口和unit test共用。

- [ ] **Step 1: 写pointer/length失败测试**

增加：

```go
func TestCopyPluginRequestRejectsNullWithLength(t *testing.T) {
	if got, ok := copyPluginRequest(nil, 1); ok || got != nil {
		t.Fatalf("copyPluginRequest(nil,1)=(%v,%v), want (nil,false)", got, ok)
	}
	if got, ok := copyPluginRequest(nil, 0); !ok || got != nil {
		t.Fatalf("copyPluginRequest(nil,0)=(%v,%v), want (nil,true)", got, ok)
	}
	if _, ok := copyPluginRequest(nil, maxCIntLength+1); ok {
		t.Fatal("oversized request accepted")
	}
}
```

- [ ] **Step 2: 运行确认RED**

```powershell
go test . -run '^TestCopyPluginRequestRejectsNullWithLength$' -count=1
```

Expected：compile FAIL，helper未定义。

- [ ] **Step 3: 实现统一验证和复制**

`copyPluginRequest`先调用`cIntLength`，再拒绝`ptr == nil && length != 0`，zero length返回nil,true，其余调用`C.GoBytes`。`cliproxyPluginCall`在response置零后调用它，false立即return 1；删除入口内重复长度与nil判断。

- [ ] **Step 4: 运行ABI和全package测试**

```powershell
go test . -run '^(TestCIntLengthBounds|TestPluginResponseLengthBounds|TestCopyPluginRequestRejectsNullWithLength)$' -count=1
go test . -count=1
```

Expected：PASS。

- [ ] **Step 5: Commit**

```powershell
git add abi_cgo.go abi_cgo_test.go
git commit -m "fix: validate ABI request buffers"
```

---

### Task 7: 拒绝non-numeric GLIBC requirements

**Files:**
- Modify: `.github/scripts/check-release-compatibility.go:14,44-55`
- Modify: `.github/scripts/check-release-compatibility_test.go:12-40`

**Interfaces:**
- `checkGLIBCCompatibility(io.Reader,string) error`签名不变。

- [ ] **Step 1: 写失败测试**

在table加入：

```go
{name: "DT RELR ABI requirement", output: "Name: GLIBC_2.2.5\nName: GLIBC_ABI_DT_RELR\n", wantErr: true},
{name: "private ABI requirement", output: "Name: GLIBC_2.17\nName: GLIBC_PRIVATE\n", wantErr: true},
```

error断言包含完整unsupported token。

- [ ] **Step 2: 运行确认RED**

```powershell
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -run '^TestCheckGLIBCCompatibility$' -count=1
```

Expected：两个case错误通过。

- [ ] **Step 3: 扫描所有GLIBC token**

用一个regexp匹配`GLIBC_`后的ASCII alphanumeric、underscore和dot。对每个token：若完整suffix符合numeric dotted grammar，则加入versions；否则立即返回`unsupported GLIBC requirement <token>`。继续复用`checkMaximumVersion`。

- [ ] **Step 4: 运行script测试**

```powershell
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
```

Expected：PASS。

- [ ] **Step 5: Commit**

```powershell
git add .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
git commit -m "fix: reject unsupported glibc requirements"
```

---

### Task 8: 防止package路径alias并清理stale current-version archives

**Files:**
- Modify: `.github/scripts/package-release.go:33-65,68-107`
- Modify: `.github/scripts/package-release_test.go:88-251`

**Interfaces:**
- 新增`validateDistinctPaths(paths ...string) error`或等价最小helper，在single-platform mode写入前调用。
- `packageExistingArtifacts`签名不变。

- [ ] **Step 1: 写single-platform alias失败测试**

table覆盖`library=archive`、`library=checksum`、`archive=checksum`、`archive`与`checksum`使用`x.zip`和`./x.zip`。每个path先写不同sentinel，调用`run`，要求error包含`must be distinct`，随后比较所有原文件bytes没有变化。另在支持`os.Link`时建立hardlink alias并断言同样拒绝。

- [ ] **Step 2: 运行确认RED**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -run '^TestRunRejectsAliasedSinglePlatformPathsBeforeWriting$' -count=1
```

Expected：至少archive/checksum alias返回nil且文件被覆盖。

- [ ] **Step 3: 在所有写操作前验证三条路径**

对每一对path先取normalized absolute path；Windows使用`strings.EqualFold`，其他平台直接比较。若两个path均可`os.Stat`，再用`os.SameFile`识别hardlink/symlink。`os.Stat`除`os.ErrNotExist`外的error必须返回。任何alias都拒绝，然后才调用`packageLibrary`。

- [ ] **Step 4: 写aggregate stale archive失败测试**

第一次在同一`outDir`打包Linux和Windows 0.5.3。删除Windows binary和`.version`，第二次打包。要求：

- Linux ZIP存在。
- `model-mapper_0.5.3_windows_amd64.zip`不存在。
- `checksums.txt`不含Windows。
- 预置`model-mapper_0.5.2_windows_amd64.zip`、`model-mapper_0.5.3_unknown_riscv.zip`和`notes.txt`仍存在。

- [ ] **Step 5: 运行确认RED**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -run '^TestPackageExistingArtifactsRemovesOnlyStaleCurrentVersionArchives$' -count=1
```

Expected：stale受支持Windows ZIP仍存在。

- [ ] **Step 6: 删除固定matrix中的stale current-version ZIP**

收集本次found artifacts为`map[artifactSpec]bool`，所有package成功后遍历`artifactSpecs()`。对未found spec计算当前version的exact zip filename并`os.Remove`；`os.ErrNotExist`忽略，其他error返回。随后写本次checksums。不要glob，不删除未知名称或其他version。

- [ ] **Step 7: 运行package script测试**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
```

Expected：PASS。

- [ ] **Step 8: Commit**

```powershell
git add .github/scripts/package-release.go .github/scripts/package-release_test.go
git commit -m "fix: keep release package outputs coherent"
```

---

### Task 9: 使live smoke只接受目标失败并清理secret

**Files:**
- Modify: `.github/scripts/smoke-local.go:42-56,120-145,160-182,260-295,361-445`
- Modify: `.github/scripts/smoke-local_test.go:1-87`

**Interfaces:**
- `caseConfig`删除`allowStartFailure`和`allowConfigFailure`，新增`wantConfigFailureContains string`。
- 新增`writeSmokeConfig(string,[]byte) error`与`requireConfigFailure(smokeEnv,error,string) error`或等价最小helper。
- `stopCPA`签名不变。

- [ ] **Step 1: 写pre-exited process失败测试**

扩展helper-process branch：环境变量值为`exit`时向stdout写`CPA crashed before stop`并`os.Exit(3)`。启动后等待`waitDone`有结果，再调用`stopCPA`，要求error包含log文本和非零退出语义。

- [ ] **Step 2: 运行确认RED**

```powershell
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -run '^TestStopCPAReturnsPreexistingFailure$' -count=1
```

Expected：`stopCPA`返回nil。

- [ ] **Step 3: 保留停止前的process failure**

`stopCPA`第一次nonblocking receive改为接收`waitErr`。非nil时调用`earlyExitError(proc.logFile.Name(), waitErr)`；nil正常返回。由本函数成功发出interrupt或kill后的Wait error继续视为cleanup结果。Signal返回`os.ErrProcessDone`时读取已经结束的wait result并按preexisting failure处理。

- [ ] **Step 4: 写config cleanup与permission失败测试**

`TestRunCaseRemovesConfigAfterStartFailure`创建临时env、将`cpaBin`设为不存在路径、在apiKey使用sentinel；调用`runCase`后要求config不存在。`TestWriteSmokeConfigTightensPermissions`在非Windows预建0644文件，调用helper后要求`mode.Perm()==0600`。

- [ ] **Step 5: 运行确认RED**

```powershell
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -run '^(TestRunCaseRemovesConfigAfterStartFailure|TestWriteSmokeConfigTightensPermissions)$' -count=1
```

Expected：cleanup test发现config仍存在，permission helper尚未定义。

- [ ] **Step 6: 私密写入并在所有路径删除config**

`writeSmokeConfig`对已有path先`os.Chmod(path,0600)`；只忽略`os.ErrNotExist`，然后`os.WriteFile(path,data,0600)`。`runCase`使用named return，写入成功后立即defer删除：

```go
defer func() {
	if err := os.Remove(env.config); err != nil && !errors.Is(err, os.ErrNotExist) {
		caseErr = errors.Join(caseErr, fmt.Errorf("remove smoke config: %w", err))
	}
}()
```

确保`stopCPA`在return expression求值阶段先执行，随后defer删除。

- [ ] **Step 7: 写bad-rules unrelated failure测试**

占用一个loopback port，构造`wantConfigFailureContains:"invalid rule"`的case并调用`runCase`。要求返回`CPA port ... unavailable`，不能nil。另直接测试`requireConfigFailure`：log/error包含`invalid rule`时接受，普通`model not found`和空log拒绝。

- [ ] **Step 8: 替换宽泛failure booleans**

bad-rules case设置`wantConfigFailureContains:"invalid rule"`。start或readiness error只有error文本或当前log包含该marker时才接受。CPA ready时不发送普通model request，而是读取log并要求marker。删除`runJSONCase`中的`allowConfigFailure`分支。端口检查发生在log/config诊断前，不能被marker逻辑吞掉。

- [ ] **Step 9: 写malformed stream data integration测试**

启动`httptest.Server`，对`/v1/chat/completions`返回：

```text
data: {"model":"client"}

data: {broken

data: [DONE]

```

解析server port后调用`runStreamCase`，要求error包含`decode streamed data`和malformed payload。success control只返回合法model和`[DONE]`并要求nil。

- [ ] **Step 10: 运行确认RED后修复parser**

先运行新test，确认当前continue使其错误通过。然后在`runStreamCase`中：empty payload continue，`[DONE]`记录terminal，其余`json.Unmarshal` error立即return wrapped error。不要输出API key。

- [ ] **Step 11: 运行smoke helper tests**

```powershell
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
```

Expected：PASS。

- [ ] **Step 12: Commit**

```powershell
git add .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
git commit -m "fix: make live smoke failures authoritative"
```

---

### Task 10: 对production性能候选执行前后gate

**Files:**
- Modify only if retained: `main.go` and/or `performance_regression_test.go`
- After output outside repository: `C:\Users\user\AppData\Local\Temp\cpa-plugin-0.5.3-bench-after.txt`
- Candidate scratch output outside repository only

**Interfaces:**
- Consumes: Task 1 benchmarks和Tasks 2至5最终core代码。
- Produces: 只有通过gate的最小production优化；也允许“无production性能修改”。

- [ ] **Step 1: 运行修改后同一benchmark command**

```powershell
go test . -run '^$' -bench 'Benchmark(RestoreResponseModel|CallerPatternCacheWarmParallel|EmitRewrittenBatch|SSERewriterSplitLargeEvent|SSERewriterMultiEventBatches|StreamChunkRewriterCompleteSSEBatch|StreamChunkRewriterFragmentedRawJSON)$' -benchmem -benchtime=500ms -count=5 -cpu=1,8,32 | Tee-Object -FilePath 'C:\Users\user\AppData\Local\Temp\cpa-plugin-0.5.3-bench-after.txt'
```

- [ ] **Step 2: 比较correctness修改自身的性能影响**

若`benchstat`存在：

```powershell
benchstat 'C:\Users\user\AppData\Local\Temp\cpa-plugin-0.5.3-bench-before.txt' 'C:\Users\user\AppData\Local\Temp\cpa-plugin-0.5.3-bench-after.txt'
```

否则从每个case的5个结果计算median。记录ns/op、B/op和allocs/op。若正确性修复使不相关case退化超过10%，先定位并消除不必要工作，再继续。

- [ ] **Step 3: 只选择profile与benchmark同时支持的候选**

候选顺序：

1. response changed path：只有长body benchmark显示decode/marshal是主要成本且可用现有JSON scanner最小替换时尝试。
2. split SSE buffer growth：只有split benchmark和profile均显示明确buffer allocation成本时尝试预分配或减少copy。
3. warm caller cache：只有`-cpu=8,32`相对`-cpu=1`吞吐明显塌陷且mutex profile确认锁竞争时才尝试。

`emitRewritten`如果multi-chunk benchmark只显示必要的一次join allocation，不修改API或增加抽象。

- [ ] **Step 4: 对每个候选执行独立试验**

一次只改一个候选。运行对应benchmark至少5次，比较目标case。保留条件：median ns/op改善>=10%，B/op和allocs/op均不恶化，所有相关semantic test通过。否则立即恢复该候选的production diff；不要把失败试验commit。

- [ ] **Step 5: 对保留候选运行语义与race验证**

```powershell
go test . -count=1
go test -race . -count=1
go test . -gcflags=all=-d=checkptr=2 -count=1
```

Expected：全部PASS。

- [ ] **Step 6: 保存最终after结果并commit通过gate的修改**

重新执行Step 1覆盖after文件。若有production优化：

```powershell
git add main.go performance_regression_test.go
git commit -m "perf: reduce measured model mapping overhead"
```

若没有候选通过，不创建空commit，计划记录为“benchmark完成，无production性能修改”。

---

### Task 11: 集成检查、针对性review和修复

**Files:**
- Review: only files changed since `599737af7077d43bb7dcb9149c801427ff076543`
- Modify: only files required by a reproduced review finding

**Interfaces:**
- Produces: clean, reviewed worktree diff。

- [ ] **Step 1: 确认所有worker commits已在当前branch**

```powershell
git status --short --untracked-files=all
git log --oneline --decorate 599737af7077d43bb7dcb9149c801427ff076543..HEAD
git diff --stat 599737af7077d43bb7dcb9149c801427ff076543..HEAD
```

Expected：只包含本spec、plan和列出的源码/测试/README；无build/profile产物。

- [ ] **Step 2: 运行格式化并检查diff**

```powershell
gofmt -w main.go main_test.go abi_cgo.go abi_cgo_test.go .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go .github/scripts/package-release.go .github/scripts/package-release_test.go .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
git diff --check
git diff 599737af7077d43bb7dcb9149c801427ff076543..HEAD -- . ':(exclude).claude/plan/tender-yawning-shamir.md'
```

只修复本次修改产生的format问题。

- [ ] **Step 3: Opus 5 `[1m]`、`xhigh` review changed diff**

按互斥维度审查，不重复全仓扫描：core JSON/stream/config、ABI、compatibility、package、smoke、benchmark validity各一个owner。每个finding必须给出具体input/state、错误结果和`file:line`。

- [ ] **Step 4: 逐项复现review findings**

对每个finding先加或运行最小test。不能复现且无直接协议/类型证据的finding拒绝；confirmed finding按TDD修复并运行所属focused suite。

- [ ] **Step 5: Commit review修复或仅记录无finding**

有修复时按domain创建明确commit；没有confirmed finding时不创建空commit。

---

### Task 12: 完整verification和release artifact预演

**Files:**
- Generated only under ignored `dist/` and OS temp; clean afterward
- No source edits unless a verification failure is reproduced and fixed through TDD

- [ ] **Step 1: 运行所有Go tests**

```powershell
go test ./... -count=1
go test ./... -count=10 -shuffle=on
go test -race ./... -count=1
```

Expected：全部exit 0。

- [ ] **Step 2: 运行三个script suite和vet**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
go vet ./...
```

Expected：全部exit 0。不要运行`go test ./.github/scripts`。

- [ ] **Step 3: 运行strict pointer/cgo检查**

```powershell
go test ./... -gcflags=all=-d=checkptr=2 -count=1
$old = $env:GOEXPERIMENT
try {
  $env:GOEXPERIMENT = 'cgocheck2'
  go test ./... -count=1
} finally {
  $env:GOEXPERIMENT = $old
}
```

Expected：全部exit 0。

- [ ] **Step 4: 构建0.5.3 Windows amd64**

```powershell
make build-windows-amd64 VERSION=0.5.3
```

Expected：`dist/windows_amd64/model-mapper.dll`和`.version`存在，sidecar为`0.5.3`。

- [ ] **Step 5: 用Zig构建0.5.3 Linux amd64并执行compatibility gate**

```powershell
make build-linux-amd64 VERSION=0.5.3 LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"
make package-platform VERSION=0.5.3 GOOS=linux GOARCH=amd64 LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"
```

Expected：build和GLIBC 2.17 gate通过；生成Linux ZIP和checksum。

- [ ] **Step 6: 运行aggregate package并检查内容**

```powershell
make package VERSION=0.5.3
```

检查当前可用platform ZIP根只含对应动态库和`LICENSE`，`checksums.txt`每行只有SHA-256和archive basename。重新计算SHA-256并逐行比较。

- [ ] **Step 7: 条件运行live smoke**

```powershell
if ($env:CPA_SMOKE_API_KEY -and $env:CPA_SMOKE_CPA_BIN) {
  make smoke-local
} else {
  'SKIPPED: CPA_SMOKE_API_KEY or CPA_SMOKE_CPA_BIN is unset'
}
```

Expected：变量存在时所有case打印`ok:`并exit 0；否则保留明确SKIPPED证据。

- [ ] **Step 8: 清理generated artifacts并确认tree**

```powershell
make clean
git status --short --untracked-files=all
git diff --check
```

Expected：worktree无`dist/`、profile、test binary或coverage文件；只有预期tracked changes/commits。

- [ ] **Step 9: 使用verification-before-completion checklist复核spec覆盖**

逐条标记13个confirmed问题、benchmark gate、CPA-only exclusions和每条测试证据。任何未完成项返回对应task，不缩小scope。

---

### Task 13: 提交文档、合并main并发布v0.5.3

**Files:**
- Commit: `docs/superpowers/specs/2026-09-09-cpa-model-mapper-0.5.3-audit-design.md`
- Commit: `docs/superpowers/plans/2026-09-09-cpa-model-mapper-0.5.3-audit-implementation.md`
- External state: local `main`、remote `origin/main`、tag/release `v0.5.3`

- [ ] **Step 1: 确保spec和plan已提交**

```powershell
git add docs/superpowers/specs/2026-09-09-cpa-model-mapper-0.5.3-audit-design.md docs/superpowers/plans/2026-09-09-cpa-model-mapper-0.5.3-audit-implementation.md
git commit -m "docs: specify the 0.5.3 audit release"
```

若已在更早commit中，不重复创建commit。

- [ ] **Step 2: 最终branch状态检查**

```powershell
git status --short --untracked-files=all
git log --oneline 599737af7077d43bb7dcb9149c801427ff076543..HEAD
```

Expected：worktree clean；commit序列包含docs、各domain fixes、benchmark，且没有用户原有plan。

- [ ] **Step 3: 在原checkout合并worktree branch**

在`C:\Users\user\Downloads\cpa-plugin`确认当前branch为`main`且除用户原有`.claude/plan/tender-yawning-shamir.md`外没有变化。执行non-fast-forward merge：

```powershell
git merge --no-ff worktree-audit-0.5.3 -m "release: prepare model-mapper 0.5.3"
```

不得add、delete或读取用户原有plan。

- [ ] **Step 4: 在merged main重跑release前最小gate**

```powershell
go test ./... -count=1
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
```

Expected：全部exit 0。

- [ ] **Step 5: 创建annotated tag并push**

确认remote没有`v0.5.3`后：

```powershell
git tag -a v0.5.3 -m "model-mapper v0.5.3"
git push origin main
git push origin v0.5.3
```

用户已明确授权本次push和release，不再次询问。

- [ ] **Step 6: 监控tag workflow**

用`gh run list --workflow build.yml --branch v0.5.3`找到该tag run，再用`gh run watch <run-id> --exit-status`等待。若失败，读取具体job log，按TDD修复根因，创建新commit并使用新的patch tag；不要force-move已发布tag。若tag尚未形成公开release且GitHub允许安全删除失败tag，仍不得未经核实执行破坏性操作。

- [ ] **Step 7: 验证GitHub Release assets**

`gh release view v0.5.3 --json tagName,isDraft,isPrerelease,assets,url`必须显示非draft、非prerelease，以及7个exact platform ZIP和`checksums.txt`。下载到OS temp，逐项验证：

- filenames为`model-mapper_0.5.3_<goos>_<goarch>.zip`。
- 每个ZIP root含正确动态库和`LICENSE`，无额外目录层。
- `checksums.txt`含7行并与下载bytes SHA-256一致。
- GitHub asset digest若提供，也与本地SHA-256一致。
- Windows/Linux/macOS/FreeBSD binary magic与目标平台一致。

- [ ] **Step 8: 最终报告**

报告commit、tag、workflow URL、release URL、13项修复、性能gate的实际前后结果、完整verification命令结果和live smoke是否因环境变量缺失而跳过。明确列出未修改的CPA-only问题和caller cache架构限制，不宣称已修复。
