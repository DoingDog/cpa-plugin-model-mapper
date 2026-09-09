# CPA Model Mapper 0.5.3 功能与性能审计设计

## 状态

- 日期：2026-09-09
- 基线：`599737af7077d43bb7dcb9149c801427ff076543`，tag `v0.5.2`
- 目标版本：`v0.5.3`
- 实施范围：仅 `cpa-plugin-model-mapper` 仓库

## 目标

修复已经由源码、依赖边界或官方协议资料确认，并且可通过聚焦测试复现的问题。验证插件在 `openai`、`openai-response`、`claude`、`gemini` 和 `interactions` 格式下不会破坏请求、非流式响应或流式响应。评估高并发、长输入和长输出路径；production 性能改动只有在同一 workload 的修改前后 benchmark 显示明确改善时才保留。

## 约束

1. 不修改 CPA 源码，不继续检查与插件调用边界无关的 CPA 实现。
2. 使用 TDD。每个功能修复先加入能在 `v0.5.2` 基线上失败的聚焦测试，再修改根因。
3. 只修改与本次确认问题直接相关的代码、测试和文档。
4. 不改变规则 DSL 的既有语义，特别是不增加 wildcard backtracking。
5. 不递归替换任意 JSON 内容。响应恢复仍只允许 `model`、`modelVersion`、`response.model`、`response.modelVersion`、`message.model` 和 `interaction.model`。
6. 已经 framed 的 SSE 和 `application/json` raw stream 不得被错误转换。
7. benchmark、CPU profile 或 allocation profile 只能形成性能假设；候选 production 优化必须通过前后对照 gate。
8. 不读取或修改本次任务以外的旧 spec、plan。保留原 checkout 中用户已有的 `.claude/plan/tender-yawning-shamir.md`。

## 已确认问题与设计

### 1. raw JSON stream 丢失 value separator

`streamChunkRewriter.tryRawJSONChunks` 对完整单值先 trim，对多值使用 `json.RawMessage`，随后只输出 JSON value。这样会删除前导、尾随和 value 间的 JSON whitespace。`1 2` 分两块发出后会在接收端拼成 `12`；NDJSON 的尾随 LF 也会丢失。

修改 raw JSON 路径，使未转成 SSE 的输出保留所有原始 whitespace：

- 单值 fast path 只替换 JSON value 本身，保留 value 前后的字节。
- 多值 path 根据 decoder 的 value 边界保留每个 value 前的 separator，并把完整输入末尾的 whitespace 附到最后一个输出。
- 若末尾 value 不完整，separator 和不完整 value 一起保留到 `pending`，不能提前删除。
- 转成 SSE 时，value separator 不进入 `data:` payload；每个 JSON value仍成为一个独立 event。
- 任意 read partition 的最终有序输出必须一致。

### 2. Gemini streamed JSON array 未恢复 `modelVersion`

Gemini Developer API 的 `streamGenerateContent` 默认 `alt=json`。Google 的 Discovery descriptor 和 HTTP streaming 规则定义它为 `GenerateContentResponse` 的 JSON array；每个 immediate array element 可以包含顶层 `modelVersion`。当前响应恢复只接受 object root，因此 array 中的 upstream `modelVersion` 会泄漏给 client。

扩展响应恢复入口：

- object root 保持现有 whitelist 行为。
- array root 逐个检查 immediate element；只对 object element应用相同 whitelist。
- primitive、`null`、nested opaque array和任意非 whitelist 字段保持不变。
- 保留 array 的 brackets、commas 和 value 间 whitespace；只允许 changed object element按现有 object rewrite规则变化。
- 空 array和完全无变化的 array返回 byte-identical clone，`changed=false`。
- 非流式 executor 和 raw JSON stream共用此根因修复。

Gemini direct REST 的 model selector位于 URL path。插件继续通过 host execution request 的 `Model` 传递 upstream model；没有顶层 body `model` 时，请求 body保持 byte-identical。

### 3. authenticated credential recovery只检查每个位置的第一个值

`callerAPIKey` 使用 `Header.Get` 和 `url.Values.Get`。CPA 默认 config access provider也只检查第一个值，但 CPA SDK允许 access provider把同一 credential位置的后续 presented value作为 `Principal`。此时 `caller_scope` 正确，插件却无法恢复对应 plaintext，wildcard scope错误地 fail closed。

修改 `callerAPIKey`：

- 按当前 credential source优先级遍历 `Authorization`、`X-Goog-Api-Key`、`X-Api-Key`、`key`、`auth_token`。
- 在每个 source内按出现顺序遍历所有值。
- 每个 `Authorization` value独立处理可选 Bearer scheme。
- trim后只有 digest等于 authenticated `caller_scope` 的 candidate才能返回。
- 不接受仅由 client header提供、但 digest不匹配的值。

### 4. raw JSON转 SSE 时缺少 format-specific framing

当前 `frameSSEData`对所有格式只生成 `data:`。官方协议要求不同：

- OpenAI Chat Completions：JSON chunk使用 `data:`，正常完成以 `data: [DONE]`结束。
- OpenAI Responses：每个 event使用 `event: <type>`和`data: <JSON>`，没有 Chat的 `[DONE]`。
- Anthropic Messages：每个 event使用与 JSON顶层 `type`一致的 `event:`行，没有 `[DONE]`。
- Gemini `alt=sse`：官方资料确认 `data:` records和 HTTP body completion，没有定义 `[DONE]`。
- `interactions`：没有足够的公开协议资料，不推测 event name或 terminal marker；保留 data-only现状。

设计：

- `streamChunkRewriter`持有 client-facing output format。
- 只有 host response `Content-Type`解析为 `text/event-stream`，且输入 chunk被确认是 raw JSON value时才生成 SSE framing。
- `openai-response`和`claude`从有效 JSON object的顶层 string `type`生成 `event:`；缺失或非 string时只生成 `data:`，不猜值。
- `openai`只在正常 host `Done`、本次 stream确实转换过 raw JSON、且尚未转发 `[DONE]`时追加一次 `data: [DONE]\n\n`。read error、decode error、payload error和 host error不得伪造成功终止。
- 已 framed SSE逐字节保留其 prefix、field、line ending和event delimiter，除目标 JSON string value外不重建 framing。
- `application/json` raw stream，包括 WebSocket-backed Responses消息，不生成 SSE。

### 5. ABI接受 `NULL` request和非零长度

`cliproxyPluginCall`当前把 `request == NULL && requestLen > 0`当作空 request。pointer/length pair不一致是 ABI输入错误。

在调用 `handleMethod`前统一验证和复制 request：

- `NULL, 0`仍表示合法空 request。
- `NULL, n>0`立即返回 ABI failure `1`。
- 失败时 response保持 `{NULL, 0}`，不分配 payload。
- 继续保留 `C.int`长度上限。

### 6. lifecycle YAML静默忽略额外 document或 trailing data

`yaml.Unmarshal`只解析第一个 YAML document。一个有效 plugin config后追加第二个 document时，后者会被静默忽略，导致已接受配置与用户提供内容不一致。

改用 `yaml.Decoder`：

- 第一次 Decode读取唯一配置 document。
- 第二次 Decode必须返回 `io.EOF`；任何第二 document或 trailing parse error都拒绝。
- reject时不发布部分或新配置，维持既有 atomic publication。
- direct JSON config路径不变；不启用未经要求的 `KnownFields` strict mode。

### 7. glibc compatibility gate忽略非 numeric GLIBC requirement

`check-release-compatibility.go`只匹配 `GLIBC_<number>`。`GLIBC_ABI_DT_RELR`可与低 numeric version同时出现并错误通过 `2.17` gate。

扫描所有 `GLIBC_` requirement token：numeric token继续参加最大版本比较；任意非 numeric token，包括 `GLIBC_ABI_DT_RELR`和`GLIBC_PRIVATE`，明确返回 unsupported requirement错误。不能用一个低 numeric requirement掩盖未识别 requirement。

### 8. single-platform package路径 alias会覆盖 archive或input

单平台模式没有验证 `library`、`archive`和`checksum`是否指向同一路径。checksum与archive相同会把ZIP覆盖成文本；checksum或archive与library相同可破坏输入 binary。

在任何 `MkdirAll`、create、truncate或write之前比较三条路径：

- normalized absolute path相同即拒绝。
- 两个已存在路径使用 `os.SameFile`识别 hardlink或symlink alias。
- Windows normalized path比较不区分大小写。
- 任意一对 alias都返回清楚错误，且三个原有文件保持 byte-identical。

不引入新的打包格式或临时发布布局。

### 9. aggregate package保留当前版本的 stale platform archive

同一 `outDir`重复打包，若第二次少了某个平台，旧ZIP仍存在但不在新的 `checksums.txt`中。

在所有输入版本验证完成并成功生成当前 artifacts后：

- 枚举固定 `artifactSpecs()`。
- 删除当前 version、当前 plugin name且本次未发现的受支持平台ZIP。
- 不删除其他 version、未知平台或无关文件。
- `checksums.txt`只列当前实际生成的ZIP。

### 10. `stopCPA`隐藏已提前发生的非零退出

若 process在调用 `stopCPA`前已退出，当前代码读取 `waitDone`后返回 nil。smoke case可能在 CPA crash后显示成功。

当 process在停止信号前已结束时，保留 `cmd.Wait()` error并结合当前 log通过 `earlyExitError`返回。由 `stopCPA`主动发出的 interrupt/kill仍按 cleanup处理，避免把预期停止信号误报成 case失败。

### 11. live smoke在磁盘持续保留 upstream API key

每个 case把 `CPA_SMOKE_API_KEY`写入固定 `.test-cpa/config.yaml`，结束后不删除；对已有文件调用 `os.WriteFile(..., 0600)`也不会收紧原权限。

- 写入前将已有文件权限收紧到 `0600`；新文件以 `0600`创建。
- 每个 case无论 start、readiness、request还是stop成功或失败，都删除 config。
- 删除失败必须与原 case error合并返回，不能静默保留 secret。
- log和error不得包含 credential。

### 12. bad-rules smoke接受无关启动或请求失败

`allowStartFailure`和`allowConfigFailure`会把端口占用、binary错误、readiness timeout和普通 model-not-found都当成预期 invalid rule结果。

用明确的 config diagnostic expectation替换两个宽泛 boolean：

- bad-rules case要求 error或CPA log包含插件的 `invalid rule`诊断。
- start/readiness失败只有包含该诊断才算预期结果。
- CPA正常 ready后，检查 log中的同一诊断；普通 HTTP failure不能代替 config validation。
- port unavailable发生在CPA启动前，必须直接失败。

### 13. streaming smoke忽略 malformed nonterminal `data:` JSON

OpenAI Chat smoke对任意非`[DONE]` data payload的 `json.Unmarshal` error执行continue。一个 malformed event后跟合法 model event和`[DONE]`仍会通过。

对非空、非`[DONE]`的`data:` payload，JSON decode失败立即返回包含event上下文的协议错误。空 data field仍可忽略。现有 original/forbidden model和terminal检查保持。

## 性能评估设计

### Workload

1. 完整 response model restoration：4 KiB、64 KiB、1 MiB、8 MiB；无marker、marker但值已相同、顶层changed、nested changed、Gemini array changed。
2. caller-pattern cache warm并发：单一hot key和1,024-key working set，使用`RunParallel`，外部以`-cpu=1,8,32`运行。
3. stream emit batching：1、2、32、128 chunks，总大小64 KiB和1 MiB。
4. SSE：完整batch、split 64 KiB event、many-event batch，以及包含model marker而不能走no-marker fast path的batch。
5. raw JSON：单值、fragmented value、NDJSON、多scalar和Gemini array。

### Benchmark有效性修复

- `BenchmarkStreamChunkRewriterCompleteSSEBatch`在fixture和semantic preflight后调用`b.ResetTimer()`。
- `BenchmarkStreamChunkRewriterFragmentedRawJSON`在计时外拼接完整输出，断言valid JSON、restored model和opaque ID；计时内把输出长度或bytes写入package sink，不能只数slice。
- 新benchmark都在计时外构造fixture并验证一次语义，设置`SetBytes`和`ReportAllocs`。

### Production优化 gate

对任何性能候选：

1. 先只加入benchmark并在未改production代码的基线上运行至少5次，保存原始结果到仓库外临时文件。
2. 一次只实现一个最小候选；运行相同命令至少5次。
3. 使用`benchstat`，或在不可用时比较每个case的median。
4. 只有目标 workload的`ns/op`改善至少10%，且没有统计噪声级反转，同时`B/op`和`allocs/op`不恶化，才保留production改动。
5. 必须运行所有语义测试、race和checkptr。任何输出、framing、header或并发语义回归都撤销候选。
6. 未通过gate的production候选完整回退；有效benchmark修正和覆盖可保留。

优先评估已由profile定位的SSE buffer growth/per-event allocation、完整长response decode/marshal和warm cache锁竞争。不预先承诺更换cache或JSON parser。

## 测试策略

### Core unit与integration

- raw JSON：leading/trailing LF、space、blank line、`1 2`、complete prefix + incomplete tail、每个single split point、SSE与non-SSE对照。
- Gemini：nonstream object、stream JSON array、array跨任意read boundary、opaque nested `modelVersion`和`role: "model"`保持。
- caller identity：同一header/query的later matching value、earlier spoofed value、Bearer/raw Authorization、多source priority、digest mismatch。
- format-specific SSE：OpenAI Chat JSON + one `[DONE]`、Responses/Claude `event:`、Gemini无`[DONE]`、already-framed SSE byte preservation、application/json无framing、error path无successful terminal。
- ABI：`NULL,0` control、`NULL,1` rejection、oversized length、zeroed response。
- YAML：single document control、second document、malformed trailing content、atomic config retention。
- header：body changed才删除nonstream `Content-Length`；stream始终删除`Content-Length`和`Transfer-Encoding`；unchanged nonstream保持。

### Script tests

- compatibility checker：numeric baseline/newer/missing和nonnumeric GLIBC requirement。
- packager：三种pair alias、relative alias、existing-file alias、无写入；stale current-version cleanup和unrelated-file preservation。
- smoke helper：pre-exited nonzero process、config cleanup on every return path、Unix mode、bad-rules unrelated failure、malformed SSE JSON。

### Full verification

至少运行：

```powershell
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
go test ./... -gcflags=all=-d=checkptr=2
$env:GOEXPERIMENT='cgocheck2'; go test ./... -count=1
```

另行构建Windows amd64和Linux amd64 Zig `c-shared`，执行0.5.3单平台及aggregate package，检查ZIP root、版本sidecar和sha256sum manifest。只有`CPA_SMOKE_API_KEY`与`CPA_SMOKE_CPA_BIN`存在时运行live smoke；缺失时明确记录未运行。

## 不纳入本次修改

- caller wildcard route decision的有限generational cache可能在跨RPC状态被淘汰。没有request correlation可在插件内同时保证无限正确性和有界内存；完整修复需要CPA显式传递route decision，本次不以扩大cache伪装成修复。
- CPA stream header race、stream bridge close/emit race、nested host error status丢失、nested router re-entry和metadata cycle均属于CPA实现，不修改。
- wildcard backtracking与当前公开DSL契约一致。
- Anthropic beta fallback/advisor/usage model字段语义不同，不递归替换。
- `interactions`没有公开provider contract，不推测额外framing或model paths。
- incremental-delivery live smoke属于测试能力扩展，当前没有production failure证据，不在0.5.3增加。
- 没有可达payload证据的panic recovery、host API lifetime和C function pointer qualifier hardening不纳入。

## 发布

全部计划项和verification通过后：

1. 在worktree branch提交spec、plan、修复、测试和必要文档。
2. 合并到原checkout的本地`main`。
3. 创建annotated tag `v0.5.3`。
4. push `main`和`v0.5.3`到`origin`。
5. 监控GitHub Actions直到tag workflow和Release完成。
6. 验证7个平台ZIP、`checksums.txt`、asset digest、ZIP root动态库和`LICENSE`。
