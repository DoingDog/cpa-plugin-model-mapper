# v0.5.8 功能审计修复实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复映射规则导致 Claude 计数请求失败，以及打包输出覆盖 `LICENSE` / `.version` 的两处已确认缺陷，完成验证并发布 `v0.5.8`。

**Architecture:** 在既有 `handleModelRoute` 入口依据 CPA 可信的 `metadata.request_path` 跳过 Claude 计数；在现有打包前置别名检查内纳入被读取的输入路径。两个任务互不修改相同文件，可并行进行 TDD，由主协调者整合并依次提交。

**Tech Stack:** Go 1.26、现有 `pluginapi`/`pluginabi`、Go 标准库、GitHub Actions。

**Spec:** `docs/superpowers/specs/2026-09-29-functional-audit-fixes-design.md`

## Global Constraints

- 基线为 `origin/main` / `v0.5.7`；只修改本仓库，不修改 CPA 本体或旧计划/spec 文件。
- 不改变模型 DSL 的不回溯通配符语义，也不改变正常 Claude、Gemini 生成请求的映射。
- 当前 CPA v7.2.152 的 Gemini `:countTokens` 与生成请求同为 `/v1beta/models/*action`，无法在现有插件入参下安全地区分；不得以禁用 Gemini 映射或执行生成来假装修复。最终交付应说明此上游接口限制。
- 两组代码任务由独立 Sonnet 1M、xhigh 工作者分别负责，仅读取本次 spec/plan 与本组指定文件；禁止读取任何旧 plan/spec，也不得同时修改或读取另一组文件。Opus 1M、xhigh 负责计划和审查。工作者不提交、不切分支、不推送。
- 只改与确认问题和回归测试直接相关的行；每个修复先测出红，再实施，再测绿。未确认的性能优化不进入发布。

## Review Focus

- 计数路径提供为字符串以外的元数据值时，不应错误地跳过普通映射；Task 1 的表驱动测试覆盖。
- 有 `request_path` 但路径为 `/v1/messages` 时，原有映射继续工作；Task 1 覆盖。
- Gemini `/v1beta/models/*action` 请求不能被误认为 Claude 计数；Task 1 覆盖。
- 单平台 checksum 恰好等于不存在的 `LICENSE` 时，也必须在写入前拒绝；Task 2 覆盖。
- 聚合输出指向其他平台的 sidecar 或 `LICENSE` 硬链接时，必须在删除旧 checksum 前拒绝；Task 2 覆盖。

---

### Task 1: Claude 计数路由退出插件执行器

**Files:**
- Modify: `main.go:1458-1476`
- Test: `main_test.go:1726-1775` 附近

**Interfaces:** 消费 CPA 已有 `pluginapi.ModelRouteRequest.Metadata map[string]any` 中的 `request_path` 字符串；保持 `handleModelRoute([]byte) ([]byte,error)` 返回格式不变。

- [ ] **Step 1: 编写失败的表驱动测试**，使用仓库现有 `setLoadedConfigForTest`、`pluginapi.ModelRouteRequest` 和 JSON 响应解码。测试中包含以下数据；`Metadata` 为 nil 时直接传 nil，非字符串时放 `map[string]any{"request_path": 1}`。逐条检查 `resp.Handled`，正常路径额外检查 `TargetKind==pluginapi.ModelRouteTargetSelf`：

```go
cases := []struct {
    name, format, path string
    metadata map[string]any
    wantHandled bool
}{
    {name: "Claude count", format: "claude", metadata: map[string]any{"request_path": "/v1/messages/count_tokens"}},
    {name: "Claude messages", format: "claude", metadata: map[string]any{"request_path": "/v1/messages"}, wantHandled: true},
    {name: "missing path", format: "claude", wantHandled: true},
    {name: "non-string path", format: "claude", metadata: map[string]any{"request_path": 1}, wantHandled: true},
    {name: "Gemini wildcard action", format: "gemini", metadata: map[string]any{"request_path": "/v1beta/models/*action"}, wantHandled: true},
}
// setLoadedConfigForTest(Config{GlobalRules: "registered=>upstream"})
// Marshal pluginapi.ModelRouteRequest{SourceFormat: tt.format, RequestedModel: "registered", Metadata: tt.metadata}
// Unmarshal handleModelRoute(raw) into pluginapi.ModelRouteResponse; compare Handled with tt.wantHandled.
```

- [ ] **Step 2: 红灯。** `go test . -run '^TestHandleModelRouteSkipsClaudeCountTokens$' -count=1` 必须在 Claude count 子测试返回 `Handled:true` 时失败。记录具体输出，不因其他用例失败误判。
- [ ] **Step 3: 最小实现。** 在 `handleModelRoute` 的 JSON 解码之后、现有 `interactions` 和规则解析之前，仅对 `req.Metadata["request_path"] == "/v1/messages/count_tokens"` 返回 `json.Marshal(pluginapi.ModelRouteResponse{Handled:false})`；Go `any` 与字符串常量比较对非字符串值合法且结果为 false，但如用类型断言应确保非字符串不跳过。不改路由缓存、executor 或配置接口。

```go
if req.Metadata["request_path"] == "/v1/messages/count_tokens" {
    return json.Marshal(pluginapi.ModelRouteResponse{Handled: false})
}
```

- [ ] **Step 4: 绿灯。** 重跑上述单测、`go test ./...`，确认现有注册/映射/交互测试仍通过；不提交，交付给主协调者作跨域审查。

### Task 2: 发布打包前禁止输出与输入别名

**Files:**
- Modify: `.github/scripts/package-release.go:54-70,233-299`
- Test: `.github/scripts/package-release_test.go:276-340,594-686` 附近

**Interfaces:** 复用 `validateDistinctPaths(paths ...string)`；不更改 CLI 参数、版本格式、归档文件名或 checksum 格式。

- [ ] **Step 1: 编写不会触发旧实现归档自复制的两个失败测试。** 单平台在 `t.TempDir` 内调用 `t.Chdir(dir)`，写一个库和 `LICENSE` 文本，令 checksum 与 `LICENSE` 是相同路径；聚合写 `dist/linux_amd64/model-mapper.so`、其 `.version` 为 `0.5.8\n`，创建 `out/model-mapper_0.5.8_linux_amd64.zip` 与 `.version` 的硬链接，调用 `packageExistingArtifacts("0.5.8",dist,out)`。断言错误包含 `must be distinct`、输入原始内容未变，并断言其他输出未生成/原有 checksum 保持原值。`os.Link` 不可用时只跳过对应硬链接子测试：

```go
// 单平台：run([]string{"-library", library, "-archive", archive, "-checksum", filepath.Join(dir, "LICENSE")})
// 聚合：os.Link(library+".version", filepath.Join(out, "model-mapper_0.5.8_linux_amd64.zip"))
// 聚合：packageExistingArtifacts("0.5.8", dist, out)
// 对两种情况均先检查 err != nil，再检查 strings.Contains(err.Error(), "must be distinct")。
```

- [ ] **Step 2: 红灯。** `go test .github/scripts/package-release.go .github/scripts/package-release_test.go -run 'TestRunRejectsLicenseOutputAlias|TestPackageExistingArtifactsRejectsVersionSidecarAlias' -count=1` 必须失败且能展示旧实现误删/截断输入；若出现归档自复制或路径不符合预期，停止并改为安全测试输入。
- [ ] **Step 3: 最小实现。** 单平台扩充写入之前的检查：`validateDistinctPaths(*libraryPath, *archivePath, *checksumPath, "LICENSE")`。聚合路径列表在已有库和全部候选 zip/checksums 路径之外追加每个已找到库的 `artifact.binaryPath(distDir)+".version"` 及 `"LICENSE"`；保证在 `os.MkdirAll(outDir)` 和 `removeChecksum` 前执行检查。复用现有软链接解析和 inode 同一性检测，不增加第二套规范化逻辑。

```go
paths = append(paths, filepath.Join(outDir, "checksums.txt"), "LICENSE")
for _, artifact := range artifacts {
    paths = append(paths, artifact.binaryPath(distDir)+".version")
}
if err := validateDistinctPaths(paths...); err != nil { return err }
```

- [ ] **Step 4: 绿灯并补保护边界。** 红灯测试通过后，添加 archive 与 `LICENSE` 硬链接/软链接别名、`LICENSE` 不存在但输出路径为 `LICENSE`、聚合其他平台 sidecar 别名的测试；此时再运行这些可能在旧实现上自复制的案例。检查拒绝时输入字节不变。运行完整 packager 测试及 `go test ./...`，确认正常包仍包含完整 `LICENSE`。不提交，交付给主协调者。

### Task 3: 整合审查、验证与发布

**Files:**
- Review: `main.go`, `main_test.go`, `.github/scripts/package-release.go`, `.github/scripts/package-release_test.go`, 本次 spec 和 plan。
- No unrelated code changes.

- [ ] **Step 1: Opus 1M xhigh 按两个已确认缺陷独立审查新 diff。** 首先核对每个失败测试曾呈红色、实现最小、未引入跨文件回归；发现缺陷时只修本次代码并重新测红/绿。拒绝把 `main_test.go:1152` 的既定通配符语义当成 bug。
- [ ] **Step 2: 本地验证。** 运行 `go test ./...`、`go test -race ./...`、`go vet ./...`，并按 `CLAUDE.md` 分别运行三个 `.github/scripts` 测试对。运行 `go build -buildmode=c-shared -o <用户临时目录>/model-mapper.dll .` 与 CPA v7.2.152 `./cmd/server` 的真实 `TestCPAPluginIntegration`；若当前 Git Bash 的 `make` 清理 Windows 环境变量，直接调用等价 Go 命令并如实记录，不能把环境失败写成通过。
- [ ] **Step 3: 只暂存上述四个源码/测试文件和本次两个文档，检查 `git diff --check`、`git diff --cached --stat`、`git status`，提交明确的修复提交；保留原本未跟踪的 `.claude/`，不触碰其他工作树。
- [ ] **Step 4: 将审核通过的分支整合到本地 `main`。** 先确认 `origin/main` 未发生新提交及本地 main 与审计基线的快进关系；`git switch main` 后以 `git merge --ff-only audit/functional-20260929` 合并，不使用重置或强推。
- [ ] **Step 5: 发布修复版。** 确认 `v0.5.8` 标签及 GitHub Release 尚不存在；在 main HEAD 创建带说明的 `v0.5.8` tag，依次推送 main、tag 触发 `.github/workflows/build.yml`。检查 GitHub main 与 tag 两次 workflow 的 Test、全平台 build、Release，校验发布 zip/checksums 数量与版本；如 workflow 失败，定位后追加修复提交及新修复版本，不覆盖既有 tag。
- [ ] **Step 6: 最终报告。** 列出确证缺陷、修复、真实验证命令及结果、提交/tag/CI/发布地址；明确 Gemini count 与无 Gin 元数据的 SDK count 限制，确认旧 `.claude/` 未受影响。缺少任何一项验证都如实标记。
