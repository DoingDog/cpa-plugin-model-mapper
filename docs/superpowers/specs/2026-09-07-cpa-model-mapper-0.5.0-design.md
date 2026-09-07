# CPA Model Mapper 0.5.0 Design

**日期：** 2026-09-07
**范围：** 仅修改 `cpa-plugin-model-mapper` 插件仓库，不修改 CLIProxyAPI 本体。
**目标版本：** `v0.5.0`

## 背景

当前 `v0.4.4` 使用 CLIProxyAPI SDK `v7.2.48`，支持 `openai`、`openai-response`、`claude` 和部分 `gemini` executor 输入，但没有 `interactions`，也没有完整声明五种格式的 executor 输出。专属规则存在有效条目时会完全替代 `global_rules`，规则 DSL 不支持注释，插件 registration 也没有 Logo。

`v0.5.0` 将 SDK 升级到 `v7.2.152`，完整支持 CPA 的五种 canonical format，增加三态规则叠加、可视化 enum 配置、规则项注释和 Logo，并修复本轮审计确认的插件缺陷及有测量依据的性能问题。

## 上游协议依据

CLIProxyAPI `v7.2.152` 定义以下 canonical format，顺序也作为本插件 registration 的稳定顺序：

1. `openai`
2. `openai-response`
3. `claude`
4. `gemini`
5. `interactions`

`pluginapi.ConfigField` 支持 `ConfigFieldTypeEnum` 与 `EnumValues`，但没有独立的默认值或显示标签字段。`pluginapi.Metadata.Logo` 可保存图标 URL。`ExecutorInputFormats` 表示插件可以接收的 payload format，`ExecutorOutputFormats` 表示插件可以返回的 payload 或 chunk format。

本插件继续使用 `pluginabi.SchemaVersion`，不 hardcode RPC schema。native ABI 保持 1。

## 功能范围

### 本版本实现

- 将 `github.com/router-for-me/CLIProxyAPI/v7` 精确升级到 `v7.2.152`。
- 为五种 canonical format 提供 request 模型重写和 response 模型恢复。
- 保留四个现有规则字段，并新增 `rules_stack_mode`。
- `rules_stack_mode` 在 CPA 配置界面中显示为 enum。
- 允许专属规则和 `global_rules` 按配置顺序叠加。
- 支持以未转义 `!` 标记整条规则项为注释。
- 在 registration 中编译进仓库 Logo 的 raw URL。
- 修复 request 或 response payload 改写后遗留旧 `Content-Length` 的问题。
- 修复 host stream read-call error 时丢失 rewriter pending bytes 的问题。
- 避免 ASCII 大小写 no-op 的无效分配。
- 避免 lifecycle config 的重复编译。
- 将现有 smoke helper tests 加入 GitHub Actions test job。

### 本版本不实现

- 不增加 `gemini_rules` 或 `interactions_rules`。
- 不修改 CPA 本体、plugin host、stream lease、unload、count-token 或 `caller_scope` 生成逻辑。
- 不递归改写任意 JSON `model` 字段。
- 不推断 Gemini 或 Interactions 的新 wire framing。
- 不缓存跨 host callback 的任意 incomplete raw JSON value。
- 不根据 `application/json` 猜测客户端需要 SSE framing。
- 不在 request body 缺少顶层 `model` 时拒绝执行。
- 不增加 caller cache TTL 或容量策略。
- 不重构 release workflow 的触发条件、权限或 job 结构。
- 不引入新依赖。

## Registration 与插件元数据

`pluginRegistration` 必须保持 `model_router`、`executor` 和 `executor.execute_stream` 能力，并将以下两个列表都设置为完全相同的五格式 canonical 顺序：

```text
openai
openai-response
claude
gemini
interactions
```

设置：

```text
Metadata.Logo = https://raw.githubusercontent.com/DoingDog/cpa-plugin-model-mapper/refs/heads/main/logo.png
```

Logo URL 是插件 registration 的静态元数据，不要求用户额外配置。

## 配置模型

插件拥有以下五个配置字段：

| 字段 | 类型 | 含义 |
|---|---|---|
| `global_rules` | string | 所有格式可用的全局规则 |
| `claude_messages_rules` | string | `claude` 专属规则 |
| `codex_responses_rules` | string | `openai-response` 专属规则 |
| `openai_completions_rules` | string | `openai` 专属规则 |
| `rules_stack_mode` | enum | 专属规则与 global 的执行方式 |

`rules_stack_mode` 的 `EnumValues` 顺序固定为：

```text
off
specific_first
global_first
```

其语义如下：

- `off`：默认值，保持 `v0.4.4` 行为。存在有效专属规则时不使用 global。
- `specific_first`：先执行专属规则，再执行 global。
- `global_first`：先执行 global，再执行专属规则。

配置省略 `rules_stack_mode` 或传入空字符串时，统一归一为 `off`。只接受三个精确小写值。未知值、大小写变体、连字符变体、其他拼写和非字符串值都返回配置错误。

JSON lifecycle config 与 YAML lifecycle config 必须应用同一套归一和校验。reconfigure 失败时保留此前已发布的有效配置 snapshot。

## 规则选择和执行顺序

配置加载时编译全部 ruleset。request hot path 只读取 immutable `[]rule`，不重新解析 raw string，也不拼接或复制规则 slice。

内部选择结果使用两个有序集合：

```go
type ruleSelection struct {
    first  []rule
    second []rule
}
```

格式和模式矩阵：

| format | 专属规则 D | `off` | `specific_first` | `global_first` |
|---|---|---|---|---|
| `openai` | `openai_completions_rules` | D | D, G | G, D |
| `claude` | `claude_messages_rules` | D | D, G | G, D |
| `openai-response` | `codex_responses_rules` | D | D, G | G, D |
| `gemini` | 无 | G | G | G |
| `interactions` | 无 | G | G | G |

其中 G 表示 `global_rules`。

选择规则：

1. D 有效规则数量为零时只使用 G，包括 D 为空和 D 全部为注释的情况。
2. G 有效规则数量为零时只使用 D。
3. D 与 G 都没有有效规则时返回空选择，route 不处理请求。
4. 第二组以第一组的输出模型作为输入。
5. `matchedAny` 是两组匹配结果的逻辑 OR。
6. 至少一条规则匹配但最终模型回到原值时，继续按现有语义返回 `Handled: false`。
7. 不通过拼接 raw rules 或向共享 slice `append` 实现叠加。

## Caller scope 与认证边界

caller 规则的认证安全边界保持不变：

- exact caller scope 比较 metadata 中的 `caller_scope` digest。
- wildcard caller scope 只从 raw inbound credential 恢复 Principal，且 credential digest 必须等于 `caller_scope`。
- client-controlled headers 不能单独建立可信身份。
- access provider 返回的 Principal 与所有提交 credential 都不一致时，wildcard caller 规则跳过。
- caller identity 缺失或未绑定时，positive 与 inverse scope 都跳过。
- route 缓存仍只保存 `caller_scope + pattern` 的 boolean 结果，executor 在 interceptor 改变 credential header 后复用该判断。

叠加模式下，caller credential recovery 必须扫描 `first` 和 `second` 两个选中集合。只存在于第二组的 caller rule 与只存在于第一组的 caller rule 具有相同认证行为。

## 规则项注释语法

ruleset 仍由未转义 `;` 分隔。新增 comment-aware 顶层 scanner，在任何 entry 级语法检查之前识别注释。

### 注释判断

一个 entry 任意位置出现未转义 `!` 时，整个 entry 是注释并被跳过。注释 entry 不执行以下检查：

- 空白或引号检查。
- caller scope 检查。
- `=>` 数量和位置检查。
- `$N` reference 检查。
- escape 合法性检查。
- dangling backslash 检查。

因此以下 ruleset 合法，中间 entry 不产生 rule：

```text
\a;nihao!gpt***==>>>;*=>gpt-4
```

### 转义

- `\!` 解码为普通字面 `!`。
- 判断 `!` 是否转义时，只统计其前方连续反斜杠数量。
- 连续反斜杠数量为奇数时，`!` 被转义。
- 连续反斜杠数量为偶数时，`!` 是 comment marker。
- `\!` 在 caller pattern、find 和 replace 中都必须作为字面字符工作。

### 空 entry 与全注释 ruleset

- 全注释 ruleset 合法，编译结果为零条规则。
- 非注释空 entry 仍非法。
- leading `;`、trailing `;` 和 double `;` 产生非注释空 entry，仍返回错误。
- active entry 的原有语法和单次 left-to-right 执行语义不变。

scanner 使用普通字节循环，不使用正则、AST 或新依赖。

## 五格式 request 契约

Model router 始终从独立的 `RequestedModel` 开始应用规则。路由成功后：

- `HostModelExecutionRequest.Model` 必须是 mapped model。
- `HostModelExecutionRequest.EntryProtocol` 与 `ExitProtocol` 必须保持当前 canonical format。
- direct executor path 中，`ExecutorRequest.SourceFormat` 和 `ExecutorRequest.Format` 都必须等于当前 format。

request body 的安全边界：

- 只检查并改写顶层 string `model`。
- nested object、content、tool arguments 和其他文本中的 `model` 不得修改。
- body 没有顶层 `model` 时保持原样，不能拒绝请求。
- body 的顶层 `model` 不是 string 时保持现有错误或 no-change 语义，不扩大重写范围。
- Gemini 等格式可以只通过独立 `HostModelExecutionRequest.Model` 传递 mapped model。

request body 实际变化时，删除传给 host 的 request header `Content-Length`。body 未变化时保留原 header。

## 五格式 response 契约

只恢复以下精确路径：

```text
model
modelVersion
message.model
response.model
response.modelVersion
```

任何其他位置保持不变，包括 nested payload、content、tool input、tool output 和用户文本中的模型名。

非流式 response payload 实际变化时，删除返回客户端的 response header `Content-Length`。payload 未变化时保留原 header。

流式响应继续使用现有 `streamChunkRewriter`：

- 处理完整 SSE event。
- 处理 split SSE prefix 和现有已定义的 event buffering。
- flush 时处理 unterminated SSE data。
- 处理完整 raw JSON chunk。
- 处理现有 line 或 space-delimited JSON values。
- 对 Responses SSE client 保留现有 raw JSON framing 行为。

五格式测试只使用仓库已支持的完整 SSE event 或完整 raw JSON chunk，不新增格式特有 wire 假设。

## Host stream read error 修复

当前 `runStreamForward` 在 `MethodHostModelStreamRead` 调用本身返回 error 时，会直接退出，未输出 `streamChunkRewriter` 中等待 flush 的 bytes。

修复后的关闭顺序：

1. host read-call 返回 error。
2. 调用与正常 EOF、`chunk.Error` 相同的 flush+emit seam。
3. 如果 pending bytes 可输出，先通过 `host.stream.emit` 发出恢复后的内容。
4. 关闭 host model stream。
5. 返回原始 read error。
6. 外层用该错误关闭 plugin stream。

如果 flush 或 emit 自身失败，不能伪造成功；错误优先级沿用现有 stream error 处理约定，但必须确保 host stream 和 plugin stream 最终关闭，客户端不能因 pending bytes 或未关闭流而挂起。

## 性能要求

### ASCII 大小写 no-op

`applyASCIIModelCase` 先扫描 string 是否存在需要改变的 ASCII byte：

- lowercase 操作只在发现 `A` 到 `Z` 时分配。
- uppercase 操作只在发现 `a` 到 `z` 时分配。
- 无需改变时直接返回原 string，目标为 `0 B/op`、`0 allocs/op`。
- 需要改变时才创建 `[]byte`，输出必须与现有行为相同。
- 非 ASCII byte 保持不变。

### Lifecycle config 单次编译

`decodeConfig` 返回已编译的完整 snapshot。生产 `applyLifecycleConfig` 直接发布这个 snapshot，不再通过 test setter 再编译一次。

实现必须把以下职责分开：

- compile：解析 raw config、归一 `rules_stack_mode`、校验并编译 ruleset。
- publish：加锁替换 snapshot，并清理 caller cache。

test setter 仍可接收 raw `Config`，但一次调用最多编译一次。

### SSE marker benchmark gate

只有同一工具链、相同输入下至少五次 benchmark 都证明直接调用 candidate helper 稳定降低实际耗时，且 `B/op` 与 `allocs/op` 不增加时，才保留 SSE marker 重复扫描优化。若证据不满足此门槛，不修改该 production path，也不在发布说明中声称该优化。

任何性能改动都必须保持输出 bytes、chunk 顺序、ownership、response whitelist、SSE delimiter 和 race safety。

## CI 与文档

GitHub Actions 的现有 `test` job 增加：

```text
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

不修改 workflow events、global permissions、release permissions、artifact matrix 或 release policy。

`README.md` 必须说明：

- 五种 canonical format。
- Logo 已包含在插件 metadata 中。
- 五个 plugin-owned 配置字段。
- `rules_stack_mode` 三态和执行矩阵。
- Gemini 与 Interactions 只使用 `global_rules`。
- `!` 注释与 `\!` literal。
- comments-only 专属规则回退 global。
- request 顶层 `model` 安全边界。
- response 五个精确恢复路径。

`CLAUDE.md` 必须同步维护实现约束和 smoke helper test 命令。

## 测试策略

所有 production behavior change 使用 TDD：先增加 focused failing test 并确认失败原因正确，再实现最小改动，然后运行 focused GREEN 和相关回归。

### Registration 和配置

测试必须覆盖：

- input/output exact five canonical formats。
- Logo exact raw URL。
- `rules_stack_mode` 为第五个 plugin-owned field。
- field type 是 enum，枚举顺序固定。
- description 明示 default `off`。
- omitted、empty、三个合法值、未知值、大小写变体、连字符变体和非字符串值。
- JSON 与 YAML lifecycle config。
- reconfigure 失败保留旧 snapshot。

### 注释 DSL

测试必须覆盖：

- `!` 位于 entry 开头、中间和末尾。
- 注释内包含空白、引号、坏 scope、重复 separator、坏 reference、非法 escape 和 dangling backslash。
- active-comment-active。
- 全注释 ruleset。
- `\!` 位于 scope、find 和 replace。
- `!` 前连续反斜杠的奇偶语义。
- 非注释 leading、trailing、double `;` 继续报错。

### 规则叠加

测试必须覆盖：

- 三个专属 format × 三个 mode。
- D 有效、D 为空和 D comments-only。
- Gemini、Interactions × 三个 mode。
- D -> G 与 G -> D 的不同结果。
- 两组执行后最终回到原模型。
- caller positive/inverse wildcard 只出现在第一组或第二组。
- digest binding、Principal 一致性和 route -> executor cache。
- selection 与 exact-rule route 不因 stacking 产生额外 heap allocation。

### 五格式 executor

nonstream 和 stream table tests 遍历五种 format，断言：

- direct `SourceFormat == Format == 当前 format`。
- host 收到 mapped `Model`。
- request 顶层 string `model` 在存在时被改写。
- Gemini 无顶层 `model` 时 body 原样。
- response 五个白名单路径恢复。
- opaque nested/content/tool text 不变。

### 回归修复

测试必须覆盖：

- nonstream 与 stream request 改写后删除旧 `Content-Length`。
- nonstream response 恢复后删除旧 `Content-Length`。
- no-change control 保留 header。
- host stream read-call error 前先 flush pending restored bytes，随后 error close，且不 hang。

### 性能

测试和 benchmark 必须覆盖：

- ASCII already-lower、already-upper 与 changed path。
- no-op `AllocsPerRun` 为零。
- lifecycle compile 次数或等效 benchmark 证明 production 不重复编译。
- SSE marker candidate 只有达到门槛才保留。

## 本地验证 gate

feature branch 和合并后的 `main` 都必须运行：

```text
git diff --check
go mod verify
go test ./... -count=1
go test -race ./... -count=1
go test -gcflags=all=-d=checkptr=2 ./... -count=1
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
make build-windows-amd64
make build-linux-amd64 LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"
make package VERSION=0.5.0
```

不得运行 `go test ./.github/scripts`。

只有 `CPA_SMOKE_API_KEY` 和 `CPA_SMOKE_CPA_BIN` 都存在时运行 `make smoke-local`。缺失时记录为 skipped，不写成通过。

package 验证必须确认 archive 根只包含目标动态库和可选的 `LICENSE`，checksum 使用 archive basename 且内容匹配。

## Review 和范围审计

最终 review 按文件分区，避免 reviewer 重复读取同一文件：

- 核心分区：`main.go`、`main_test.go`。
- 辅助分区：`go.mod`、`go.sum`、`README.md`、`CLAUDE.md`、`.github/workflows/build.yml`、本版本 spec 和 plan。

finding 只在可复现并与本 spec 相关时进入修复。功能 finding 先增加 focused failing test，再做最小修复。

最终 diff 不得包含：

- CLIProxyAPI 源码或 module cache。
- `upstream/`。
- `.test-cpa/`。
- `dist/`。
- `.claude/`。
- 与本版本无关的重构或格式调整。

## 发布成功条件

1. 所有明确功能均有 focused tests，默认配置与 `v0.4.4` 兼容。
2. SDK 版本精确为 `v7.2.152`，没有无关依赖升级。
3. 本地 correctness、race、checkptr、vet、script、build 和 package gate 全部通过。
4. 保留的性能改动有同工具链前后数据，未达到门槛的候选不进入 production diff。
5. live smoke 只在凭证存在时运行，状态如实记录。
6. feature branch fast-forward 合并到本地 `main`。
7. local `main`、`origin/main` 和 annotated `v0.5.0` 指向同一已验证 commit。
8. tag 使用 atomic push，不 force push，不覆盖已有 tag。
9. GitHub tag workflow 成功。
10. GitHub release 非 draft、非 prerelease，tag 精确为 `v0.5.0`。
11. release 包含平台 matrix 对应的 7 个 zip 和 `checksums.txt`。
12. 所有 checksum 与下载资产匹配，每个 zip 根目录内容符合 packaging 契约。
