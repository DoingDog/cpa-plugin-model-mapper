# cpa-plugin-model-mapper 0.5.2 审计修复设计

日期：2026-09-08

状态：已批准实施。用户已要求无人值守完成审计、修复、验证、合并和 `v0.5.2` 发布，不设置中途确认点。

## 目标

修复本次审计确认的全部插件 correctness、stream lifecycle、header、配置和发布边界问题。性能改动必须先有可复现 benchmark，再以相同 workload 比较；没有稳定改善的候选不进入最终 diff。

完成标准：

1. 五种注册 format 的请求路由和响应模型恢复保持正确：`openai`、`openai-response`、`claude`、`gemini`、`interactions`。
2. 任意 host read 边界不改变 SSE 字节语义；raw JSON 与 outer response header 使用一致 framing；终止 payload 不丢失。
3. request/response body 改变后不转发 stale `Content-Length`；stream setup 不转发不再成立的 body-length transport headers。
4. JSON RPC 中任意大小写的 HTTP header 都按 HTTP 大小写不敏感语义读取、合并和删除。
5. async stream worker 在 native shutdown 返回前全部退出，shutdown 返回后不再调用 host API。
6. caller wildcard route decision 能跨等价 reconfigure 复用，cache 长期运行时有明确内存上限。
7. aggregate package 不能把 dev 或旧 binary 标为新 release；`VERSION=v0.5.2` 与 `VERSION=0.5.2` 生成相同 metadata 和 archive 名。
8. 所有新增 regression tests、现有 tests、`go vet`、race tests、release helper tests和可用的 smoke tests通过。
9. 最终提交合并到本地 `main`，创建并 push `v0.5.2`，GitHub Build workflow成功并生成完整 release assets。

## 一手合同结论

### CPA plugin contract

- CPA `v7.2.101` 首次发布 authenticated `caller_scope`。引入 commit 为 [`f6c32ec3`](https://github.com/router-for-me/CLIProxyAPI/commit/f6c32ec3ffe7cdb9310916fa56f9c2e8e2e0a1b3)，首个正式 tag 为 [`v7.2.101`](https://github.com/router-for-me/CLIProxyAPI/releases/tag/v7.2.101)。本插件依赖的 CPA `v7.2.152` 已包含该合同。
- CPA `v7.2.48` 不提供 `caller_scope`。插件在该版本跳过 positive 和 inverse scoped entries属于安全的 fail-closed 行为，不能在插件内信任任意 client header来补偿。
- current CPA 的 host HTTP bridge按任意 read boundary 返回 raw bytes，不保证 chunk 是 SSE line、event或完整 JSON。
- `HostModelStreamReadResponse` 可同时包含 `Payload`、`Error` 和 `Done=true`。插件必须先处理 payload，再处理 terminal state。
- plugin `executor.execute_stream` 必须先返回，再异步 emit。CPA outer bridge在 call 返回前最多接受16个 chunk。
- host API 从 `cliproxy_plugin_init` 保持有效到 `cliproxyPluginShutdown` 返回。shutdown 返回后 async worker不能继续使用 callback。
- `ExecutorResponse` 没有 `StatusCode` 字段。成功 status不能由插件表达，不在本次修复中虚构字段。
- CPA 的 after-auth `ClearHeaders` 聚合缺陷属于 CPA 本体。本插件只信任 `caller_scope`，不推测 interceptor 是否真的删除了 credential header。

### Wire protocols

- OpenAI Chat Completions、OpenAI Responses、Anthropic Messages和Interactions request使用顶层 `model`。Gemini native REST model位于 URI path，body通常没有 `model`。
- response model白名单需要覆盖：顶层 `model`、顶层 `modelVersion`、`response.model`、`response.modelVersion`、`message.model`、`interaction.model`。
- WHATWG SSE将 CRLF、LF和bare CR都视为 line ending；空行 dispatch event；多个 `data:` field以LF连接。HTTP read boundary没有逻辑意义。
- outer response声明 `text/event-stream` 时，bare JSON必须转换为合法 `data:` event。完整 SSE必须按原 event boundary转发。
- body bytes改变后，旧 `Content-Length` 无效。stream重新 framing 后，host的 `Content-Length` 和 `Transfer-Encoding` 都不能作为 downstream headers复用。

完整资料和URL保存在临时调研文件：`C:\Users\user\AppData\Local\Temp\cpa-plugin-v0.5.2-protocol-research.md`。

## 已确认问题与设计

### 1. Stream setup、headers与framing使用不同事实来源

当前 `startExecutorStream` 在 host setup 前固定返回 `Content-Type: text/event-stream`，worker随后却按 host `Content-Type` 决定是否 framing raw JSON。CPA不要求五种format都返回SSE，也允许host返回空header或非SSE媒体类型，因此outer header与实际payload framing可能矛盾。host的其他response headers也在异步worker中到达，无法写入plugin setup response。

设计：

1. 把 route、request rewrite和 `host.model.execute_stream` setup移到同步 prepare 阶段。
2. validate host status、decode response并取得 `StreamID` 后，再启动只负责 read/rewrite/emit/close 的 goroutine。
3. 以 host response headers为基础返回 plugin stream headers，canonicalize并合并大小写重复键。
4. 删除 `Content-Length` 与 `Transfer-Encoding`。host明确返回 `Content-Type` 时保留该值；缺失时使用兼容fallback `text/event-stream`。
5. worker根据最终返回的 `Content-Type` 决定raw JSON framing。最终类型为 `text/event-stream` 时生成合法SSE event；非SSE类型时保持raw JSON value分块。完整SSE继续由现有parser识别并保留。
6. 保留custom headers，例如request ID和rate-limit headers。CPA是否最终向client透传仍由CPA配置和interceptor决定。

### 2. SSE parser根据 transport chunk boundary补换行

`knownSSELogicalBoundary` 会在 pending `event:` 或包含完整 JSON 的 `data:` 后，遇到下一个看似 field的chunk时插入 `\n`。CPA read boundary是任意字节边界，因此合法字段内容可以被改成新 field。

设计：删除该启发式及其专用 helper。stream rewriter只根据原始 CRLF、LF、CR和空行判断line/event boundary。未完成 event继续跨chunk缓冲，绝不根据 `Write` 调用边界生成字节。

### 3. 完整 raw JSON前缀被不完整tail阻塞

`splitJSONValues` 在后续 value返回 `io.ErrUnexpectedEOF` 时丢弃已经解码成功的 values。Flush随后把完整前缀和tail原样输出，完整前缀没有恢复 model，SSE模式也没有 framing。

设计：

1. splitter返回完整 values、最后消费 offset和 incomplete状态。
2. `Write` 立即 rewrite/emit完整前缀，只把未消费tail复制进 pending。
3. tail补全后只处理tail，不重复发送前缀。
4. terminal Flush在outer SSE模式下仍以 `data:` framing发送pending bytes。
5. 确定性 invalid trailing data继续按现有 fallback处理，不把已解析前缀误判成完整 sequence。

non-SSE 多value不保留原separator是已有公开语义，本次不改变。

### 4. Terminal stream payload会丢失，cleanup可覆盖原始错误

当前 read loop先检查 `Error`、`Done`，后处理 `Payload`。`Done=true` 或 `Error` 同时带payload时，最后一块被丢弃。in-band error分支的flush、host close或plugin close失败会替换原始 upstream error。

设计：

1. 每次read先处理非空 `Payload` 并emit当前完整输出。
2. 再处理 `Error`；先Flush pending，再无条件尝试host close和plugin close。
3. 原始 in-band error始终是primary error；cleanup error通过 `errors.Join` 追加。
4. plugin close成功后worker返回nil，避免重复close；plugin close失败时返回包含原始 error的joined error，由outer wrapper重试close。
5. `Done=true` 且没有Error时，处理payload后进入正常Flush和双侧close。

### 5. SSE framing不支持bare CR

`frameSSEData` 只按LF分行。JSON合法whitespace中的bare CR会被SSE parser解释成line ending，而后续行没有 `data:` prefix，导致data被截断。

设计：复用现有 `sseLineEnding`，统一识别CRLF、LF和CR，为每一逻辑行写一个 `data:` field，再写空行dispatch。保持空payload与terminal line ending的data语义。

### 6. Interactions stream模型字段未恢复

Google Interactions stream的 `interaction.created`、`interaction.completed` 等事件把模型放在 `interaction.model`。当前白名单没有该路径。

设计：在现有非递归白名单中增加且仅增加 `interaction.model`。继续禁止递归修改opaque content、tool text或任意nested `model`。

### 7. JSON RPC header key没有canonicalize

`encoding/json` 保留map key casing，`http.Header.Get` 和 `Del` 按canonical key索引。lowercase `authorization`、`content-length`、`content-type` 因此无法读取或删除。

设计：增加一个in-place header canonicalization helper，在三个plugin request decode边界和两个host response decode边界调用。helper使用 `http.CanonicalHeaderKey`，把大小写不同的重复key合并到一个canonical entry。已canonical的普通请求不复制整个map。

### 8. Stream setup decode error可泄漏已创建的host stream

JSON unmarshal可能先写入 `StreamID`，再因其他字段类型错误返回。当前代码直接返回，host stream未关闭。

设计：decode失败且partial `StreamID` 非空时，best-effort调用 `host.model.stream_close`，随后返回原decode error。close error不覆盖decode root cause。

### 9. Native shutdown不管理async stream worker

`execute_stream` 在plugin call返回后继续运行goroutine。当前shutdown只把host callback设为nil并立即返回，worker随后可能继续调用host API，且可能与dynamic library unload重叠。

设计：

1. 增加最小stream lifecycle registry，使用mutex、WaitGroup和active worker set。
2. stream在启动goroutine前完成admission；shutdown开始后拒绝新stream。
3. prepared host stream提供idempotent close函数。shutdown标记停止后snapshot active streams并请求close，使阻塞read退出。
4. shutdown等待所有worker完成，清空registry和caller cache，最后才清空host callback。
5. init开始新的lifecycle，允许正常重新加载。
6. 不重复跟踪同步 `cliproxyPluginCall`，该生命周期已由CPA host guard负责。

这是一个有意的最小模型。如果未来CPA提供request-scoped cancellation token，应替换close-to-cancel方式。

### 10. Lifecycle `config_yaml` 类型错误被吞掉

声明 `config_yaml` 但值为number、null或object时，第一次unmarshal error被忽略，payload退化为direct config并成功发布空规则。

设计：先把top-level JSON解成 `map[string]json.RawMessage`。没有 `config_yaml` 时保持direct config路径；存在时必须是JSON string，包括允许空string。错误必须在publish前返回，旧配置保持不变。

### 11. 未注册format会使用global rules

`selectRules` 的default分支同时承担 `gemini`、`interactions` 和未知format。未知format可被global rule处理，越过registration capability boundary。

设计：显式列出 `gemini`、`interactions` 使用global rules；其他值返回空selection并保持unhandled。

### 12. Caller wildcard cache在reconfigure后失效且无上限

cache value由 `caller_scope + callerPatternText` 决定，pattern语义是确定的，不依赖当前mapping config。每次reconfigure清空cache会破坏route到executor之间的复用；长期不reconfigure时，unique authenticated scope又会无限增加map。

设计：

1. reconfigure只原子发布compiled config，不清理仍然确定有效的match结果。
2. cache改为两代固定容量map，每代最多16,384个 `scope + pattern` 结果，总上限32,768。
3. current generation满后变为previous generation并创建新current；lookup同时查current和previous，不在hit上做LRU写锁。
4. 被淘汰的entry在缺少raw credential时只能fail closed，绝不能产生错误路由。
5. init、shutdown和test isolation可显式reset cache。
6. 保留现有RWMutex并发读取，不引入dependency或per-hit list mutation。

已知上限应写成 `ponytail:` comment。如果CPA未来提供request-scoped route decision metadata，应删除该跨RPC cache。

### 13. Aggregate package未绑定binary build version

README先执行不带VERSION的build，再执行aggregate package。packager只根据参数命名zip，不知道binary内置的 `pluginVersion`，可把 `0.0.0-dev` 或旧binary发布为0.5.2。

设计：

1. `build-platform` 在binary旁写 `<binary>.version` sidecar，内容与实际注入的 `pluginVersion` 完全相同。无VERSION的dev build写 `0.0.0-dev`。
2. aggregate packager在创建任何output前，对所有发现的binary读取sidecar并要求其trim后严格等于normalized release version。
3. 缺少、为空或不匹配时失败，不生成部分release zip或checksums。
4. sidecar只用于本地aggregate preflight，不进入release zip。
5. README build示例为每个平台显式传入同一个VERSION。

跨平台 `debug/buildinfo` 不记录 `-X main.pluginVersion`，不能替代sidecar；跨平台 `dlopen` 不可行。

### 14. Make接受前导v但继续使用原值

packager validator将 `v0.5.2` normalized为 `0.5.2`，Make仍把原始值用于ldflags和archive名。

设计：Make定义一次 `RELEASE_VERSION`，只删除一个前导 `v`。validation继续检查原始VERSION，因此 `vv0.5.2` 仍失败。ldflags、sidecar和archive名使用normalized值。递归make不传预先展开的错误 `VERSION_LDFLAGS`。

### 15. Live smoke未覆盖wildcard Principal恢复

现有所有scoped smoke都使用exact scope，不读取raw credential，无法发现 `Authorization` 或 `X-Api-Key` forwarding回归。

设计：把一个OpenAI scoped hit和一个Claude scoped hit改为 `local-smoke-*#...`。保留相邻fallback key case，确认不匹配wildcard时继续unscoped rule。README把caller scope最低CPA版本从错误的 `v7.2.145` 修正为 `v7.2.101`。

## 性能实验

### A. 大request的顶层model rewrite

当前实现把整个body解成 `map[string]json.RawMessage`，替换一个field后重新marshal全部内容，改变key order并按payload大小分配中间值。

先添加有语义preflight的 `BenchmarkRewriteTopLevelModel`，sizes为1 KiB、64 KiB、1 MiB、8 MiB，报告ns/op、B/op和allocs/op。最小候选使用stdlib `json.Decoder` 加无复制skip target定位top-level `model` span，只分配最终output，并保持opaque字段原字节。必须覆盖escaped string、whitespace、duplicate top-level model、nested model、missing/invalid model和invalid JSON。

保留门槛：

- 1 MiB与8 MiB workload的median ns/op至少降低20%，B/op至少降低30%。
- 1 KiB workload不能稳定退化超过10%。
- 所有语义tests通过。

任何一项不满足，恢复implementation candidate。benchmark可以保留作为后续基线。

### B. Response model marker扫描

当前scanner每个byte移动14-byte tail；caller已确认marker后，部分路径又执行第二次完整scan。

先添加no-marker、literal marker、escaped-key marker三类benchmark，sizes为4 KiB、64 KiB、1 MiB，包含exact output preflight。候选先用 `bytes.Contains` 查literal keys，仅在存在backslash且无literal marker时运行escaped-key scanner；已确认marker的caller直接调用checked rewrite，避免第二遍scan。

保留门槛：64 KiB与1 MiB的no-marker或literal-marker median ns/op至少降低15%，B/op不增加，escaped-key correctness不变。未满足则恢复candidate。

### C. Caller cache retained memory

添加固定1,000、10,000、100,000 unique scope workload，报告throughput和custom `retained-entries` metric。旧实现应随N增长；新实现N=100,000时必须不超过32,768。warm lookup不应因容量策略改为exclusive lock。

## TDD与验证矩阵

每项先增加最小失败test并单独运行确认RED，再写production code确认GREEN。相邻同根因case可放在table test，不为每个format复制相同parser测试。

必需regression coverage：

- arbitrary split位于 `event:`、`data:` field value和JSON key/value任意位置，拼接结果partition-invariant。
- empty header使用 `text/event-stream` fallback并framing raw JSON；`application/json`保持raw JSON；`text/event-stream` framing raw JSON。每种payload都与outer `Content-Type` 一致。
- host custom stream header保留，`Content-Length`、`Transfer-Encoding`删除。
- complete JSON prefix加incomplete tail，包括正常补全、Done和read error。
- `Payload + Done`、`Payload + Error + Done`，先emit payload后close。
- in-band error加flush emit、host close、plugin close failure，primary error不丢。
- CRLF、LF、CR multiline raw JSON framing。
- `interaction.model`恢复，opaque nested/tool text不改。
- lowercase/mixed-case Authorization、Content-Length、Content-Type和duplicate casing merge。
- malformed setup response含partial stream ID时close。
- shutdown阻塞到worker退出，主动close解除read，返回后callback count不增加，新stream被拒绝，re-init可工作。
- invalid `config_yaml` 的number、null、object不发布；direct config和empty string lifecycle仍可用。
- unknown format unhandled；五种registered format保持原selection。
- equivalent reconfigure后executor复用caller match；cache generation bound和race安全。
- aggregate version sidecar missing/mismatch/valid，以及任何失败不留下partial release files。
- `VERSION=v0.5.2` dry-run和package产物使用 `0.5.2`。
- live smoke的Authorization和X-Api-Key wildcard paths。
- ordinary Headers、Query、nonstream response Headers继续转发；`Format != SourceFormat` 使用正确EntryProtocol/ExitProtocol。
- emit failure停止read并关闭两侧。

验证命令：

```powershell
go test ./...
go vet ./...
go test -race ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

如果 `CPA_SMOKE_API_KEY` 和 `CPA_SMOKE_CPA_BIN` 可用，再运行：

```powershell
make smoke-local VERSION=0.5.2
```

构建和打包验证至少覆盖Windows amd64、本机aggregate preflight和archive内容/metadata；可用Zig时再覆盖Linux amd64。

## 明确不做

- 不修改CPA源码，也不补偿CPA的after-auth `ClearHeaders` bug。
- 不让v7.2.48上的scoped rules信任client-controlled headers；只修正文档最低版本。
- 不改变README已声明的non-backtracking wildcard语义。
- 不改变non-SSE multi-value按value分块且不保留separator的既有语义。
- 不递归修改opaque JSON、content或tool text中的model字符串。
- 不增加第三方dependency。
- 不增加无法由benchmark证明的优化。
- 不读取或修改本次任务之外的旧spec/plan。

## 预计改动文件

Runtime与测试：

- `main.go`
- `main_test.go`
- `performance_regression_test.go`
- `abi_cgo.go`
- 必要时 `abi_cgo_test.go`

Release与文档：

- `Makefile`
- `.github/scripts/package-release.go`
- `.github/scripts/package-release_test.go`
- `.github/scripts/check-release-compatibility_test.go`
- `.github/scripts/smoke-local.go`
- `.github/scripts/smoke-local_test.go`
- `README.md`
- `CLAUDE.md`，仅更新实际response model白名单

本文件和后续本次implementation plan属于任务产物。

## 集成与发布

1. Runtime与release改动分别由Sonnet 1M xhigh执行，文件独占且可并行。
2. 每个实现代理按RED、GREEN、benchmark、局部verification顺序工作。
3. Opus 1M xhigh按相同文件分区review，不让多个代理重复审查同一文件。
4. 主会话运行完整verification并核对diff只包含本spec范围。
5. 提交任务产物，切回本地 `main`，以fast-forward merge合并 `fix/v0.5.2-audit`。
6. 在verified main创建annotated tag `v0.5.2`，push `main` 和tag。
7. 监视tag触发的GitHub Build workflow，确认test、所有platform build和release job成功；核对release包含完整zip矩阵和 `checksums.txt`。
