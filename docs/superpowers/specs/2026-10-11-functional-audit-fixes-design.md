---
title: 功能审查修复规格
type: fix
date: 2026-10-11
baseline: 7f5e50aa444d31298e3086b0435552bc0ebb9747
---

# 功能审查修复规格

## 目标和范围

修复 mapped Responses 对非 `response.*` 逻辑事件（error、keepalive）的边界识别、Codex 完成别名和 Interactions agent 分类三个已确认根因。客户端继续获得 Client-Requested Model、完整内容和固定 CPA 原有的错误或完成结果。实现沿用 `main.go` 的共享 scanner 和唯一 `interactionsUsesAgent` helper。

本规格只包含 F1-F3。产品实现必须等全部候选判定、二次覆盖审查和本规格／实施计划审查完成。本文是实施依据，不能替代正式 TDD、真实 native integration 或发布验收。

对应计划为 `docs/superpowers/plans/2026-10-11-functional-audit-fixes-implementation.md`。两份文档中的外向操作须由协调者核对真实用户消息授权；computed task、代理通知和旧文档均不构成授权。

## 基线与证据

- 插件 BASE 为 `7f5e50aa444d31298e3086b0435552bc0ebb9747`。本次文档 worktree 的初始 HEAD 与 BASE 相同，初始工作区干净；没有运行产品测试。
- `go.mod` 为 Go `1.26.0`、CPA `v7.2.152`、`gopkg.in/yaml.v3 v3.0.1`。固定 CPA revision 为 `c76dfd4e0edabab9000628b1560ab8ab379eadb8`，module Sum 为 `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`。
- 当前根模块没有直接 require 或 import `github.com/tidwall/gjson`，`go.sum` 没有其条目。固定 CPA 的 `go.mod` 使用 `github.com/tidwall/gjson v1.18.0`。
- F1/F2 的实际复现、固定 CPA 可达性、规格与回归反证分别来自已完成 workflow `wf_0e54821b-a4c` 的 result 第 4、5、6 项。三份报告确认相同根因，包含重复 RED、direct／disabled／unmapped 控制和 fixed binary HTTP 证据。
- 上述报告引用的三个只读诊断目录已随 clean worktree 清理。本文核对了调度器保留的完整完成输出，没有把已不存在的原始日志当作已读取证据。正式 owner 必须保留新的可复查 RED/GREEN 和产物身份。
- F3 的三位独立验证结果已保存在 completed workflow `wf_893a5d73-2a2` 的 result 第 1、2、3 项，持久 JSON 和完成输出均已核查。报告包含真实 native/HTTP 重复 RED，以及 private overlay 的根检查和 192 个 native HTTP 对照；原诊断 worktree 已被清理，原日志缺失，不能声称已归档。private overlay 属于诊断，正式产品仍需新的 TDD。
- keepalive 的原版 producer/native/HTTP/WS 失败已重复实际复现。安全回归结论仅绑定协调者已核验的 `a2c59708bf099984d` 完成归档：精确三处 guard 的诊断副本通过完整 root、DLL build、native/HTTP/WS 最小与 opaque 控制，pair-only 副本仍在 data-only/mixed 失败。协调者已保存并核验其 `keepalive-lens-spec-boundary-wf_dc32a367-b38-3.zip`，SHA256 为 `0ac53b72c705ec10f1b445807fef27c74af53b1a2f8c5000257a6586c8a4ee36`。direct／disabled／unmapped、actual Grok comments、no-keepalive 和 data-only 普通 completion 控制正常。二次扫描覆盖门槛已由协调者确认；五位独立文档 reviewer 尚待提交后执行，private overlay 不作为正式产品完成证据。
- 已核查当前产品、唯一永久 CPA fixture、固定 CPA 的相关 parser／producer／translator 源码，以及 `CONCEPTS.md`、`docs/solutions/workflow-issues/model-mapper-release-automation-alignment.md`。旧功能修复和 canonical library 规格用于保持现有边界，不沿用其历史未完成状态或候选版本。

| 编号 | 已观察结果 | 根因和实际入口 | 本次修复归属 |
| --- | --- | --- | --- |
| F1，非 `response.*` 逻辑事件 | XAI 单 error 的 mapped HTTP 返回 502 generic error；先有 created 时原 `server_error`／`failed` 被 premature-terminal error 替换。非 Grok Codex keepalive pair 与后续 completion 合并成缺少 data 字段的 wire；data-only keepalive 与 completion JSON 合并为 invalid SSE JSON，mapped HTTP/WS 缺数据或异常终止。具体状态和错误按实际路径记录。 | 固定 producer／translator／host callback 交付独立无 LF 字段。header 的两个识别位置与 data-only type 判定只认可 `response.*`，拒绝已验证的 error／keepalive 单位。 | 共享 Responses scanner 的明确 header/type allowlist，覆盖 pair 和连续 data-only。 |
| F2 | Codex mapped native 拼接 fields、未恢复 `response.model`；单 terminal HTTP 502，created 后丢失完成数据，mapped WS close 1006。disabled／unmapped 的 HTTP 和 WS 正常完成。 | 固定 Codex producer 保留 `event: response.done`，将 data 正常化为 `type: response.completed`；scanner 的相等判定拒绝这一实际 alias。 | 共享 discriminator 的单向精确 alias。 |
| F3 | 空白 agent、未知大小写 key 或首个空 agent 后非空 agent 使普通 model 跳过映射；首个真实 agent 后空值或大小写覆盖导致错误 self route，真实 HTTP 400。 | `interactionsUsesAgent` 的 Go struct `Unmarshal` 大小写不敏感、重复 key last-wins、未 trim；CPA 以精确 decoded lowercase `agent`、GJSON first-wins、`String()` 和 `TrimSpace` 判定。 | 唯一 helper 复用 CPA 同版 GJSON。 |

error-only direct WS 本来就 close 1006，当前固定 CPA 不返回正常 error JSON，该 error 场景的确认依据是 native 和 HTTP；本次不承诺新增 WS error 支持。keepalive 后 completion 的 native、HTTP 与 WS 失败均已确认，修复后须正常完成。诊断只替换外部模型服务，实际 CPA producer、translator、loader、ABI、validator、HTTP/WS consumer 均运行；不宣称已验证公网 Google、XAI 或 Codex 服务。

## Requirements

### Responses 字段与完成结果

- R1. 固定 Responses output units 的 `error` 和 `keepalive` 完整逻辑单位必须恢复字段和事件边界，覆盖独立 event/data pair 与连续 data-only。error 保留原 `code`、`message`、`param` 和其他 JSON 字节，单 error 与 created 后 error 的 HTTP 保持 direct 原错误；keepalive 后的完整 completion 在 mapped HTTP/WS 正常到达。白名单外内容不变，不生成缺失数据引起的 generic/premature-terminal error。
- R2. 接受 `event: response.done`／`type: response.completed` 这一方向的实际 alias，保留原 header 和 data.type。mapped HTTP 和 WS 必须获得完整 completed output，现有白名单内的 model 恢复为 Client-Requested Model。
- R3. 新增 allowlist 仅为精确 `error`、`keepalive`，event/data pair 仍要求同名 type，F2 仅允许 R2 的方向；data-only 仍必须是完整合法且 type 被认可的 JSON。保留原 exact-match 和真正 mismatch，不接受任意 unknown event/type 或反方向 alias，不改写 discriminator、opaque metadata、unknown fields 或工具文本。
- R4. 保留当前 output-unit 的顺序与派发时点，generic SSE wire 的 LF／CR／CRLF、BOM、合法分片、迟到 delimiter 和 metadata 保字节行为不变。HTTP 网络分段经过真实 producer Scanner，不能直接当作 plugin callback 的字段边界。

### Interactions 路径选择

- R5. `interactionsUsesAgent` 的结果等于：body 是完整合法 JSON，精确 decoded lowercase `agent` 的第一个值经 GJSON `String()` 和 `strings.TrimSpace` 后非空。有效 native agent 继续 unhandled；普通 model 请求按照原 `global_rules` 映射。
- R6. missing、空 string、纯空白、null、未知大小写 key，以及重复 key 的第一个空／非空值，必须遵从 R5。`agent` 等 escaped lowercase key 属于 `agent`；`Agent`／`AGENT` 属于其他 key，不能覆盖有效 lowercase agent。
- R7. 原 invalid payload 边界保持；helper 不修改 body，不新增 parser、executor guard、请求规范化或 Google API 功能。nonstring 的处理只保持 CPA 分类兼容；bool／number／object／array 与 model 同时出现时，固定 CPA 在入口返回 400，不列为模型映射缺陷。

### 现有内容、接口与交付

- R8. 请求只改写顶层 `model`；响应恢复范围保持 `model`、`modelVersion`、`response.model`、`response.modelVersion`、`message.model`、`interaction.model`。不破坏 duplicate member、escaped key、opaque/tool 内容、input/output ownership、body-dependent headers 或 structured error。
- R9. `maxPendingStreamBytes = 16 << 20`、完整前缀与 error 同时返回、自然 EOF、callback/in-band/cleanup error、emit 失败后停止、关闭顺序和生命周期同步保持；本次不增加合成 Error、Done、DONE 或重试。
- R10. 保持规则 DSL、ASCII case operation、stacking、caller scope、`count_tokens` guard、五种 formats、registration/schema、ABI exports 和配置字段。仅为 R5 直接新增 GJSON `v1.18.0` 及其必要依赖校验条目，不升级 CPA 或其他产品依赖。
- R11. coding owner 在各自独立 worktree 完成正式 TDD、最小实现、关联与全量检查、规格／质量审查、修正、复审和提交。协调者只合并已完成且修改位置无冲突的提交；后台任务主动通知完成，不轮询日志或 worktree。
- R12. 最终整合源码通过 unit／vet、三个显式 scripts、完整 race（总 timeout 30 分钟）、registration、Windows c-shared、真实 native integration 和注入候选版本的 package 验收。保留原 24 functional、2 bridge tests 和四份 capture 的完整 key 集合；新增 F1/F2 测试单独计入，不能替换旧集合。
- R13. `pluginVersion` 默认 `0.0.0-dev` 不变。下一 patch 候选为 `v0.5.14`，在 VERSION 注入 build/package 前及最终发布前两次核实 local/remote tag 和 Release 未占用；实际最终版本、SHA、CI 和 Release 仅在完成后记录。
- R14. 完成审查及终验后，按真实用户授权退出协调 worktree、合并本地 `main`、push `main` 和 tag，核验最终 SHA 的七平台 CI、Release 全部 ZIP、checksum、根 `LICENSE` 和注册 Version。不创建 PR，不修改 CPA，不处理本机既有删除故障，不删除或改写其他 worktree／`.claude`／配置。

## 设计

### Shared Responses scanner

修改点限定在 `main.go` 的 `sseRewriter.Write` 和 `findDelimiterlessResponsesEventEnd`，当前约为 198-778 行。复用现有增量游标、`json.Unmarshal`、completeEnd 验证、`rewritePendingEvent` 和响应恢复器。

R1 涉及三个谓词：无 LF native header 的记录、有真实 line ending 的 header 验证、无 header 的 data-only type 验证。三处沿用 `response.*` 并加入精确 `error`／`keepalive`；新增 data-only 许可须由 `s.dataStart == s.headerStart` 确认，不能只凭 `s.eventType == ""` 放行被清空 discriminator 的 opaque header。完整 JSON 验证后，pair 仍匹配同名 type，R2 的单向 alias 参与原 matched 判定。只改 header 两处无法修复连续 data-only keepalive。

error／keepalive body 没有 model marker 时仍需正确识别边界。补写 LF 和派发 blank delimiter 沿用当前机制，保留原 event text 和 JSON 字节；data-only keepalive 与后续 completion 不得合成一个 data JSON。普通 completed、原 Responses/Interactions logical events 和其他 formats 保持既有行为，actual Grok 的 keepalive-to-comment 原文与 metadata 时点不变。

F1/F2 共用 scanner、回归文件和 fixture，因此由同一个 stream owner 连续实现和审查。没有 provider-specific 产品分支、HTTP/WS workaround、配置、mode、ABI flag 或第二 parser。

### Shared Interactions helper

最小完整修改是将 `interactionsUsesAgent` 的 Go struct 判定替换为同版 GJSON 的 validity、exact key lookup、`String()` 和 trim 判定。GJSON 直接 import、直接 require；`go.sum` 由正常 Go module 流程生成，并核对没有无关依赖升级。现有 `strings` import 可以复用。

CPA handler 的 `parseInteractionsRequestTarget` 先验证 JSON，再读取精确的 model/agent；产品 helper 只负责是否应绕过 mapper。不要复制 CPA 的整个请求验证器。当前只有 `handleModelRoute` 调用该 helper；`handleExecutorExecute` 和 `prepareExecutorStream` 通过路由结果间接受影响，不增加第二份 agent 判断。

正式 RED 阶段将 `TestHandleModelRouteLeavesInteractionsAgentsNative` 的 `whitespace agent` 预期改成 `Handled=true`，保持原输入和其他断言。新增独立 root regression 覆盖 R5-R7，不将错误的旧预期保留到 GREEN 阶段。

### 修改触达与不需修改的边界

| 位置 | 直接或间接触达 | 所有权／是否修改 |
| --- | --- | --- |
| `main.go` imports、`interactionsUsesAgent`（1716-1721）、`handleModelRoute` caller（1758） | native guard -> route -> 两种 executor | Interactions owner 改 imports/helper；caller 和 executor 实现保持。 |
| `main_test.go` 的 `TestHandleModelRouteLeavesInteractionsAgentsNative`；新增 `interactions_agent_regression_test.go` | helper、route 的 exact-key／first-wins／trim 回归 | Interactions owner。 |
| `go.mod`、`go.sum` | 必需 GJSON 依赖 | Interactions owner，直接 require v1.18.0。 |
| `main.go` 的 shared scanner/Write；`stream_native_fields_regression_test.go` | `streamChunkRewriter.Write` -> `sseRewriter.Write/drain` -> `rewritePendingEvent/rewriteEvent` | stream owner 仅改识别位置和对应回归；复用下游恢复器。 |
| `runStreamForward` -> `processPayload`、`finish` -> `flushAndEmit` -> `Flush/Finish` | terminal Payload、EOF 和错误时的有效 bytes | stream owner 通过 forwarder tests 核查；不修改这些调用者、生命周期或 batching。 |
| `.github/scripts/testdata/cpa-functional-regression_test.go` | producer、native loader、HTTP/WS、registration 和生命周期 | Interactions owner 改 `TestModelMapperFunctionalInteractionsAgent` 及其必需 GJSON import；stream owner 只新增独立 terminal fields 测试函数，不改 imports。 |
| `gCPANewLocalProducer`、`setFixture`、`gCPALoadNative`、`eNativeDrain`、`eResponsesServer`、`eHTTP`、`eWS` | 两个 owner 复用测试能力 | 公共 helpers 不修改；stream 使用已有 `p.wire` custom wire，无需改 producer/imports。每 case 显式设置或清空 wire。 |
| `.github/scripts/smoke-local_test.go` 的 `TestCPAPluginIntegration`／`runFunctionalCPAOverlay` | 唯一 parent-lifetime library 和 overlay 执行入口 | 保持现有入口；终验 owner 验证新增 test 被现有 regex 执行。 |
| `abi_cgo.go`、`handleMethod`、registration/schema、配置／README／Makefile／CI | 公共行为、exports 和发布流程 | 不修改；使用现有测试、build/package 和 workflow 验收。 |

同一个 `.go` 文件中的不同函数不自动保证无冲突。两个 owner 从相同已审查起点开始；stream 不修改 imports、Agent 函数或公共 fixture helpers，Interactions 不修改 scanner 或 stream 测试。若正式测试需要改同一 helper、import block 或 test function，协调者先更新精确所有权，按依赖顺序由单个 owner 完成该位置，不把冲突留给 merge。

## Acceptance Examples

- AE1，Covers R1、R3、R4。两个独立 callbacks `event: error`、`data: {"type":"error","code":"server_error","message":"failed","param":null}` 经 mapped native 输出后形成一个有效 error data event。先前没有 data 时，HTTP 为 direct 的 200 SSE，保留原 code/message；有 created 时，两条有序 data 均保留，不出现插件新增的 premature-terminal error。error-only WS 与 fixed direct 的 1006 边界分别记录。
- AE2，Covers R2、R3、R8。两个 callbacks `event: response.done`、`data: {"type":"response.completed","response":{"model":"upstream","output":[...]}}` 保留原 header 和 type，模型恢复为 client，完整 output／opaque 文本保持。单 terminal 和 created 后 completion 的 HTTP/WS 均正常完成。
- AE3，Covers R3。`response.completed` header 配 `response.created` data 保持原 `mismatch-control` 字节；新 reverse alias、unknown type、nonstring active discriminator 不因本次识别扩展成为新正常事件。闲置 `event_type=17/false` 保持 opaque。
- AE4，Covers R5、R6。`{"agent":" \t　","model":"client"}`、`{"Agent":"research-agent","model":"client"}`、`{"agent":"","agent":"research-agent","model":"client"}` 都按普通 model 执行原映射，上游 model 为 upstream，下游白名单 model 为 client。
- AE5，Covers R5、R6、R7。`{"agent":"research-agent","agent":""}`、`{"agent":"research-agent","AGENT":false}`、`{"agent":"research-agent","Agent":""}` 继续 native unhandled，真实 HTTP 200／完整流，原始请求不被 mapper 改写。trim 后非空 agent 和普通唯一 lowercase agent 仍受保护。
- AE6，Covers R7。invalid JSON 使 helper 返回 false；null+model 按普通 model。nonstring+model 的入口 400 与 direct 相同；agent-only nonstring 只核查 R5 的 CPA 分类，不把本地 fake upstream 的响应写成 Google API 支持证明。
- AE7，Covers R1、R3、R4、R8。非 Grok Codex 依次交付 `event: keepalive`、`data: {"type":"keepalive"}`、`event: response.completed`、完整 completed data，或两个独立 data-only 单位。mapped native 保留两个有效逻辑 data，不合并 JSON；HTTP 200 正常到达完整 completion，WS 获得完整 completion JSON。keepalive 原字节、完成模型恢复与完整 output 一并核查；direct／disabled／unmapped、actual Grok comments、no-keepalive、data-only 普通 completion 保持。RED 按具体入口记录状态、缺失 data 和终止错误，不统一假定失败状态码。

## 永久测试与终验

### Stream 回归

在现有 `stream_native_fields_regression_test.go` 增加独立 error、keepalive、done alias、派发和 forwarder 回归。各条均核查 exact bytes、data event 数量、顺序和完整内容，不能仅检查 model `Contains` 或 emit 次数。

覆盖独立无 LF fields、连续 data-only、标准 LF/CR/CRLF logical events、Flush/Finish、单 terminal、created 后 terminal、keepalive 后 completion、metadata 前置／header与data之间／事件之间、连续 opaque comment、迟到 delimiter、callback/in-band error 和 terminal Payload+Done。原 `Discriminator/mismatch-control`、`DataOnlyTypeControl`、`FormatIsolation`、generic wire、mixed metadata、增量游标、ownership、完整大单位与 incomplete 限额保持。unknown/nonstring/missing type 控制保持，不将非目标 logical event 当作新增边界。

唯一 CPA fixture 新增 `TestModelMapperFunctionalResponsesTerminalFields`，复用真实 XAI/Codex producer 和已有 custom wire。矩阵明确区分 error／done alias／普通 completed／keepalive 后 completion，field-pair／data-only，terminal-only／after-created，whole／bytewise 网络分段，mapped／enabled-unmapped／disabled 两个实际 model 和 no-mapper direct。metadata、actual Grok comments、no-keepalive 控制与原数据、type/header、model、完整 output、原 upstream body/headers/URI/calls 一并核查。error-only WS 是既存限制控制；keepalive+completion 和 F2 WS 必须正常完成。

### Interactions 回归

root tests 逐项覆盖 missing／empty／ASCII和Unicode空白／null、`Agent`／`AGENT`、escaped lowercase key、两个 duplicate 方向、lowercase first agent 后 uppercase empty／null／bool／number／object／array、trim 后非空 native agent、invalid JSON、nonstring `String()` 分类。

扩展原 `TestModelMapperFunctionalInteractionsAgent`，同时用真实 handler、native DLL、GeminiInteractionsExecutor、普通 model executor 检查 model mapping 和 native bypass。其 fake upstream 当前也用 Go struct 读取 Agent/Model，正式测试需改为各 case 的 raw byte 断言和同版 GJSON 分类，避免 test oracle 的 last-wins／大小写规则拒绝正确 native 请求；只改本函数和其必需 import。正常 string native agent 的 stream/nonstream、重复和大小写覆盖必须有 direct／disabled／unmapped 对照；请求字节、upstream identity、完整 response/SSE、调用次数必须保持。helper 正常不等于真实 native HTTP 正常。

### Canonical DSO 与完整旧集合

保持 parent `t.TempDir()` 在 child process 整个生命周期存活、单一 canonical library 文件、实际 SHA256 核查和顺序 enabled owner。每个 owner 仍新建 Host、执行真实 ApplyConfig/init/reconfigure/unload/reload；不缓存 Host，不每个子测试复制新 DSO，不新增共享 `t.Parallel`。

原 24 个 functional tests、2 个 bridge tests 必须逐项执行并通过。四份 capture 保留完整 expected key 集合：`native-binary-captures.json` 1536、`mixed-native-captures.json` 1800、`mixed-binary-captures.json` 1440、`mixed-shape-controls.json` 1680，总数 6456；unknown／duplicate／missing 均为空。新 terminal 测试及新的 Agent 子测试另行列明，不能沿用 24 作为扩大后的全部顶层数。

正式证据记录源 BASE／起点／最终 HEAD、测试源码和 overlay SHA256、CPA revision/module Sum、实际命令和数字 exit、DLL／shadow DLL identity、原始 upstream/client payload。Go race 与正常 native 加载分层报告；尚未执行、环境拒绝和产品 RED 分开记录。缺日志时写明缺失，不以旧诊断输出代替正式 RED/GREEN。

## 发布与完成条件

两个 coding owner 全部完成 R11 后，终验 owner在新的独立 worktree 验收 R12、R13；规格／质量问题在对应 owner 内修正并复审。源码再次改变后，重跑受影响检查和完整整合验收。

本地 build/package 核查注入版本、注册 metadata、ABI 和 ZIP/checksum/根 LICENSE；兼容性维持 Linux GLIBC <=2.17、macOS deployment target 12.0。保留七个平台：linux/amd64、linux/arm64、darwin/amd64、darwin/arm64、windows/amd64、windows/arm64、freebsd/amd64。

最终交付须满足 R14：本地 main、remote main、tag、Release 和 CI 的最终 SHA 一致；七平台 CI 成功；下载全部资产并用可靠 ZIP/parser、checksum 和真实注册 Version 核查。只构建或只发布成功不等于资产内容验收完成。发布失败时按实际状态报告，不移动既有 tag、force push 或删除用户已有文件。

本次文档任务的完成条件是只提交本规格和对应详细计划，验证 frontmatter、真实源码位置、现存测试名称、计划新增测试声明、需求／验收对应与无冲突所有权。产品代码、tests、CPA、配置、其他 worktree 和发布操作均未执行。
