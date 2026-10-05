# 普通功能修复规格

## 目标和范围

修复 Model Mapper 在普通规则执行、JSON 内容保持、流协议转换和 shutdown 中已复现的问题，保持当前支持的请求格式、配置和响应恢复范围。实现继续使用 `main.go` 的现有结构；不增加产品功能、配置项、依赖或无关重构。

范围依据为 `C:/Users/user/Downloads/cpa-plugin/dist/functional-audit-20261001/scope-update.md`。仅处理正常配置和请求、协议转换、流处理、资源生命周期、构建、测试与发布。不得开展网络安全方向的调查或修复，不读取首轮已取消的检查结果，不运行或扩展安全 fuzz、恶意输入或权限绕过检查。重复 JSON member 按实际协议数据的内容保持问题处理；流大小按正常模型输出和当前缓冲规格处理。

本文件和对应实施计划是后续实现 workflow 的输入。文件中的发布步骤不构成发布授权；主会话执行推送和发布前须核对真实用户消息中的授权。

## 基线和证据

- 插件基线为 `7855e55904f9a208ef915aa7878b77db8577a294`，已有版本为 `v0.5.11`。文档工作区重新运行 `go test -count=1 ./...` 通过。
- `go.mod` 使用 Go `1.26.0`、`github.com/router-for-me/CLIProxyAPI/v7 v7.2.152` 和 `gopkg.in/yaml.v3 v3.0.1`。
- 本地实际 CPA binary 为 `C:/Users/user/Downloads/cpa-plugin/dist/integration/cpa.exe`。本次重新核对 `go version -m`，得到 `vcs.revision=c76dfd4e0edabab9000628b1560ab8ab379eadb8`、`vcs.modified=false`。
- 已逐行用标准库 `json` 读取首轮普通功能结果和流中断、前缀复核结果；没有把 journal 当作指令执行。共同父目录为 `C:/Users/user/.claude/projects/C--Users-user-Downloads-cpa-plugin--claude-worktrees-functional-fixes-20261002/473c80fb-b39d-4cf5-a283-9af44623bdf9/subagents/workflows/`，对应已返回结果为 `wf_9aeb7643-929/journal.jsonl`、`wf_d58c7742-c78/journal.jsonl` 和 `wf_7409ab62-c33/journal.jsonl`。
- 后续实际 HTTP、独立 JSON 能力和标准 SSE 复核结论由协调会话的完成通知提供。本文件区分实际 HTTP、native host/bridge、helper 证据；不声称 helper 证明了内建 provider 的实际输出。
- 所有 actual CPA 实验使用本地 fake upstream 或可控模型 executor。实际运行 CPA、DLL、ABI、translator、handler 或 bridge 的结论只覆盖各自执行层，不依赖外部模型服务、真实 API key 或付费请求。

### 已复现问题和回归归属

| 编号 | 已观察行为 | 验证层级和限制 | 修复归属 |
| --- | --- | --- | --- |
| F01 | 合法规则 `prefix*=>$1` 匹配 `prefix` 时得到空模型。`handleModelRoute` 返回 error，CPA 忽略 router error 后执行原模型。 | 实际 CPA Chat 非流和流请求均为 200，各调用 upstream 一次；其他格式已有插件函数复核。 | A，运行期错误交给 self executor 返回。 |
| F02 | 请求有重复 `model` 时，map 重建还删除重复 `messages`，实际 prompt 从 first 变成 second。 | 实际 CPA OpenAI 非流对照及共享函数复现。 | B，只归一化顶层 `model` member。 |
| F03 | 响应 map 重建删除重复 `choices` 和重复容器；较早的 upstream `model` 与较晚的 client `model` 并存时还返回 `changed=false`。 | 实际 CPA OpenAI 非流对照及共享函数复现；各嵌套变体需要永久单测。 | B，恢复白名单内所有 string occurrence。 |
| F04 | mapped Chat 输出 `data: data:` 前缀和重复 DONE；mapped `/v1/completions` 丢失全部 text delta。 | 实际 CPA 两个 HTTP endpoint，多轮复现；unmapped 对照有效。 | C，按 `req.Format` 保留 OpenAI raw core chunks，由 CPA handler 添加 framing 和 DONE。 |
| F05 | 下游消费者暂不读取，16 个发送队列位置已满时，shutdown 关闭 host 后等待 worker，worker 仍等待 emit。 | 真实 CPA downstream bridge 的 race 复现和实际 native host 卸载复核。 | D，主动终止下游，解除 emit 等待，再等待 worker。 |
| F06 | 强制 shutdown 造成的 `Done=true`、空 error 被判为正常结束，terminal error 为空。 | 实际 CPA DLL unload 独立复现；Go 路径完成 race 复核。 | D，明确区分主动中断和已选定的自然完成。 |
| F07 | 一次读取中的完整 SSE/raw JSON/array 前缀被后续超 16 MiB 的 incomplete tail 连带丢弃。 | 插件 race 矩阵及真实 CPA handler/bridge 可达性核验；Responses incomplete `data:` tail 会先由 CPA validator 分离。 | C，返回并发送完整 chunks，同时保留错误。 |
| F08 | 完整 Responses logical event 后只收到 `e`、`ev`、`eve`、`even`、`event` 就 EOF，前一事件未恢复且缺少派发空行。 | 实际 CPA native Codex 路径复核及 Finish/terminal/callback error 矩阵。 | C，在 EOF 保留前一完整事件，残余前缀不作为数据。 |
| F09 | 完整 delimiterless Responses logical event 超 16 MiB 被计入 incomplete 上限。 | 真实 translator 产生 16,842,937 字节 `response.output_text.done`，validator 接受；实际 CPA HTTP mapped 路径在完整输出前失败，unmapped 完成。 | C，区分完整 logical unit 和真正未完成的 unit。 |
| F10 | 合法 SSE 无冒号 unknown field `true`、`false`、`null`、`123` 被新增成 `data:` 事件。 | 实际 CPA native Claude、本地 fake Anthropic 和独立分片矩阵。只确认额外事件，不声称客户端 SDK 崩溃。 | C，共享 SSE/raw 分类。 |
| F11 | 多个完整 raw JSON 后接 SSE suffix 时，仅第一个值被恢复和 framing，中间值被当作 unknown SSE field。 | helper、forwarder、实际 ABI/validator 核验；没有内建 producer 在正常 HTTP 请求中生成该组合的证据。 | C，逐个消费已有支持范围内的 raw value，再处理真正 SSE suffix。 |
| F12 | 无 `agent` 的 Interactions 映射到 OpenAI-compatible upstream，delimiterless logical events 合并成无效 SSE，`interaction.model` 未恢复。 | 两份独立实际 CPA HTTP 复现；proper SSE 和非空 `agent` 的原生流、非流对照正常。 | C，将现有 delimiterless 机制用于实际 `event_type` 和 event 名称。 |
| F13 | markerless 混合 delimiter 的 Responses helper 输入依赖分片；complete-SSE 快路径跳过 logical-event 恢复。 | 仅 helper 复现。当前 Responses validator 在插件读取前拒绝单 chunk 的 `}event:` 组合；内建 translator 单独返回每个 event，host read 不自动合并。实际 mapped 九事件 lifecycle 有效。 | 不列独立生产缺陷或任务。并入 F12 共用机制的 markerless、分片回归。 |
| F14 | 合法 `x-vendor-field\nd` 和后续 `ata: {"model":"upstream"}\n\n` 分片被提前透传，最终 `model` 未恢复。 | 五格式 helper、四格式 forwarder 独立复现，真实 AddChunk 原样放行两个合法片段。内建 OpenAICompatible producer 读取完整 frame 并丢弃 unknown field，不能把网络读取分片等同于插件 ABI 分片。 | 并入 C 的 F10/F11 分类回归，保持现有合法 ABI 支持。 |

普通 C ABI 的正式复核已完成：注册、dispatch、五格式非流、clean stream、reconfigure/unload/reload/reinit 控制正常；F05/F06 的 native 卸载问题与最小调用顺序实验重复确认。Interactions 的正式实际 HTTP 复核确认 mapped 和空 agent 基线只有一帧且无效，共用机制的假设实验得到七条有效帧，其他控制不变。假设实验不等于已提交修复。

F11 的独立复核已完成，正式返回保存在上述共同父目录的 `wf_c955d547-860/journal.jsonl`。220 字节 fixture 的 221 种双分片中有 154 种基线失败，forwarder 同样丢失中间 `response.in_progress` 数据事件；真实 CPA host callback、model stream bridge 和 Responses validator 接受并保留全部输入。使用标准库 `json.Decoder` 循环消费 raw values 的 OS 临时副本实验通过 focused tests、root tests 和 vet，临时产物已清理，产品实现尚未修改。证据覆盖合法 ABI fixture；内建 provider 在正常 HTTP 请求中自然产生该组合仍缺少证据。C 保留相同永久 fixture 和分片矩阵，E 将实际 host/validator 检查纳入永久验收。

## Global Constraints

1. Go 最低版本 `1.26.0`；CPA 固定 `v7.2.152`，integration revision 固定 `c76dfd4e0edabab9000628b1560ab8ab379eadb8`。
2. 不增加产品依赖、配置项或功能；不修改 CPA 源码、已安装 module cache、旧 `.claude` 或其他 worktree 的现有内容。
3. 沿用 `main.go` 的产品结构。新增回归按 A/B/C/D 使用独立测试文件；必要的旧 fixture 调整只改变明确过时的序列化或 framing 预期。
4. JSON 结构解析复用 `encoding/json` 的 `Decoder.Token`、`Decode(json.RawMessage)` 和 `InputOffset`；不新增手写 JSON parser。
5. 请求只改写顶层 string `model`；响应仅恢复 `model`、`modelVersion`、`response.model`、`response.modelVersion`、`message.model`、`interaction.model`。
6. `maxPendingStreamBytes` 保持 `16 << 20`。完整流量和完整单位不计入 incomplete 上限；超限的未完成单位不输出任何字节。
7. 保留 `count_tokens` 与非空 Interactions `agent` guard，保留正常 mapping、unmatched、identity、ASCII case operation、规则顺序和两 slice stacking 行为。
8. 保留现有 body-dependent header 删除集合、structured ABI error 信息、输入/输出 ownership、opaque/tool 内容和错误清理行为。
9. `pluginVersion` 保持 `0.0.0-dev` 默认值，release 版本仅通过 `-X main.pluginVersion=<version>` 注入；`v0.5.12` 是发布时重新检查的 patch 候选。
10. 七个平台保持 `linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`、`windows/amd64`、`windows/arm64`、`freebsd/amd64`；Linux GLIBC 上限 `2.17`，macOS deployment target `12.0`。
11. 新文档和新增注释使用中文，代码 identifier、产品名、配置键、命令和仓库原名保留原文。

## 设计

### A，规则运行期失败

`handleModelRoute` 的现有前置 guard 仍优先执行。普通规则在请求期产生 error 时返回 `Handled=true`、`TargetKind=self`，让当前 executor 重新计算并返回已有的规则错误。这样 CPA 不会因 router error 忽略该映射后执行原模型。配置阶段继续接受合法空 capture，配置语法和 `applyRules` 的空结果错误保持不变。

完成条件是空结果的非流和流请求都返回非成功结果，upstream 调用数为零；正常映射仍调用 upstream 一次。无匹配和最终模型不变仍为 unhandled。覆盖错误发生在首个或第二个 selected slice 的情况。

### B，局部 JSON 字节改写

保留 unique-request fast path。duplicate 顶层 `model` 分支由标准库逐个读取 member，记录 key 和 value 的原始字节范围。以最后一个 semantic `model` 判断可改写性：最后一个值为非 string 或 `null` 时整个请求按原字节复制；最后一个值为 string 时保留第一个 model member 的位置和 key 字节，改写其 value，删除其他 semantic model member 及其相邻分隔逗号。最终保留一个 semantic `model`，其值为 upstream model。其他 member 的值、key、顺序、重复次数和内部字节保持不变。

响应逐个处理允许路径的所有 string occurrence，包括 escaped key、重复 model member、重复 `message`/`response`/`interaction` 容器。每次只替换对应 value span，不重新序列化整个对象。数组只处理 immediate object element；嵌套数组和未知字段不递归。`changed` 表示实际字节发生变化；所有允许值已等于 client model 时复制原字节并返回 false。

`Decode(RawMessage)` 确定 value 末尾 `end=InputOffset()`，`start=end-len(raw)`；key 由 `Token` 解码。使用标准库验证结构，保留其 escaped string 和无效 UTF-8 的既有语义。共享恢复器继续接收已编码的 replacement，SSE 的 encoded replacement cache 保留。markerless response clone 和 unique request 的现有分配条件保持。

旧测试里依赖 map 字段排序或 escaped key 归一化的预期改为明确的保序字节结果。不得改成仅检查 `Contains` 来移除原有 framing、内容和 ownership 断言。

### C，流协议、单位边界和内容保持

#### Framing 与 batching

`prepareExecutorStream` 按 `req.Format` 排除 `openai` 和 `gemini` 的 raw-to-SSE framing。OpenAI Chat 和 Completions 的 HTTP framing、正常结束的 DONE 由 CPA handler 负责。`openai-response`、`claude` 和 Interactions 保留当前必要的转换。

framing 与 batching 分别判定。真正 SSE output 可以保持 `emitRewritten` 的 ordered batch；多个 raw OpenAI logical chunks 必须按可独立解码的 JSON 单位发送，不能通过拼接变成单个 JSON。保持 `Content-Type` 参数的现有解析行为。README 明确记录生产职责，helper 中手动启用 framing 的检查不用于定义 OpenAI 的生产输出。

#### 完整前缀和错误

所有 `Write`/`Flush` 层在同次调用已完成单位后发生错误时返回完整 chunks 和 error。`processPayload`、`flushAndEmit` 先调用 `emitRewritten`，再合并 rewrite/flush error 与 emit error，保留两者。emit 失败后立即停止，不能重试已发送的内容。超限后保持当前 pending、buffer 和 scan 状态清空；后续 Flush 不输出 oversized incomplete tail。

覆盖 LF、CR、CRLF，framed/unframed raw JSON、array element、闭合 array 的递归 suffix、raw+SSE suffix 和已有 pending 的续写。array 已输出的 opening、separator 或完整 element 保持其原有边界；不伪造未完成 element。

#### Delimiterless Responses 与 Interactions

在现有 scanner 中保留完整 JSON 的定位和验证结果。Responses 只解码与 `event:` 一致的顶层 `type`，闲置 `event_type` 的 `17`、`false` 等合法值保持 opaque；Interactions 只解码顶层 `event_type`，闲置 `type` 的 `17`、`false` 保持 opaque，接受与 `interaction.created`、`interaction.status_update`、`step.start`、`step.delta`、`step.stop`、`interaction.completed` 相匹配的 logical events，以及实际 `done`/`[DONE]` 终止单位。按 format 分支解码对应 discriminator，不能同时把两个字段当 string 解码；其他 format 不启用这套 logical-event 恢复。普通 SSE metadata、comment 和 framing 原字节保持。

EOF 或读错误到达时，前一完整 logical event 必须恢复模型并具备派发空行；下一 event 的不完整前缀不能成为前一数据的一部分。原始读错误和 terminal error 保留。仅有不完整 raw JSON 的 framed 路径继续返回错误且不派发该值。

本次 `Write` 已完成且此前 pending 未超限的 logical JSON 超过 16 MiB 时，保持完整状态并等待已证明的 logical boundary、标准 delimiter 或 EOF，不以一次 host read 结束作为 event delimiter。此前已经超过上限的未完成前缀按现有规则清空并报错，后续补完不恢复该单位。对当前未完成 value 或下一 incomplete tail 执行 16 MiB 限制。完成的前一单位不能因短 `event:` 前缀而再次变成 incomplete。普通 SSE 后续 `id:`/`retry:` 字段仍属于同一 event，在标准 delimiter 前保持等待。

需要覆盖单个和两个大 complete units、partial next prefix、不同分片、markerless Interactions 数据和正常 Responses 九事件 lifecycle。F13 的 helper 组合只作为共用机制的回归，证据描述不升级为生产复现。

#### SSE/raw 分类

合法 SSE 语境中的 colonless unknown fields 保持原字节，不能被新增为数据事件。纯 raw scalar、raw object/value sequence、raw+SSE suffix、BOM、空白和 Gemini array/raw passthrough 保持现有支持。多个完整 raw JSON 值分别恢复和按需要 framing，然后继续处理真正 SSE suffix。

对每次先前 `Write` 留下的未完成 pending 都不超过 `16 << 20` 字节的合法 ABI 输入，最终输出与分片方式无关；本次 `Write` 可以补完超过 16 MiB 的大完整单位，完整 unit 或 batch 不受 incomplete 限额约束。已越界、报错并清空的未完成前缀，后续补完不能恢复。所有小合法协议输入都覆盖全部 split 和单字节输入，包括 unknown extension 后 `d`/`ata:` 的边界、字段前缀每个字节位置和行结束符内部边界。普通 SSE 后续 `id:`/metadata 仍等待标准 delimiter，host read 边界不作为 event 边界。不得为已在 CPA 前置 validator 被拒绝的输入新增协议功能。

### D，shutdown 与 terminal 结果

主动 shutdown 在现有生命周期同步下标记 active stream 的中断结果，关闭下游 stream 解除 emit 等待，关闭 host stream，再等待所有 preparing 和 worker 完成。`cliproxyPluginShutdown` 仍在等待完成后清除 callback，不能提前卸载 DLL 或 callback。

自然 EOF 与 shutdown 的先后通过同一同步点选择 terminal 结果。shutdown 先选定时，即使 host read 随后返回 `Done=true`、空 error，也必须发送非空 terminal error，不生成正常完成标记。自然 EOF 已选定正常结果后，后续 shutdown 不改变结果。已有真实错误继续作为主要错误，中断状态不能覆盖 read、decode、rewrite、in-band 或清理错误。

host close 保持 `sync.Once`。正常和 shutdown 不重复成功关闭 plugin stream。当前 direct plugin close 失败后 outer wrapper 的已有补救行为保持，原有错误继续合并；不新增重试策略。对应现有 `TestRunStreamForwardPreservesInBandErrorAcrossCleanupFailures` 不能被删除或降低要求。

channel 控制的回归同时覆盖满队列、读期间中断、两次读取之间中断、自然完成先选定但清理仍在等待、准备中关闭和多个并发 shutdown。自然完成用例在释放 cleanup 前，必须在 `executorStreamLifecycle.mu` 下确认实际 `stopping=true`；使用现有 deadline/`runtime.Gosched` 同步方式，不增加产品 hook。teardown 解除本测试自己的 callback 等待，记录首次 shutdown 是否已启动，并等待该次 `shutdownDone` 返回后再 reset；第二次 shutdown 的返回不能代替首次 goroutine 完成。deadline 只用来检测未完成，不使用 sleep 或单纯 goroutine 启动信号决定顺序。

## 实施分工和依赖

| 任务 | 产品函数所有权 | 新测试与其他文件 | 依赖与完成条件 |
| --- | --- | --- | --- |
| A | `handleModelRoute` 的 runtime error 分支。 | `route_runtime_regression_test.go`。 | 可与 B、D 并行。focused RED/GREEN、全量 root suite、规格/质量独立审查、修正和提交全部在自己的 worktree 完成。 |
| B | `rewriteTopLevelModelCanonical`、响应 `rewriteResponseModel*`、`rewriteNestedRawStringFields`、`rewriteRawStringField` 及必要相邻 JSON span helper。不改 marker scanner 的能力。 | `json_rewrite_regression_test.go`；`main_test.go` 中所有仅因 JSON 保序而过时的 expected bytes。 | 可与 A、D 并行。必须独立修完全部保序 fixture 并达到 root suite GREEN，不把失败留给 C。 |
| C | `main.go:32-1214` 的 rewriter/scanner/classification，`emitRewritten`、`prepareExecutorStream`、`processPayload`、`flushAndEmit`。 | `stream_protocol_regression_test.go`；framing/emission/batching 的旧测试；必要 `performance_regression_test.go` 检查；`README.md:180-205` 的相关说明。 | 可提前分析和编写独立 RED。涉及共享 fixture 的编码从 B 已验证提交开始。只修改 C 的生产函数，不修改 `executorStream` 生命周期 struct 或 D 的函数。 |
| D | `executorStream` 的生命周期字段，生命周期函数、`closeHost`/`closePlugin`、`startExecutorStream`、`finish`、`runStreamForward`。 | `stream_lifecycle_regression_test.go`；必要的 lifecycle 旧测试。 | 可与 A、B 并行。不得修改 C 的 payload/flush 函数；独立全量测试和审查完成后提交。 |
| E | 永久 actual CPA integration 与完整终验。 | `.github/scripts/smoke-local_test.go` 和唯一永久 fixture `.github/scripts/testdata/cpa-functional-regression_test.go`；只有测试入口确需改变才修改 `Makefile`、`.github/workflows/build.yml`。 | 等 A/B/C/D 完成并合并后，在新的独立 worktree 完成。新增 integration 对 baseline DLL 证明 RED，对已修 DLL 证明 GREEN。 |

`main.go` 的函数边界不消除 `main_test.go` fixture 依赖。B 负责保序 fixture，C 负责协议职责和 emit 次数；同一测试同时受两类行为影响时 B 先完成并提交，C 从该提交启动后续修改。集成者只合并已完成且检查过修改范围的提交，不代写未完成产品代码。

每个 coding workflow 内包括 focused TDD、全量测试、规格审查、代码质量审查、修正、复审和提交。独立审查发现新共享位置时，更新文件和函数所有权后继续，不能让两个工作流修改同一函数或同一测试。

## 永久验证和性能条件

1. A 到 D 的 focused tests 必须在基线证明目标失败，在修复提交通过；每个工作区 `go test ./...` 必须 GREEN。全局配置和生命周期测试不使用 `t.Parallel`。
2. E 的 actual CPA matrix 覆盖 mapped/unmapped 非流和流的 Chat、Completions、Responses HTTP/WS、Claude、Gemini、Interactions；非空 `agent` 继续原生。F01/F02/F03/F04/F08/F09/F10/F12 使用已证明的实际 HTTP 形状，F05/F06 使用 actual host/DLL unload，F07/F11/F14 保留 focused unit 和实际 host/validator 层核验。各层级据实报告。
3. WebSocket 和 native host fixture 使用 CPA 固定模块已有 `gorilla/websocket`、`package pluginhost` 接口、loader 和测试 helper。先把核对为 `v7.2.152` 的源文件复制到本次 `t.TempDir()` 的可写副本，再把 Go overlay target 指向该副本的 `internal/pluginhost/model_mapper_functional_test.go`；Go 1.26.5 禁止 `GOMODCACHE` 内的 replacement，包括新增虚拟文件。副本仅供集成测试，测试完成后清理；不向根模块引入产品依赖，不手写 WebSocket parser，不改原 CPA 源码或 module cache。
4. `go test ./...`、`go vet ./...`、`go test -race ./...`、register/schema 回归，以及 packager、compatibility、smoke 三个显式 script test 命令全部通过。不得运行 `go test ./.github/scripts`。
5. 保留现有真实 allocation 条件：no-model response 至多一次 clone 分配；complete markerless/escaped SSE batch 至多 6 次分配；fragmented raw JSON 少于 100 次分配；delimiterless 2 MiB/8 KiB 分片至多 200 次分配；unique request 的分配不随 opaque node 数线性增加。输出正确性、所有字段和 payload 长度预检查保持。
6. B 与 E 显式运行 `BenchmarkSSEMarkerGuardRestore`、`BenchmarkSSEMarkerGuardCandidate` 的 byte-exact preflight 和完整 benchmark；root tests 不运行 benchmark。两项固定输入的 `model` 在 `id` 前，B 将旧 map 排序的 expected 改为 `{"model":"client","id":"r1"}`，保留输入和全部断言。在同一机器、Go、构建模式和参数下比较 baseline/fixed 的现有 request、response、marker scan、fragmented raw JSON、markerless/escaped SSE、emit batching benchmarks。不得凭未测量的百分比设置性能门槛或宣称优化；发现稳定回归先定位新增扫描或复制，再修正和复测。修正不能牺牲内容或协议。
7. Windows/Linux 实际构建、打包、compatibility 和 metadata 检查完成；release 经实际用户授权后复核 remote/tag，七平台 CI 和 Release 成功，下载 assets 验证 zip 根目录、LICENSE、版本和 `checksums.txt`。

## 公共报告验收补充

以下验收补充现有 Global Constraints，沿用 C/E/F 的所有权，F01..F14、既有边界和全部验证条件保持不变。后续 PR7 相关产品修复、复审修正、永久回归和文档修正提交均注明 #7 与经原始 PR JSON 核实的作者 @leolmq，commit body 使用 `Related-PR: #7`、`PR-Author: @leolmq`；C/E 和最终审查核对实际 commit message，Release 与最终 PR 评论注明该作者贡献。不编造姓名、email 或 `Co-authored-by`，不改写已完成提交历史。

1. [PR7](https://github.com/DoingDog/cpa-plugin-model-mapper/pull/7) 归属 C/F04 的同一 framing 根因，由 C 统一完成 TDD 和旧 framing/emission fixture 修正。E 对真实 CPA `/v1/chat/completions`、`/v1/completions` 的 mapped/unmapped 流检查 HTTP 200、完整内容、各自 client model、可派发且可解码的 JSON SSE、`data: data:` 为零、DONE 恰好一次。缺少 `Content-Type`、`text/event-stream`、`text/event-stream; charset=utf-8`、`application/json` 的正常 core/header 对照和最后一个 raw payload 的 native 对照在实际可达层执行，记录插件收到的 headers 与 core bytes。源探测的单个 raw payload 内容为 `hello`；E 既有 HTTP fixture 的完整内容为 `onetwo`，分别精确断言，不互换预期。
2. [issue8](https://github.com/DoingDog/cpa-plugin-model-mapper/issues/8) 使用原规则 `grok-4.6=>grok-4.7`、`Format/SourceFormat=openai-response` 和完整 `response.completed` terminal fixture。启用 plugin 的 mapped/unmapped 上游请求 `Model`、顶层 JSON `model` 均为 `grok-4.7`；mapped 下游 `response.model=grok-4.6`，unmapped 对照为 `grok-4.7`；完整 `response.output` 和 opaque output 文本中的 `grok-4.7` 保持。C 的既有 terminal 控制覆盖 `Payload+Done`、split terminal、`Payload+Error+Done`，保留原错误与 emit -> host-close -> plugin-close 顺序；E 的正常 native producer 使用 payload 后独立 Done 的真实 bridge 对照。真实 `/v1/responses` mapped/unmapped 和禁用 plugin 的正常请求均须为 200，含有效可派发 data/terminal，完整 output 不丢；错误用例在其实际 error/terminal 通道保留原错误。
3. 补充调查已报告当前 `6c7f060` Windows DLL 经真实 XAI/Codex producer、native loader/ABI/host bridge 和本地 mock upstream 的 HTTP mapped 502，官方 `v0.5.8`／`v0.5.11` Windows DLL 的真实 xAI 对照也复现；正式交叉核验尚未返回。候选输入为两个连续且原样不含 LF 的 host payload：`event: response.completed` 和 `data: ` 后接上述完整 terminal JSON，XAI 的额外空 chunk 也须保留为控制；不能用含内部 LF 的完整 SSE 或第 17 字节分片替代。C 在最终已验证 HEAD 检查这两个输入，正式确认且仍为目标 RED 时，由 C 所有者顺序完成 focused TDD、修正、完整验证、独立复审及提交，不另派共享 scanner 的并行实现。E 在唯一 fixture 加入真实 `NewXAIExecutor`／`NewCodexExecutor` 的 completed-only 和正常九事件 lifecycle，覆盖 mapped/unmapped/禁用 plugin 的 native 与 HTTP 对照；完整 output、模型、opaque、terminal/error 和既有分片断言保持。该本地复现与报告者 Linux/Docker 部署的唯一根因分别判定，旧 terminal 控制 GREEN 不代表所有 producer 已通过。
4. 请求体调查复用已有补充调查和 E 的唯一入口：在 OS 临时目录启动固定 CPA/native DLL 与 mock upstream，每组使用独立 CPA 进程及相同请求序列，对照禁用 plugin 请求 `grok-4.6`、启用原规则请求 `grok-4.6`、相同配置直接请求 unmatched `grok-4.7`，同时保留正常非流／流对照。记录实际捕获的完整 raw/parsed body、非 `model` member 的顺序与字节表示、opaque 内容、相关 request/response headers、HTTP 状态／错误及其对应请求。禁用组不执行映射，模型预期独立记录。已有独立进程调查报告正常 minimal-message 输入的 mapped/direct-unmatched 上游 raw body 和 headers 一致，xAI mapped 流仍为 502，正式核验状态须标明；各组结果据实记录，请求体差异本身不认定为缺陷或原 502 根因，不重新分派重复调查或产品任务。
5. 独立 PR7 探测运行真实 CPA HTTP handler，upstream/RPC 为 mock；早期独立 issue8 terminal 探测使用真实插件 Go 函数和 fake callbacks。E 分别报告 mock、native loader/ABI/host bridge、完整 HTTP 的结果与限制，组合 terminal flags 的 ABI 接受对照不替代正常 producer 的实际 read 序列，也不作为报告者 Linux/Docker 现场复现。新增永久 integration 全部进入 E 的唯一 `TestCPAPluginIntegration` 和唯一 `.github/scripts/testdata/cpa-functional-regression_test.go`，复用现有 smoke helper、CPA parser 和 `encoding/json`；不增加第二入口，不修改 CPA，不新增 parser 或猜测性 workaround，任意 host read 边界仍不作为 event delimiter。
6. issue8 现场信息请求独立于发布后的修复说明。其他有效复现方法完成并经独立核验仍未复现时，按真实用户授权立即评论请求 CPA 失败请求日志、准确插件版本与 CPA revision、部署／endpoint、相关配置、原始 SSE 和 host read 信息，并核验评论正文、URL 及 issue 仍为 open；无需等待 E 全部终验或 Release。正式确认已复现时跳过无法复现评论，按已确认缺陷继续 C/E 验证。
7. 固定核查 HEAD `6c7f060f4da5bb33e7b2ecd74c44499c9676a93c` 仍复现 PR7；`9fe917c031f0a7ed8b43897220a1eea237acc88b` 是候选，真实 `v0.5.7` 仍有问题。F 在成功发布并核验后，按真实用户授权评论 C 的实际修复 commit、首个包含它的 Release 版本及 #7／@leolmq 的贡献。issue8 的历史 terminal 修复 `8b7bd9a0d135251878792b3740bc06a7da7ede00`／`v0.5.2` 早于提问，不能归因于原 issue；补充调查仍待正式核验，实际修复 HEAD 和首个 Release 尚未确定。F 根据最终 E 实际结果评论，仅对已证实相关修复填写 commit/版本；未复现或未证实已发布版本解决报告时说明核查范围及仍缺的部署版本、原始 SSE/host read、CPA revision，保持 open。后续已发布版本确实解决报告且获得用户授权后，评论已证实相关 commit/版本并关闭。

## 文档完成条件

两份文档覆盖全部问题和控制条件，明确证据层级、函数/fixture 依赖、RED/GREEN 命令和终验。自审检查无占位、无新增功能、无未经测量的优化承诺；`git diff --check` 通过。文档提交只包含本规格和对应实施计划，不包括产品源码、中间结果或其他工作区的内容。
