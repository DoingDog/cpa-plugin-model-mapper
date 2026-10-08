# Task I：native integration 生命周期实施计划

## 固定输入和执行边界

依据 `docs/superpowers/specs/2026-10-08-native-integration-lifetime-design.md`。动态调查和完整 spec 自审已先于本计划完成，证据为 `dist/task-I-native-lifetime/spec-self-review.json`。

- BASE：`2a717cb99776e767c9168bda21162730ac5f5dd9`。
- owner worktree：`C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/wf_a6144e64-63a-2`。
- 固定 CPA：`c76dfd4e0edabab9000628b1560ab8ab379eadb8`，`v7.2.152`，Sum `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`。
- Linux SDK：`/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go`。
- Windows SDK：`F:/go-sdk/go1.26.5/bin/go.exe`，CC `C:/msys64/ucrt64/bin/gcc.exe`。
- Linux 临时根：`/var/tmp/cpa-task-I-native.aHpB61et`；Windows 临时根：`C:/Users/user/AppData/Local/Temp/cpa-task-I-windows-jq615c12`。仅这两个本任务目录允许写入和后续清理。
- Go 命令使用证据目录的 `linux-env-confined.sh` 或 `windows-env-confined.json`。`TEST_TELEMETRY_DIR` 已由两个实际 SDK 的 `go env -json` 核验。
- 每项命令使用 `run_command.py` 或等价逐命令记录器保存 argv、cwd、实际数字 exit、开始/结束时间、日志和 SHA256。失败日志不覆盖。

不派新代理，不向用户提问，不修改其他 worktree、CPA 原 checkout、SDK、全局状态或系统组件，不执行发布及外向操作。

## 1. 永久 focused RED

文件：`.github/scripts/testdata/cpa-functional-regression_test.go`。

在 `TestModelMapperFunctionalReconfigureReload` 的原内容之前添加 canonical lifetime 子测试。两个顺序 owner 分别真实加载并执行 native `Executor.Execute`：

- parent 文件存在，读取 FileInfo 和 SHA256。
- 每个 owner 使用不同 callback 响应和计数，确认 upstream `grok-4.7`、原请求 top-level model 改写、响应恢复 `grok-4.6` 和 opaque text 保持。
- active record 的 library 与 parent `os.SameFile`。
- 子测试 cleanup 后核查 parent 文件身份和 hash 保持；下一 owner 重新 New Host/ApplyConfig/init。

此阶段不修改 helper。保留实际 RED fixture 快照和 SHA256，显式生成 overlay Replace 到当前未提交源码，使用调查阶段同一 BASE `.so` 编译固定 CPA test executable。

focused 命令：

```bash
go -C "$native/dist/integration/cpa-src" test -mod=readonly -overlay "$out/focused-red-overlay.json" -c -o "$native/diagnostic/focused-red-cpa.test" ./internal/pluginhost
CPA_SMOKE_PLUGIN="$native/diagnostic/plugins/linux/amd64/model-mapper.so" "$native/diagnostic/focused-red-cpa.test" -test.run='^TestModelMapperFunctionalReconfigureReload$/^canonical-lifetime$' -test.count=1 -test.v -test.timeout=60s
```

核查：实际 executable exit 非零，失败来自不同 physical library identity，真实加载先发生；不能把构建、环境或解析失败记为 RED。原 24/2 顶层集合不变。

## 2. 最小 helper 实现和 focused GREEN

只修改 `gCPALoadNative` 中 library 路径和文件准备部分：

- `CPA_SMOKE_PLUGIN` 转绝对路径，读取真实文件。
- 从 `plugins/<GOOS>/<GOARCH>/model-mapper.<ext>` 推导 plugins directory 并验证路径结构。
- 删除每次 `t.TempDir`/MkdirAll/WriteFile 和冗余 copy/read-back，只读 parent canonical 文件。
- New Host、SetModelExecutor、原 YAML、ApplyConfig、active checks、cleanup 保持。
- 日志记录 canonical path 和 SHA256。

保存 GREEN fixture，重新绑定 overlay，编译并运行同样 focused 命令。运行完整 `ReconfigureReload` 及原两个重复加载对照：

```bash
"$native/diagnostic/focused-green-cpa.test" -test.run='^TestModelMapperFunctionalReconfigureReload$' -test.count=1 -test.v -test.timeout=60s
"$native/diagnostic/focused-green-cpa.test" -test.run='^TestModelMapperFunctionalNativeRequestDuplicates$' -test.count=320 -test.v -test.timeout=600s
"$native/diagnostic/focused-green-cpa.test" -test.run='^TestModelMapperFunctionalOrdinaryMetadata$' -test.count=2 -test.v -test.timeout=600s
```

核查：GREEN exit 0；current owner callback、SameFile、parent survival、原 schema/metadata/five formats/reconfigure/explicit reload 均通过；artifact SHA256 与 RED 相同。`gofmt` 只针对本文件，`git diff --check` 和完整 diff 确认没有无关修改。

## 3. 两个平台完整 integration

Linux 的任务 clean clone 固定在 BASE。完整运行前只把当前 fixture 复制到 clone 的同一相对路径，逐字节核对原始 worktree、复制文件和 overlay Replace source 的 SHA256，并保存源 manifest。产品文件逐字节核对 BASE，不把未提交修改假定已进入 clone。

Linux：

```bash
source "$out/linux-env-confined.sh"
export CPA_FUNCTIONAL_EVIDENCE="$out/linux-integration-evidence"
make -C "$native/plugin-src" integration DIST_DIR="$native/dist"
```

Windows：从当前 owner worktree 使用已固定的自有 CPA checkout，确保 PATH 的第一个 Go 为 `F:/go-sdk/go1.26.5/bin`，make 和 CC 指向现有安装；使用自有缓存和 `CPA_FUNCTIONAL_EVIDENCE`：

```bash
make integration GO=F:/go-sdk/go1.26.5/bin/go.exe DIST_DIR=C:/Users/user/AppData/Local/Temp/cpa-task-I-windows-jq615c12/dist
```

两个平台的 evidence 使用不同新目录，不能覆盖。核查原 make 的 CPA revision/clean guard 正常运行，默认版本真实 build、CPA process、HTTP/WS、overlay test 全部 exit 0。

从保存的 `cpa-overlay.log` 严格解析 24 functional 和 2 bridge 的顶层 RUN/PASS 集合，unknown/duplicate/missing 均为空。标准 JSON parser 读取四份 captures，在原测试定义的完整 key 上检查集合、重复和缺失，数量分别 1536/1800/1440/1680。原 overlay 同时执行全部 body/header/model/upstream 身份断言；报告保留完整原始数据和执行日志。核查新增 canonical-lifetime 的两个 owner、disabled/direct、16 MiB incomplete、16,842,937-byte 完整 translator unit、error/EOF/backpressure/terminal/zero-state 的原子测试均通过。

## 4. root、scripts、build 和性能

在 Windows 和固定 Linux SDK 上逐项执行并单独保存记录：

```bash
go test -count=1 ./...
go vet ./...
go test -race -count=1 -timeout=30m ./...
go test -count=1 .github/scripts/package-release.go .github/scripts/package-release_test.go
go test -count=1 .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
go test -count=1 .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
go test -count=1 -run '^Test.*(Allocation|Allocations|Performance)' .
go test -run '^$' -bench . -benchtime=1x -benchmem .
```

诊断证据目录包含 CPA fixture 快照和独立工具源码，在该已忽略目录内用临时 `go.mod` 划分 module，使原 `./...` 只扫描插件源码，不修改产品 `go.mod` 或删减包集合。首次 Windows root/vet 的诊断目录 setup failure 和两平台 race 默认 10 分钟 timeout 均保留完整日志；重新执行 Windows 完整 root/vet，以及两个平台 `-timeout=30m` 的完整 race。增加的是 instrumented suite 总时限，原测试内部关闭/请求时限、数据规模和断言不变。

性能核查使用原完整 root tests 及全部 benchmark preflight，确认 original 2 MiB 200 allocations 断言没有变化。不重做原旧版本对照，不声称 CPU 独占。Windows/Linux 各自的 race 与常规 native integration 分开报告。

实际 integration build 验证默认注册/schema/metadata。额外用两个平台真实 c-shared build 注入 `VERSION=0.5.12` 到本任务新 dist directory，使用真实 ABI loader init/register 检查 metadata Version，capabilities formats、schema、config fields 和 ABI version。该字符串仅用于现有版本注入机制验证，不创建或变更 tag/release。

产品和 benchmark 源码包括根 `.go`、`go.mod`、`go.sum`、Makefile、父 smoke 入口、workflow 与 BASE 逐字节比较。若出现意外差异，停止提交并调查；不扩大任务范围。

## 5. 规格、质量自审和复审

owner 连续完成，写 JSON 审查记录，不生成报告类 Markdown。

- 逐条覆盖 spec 和 brief 的条件及实际 evidence；每个成功结论具有真实 exit 和 source/artifact/CPA identity。
- 核查全部 native callers，enabled cleanup 先于下一 enabled init，原 enabled/disabled 共存保持，禁止把相同路径当并行隔离。
- 检查 focused 测试能真实发现 copy 退化，callback 当前 owner 和 parent survival 断言存在；新实现无 cached Host、写 library、可配置项或新增依赖。
- 原源码差异只含新子测试、helper 必需修改、本 spec/plan；原内容断言保持。
- 覆盖 24/2/6456、所有原边界、root/vet/race/scripts/build/performance 和平台限制。
- 必要修正后重跑受影响测试和完整覆盖核查，再复审。owner 自审不作为 controller 独立审批。

## 6. 提交和正式交付

确认本 worktree 当前变更，按具体路径 stage fixture/spec/plan。运行 staged diff --check、完整 staged diff 自审，再执行正常 commit，不绕 hook/signing。保留 `Refs: #8`、`Related-PR: #7`、`PR-Author: @leolmq`，不虚构 co-author。

提交后核查 branch、HEAD、BASE 祖先关系、干净状态和 `BASE..HEAD` 全部 diff --check。生成 `task-I-review-package.txt`，内容为实际 BASE..HEAD 的完整 git log、diff --stat 和 diff -U10，不使用 HEAD~1。

生成可核查 source/evidence manifests 和正式 `task-I-owner-report.json`。报告包含所有实际成功、失败、guard 拒绝、未启动/未知 exit、修正记录、平台环境、输入/源/产物/CPA 身份、RED/GREEN、精确覆盖、自审、package/hash、限制和实际截止时间状态。报告 hash 在最终结构中返回，避免自包含 hash。

保留正式可复查证据，清理本任务 OS 临时 clones/caches/copies，仅删除本任务创建内容。报告必须声明 Debian 不替代 Ubuntu24.04 CI、signal 唯一机制未证实、七平台 CI/runtime/Release 和 controller 两门/最终分支复审/main 整合/评论尚未执行。全部 owner 完成条件达成时 `canReview=true`，未完成则返回实际阻塞状态，不把后续未完成步骤标为 DONE。
