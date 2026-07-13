# Ordered ASCII Case Operations Design

**日期：** 2026-07-13
**状态：** 已批准
**范围：** 在现有规则 DSL 中增加两个按顺序执行的独立操作条目：`\a` 和 `\A`。

## 目标

允许规则集在不改变现有 `find=>replace`、通配符、捕获、端点选择和响应恢复语义的前提下，对处理到该条目时的完整当前模型名执行 ASCII 英文字母大小写转换：

- `\a`：将 `A-Z` 转为 `a-z`。
- `\A`：将 `a-z` 转为 `A-Z`。
- 数字、标点、分隔符以及所有非 ASCII 字节保持不变。

## DSL 语义

规则集仍由未转义的 `;` 分隔，但分隔后的内容改称“有序条目”。每个条目只能是：

1. 现有映射：`find=>replace`。
2. 精确独立操作：`\a`。
3. 精确独立操作：`\A`。

操作必须作为完整条目出现。`\x`、`\a=>x`、`\A=>x`、`x=>\a` 和 `x=>\A` 均保持非法。现有查找侧字面量反斜杠写法不变，例如 `\\a=>mapped` 匹配字面模型名 `\a`，不会被解析成操作。

条目从左到右只执行一次。操作读取前一条目产生的当前模型名，后续映射仍以大小写敏感、完整模型名方式匹配操作后的值。

示例：

```text
\a;gpt-*=>deepseek-V3;\A;DEEPSEEK-*=>gpt-5.5;\A
```

对 `GPT-X` 的处理：

```text
GPT-X -> gpt-x -> deepseek-V3 -> DEEPSEEK-V3 -> gpt-5.5 -> GPT-5.5
```

## 数据结构

在现有私有 `rule` 结构中增加一个私有 `caseOperation` discriminator：

```go
type caseOperation uint8

const (
	caseOperationNone caseOperation = iota
	caseOperationLower
	caseOperationUpper
)
```

零值表示普通映射。`rule` 的 pattern、replacement 和 capture 字段保持不变。解析器保证一个条目要么是普通映射，要么是大小写操作，不使用 nil token、`captureCount` 或函数值作为隐式 sentinel。

## 解析

保留 `parseRules` 的全局字符校验和 `splitEscaped(raw, ';')`。在分割后的逐条处理循环中，先精确识别 `\a` 和 `\A`；仅这两个条目绕过 `findRuleSeparator`。所有其他条目继续进入原有 `find=>replace` 解析路径，因此现有错误和转义边界保持不变。

`parseFind`、`parseReplace`、`matchTokens` 和 `buildReplacement` 不修改。

## ASCII 转换

使用 byte-wise helper，只处理两个 ASCII 范围：

```text
A-Z -> a-z
 a-z -> A-Z
```

不使用 `strings.ToLower` 或 `strings.ToUpper`，因为它们会转换 Unicode 字母，超出“英文字母”范围。UTF-8 非 ASCII 字节逐字节原样保留。

## 执行与路由

`applyRules` 在现有单次左到右循环中处理操作：

- 操作总是执行并将返回布尔值置为 true，即使转换是 no-op。
- 普通映射仍只在匹配时将布尔值置为 true。
- 任一已执行条目产生空模型名时继续返回 `empty mapped model`。

内部布尔值的含义扩展为“映射匹配或操作执行”。`routeModel` 继续额外要求最终模型名与原始模型名不同，因此：

- no-op 操作不接管路由。
- 多个条目最终回到原模型名时不接管路由。
- 最终发生变化时沿用现有 Executor 请求改写和响应模型字段恢复逻辑。

不增加 operation-specific Executor、SSE 或 WebSocket 分支。

## 配置表示

公开 YAML 示例使用单引号，确保反斜杠经过 CPA YAML 边界后保持为 DSL 字符：

```yaml
global_rules: '\a;gpt-*=>deepseek-V3;\A'
```

生命周期测试必须覆盖该表示从 `decodeLifecycleConfig` 到 `decodeConfig` 和 `parseRules` 的完整边界。

## 测试策略

采用 TDD：

1. Parser：独立操作、混排、重复、未知操作、错误位置、字面量反斜杠消歧。
2. Apply：ASCII 范围、非 ASCII 保留、前后顺序、大小写敏感、no-op、重复操作、最终恢复原值、空模型错误。
3. Route：操作导致变化时 handled；no-op 和 net identity 时 unhandled。
4. Lifecycle：单引号 YAML 保留单个 DSL 反斜杠；未知操作拒绝配置。
5. 回归：全量测试、vet、Windows amd64 构建和 Zig Linux amd64 交叉构建。

## 文件范围

修改：

- `main.go`
- `main_test.go`
- `README.md`
- `CLAUDE.md`

新增：

- 本设计文档
- 对应实施计划

不修改现有历史设计/计划文档；本设计明确取代其中“每个条目都必须包含 `=>`”和“只有映射匹配才能执行路由”的旧约束。

## 非目标

- 不增加 Unicode 大小写转换。
- 不增加 case-insensitive mapping。
- 不增加通用操作插件框架。
- 不实现 JSON alias。
- 不修改模型列表。
- 不发布、不推送。
