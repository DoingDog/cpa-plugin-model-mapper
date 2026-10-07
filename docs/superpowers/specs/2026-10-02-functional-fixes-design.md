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
| F15 | 真实 XAI/Codex 的独立无 LF event/data fields 或多个 data-only Payload 被合并，丢失单位边界；field-pair 的 mapped `/v1/responses` 返回原文 502，模型未恢复。单 terminal data-only 正常。 | 固定 `6c7f060` 的真实 core/host/native/HTTP 和发布库 native/framer 证据；新 root/CPA 草稿已编译，真实 producer/无 mapper HTTP 控制通过，新 native DLL mapped/disabled 矩阵尚未运行。 | G，在固定 C 最终审查提交之后的完整整合起点有限核查 G2 的 output-unit 语义，规格修正独立审查通过后完成必要 TDD、修正、审查和提交。E 负责唯一永久 actual matrix；语义冲突未解决时停止产品编码，不放行发布。 |

普通 C ABI 的正式复核已完成：注册、dispatch、五格式非流、clean stream、reconfigure/unload/reload/reinit 控制正常；F05/F06 的 native 卸载问题与最小调用顺序实验重复确认。Interactions 的正式实际 HTTP 复核确认 mapped 和空 agent 基线只有一帧且无效，共用机制的假设实验得到七条有效帧，其他控制不变。假设实验不等于已提交修复。

F11 的独立复核已完成，正式返回保存在上述共同父目录的 `wf_c955d547-860/journal.jsonl`。220 字节 fixture 的 221 种双分片中有 154 种基线失败，forwarder 同样丢失中间 `response.in_progress` 数据事件；真实 CPA host callback、model stream bridge 和 Responses validator 接受并保留全部输入。使用标准库 `json.Decoder` 循环消费 raw values 的 OS 临时副本实验通过 focused tests、root tests 和 vet，临时产物已清理，产品实现尚未修改。证据覆盖合法 ABI fixture；内建 provider 在正常 HTTP 请求中自然产生该组合仍缺少证据。C 保留相同永久 fixture 和分片矩阵，E 将实际 host/validator 检查纳入永久验收。

## Global Constraints

1. Go 最低版本 `1.26.0`；CPA 固定 `v7.2.152`，integration revision 固定 `c76dfd4e0edabab9000628b1560ab8ab379eadb8`。
2. 不增加产品依赖、配置项或功能；不修改 CPA 源码、已安装 module cache、旧 `.claude` 或其他 worktree 的现有内容。
3. 沿用 `main.go` 的产品结构。新增回归按 A/B/C/D/G 使用独立测试文件；必要的旧 fixture 调整只改变明确过时的序列化或 framing 预期。
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

对 generic 连续 wire 入口中每次先前 `Write` 留下的未完成 pending 都不超过 `16 << 20` 字节的合法 ABI 输入，最终输出与合法字节分片方式无关；本次 `Write` 可以补完超过 16 MiB 的大完整单位，完整 unit 或 batch 不受 incomplete 限额约束。已越界、报错并清空的未完成前缀，后续补完不能恢复。generic 小合法协议输入覆盖全部 split 和单字节输入，包括 unknown extension 后 `d`/`ata:` 的边界、字段前缀每个字节位置和行结束符内部边界。generic SSE 后续 `id:`/metadata 仍等待标准 delimiter，host read 边界不作为 event 边界。Responses 保持正常 output-unit 数组及原消费者的顺序和时机；F14 等合法 callback 分片按原 validator 和对应 HTTP/WS 消费者核验，HTTP 网络分段先经真实 builtin Scanner。不得为已在 CPA 前置 validator 被拒绝的输入新增协议功能。

### D，shutdown 与 terminal 结果

主动 shutdown 在现有生命周期同步下标记 active stream 的中断结果，关闭下游 stream 解除 emit 等待，关闭 host stream，再等待所有 preparing 和 worker 完成。`cliproxyPluginShutdown` 仍在等待完成后清除 callback，不能提前卸载 DLL 或 callback。

自然 EOF 与 shutdown 的先后通过同一同步点选择 terminal 结果。shutdown 先选定时，即使 host read 随后返回 `Done=true`、空 error，也必须发送非空 terminal error，不生成正常完成标记。自然 EOF 已选定正常结果后，后续 shutdown 不改变结果。已有真实错误继续作为主要错误，中断状态不能覆盖 read、decode、rewrite、in-band 或清理错误。

host close 保持 `sync.Once`。正常和 shutdown 不重复成功关闭 plugin stream。当前 direct plugin close 失败后 outer wrapper 的已有补救行为保持，原有错误继续合并；不新增重试策略。对应现有 `TestRunStreamForwardPreservesInBandErrorAcrossCleanupFailures` 不能被删除或降低要求。

channel 控制的回归同时覆盖满队列、读期间中断、两次读取之间中断、自然完成先选定但清理仍在等待、准备中关闭和多个并发 shutdown。自然完成用例在释放 cleanup 前，必须在 `executorStreamLifecycle.mu` 下确认实际 `stopping=true`；使用现有 deadline/`runtime.Gosched` 同步方式，不增加产品 hook。teardown 解除本测试自己的 callback 等待，记录首次 shutdown 是否已启动，并等待该次 `shutdownDone` 返回后再 reset；第二次 shutdown 的返回不能代替首次 goroutine 完成。deadline 只用来检测未完成，不使用 sleep 或单纯 goroutine 启动信号决定顺序。

### G，native SSE field boundary，F15

本节采用固定 CPA v7.2.152 的 Responses output-unit 消费语义。输入层级依据协调目录的 `task-G-consumer-semantic-verification.json`（verified=true），实际 producer、validator、host callback、原 framer、HTTP/WS 对照和用户消息来源已经核验。原补充草稿及独立复审保留为历史依据；更正后的草稿和本次实际检查在独立文档任务报告中记录。规格修正须独立审查通过后启动 G；产品尚未修复。

#### 绑定目标

新增 F15 native SSE field boundary。正常 XAI/Codex builtin 向 plugin host 交付的无 LF/CR logical fields，必须在映射 `grok-4.6=>grok-4.7` 后保留单位边界、有序交付全部 Responses 数据，并把白名单内 `response.model` 恢复为 `grok-4.6`。完整 delta、completed output 和含 `grok-4.7` 的 opaque 文本保持。真实 `/v1/responses` 正常流与非流返回 HTTP 200。

F15 同时拥有两种已确认生产输入：

1. field-pair：单 completed 的两个独立 Payload 为 `event: response.completed`、`data: {完整 JSON}`；九事件为十八个独立 field Payload。
2. data-only：单 completed 为一个独立 `data: {完整 JSON}`，这是既有正常控制；九事件为九个独立 data Payload，多事件需要共享边界修复。两个 created/completed data Payload 是最小函数回归，不能用单 terminal GREEN 代替它或九事件验收。

保留 F01..F14 的编号和原任务范围。F13 保持 helper 回归层级；F14 保持实际 validator 接受的合法 ABI 分片层级。F15 记录真实 builtin producer 的新输入，不把 HTTP 网络分段等同于 host Payload 分段。

#### 依据与实际核验范围

固定插件设计源为 `6c7f060f4da5bb33e7b2ecd74c44499c9676a93c`。CPA 为 `v7.2.152`，module Sum 为 `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`；指定 integration binary revision 为 `c76dfd4e0edabab9000628b1560ab8ab379eadb8`。Go 最低版本 1.26.0，本轮实际 Go 为 go1.26.5 windows/amd64。

已完整读取以下本地证据：

- `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/.superpowers/sdd/2026-10-02-functional-fixes-implementation/issue8-native-final-evidence/evidence/producer-trace.json`。真实 XAI/Codex core、BaseAPIHandler/validator、Host/native DLL 均无 error，host 交付独立无 LF event/data fields，插件输出 `event: response.completeddata: {JSON}`，dataFrames=0，response.model 仍为 grok-4.7。
- 同一协调目录内的 `issue8-independent-evidence-20261005/issue-original.json`、`issue8-request-body-independent.json`。原 issue 要求完整 terminal Payload 和模型恢复；普通最小 mapped/direct 的上游 request body 与正常 response 完全相同，请求改写差分没有解释本地 502。
- 同一协调目录内的 `issue8-published-structured-output.json`。其 real-responses-framer-data-only、actual-builtin-producer-capture-and-done-replay 记录原生单 terminal 和九事件 data-only 正常；mapper 单 terminal 正常，九事件合并后 dataFrames=0。这里引用已保存的发布核验，未在本轮重跑发布 DLL/.so。
- 同一归档中的 `driver/producer_trace_test.go`、`driver/native_fixture_test.go` 已通读，`driver/probe_test.go` 和 `evidence/verification.json` 仅读取具体相关范围。

本轮新 Go overlay 在固定插件源码上编译成功。field-pair 的单 terminal、两个/九个事件及 data-only 的两个/九个事件均为实际内容 RED；单 terminal data-only、普通 wire、两对 canonical 字符串组成的单个普通 event field 与迟到 LF/CRLF delimiter 控制为 GREEN。

完整 CPA 测试草稿也已在 OS 临时可写副本编译。本轮实际运行真实 XAI/Codex 的 core/host producer 控制，以及未加载 mapper 的真实 Responses HTTP handler/framer 控制。两种输入、单 completed/九事件、LF/CRLF、正常 7-byte/单字节网络分段及 charset 均符合精确 field、事件与内容断言。这些控制证明新 fixture 的真实生产形状及原生 HTTP 正常行为；本轮没有运行新草稿的 native DLL 映射与禁用矩阵。

旧 terminal-order `8b7bd9a`/`v0.5.2` 和内部已有 LF 的完整-event fixture 保持其原结论，不能用于宣称 F15 已覆盖。原 issue 的 Linux/Docker 现场缺少准确版本、配置和逐次 host bytes，仍不能认定本地 fixture 是其唯一根因。

#### 函数所有权与任务依赖

C 最终审查提交已固定为 `51b006438c1a0edd4263020297747f99a174c47c`，G 产品 reviewBASE 始终使用该完整 SHA。G 的执行起点由协调者主动交付含 A/B/C/D 及本规格修正的完整整合 HEAD，须确认固定 C 为其 ancestor，禁止只从 C 的旧树启动而遗漏其他已整合修复。最终产品 review 覆盖固定 C..G 全部最终 HEAD 的累计范围，并明确其中的文档范围。禁止读取或轮询其他在修改 worktree。

G 只拥有自己分配 worktree 内共享 stream rewriter/scanner 的 F15 必要修改：`streamChunkRewriter.Write/Flush/Finish`、`sseRewriter.Write/drain`、共用 delimiterless scanner 的相关状态、reset 与分类，以及 `stream_native_fields_regression_test.go` 和必要 protocol/performance fixture。当前设计源的绝对路径为 `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/main.go`、`main_test.go`、`performance_regression_test.go`。

公开给任务间使用的签名保持：

```go
Write(p []byte) ([][]byte, error)
Flush() ([][]byte, error)
Finish() ([][]byte, error)
emitRewritten(chunks [][]byte, batch bool, emit func([]byte) error) error
(s *executorStream) processPayload(rewriter *streamChunkRewriter, payload []byte) error
(s *executorStream) flushAndEmit(rewriter *streamChunkRewriter, cleanCompletion bool) error
```

G 消费 C 的 framing/batching、`batchSSEOutput` 与 chunks+error 处理，不重写 C 的 `emitRewritten`、`prepareExecutorStream`、`processPayload`、`flushAndEmit`。A 的 `handleModelRoute`、B 的 JSON helpers/保序 fixture、D 的 terminal/lifecycle、`closeHost`、`closePlugin`、`startExecutorStream`、`finish`、`runStreamForward` 均不修改。G 可在函数回归中调用这些已存在接口。

E 依赖 C/G 最终审查提交，保留唯一 `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/.github/scripts/smoke-local_test.go` 中的 `TestCPAPluginIntegration` smoke 入口及唯一 `.github/scripts/testdata/cpa-functional-regression_test.go` CPA fixture。G 的真实 producer/native/HTTP 草稿交 E 合并到该 fixture；不创建第二份 CPA fixture 或第二个 smoke 入口。

两份 tracked 文档由本次 F15 文档整合 workflow 编辑：`C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/docs/superpowers/specs/2026-10-02-functional-fixes-design.md` 与 `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/docs/superpowers/plans/2026-10-02-functional-fixes-implementation.md`。补充草稿与回归已独立复审并由文档 workflow 整合；后续 G 在本规格修正独立审查通过后执行 G1/G2 的有限核查和必要完整 TDD；若发现同一实际入口的可靠相反要求，按 G2 停止。

#### 共用根因与判定边界

固定源码中 `sseRewriter.Write` 直接 append Payload bytes；`findDelimiterlessResponsesEventEnd` 在 event header 中等待真实 LF/CR。field-pair 因缺少内部行结束而成为单个 event field；data-only 多事件因缺少逻辑分隔而成为单个 data field。两者必须由同一共用机制处理，不能增加 XAI/Codex 专用处理或 HTTP handler workaround。

JSON 结构与 discriminator 验证复用 `encoding/json`。Responses 只读取 string `type`；闲置 `event_type=17/false` 保持 opaque。event field 与 type 必须相符；data-only 使用实际 type 确认完整单位，不要求不存在的 event header。复用 C 最终的增量 scanner、completeEnd、SSE helpers、`rewriteEvent` 和 B 的响应恢复器。不新增手写 JSON/SSE parser、产品依赖、配置或 ABI 字段。

##### 输入单位与派发时点

`openai-response` executor 接收固定 CPA 按 response format 交付的 output chunks。`HostModelStreamReadResponse` 的 `Payload []byte`、`Error string`、`Done bool` 和现有 `Write([]byte)` 接口保持；output-unit 边界、顺序及派发时点以同输入的正常无 mapper Responses 消费链为依据。复用现有 format 和调用入口，不新增 logical/wire 标志、mode、config 或 ABI 字段。

G1 的四项 canonical callback 为 created event、created data、completed event、completed data。原 validator 接受这些单位，原 `responsesSSEFramer` 在四次输入后累计 dataFrames 为 `0、1、1、2`，对应第 2 次派发 created、第 4 次派发 completed。迟到 LF/CRLF delimiter 不改变两个已成立事件，也不增加 data 事件。data-only 在下一独立 data chunk 到达时派发前一个完整值，Flush 派发末值；单 terminal、两个和九个事件均保持完整内容、顺序及这一时机，禁止把全部完整单位积存到 EOF。

以下 canonical 字符串拼接组成单个普通 event field。在 generic 连续 SSE wire 入口，whole、全部双分片、单字节分片及四次续写后才送 LF/CRLF delimiter 均保持原文，标准 delimiter 前不派发：

```plaintext
event: response.createddata: {"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}event: response.completeddata: {"type":"response.completed","response":{"id":"resp-issue8","object":"response","status":"completed","model":"grok-4.7","output":[{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}]}}\n\n
```

Responses 的 whole event-only metadata 后接真正 completed data 是正常控制，保留普通 event field 原文并恢复后续数据的模型。相同 event-only 字节通过 HTTP 网络四次 Write/Flush 后 delimiter，须先经过真实 XAI/Codex Scanner；实际 host 只交付一个 463-byte event field，孤立流在无 mapper HTTP/WS 消费链失败，不能作为 mapper 破坏正常请求的证据。相同四个 callback 则按前述两事件语义验收，不重新解释为这一网络控制。

单个普通 data field 内拼接两个 JSON/data 字符串的 generic 原文控制保持。该 field 的 JSON 无效，Responses 原 validator 的 `invalid SSE data JSON` 错误保持，不列为正常 Responses 成功输入。F14 等合法 callback 分片仍按原 validator 加对应 HTTP/WS 消费者核验；validator 接受与每个消费者支持分开记录。LF/CR/CRLF、BOM、unknown/id/retry/comment metadata、完整内容及任意合法字节分片继续在各自入口验收。

完整 JSON 的定位、验证、候选记录与不可逆派发分别明确时点。generic wire 候选保存原始 bytes，等待标准 delimiter；Responses output units 按已核验的消费语义生成必要 LF/blank delimiter并调用现有恢复器。共享增量 scanner/reset、既有内部含 LF 的 logical events 和错误处理保持。G2 有限核对实际入口、validator、HTTP/WS 消费者及上述时机后实施；本规格修正须独立审查通过。若后续有可靠证据证明同一实际入口必须同时满足两种相反派发要求，停止产品编码并报告具体冲突。禁止 provider/substring 推断、timeout、lookahead 个数或忽略错误选择解释。

##### 普通 metadata output units

Responses output units 同时包括独立、无 LF/CR 的 `id: event-1`、`retry: 100` 和 `: heartbeat`。正常 upstream SSE 的每条 metadata 行先经 XAI/Codex builtin Scanner、translator 和 validator，再作为独立 host Payload 交付；网络 Write/Flush 的分段保持由 Scanner 处理。以单 completed 为例，源 wire 和 host units 为：

```plaintext
id: event-1\nevent: response.completed\ndata: <完整 gCPACompleted>\n\n
```

```go
[]string{"id: event-1", "event: response.completed", "data: " + gCPACompleted}
```

共享 Responses output-unit 机制必须保留 metadata 原文字节、顺序及其与真正 event/data 之间的字段边界。对应 mapped native 输出为 `id: event-1\nevent: response.completed\ndata: <完整 gCPACompletedWant>\n\n`；`retry: 100` 和 `: heartbeat` 使用相同规则。只恢复真正 data JSON 的现有白名单模型字段为 `grok-4.6`，完整 `response.output`、delta、opaque 中的 `grok-4.7` 保持。metadata 文本中出现 `event:`、`data:` 或 `{"model":"grok-4.7"}` 时保持 opaque，不创建数据事件、不改写其中的模型。

单个和连续 metadata、field-pair/data-only、单 completed/九事件均属必需验收。正常支持的位置包括 event 前、event 与 data 之间，以及前一事件之后、下一事件之前；每个位置的 units 必须用真实 producer/validator/HTTP/WS 无 mapper 控制核验。对于 data-only，event 与 data 之间的位置对应 data 前。连续控制固定为 `id: event-1`、`retry: 100`、`: opaque event: response.created data: {"model":"grok-4.7"} 中文`。不由这些已核验 metadata 推导未知字段的新 callback 支持；unknown fields 的既有 generic wire 和实际 consumer 控制继续保留。

metadata 不改变已成立的数据单位或原派发时机：field-pair 四项 callback 的累计 dataFrames 仍为 `0、1、1、2`；data-only 仍在下一独立 data 到达时派发前值、Flush 派发末值。前置或连续 metadata 本身不增加数据帧，迟到 LF/CRLF delimiter 不重复派发。已完整 metadata 前缀、完整 JSON 和下一 incomplete tail 分开处理，保持 16 MiB incomplete 限额、所有完整 chunks+error、输入/输出 ownership、自然 EOF 与全部错误/关闭策略。

修正只在 G 拥有的共享 rewriter/scanner 中完成。保留当前 format、接口、增量游标/reset、B 的白名单恢复器、C 的 batching/error 消费和 D 的生命周期；不修改 CPA，不在 HTTP/WS 各加处理，不增加 provider 推断、ABI 标志、配置、parser 或 fallback。generic 连续 wire 含真实 LF/CR/CRLF 时继续保留 whole、全部 split、bytewise、BOM、metadata 原字节并等待标准 delimiter，不把任意网络/read 分片补成独立字段。

真实 mapped `/v1/responses` HTTP 必须为 200，包含全部可解码且可派发的数据事件；WS 必须依次交付完整 JSON，包含完整 completed output 和恢复后的 client model。相同原 direct 保持 `grok-4.7`，disabled 的实际 `.6/.7` 请求分别保持其模型；无 metadata、原 event-only opaque metadata、全部旧 F15 和 F01..F14 控制保留。该 metadata 要求的永久 baseline 使用 `3dce8db7373d2da6cfd792a30e2c323e0525bdc5` 的未修复 DLL，原 F01..F14 与 F15 两个 baseline 不改变。

##### 增量扫描与性能

进入 header helper 前先检查 format、是否仍需记录 header、下一 Payload 是否具有相关 field 前缀。优先复用 C 已有 header 游标；候选已记录后不重新从位置 0 扫描 JSON bytes。data-only 同样使用已记录的完整值/单位位置。

本轮对旧示例中的 `splitSSELine` 重复全缓冲调用实测：2 MiB/8 KiB 为 89.78..90.85 ms；8 MiB/8 KiB 为 1435.47..1461.44 ms，均零 allocation。输入四倍产生约十六倍扫描耗时。此结果只验证旧示例，未测量 C 的在修改实现。

永久检查加入两类 native 2 MiB/8 KiB 与 8 MiB/8 KiB 的真实续写 benchmark，保留 byte-exact 模型/opaque/长度预检查和 input/output ownership。allocation 门槛不放宽；同时检查增量游标和实际 ns/op、B/op，不能仅以 allocation GREEN 宣称扫描线性。

#### 不变条件

- generic 连续 SSE wire 的 LF、CR、CRLF、BOM、任意合法网络/ABI 字节分片、多个 event、id/retry/comment/unknown fields 保持既有字节和单位。Responses 在原 validator 及对应 HTTP/WS 消费者支持的 callback 分片上保持数据、metadata 与 output-unit 语义；网络 Write/Flush 不直接等同于 callback 边界。
- OpenAI/Gemini raw core、Responses/Interactions 的格式隔离、raw JSON 单值/序列/array、framing/batching 和既有 logical-event 机制保持。只恢复现有六项白名单，不递归改写 opaque/tool 文本。
- `maxPendingStreamBytes = 16 << 20` 不变。完整单个或多个单位及完整累计流量不计入 incomplete 上限；此前 pending 未超限才可继续补完。超限 incomplete 单位清空并报错，Flush 不输出它；C 的完整前缀与并存 error 传递保持。
- 自然 EOF、terminal Payload+Done、in-band/callback 读错误、cleanup error、host close 一次及已有关闭失败补救保持。正常 Responses 不添加 DONE。
- 不修改 CPA 源码/module cache；只在分配的 worktree 或 OS 临时副本测试；不开展任务范围外的调查。不新增产品功能、配置或依赖，开发默认 `pluginVersion=0.0.0-dev` 保持。

#### 唯一永久 native/HTTP 矩阵

E 对 XAI/Codex 两种真正 builtin 同时覆盖 field-pair 和 data-only；每种含 completed-only 和九事件。九事件顺序固定为 created、in_progress、output_item.added、content_part.added、output_text.delta、output_text.done、content_part.done、output_item.done、completed，使用 Task G 的完整 type 名称和 JSON。

每种输入覆盖 LF/CRLF whole、LF 7-byte、CRLF 单字节及两种 charset 控制。上游分段是正常带行结束的 HTTP SSE，另行核对 core/host 的真实 logical fields。真实 native loader、RPC/callback、BaseAPIHandler/validator、Responses HTTP handler/framer 均运行，只把外部服务替换为正常本地 upstream。

HTTP matrix 保持同规则 enabled mapped grok-4.6、enabled direct/unmatched grok-4.7、disabled 同名 grok-4.6 与 grok-4.7，以及全部非流控制。注册两个实际 model，不用 alias 暗中替换 disabled 的 upstream model。mapped/direct 上游普通 request/response bytes 完全相同；disabled 按其实际 model 验收。

精确检查 data 事件数量、顺序、type/event 名、全部 JSON payload、完整 delta、全部 completed output、opaque 文本、response.model 与 HTTP 200。data-only 的 SSE decoder 默认 event 名为 message，按 JSON type 识别生命周期；没有人为补 event header 的要求。native 单 terminal data-only 的无最终 blank delimiter不能单独算失败，真实 HTTP framer 必须正常派发一条。多个 emit 可含多个有效事件，emit 次数不代替 data 事件数。

CPA fixture 复用已安装 `gin-contrib/sse.Decode` 和 `encoding/json`；该 decoder 只识别 LF，测试只对 parser 输入统一已知 CRLF，原始 captured bytes继续保留，不自行编写 SSE parser。正常 mapped/unmapped/disabled、非流、两类九事件均须通过，才可确认 C/G 完整覆盖 F15。

native 单 terminal data-only 的直接 JSON 断言仅用于 `none`；有 metadata 时调用同一 SSE decoder，精确检查 metadata 原文及字段边界、真正 data、type、client model、全部 completed output 和 opaque。`none` 的无最终 blank delimiter 控制保留；上述 decoder 也支持 EOF 派发。

唯一 actual binary matrix 完整覆盖 `2 providers × 2 input shapes × 2 event counts × 6 transports × 4 route/model controls × 2 stream modes × 4 metadata values = 1536` 个组合。metadata 轴固定为 `none`、`id: event-1`、`retry: 100`、`: heartbeat`，每个有 metadata 的组合在每条正常 event/data 之前生成该 metadata 行；none 保留原完整 384 个组合。fixture/capture identity 包含 metadata 维度，精确核对笛卡尔积、重复和遗漏。连续 metadata 与 event 中/事件之间的位置另由同一永久 fixture 的真实 consumer/native/HTTP/WS 控制覆盖，不减少 binary 矩阵。复用当前进程分组、producer、loader 和 helpers，保存全部 upstream bytes、headers、URI、calls/status 与 client payload。必须运行固定 `cpa.exe`，package 层成功不能代替 binary 验收。

G 完成共享 metadata 修正、根回归、性能和累计独立审查；E 在唯一永久 fixture/入口完成新 metadata baseline RED 与最终 G/E DLL 的相同命令 GREEN。E 可在 G 修正期间并行准备自己的独立完整测试草稿和 baseline 证据，其最终验收与提交依赖 G 已验证产品。扩大 matrix 时增加外层及子进程 deadline，保留完整规模和 race/checkptr；根 race、普通 native DLL 和其他平台实际加载分别报告。共享源码改变后重跑受影响性能 preflight、allocation 与计时，旧报告只作历史对照；正式性能成本、C 原始 RED 日志缺失、Go VCS stamp 与报告者部署限制继续保留。

metadata 文档审查通过后，G/E 各自记录完整固定 BASE、执行起点和全部最终 HEAD，并在自己的新 native worktree 连续完成测试、自审、修正、提交及累计独立复审，不使用 `HEAD~1`。G 原产品 reviewBASE=`51b006438c1a0edd4263020297747f99a174c47c` 仍保留；metadata 审查另外覆盖各自固定 BASE..全部最终 HEAD。修正后完成受影响全分支复审和全部范围核对，才可进入 F；原发布授权、七平台 CI/Release、下载资产/runtime 来源及 issue8/PR7 评论要求保持。

#### 完成条件与发布边界

完整整合起点同时通过两类新回归和全部控制时，G 不重复改产品，记录实际修复归属，仍增加缺失的永久 producer 回归。仍 RED 且本规格修正已经独立审查通过、G2 的实际入口核查完成时，G 在同一 worktree 完成最小共用修复、GREEN/control、root/vet/race、allocation/benchmark、自审、独立审查、修正、复审和任务提交。同一实际入口出现可靠相反要求时按 G2 停止，不能提交为已完成产品修复。

E 完成唯一入口/fixture 的 baseline RED 与 fixed GREEN。F15 baseline 固定 6c7f060 的 DLL，单独记录来源，不改写 F01..F14 的原 baseline。新 native/HTTP GREEN、最终 C/G commit 及真实 Release 均尚未完成。

PR7 相关新提交 body 注明 `Related-PR: #7`、`PR-Author: @leolmq`；issue8 使用 `Refs: #8`，发版前不自动关闭。最终评论和版本在实际 Release 成功及资产核验后填写；不预填候选版本为修复版本，不改写旧提交署名。

## H，mixed-shape Responses ordinary metadata 修正

本节是当前 H 任务的完整规格，保留前面的 F01..F15、G/G-M/E/E-M 原要求和历史证据。固定起点为 `9f84a8135982f3c0097aba58e804ccf8fd38b5ba`。独立完成的 `M1-F15-MIXED-METADATA-01` 已确认 native/WS 缺陷，正式报告 SHA256 为 `7bcca60febf3868a64e8cc12af18538bbe671534e88c09dc94322e213029b58a`，协调核验 SHA256 为 `985c4fec85e133177b7cb8e1864880504d3bff0bb4748f5195d419f4877aef27`。这些报告属于旧 HEAD，不能作为 H 最终产品的审批。

### 当前输入和消费者边界

真实 XAI/Codex producer/host 接受 data-only `response.created`、ordinary metadata、field-pair `response.completed` 的独立无 LF/CR output units。9f84 把 created JSON、metadata、event/data 拼接为无效 data 行，两个 `response.model` 未恢复；同层 mapped metadata WS 96 项无 JSON，close 1006 unexpected EOF。direct/disabled WS 360 项、mapped none WS 24 项和 480 项非流正常。根回归与真实 native 验证必须重现该失败后修正。

共享 scanner 按已核验的 output-unit 边界处理 data-only -> field-pair、field-pair -> data-only 和多次交替。ordinary `id: event-1`、`retry: 100`、`: heartbeat` 以及连续 opaque metadata 的原文字节、顺序、before-event/in-event/between-events 位置保持。仅恢复原六项响应白名单，完整 delta、completed output、tool/opaque 字节保持。沿用现有 scanner、增量游标、JSON parser、framing、batching 和错误消费接口，不新增 provider 分支、配置、依赖、ABI 字段或第二 parser。

先前完整 data-only 单位在下一已核验的 event/data 单位到达时派发；metadata 本身不增加 data 帧。前值派发后 metadata 留给下一事件，不能作为 JSON suffix 使 scanner disabled。field-pair 在完整匹配 data 到达时派发；末 data-only 由 Flush/Finish 派发。保持原 none、同形状、迟到 LF/CRLF delimiter、ordinary metadata 和所有正常反证。

direct/disabled mixed HTTP stream 自身缺少 completed，无 metadata 时也存在。H 对这条既存消费者限制记录准确 payload 和完成边界，HTTP 200 不能冒充正常完成；不修 CPA，不把该限制记为插件破坏正常 direct HTTP 的证据。正常 native 与 WS 必须完整通过。XAI 独立 WS 的 `prompt_cache_key`/`X-Grok-Conv-Id` 存在 60 项会话差异，保留完整捕获和严格 equality 的失败事实，精确检查差异范围；不能删除字段或声称严格 equality。Codex WS、两种 HTTP 的 mapped/direct request/header/URI/response equality 保持。

### 必需验证和所有权

H 的唯一 coding owner 在实际分配的 isolation worktree 连续完成文档、TDD、最小 shared 修正、完整验证、自审、修正和提交。只修改 `main.go` 的相关 shared scanner/rewriter、`stream_native_fields_regression_test.go`、唯一 `.github/scripts/testdata/cpa-functional-regression_test.go` 和两份 tracked spec/plan；确需固定 binary 交接时精确修改 `.github/scripts/smoke-local_test.go`。必要 benchmark 保存在现有 native/performance 文件。其他产品、CPA/cache、依赖、Makefile、CI、配置和其他 worktree 均不改。

根 tests 覆盖三种 metadata、连续 opaque metadata、none、两个方向/多次交替、三种位置、Flush/Finish、dispatch、所有权/增量游标、2 MiB/8 MiB 续写、16 MiB incomplete/完整前缀+error/累计完整流量。generic LF/CR/CRLF、BOM、任意合法 splits、四其他 formats、raw framing 和 C/G/G-M 原控制保持。真实永久验收只使用原唯一 smoke 入口和 CPA fixture，完整核对 producer/host/native/WS 及 fixed binary，保存上游 raw body、headers、URI、response 和最终 DLL 身份。

原 binary `2 providers × 2 shapes × 2 counts × 6 transports × 4 routes × 2 stream modes × 4 metadata = 1536` 矩阵逐项不变，其中 none384 保持。新增 mixed 组合单独命名并生成完整 expected-map，unknown/duplicate/missing 失败，每项有准确消费者预期。不得在原 matrix 数量下冒称新增维度，不过滤旧断言或新失败。

最终当前源码必须通过 `go test ./... -count=1`、`go vet ./...`、`go test ./... -race -count=1 -timeout=1800s`、三个显式 scripts、版本注入/注册/schema、全部 59 个原 benchmark 的 1x byte-exact preflight（含已新增 metadata 共 60 个旧 preflight）及本次必要新增 preflight。实际 CPA 唯一入口在新产品/永久 tests 上完整 GREEN，loader/reconfigure/reload/首次 unload17 drain/bridge 原控制保持。根 race 与 native DLL/实际 binary 分层报告。

性能使用相同已验证 policy，9f84 source 与新 fixed 受影响 benchmarks 顺序 foreground `-count=5 -benchmem`，保存全部样本、中位数和比较。旧五版本数据保留历史，9f84 错误 mixed 输出不计正常性能基线；新增 mixed benchmark仅报告 fixed 测量。保留 allocation 门槛和游标/ownership，记录实际性能成本，不宣称 CPU 独占或未经测量的复杂度。Windows/Linux c-shared、source identity、packaging/compatibility/zip/checksum按现有 parser 完成，Linux GLIBC <=2.17；linked-worktree GoVCS、Go1.26.5/GCC16.1.0/Zig0.17.0 与 CI Zig0.16.0 的限制保留，七平台 runtime/CI/Release 留给 F。

全部稳定证据在 owner ROOT 的 ignored `dist/task-H-mixed-metadata/`，完整报告为 `task-H-report.json`。保留真实命令/exit、修正过程，未公开数字为 null，guard 拒绝为 processStarted=false，不伪造成功。最终提交 body 包含 `Refs: #8`、`Related-PR: #7`、`PR-Author: @leolmq`；累计 packages 覆盖固定 9f84..最终 HEAD、原 7855..最终 HEAD、3dce8db..最终 HEAD。owner 自审不代替两门独立审批，控制 workflow 在完成后分派；确认缺陷或缺口修正并重验后才可继续 F。H 不推送、tag、Release、评论或关闭。

## 实施分工和依赖

| 任务 | 产品函数所有权 | 新测试与其他文件 | 依赖与完成条件 |
| --- | --- | --- | --- |
| A | `handleModelRoute` 的 runtime error 分支。 | `route_runtime_regression_test.go`。 | 可与 B、D 并行。focused RED/GREEN、全量 root suite、规格/质量独立审查、修正和提交全部在自己的 worktree 完成。 |
| B | `rewriteTopLevelModelCanonical`、响应 `rewriteResponseModel*`、`rewriteNestedRawStringFields`、`rewriteRawStringField` 及必要相邻 JSON span helper。不改 marker scanner 的能力。 | `json_rewrite_regression_test.go`；`main_test.go` 中所有仅因 JSON 保序而过时的 expected bytes。 | 可与 A、D 并行。必须独立修完全部保序 fixture 并达到 root suite GREEN，不把失败留给 C。 |
| C | `main.go:32-1214` 的 rewriter/scanner/classification，`emitRewritten`、`prepareExecutorStream`、`processPayload`、`flushAndEmit`。 | `stream_protocol_regression_test.go`；framing/emission/batching 的旧测试；必要 `performance_regression_test.go` 检查；`README.md:180-205` 的相关说明。 | 可提前分析和编写独立 RED。涉及共享 fixture 的编码从 B 已验证提交开始。只修改 C 的生产函数，不修改 `executorStream` 生命周期 struct 或 D 的函数。 |
| D | `executorStream` 的生命周期字段，生命周期函数、`closeHost`/`closePlugin`、`startExecutorStream`、`finish`、`runStreamForward`。 | `stream_lifecycle_regression_test.go`；必要的 lifecycle 旧测试。 | 可与 A、B 并行。不得修改 C 的 payload/flush 函数；独立全量测试和审查完成后提交。 |
| G | C 最终已验证 HEAD 上，共享 `streamChunkRewriter.Write/Flush/Finish`、`sseRewriter.Write/drain`、delimiterless scanner/reset/classification 的 F15 必要修改。只消费 C 的 framing/batching 和 chunks+error，不改 A/B/D 或 C 的 executor helper。 | `stream_native_fields_regression_test.go`；必要协议／性能 fixture。完整 producer/native/HTTP 草稿交 E 合入唯一 CPA fixture。 | 产品 reviewBASE 固定 `51b006438c1a0edd4263020297747f99a174c47c`，执行起点为协调者主动交付的完整整合 HEAD；规格修正独立审查通过后按 G2 核查入口和时机。同一实际入口出现可靠相反要求时停止产品编码。满足后在同一独立 worktree 连续完成必要 TDD、全量测试、性能、自审、独立审查、修正、复审和提交；C 已覆盖时只补缺失回归并记录归属。 |
| E | 永久 actual CPA integration 与完整终验。 | `.github/scripts/smoke-local_test.go` 和唯一永久 fixture `.github/scripts/testdata/cpa-functional-regression_test.go`；只有测试入口确需改变才修改 `Makefile`、`.github/workflows/build.yml`。 | 等 A/B/C/D/G 完成并合并后，在新的独立 worktree 完成。新增 integration 对 baseline DLL 证明 RED，对已修 DLL 证明 GREEN；F15 baseline 固定 `6c7f060`，F01..F14 原 baseline 保持。 |

`main.go` 的函数边界不消除 `main_test.go` fixture 依赖。B 负责保序 fixture，C 负责协议职责和 emit 次数；同一测试同时受两类行为影响时 B 先完成并提交，C 从该提交启动后续修改。固定 C 最终审查提交 -> 本输入层级规格修正独立审查通过 -> G 有限接口核查、必要完整 TDD／审查／提交 -> E 全部永久 actual 验收 -> F 发布。G 与 C 不并行修改共享 rewriter/scanner；G2 停止条件未解决时 E/F 不放行。集成者只合并已完成且检查过修改范围的提交，不代写未完成产品代码。

每个 coding workflow 内包括 focused TDD、全量测试、规格审查、代码质量审查、修正、复审和提交。独立审查发现新共享位置时，更新文件和函数所有权后继续，不能让两个工作流修改同一函数或同一测试。

## 永久验证和性能条件

1. A/B/C/D/G 的 focused tests 必须在各自固定基线证明目标失败，在修复提交通过；每个完成的编码工作区 `go test ./...` 必须 GREEN。G 的产品 reviewBASE 固定为 C 最终 HEAD，F15 原始 RED／E baseline 固定 `6c7f060`；规格修正尚未独立审查通过，或同一实际入口出现可靠相反要求时停止产品编码并交回证据，不提交产品修复。全局配置和生命周期测试不使用 `t.Parallel`。
2. E 的 actual CPA matrix 覆盖 mapped/unmapped 非流和流的 Chat、Completions、Responses HTTP/WS、Claude、Gemini、Interactions；非空 `agent` 继续原生。F01/F02/F03/F04/F08/F09/F10/F12 使用已证明的实际 HTTP 形状，F05/F06 使用 actual host/DLL unload，F07/F11/F14 保留 focused unit 和实际 host/validator 层核验。F15 使用 G4 的真正 XAI/Codex producer、native loader/ABI/host/validator、Responses HTTP framer 和 binary smoke，两类 1/9 事件、六种 transport、mapped/direct-unmatched/disabled 同名 `.6/.7` 及全部非流均精确验收。各层级据实报告。
3. WebSocket 和 native host fixture 使用 CPA 固定模块已有 `gorilla/websocket`、`package pluginhost` 接口、loader 和测试 helper。先把核对为 `v7.2.152` 的源文件复制到本次 `t.TempDir()` 的可写副本，再把 Go overlay target 指向该副本的 `internal/pluginhost/model_mapper_functional_test.go`；Go 1.26.5 禁止 `GOMODCACHE` 内的 replacement，包括新增虚拟文件。副本仅供集成测试，测试完成后清理；不向根模块引入产品依赖，不手写 WebSocket parser，不改原 CPA 源码或 module cache。
4. `go test ./...`、`go vet ./...`、`go test -race ./...`、register/schema 回归，以及 packager、compatibility、smoke 三个显式 script test 命令全部通过。不得运行 `go test ./.github/scripts`。
5. 保留现有真实 allocation 条件：no-model response 至多一次 clone 分配；complete markerless/escaped SSE batch 至多 6 次分配；fragmented raw JSON 少于 100 次分配；delimiterless 2 MiB/8 KiB 分片至多 200 次分配；unique request 的分配不随 opaque node 数线性增加。F15 的 field-pair/data-only 2 MiB/8 KiB 续写同样至多 200 次分配，并运行两类 2 MiB、8 MiB／8 KiB benchmark、核查增量游标和实际 ns/op、B/op。输出正确性、所有字段和 payload 长度、opaque/ownership 预检查保持，不以 allocation 通过代替线性扫描证据。
6. B 与 E 显式运行 `BenchmarkSSEMarkerGuardRestore`、`BenchmarkSSEMarkerGuardCandidate` 的 byte-exact preflight 和完整 benchmark；root tests 不运行 benchmark。两项固定输入的 `model` 在 `id` 前，B 将旧 map 排序的 expected 改为 `{"model":"client","id":"r1"}`，保留输入和全部断言。在同一机器、Go、构建模式和参数下比较 baseline/fixed 的现有 request、response、marker scan、fragmented raw JSON、markerless/escaped SSE、emit batching benchmarks。不得凭未测量的百分比设置性能门槛或宣称优化；发现稳定回归先定位新增扫描或复制，再修正和复测。修正不能牺牲内容或协议。
7. Windows/Linux 实际构建、打包、compatibility 和 metadata 检查完成；release 经实际用户授权后复核 remote/tag，七平台 CI 和 Release 成功，下载 assets 验证 zip 根目录、LICENSE、版本和 `checksums.txt`。

## 公共报告验收补充

F15 的具名后续所有者为 G，按固定 C 最终审查提交 -> 本规格修正独立审查通过 -> 完整整合起点上的 G2 有限接口核查／必要完整 TDD 与审查提交 -> E 全部永久 actual 验收 -> F 发布执行。以下既有公共报告的证据、控制和命令保持；新增 F15 矩阵按 G 绑定规格及 G4 执行，两个 baseline 独立记录。此前 disabled same-input alias 是原有控制／历史证据，G4 新矩阵另注册两个实际 model，disabled `.6/.7` 按其实际 model 验收，不用 alias 替换新矩阵的 upstream model。

以下验收补充现有 Global Constraints，沿用 C/E/F 的所有权，F01..F14、既有边界和全部验证条件保持不变。后续 PR7 相关产品修复、复审修正、永久回归和文档修正提交均注明 #7 与经原始 PR JSON 核实的作者 @leolmq，commit body 使用 `Related-PR: #7`、`PR-Author: @leolmq`；C/E 和最终审查核对实际 commit message，Release 与最终 PR 评论注明该作者贡献。不编造姓名、email 或 `Co-authored-by`，不改写已完成提交历史。

1. [PR7](https://github.com/DoingDog/cpa-plugin-model-mapper/pull/7) 归属 C/F04 的同一 framing 根因，由 C 统一完成 TDD 和旧 framing/emission fixture 修正。E 对真实 CPA `/v1/chat/completions`、`/v1/completions` 的 mapped/unmapped 流检查 HTTP 200、完整内容、各自 client model、可派发且可解码的 JSON SSE、`data: data:` 为零、DONE 恰好一次。缺少 `Content-Type`、`text/event-stream`、`text/event-stream; charset=utf-8`、`application/json` 的正常 core/header 对照和最后一个 raw payload 的 native 对照在实际可达层执行，记录插件收到的 headers 与 core bytes。源探测的单个 raw payload 内容为 `hello`；E 既有 HTTP fixture 的完整内容为 `onetwo`，分别精确断言，不互换预期。
2. [issue8](https://github.com/DoingDog/cpa-plugin-model-mapper/issues/8) 使用原规则 `grok-4.6=>grok-4.7`、`Format/SourceFormat=openai-response` 和完整 `response.completed` terminal fixture。启用 plugin 的 mapped/unmapped 上游请求 `Model`、顶层 JSON `model` 均为 `grok-4.7`；mapped 下游 `response.model=grok-4.6`，unmapped 对照为 `grok-4.7`；完整 `response.output` 和 opaque output 文本中的 `grok-4.7` 保持。C 的既有 terminal 控制覆盖 `Payload+Done`、split terminal、`Payload+Error+Done`，保留原错误与 emit -> host-close -> plugin-close 顺序；E 的正常 native producer 使用 payload 后独立 Done 的真实 bridge 对照。真实 `/v1/responses` mapped/unmapped 和禁用 plugin 的正常请求均须为 200，含有效可派发 data/terminal，完整 output 不丢；错误用例在其实际 error/terminal 通道保留原错误。
3. 正式报告 `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/.superpowers/sdd/2026-10-02-functional-fixes-implementation/issue8-alternative-verification.json` 已完成两种 native 方法交叉核验，`originalIssueReproduced=true`、`commentAllowed=false`。独立重建并重跑 `6c7f060` Windows DLL／固定 CPA，确认真实 XAI/Codex producer 的无 LF 字段边界缺陷：两个独立 host payload `event: response.completed` 和 `data: ` 后接完整 terminal JSON 被错误合并，零有效 data frame 导致 mapped `/v1/responses` 返回原文 502。官方 `v0.5.8`／`v0.5.11` Windows DLL 与 Debian WSL2 Linux `.so` 的相同结果依据保留的结构化证据、transcript 和原始输出核对；原发布资产和原始 HTTP JSON 已清理，交叉核验未重跑 Linux matrix，`.so` 的 dirty build metadata 不证明其源码字节等同于 clean tag。C 在最终已验证 HEAD 原样检查两字段输入、18 个无 LF 字段的九事件 lifecycle、单 terminal data-only 和九个独立无 LF data-only payload；仍为目标 RED 时由同一共享 rewriter 的已分派后续任务顺序完成 TDD、修正、完整验证、独立复审及提交，已有 GREEN 时保留回归和实际结果。E 在唯一 fixture 保留真实 `NewXAIExecutor`／`NewCodexExecutor` 的 mapped/unmapped/禁用 plugin native/HTTP 对照及 XAI core 空 chunk；单 terminal data-only 保持一帧、模型恢复和完整 opaque output，九个 data-only payload 保持九个有序有效帧、完整 delta/output 和 `response.completed`。真实 producer、native bridge、Responses framer 和 HTTP 分层记录，不将 framer 结果当作未运行的 HTTP 结果。完整 SSE、合法字节分片和原错误控制不变；本地复现与报告者 Linux/Docker 的唯一根因分别判定。
4. 请求体调查复用已有补充调查和 E 的唯一入口：在 OS 临时目录启动固定 CPA/native DLL 与 mock upstream，每组使用独立 CPA 进程及相同请求序列，对照禁用 plugin 请求 `grok-4.6`、启用原规则请求 `grok-4.6`、相同配置直接请求 unmatched `grok-4.7`，同时保留正常非流／流对照。记录实际捕获的完整 raw/parsed body、非 `model` member 的顺序与字节表示、opaque 内容、相关 request/response headers、HTTP 状态／错误及其对应请求。禁用组不执行映射，模型预期独立记录。请求体独立核验已完成，正式报告 `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-fixes-20261002/.superpowers/sdd/2026-10-02-functional-fixes-implementation/issue8-request-body-independent.json` 记录 `originalIssueReproduced=true`。真实 Windows DLL／固定 CPA／HTTP 的 xAI mapped 流返回 HTTP 502 `upstream stream closed before first payload`，direct／disabled 流及非流对照正常；正常 minimal-message 的 mapped/direct-unmatched 上游 raw body 和正常响应逐字节相同，捕获 headers/URI 一致，未确认额外请求体改写缺陷。两种 native 方法的整体交叉核验已由前述正式报告确认无 LF 字段边界缺陷；报告者 Linux/Docker 的唯一根因尚未匹配，最终相关修复 commit／首个 Release 尚未确定。当前不发送无法复现评论；各组结果据实记录，请求体差异本身不认定为缺陷或原 502 根因，不重新分派重复调查或产品任务。
5. 独立 PR7 探测运行真实 CPA HTTP handler，upstream/RPC 为 mock；早期独立 issue8 terminal 探测使用真实插件 Go 函数和 fake callbacks。E 分别报告 mock、native loader/ABI/host bridge、完整 HTTP 的结果与限制，组合 terminal flags 的 ABI 接受对照不替代正常 producer 的实际 read 序列，也不作为报告者 Linux/Docker 现场复现。新增永久 integration 全部进入 E 的唯一 `TestCPAPluginIntegration` 和唯一 `.github/scripts/testdata/cpa-functional-regression_test.go`，复用现有 smoke helper、CPA parser 和 `encoding/json`；不增加第二入口，不修改 CPA，不新增 parser 或猜测性 workaround，generic 连续 wire 的任意 host read 边界仍不作为 event delimiter；Responses output chunks 的边界按 G2 的原消费者语义验收。
6. issue8 现场信息请求独立于发布后的修复说明。正式交叉核验已记录 `originalIssueReproduced=true`、`commentAllowed=false`，无法复现时的信息请求评论阶段已跳过，按已确认缺陷继续 C/E 验证。其他有效复现方法完成并经独立核验仍未复现时，按真实用户授权立即评论请求 CPA 失败请求日志、准确插件版本与 CPA revision、部署／endpoint、相关配置、原始 SSE 和 host read 信息，并核验评论正文、URL 及 issue 仍为 open；该条件评论无需等待 E 全部终验或 Release。
7. 固定核查 HEAD `6c7f060f4da5bb33e7b2ecd74c44499c9676a93c` 仍复现 PR7；`9fe917c031f0a7ed8b43897220a1eea237acc88b` 是候选，真实 `v0.5.7` 仍有问题。F 在成功发布并核验后，按真实用户授权评论 C 的实际修复 commit、首个包含它的 Release 版本及 #7／@leolmq 的贡献。issue8 的历史 terminal 修复 `8b7bd9a0d135251878792b3740bc06a7da7ede00`／`v0.5.2` 早于提问，不能归因于原 issue；请求体独立核验及正式 native 交叉核验已确认本地 mapped 502 和无 LF 字段边界缺陷，实际修复 HEAD 和首个 Release 尚未确定。F 根据最终 E 实际结果评论，仅对已证实相关修复填写 commit/版本；未复现或未证实已发布版本解决报告时说明核查范围及仍缺的部署版本、原始 SSE/host read、CPA revision，保持 open。修复发版并完成资产核验后，issue8 评论注明已尝试修复及实际 commit/版本，要求提问者更新并在自己的环境测试；本地通过不表示报告者环境已解决。后续已发布版本确实解决报告且获得用户授权后，评论已证实相关 commit/版本并关闭。

## 文档完成条件

两份文档覆盖 F01..F15 和全部控制条件，明确证据层级、函数/fixture 依赖、RED/GREEN 命令和终验。原 F15 补充草稿的复审保留为历史记录。本次完整更正草稿已经编译并运行正常控制，F15 目标仍真实 RED；本规格修正须独立审查通过后启动 G，产品尚未修复。自审核对输入单位、派发时点、generic 分片控制、全部原验收和停止条件；无占位、无新增功能、无未经测量的优化承诺。更正后的完整 Go 草稿与本次编译输入逐字核对，`git diff --check` 通过。文档提交只包含本规格和对应实施计划，不包括产品源码、中间结果或其他工作区的内容。
