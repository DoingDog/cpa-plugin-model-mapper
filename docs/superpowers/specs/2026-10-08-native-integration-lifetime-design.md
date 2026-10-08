# Task I：native integration 动态库生命周期

## 目标和范围

修复 integration fixture 在一个进程中不断加载不同 Go c-shared 动态库实体的生命周期问题，完整保留原功能修复的请求、流和生命周期验收。起点为 `2a717cb99776e767c9168bda21162730ac5f5dd9`。

永久修改范围为 `.github/scripts/testdata/cpa-functional-regression_test.go` 和本任务 spec/plan。父入口 `.github/scripts/smoke-local_test.go` 已提供整个 overlay 运行期间存活且经过 SHA256 核验的动态库，因此无需修改。产品源码、benchmark、CPA、workflow、依赖和发布资产保持不变。

本任务只研究正常 native loader、runtime、请求和流关闭，不研究网络安全，不修改系统、安装组件、SDK、既有缓存或 CPA 原 checkout。不执行 push、tag、dispatch、CI rerun、Release 或评论。已推送 `v0.5.12` 保持不变。独立规格、质量及最终分支审查由 controller 后续执行。

## 动态依据

本任务证据保存在已忽略的 `dist/task-I-native-lifetime/`，原始命令、日志、源码、产物和 SHA256 均保留。

实际环境为 WSL Debian 13.6、glibc 2.41、GCC 14.2.0、固定 Go 1.26.0。CPA revision 为 `c76dfd4e0edabab9000628b1560ab8ab379eadb8`，module 为 `github.com/router-for-me/CLIProxyAPI/v7 v7.2.152`，Sum 为 `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`。固定 SDK 只读，公开依赖下载到本任务自有缓存。

BASE fixture SHA256 为 `7f55ba6ebe86f1b32553360628e6ec35a90b282fea5ca1cda7799ffcb9234a0d`，BASE Linux artifact SHA256 为 `3faf125ba6b3bbe5a8f5232ae9aadd1f0c9f0fbf681314e217dbad6aeaf349d0`。真实 ELF 检查确认 `SYMBOLIC STATIC_TLS`、`NODELETE`、8-byte `PT_TLS` 和 `R_X86_64_TPOFF64`。CPA 正常使用 `RTLD_NOW | RTLD_LOCAL` 和 `dlclose`。

| 相同 artifact 的对照 | 实际结果 |
| --- | --- |
| same object / same process，312 次 | 312 次 load/init/register/shutdown/dlclose 成功，1 个 handle、1 个 DSO，最终 5 段 library 映射、12 个线程 |
| different inode copies / same process，312 次 | 208 次成功，209..312 共 104 次 static TLS 失败；最终保留 208 个 DSO、1040 段 library 映射、1673 个线程；成功加载对象的 dlclose 均返回 0 |
| same object / fresh exec，312 个进程 | 312 次成功，每个进程最终保留 1 个 DSO，线程数 8..11 |
| different copies / fresh exec，312 个进程 | 312 次成功，每个进程最终保留 1 个 DSO，线程数 8..11 |

实际固定 CPA 的原 `NativeRequestDuplicates -count=320` 退出 1，208 次成功加载、112 次 static TLS 失败。原 `OrdinaryMetadata -count=2` 退出 1，208 次成功加载、176 次失败。日志中的逻辑 unload 不代表 native mappings、runtime threads 或 static TLS 已释放。

仅把原 helper 的每次新副本改为 parent 提供的同一文件，其余源码逐字保留，使用同一 BASE artifact：

- `NativeRequestDuplicates -count=320` 退出 0，320 次加载和卸载。
- `OrdinaryMetadata -count=2` 退出 0，384 次加载和卸载。
- `NativeSSEFields|NativeUnload|ReconfigureReload -count=20` 退出 0，120 次加载和卸载，原当前 Host callback、真实 producer、backpressure、terminal error、zero bridge state、reconfigure 和显式 reload 断言通过。

这些对照支持 parent-lifetime canonical library 的最小修复。C loader 诊断只验证 loader/ABI，实际 CPA 测试负责 callback、请求和流验证。

本地未捕获 signal13/SA_ONSTACK fatal，已安装的 `strace`、`gdb` 不可用，不安装新工具。固定 runtime 的 fatal 分支检查实际 SP/stack 范围，不能根据文案断言实际 SA_ONSTACK bit 缺失。本任务不宣称两次 Ubuntu CI 的不同失败具有唯一相同机制。

## 接口和生命周期

保持 `gCPALoadNative(t *testing.T, p *gCPALocalProducer, enabled bool) *Host` 和全部 callers。`CPA_SMOKE_PLUGIN` 指向 parent 提供的 `plugins/<GOOS>/<GOARCH>/model-mapper.<ext>`：

1. 验证环境变量非空，把路径转成绝对路径，读取真实文件并计算 SHA256。
2. 从该文件推导 plugins directory，验证文件路径符合当前平台和 `model-mapper` 文件名。只读取 canonical library，不新建或覆盖任何 library。
3. 每次仍创建新的 `Host`，设置当前 `p.base`，解析原配置并调用真实 `ApplyConfig`。
4. enabled 时仍要求恰好一个 active `model-mapper` executor，并登记原 `UnloadPlugin` cleanup；disabled 时仍要求没有 active plugin。
5. 日志记录 enabled、canonical path 和实际 SHA256，不再记录不存在的副本。

parent 的 `t.TempDir()` 存活到 overlay child process 结束。该 parent 已对源文件和副本 SHA256 作比较，Windows CPA 进程的真实 shadow DLL 也已作 SHA256 核验。子测试不缓存第一个子测试的 `t.TempDir()`，不改写已加载文件，不缓存 Host，不跳过真实加载或显式 reload。

同一 DSO 共享 `hostCallbackFn`、config 和 stream globals。本 fixture 的全部 callers 没有 `t.Parallel`，每个 enabled owner 在下一个 enabled owner 初始化前完成 cleanup；同时存在的 enabled/disabled Hosts 保持，disabled 不执行 native Open，不替换 callback。该约束只覆盖当前 sequential fixture，不承诺多个同时 enabled Hosts 可共享一个 DSO。

native shutdown 等待本次 streams/workers/preparing 完成，清空 callback；下次 ABI init 恢复 lifecycle 并绑定新的 Host context。当前调用必须到达当前 Host 的 executor。不同 owner、reconfigure、Unload/Reload 均保持原真实调用，不能根据相同路径直接返回缓存结果。

## 永久回归测试及 TDD

在已有 `TestModelMapperFunctionalReconfigureReload` 内添加 canonical lifetime 子测试，保持顶层集合不变。

- parent 文件的 FileInfo 和 SHA256 在两个 owner 开始前记录。
- 两个顺序子测试分别创建真实 native Host，并调用真正的 native `Executor.Execute`。使用不同 owner 的响应内容和 callback 计数确认当前 Host context 被重新绑定；核查 upstream model/request body 和 client response model/opaque text。
- active record path 对应文件必须与 parent 文件 `os.SameFile`。不同 inode 的同字节副本必须失败，不能只比较路径字符串或日志数量。
- 每个子测试结束执行原 cleanup。下一 owner 开始时 parent 文件仍存在且 FileInfo/SHA256 保持不变。
- 先把该测试绑定到实际未提交源码和同一 BASE artifact 运行，保存真实 RED 和失败原因，再修改 helper，运行同一命令获得 GREEN。
- 原 ReconfigureReload 的 schema、metadata、配置字段、五种 formats、count_tokens、Interactions agent、identity route、reconfigure 和显式 Unload/Reload 内容断言全部保留。

## 完整验收

### 原顶层集合

严格核查下面 24 个 functional tests 各执行一次并通过，另两个 bridge tests 各执行一次并通过，不允许新增替代顶层测试或漏跑。

1. `TestModelMapperFunctionalNativeProducerFields`
2. `TestModelMapperFunctionalNativeSSEFields`
3. `TestModelMapperFunctionalNativeFieldsHTTP`
4. `TestModelMapperFunctionalNativeFieldsHTTPUnmappedControl`
5. `TestModelMapperFunctionalNativeUnload`
6. `TestModelMapperFunctionalNativeRequestDuplicates`
7. `TestModelMapperFunctionalNativeFormatsAndHeaders`
8. `TestModelMapperFunctionalNativeProtocolAndErrors`
9. `TestModelMapperFunctionalResponsesWS`
10. `TestModelMapperFunctionalMetadataBuiltin`
11. `TestModelMapperFunctionalValidatorAndConsumers`
12. `TestModelMapperFunctionalProtocolHTTP`
13. `TestModelMapperFunctionalReconfigureReload`
14. `TestModelMapperFunctionalCompatLifecycle`
15. `TestModelMapperFunctionalCodexTruncatedPrefix`
16. `TestModelMapperFunctionalBinaryCaptures`
17. `TestModelMapperFunctionalInteractionsAgent`
18. `TestModelMapperFunctionalValidatorPrefixLimit`
19. `TestModelMapperFunctionalOrdinaryMetadata`
20. `TestModelMapperFunctionalOrdinaryMetadataDisabledWS`
21. `TestModelMapperFunctionalOrdinaryMetadataConsumerControls`
22. `TestModelMapperFunctionalMixedMetadata`
23. `TestModelMapperFunctionalMixedBinaryCaptures`
24. `TestModelMapperFunctionalMixedShapeControls`

bridge tests 为 `TestStreamBridgeCloseUnblocksPendingEmit` 和 `TestStreamBridgeClosePreservesTerminalErrorWhenBufferIsFull`。

### 精确 capture 集合和内容

Windows 和真实 Linux 均运行原 `make integration`。保留全部四份 capture 原始 JSON、overlay 源、测试输出和 CPA 日志，在完整 expected key 集合上检查 unknown、duplicate、missing，不只核对总数。

| 文件 | 精确数量 |
| --- | ---: |
| `native-binary-captures.json` | 1536 |
| `mixed-native-captures.json` | 1800 |
| `mixed-binary-captures.json` | 1440 |
| `mixed-shape-controls.json` | 1680 |
| 合计 | 6456 |

每条保留原 body、headers、model、upstream 身份断言。ordinary metadata、data-only、field-pair、mixed、reverse、alternating、position、raw core、host callback、真实 native/HTTP/WS、disabled/direct、count_tokens、Interactions agent、ASCII case/stacking、duplicate JSON/opaque bytes、16 MiB incomplete 上限、16,842,937-byte 完整 translator unit、error/EOF、backpressure、terminal error、zero bridge state、metadata、reconfigure、explicit reload 全部执行。

### 其他验证

- Windows 和固定 Go1.26.0 Linux 的完整 root tests、vet、race，以及 packager、compatibility checker、smoke helper 三组逐文件 scripts tests。禁止 `go test ./.github/scripts`。诊断目录中的 fixture 快照属于独立临时 module，不参与插件 `./...`；产品 module 和原包集合保持不变。完整 race suite 使用 30 分钟总时限，原测试内部请求/关闭时限、数据规模和断言保持。
- 当前平台真实 build 和 ABI init/register/schema/版本注入检查。常规 integration 使用原默认 `0.0.0-dev`，单独 build 验证版本注入，不改变 release/tag。
- 产品和 benchmark 源码逐字节核对 BASE；运行原性能/allocations tests 和全部 benchmark preflight。原 2 MiB/200 allocations 门槛保持，不声称 CPU 独占或补做旧版本性能比较。
- 使用实际未提交 RED/GREEN 源码的显式 overlay。Linux clone 只复制 Git objects，不会自动携带未提交修改，完整 integration 前必须显式同步本次 fixture 并核查 hash。
- 保存环境、测试源、artifact、CPA revision/module Sum、实际命令、数字 exit 和 SHA256；未启动或未公开 exit 记为 null。环境失败与产品 RED 区分。
- 所有新 Go 输出使用自有 GOMODCACHE/GOCACHE/GOPATH/temp 和有效的 `TEST_TELEMETRY_DIR`。早期 `GOTELEMETRYDIR` 环境变量未能修改目录，原始输出保留，报告自动 telemetry 输出范围限制，不修改 global Go 状态。

## 完成和交付

动态调查 -> 完整 spec 自审 -> 详细 plan -> 永久 focused RED -> 最小 helper 修复 -> focused GREEN -> 完整终验 -> 规格/质量自审、修正及复审 -> 本任务提交。

提交仅含 fixture 和本 spec/plan；提交前、提交后核查 `git diff --check`、实际变更清单、BASE..HEAD 完整累计 diff、24/2/6456 覆盖、source/evidence manifests 和 SHA256。正式报告为 `dist/task-I-native-lifetime/task-I-owner-report.json`，累计 package 为同目录 `task-I-review-package.txt`，包含实际 BASE..最终 HEAD 的完整 log、stat、diff -U10。

owner 的 `canReview=true` 仅表示上述 owner 条件已完成且可进入 controller 独立审查。Debian 本地成功不能替代修复 commit 的 Ubuntu24.04 CI，Windows race/root tests 不代替真实普通 native 加载。七平台 CI/runtime、Release、最终分支复审、main 整合和 issue8/PR7 评论属于后续外部步骤，报告必须列明尚未执行。
