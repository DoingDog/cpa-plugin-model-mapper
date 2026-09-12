# Stream Safety and Error Propagation Design

日期：2026-09-12

## 目标

修复 model-mapper 中三个已经复现或由代码路径直接证明的插件问题：

1. CPA v7.2.152 的 Kimi executor 向插件逐条返回无尾随 SSE 空行的 OpenAI Responses 逻辑事件时，插件会把相邻事件拼成无效的 `JSON}event:` 字节序列，最终触发 HTTP 502 `invalid SSE data JSON`。
2. 未终止的 SSE event 或不完整 raw JSON 会在单个 stream 中无限累积，远端 provider 可以持续增加 CPA 进程内存。
3. 插件把 host callback 的结构化 error 和 mapped execution 的 HTTP error status 转成普通 Go error，导致 CPA 能够保留的 `code`、`message`、`http_status` 被降级为无 status 的 `plugin_error`，400、429、503 等响应会变成外层 500。

改动只涉及插件，不修改 CPA 本体或其源码。

## 已确认的故障

### Delimiterless Responses logical payload

Responses 客户端请求 `claude-opus-5`，插件将其映射到 `kimi-k3`。CPA v7.2.152 先执行 Kimi Chat Completions，再将一条上游 chunk 展开为多个 OpenAI Responses 逻辑事件。每次 `host.model.stream_read` 返回一个完整 payload：

```plaintext
event: response.created
data: {"type":"response.created",...,"model":"kimi-k3"}
```

payload 在 JSON 的最终 `}` 处结束，不含尾随换行或 SSE 空行。下一个 read 返回下一条同形 payload。CPA Responses validator 能逐次接受这些 payload，但当前 `sseRewriter` 只在发现 SSE 空行后结束 event，因此会输出：

```plaintext
..."model":"kimi-k3"}}event: response.in_progress
data: {...}
```

validator 随后把相邻内容当成同一个 `data:` JSON，返回 HTTP 502。仓库外的精确 payload reproduction 已稳定得到同形错误，责任在插件的 stream rewrite 边界。

`claude-opus-5` 是合法的 client-requested alias，protocol 由 CPA 的 `SourceFormat`/`Format` 决定，模型名称不是根因。

### Unbounded retained stream state

`sseRewriter.buf` 会保存尚未遇到 event delimiter 的全部字节，`streamChunkRewriter.pending` 会保存尚未形成完整 JSON 或尚未判定格式的全部字节。两者都没有上限。远端 provider 持续发送不闭合的 event 或 JSON 时，单个 stream 可以耗尽 CPA 进程内存。

### Structured error metadata loss

`pluginabi.Error` 提供 `code`、`message`、`retryable`、`http_status`。当前路径存在两类信息丢失：

- `callHost` 将 nested host error envelope 转成 `fmt.Errorf`，丢失全部结构化字段。
- `handleExecutorExecute` 和 `prepareExecutorStream` 将 `HostModelExecutionResponse.StatusCode >= 400` 转成普通 `fmt.Errorf`。

`wrapEnvelope` 再把普通 error 固定编码为无 `http_status` 的 `plugin_error`。CPA v7.2.152 的外层 decoder 会保留 `code`、`message`、`http_status`，因此这三个字段可以由插件完整修复。插件也应在自己的 ABI envelope 中复制 `retryable`，但 CPA v7.2.152 外层不会继续传播该字段；本次不声称改变 CPA 的该行为。

## 设计

### 1. 按内容恢复 delimiterless Responses event 边界

仅在 `openai-response` stream 中启用 delimiterless logical-event recovery。普通 SSE 仍以 LF、CRLF 或 CR 组成的空行作为 event delimiter。

rewriter 在 buffered event 中识别以下安全边界：

- event 具有 Responses 常见的 `event:` 行和 `data:` 行。
- `data:` 后的内容可以被 Go `encoding/json` 完整解码为一个 JSON value。
- JSON value 的结束位置紧接下一条完整的 `event:` field prefix，或者 stream 已到 EOF。

JSON decoder 的消费位置决定 JSON 结束点，不能按字符串搜索 `event:`，以免在 JSON string 或 tool arguments 中误切分。下一条 prefix 可以跨任意 read 分片；在 prefix 尚未完整时继续 buffering。host read 边界本身不构成 event 边界。

识别到边界后，插件恢复白名单中的 response model 字段，并在逻辑事件之间输出标准 SSE 空行。最终完整的 delimiterless Responses event 在 clean EOF 时同样补齐标准 delimiter。由此即使 `emitRewritten` 合并多个 output chunks，CPA validator 仍能分别验证每个 event。

以下行为保持不变：

- 已有标准 blank-line-delimited SSE 的原始 delimiter 和字段顺序。
- 任意位置的 `event:`、`data:`、JSON token、LF、CRLF、CR fragmentation。
- incomplete JSON 不得在 read 边界提前输出。
- raw JSON framing、Gemini JSON array、Gemini `alt=sse`、OpenAI Chat Completions `[DONE]` 和 Anthropic unknown event 行为。
- model restoration 只修改既有白名单字段，不修改 opaque content 或 tool arguments。

### 2. 限制每个 stream 当前未完成单元的 retained bytes

增加一个 package-level 固定上限 `maxPendingStreamBytes = 16 << 20`，即 16 MiB。它高于已知 Kimi scanner 的 1 MiB ceiling，并为 framing 和大型合法 tool payload 留出空间，同时将单个异常 stream 的 retained incomplete state 限制在确定范围内。该值不是累计 stream 流量上限，也不是单次 `Write` 大小上限。

上限同时覆盖：

- `sseRewriter.buf` 中当前未完成的 SSE event。
- `streamChunkRewriter.pending` 中当前未完成的 raw JSON、JSON array element 或尚未完成的 SSE prefix。

每次先消费本次输入中已经完成的 SSE events 或 JSON values，再检查仍需保留的 incomplete tail。一个很大的 `Write` 如果包含许多小而完整的逻辑单元应成功；累计流量超过 16 MiB 也应成功。只有当前需要跨调用保留的单个 incomplete unit 超过上限时才失败。

overflow 时清空该 rewriter 的 retained state，并返回明确 error。现有 stream forwarding 路径负责关闭该 plugin stream 和 host stream。不得 emit oversized unit 的任何 partial bytes，也不得影响其他 stream。

### 3. 在插件 error envelope 中保留 host error metadata

增加一个仅供内部使用的 typed error，携带完整 `pluginabi.Error`。`wrapEnvelope` 使用 `errors.As` 识别该类型：

- nested host error envelope：复制 `code`、`message`、`retryable`、`http_status`。
- status-only mapped execution response：使用 `plugin_error`，保留当前包含 status 和 body 的 diagnostic message，并设置精确 `http_status`；不编造 upstream code 或 retryability。
- 普通 parser、rewrite、lifecycle 和内部 Go error：保持现有 `plugin_error` 且无 `http_status` 的行为。

nonstream execution 和 stream preparation 必须采用相同行为。若关闭已建立的 host stream 同时失败，error message 保留 status failure 和 cleanup failure，但 HTTP status 仍来自原始 host response。

ABI 没有 error response headers 字段，因此本次不合成或传播 `Retry-After` 等 headers。

## 测试要求

### Stream boundary regression

- 使用 CPA v7.2.152 Kimi 的两个精确 delimiterless Responses payload 复现，确认输出不含 `JSON}event:`，每个 event 的 `data:` 都是 valid JSON。
- 覆盖 Kimi 一条 content delta 展开的完整 lifecycle event sequence，并确认每个 `response.model` 恢复为 `claude-opus-5`。
- 在 `runStreamForward` fake host integration 中验证真实 read、emit、Done/Error 顺序，而不只直接调用 rewriter。
- 对同一逻辑输入覆盖 one read、one-byte reads、第二条 `event:` prefix 内部切分、JSON token 切分、CR/LF/CRLF 切分和一个 read 内多 event。
- 标准 SSE event 在没有空行且后续仍可能追加字段时不得仅因 read 结束而提前输出。
- payload 与 `Done` 或 `Error` 同 read 时，先处理完整 payload，再执行终止路径。
- 保留 raw JSON、Gemini 和普通 OpenAI/Anthropic stream regression。

### Retained-state limit

- SSE 和 raw JSON 分别用小 fragments 让同一 incomplete unit 超过 16 MiB，确认返回 terminal error、清空 pending state且不 emit partial oversized unit。
- 至少 1 MiB 的完整单 event/raw JSON 成功。
- 许多小完整单元的累计流量超过 16 MiB 仍成功，证明限制针对 incomplete retained unit。
- forwarding integration 确认只关闭 offending stream，并关闭对应 host stream。

### Error propagation

- table-driven 覆盖 400、429、503。
- nested host error 在插件输出 envelope 中保留 `code`、`message`、`retryable`、`http_status`。
- status-only nonstream 和 stream preparation error 保留 status 与原 diagnostic message，code 为 `plugin_error`。
- generic internal error 仍为无 status 的 `plugin_error`。
- successful nonstream response 和 stream setup 不改变。

## 明确排除

### `executor.count_tokens`

CPA 会先接受 model router 的 self target，再调用插件不支持的 `executor.count_tokens`。当前 plugin ABI 没有 `host.model.count_tokens`，Gemini `generateContent` 和 `countTokens` 也没有可供插件可靠区分的 operation signal。Claude-only path filter、body heuristic、硬编码 provider 或用 generation callback 替代 counting 都是不完整或错误的 workaround。完整修复需要 CPA contract 支持，因此本次不修改。

### route/reconfigure race

CPA 在 `model.route` 后只保留 executor plugin ID，没有把 mapped model、config generation 或 host-controlled request token 传入 `ExecutorRequest`。插件无法在 concurrent、retry、cancel 或 reorder 条件下可靠关联 route-time decision。plugin-local queue 或 generation cache 会串请求。完整修复需要 CPA 传递 request-bound route decision，因此本次不修改。

### 其他不纳入项

- 不升级 CPA dependency；已确认的 Kimi framing 缺陷可在当前 pinned v7.2.152 上由插件修复。
- 不增加 YAML hardening；当前 lifecycle config 已验证单 document，且 config 是 operator-controlled boundary，本次没有可执行的 client-triggered finding。
- 不做邻近 refactor，不改变 config schema，不增加新 dependency。

## 完成标准

- 三个纳入问题均先有 focused failing test，再以最小 production change 修复。
- focused regression、全量 test、race、vet、release helper tests 和 package/build checks 全部通过。
- README 只增加用户需要知道的 16 MiB incomplete-unit limit 与 error behavior，不扩展无关文档。
- tracked diff 只包含本次 spec、plan、tests、修复、必要 README 和 patch release 文件。
