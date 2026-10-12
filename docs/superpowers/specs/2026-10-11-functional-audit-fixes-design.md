---
title: 功能审查修复规格
type: fix
date: 2026-10-11
baseline: 7f5e50aa444d31298e3086b0435552bc0ebb9747
---

# 功能审查修复规格

## 目标和范围

修复 mapped Responses 对非 `response.*` 逻辑事件（error、keepalive）及已验证完整 data-only 值后完整 metadata 单位的边界识别、Codex 完成别名和 Interactions agent 分类三个已确认根因，并将生产代码按职责组织为10份同 package 文件。客户端继续获得 Client-Requested Model、完整内容和固定 CPA 原有的错误或完成结果；全部既有回归，包括 issue8，保持有效。

行为修复范围为 F1-F3。两项修复完成、审查和提交后再执行纯声明移动，不同时重写函数实现。全部候选判定、二次覆盖审查和修复前本规格／实施计划独立审查门槛已满足，U2/U3 已完成并合并，U6 已完成声明移动及前后运行；当前待补齐 U6 的实际 native overlay child shadow DLL 身份证据，再经独立复核后合并。本文是实施依据，不能替代正式 TDD、真实 native integration 或发布验收。

对应计划为 `docs/superpowers/plans/2026-10-11-functional-audit-fixes-implementation.md`。两份文档中的外向操作须由协调者核对真实用户消息授权；computed task、代理通知和旧文档均不构成授权。

## 基线与证据

- 插件 BASE 保持 `7f5e50aa444d31298e3086b0435552bc0ebb9747`。此前 metadata 边界文档轮次初始 HEAD 为 `86a3c2ceb8d5e123015d563c5ab07eefb2670149`；本轮文档 worktree 初始 HEAD 为 `1bb1e3d54e8cc73f8d8f9eab24fe14940f2c4915`，初始工作区干净。本轮只更新 spec/plan 的执行事实和补充验收安排，没有实施或运行产品测试。
- `go.mod` 为 Go `1.26.0`、CPA `v7.2.152`、`gopkg.in/yaml.v3 v3.0.1`。固定 CPA revision 为 `c76dfd4e0edabab9000628b1560ab8ab379eadb8`，module Sum 为 `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`。
- U2 已在根模块直接 require/import `github.com/tidwall/gjson v1.18.0` 并补齐必要 `go.sum` 条目，固定 CPA 使用同版本。修复前根模块尚无该直接依赖的记录仅描述历史起点。
- F1/F2 的实际复现、固定 CPA 可达性、规格与回归反证分别来自已完成 workflow `wf_0e54821b-a4c` 的 result 第 4、5、6 项。三份报告确认相同根因，包含重复 RED、direct／disabled／unmapped 控制和 fixed binary HTTP 证据。
- 上述报告引用的三个只读诊断目录已随 clean worktree 清理。本文核对了调度器保留的完整完成输出，没有把已不存在的原始日志当作已读取证据。正式 owner 必须保留新的可复查 RED/GREEN 和产物身份。
- F3 的三位独立验证结果已保存在 completed workflow `wf_893a5d73-2a2` 的 result 第 1、2、3 项，持久 JSON 和完成输出均已核查。报告包含真实 native/HTTP 重复 RED，以及 private overlay 的根检查和 192 个 native HTTP 对照；原诊断 worktree 已被清理，原日志缺失，不能声称已归档。private overlay 属于诊断，不充当正式 TDD；U2 当前完成状态见下文。
- keepalive 的原版 producer/native/HTTP/WS 失败已重复实际复现。旧安全回归结论绑定协调者已核验的 `a2c59708bf099984d` 完成归档：精确三处 guard 的诊断副本通过其完整 root、DLL build、native/HTTP/WS 最小与 opaque 控制，pair-only 副本仍在 data-only/mixed 失败。归档 `keepalive-lens-spec-boundary-wf_dc32a367-b38-3.zip` 的 SHA256 为 `0ac53b72c705ec10f1b445807fef27c74af53b1a2f8c5000257a6586c8a4ee36`。其中 actual Grok 成功 cases 只有完整 comment(s) -> pair 或 data-only completed，没有前置 data-only created；旧 root after-created 使用 pair-created 和 JSON keepalive。direct／disabled／unmapped、no-keepalive 和普通 completion 的成功在该实际矩阵内成立，不能推断全部 Grok 排列正常或三谓词已满足全部控制。二次扫描覆盖门槛已由协调者确认。原文档提交 `93d99213bb6a8fe45af2770d92faf95ea1f4e2a9` 的五位独立 reviewer 均返回 `findings=[]`；该结果不覆盖随后新增的完整 metadata 边界事实和技术范围；修复前更新后的 spec/plan 已完成独立审查，U3 已正式完成。private overlay 不作为正式产品完成证据。
- 新 workflow `wf_31e9e13c-e72` 的 runtime、oracle-adversarial、boundary-history 三项独立诊断均已完成。协调者的只读记录为协调 worktree 中的 `dist/functional-audit-fixes-20261011/coordination/U3-Grok-synthesis-wf_31e9e13c-e72.json`；其证据目录 `dist/functional-audit-fixes-20261011/U3-independent-confirmed-wf_31e9e13c-e72/` 保存三个 ZIP、receipts 和 `manifest-verification.json`，协调者已逐项核对全部 length／SHA256／CRC。此前 metadata 边界文档轮次核对记录、完整 workflow 输出和 ZIP 内必要原始证据，未执行归档脚本或重新运行产品。
- 三个新 ZIP 的 SHA256 分别为：`runtime.zip`，`de5e18bb7f9e5d8eaa8e5fde44a579c767315d13f04aaf942396768a0f6f7598`；`oracle-adversarial.zip`，`04d4b2acefac10973cb682d86908c5d93017a6a5d0f9072d340b273a0b5c9396`；`boundary-history.zip`，`17fd96f0161bec2f057a6f6be1d8d4eea0d2460e0aebbfe781b0201e3037acfa`。
- 新真实 fixed CPA Codex Grok client context 交付三个非空 callbacks：无 LF data-only response.created -> 完整 `: keepalive\n\n` -> 无 LF data-only response.completed。冻结 BASE 和精确三谓词版本的新 build／ABI／native／HTTP／WS 重复 RED；第一次 comment callback emit 已将 created JSON 与 comment 合并，created.model 保留 Upstream Model。native 后续 completion 本身完整且 model 恢复；mapped HTTP 为 502 `invalid SSE data JSON`，mapped WS 没有取得 created/completed 数据并 close 1006。direct／disabled／enabled-unmapped 的两个 model 正常，whole／bytewise upstream 写入产生相同非空 callbacks。另一独立 oracle 按事件数量、合法 JSON、status/model 和完整 output 复现，未依赖 nil/empty 或 native 与 HTTP delimiter equality。
- 新确认依据冻结 Git source、精确诊断副本、有效 overlay、实际 build 和 built／canonical／shadow DLL 链。BASE `main.go` 的 Git 内容是 LF，原始 SHA256 为 `207f4290f3ca0308d562acc08d2e6aedd731df32a9ac36c5b89e8b539fe13a42`；同内容 CRLF 副本为 `6981b88615c132c87f6614d0042a327b9a8eb4df6e553aea0e2ff387db5bc2a2`。runtime 的 build cwd HEAD 为 `86a3c2ceb8d5e123015d563c5ab07eefb2670149`，实际 BASE 编译输入来自固定 BASE 对象，三谓词输入来自固定 START 的限定替换；build cwd HEAD、buildinfo VCS 字段与 production source 身份分别记录。旧 BASE runtime metadata 采样当时 current source hash，不能独自证明 BASE 编译来源；文件名、Version 或相同 DLL hash 也不能证明 source 归属。
- Grok 边界新证据推翻 KTD1/U3 仅三谓词足以满足全部真实控制的技术判断；F1-F3 产品目标和全部保护边界不变。用户原有全面修复、先 spec/plan、TDD 和全程自主执行要求覆盖该技术修正；代理通知不构成新批准。当时文档提交及独立审查已完成，U3 已正式完成该修正；原产品写入暂停记录保留为历史门槛。
- U2 完整提交为 `7f17c7db69d318d84e68a0b5fcd8e0e616bd44f1`，两位 fresh reviewer 均返回 `findings=[]`，协调者已核对完整 diff 和六文件范围；fresh U3 完整提交为 `06a2a3d7d902aaa8c6d65cf1b1aebf776340c4d4`。两任务已完成正式 TDD、全部 owner 验收及审查，并无冲突合并为 `1bb1e3d54e8cc73f8d8f9eab24fe14940f2c4915`。U3 历史三文件未提交来源 `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/wf_43831682-e1f-2` 及其报告 HEAD `86a3c2ceb8d5e123015d563c5ab07eefb2670149` 保持永久只读，旧 owner 不恢复；不重新分派或重做 U2/U3。
- U6 原 owner 已从上述整合 HEAD 连续完成 post-fix 全部运行、10文件纯声明移动、post-module 全部运行、自审修正复审和正常提交 `e5b4ab7c1e2111ae6fe888183f8089322a3065b2`，tree 为 `79ba809f4c48cc68b62336033c88013930e736d8`，尚未合并。静态独立复核为 `complete`、`findings=[]`；动态独立复核因两个阶段实际 native overlay child 的 shadow DLL 身份记录缺口为 `incomplete`。其余完整运行、集合、声明等价和保护字节结果保留；该缺口不证明产品回归。
- 有限事实依据为协调记录 `dist/functional-audit-fixes-20261011/coordination/U6-shadow-evidence-execution-plan-20261012.json`、完成输出 `wkjjhk597.output`、入口受限输出 `wtn10eg8e.output` 及原 U6 `tools/coverage.py:149-199` 的只读内容。动态独立复核记录确认 post-fix/post-module 的 `cpa-overlay.log` 各有341条 canonical SHA256 记录，均缺少 actual loaded-shadow path/PID/hash；本轮读取复核输出，没有重新解析两份原始 native 日志。每阶段 `integration.log` 的6条 shadow 来自 `functionalCPAProcess` 启动的另外进程；collector 第173-174行接受 canonical OR shadow，不能证明 actual child 身份已验收。原 U6 树及证据保持只读，不执行或改写来源脚本。
- 原 U6 owner `ab17366f5ece24ece` 的恢复请求返回 `No transcript found`，未启动恢复；`wf_1cdc9c16-6ef` 的 `EnterWorktree path` 被拒绝，未切换，未执行 Git/native/写入，exit 未知。禁止任何工具或 peer 再次进入或写入原 U6 树，不修改权限。新的唯一补充验收负责人只在自己的新独立树核查本地已有 `1bb1e3d`／`e5b4ab7` 对象、读取获准旧证据，并连续完成两个阶段完整 native overlay 补跑、自检、自审修正复审和交接；这项独立证据补充不称为原 owner 恢复或原树续跑，也不降低 R11。
- 补跑优先使用原冻结默认 DLL：post-fix SHA256 为 `68f01a7d15255f584035f4c446c7d1c5f561aee5304206a6674db9c43eb7183b`，post-module 为 `e07a54f4d2bdff0654fd54eb00a06836f4d49ffb4441cb36620c253a28accd2b`。获准读取能力无法取得时，记录事实，在自有树冻结相同源码后重建，分别记录新旧身份，不能称为原 DLL 或 U4 final 产物。私有 observation overlay 只增加实际 child PID、加载成功的 shadow path／file SHA256，绑定 stage source/built/canonical/CPA/overlay；严格 collector 必须拒绝原 canonical-only 日志。新补跑按真实时间另存，不伪造旧运行已有记录；父 shadow、Version、static loader 或假数据不能替代。完整补充及 fresh 只读独立证据复核全部通过后才合并 U6 并启动 U4；U4/final/package/U5 仍未开始。
- 此前规划已核查当时产品、唯一永久 CPA fixture、固定 CPA 的相关 parser／producer／translator 源码，以及 `CONCEPTS.md`、`docs/solutions/workflow-issues/model-mapper-release-automation-alignment.md`。旧功能修复和 canonical library 规格用于保持现有边界，不沿用其历史未完成状态或候选版本。
- 此前文档扩展全文读取已完成任务 `wa2mmgu21.output` 的三份模块设计、`wttct1sv9.output` 的 issue8／完整回归报告和 `wa7ayrae9.output` 的原文档及五份独立审查结果。模块方案采用第三份报告的10文件划分，并用另两份报告核对初始化、CGo 和构建边界。调查 worktree 的中间清单可能已清理；U6 已从实际 post-fix HEAD 重新取得 AST，没有依赖旧行号或缺失文件。
- 此前文档扩展从 clean BASE 通过 fast-forward 接入完整文档提交链，并核查 `main.go`、`abi_cgo.go` 全文、Makefile／workflow 和 canonical lifetime spec／plan。此前文档轮次未运行产品 tests/build；本轮在已合并修复 HEAD 更新两文件，限定核对本地提交对象、必要源码及上述 U6 输出，不重做已完成产品检查，不重写旧提交。
- issue8 四项产品修复 `4bc47fe0c3718a2194658c90e28775be284336b9`、`e9c005b4000628a62da0897f87869a85b5d48031`、`8fe51c134e327a21e56e4fb3205d0cdf50e0847a`、`cf39f6e5575a6a5d8de36e707687399eeadbae68` 均已用 Git 核对为 BASE ancestors；本地 `v0.5.13` tag 指向 BASE。完成报告确认它是首次正式发布这些修复的 Release。原 terminal-order 提交 `8b7bd9a0d135251878792b3740bc06a7da7ede00` 早于 issue8，单列为历史控制。

| 编号 | 已观察结果 | 根因和实际入口 | 本次修复归属 |
| --- | --- | --- | --- |
| F1，非 `response.*` 逻辑事件及完整 metadata 边界 | XAI 单 error 的 mapped HTTP 返回 502 generic error；先有 created 时原 `server_error`／`failed` 被 premature-terminal error 替换。非 Grok Codex keepalive pair／data-only 与后续 completion 合并。真实 Grok data-only created 后完整 comment 在 BASE 和三谓词版本均与 created JSON 合并，created.model 未恢复；native 后续 completion 完整且 model 恢复，mapped HTTP 502 invalid SSE JSON，mapped WS 无数据并 close 1006。 | 固定 producer 交付独立无 LF fields，也交付完整带 LF comment。原三处识别拒绝 error／keepalive；`sseRewriter.Write` 的 metadata 分支要求无 CR/LF，完整 comment 被追加到已验证 pending data-only 值，随后标准 delimiter 消费合并值。 | 保留三谓词识别修复；U3 另独占 shared Write 中已验证完整 data-only 前值与完整带 line-ending metadata 单位的边界及有序派发。该 metadata 修复已由 U3 正式完成并合并，历史失败记录保持。 |
| F2 | Codex mapped native 拼接 fields、未恢复 `response.model`；单 terminal HTTP 502，created 后丢失完成数据，mapped WS close 1006。disabled／unmapped 的 HTTP 和 WS 正常完成。 | 固定 Codex producer 保留 `event: response.done`，将 data 正常化为 `type: response.completed`；scanner 的相等判定拒绝这一实际 alias。 | 共享 discriminator 的单向精确 alias。 |
| F3 | 空白 agent、未知大小写 key 或首个空 agent 后非空 agent 使普通 model 跳过映射；首个真实 agent 后空值或大小写覆盖导致错误 self route，真实 HTTP 400。 | `interactionsUsesAgent` 的 Go struct `Unmarshal` 大小写不敏感、重复 key last-wins、未 trim；CPA 以精确 decoded lowercase `agent`、GJSON first-wins、`String()` 和 `TrimSpace` 判定。 | 唯一 helper 复用 CPA 同版 GJSON。 |

error-only direct WS 本来就 close 1006，当前固定 CPA 不返回正常 error JSON，该 error 场景的确认依据是 native 和 HTTP；本次不承诺新增 WS error 支持。keepalive 后 completion 的 native、HTTP 与 WS 失败均已确认，修复后须正常完成。诊断只替换外部模型服务，实际 CPA producer、translator、loader、ABI、validator、HTTP/WS consumer 均运行；不宣称已验证公网 Google、XAI 或 Codex 服务。

## Requirements

### Responses 字段与完成结果

- R1. 固定 Responses output units 的 `error` 和 `keepalive` 完整逻辑单位必须恢复字段和事件边界，覆盖独立 event/data pair、连续 data-only，以及已验证完整 data-only created -> 完整 `: keepalive\n\n` -> data-only completed。该 metadata 边界须分别输出合法 created、原 comment 和合法 completed，保持顺序、次数和完整 output，两个响应的白名单 model 均恢复为 Client-Requested Model。error 保留原 `code`、`message`、`param` 和其他 JSON 字节，单 error 与 created 后 error 的 HTTP 保持 direct 原错误；keepalive 后的 completion 在 mapped HTTP/WS 正常到达，不生成缺失或非法 data 引起的 generic/premature-terminal error。
- R2. 接受 `event: response.done`／`type: response.completed` 这一方向的实际 alias，保留原 header 和 data.type。mapped HTTP 和 WS 必须获得完整 completed output，现有白名单内的 model 恢复为 Client-Requested Model。
- R3. 新增 allowlist 仅为精确 `error`、`keepalive`，event/data pair 仍要求同名 type，F2 仅允许 R2 的方向；data-only 仍必须是完整合法且 type 被认可的 JSON。保留原 exact-match 和真正 mismatch，不接受任意 unknown event/type 或反方向 alias，不改写 discriminator、opaque metadata、unknown fields 或工具文本。event/type 配对要求只约束无标准 delimiter 的 output-unit 边界；generic 标准 SSE 的 error／keepalive 和 header/type mismatch 继续按既有白名单恢复模型。
- R4. 允许 R1 新确认的完整带 line-ending metadata 单位为已验证完整 data-only 前值确认边界，并在该 Write 中先输出前值，再按原字节输出完整 comment。其余派发策略保持：普通完整 data-only 值不普遍提前派发，无 LF ordinary/mixed metadata callbacks 期间的原 zero-output 时点不变。generic SSE wire 的任意网络分段、LF／CR／CRLF、BOM、合法分片、迟到 delimiter 和 partial-metadata-characterization 原断言及 metadata 字节保持；HTTP 网络分段经过真实 producer Scanner，不能直接当作 plugin callback 的字段边界。

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

### 生产代码模块化与完整回归

- R15. 生产代码采用下文的10文件布局，保持同一个 `package main`、Go module 和现有构建入口。只移动完整声明、分配各文件 imports、作必要排版并拆分原混合 var group；不新增 package、interface、wrapper、配置、build tag 或平台文件名后缀。
- R16. 模块化相对已合并且 GREEN 的 F1-F3 源码保持全部声明、receiver、signature、body、type fields/tags、initializer、const block、directive、共享状态和 ABI 等价。`abi_cgo.go`、tests、benchmark 和 fixture 相对模块化起点逐字不变；纯移动采用前后验收，不制造虚假 RED。当前架构说明只精确更新 `CLAUDE.md` 和 README 的必要源码布局。
- R17. 完整保留 BASE 的323个 root Tests、60个显式 scripts Tests、24个 CPA fixture Tests及29个 Benchmark声明，并保留全部展开的 subtests/table cases、expected keys、断言、数据规模和内部时限。唯一已确认的旧 oracle 纠正是 whitespace agent 的 `Handled=false` 改为 true；Agent fixture 的必需 oracle 修改只限原 Agent 函数及同版 GJSON import。新增 cases 单列，不删除、skip、放宽或替换原控制。新增回归以现有输出行为、model_route RPC、forwarder 和真实 producer/native/HTTP/WS 入口为测试边界，不新增依赖 helper 实现或私有 scanner 状态的断言。
- R18. post-fix、post-modularization 和 final 三阶段分别取得完整本地验收及源码／产物身份，使用默认 `0.0.0-dev` 动态库执行真实 integration；候选版本 build/package 和真实 ABI registration Version 使用独立产物。最终 SHA 的 Ubuntu `make integration` 和七平台 CI／Release 必须实际成功，平台构建与实际 runtime 验证分别报告。

## 设计

### Shared Responses scanner

修改点限定在 `main.go` 的 `sseRewriter.Write` 和 `findDelimiterlessResponsesEventEnd`，BASE 约为 198-778 行。U3 保留原三谓词修复，另独占 Write 内已验证完整 data-only 前值与完整带 line-ending metadata 单位的边界及顺序派发。复用现有 scanner 状态、增量游标、`json.Unmarshal`、completeEnd 验证、`drain`、`rewritePendingEvent` 和白名单恢复器。

三个谓词分别处理无 LF native header 的记录、有真实 line ending 的 header 验证、无 header 的 data-only type 验证。三处沿用 `response.*` 并加入精确 `error`／`keepalive`；新增 data-only 许可须由 `s.dataStart == s.headerStart` 确认，不能只凭 `s.eventType == ""` 放行被清空 discriminator 的 opaque header。完整 JSON 验证后，pair 仍匹配同名 type，R2 的单向 alias 参与原 matched 判定。三谓词覆盖识别问题，尚不能处理新确认的完整 comment 边界。

Write 必须在该边界用现有路径派发已验证前值，再处理完整 metadata，不能将完整 comment 拼入前值 JSON。无 LF metadata 仍按原策略留给下一事件，普通完整 data-only 值仍等待原边界或 EOF；generic 分片和 partial metadata 不能被当作已确认完整单位。该 metadata patch 已完成正式 TDD 和 owner 验收并合并；单独删除 `bytes.IndexAny` 的换行 guard 不构成充分修复这一技术边界保持。允许范围不扩展到 scanner 算法、JSON 恢复器、生命周期、batching 或公共 helpers；实现若证明还需扩大范围，保留证据并交协调者更新文档和依赖后再继续。

error／keepalive body 没有 model marker 时仍需正确识别边界。补写 LF 和派发 blank delimiter 复用当前机制，保留原 event text 和 JSON 字节；data-only keepalive 与 completion 不得合成一个 data JSON。新 Grok created/comment/completed 按 R4 在完整 comment callback 有序输出前值及 comment，comment 原文、两个合法 data 的次数、model 白名单和完整 output 保持。其余普通 completed、Responses/Interactions logical events、其他 formats 和原 metadata 派发控制不变；完整 prefix+error、16MiB、ownership、allocation、增量游标、Flush/Finish 和关闭顺序保持。

F1/F2 共用 scanner、回归文件和 fixture，因此由同一个 stream owner 连续实现和审查。没有 provider-specific 产品分支、HTTP/WS workaround、配置、mode、ABI flag 或第二 parser。

### Shared Interactions helper

最小完整修改是将 `interactionsUsesAgent` 的 Go struct 判定替换为同版 GJSON 的 `ValidBytes`、`GetBytes(body, "agent").String()` 和 `strings.TrimSpace` 判定。GJSON 直接 import、直接 require；`go.sum` 由正常 Go module 流程生成，并核对没有无关依赖升级。现有 `strings` import 可以复用。

CPA handler 的 `parseInteractionsRequestTarget` 先验证 JSON，再读取精确的 model/agent；产品 helper 只负责是否应绕过 mapper。不要复制 CPA 的整个请求验证器。当前只有 `handleModelRoute` 调用该 helper；`handleExecutorExecute` 和 `prepareExecutorStream` 通过路由结果间接受影响，不增加第二份 agent 判断。

正式 RED 阶段将 `TestHandleModelRouteLeavesInteractionsAgentsNative` 的 `whitespace agent` 预期改成 `Handled=true`，保持原输入和其他断言。新增独立 root regression 覆盖 R5-R7，不将错误的旧预期保留到 GREEN 阶段。

### 生产模块和完整声明归属

R15 采用10文件布局，包含现有 `main.go`／`abi_cgo.go` 和8份新文件。下面清单绑定 BASE 的188个命名声明（main179、ABI9）；169个非 import 顶层 declarations 和174条另一分组记录属于不同统计单位，不混用、不固定最终函数数量。U6 从实际 post-fix HEAD 重新清点全部声明、唯一分配新增 helper 并核验 R16，不能按 BASE 行号复制旧实现。

| 文件 | 职责 | BASE 命名声明数 |
| --- | --- | ---: |
| `main.go` | entry、pluginVersion、registration、RPC、host callback、structured error/envelope | 25 |
| `abi_cgo.go` | 原 CGo ABI、C preamble、exports、长度校验和 buffer ownership | 9 |
| `config.go` | Config、JSON/YAML lifecycle 解码、编译、配置快照／发布 | 17 |
| `model_router.go` | rule selection、model route、F3 helper、authenticated caller／cache | 20 |
| `rules.go` | DSL token/rule、解析、ASCII case、caller 条件和顺序 replacement | 18 |
| `executor.go` | 共用 executor request/payload、headers、非流执行 | 5 |
| `executor_stream.go` | setup/admission、worker、forwarding、terminal／close／shutdown | 24 |
| `json_model.go` | 请求顶层 model、响应白名单、JSON byte edits、marker 和现有 JSON helpers | 21 |
| `sse_rewriter.go` | SSE 状态、BOM、metadata、delimiterless scanner、data 恢复／framing | 30 |
| `stream_rewriter.go` | chunk 分类、raw JSON scanner、pending、array/value、Flush/Finish | 19 |

完整 BASE 声明归属如下，方法按 receiver 区分，现有 `ForTest` helpers 和 wrappers 均保留。

- `main.go`：const `pluginRPCSchemaVersion`；types `registration`、`registrationCapabilities`、`hostCallback`、`hostCaller`、`structuredPluginError`；vars `pluginVersion`、`hostAPIMu`、`hostCallbackFn`；functions `main`、`pluginRegistration`、`handlePluginRegister`、`handlePluginReconfigure`、`handleExecutorIdentifier`、`newStructuredPluginError`、`structuredError`、`wrapEnvelope`、`marshalErrorEnvelope`、`errorEnvelope`、`handleMethod`、`callHost`、`setHostCallbackForTest`、`setHostCallback`；`*structuredPluginError` methods `Error`、`Unwrap`。
- `abi_cgo.go`：const block `maxCIntLength`、`maxPluginResponseBytes`；functions `cIntLength`、`copyPluginRequest`、`pluginResponseLength`、`cliproxy_plugin_init`、`cliproxyPluginCall`、`cliproxyPluginFree`、`cliproxyPluginShutdown`。整个文件逐字保留，包括8个 C typedef、3个前向声明、3个 static helpers、C includes、`import "C"`、`//go:build cgo`、四个 `//export` 及字段顺序／casts。
- `config.go`：完整 const block `rulesStackModeOff`、`rulesStackModeSpecificFirst`、`rulesStackModeGlobalFirst`；types `Config`、`lifecycleYAMLConfig`；vars `loadedConfigMu`、`loadedCfg`；functions `decodeConfig`、`compileConfig`、`compileRuleSet`、`loadedConfig`、`publishLoadedConfig`、`setLoadedConfigForTest`、`applyLifecycleConfig`、`yamlStringConfigField`、`decodeLifecycleConfig`、`defaultConfig`。
- `model_router.go`：const `callerPatternCacheGenerationSize`，完整 const block `callerScopeMetadataKey`、`callerScopeDomain`；types `callerPatternCacheKey`、`callerPatternCacheState`、`routeDecision`、`ruleSelection`、`modelRouteRPCRequest`；vars `callerPatternCacheMu`、`callerPatternCache`；functions `resetCallerPatternCache`、`selectRules`、`callerAPIKeyForSelectedRules`、`interactionsUsesAgent`、`handleModelRoute`、`routeModel`、`callerScope`、`callerScopeFromMetadata`、`callerAPIKey`、`callerPatternMatch`。F3 的最终 helper 和 GJSON import 一起移动。
- `rules.go`：types `token`、`caseOperation`、`rule`；完整 iota block `caseOperationNone`、`caseOperationLower`、`caseOperationUpper`；functions `parseRules`、`splitRuleEntries`、`hasUnescapedCommentMarker`、`findRuleSeparator`、`splitEscaped`、`parseFind`、`parseReplace`、`applyASCIIModelCase`、`callerMatchesRule`、`applyRules`、`matchTokens`、`buildReplacement`。
- `executor.go`：types `executorRPCRequest`、`hostModelExecutePayload`；functions `canonicalizeHeaders`、`removeChangedBodyHeaders`、`handleExecutorExecute`。
- `executor_stream.go`：type `executorStream`；vars `errExecutorStreamShutdown`、`executorStreamLifecycle`；functions `isEventStreamContentType`、`releaseExecutorStreamSetup`、`emitRewritten`、`handleExecutorExecuteStream`、`resetExecutorStreamLifecycle`、`beginExecutorStreamPreparation`、`finishExecutorStreamPreparation`、`admitExecutorStream`、`unregisterExecutorStream`、`shutdownExecutorStreams`、`prepareExecutorStream`、`startExecutorStream`、`joinStreamErrors`、`runStreamForward`；`*executorStream` methods `closeHost`、`closePlugin`、`emit`、`processPayload`、`flushAndEmit`、`selectTerminal`、`finish`。
- `json_model.go`：types `jsonByteEdit`、`responseModelMarkerScanner`；functions `rewriteRequestModel`、`restoreResponseModel`、`rewriteTopLevelModel`、`applyJSONByteEdits`、`rewriteTopLevelModelCanonical`、`findTopLevelModelValue`、`topLevelModelKey`、`skipTopLevelModelJSONSpace`、`skipTopLevelModelJSONString`、`skipTopLevelModelJSONValue`、`hexNibble`、`escapedResponseModelKey`、`mightContainResponseModelField`、`rewriteResponseModelFields`、`rewriteResponseModelFieldsWithReplacement`、`rewriteResponseModelFieldsWithReplacementChecked`、`collectResponseModelEdits`、`responseModelStringEqual`；`*responseModelMarkerScanner` method `feed`。
- `sse_rewriter.go`：types `sseRewriter`、`delimiterlessResponsesEventScanner`；var `utf8SSEBOM`；functions `newSSERewriter`、`sseFieldValue`、`splitSSELine`、`hasSSEDataField`、`appendUnchangedSSEEvent`、`sseLineEnding`、`findSSEEventDelimiter`、`isResponsesMetadataField`、`sseEventDelimiter`、`isColonlessSSEChunk`、`completeSSEEvents`、`sseContainsEscapedModelField`、`hasSSEDoneEvent`、`frameSSEData`、`frameSSEEvent`；`*sseRewriter` methods `resetDelimiterlessResponsesEventScanner`、`consumeLeadingBOM`、`flushLeadingBOM`、`restoreResponseModelCandidate`、`restoreResponseModel`、`Write`、`Flush`、`drain`、`rewritePendingEvent`、`rewriteMultiDataEvent`、`rewriteEvent`、`findDelimiterlessResponsesEventEnd`。F1/F2 实际新增的 SSE 识别声明也归此文件。
- `stream_rewriter.go`：const `maxPendingStreamBytes`；types `streamChunkRewriter`、`rawJSONScan`；functions `newStreamChunkRewriter`、`splitJSONValues`、`hasSSEFieldPrefix`、`couldStartJSONValue`、`isSSEChunk`、`isIncompleteSSEPrefix`；`*rawJSONScan` method `advance`；`*streamChunkRewriter` methods `retainPending`、`Write`、`writeRawJSONArray`、`writeSSESuffix`、`rawJSONChunks`、`tryRawJSONChunks`、`Flush`、`Finish`、`frameRawJSON`。

保留 aliases `pluginabi`／`pluginapi` 和 paths `github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi`／`github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi`。各文件 imports 按下表分配，再由 U6 核对修复后新增声明的实际使用。

| 文件 | imports |
| --- | --- |
| `main.go` | `encoding/json`、`errors`、`fmt`、`sync`、`pluginabi`、`pluginapi` |
| `config.go` | `bytes`、`encoding/base64`、`encoding/json`、`fmt`、`io`、`strings`、`sync`、`gopkg.in/yaml.v3` |
| `model_router.go` | `crypto/sha256`、`encoding/hex`、`encoding/json`、`net/http`、`net/url`、`strings`、`sync`、`pluginapi`、F3 的 `github.com/tidwall/gjson` |
| `rules.go` | `fmt`、`strconv`、`strings`、`unicode` |
| `executor.go` | `encoding/json`、`fmt`、`net/http`、`net/url`、`pluginabi`、`pluginapi` |
| `executor_stream.go` | `encoding/json`、`errors`、`fmt`、`mime`、`net/http`、`strings`、`sync`、`pluginabi`、`pluginapi` |
| `json_model.go` | `bytes`、`encoding/json`、`io`、`unicode/utf8` |
| `sse_rewriter.go` | `bytes`、`encoding/json`、`fmt`、`strings`、`unicode` |
| `stream_rewriter.go` | `bytes`、`encoding/json`、`fmt`、`io` |
| `abi_cgo.go` | 原 `C`、`fmt`、`unsafe`、`pluginabi` 原文保持 |

同 package 符号继续直接调用。ABI -> RPC／caller reset／stream lifecycle；config -> DSL；rules -> caller pattern；route -> config／rules／身份绑定／共用 headers；两种 executor -> route／JSON／RPC，stream executor 再调用 chunk rewriter -> SSE rewriter -> JSON 白名单。`joinStreamErrors` 继续供 stream executor 和 chunk Flush 共用，`maxPendingStreamBytes` 保持单一定义。

### 初始化与纯移动核验

BASE 没有 `init()`。十个 package vars 的类型和初始化保持：main 的 `pluginVersion = "0.0.0-dev"` 及零值 `hostAPIMu`／`hostCallbackFn`；config 的零值 `loadedConfigMu` 和 `loadedCfg = defaultConfig()`；router 的零值 `callerPatternCacheMu` 和原 caller map initializer；SSE 的固定 `utf8SSEBOM` byte array；stream executor 的固定 `errors.New(...)` 和原 anonymous struct／active map initializer。`defaultConfig()` 只构造 Config 并读取 stacking 常量，当前其他初始化不依赖其他可变 globals。

原混合 var group 只按独立 ValueSpec 分配，caller cache 注释跟随声明。完整 const blocks 保留分组、顺序、隐式表达式和类型继承，特别是 iota。mutex、WaitGroup、`sync.Once` 和全部 package globals 保持唯一；不增加初始化函数或状态实例。

R16 保留 ABI init 的 lifecycle reset -> caller reset -> 当前 Host callback 绑定 -> plugin API 设置顺序；shutdown 仍等待 preparing／active／workers，再 reset cache、清空 callback。保留 identifier、signature、tags、validation、RPC／schema／metadata、16MiB、ownership、白名单、headers、dispatch、错误及关闭顺序。

U6 用标准 `go/parser`／`go/ast` 比较全部非 import 声明、receiver/signature/body/type/tag/initializer/hash、完整 const block 和 directive；只允许文件归属、imports、必要排版及指定 var 分组差异。另逐字比较 ABI、全部 tests／benchmark／fixture、module/dependencies、Makefile／workflow，核查七平台 `go/build.MatchFile` 文件选择和真实构建。AST 等价不能替代动态验收。Task I 的产品字节等于旧 BASE 条件属于当时 fixture-only 任务；本次比较修复后起点。

Makefile、Windows arm64 和 FreeBSD CI 均以 package `.` 构建，新文件自动参与。`-X main.pluginVersion` 保持；新普通 Go 文件不增加 build tags 或 GOOS/GOARCH suffix，原 cgo constraint 只留在 ABI。module owner 精确更新 `CLAUDE.md` 当前架构说明，README 只加必要简短源码布局；历史 docs 和 CONCEPTS 不追改。新注释中文，已有无关注释保持。

### 修改触达与不需修改的边界

| 位置 | 直接或间接触达 | 所有权／是否修改 |
| --- | --- | --- |
| `main.go` imports、`interactionsUsesAgent`（1716-1721）、`handleModelRoute` caller（1758） | native guard -> route -> 两种 executor | Interactions owner 改 imports/helper；caller 和 executor 实现保持。 |
| `main_test.go` 的 `TestHandleModelRouteLeavesInteractionsAgentsNative`；新增 `interactions_agent_regression_test.go` | helper、route 的 exact-key／first-wins／trim 回归 | Interactions owner。 |
| `go.mod`、`go.sum` | 必需 GJSON 依赖 | Interactions owner，直接 require v1.18.0。 |
| `main.go` 的 shared scanner/Write；`stream_native_fields_regression_test.go` | `streamChunkRewriter.Write` -> `sseRewriter.Write/drain` -> `rewritePendingEvent/rewriteEvent` | U3 独占三谓词、单向 alias 及 Write 中完整 data-only 前值／完整带 line-ending metadata 的边界和有序派发；永久 root exact-byte/per-Write/Finish/error 回归，复用下游恢复器，不修改 scanner 算法。 |
| `runStreamForward` -> `processPayload`、`finish` -> `flushAndEmit` -> `Flush/Finish` | terminal Payload、EOF 和错误时的有效 bytes | stream owner 通过 forwarder tests 核查；不修改这些调用者、生命周期或 batching。 |
| `.github/scripts/testdata/cpa-functional-regression_test.go` | producer、native loader、HTTP/WS、registration 和生命周期 | Interactions owner 改 `TestModelMapperFunctionalInteractionsAgent` 及其必需 GJSON import；stream owner 只新增独立 terminal fields 测试函数，不改 imports。 |
| `gCPANewLocalProducer`、`setFixture`、`gCPALoadNative`、`eNativeDrain`、`eResponsesServer`、`eHTTP`、`eWS` | 两个 owner 复用测试能力 | 公共 helpers 不修改；stream 使用已有 `p.wire` custom wire，无需改 producer/imports。每 case 显式设置或清空 wire。 |
| `.github/scripts/smoke-local_test.go` 的 `TestCPAPluginIntegration`／`runFunctionalCPAOverlay` | 唯一 parent-lifetime library 和 overlay 执行入口 | 保持现有入口；终验 owner 验证新增 test 被现有 regex 执行。 |
| U6 的 `main.go`、8份新生产文件及配置／route／executor／rewrite 声明 | 生产职责划分、imports 和唯一共享状态 | module owner 按 R15/R16 移动 post-fix 声明，不与 U2/U3 并行写；registration/schema、配置语义和 executor 调用不变。 |
| `CLAUDE.md`、README | 当前源码布局 | U6 按最终10文件精确更新架构段落和必要简短布局，不改历史 docs 或重复行为说明。 |
| `abi_cgo.go`、Makefile、CI、公共 CPA helpers 和 smoke 入口 | C ABI、现有构建／integration／发布边界 | 全程保留；用原测试、build/package 和 workflow 验收，不新增接口或入口。 |

同一个 `.go` 文件中的不同函数不自动保证无冲突。U2/U3 已完成全部 owner 步骤，协调者比较完整 diff、确认无位置冲突后合并；不重新分派或重做。原精确所有权保持：stream 不修改 imports、Agent 函数、module 或公共 fixture helpers，U2 范围不修改 scanner、stream tests 或公共 helpers。若后续正式检查需要改同一 helper、import block 或 test function，协调者先更新精确所有权和依赖，由单个 owner 完成该位置。

U6 已在 U2/U3 无冲突合并后，从整合 HEAD 连续完成前置完整运行、移动、后置完整运行、规格／质量自审、修正、复审和提交。当前只补充两个阶段 actual child shadow DLL 身份及独立证据复核，原产品与模块化任务不重启。U4 独立终验依赖 U6 完整证据通过并合并，U5 发布依赖 U4；不从原 F1-F3 旧计划直接启动编码。

## Acceptance Examples

- AE1，Covers R1、R3、R4。两个独立 callbacks `event: error`、`data: {"type":"error","code":"server_error","message":"failed","param":null}` 经 mapped native 输出后形成一个有效 error data event。先前没有 data 时，HTTP 为 direct 的 200 SSE，保留原 code/message；有 created 时，两条有序 data 均保留，不出现插件新增的 premature-terminal error。error-only WS 与 fixed direct 的 1006 边界分别记录。
- AE2，Covers R2、R3、R8。两个 callbacks `event: response.done`、`data: {"type":"response.completed","response":{"model":"upstream","output":[...]}}` 保留原 header 和 type，模型恢复为 client，完整 output／opaque 文本保持。单 terminal 和 created 后 completion 的 HTTP/WS 均正常完成。
- AE3，Covers R3。`response.completed` header 配 `response.created` data 保持原 `mismatch-control` 字节；新 reverse alias、unknown type、nonstring active discriminator 不因本次识别扩展成为新正常事件。闲置 `event_type=17/false` 保持 opaque。
- AE4，Covers R5、R6。`{"agent":" \t　","model":"client"}`、`{"Agent":"research-agent","model":"client"}`、`{"agent":"","agent":"research-agent","model":"client"}` 都按普通 model 执行原映射，上游 model 为 upstream，下游白名单 model 为 client。
- AE5，Covers R5、R6、R7。`{"agent":"research-agent","agent":""}`、`{"agent":"research-agent","AGENT":false}`、`{"agent":"research-agent","Agent":""}` 继续 native unhandled，真实 HTTP 200／完整流，原始请求不被 mapper 改写。trim 后非空 agent 和普通唯一 lowercase agent 仍受保护。
- AE6，Covers R7。invalid JSON 使 helper 返回 false；null+model 按普通 model。nonstring+model 的入口 400 与 direct 相同；agent-only nonstring 只核查 R5 的 CPA 分类，不把本地 fake upstream 的响应写成 Google API 支持证明。
- AE7，Covers R1、R3、R4、R8。非 Grok Codex 的 keepalive pair 或 data-only 后接 completion，mapped native 保留两个有效逻辑 data，HTTP 200／WS 获得完整 completion。真实 Grok 的无 LF data-only created -> 完整 `: keepalive\n\n` -> 无 LF data-only completed：首个 Write 无输出，comment Write 输出已恢复的合法 created data event，随后输出原 comment；completed Write 仍按原 data-only 策略等待，Finish 输出一次完整且已恢复的 completed。累计输出只有两个 data events，comment 原字节和顺序保持，完整 output／opaque 内容不变，HTTP／WS 正常取得 created/completed。保留 whole／bytewise × pair／data-only × created／no-created 的 Grok 与普通 completed，以及非 Grok keepalive/error/done alias 全部对照。direct／disabled／unmapped 两个实际 model、no-keepalive 和无 LF metadata 原派发时点保持；BASE 及三谓词新排列均须正式目标 RED，再取得最小 GREEN。各入口按自身内容和 framing 验收，不要求 native 与 HTTP delimiter equality。

- AE8，Covers R15、R16。两项修复合并且 GREEN 后，U6 将全部生产声明唯一分配到10文件；移动前后完整 AST 等价、ABI／tests／benchmark／fixture 逐字不变。新文件布局编入原 package，默认与注入版的真实 registration、init/shutdown 和全部回归分别通过。
- AE9，Covers R8、R9、R17。issue8 的四个 terminal cases 和全部36个 native field Tests，连同 OrdinaryMetadata／MixedMetadata／reverse／alternating 控制继续验证完整 `response.output`、九事件、opaque 中文内容、客户端 model、派发时点及 `emit -> host-close -> plugin-close`。原 subtests、table cases、断言和完整输入不减少。
- AE10，Covers R12、R13、R18。post-fix、post-module 和 final 的默认 DLL 分别执行完整 `make integration`，`TestModelMapperFunctionalReconfigureReload` 仍精确要求 `0.0.0-dev`。独立候选版 DLL 的真实 ABI Version 等于核实后的 patch；最终 CI／Release 绑定含模块化的最终 SHA，原24／2／6456集合和新增 cases 分别验收。

## 永久测试与终验

### Stream 回归

在现有 `stream_native_fields_regression_test.go` 保留5个新增 root 名称：`TestFunctionalNativeSSEFieldError`、`TestFunctionalNativeSSEFieldKeepalive`、`TestFunctionalNativeSSEFieldDoneAlias`、`TestFunctionalNativeSSEFieldTerminalDispatch`、`TestFunctionalNativeSSEFieldTerminalForwarder`。在其对应测试补齐行为 cases；各条均核查 exact bytes、data event 数量、顺序和完整内容，不能仅检查 model `Contains` 或 emit 次数，不新增 private scanner state 断言。

新增永久完整 metadata 边界 case 的三个输入为 `data: {"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`（无 LF）、`: keepalive\n\n`、`data: ` + `gNativeCompleted`（无 LF）。TerminalDispatch 的首个 Write 输出为空；comment Write 的 exact expected 是 `data: {"type":"response.created","response":{"model":"grok-4.6","status":"in_progress","output":[]}}\n\n: keepalive\n\n`；completed Write 无新增输出，Flush/Finish 的 expected 是 `data: ` + `gNativeCompletedWant` + `\n\n`，只输出一次。累计两条合法 JSON data，零额外 data，全部白名单和 opaque 内容保持。TerminalForwarder 覆盖该排列的自然 EOF、Payload+Done、callback/in-band/cleanup error、emit 失败及完整 prefix+error，核查原错误和关闭顺序。在冻结 BASE 和当前三谓词源码／DLL 上分别取得本 case 的目标 RED，再完成最小 GREEN；独立诊断或旧部分完成不能充当正式 TDD。

原覆盖继续包括独立无 LF fields、连续 data-only、标准 LF/CR/CRLF logical events、Flush/Finish、单 terminal、created 后 terminal、keepalive 后 completion、metadata 前置／header与data之间／事件之间、连续 opaque comment、迟到 delimiter、callback/in-band error 和 terminal Payload+Done。原 `Discriminator/mismatch-control`、`DataOnlyTypeControl`、`FormatIsolation`、generic wire 任意分段／BOM、ordinary/mixed metadata 的无 LF zero-output、logical-metadata-callback-characterization、增量游标、ownership、allocation、完整大单位与 incomplete 限额保持。unknown/nonstring/missing type 控制保持，不将非目标 logical event 或 partial metadata 当作新增完整边界。

唯一 CPA fixture 保留末尾唯一 `TestModelMapperFunctionalResponsesTerminalFields` 长矩阵，复用真实 XAI/Codex producer 和已有 custom wire。whole／bytewise × field-pair／data-only × created／no-created 的 actual Grok keepalive comment 与普通 completed 均逐项运行；error／done alias／非 Grok keepalive 后 completion、metadata 全位置和合法交替 shapes 等原要求不减少。各 case 核查 core／host callback 单位、mapped native／HTTP／WS、enabled-unmapped、disabled 和 no-mapper direct 的两个实际 model、type/header、完整 output、原 upstream body/headers/URI/calls；按各入口自身 framing 核对，不以 nil/empty 或跨入口 delimiter equality 判成功。no-keepalive 控制保持；error-only WS 是既存限制控制，keepalive+completion 和 F2 WS 必须正常完成。

### Interactions 回归

root tests 逐项覆盖 missing／empty／ASCII和Unicode空白／null、`Agent`／`AGENT`、escaped lowercase key、两个 duplicate 方向、lowercase first agent 后 uppercase empty／null／bool／number／object／array、trim 后非空 native agent、invalid JSON、nonstring `String()` 分类。

扩展原 `TestModelMapperFunctionalInteractionsAgent`，同时用真实 handler、native DLL、GeminiInteractionsExecutor、普通 model executor 检查 model mapping 和 native bypass。其 fake upstream 当前也用 Go struct 读取 Agent/Model，正式测试需改为各 case 的 raw byte 断言和同版 GJSON 分类，避免 test oracle 的 last-wins／大小写规则拒绝正确 native 请求；只改本函数和其必需 import。正常 string native agent 的 stream/nonstream、重复和大小写覆盖必须有 direct／disabled／unmapped 对照；请求字节、upstream identity、完整 response/SSE、调用次数必须保持。helper 正常不等于真实 native HTTP 正常。

### 完整旧回归矩阵

以下为 BASE 的全部 Test／Benchmark 声明数，由标准 Go AST 重新核查。声明清单与实际运行的展开 cases 分别核对；只核对顶层数量不能证明完整执行。

| 文件 | Tests | Benchmarks | 必须保持的内容 |
| --- | ---: | ---: | --- |
| `main_test.go` | 216 | 15 | 配置／DSL／caller／route／executor、stream、registration、header 和 JSON 原控制。 |
| `abi_cgo_test.go` | 4 | 0 | 实际 cgo 编入，nil、length、overflow 和失败 response 清零。 |
| `json_rewrite_regression_test.go` | 7 | 0 | duplicate/escaped、六项白名单、opaque、数字字节与 ownership。 |
| `performance_regression_test.go` | 12 | 10 | 全部 allocation／retention／batch 内容断言和门槛。 |
| `route_runtime_regression_test.go` | 1 | 0 | runtime replacement 为空时 handled self、无 TargetModel，execute/stream 返回 `empty mapped model` 且 upstream calls 为零。 |
| `stream_protocol_regression_test.go` | 38 | 0 | terminal、raw/SSE、格式隔离、完整大单位、prefix/error、limits 和 framing。 |
| `stream_native_fields_regression_test.go` | 36 | 3 | field-pair/data-only、metadata/mixed、dispatch、wire、ownership、cursor／allocations。 |
| `stream_lifecycle_regression_test.go` | 9 | 1 | 全部 shutdown、blocked emit/read/final flush、errors、close once 和 lifecycle reset。 |
| `.github/scripts/package-release_test.go` | 26 | 0 | ZIP根 library/LICENSE、checksum、path alias拒绝、原子失败清理、sidecar和stale cleanup范围。 |
| `.github/scripts/check-release-compatibility_test.go` | 8 | 0 | GLIBC/macOS inspector 和失败时不继续 package。 |
| `.github/scripts/smoke-local_test.go` | 26 | 0 | host-platform build、process错误、用户config保护及唯一 integration 入口。 |
| `.github/scripts/testdata/cpa-functional-regression_test.go` | 24 | 0 | 全部 producer/native/HTTP/WS、binary captures、metadata/mixed、reload 和大 translator。 |

总计407个 Test 声明，分为 root323、scripts60、fixture24；root 另有29个 Benchmark声明。保留全部原 subtests/table cases、expected maps、断言、内部 deadlines 和数据规模。root／fixture 当前没有 Skip 或 `t.Parallel`；scripts 原平台、权限、make availability 和 integration 环境 Skip 保留条件，实际跳过单列。真实 integration 不得 Skip。不得通过缩小输入、放宽 equality、增加内部 timeout 或降低 allocation 门槛获得 GREEN。

issue8 的四祖先提交分别保护独立无 LF event/data 与连续 data-only、opaque metadata、独立 id/retry/comment、data-only与field-pair切换时的完整前值及下一事件metadata。原 `TestFunctionalIssue8TerminalPayload` 四 cases（Payload+Done、split terminal、Payload+Error+Done、independent Done）保留上游 request、完整 terminal/model、原 error、精确 close counts 和 `emit -> host-close -> plugin-close`。`TestRunStreamForwardProcessesTerminalPayload` 的更早 terminal-order 历史归属单列，不归因于 issue8。

完整历史控制同时覆盖：JSON/YAML 类型与单文档验证、原子 reconfigure、三个 stacking modes、comments/escaping/captures/ASCII case、wildcard 不回溯；exact 和 positive/inverse caller 身份绑定，credential 移除／替换后 warm cache 不得授权；五 formats、unmatched/identity/two-slice/count_tokens。请求顶层 semantic model、duplicate members/parents、escaped keys、`9007199254740993`、opaque/tool、unchanged bytes 与 changed/unchanged headers 均保持。

stream 控制覆盖九事件顺序、metadata 的位置／次数／原字节、双向与交替 mixed shapes、metadata 不增加 data frames、迟到 delimiter 不重复 dispatch；generic LF/CRLF/bare CR/mixed/BOM、全部 splits/bytewise、raw JSON sequences/arrays、EOF、per-read boundaries 和 Gemini raw core 不二次 framing。完整大单位／累计完整流量可超过16MiB，incomplete 超限显露错误并清空，完整 prefix 与并存 error 同时保留，framed incomplete raw JSON 不派发，emit 失败停止输出与 replay，不制造 `[DONE]`。shutdown 保留原2s deadlines、无多余读取、原 primary/cleanup error、close once、preparing/active/workers 清理及下一 init/reset。

全部29个 Benchmark声明的全部实际展开 cases 执行 preflight。现有2MiB／200 allocations 等门槛不变；`-benchtime=1x` 只证明 cases 可运行。修复影响的性能计时按同环境／policy 记录实际成本，preflight 不证明耗时无变化。

### Canonical DSO 与完整旧集合

保持 parent `t.TempDir()` 在 child process 整个生命周期存活、单一 canonical library 文件、实际 SHA256 核查和顺序 enabled owner。每个 owner 仍新建 Host、执行真实 ApplyConfig/init/reconfigure/unload/reload；不缓存 Host，不每个子测试复制新 DSO，不新增共享 `t.Parallel`。

原 24 个 functional tests、2 个 bridge tests 必须逐项执行并通过。四份 capture 保留完整 expected key 集合：`native-binary-captures.json` 1536、`mixed-native-captures.json` 1800、`mixed-binary-captures.json` 1440、`mixed-shape-controls.json` 1680，总数 6456；unknown／duplicate／missing 均为空。普通1536组合包含原 none384，mixed1440独立验收。首次 unload/backpressure 后顺序读尽已接收的17帧，保留 terminal error 和 zero bridge state；完整16,842,937-byte translator unit 及原大 HTTP event 不缩小。新 terminal 测试及新的 Agent 子测试另行列明，不能沿用24作为扩大后的全部顶层数。

正式证据记录源 BASE／起点／最终 HEAD、测试源码和 overlay SHA256、CPA revision/module Sum、实际命令和数字 exit、DLL／shadow DLL identity、原始 upstream/client payload。Go race 与正常 native 加载分层报告；尚未执行、环境拒绝和产品 RED 分开记录。缺日志时写明缺失，不以旧诊断输出代替正式 RED/GREEN。

### 三阶段验收与版本产物

| 阶段 | owner／起点 | 必须取得的证据 |
| --- | --- | --- |
| post-fix | U6 从 U2/U3 无冲突合并后的 GREEN HEAD 开始 | 全部 root/unit/vet、三个显式 scripts、全部 benchmark cases preflight、完整 race（总 timeout30m）、默认 Windows c-shared、真实 ABI registration 和 `make integration VERSION=`；保存全部声明／tests／fixture manifest 与展开 cases。 |
| post-modularization | U6 完成纯移动后 | 相同完整检查重新执行；AST／initializer／const／directive 等价及 ABI/tests/fixture 字节证明；同 owner 审查修正复审提交。 |
| final | U4 独立最终整合 worktree | 全部本地检查重新绑定最终 HEAD、新 build 和 overlay，完整旧矩阵与 F1-F3；独立候选注入产物的真实 ABI Version／package；U5 再验最终 SHA 的 Ubuntu integration、七平台 CI 和全部资产。 |

三阶段分别重建动态库并记录 source commit/hash、built/copied library、实际 Windows shadow DLL、CPA binary SHA256、fixed CPA revision/module Sum 和真实命令/exit。旧 DLL、owner 合并前日志和历史发布反馈均不能作为后续阶段的结果。当前 post-fix/post-module 补跑属于相应阶段的新增身份证据，另存真实时间及新旧关联，不充当 final 结果；身份核查要求 actual child 的加载成功 path/PID/hash 存在并匹配，canonical-only 日志判未完成。root ABI 必须 `CGO_ENABLED=1` 实际编入；普通 scripts 可能 Skip `TestCPAPluginIntegration`，不能冒称真实 integration。正常 native 加载与 race 分层记录。完整命令和 expected 顶层集合见对应计划 Verification Contract。

默认产物保留 `pluginVersion = "0.0.0-dev"`；`TestModelMapperFunctionalReconfigureReload` 精确期待该值。三阶段默认 build/integration 明确清空 VERSION，真实 init/register/reconfigure/unload/reload 全部执行。候选版本使用独立路径／产物，以 `-X main.pluginVersion=<verified-patch>` 注入，再通过真实 ABI init/register 核对 Metadata.Version；不能覆盖默认 library，不能用 `.version` sidecar、archive basename 或源码变量证明 DLL 内 Version。sidecar 仅按现有 compatibility/packaging 流程核验。R13 的注入前及发布前两次占用核查不省略。

### 宿主与实际运行限制

fixed CPA 的 mixed direct/disabled HTTP 缺少 completed，按原 fixture 的完整字节控制单独报告，HTTP200不证明完整结束。XAI 独立 mixed WS60项的会话差异只允许已核实的 `prompt_cache_key`／`X-Grok-Conv-Id`，其余 mapped/direct equality保持。error-only WS 继续按 AE1 的既存1006边界；keepalive+completion和F2 WS必须正常完成。

Gemini countTokens 没有可靠 operation signal／host count callback、Home 拒绝 plugin executor、current v8 unload callback 风险属于宿主边界；保持既有 guard/control和披露，不开发 CPA 功能。历史 Docker／CPA v8.0.10／公网 xAI反馈只证明当时结果，不能替代本次最终源码/DLL。最终 Ubuntu job 必须实际运行 `make integration`；七平台构建成功与七平台 runtime 验证分别报告，无法加载的平台不声称已验证运行。

## 发布与完成条件

U2/U3 已全部完成 R11 并无冲突合并，U6 已在独立 worktree 连续完成 post-fix运行、模块化、post-module运行、审查修正复审和提交。当前 actual child shadow 身份缺口由新的唯一补充验收负责人在自己的新树连续完成程序、自检、两阶段完整 native overlay 补跑、自审修正复审和交接，不改产品、永久 tests/fixture、CPA、Makefile、CI 或 module。补充证据及 fresh 只读独立复核全部通过后才合并 U6，再由 U4 在新的独立 worktree 验收 R12、R13、R15-R18，U5 依赖 U4。规格／质量问题在对应 owner 内修正并复审。源码再次改变后，重跑受影响检查和完整整合验收。

本地 build/package 核查注入版本、注册 metadata、ABI 和 ZIP/checksum/根 LICENSE；兼容性维持 Linux GLIBC <=2.17、macOS deployment target 12.0。保留七个平台：linux/amd64、linux/arm64、darwin/amd64、darwin/arm64、windows/amd64、windows/arm64、freebsd/amd64。

最终交付须满足 R14：本地 main、remote main、tag、Release 和 CI 的最终 SHA 一致；七平台 CI 成功；下载全部资产并用可靠 ZIP/parser、checksum 和真实注册 Version 核查。只构建或只发布成功不等于资产内容验收完成。发布失败时按实际状态报告，不移动既有 tag、force push 或删除用户已有文件。

本次文档任务的完成条件是只提交本规格和对应详细计划，核验完整 diff、frontmatter、R/AE/KTD/U 编号、引用路径、需求／验收对应、保护断言及执行状态一致性，并在自有 ignored `dist/functional-audit-fixes-20261011/U6-doc-arrangement/` 保存自审修正复审收据。事实核对限于获准 Read 的旧证据内容、自有本地提交对象及必要源码，不重新扫描或运行已完成产品任务；本轮未执行产品 tests/build/native、CPA／配置修改、外树 Git 或发布操作。新提交标题和正文使用英语，不重写旧提交。
