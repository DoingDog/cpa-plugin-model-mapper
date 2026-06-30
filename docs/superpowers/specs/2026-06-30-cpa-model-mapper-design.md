# CPA Model Mapper Plugin Design

**日期：** 2026-06-30  
**范围：** 只实现“模型请求映射器”。“模型列表修改”暂不实现，只记录后续开发计划。  
**目标形态：** 独立 CPA 插件仓库，参考 `cpa-plugin-codex-invite` 的 Go + cgo 动态库插件结构。

## 背景

用户需要一个 CPA 插件，用于在文本生成请求进入上游前改写模型名，并在响应返回客户端前把模型名伪装回客户端原始请求模型。

原始需求包含两个模块：

1. 模型请求映射器。
2. 模型列表修改。

调查 CPA 当前插件 ABI 后确认：独立插件可以通过 ModelRouter、Executor、Response/Stream hooks 完成请求映射；但独立插件不能任意过滤或追加最终 `/models` 响应。当前 CPA 只允许插件通过 `model_provider`/`model_registrar` 贡献模型，不能拦截最终聚合后的模型列表。因此本轮只做模块一。

## 已批准范围

### 做

- 新建独立 Go 插件项目。
- 使用 CPA SDK：
  - `github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi`
  - `github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi`
- 插件输出动态库：Windows 为 `.dll`，Linux 为 `.so`，其他平台按 Go `c-shared` 产物规则输出 `.so`/`.dylib`。
- 最终交付两个可部署插件文件：当前 Windows `amd64` 版本和服务器用 Linux `amd64` 版本，并提供部署方法。
- 如果本机缺少 Linux `amd64` cgo 交叉编译工具链，先通知用户；不在本机全局安装编译器。
- 提供配置字段：
  - `enabled`：布尔值，插件自身启用开关，默认 `false`。
  - `global_rules`：字符串，全局规则。
  - `claude_messages_rules`：字符串，Claude messages 专用规则。
  - `codex_responses_rules`：字符串，Codex responses 专用规则。
  - `openai_completions_rules`：字符串，OpenAI completions/chat completions 专用规则。
- 对文本生成执行链路做模型映射和响应伪装。
- 不处理图像生成端点。
- 写最小测试覆盖规则解析、规则应用、端点规则选择、请求体改写、响应体恢复、SSE chunk 恢复。

### 不做

- 不做模型列表修改模块。
- 不改 CPA 主仓库。
- 不做自定义管理 UI。
- 不引入除 CPA SDK 与 YAML 解码外的新依赖。
- 不缓存完整流式响应。

## CPA 机制结论

### 请求映射不能只靠 RequestInterceptor

CPA 的模型路由发生在 request interceptor 之前。如果只在 `request.intercept_before` 里改 `body.model`，可能出现：

- CPA 已按旧模型选择 provider/auth。
- CPA 日志不一定体现“修改后模型”的请求。
- 用户要求的“修改发生在请求之前，因此 CPA 日志记录修改后模型请求记录”无法可靠满足。

### 推荐执行路径

采用 `ModelRouter + Executor`：

1. ModelRouter 收到原始请求模型。
2. 插件按配置规则计算上游模型。
3. 若模型没有变化，则返回 `Handled: false`，让 CPA 原路径执行。
4. 若模型发生变化，则返回 `Handled: true`、`TargetKind: self`，让 CPA 调用本插件 Executor。
5. Executor 解开 CPA 传入的 RPC wrapper，保留 `host_callback_id`；它必须原样传给后续 `host.model.execute` 或 `host.model.execute_stream`，让 CPA 跳过当前插件，避免递归。
6. Executor 把请求体里的 `model` 改为上游模型。
7. Executor 通过 CPA host callback 再交回 CPA 原执行链路，并设置：
   - `HostModelExecutionRequest.Model = 上游模型`。
   - `HostModelExecutionRequest.Body = 已改写请求体`。
   - `HostModelExecutionRequest.EntryProtocol = ExecutorRequest.SourceFormat`。
   - `HostModelExecutionRequest.ExitProtocol = ExecutorRequest.Format`。
   - 透传 `Headers`、`Query`、`Alt`、`Stream`。
8. 插件拿到响应后，把响应里的模型名改回客户端原始请求模型。
9. 流式响应不整体缓存，但会为每个 SSE `data:` 事件缓存到完整事件/行，再安全改写 JSON 顶层 `model`，避免拆包时泄漏上游模型名。

## 端点规则选择

插件最多对每个请求应用一套规则。

规则选择按 CPA 传入的协议/格式字段判断：

| CPA 格式 | 对应配置 | 说明 |
|---|---|---|
| `claude` | `claude_messages_rules` | Claude messages 请求 |
| `openai-response` | `codex_responses_rules` | Codex responses / OpenAI Responses 风格请求 |
| `openai` | `openai_completions_rules` | OpenAI chat/completions 请求 |
| 其他 | `global_rules` | Gemini 等其他文本生成格式 |

选择逻辑：

1. 如果当前格式有专用规则且专用规则非空，只使用该专用规则。
2. 如果当前格式没有专用规则，或专用规则为空，使用全局规则。
3. 如果最终选中的规则为空，则不改写模型，返回未处理。
4. 不叠加专用规则和全局规则。

## 规则格式

### 输入格式

整体格式：

```text
规则1;规则2;规则3
```

单条规则：

```text
查找式=>替换式
```

核心符号：

- `*`：匹配任意内容，并按出现顺序捕获。
- `$1`、`$2`：引用第 N 个 `*` 捕获。
- `;`：分隔多条规则。
- `=>`：查找/替换分隔符。
- `\`：转义特殊符号。

匹配规则从前到后执行，上一条替换后的结果继续进入下一条。

### 合法性

整体规则：

- 不能包含空格。
- 不能包含引号。
- 不能为空。
- 必须包含 `=>`。
- 规则之间用 `;` 分隔。
- 不能出现空规则。

查找式：

- 不能为空。
- 可以包含普通字符。
- `*` 表示通配符捕获。
- `\` 可转义特殊符号。
- 不能以未完成的 `\` 结尾。

替换式：

- 不能为空。
- `$1`、`$2`、`$3` 表示召回查找式里的第 N 个 `*`。
- `$数字` 不能超过查找式里的 `*` 数量。
- 不能出现单独的 `$`。
- 不能出现 `$0`。
- 不能出现 `$x` 这类非数字引用。
- 不能以未完成的 `\` 结尾。

### 应用失败

如果启用后出现以下任一情况，当前请求失败：

- 所选规则无法解析。
- 规则应用过程中出现错误。
- 最终输出模型名为空。

插件错误以 CPA 插件调用错误返回，由 CPA 按当前执行路径转为请求失败。

## 数据流

### 非流式

```text
client request(model=A)
  -> CPA ModelRouter
  -> plugin maps A => B
  -> plugin Executor mutates request body model=B
  -> plugin calls host.model.execute(model=B, body.model=B)
  -> CPA original scheduling/auth/executor
  -> upstream response(model=B)
  -> plugin rewrites response model=A
  -> client response(model=A)
```

### 流式

```text
client stream request(model=A)
  -> CPA ModelRouter
  -> plugin maps A => B
  -> plugin Executor mutates request body model=B
  -> plugin calls host.model.execute_stream(model=B, body.model=B, host_callback_id=original)
  -> plugin reads host stream chunks via host.model.stream_read
  -> plugin buffers only until a complete SSE data event/line is available
  -> for each complete event/line:
       - pass through [DONE], comments, keep-alives, and non-JSON events
       - rewrite complete JSON event top-level model B => A
       - emit immediately through host.stream.emit
  -> plugin closes upstream host model stream
  -> plugin closes plugin stream via host.stream.close
  -> client receives stream without full-response buffering
```

## 组件设计

### `main.go`

单文件实现，保持插件项目最小。

职责：

- 导出 CPA C ABI：
  - `cliproxy_plugin_init`
  - `cliproxyPluginCall`
  - `cliproxyPluginFree`
  - `cliproxyPluginShutdown`
- 处理 CPA RPC method：
  - `plugin.register`
  - `plugin.reconfigure`
  - `model.route`
  - `executor.execute`
  - `executor.execute_stream`
  - `executor.count_tokens` 可返回不支持或空实现，除非 CPA 要求能力存在。
- `executor.execute`/`executor.execute_stream` 必须解析 wrapper 字段：
  - `ExecutorRequest`
  - `host_callback_id`
  - `stream_id`（仅 `executor.execute_stream`）
- 非流式调用 `host.model.execute` 后同步返回改写后的响应。
- 流式必须匹配 CPA c-shared stream bridge：收到 `stream_id` 后启动转发工作，调用 `host.model.execute_stream`，循环 `host.model.stream_read` 读取上游 chunk，按完整 SSE 事件/行改写模型名，通过 `host.stream.emit` 发给 CPA，最后关闭 host model stream 并调用 `host.stream.close`；出错时通过 `host.stream.close` 的错误字段结束插件流。
- 解析配置。
- 解析和应用模型映射规则。
- 改写请求 JSON 顶层 `model`。
- 改写响应 JSON 顶层 `model`。
- 改写 SSE `data:` JSON chunk 顶层 `model`。
- 调用 host model callback。

保持单文件是刻意选择：插件逻辑小，过早拆包会增加跳转成本。若 `main.go` 超过约 800 行或规则解析被复用，再拆成 `rules.go`。

### `main_test.go`

职责：

- 测试规则解析合法/非法输入。
- 测试 `*` 捕获与 `$N` 替换。
- 测试多规则顺序执行。
- 测试专用规则优先于全局规则。
- 测试空专用规则回落全局规则。
- 测试 disabled 默认不处理。
- 测试请求 JSON 顶层 `model` 改写。
- 测试非流式响应 JSON 顶层 `model` 恢复。
- 测试 SSE chunk：
  - `data: {"model":"B"}` 改为 `A`。
  - `data: [DONE]` 原样返回。
  - 非 JSON 原样返回。

### `Makefile`

职责：

- `make test`：运行 `go test ./...`。
- `make build`：运行 `go build -trimpath -buildmode=c-shared` 输出当前平台动态库到 `dist/model-mapper.<ext>`。
- `make install-local CPA_PLUGINS_DIR=<path>`：复制构建产物到 CPA 插件目录，例如 `plugins/windows/amd64/model-mapper.dll`。
- `make clean`：删除构建产物。

### `README.md`

职责：

- 插件说明。
- 配置示例。
- 规则语法。
- 构建命令。
- 部署方法：复制动态库到 CPA 插件目录、启用插件、填写规则、重启或热加载 CPA 配置。
- 当前不支持模型列表修改的说明。

### `docs/model-list-modification-plan.md`

职责：

记录后续模型列表修改模块开发计划：

- 当前独立插件无法完整实现最终 `/models` 响应过滤/追加。
- 需要 CPA 宿主新增 models-list interceptor hook。
- hook 应覆盖 OpenAI/Claude/Gemini/Home/Codex client version 等模型列表路径。
- hook 应在最终模型列表聚合完成、写响应前执行。
- 独立插件在新 hook 可用后复用同一规则解析风格实现删除/添加。

## 插件注册

插件 ID 建议：`model-mapper`

能力声明：

- `model_router: true`
- `executor: true`
- `executor_model_scope: static`
- `executor_input_formats`：`openai`、`openai-response`、`claude`、`gemini`
- `executor_output_formats`：同 input formats

ConfigFields：

- `enabled`：boolean
- `global_rules`：string
- `claude_messages_rules`：string
- `codex_responses_rules`：string
- `openai_completions_rules`：string

插件内部默认配置：

```yaml
enabled: false
global_rules: ""
claude_messages_rules: ""
codex_responses_rules: ""
openai_completions_rules: ""
```

CPA host 层仍要求：

```yaml
plugins:
  enabled: true
  configs:
    model-mapper:
      enabled: true
      priority: 1
```

插件自身的 `enabled: false` 默认表示：即使 host 启用插件，未显式打开功能时也不改写请求。

## 错误处理

- 配置解析失败：`plugin.register`/`plugin.reconfigure` 返回错误 envelope。
- 规则解析失败：匹配请求时返回 executor/model route 错误，使当前请求失败。
- 改写后模型为空：当前请求失败。
- 请求体不是 JSON 或没有字符串 `model`：不处理，返回 `Handled: false`。
- host model callback 返回错误：透传为当前请求失败。
- 流式非 JSON 完整事件：原样传递，不失败，避免破坏流式传输。
- 流式拆包 JSON 事件：最多缓存当前未完成事件/行；完整后再改写并发出，不直接透传可能包含上游模型名的半截 JSON。

## 响应模型恢复范围

本轮只恢复顶层 `model` 字段。

原因：OpenAI/Claude/Codex/Gemini 常见文本生成响应的模型字段位于顶层；递归改写所有 `model` 字段可能误伤工具参数、用户内容或嵌套业务字段。

如果后续发现 CPA 某个格式把模型名放在非顶层字段，再按实测格式增加精确路径。

## 交付与部署

最终实现完成时必须产出两个可直接使用的插件动态库：

```text
dist/windows_amd64/model-mapper.dll
dist/linux_amd64/model-mapper.so
```

Windows 部署方法：

1. 在本插件仓库运行 `make build-windows-amd64`。
2. 将 `dist/windows_amd64/model-mapper.dll` 复制到 CPA 的插件目录：

```text
<CPA目录>/plugins/windows/amd64/model-mapper.dll
```

Linux `amd64` 部署方法：

1. 在本插件仓库运行 `make build-linux-amd64`。
2. 将 `dist/linux_amd64/model-mapper.so` 复制到服务器 CPA 插件目录：

```text
<CPA目录>/plugins/linux/amd64/model-mapper.so
```

3. 在 CPA 配置中启用插件：

```yaml
plugins:
  enabled: true
  configs:
    model-mapper:
      enabled: true
      priority: 1
      global_rules: ""
      claude_messages_rules: ""
      codex_responses_rules: ""
      openai_completions_rules: ""
```

4. 按需把插件自身 `enabled` 改为 `true` 并填写规则。
5. 重启 CPA，或使用 CPA 当前支持的配置热加载方式让插件重新注册。

如果用户指定了 CPA 插件目录，实现阶段可以运行：

```powershell
make install-local CPA_PLUGINS_DIR="C:/path/to/CLIProxyAPI/plugins/windows/amd64"
make install-linux-amd64 CPA_PLUGINS_DIR="/path/to/CLIProxyAPI/plugins/linux/amd64"
```

## 测试策略

最小可运行检查：

```powershell
go test ./...
```

构建检查：

```powershell
make build
```

若当前 Windows 环境缺少 CGO 编译器，停止并通知用户安装外部工具，不在本机全局安装。

## 模型列表修改后续开发计划

### 现状

当前 CPA 独立插件无法任意删除或追加最终 `/models` 响应。已有能力只能贡献模型，不能拦截聚合后的最终列表。

### 需要的 CPA 宿主能力

新增 models-list interceptor hook，例如：

```go
type ModelListInterceptor interface {
    InterceptModelList(context.Context, ModelListInterceptRequest) (ModelListInterceptResponse, error)
}
```

请求应包含：

- `SourceFormat`：`openai`、`claude`、`gemini` 等。
- `Models`：最终聚合完成的模型列表。
- `Headers`/`Query`：用于区分兼容端点。

响应应包含：

- 修改后的 `Models`。

### 模型列表规则语法

按用户原始要求：

- 配置为逗号分隔字符串。
- 首尾逗号忽略。
- 连续多个逗号合并。
- 条目前缀一个或多个 `-` 表示删除。
- `*` 通配 0 到任意数量字符。
- 非删除项表示添加模型。
- 从左到右顺序处理。

示例：

```text
-claude*,gpt-5
```

效果：删除所有 `claude` 开头模型，再添加 `gpt-5`。

### 实现条件

等 CPA 主仓库提供最终 models-list hook 后，再在本插件新增该模块。届时仍保持独立插件，不修改现有请求映射模块接口。
