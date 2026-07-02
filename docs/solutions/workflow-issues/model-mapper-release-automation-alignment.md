---
title: Align model-mapper release automation with plugin packaging requirements
date: 2026-07-02
category: workflow-issues
module: model-mapper release automation
problem_type: workflow_issue
component: development_workflow
severity: medium
applies_when:
  - Publishing CPA native plugins through GitHub Actions
  - Separating PR validation, main-branch builds, and v* tag releases
  - Packaging cross-platform plugin artifacts with injected versions, checksums, and LICENSE files
symptoms:
  - Release automation needed to match plugin repository artifact and metadata expectations
  - Cross-platform build packaging, checksums, LICENSE inclusion, version injection, permissions, and release gating had to be aligned before publishing
root_cause: missing_workflow_step
resolution_type: workflow_improvement
related_components:
  - tooling
  - testing_framework
tags:
  - model-mapper
  - cpa-plugin
  - github-actions
  - release-automation
  - cross-platform-builds
  - checksums
  - version-injection
  - packaging
---

# Align model-mapper release automation with plugin packaging requirements

## Context

`model-mapper` 需要把 GitHub 自动化补齐到符合 `CLIProxyAPI-Plugins-Store` 的发布要求，并参考 `cpa-plugin-usage-keeper/.github/scripts` 做成可复用的全平台构建、打包、发版流程。最终目标不是单纯让 CI 变绿，而是让 tag release 能产出插件商店可消费的动态库 zip、校验和、版本元数据和许可文件。

早期会话已试过把 release zip/checksum 逻辑放进仓库内 Go 脚本，并在 workflow 中串起 `go test ./...`、`go vet ./...`、build/package 阶段；这些方向保留了下来，但最终仍要用当前代码和插件仓库规范补齐 LICENSE、module path、version injection、权限与 gating 等细节（session history）。

这次已验证的结果是：`model-mapper` 发布到 `DoingDog/cpa-plugin-model-mapper`，最终 release `v0.1.2` 成功，release assets 包含 7 个平台 zip 和 `checksums.txt`。

这类问题同时牵涉 GitHub Actions 触发条件、Go 动态库构建、跨平台打包、版本注入、module path、许可证、checksum 格式和本地/线上验证。任何一处不一致，都会让自动化“看起来完成”，但发布产物不能被插件商店稳定消费。

## Guidance

把插件仓库的发布自动化拆成三层：CI 触发策略、单平台产物打包器、版本/元数据一致性。

1. **GitHub Actions 触发策略要分清测试、构建、发版。**

   `PR` 只跑 `test` / `vet`，避免在未合并代码上构建或发版。`main push`、`tag`、`workflow_dispatch` 才构建。只有 `v*` tag 才发 release。

   权限保持最小化：全局只给 `contents: read`，只有 release job 提升到 `contents: write`。

   ```yaml
   permissions:
     contents: read

   jobs:
     build:
       if: github.event_name != 'pull_request'

     release:
       if: startsWith(github.ref, 'refs/tags/v')
       permissions:
         contents: write
   ```

2. **平台矩阵覆盖常规 runner，补齐特殊目标。**

   常规矩阵覆盖：

   - `linux/amd64`
   - `linux/arm64`
   - `darwin/amd64`
   - `darwin/arm64`
   - `windows/amd64`

   另外用 `go-cross` 补齐：

   - `windows/arm64`
   - `freebsd/amd64`

   这样 release 里能稳定产出 7 个平台 zip，而不是只覆盖 GitHub hosted runner 最容易构建的组合。

3. **`.github/scripts/package-release.go` 同时支持单平台打包和聚合。**

   单平台模式接收：

   - `-library`
   - `-archive`
   - `-checksum`

   每个平台产物命名为：

   ```text
   model-mapper_<version>_<goos>_<goarch>.zip
   ```

   zip 根目录只放动态库和可选 `LICENSE`，不要额外包一层目录。checksum 使用 `sha256sum` 兼容格式，并且只写 archive 的 basename：

   ```text
   <sha256>  model-mapper_0.1.2_windows_amd64.zip
   ```

   哈希计算使用文件流读取，不把整个 archive 一次性读进内存。

4. **打包器必须在 host Go 环境下运行。**

   构建动态库时可以设置目标 `GOOS` / `GOARCH`，但运行 Go 写的打包器时不能继承这些目标环境。否则在 Windows 上残留 `GOOS=linux` 会让 `go run` 生成 Linux 可执行文件，再由 Windows shell 执行，最终报类似：

   ```text
   executable file not found in %PATH%
   ```

   `Makefile` 中的关键修复是清空目标环境后再运行打包器：

   ```make
   VERSION_LDFLAGS := -X main.pluginVersion=$(VERSION)

   package-platform:
   	GOOS= GOARCH= CGO_ENABLED= go run ./.github/scripts/package-release.go ...
   ```

5. **版本号从 release 流程注入到插件 metadata。**

   `main.go` 保留开发默认值：

   ```go
   var pluginVersion = "0.0.0-dev"
   ```

   release 构建时通过 linker flags 注入：

   ```make
   VERSION_LDFLAGS := -X main.pluginVersion=$(VERSION)
   ```

   registration metadata 使用这个注入值。这样本地开发不会假装是正式版本，release 产物又能携带真实 tag 版本。

6. **仓库身份、许可证和 release 产物要一致。**

   `go.mod` 的 module path 需要和 GitHub repository 对齐：

   ```text
   github.com/DoingDog/cpa-plugin-model-mapper
   ```

   `LICENSE` 使用 `The Unlicense`，并被包含进 release zip。插件商店和下游用户拿到单个 zip 时，也能直接看到许可信息。

7. **脚本测试要按文件级别跑。**

   `.github/scripts` 目录里有多个 `package main` 脚本时，不能直接跑整个目录：

   ```bash
   go test ./.github/scripts
   ```

   这会因为多个脚本重复定义 `main` / `run` 等符号失败。正确方式是只测试目标脚本和对应测试文件：

   ```bash
   go test .github/scripts/package-release.go .github/scripts/package-release_test.go
   ```

## Why This Matters

全平台 release 自动化的失败通常不是“CI 没跑”，而是产物边界没定义清楚：文件名不稳定、zip 结构多一层目录、checksum 写了绝对路径、版本还是 dev、module path 指向旧仓库、PR 流程拿了不该拿的 release 权限，都会让发布流程变脆。

这次修复把几个容易漂移的契约固定下来：

- `PR` 安全，只验证代码，不产出 release 资产。
- `v*` tag 是唯一发版入口，release 权限只在 release job 打开。
- 每个平台 zip 的命名和内容稳定，便于插件商店索引。
- checksum 用 basename，跨机器、跨目录都可复现。
- Go 打包器在 host 环境运行，避免目标 `GOOS` 污染 `go run`。
- 插件 metadata、tag、archive 名和 module path 指向同一个发布身份。
- LICENSE 随 zip 交付，避免 release 资产脱离仓库上下文后丢失授权信息。

## When to Apply

适用于这些情况：

- Go 写的 CLIProxyAPI 插件需要发布动态库形式的 release assets。
- 仓库要对齐 `CLIProxyAPI-Plugins-Store` 的插件发布/索引要求。
- GitHub Actions 需要同时支持 PR 验证、main 构建、tag 发版和手动触发。
- 产物要覆盖 `linux`、`darwin`、`windows`、`freebsd` 的多个架构组合。
- `.github/scripts` 下有多个 Go `package main` 脚本，需要单独测试其中一个脚本。
- Makefile 在同一目标中既要交叉编译，又要运行 host 侧 Go 工具。
- release zip 需要包含动态库、checksum 和 LICENSE，并保持根目录结构稳定。

不需要套用到只发布源码包、没有跨平台动态库、或不需要 GitHub Release assets 的普通 Go 库；那会把简单 CI 过度做成发布系统。

## Examples

### PR、构建、发版分离

推荐形态：

```yaml
on:
  pull_request:
  push:
    branches: [main]
    tags: ['v*']
  workflow_dispatch:

permissions:
  contents: read
```

再用 job-level 条件控制：

```yaml
build:
  if: github.event_name != 'pull_request'

release:
  if: startsWith(github.ref, 'refs/tags/v')
  permissions:
    contents: write
```

这样 PR 不会误触 release，tag release 又有足够权限上传资产。

### package-release 的单平台输出

单平台打包器应接收明确输入输出，而不是从 CI 环境里隐式猜路径：

```bash
go run ./.github/scripts/package-release.go \
  -library dist/model-mapper.dll \
  -archive dist/model-mapper_0.1.2_windows_amd64.zip \
  -checksum dist/model-mapper_0.1.2_windows_amd64.zip.sha256
```

zip 根目录示例：

```text
model-mapper.dll
LICENSE
```

checksum 示例：

```text
6b...  model-mapper_0.1.2_windows_amd64.zip
```

注意 checksum 里不要写 `dist/...` 或绝对路径，只写 basename。

### 避免 `GOOS` 污染 `go run`

容易出错的形态是构建目标环境残留到打包器：

```make
GOOS=$(GOOS) GOARCH=$(GOARCH) go run ./.github/scripts/package-release.go ...
```

在 Windows host 上，如果残留 `GOOS=linux`，`go run` 会生成 Linux 可执行，Windows 无法执行。

修复方式是让打包器回到 host 环境：

```make
GOOS= GOARCH= CGO_ENABLED= go run ./.github/scripts/package-release.go ...
```

### `.github/scripts` 的测试方式

错误方式：

```bash
go test ./.github/scripts
```

当目录里有多个 `package main` 脚本时，会重复定义入口函数。

正确方式：

```bash
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
```

同时，覆盖这些行为最有价值：

- version 去掉前缀 `v`。
- 平台矩阵包含预期目标。
- zip 根目录包含动态库和 `LICENSE`。
- checksum 行只包含 archive basename。

新增 `LICENSE` 后，测试需要在临时目录里 `t.Chdir(dir)` 并写入临时 `LICENSE`，否则可选文件逻辑在测试环境找不到仓库根目录下的许可证。

### 验证清单

本地验证至少覆盖：

```bash
go test ./...
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
```

还要实际打包并检查 zip：

- Windows `amd64` 打包成功。
- Linux `amd64` 打包成功。
- zip 根目录没有额外目录层级。
- checksum 文件内容是 `sha256sum` 格式。

最终仍要以 GitHub Actions 实跑为准。之前担心的 Windows bash / MSYS2 静态风险，最后通过 Actions main、tag、release 实跑成功排除。

## Related

- `.github/workflows/build.yml` — CI、构建、release 触发条件和权限边界。
- `.github/scripts/package-release.go` — 单平台打包、aggregate package、zip、checksum 逻辑。
- `.github/scripts/package-release_test.go` — version、矩阵、zip 根目录、checksum basename 覆盖。
- `Makefile` — `VERSION_LDFLAGS` 和 host 环境下运行打包器的修复。
- `main.go` — `pluginVersion = "0.0.0-dev"` 默认值和 registration metadata 版本注入。
- `go.mod` — module path 对齐 `github.com/DoingDog/cpa-plugin-model-mapper`。
- `LICENSE` — `The Unlicense`，随 release zip 一起分发。
- `cpa-plugin-usage-keeper/.github/scripts` — 参考实现来源。
- `DoingDog/cpa-plugin-model-mapper` release `v0.1.2` — 最终验证成功的发布结果。
- GitHub issue search — `gh issue list --search` 没有返回相关 issue。
