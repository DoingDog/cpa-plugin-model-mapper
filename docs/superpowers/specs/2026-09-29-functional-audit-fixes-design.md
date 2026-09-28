# v0.5.8 功能审计修复设计

日期：2026-09-29

## 目标与已确认事实

本次只修改 model-mapper 插件及其打包脚本，不修改 CPA。基于 `origin/main` 的 `v0.5.7` 审计确认两个可在本仓库修复的问题：

1. CPA v7.2.152 在处理 Claude `/v1/messages/count_tokens` 时先调用 `model.route`，命中插件自身执行器后调用 `executor.count_tokens`。插件当前对该方法返回 `unsupported`，但 `handleModelRoute` 对已配置模型映射仍返回 `Handled:true`。本来能由 CPA 原生计数的已注册模型因此报错。依据：`main.go:1458-1476,2033-2049`；CPA `sdk/api/handlers/handlers_execution.go:119-123,230-255`。CPA 在 `metadata.request_path` 中提供该端点的精确路由路径，来源为 Gin `FullPath()`。
2. 打包脚本在写 zip 前检查 library、archive、checksum 的路径别名，聚合模式检查 binary 和输出，但没有将要读取的 `LICENSE` 和已校验的 `*.version` 纳入检查。若输出与这些输入有相同目标或硬链接，`os.Create(archivePath)` 会截断输入，生成错误归档或破坏后续聚合打包所需的 sidecar。依据：`.github/scripts/package-release.go:54-70,233-299,357-397`。

## 行为设计

- `handleModelRoute` 遇到由 CPA 提供的 `metadata.request_path == "/v1/messages/count_tokens"` 时返回 `Handled:false`，不调用规则映射，也不选择本插件执行器。CPA 因而保留原始模型并走其原生计数路径。非计数端点，包括 `/v1/messages`，继续使用既有规则与转发流程；缺少该路径元数据时不推断请求类型。
- 单平台打包开始删除 checksum 或创建归档前，现有 `validateDistinctPaths` 必须同时检查 `LICENSE` 与 library、archive、checksum。`LICENSE` 即使尚不存在，也须避免输出目标恰好创建同名文件后又把未完成归档作为 LICENSE 读取。
- 聚合打包在删除 checksum、写出任意归档或删除过时归档前，将每个找到的库对应的已读 `.version` 路径及 `LICENSE` 加入现有别名检查，与所有候选输出进行比较。沿用 `canonicalPackagePath` 和 `os.SameFile`，无需新配置、新依赖或重构其他路径。
- 未改变现有通配符不回溯语义：`main_test.go:1152` 明确约定该行为。SSE 已识别 CR/LF/CRLF，末尾无空行数据在 flush 时未补分隔符；C host API 在 CPA 客户端生命周期内持有有效指针，这些不是修复项。

## 可验证用例

- 首先添加失败测试：配置 `claude` 的 A=>B 映射，向 `handleModelRoute` 发送带 `metadata.request_path=/v1/messages/count_tokens` 的 CPA 请求，应 `Handled:false`；相同模型与配置在 `/v1/messages` 应 `Handled:true`；无路径元数据维持既有行为。测试明确展示当前失败，再修改插件代码使其通过。
- 首先添加不会触发归档自复制的失败测试：在隔离临时目录让单平台 checksum 路径等于 `LICENSE`，并让聚合模式 archive 硬链接到 `*.version`；执行打包，应在首次写入前报 `package paths must be distinct`，保留原始输入字节，且不生成本次其他输出。修复后再测试 archive 与 `LICENSE` 的硬链接/软链接别名，避免在旧实现上运行可能无限自复制的输入；原有合法路径仍应成功。
- 完整执行 `go test ./...`、`go test -race ./...`、`go vet ./...`、打包/兼容性/冒烟辅助脚本测试、真实 CPA 插件注册与执行集成测试。检查 git diff 与发布包。完成后合并本地 `main`，标记并发布 `v0.5.8`，推送触发 GitHub workflow，核对构建与发布结果。

## 明确边界

CPA 的 Gemini `:countTokens` 与 `:generateContent` 共用 `metadata.request_path=/v1beta/models/*action`，路由请求也不包含可可信区分的动作字段。只在插件侧无法精确绕过 Gemini 计数而保留其普通非流式模型映射；本次不修改 CPA，不禁用 Gemini 的生成映射，也不以普通模型执行冒充计数。此限制必须在最终结果说明。对于缺失 Gin 元数据的直接 SDK count 调用，同样无法可靠识别请求类型。

无证据的性能优化、旧文档中已有的设计以及对既定 DSL 语义的修改不在本次范围内。
