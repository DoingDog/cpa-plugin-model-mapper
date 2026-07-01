# CPA Model Mapper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建一个可发布的 CPA Go 插件：在文本生成请求进入上游前按规则改写模型名，并只在成功改写的请求响应中把顶层 `model` 恢复成客户端原始模型名。

**Architecture:** 插件使用 CPA `ModelRouter + Executor` 路径。`model.route` 只在规则成功匹配且最终模型变化时返回 `Handled:true` 和 `TargetKind:self`；`executor.execute` / `executor.execute_stream` 改写请求体顶层 `model`，通过 `host.model.execute(_stream)` 交回 CPA 原调度链，并传回原 `host_callback_id` 避免递归；响应恢复只改写顶层 `model`，流式只缓存当前未完成 SSE 事件/行。

**Tech Stack:** Go 1.26+，CGO `-buildmode=c-shared`，CPA SDK `github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi` 与 `github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi`，Go 标准库，GNU Make 或兼容 `make`。

## Global Constraints

- 只实现“模型请求映射器”；“模型列表修改”暂不实现，只保留 `docs/model-list-modification-plan.md`。
- 独立 CPA 插件仓库；不修改 CPA 主仓库；`upstream/CLIProxyAPI` 仅用于只读调查。
- 插件配置字段固定为 `enabled`、`global_rules`、`claude_messages_rules`、`codex_responses_rules`、`openai_completions_rules`。
- `enabled` 默认值必须是 `true`。
- 不处理图像生成端点；只面向 CPA 文本生成执行链路。
- 不引入除 CPA SDK 与 YAML 解码外的新依赖；如果实现不需要 YAML 解码，就不添加 YAML 依赖；测试不引入 assertion framework。
- 规则字符串不能包含空格或引号；不能为空；必须包含 `=>`；不能有空规则、空查找式、空替换式、悬空 `\`、`$0`、孤立 `$`、`$x`、或超过 `*` 捕获数量的 `$N`。
- 每个请求最多应用一套规则：端点专用规则非空时只使用专用规则，不叠加全局规则；专用规则为空时才回落全局规则；未知格式只使用全局规则。
- 所选规则集从前到后完整单次遍历：不是命中一条就退出，也不是 repeat-until-stable。
- 只有至少一条规则匹配且最终模型名变化时，才返回 `Handled:true`、记录原始模型、进入 Executor、恢复响应模型。
- 未匹配、匹配后未变化、`enabled=false`、或无可用规则时，必须返回 `Handled:false`，不得记录原始模型，不得恢复响应。
- 请求和响应 JSON 只改写顶层字符串字段 `model`；不递归改写嵌套字段。
- 流式响应不能缓存完整响应；只能缓存当前未完成 SSE 事件/行，完整后再改写 JSON 顶层 `model`。
- `host_callback_id` 必须原样传给 `host.model.execute` 或 `host.model.execute_stream`，避免递归调用本插件。
- 非流式转发必须设置：`EntryProtocol=ExecutorRequest.SourceFormat`、`ExitProtocol=ExecutorRequest.Format`、`Model=上游模型`、`Body=已改写请求体`，并透传 `Headers`、`Query`、`Alt`、`Stream`。
- 流式转发必须使用 CPA c-shared stream bridge：`host.model.execute_stream`、`host.model.stream_read`、`host.stream.emit`、`host.stream.close`、`host.model.stream_close`。
- 最终产物路径固定为 `dist/windows_amd64/model-mapper.dll` 与 `dist/linux_amd64/model-mapper.so`。
- 如果当前 Windows 环境缺少 CGO、Windows 编译器、GNU Make、或 Linux `amd64` cgo 交叉编译工具链，停止并通知用户；不得全局安装编译器或污染本机环境。
- 真实 smoke 测试只使用当前仓库下 `.test-cpa/`；真实 key 只能来自进程环境变量 `CPA_SMOKE_API_KEY`，不能写入 tracked 文件；CPA 启动命令来自 `CPA_SMOKE_CPA_BIN`。
- 实现阶段必须使用 `superpowers:test-driven-development`：每个非平凡生产行为都先写失败测试、运行确认 RED、再写最小实现、运行确认 GREEN。
- 实现和验证的每一个任务都必须由 workflow 编排推进，且每个任务至少经过一个子代理执行或审查检查点；主会话负责审查后继续。

---

## Source File Map

- Create: `go.mod` — Go module；只声明 CPA SDK 直接依赖，必要时才声明 YAML 解码依赖。
- Create: `go.sum` — `go mod tidy` 生成。
- Create: `main.go` — 插件生产代码：C ABI、RPC dispatch、配置、规则解析/应用、路由决策、JSON/SSE 改写、host callbacks。保持单文件；超过约 800 行或明显不可读时才最小拆分为 `rules.go`、`stream.go`、`handlers.go`。
- Create: `main_test.go` — 所有单元测试；生产行为都先在这里 RED。
- Create: `Makefile` — `test`、`build-windows-amd64`、`build-linux-amd64`、`build`、`package`、`install-local`、`install-linux-amd64`、`smoke-local`、`clean`。
- Create: `.github/scripts/package-release.go` — 标准库 release 打包器；只白名单打包平台动态库和 README/LICENSE（如果存在）。
- Create: `.github/scripts/smoke-local.go` — 标准库 smoke runner；只在 `CPA_SMOKE_API_KEY` 和 `CPA_SMOKE_CPA_BIN` 存在时运行真实 CPA。
- Create: `.github/workflows/build.yml` — push/PR 测试和构建；tag `v*` 生成 release 资产；live smoke 只允许手动/secret-gated。
- Create: `README.md` — 插件用途、配置、规则语法、构建、部署、smoke、secret policy、模型列表模块未实现说明。
- Modify: `.gitignore` — 确认忽略 `dist/`、`.test-cpa/`、`.env`、`*.dll`、`*.so`、`*.dylib`、`*.h`、`*.zip`、`*.sha256`、本地 smoke config/log。
- Keep: `docs/model-list-modification-plan.md` — 只作为后续计划，不产生生产代码。

## Generated and Ignored Artifacts

- `dist/windows_amd64/model-mapper.dll`
- `dist/linux_amd64/model-mapper.so`
- `dist/release/model-mapper_<version>_windows_amd64.zip`
- `dist/release/model-mapper_<version>_linux_amd64.zip`
- `dist/release/*.sha256`
- `.test-cpa/**`
- generated CGO headers such as `model-mapper.h`

## Shared Internal Interfaces

These names are fixed for tasks below:

```go
type Config struct {
	Enabled                 bool   `json:"enabled"`
	GlobalRules             string `json:"global_rules"`
	ClaudeMessagesRules     string `json:"claude_messages_rules"`
	CodexResponsesRules     string `json:"codex_responses_rules"`
	OpenAICompletionsRules  string `json:"openai_completions_rules"`
}

func defaultConfig() Config
func parseRules(raw string) ([]rule, error)
func applyRules(model string, rules []rule) (mapped string, matched bool, err error)
func selectRules(cfg Config, format string) (raw string, ok bool)

type routeDecision struct {
	Handled       bool
	OriginalModel string
	UpstreamModel string
}

func routeModel(cfg Config, format string, model string) (routeDecision, error)
func rewriteRequestModel(body []byte, upstreamModel string) ([]byte, bool, error)
func restoreResponseModel(body []byte, originalModel string) ([]byte, bool, error)

type sseRewriter struct { /* private fields */ }
func newSSERewriter(originalModel string) *sseRewriter
func (r *sseRewriter) Write(chunk []byte) (out [][]byte, err error)
func (r *sseRewriter) Flush() (out [][]byte, err error)
```

Executor tests may use this seam so host callbacks are fakeable without invoking C:

```go
type hostCaller func(method string, payload any) (json.RawMessage, error)
```

---

### Task 0: Git hygiene and ignored local state

**Files:**
- Modify: `.gitignore`

**Interfaces:**
- Consumes: existing `.gitignore`.
- Produces: ignored local state so later build/smoke tasks cannot commit secrets or generated artifacts.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the reviewer to inspect only `.gitignore` requirements from this plan and confirm generated/secret paths before any artifact or smoke config is created.

- [ ] **Step 2: Extend `.gitignore` if needed**

Ensure the file contains these exact patterns. Keep existing lines if already present; add only missing lines:

```gitignore
# Investigation clones and temporary references
upstream/
.cpa-reference/

# Build outputs
dist/
*.dll
*.so
*.dylib
*.h

# Release archives and checksums
*.zip
*.sha256

# Local test CPA, smoke runtime, and secrets
.test-cpa/
.env
config.yaml
*.log

# Editor and OS noise
.DS_Store
Thumbs.db
```

- [ ] **Step 3: Verify ignore behavior before secrets/artifacts exist**

Run:

```powershell
git check-ignore dist/windows_amd64/model-mapper.dll .test-cpa/config.yaml .env model-mapper.h dist/release/model-mapper_0.1.0_windows_amd64.zip dist/release/model-mapper_0.1.0_windows_amd64.zip.sha256
```

Expected: every input path is echoed by `git check-ignore`.

- [ ] **Step 4: Commit**

```powershell
git add .gitignore
git commit -m "chore: ignore generated plugin artifacts"
```

---

### Task 1: Go module skeleton and default config

**Files:**
- Create: `go.mod`
- Create: `main.go`
- Create: `main_test.go`
- Create: `go.sum`

**Interfaces:**
- Produces: `Config`, `defaultConfig() Config`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to verify the module skeleton only: Go version, allowed dependencies, and default config field names.

- [ ] **Step 2: Create `go.mod`**

Use a concrete module path that can be renamed before publishing if the remote repo name changes:

```go
module github.com/router-for-me/cpa-plugin-model-mapper

go 1.26.0

require github.com/router-for-me/CLIProxyAPI/v7 v7.2.48
```

`v7.0.0` does not include `sdk/pluginapi` or `sdk/pluginabi`; use `v7.2.48` instead. If `upstream/CLIProxyAPI` is absent, use the local GOMODCACHE copy at `github.com/router-for-me/CLIProxyAPI/v7@v7.2.48` to reconcile seams. Do not add a `replace` pointing to `upstream/CLIProxyAPI` in the committed `go.mod`.

- [ ] **Step 3: Write the failing test**

Create `main_test.go` with:

```go
package main

import "testing"

func TestDefaultConfigEnabledTrue(t *testing.T) {
	cfg := defaultConfig()
	if !cfg.Enabled {
		t.Fatalf("default enabled = false, want true")
	}
	if cfg.GlobalRules != "" || cfg.ClaudeMessagesRules != "" || cfg.CodexResponsesRules != "" || cfg.OpenAICompletionsRules != "" {
		t.Fatalf("default rule fields must be empty: %#v", cfg)
	}
}
```

- [ ] **Step 4: Run test to verify RED**

Run:

```powershell
go test ./... -run TestDefaultConfigEnabledTrue
```

Expected: FAIL because `defaultConfig` or `Config` is undefined.

- [ ] **Step 5: Write minimal implementation**

Create `main.go` with:

```go
package main

func main() {}

type Config struct {
	Enabled                bool   `json:"enabled"`
	GlobalRules            string `json:"global_rules"`
	ClaudeMessagesRules    string `json:"claude_messages_rules"`
	CodexResponsesRules    string `json:"codex_responses_rules"`
	OpenAICompletionsRules string `json:"openai_completions_rules"`
}

func defaultConfig() Config {
	return Config{Enabled: true}
}
```

- [ ] **Step 6: Run test to verify GREEN**

Run:

```powershell
go test ./... -run TestDefaultConfigEnabledTrue
```

Expected: PASS.

- [ ] **Step 7: Tidy and commit**

Run:

```powershell
go mod tidy
go test ./...
git add go.mod go.sum main.go main_test.go
git commit -m "feat: initialize model mapper plugin module"
```

Expected: `go test ./...` PASS.

---

### Task 2: Strict rule parser

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Produces: private `rule` type and `parseRules(raw string) ([]rule, error)`.
- The `rule` type must carry enough data for `applyRules`: parsed pattern tokens, replacement tokens, and capture count.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review only the rule grammar from the spec and this task. It should reject adding regex-only shortcuts if they make escaping or `$N` validation unclear.

- [ ] **Step 2: Write failing parser tests**

Append to `main_test.go`:

```go
func TestParseRulesAcceptsValidRules(t *testing.T) {
	tests := []string{
		"a=>b",
		`deepseek-*=>claude-$1`,
		`a*bc*=>x$2y$1`,
		`literal\*=>star`,
		`a\;b=>c\=>d`,
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			rules, err := parseRules(raw)
			if err != nil {
				t.Fatalf("parseRules(%q) error = %v", raw, err)
			}
			if len(rules) == 0 {
				t.Fatalf("parseRules(%q) returned no rules", raw)
			}
		})
	}
}

func TestParseRulesRejectsInvalidRules(t *testing.T) {
	tests := []string{
		"",
		"a =>b",
		`"a"=>b`,
		"a=>",
		"=>b",
		"a-b",
		"a=>b;",
		";a=>b",
		"a=>b;;c=>d",
		`a\=>b`,
		`a=>b\`,
		`a=>x$`,
		`a=>x$0`,
		`a=>x$x`,
		`a=>x$1`,
		`a*=>x$2`,
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseRules(raw); err == nil {
				t.Fatalf("parseRules(%q) error = nil, want error", raw)
			}
		})
	}
}
```

- [ ] **Step 3: Run parser tests to verify RED**

Run:

```powershell
go test ./... -run TestParseRules
```

Expected: FAIL because `parseRules` is undefined or incomplete.

- [ ] **Step 4: Implement the minimum parser**

Implementation requirements:

```go
// parseRules rejects spaces, quotes, empty input, empty rule segments, missing =>,
// empty find/replace, dangling backslash, and invalid replacement references.
func parseRules(raw string) ([]rule, error)
```

Use a small scanner with these behaviors:

- Reject any Unicode whitespace with `unicode.IsSpace`.
- Reject `'` and `"`.
- Split on unescaped `;`.
- Split each rule on one unescaped `=>`; reject zero or more than one separator.
- In the find side, unescaped `*` increments capture count; escaped `\*`, `\;`, `\$`, `\\`, and `\=>` are literals.
- In the replace side, `$1` through `$N` are references; `$0`, lone `$`, non-numeric `$x`, and references greater than capture count are errors.
- Reject a dangling `\` on either side.

- [ ] **Step 5: Run parser tests to verify GREEN**

Run:

```powershell
go test ./... -run TestParseRules
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
go test ./...
git add main.go main_test.go
git commit -m "feat: parse model mapping rules"
```

Expected: full test suite PASS.

---

### Task 3: Rule application and full-chain execution

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `parseRules(raw string) ([]rule, error)`.
- Produces: `applyRules(model string, rules []rule) (mapped string, matched bool, err error)`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review only chain semantics: left-to-right exactly once, no first-match exit, no repeat-until-stable loop.

- [ ] **Step 2: Write failing application tests**

Append to `main_test.go`:

```go
func mustParseRules(t *testing.T, raw string) []rule {
	t.Helper()
	rules, err := parseRules(raw)
	if err != nil {
		t.Fatalf("parseRules(%q) error = %v", raw, err)
	}
	return rules
}

func TestApplyRulesFullChain(t *testing.T) {
	rules := mustParseRules(t, "deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>claude-v4-flash")
	mapped, matched, err := applyRules("deepseek-v4-pro", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "claude-v4-flash" {
		t.Fatalf("mapped=%q matched=%v, want claude-v4-flash true", mapped, matched)
	}
}

func TestApplyRulesWildcardCapture(t *testing.T) {
	rules := mustParseRules(t, "claude-*=>upstream-$1")
	mapped, matched, err := applyRules("claude-sonnet", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "upstream-sonnet" {
		t.Fatalf("mapped=%q matched=%v", mapped, matched)
	}
}

func TestApplyRulesUnmatched(t *testing.T) {
	rules := mustParseRules(t, "a=>b")
	mapped, matched, err := applyRules("z", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if matched || mapped != "z" {
		t.Fatalf("mapped=%q matched=%v, want z false", mapped, matched)
	}
}

func TestApplyRulesUnchangedStillMatched(t *testing.T) {
	rules := mustParseRules(t, "a=>a")
	mapped, matched, err := applyRules("a", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "a" {
		t.Fatalf("mapped=%q matched=%v, want a true", mapped, matched)
	}
}

func TestApplyRulesSinglePassNoLoop(t *testing.T) {
	rules := mustParseRules(t, "a=>b;b=>a")
	mapped, matched, err := applyRules("a", rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if !matched || mapped != "a" {
		t.Fatalf("mapped=%q matched=%v, want a true after one finite pass", mapped, matched)
	}
}
```

- [ ] **Step 3: Run tests to verify RED**

Run:

```powershell
go test ./... -run TestApplyRules
```

Expected: FAIL because `applyRules` is undefined or not implementing chain behavior.

- [ ] **Step 4: Implement minimal application**

Implementation requirements:

```go
func applyRules(model string, rules []rule) (string, bool, error)
```

- Start with `current := model` and `matchedAny := false`.
- Iterate rules once in slice order.
- For each rule, match against `current`; if no match, continue.
- On match, build replacement using captured values, set `current`, set `matchedAny=true`.
- If `current == ""` after a match, return error.
- After the loop, return `current`, `matchedAny`, `nil`.

- [ ] **Step 5: Run tests to verify GREEN**

Run:

```powershell
go test ./... -run TestApplyRules
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
go test ./...
git add main.go main_test.go
git commit -m "feat: apply model mapping rules"
```

Expected: full test suite PASS.

---

### Task 4: Endpoint rule selection and route decision gates

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `Config`, `parseRules`, `applyRules`.
- Produces: `selectRules(cfg Config, format string) (raw string, ok bool)` and `routeModel(cfg Config, format string, model string) (routeDecision, error)`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review only endpoint precedence and matched+changed gate. It must flag any global+endpoint stacking.

- [ ] **Step 2: Write failing selection tests**

Append to `main_test.go`:

```go
func TestSelectRulesEndpointSpecificOverridesGlobal(t *testing.T) {
	cfg := Config{Enabled: true, GlobalRules: "global=>x", ClaudeMessagesRules: "claude=>x", CodexResponsesRules: "codex=>x", OpenAICompletionsRules: "openai=>x"}
	tests := map[string]string{
		"claude":          "claude=>x",
		"openai-response": "codex=>x",
		"openai":          "openai=>x",
	}
	for format, want := range tests {
		raw, ok := selectRules(cfg, format)
		if !ok || raw != want {
			t.Fatalf("selectRules(%q)=(%q,%v), want %q true", format, raw, ok, want)
		}
	}
}

func TestSelectRulesFallsBackToGlobal(t *testing.T) {
	cfg := Config{Enabled: true, GlobalRules: "global=>x"}
	for _, format := range []string{"claude", "openai-response", "openai", "gemini"} {
		raw, ok := selectRules(cfg, format)
		if !ok || raw != "global=>x" {
			t.Fatalf("selectRules(%q)=(%q,%v), want global=>x true", format, raw, ok)
		}
	}
}

func TestSelectRulesBothEmptySkips(t *testing.T) {
	if raw, ok := selectRules(defaultConfig(), "claude"); ok || raw != "" {
		t.Fatalf("selectRules empty=(%q,%v), want empty false", raw, ok)
	}
}
```

- [ ] **Step 3: Write failing route tests**

Append to `main_test.go`:

```go
func TestRouteModelSkipsDisabledNoRulesUnmatchedAndUnchanged(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		format string
		model  string
	}{
		{name: "disabled", cfg: Config{Enabled: false, GlobalRules: "a=>b"}, format: "openai", model: "a"},
		{name: "no rules", cfg: defaultConfig(), format: "openai", model: "a"},
		{name: "unmatched", cfg: Config{Enabled: true, GlobalRules: "x=>y"}, format: "openai", model: "a"},
		{name: "unchanged", cfg: Config{Enabled: true, GlobalRules: "a=>a"}, format: "openai", model: "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := routeModel(tt.cfg, tt.format, tt.model)
			if err != nil {
				t.Fatalf("routeModel error = %v", err)
			}
			if decision.Handled || decision.OriginalModel != "" || decision.UpstreamModel != "" {
				t.Fatalf("decision=%#v, want unhandled with empty models", decision)
			}
		})
	}
}

func TestRouteModelHandlesOnlyMatchedChanged(t *testing.T) {
	cfg := Config{Enabled: true, OpenAICompletionsRules: "deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>gpt-5.4-mini", GlobalRules: "deepseek-v4-pro=>wrong"}
	decision, err := routeModel(cfg, "openai", "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("routeModel error = %v", err)
	}
	if !decision.Handled || decision.OriginalModel != "deepseek-v4-pro" || decision.UpstreamModel != "gpt-5.4-mini" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestRouteModelBadSelectedRulesErrors(t *testing.T) {
	cfg := Config{Enabled: true, ClaudeMessagesRules: "bad rule"}
	if _, err := routeModel(cfg, "claude", "a"); err == nil {
		t.Fatalf("routeModel bad selected rules error = nil")
	}
}
```

- [ ] **Step 4: Run tests to verify RED**

Run:

```powershell
go test ./... -run "TestSelectRules|TestRouteModel"
```

Expected: FAIL because `selectRules` and `routeModel` are undefined or incomplete.

- [ ] **Step 5: Implement minimal selection and route gates**

Implementation requirements:

```go
func selectRules(cfg Config, format string) (string, bool) {
	var dedicated string
	switch format {
	case "claude":
		dedicated = cfg.ClaudeMessagesRules
	case "openai-response":
		dedicated = cfg.CodexResponsesRules
	case "openai":
		dedicated = cfg.OpenAICompletionsRules
	}
	if dedicated != "" {
		return dedicated, true
	}
	if cfg.GlobalRules != "" {
		return cfg.GlobalRules, true
	}
	return "", false
}
```

`routeModel` composes `selectRules` → `parseRules` → `applyRules`. It returns `Handled:false` with empty model fields for disabled, no rules, unmatched, or unchanged. It returns error for bad selected rules or empty final output.

- [ ] **Step 6: Run tests to verify GREEN**

Run:

```powershell
go test ./... -run "TestSelectRules|TestRouteModel"
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
go test ./...
git add main.go main_test.go
git commit -m "feat: choose mapping rules for model routes"
```

Expected: full test suite PASS.

---

### Task 5: Top-level JSON request rewrite and response restore

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Produces: `rewriteRequestModel(body []byte, upstreamModel string) ([]byte, bool, error)`.
- Produces: `restoreResponseModel(body []byte, originalModel string) ([]byte, bool, error)`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review only JSON rewrite scope. It must flag recursive model rewrites or fatal errors for invalid JSON.

- [ ] **Step 2: Write failing request rewrite tests**

Append to `main_test.go`:

```go
func TestRewriteRequestModelTopLevelOnly(t *testing.T) {
	got, changed, err := rewriteRequestModel([]byte(`{"model":"A","messages":[]}`), "B")
	if err != nil {
		t.Fatalf("rewriteRequestModel error = %v", err)
	}
	if !changed || string(got) == `{"model":"A","messages":[]}` {
		t.Fatalf("changed=%v body=%s", changed, got)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("rewritten JSON invalid: %v", err)
	}
	if decoded["model"] != "B" {
		t.Fatalf("model=%v, want B", decoded["model"])
	}
}

func TestRewriteRequestModelLeavesUnsupportedBodiesUnchanged(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"payload":{"model":"A"}}`),
		[]byte(`{"messages":[]}`),
		[]byte(`{"model":123}`),
		[]byte(`not-json`),
	}
	for _, body := range tests {
		got, changed, err := rewriteRequestModel(body, "B")
		if err != nil {
			t.Fatalf("rewriteRequestModel(%s) error = %v", body, err)
		}
		if changed || string(got) != string(body) {
			t.Fatalf("rewriteRequestModel(%s)=(%s,%v), want unchanged false", body, got, changed)
		}
	}
}
```

- [ ] **Step 3: Write failing response restore tests**

Append to `main_test.go`:

```go
func TestRestoreResponseModelTopLevelOnly(t *testing.T) {
	got, changed, err := restoreResponseModel([]byte(`{"model":"B","id":"r1"}`), "A")
	if err != nil {
		t.Fatalf("restoreResponseModel error = %v", err)
	}
	if !changed {
		t.Fatalf("changed=false, want true")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("restored JSON invalid: %v", err)
	}
	if decoded["model"] != "A" {
		t.Fatalf("model=%v, want A", decoded["model"])
	}
}

func TestRestoreResponseModelLeavesUnsupportedBodiesUnchanged(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"payload":{"model":"B"}}`),
		[]byte(`{"id":"r1"}`),
		[]byte(`{"model":123}`),
		[]byte(`not-json`),
	}
	for _, body := range tests {
		got, changed, err := restoreResponseModel(body, "A")
		if err != nil {
			t.Fatalf("restoreResponseModel(%s) error = %v", body, err)
		}
		if changed || string(got) != string(body) {
			t.Fatalf("restoreResponseModel(%s)=(%s,%v), want unchanged false", body, got, changed)
		}
	}
}
```

- [ ] **Step 4: Run tests to verify RED**

Run:

```powershell
go test ./... -run "TestRewriteRequestModel|TestRestoreResponseModel"
```

Expected: FAIL because functions are undefined or incomplete.

- [ ] **Step 5: Implement minimal JSON helpers**

Implementation requirements:

- Use `encoding/json` and `map[string]any`.
- If JSON parsing fails, return a copied original body, `changed=false`, `err=nil`.
- If top-level `model` is absent or not a string, return unchanged.
- If the field exists and is a string, replace it and marshal the map.
- Do not inspect or rewrite nested fields.

- [ ] **Step 6: Run tests to verify GREEN**

Run:

```powershell
go test ./... -run "TestRewriteRequestModel|TestRestoreResponseModel"
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
go test ./...
git add main.go main_test.go
git commit -m "feat: rewrite top-level model fields"
```

Expected: full test suite PASS.

---

### Task 6: SSE rewriter for streaming model restoration

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `restoreResponseModel`.
- Produces: `sseRewriter`, `newSSERewriter`, `(*sseRewriter).Write`, `(*sseRewriter).Flush`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review only stream safety. It must flag any full-stream buffer or split JSON emission before a complete event/line.

- [ ] **Step 2: Write failing SSE tests**

Append to `main_test.go`:

```go
func flattenChunks(chunks [][]byte) string {
	var b strings.Builder
	for _, chunk := range chunks {
		b.Write(chunk)
	}
	return b.String()
}

func TestSSERewriterRestoresCompleteJSONEvent(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("data: {\"model\":\"B\",\"id\":\"1\"}\n\n"))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	got := flattenChunks(out)
	if !strings.Contains(got, `"model":"A"`) || strings.Contains(got, `"model":"B"`) {
		t.Fatalf("rewritten event = %q", got)
	}
}

func TestSSERewriterBuffersSplitJSONUntilComplete(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("data: {\"model\":\"B"))
	if err != nil {
		t.Fatalf("first Write error = %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("first Write emitted %q, want no partial output", flattenChunks(out))
	}
	out, err = r.Write([]byte("\"}\n\n"))
	if err != nil {
		t.Fatalf("second Write error = %v", err)
	}
	got := flattenChunks(out)
	if !strings.Contains(got, `"model":"A"`) || strings.Contains(got, `"model":"B"`) {
		t.Fatalf("rewritten split event = %q", got)
	}
}

func TestSSERewriterPassesThroughDoneCommentsAndNonJSON(t *testing.T) {
	r := newSSERewriter("A")
	input := ": keepalive\n\ndata: [DONE]\n\ndata: hello\n\n"
	out, err := r.Write([]byte(input))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if got := flattenChunks(out); got != input {
		t.Fatalf("got %q, want %q", got, input)
	}
}

func TestSSERewriterHandlesMultipleEventsCRLFAndFlush(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("data: {\"model\":\"B\"}\r\n\r\ndata: [DONE]\r\n\r\nleftover"))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	got := flattenChunks(out)
	if !strings.Contains(got, `"model":"A"`) || !strings.Contains(got, "data: [DONE]") || strings.Contains(got, "leftover") {
		t.Fatalf("Write output = %q", got)
	}
	flushed, err := r.Flush()
	if err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if string(bytes.Join(flushed, nil)) != "leftover" {
		t.Fatalf("Flush output = %q", string(bytes.Join(flushed, nil)))
	}
}
```

- [ ] **Step 3: Run tests to verify RED**

Run:

```powershell
go test ./... -run TestSSERewriter
```

Expected: FAIL because SSE rewriter is undefined or incomplete. Add imports `bytes` and `strings` to `main_test.go` only after the compiler asks for them.

- [ ] **Step 4: Implement minimal SSE rewriter**

Implementation requirements:

- Keep `sseRewriter` private.
- Buffer bytes until a complete delimiter `\n\n` or `\r\n\r\n` exists.
- For each complete event, process each line.
- Lines starting with `data:` are trimmed after the prefix for decision only; preserve the `data:` prefix in output.
- `data: [DONE]`, comments starting with `:`, empty keep-alives, and non-JSON data are emitted unchanged.
- JSON `data:` content is passed to `restoreResponseModel` with the original model and emitted only after a complete event is available.
- `Flush` emits remaining buffered bytes unchanged.

- [ ] **Step 5: Run tests to verify GREEN**

Run:

```powershell
go test ./... -run TestSSERewriter
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
go test ./...
git add main.go main_test.go
git commit -m "feat: restore model in SSE events"
```

Expected: full test suite PASS.

---

### Task 7: CPA SDK discovery and RPC seam reconciliation

**Files:**
- Read-only: `upstream/CLIProxyAPI/sdk/pluginapi/types.go`
- Read-only: `upstream/CLIProxyAPI/sdk/pluginabi/types.go`
- Read-only: `upstream/CLIProxyAPI/examples/plugin/host-model-callback/go/main.go`
- Read-only: `upstream/CLIProxyAPI/examples/plugin/claude-web-search-router/go/execute_stream.go`
- Read-only: `upstream/CLIProxyAPI/examples/plugin/claude-web-search-router/go/stream_forward.go`
- Modify after discovery: `main.go`
- Modify after discovery: `main_test.go`

**Interfaces:**
- Confirms method constants: `plugin.register`, `plugin.reconfigure`, `model.route`, `executor.execute`, `executor.execute_stream`, `executor.count_tokens`, `host.model.execute`, `host.model.execute_stream`, `host.model.stream_read`, `host.model.stream_close`, `host.stream.emit`, `host.stream.close`.
- Confirms SDK structs: `pluginapi.ModelRouteRequest`, `pluginapi.ModelRouteResponse`, `pluginapi.HostModelExecutionRequest`, `pluginapi.HostModelExecutionResponse`, `pluginapi.HostModelStreamResponse`, `pluginapi.HostModelStreamReadRequest`, `pluginapi.HostModelStreamReadResponse`, `pluginapi.HostModelStreamCloseRequest`, `pluginapi.ExecutorRequest`, `pluginapi.ExecutorResponse`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to read the listed SDK/example files and return only mismatches between this plan and actual CPA SDK names. If there are mismatches, update this plan before Task 8.

- [ ] **Step 2: Verify method names and stream bridge locally**

Run these read-only checks:

```powershell
git grep -n "MethodModelRoute\|MethodExecutorExecuteStream\|MethodHostModelExecuteStream\|MethodHostStreamEmit\|MethodHostStreamClose" -- upstream/CLIProxyAPI/sdk/pluginabi/types.go
git grep -n "type ModelRouteResponse\|TargetModel.*Only meaningful\|type HostModelExecutionRequest\|type HostModelStreamReadResponse\|type ExecutorRequest" -- upstream/CLIProxyAPI/sdk/pluginapi/types.go
git grep -n "go func()\|MethodHostModelExecuteStream\|MethodHostStreamEmit\|MethodHostStreamClose" -- upstream/CLIProxyAPI/examples/plugin/claude-web-search-router/go/execute_stream.go upstream/CLIProxyAPI/examples/plugin/claude-web-search-router/go/stream_forward.go
go test ./... -run TestDefaultConfigEnabledTrue
```

Expected: grep output shows the method/type names and async stream goroutine pattern; `go test` PASS. If the SDK output contradicts this plan, stop and update the plan before Task 8.

- [ ] **Step 3: Add minimal SDK imports only when handler tests need them**

In later tasks, import exactly:

```go
import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)
```

Remove unused imports immediately after each GREEN step.

- [ ] **Step 4: Commit only if discovery changed files**

If no files changed, do not create an empty commit. If this task updated the plan or test seams, run:

```powershell
go test ./...
git add main.go main_test.go docs/superpowers/plans/2026-07-01-cpa-model-mapper-implementation.md
git commit -m "chore: reconcile CPA SDK handler seams"
```

Expected: full test suite PASS.

---

### Task 8: Registration and reconfiguration handlers

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `Config`, `parseRules`.
- Produces: `pluginRegistration() registration`, `decodeConfig(raw json.RawMessage) (Config, error)`, `handlePluginRegister(raw []byte) ([]byte, error)`, `handlePluginReconfigure(raw []byte) ([]byte, error)`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review capability and config metadata only. It must flag any model-list capability or default `enabled:false`.

- [ ] **Step 2: Write failing registration tests**

Append to `main_test.go`:

```go
func TestPluginRegistrationMetadataAndConfigFields(t *testing.T) {
	reg := pluginRegistration()
	if reg.SchemaVersion != pluginabi.SchemaVersion {
		t.Fatalf("schema version=%d, want %d", reg.SchemaVersion, pluginabi.SchemaVersion)
	}
	if reg.Metadata.Name != "model-mapper" {
		t.Fatalf("plugin name=%q", reg.Metadata.Name)
	}
	if !reg.Capabilities.ModelRouter || !reg.Capabilities.Executor {
		t.Fatalf("capabilities=%#v, want model router and executor", reg.Capabilities)
	}
	if reg.Capabilities.ExecutorModelScope != string(pluginapi.ExecutorModelScopeStatic) {
		t.Fatalf("executor scope=%q", reg.Capabilities.ExecutorModelScope)
	}
	wantFields := []string{"enabled", "global_rules", "claude_messages_rules", "codex_responses_rules", "openai_completions_rules"}
	got := make([]string, 0, len(reg.Metadata.ConfigFields))
	for _, field := range reg.Metadata.ConfigFields {
		got = append(got, field.Name)
	}
	if !reflect.DeepEqual(got, wantFields) {
		t.Fatalf("config fields=%v, want %v", got, wantFields)
	}
}

func TestDecodeConfigDefaultAndBadRules(t *testing.T) {
	cfg, err := decodeConfig(nil)
	if err != nil {
		t.Fatalf("decodeConfig nil error = %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("decoded default enabled=false")
	}
	if _, err := decodeConfig(json.RawMessage(`{"enabled":true,"global_rules":"bad rule"}`)); err == nil {
		t.Fatalf("decodeConfig bad rules error = nil")
	}
	cfg, err = decodeConfig(json.RawMessage(`{"enabled":false,"global_rules":"a=>b"}`))
	if err != nil {
		t.Fatalf("decodeConfig valid error = %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("enabled=true, want false from config")
	}
}
```

- [ ] **Step 3: Run tests to verify RED**

Run:

```powershell
go test ./... -run "TestPluginRegistration|TestDecodeConfig"
```

Expected: FAIL because registration/config handlers are undefined or incomplete. Add imports `encoding/json` and `reflect` to `main_test.go` when needed.

- [ ] **Step 4: Implement registration structs and config decoder**

Use JSON tags matching CPA examples:

```go
type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	ModelRouter           bool     `json:"model_router"`
	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope"`
	ExecutorInputFormats  []string `json:"executor_input_formats"`
	ExecutorOutputFormats []string `json:"executor_output_formats"`
}
```

`decodeConfig` behavior:

- Start from `defaultConfig()`.
- If raw is empty or `{}`, return default.
- Decode known fields from JSON.
- Validate every non-empty rule field by calling `parseRules`.
- Return error for bad configured rules; bad rules cannot silently take effect.

- [ ] **Step 5: Run tests to verify GREEN**

Run:

```powershell
go test ./... -run "TestPluginRegistration|TestDecodeConfig"
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
go test ./...
git add main.go main_test.go
git commit -m "feat: register model mapper plugin"
```

Expected: full test suite PASS.

---

### Task 9: `model.route` RPC handler

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `routeModel`, `pluginapi.ModelRouteRequest`, `pluginapi.ModelRouteResponse`.
- Produces: `handleModelRoute(raw []byte) ([]byte, error)` and package-level loaded config accessors `loadedConfig() Config`, `setLoadedConfigForTest(cfg Config)`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to verify that `model.route` enters plugin Executor only for matched+changed decisions.

- [ ] **Step 2: Write failing route handler tests**

Append to `main_test.go`:

```go
func TestHandleModelRouteUnhandledWhenNoChange(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "a=>a"})
	raw, err := json.Marshal(pluginapi.ModelRouteRequest{SourceFormat: "openai", RequestedModel: "a"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	respRaw, err := handleModelRoute(raw)
	if err != nil {
		t.Fatalf("handleModelRoute error = %v", err)
	}
	var resp pluginapi.ModelRouteResponse
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode route response: %v", err)
	}
	if resp.Handled {
		t.Fatalf("route handled=true, want false")
	}
}

func TestHandleModelRouteHandledSelfForChangedModel(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "deepseek-v4-pro=>gpt-5.4-mini"})
	raw, err := json.Marshal(pluginapi.ModelRouteRequest{SourceFormat: "openai", RequestedModel: "deepseek-v4-pro"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	respRaw, err := handleModelRoute(raw)
	if err != nil {
		t.Fatalf("handleModelRoute error = %v", err)
	}
	var resp pluginapi.ModelRouteResponse
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode route response: %v", err)
	}
	if !resp.Handled || resp.TargetKind != pluginapi.ModelRouteTargetSelf {
		t.Fatalf("route response=%#v", resp)
	}
	if resp.TargetModel != "" {
		t.Fatalf("self route TargetModel=%q, want empty because SDK only defines it for provider routes", resp.TargetModel)
	}
}
```

- [ ] **Step 3: Run tests to verify RED**

Run:

```powershell
go test ./... -run TestHandleModelRoute
```

Expected: FAIL because handler/config accessors are undefined or incomplete.

- [ ] **Step 4: Implement minimal route handler**

Implementation requirements:

- Use a package-level config protected by `sync.RWMutex`; no extra abstraction.
- Decode `pluginapi.ModelRouteRequest` from raw JSON.
- Use `req.SourceFormat` and `req.RequestedModel`.
- For changed decisions, return:

```go
pluginapi.ModelRouteResponse{
	Handled:    true,
	TargetKind: pluginapi.ModelRouteTargetSelf,
	Reason:     "model mapped by model-mapper",
}
```

Do not rely on `TargetModel` for `TargetKind:self`; CPA SDK documents `TargetModel` as meaningful only for provider routes. The Executor must recompute the upstream model from `ExecutorRequest.Model` and the loaded config.

- For unhandled decisions, return `pluginapi.ModelRouteResponse{Handled:false}`.
- Let bad selected rules return an error so CPA fails the current request.

- [ ] **Step 5: Run tests to verify GREEN**

Run:

```powershell
go test ./... -run TestHandleModelRoute
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
go test ./...
git add main.go main_test.go
git commit -m "feat: route mapped models to plugin executor"
```

Expected: full test suite PASS.

---

### Task 10: Non-stream Executor forwarding

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `rewriteRequestModel`, `restoreResponseModel`, `pluginapi.ExecutorRequest`, `pluginapi.HostModelExecutionRequest`, `pluginapi.HostModelExecutionResponse`.
- Produces: `handleExecutorExecute(raw []byte, call hostCaller) ([]byte, error)`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review only host callback forwarding. It must flag missing `host_callback_id`, protocol field loss, or fake success on host errors.

- [ ] **Step 2: Write failing executor test**

Append to `main_test.go`:

```go
type rpcExecutorRequest struct {
	pluginapi.ExecutorRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
	StreamID       string `json:"stream_id,omitempty"`
}

type hostModelExecutionRequest struct {
	pluginapi.HostModelExecutionRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func TestHandleExecutorExecuteForwardsMappedRequestAndRestoresResponse(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "deepseek-v4-pro=>gpt-5.4-mini"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "deepseek-v4-pro",
			Format:          "openai",
			SourceFormat:    "openai",
			Stream:          false,
			Alt:             "alt-mode",
			Headers:         http.Header{"X-Test": []string{"1"}},
			Query:           url.Values{"q": []string{"1"}},
			OriginalRequest: []byte(`{"model":"deepseek-v4-pro","messages":[]}`),
		},
		HostCallbackID: "callback-1",
	}
	var captured hostModelExecutionRequest
	fakeHost := func(method string, payload any) (json.RawMessage, error) {
		if method != pluginabi.MethodHostModelExecute {
			t.Fatalf("method=%q, want %q", method, pluginabi.MethodHostModelExecute)
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		if err := json.Unmarshal(raw, &captured); err != nil {
			t.Fatalf("decode captured payload: %v", err)
		}
		return json.Marshal(pluginapi.HostModelExecutionResponse{
			StatusCode: 200,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       []byte(`{"model":"gpt-5.4-mini","id":"ok"}`),
		})
	}
	rawReq, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	respRaw, err := handleExecutorExecute(rawReq, fakeHost)
	if err != nil {
		t.Fatalf("handleExecutorExecute error = %v", err)
	}
	if captured.HostCallbackID != "callback-1" || captured.Model != "gpt-5.4-mini" || captured.EntryProtocol != "openai" || captured.ExitProtocol != "openai" || captured.Alt != "alt-mode" {
		t.Fatalf("captured=%#v", captured)
	}
	if !strings.Contains(string(captured.Body), `"model":"gpt-5.4-mini"`) {
		t.Fatalf("captured body=%s", captured.Body)
	}
	var resp pluginapi.ExecutorResponse
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode executor response: %v", err)
	}
	if !strings.Contains(string(resp.Payload), `"model":"deepseek-v4-pro"`) {
		t.Fatalf("payload=%s", resp.Payload)
	}
}

func TestHandleExecutorExecutePreservesHostError(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "a=>b"})
	req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "a", Format: "openai", SourceFormat: "openai", OriginalRequest: []byte(`{"model":"a"}`)}, HostCallbackID: "callback-1"}
	rawReq, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	_, err = handleExecutorExecute(rawReq, func(string, any) (json.RawMessage, error) {
		return nil, fmt.Errorf("upstream rejected model")
	})
	if err == nil || !strings.Contains(err.Error(), "upstream rejected model") {
		t.Fatalf("error=%v, want upstream error", err)
	}
}

func TestHandleExecutorExecuteReturnsErrorForHostHTTPStatus(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "a=>b"})
	req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "a", Format: "openai", SourceFormat: "openai", OriginalRequest: []byte(`{"model":"a"}`)}, HostCallbackID: "callback-1"}
	rawReq, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	_, err = handleExecutorExecute(rawReq, func(string, any) (json.RawMessage, error) {
		return json.Marshal(pluginapi.HostModelExecutionResponse{StatusCode: 404, Body: []byte(`{"error":"model not found"}`)})
	})
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("error=%v, want status and body in error", err)
	}
}
```

- [ ] **Step 3: Run tests to verify RED**

Run:

```powershell
go test ./... -run TestHandleExecutorExecute
```

Expected: FAIL because executor handler is undefined or incomplete. Add imports `fmt`, `net/http`, `net/url`, and `strings` to `main_test.go` as compiler requests.

- [ ] **Step 4: Implement minimal non-stream executor**

Implementation requirements:

- Decode `rpcExecutorRequest` from raw JSON.
- Re-run `routeModel(loadedConfig(), req.SourceFormat, req.Model)` and require `Handled:true`; if not handled, return an error because CPA should only invoke this executor after a handled route.
- Rewrite `req.OriginalRequest` top-level `model` to `decision.UpstreamModel`. If no top-level string `model` exists, still forward the original body and `HostModelExecutionRequest.Model=decision.UpstreamModel`.
- Call host with `pluginabi.MethodHostModelExecute` and `hostModelExecutionRequest{HostCallbackID:req.HostCallbackID}`.
- Preserve `Headers`, `Query`, `Alt`, `Stream`, `EntryProtocol`, `ExitProtocol` exactly as specified in Global Constraints.
- Decode `pluginapi.HostModelExecutionResponse`.
- If `resp.StatusCode >= 400`, return an error that includes the status code and response body text; CPA SDK `ExecutorResponse` has no status field, so this is the closest ABI-safe way to avoid turning upstream/CPA failures into successful executor payloads.
- For 2xx/3xx responses, restore response body top-level `model` to `decision.OriginalModel` and return `pluginapi.ExecutorResponse{Payload: restoredBody, Headers: resp.Headers}`.
- Return host callback errors as errors; do not synthesize a successful payload.

- [ ] **Step 5: Run tests to verify GREEN**

Run:

```powershell
go test ./... -run TestHandleExecutorExecute
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
go test ./...
git add main.go main_test.go
git commit -m "feat: forward mapped non-stream requests"
```

Expected: full test suite PASS.

---

### Task 11: Stream Executor forwarding through CPA stream bridge

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `sseRewriter`, `rpcExecutorRequest`, `hostModelExecutionRequest`.
- Produces: `handleExecutorExecuteStream(raw []byte, call hostCaller) ([]byte, error)`, `startExecutorStream(req rpcExecutorRequest, call hostCaller, closeStream pluginStreamCloser) ([]byte, error)`, and `runStreamForward(req rpcExecutorRequest, call hostCaller) error`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review only stream bridge behavior: start, read, rewrite, emit, close host stream, close plugin stream, and error surfacing.

- [ ] **Step 2: Write failing stream handler test**

Append to `main_test.go`:

```go
func TestHandleExecutorExecuteStreamStartsForwarderAndRestoresChunks(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "deepseek-v4-pro=>gpt-5.4-mini"})
	req := rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model:           "deepseek-v4-pro",
			Format:          "openai",
			SourceFormat:    "openai",
			Stream:          true,
			OriginalRequest: []byte(`{"model":"deepseek-v4-pro","stream":true}`),
		},
		HostCallbackID: "callback-1",
		StreamID:       "plugin-stream-1",
	}
	reads := []pluginapi.HostModelStreamReadResponse{
		{Payload: []byte("data: {\"model\":\"gpt-5.4-mini\"}\n\n")},
		{Payload: []byte("data: [DONE]\n\n")},
		{Done: true},
	}
	var emitted []string
	closedHost := false
	closedPlugin := false
	done := make(chan struct{})
	fakeHost := func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostModelExecuteStream:
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: 200, Headers: http.Header{"Content-Type": []string{"text/event-stream"}}, StreamID: "host-stream-1"})
		case pluginabi.MethodHostModelStreamRead:
			if len(reads) == 0 {
				t.Fatalf("unexpected extra stream read")
			}
			next := reads[0]
			reads = reads[1:]
			return json.Marshal(next)
		case pluginabi.MethodHostStreamEmit:
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal emit payload: %v", err)
			}
			var emit struct {
				StreamID string `json:"stream_id"`
				Payload  []byte `json:"payload"`
				Error    string `json:"error"`
			}
			if err := json.Unmarshal(raw, &emit); err != nil {
				t.Fatalf("decode emit payload: %v", err)
			}
			if emit.StreamID != "plugin-stream-1" {
				t.Fatalf("emit stream id=%q", emit.StreamID)
			}
			emitted = append(emitted, string(emit.Payload))
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostModelStreamClose:
			closedHost = true
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostStreamClose:
			closedPlugin = true
			close(done)
			return json.Marshal(map[string]any{})
		default:
			t.Fatalf("unexpected method %q", method)
			return nil, nil
		}
	}
	rawReq, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	respRaw, err := handleExecutorExecuteStream(rawReq, fakeHost)
	if err != nil {
		t.Fatalf("handleExecutorExecuteStream error = %v", err)
	}
	var resp struct {
		Headers http.Header `json:"headers"`
	}
	if err := json.Unmarshal(respRaw, &resp); err != nil {
		t.Fatalf("decode stream response: %v", err)
	}
	if resp.Headers.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("headers=%v, want text/event-stream", resp.Headers)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("stream forwarder did not close plugin stream")
	}
	joined := strings.Join(emitted, "")
	if !strings.Contains(joined, `"model":"deepseek-v4-pro"`) || strings.Contains(joined, `"model":"gpt-5.4-mini"`) || !strings.Contains(joined, "data: [DONE]") {
		t.Fatalf("emitted=%q", joined)
	}
	if !closedHost || !closedPlugin {
		t.Fatalf("closedHost=%v closedPlugin=%v", closedHost, closedPlugin)
	}
}
```

- [ ] **Step 3: Run tests to verify RED**

Run:

```powershell
go test ./... -run TestHandleExecutorExecuteStream
```

Expected: FAIL because stream handler is undefined or incomplete. Add import `time` to `main_test.go` when the compiler asks for it.

- [ ] **Step 4: Implement minimal stream forwarding**

Implementation requirements:

- Decode `rpcExecutorRequest` and require non-empty `StreamID` before starting any goroutine.
- `handleExecutorExecuteStream` must start a goroutine for the actual forwarding and return immediately with JSON equivalent to `map[string]any{"headers": http.Header{"Content-Type": []string{"text/event-stream"}}}`. Do not marshal `pluginapi.ExecutorStreamResponse` directly because its `Chunks` channel field is not JSON-marshalable. This matches the CPA c-shared stream bridge pattern from `claude-web-search-router/go/execute_stream.go`.
- The goroutine runs `runStreamForward`: re-run route decision and require `Handled:true`.
- In the goroutine, call `host.model.execute_stream` with rewritten upstream body and original `host_callback_id`.
- Decode `pluginapi.HostModelStreamResponse`; require non-empty host `StreamID`.
- In the goroutine, loop `host.model.stream_read` until `Done`.
- On each payload, pass it through `sseRewriter`; emit every returned chunk with `host.stream.emit` and the plugin `stream_id`.
- On `chunk.Error`, close the host model stream, close the plugin stream with the error string through `host.stream.close`, and stop the goroutine.
- On normal end, flush remaining bytes, emit flushed chunks, call `host.model.stream_close`, then call `host.stream.close` with empty error.
- On read/emit/flush error, close host stream and plugin stream with the error message.
- Do not buffer full stream content and do not do the stream read/emit loop synchronously inside the RPC call.

- [ ] **Step 5: Run tests to verify GREEN**

Run:

```powershell
go test ./... -run TestHandleExecutorExecuteStream
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
go test ./...
git add main.go main_test.go
git commit -m "feat: forward mapped streaming requests"
```

Expected: full test suite PASS.

---

### Task 12: RPC dispatch and thin C ABI exports

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: handler functions from Tasks 8-11.
- Produces: `handleMethod(method string, request []byte) ([]byte, error)`, `okEnvelope(v any) ([]byte, error)`, `errorEnvelope(code, message string) []byte`, `callHost(method string, payload any) (json.RawMessage, error)`, exported C functions `cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`, `cliproxyPluginShutdown`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review only dispatch and ABI thinness. Business logic must stay in pure Go handlers, not C wrappers.

- [ ] **Step 2: Write failing dispatch tests**

Append to `main_test.go`:

```go
func TestHandleMethodDispatchesRegisterAndUnknown(t *testing.T) {
	registerRaw, err := handleMethod(pluginabi.MethodPluginRegister, nil)
	if err != nil {
		t.Fatalf("handle register error = %v", err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(registerRaw, &env); err != nil {
		t.Fatalf("decode register envelope: %v", err)
	}
	if !env.OK || len(env.Result) == 0 {
		t.Fatalf("register envelope=%#v", env)
	}
	unknownRaw, err := handleMethod("unknown.method", nil)
	if err != nil {
		t.Fatalf("handle unknown returned Go error = %v", err)
	}
	if err := json.Unmarshal(unknownRaw, &env); err != nil {
		t.Fatalf("decode unknown envelope: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code != "unknown_method" {
		t.Fatalf("unknown envelope=%#v", env)
	}
}

func TestHandleMethodCountTokensUnsupportedWithoutPanic(t *testing.T) {
	raw, err := handleMethod(pluginabi.MethodExecutorCountTokens, nil)
	if err != nil {
		t.Fatalf("count tokens Go error = %v", err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode count tokens envelope: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code != "unsupported" {
		t.Fatalf("count tokens envelope=%#v", env)
	}
}
```

- [ ] **Step 3: Run tests to verify RED**

Run:

```powershell
go test ./... -run "TestHandleMethod|TestHandleMethodCountTokens"
```

Expected: FAIL because dispatch/envelope functions are undefined or incomplete.

- [ ] **Step 4: Implement dispatcher and C ABI wrappers**

Use the C preamble shape from `upstream/CLIProxyAPI/examples/plugin/host-model-callback/go/main.go`. Required dispatch map:

```go
switch method {
case pluginabi.MethodPluginRegister:
	return handlePluginRegister(request)
case pluginabi.MethodPluginReconfigure:
	return handlePluginReconfigure(request)
case pluginabi.MethodModelRoute:
	return wrapEnvelope(handleModelRoute(request))
case pluginabi.MethodExecutorExecute:
	return wrapEnvelope(handleExecutorExecute(request, callHost))
case pluginabi.MethodExecutorExecuteStream:
	return wrapEnvelope(handleExecutorExecuteStream(request, callHost))
case pluginabi.MethodExecutorCountTokens:
	return errorEnvelope("unsupported", "executor.count_tokens is not supported by model-mapper"), nil
default:
	return errorEnvelope("unknown_method", "unknown method: "+method), nil
}
```

If `handlePluginRegister` and `handlePluginReconfigure` already return enveloped bytes, do not double-wrap them. Keep the C wrappers limited to pointer conversion, `handleMethod`, and response allocation/freeing.

- [ ] **Step 5: Run tests to verify GREEN**

Run:

```powershell
go test ./... -run "TestHandleMethod|TestHandleMethodCountTokens"
```

Expected: PASS.

- [ ] **Step 6: Full unit and race check**

Run:

```powershell
go test ./...
go test -v -race -cover ./...
```

Expected: both commands PASS.

- [ ] **Step 7: Commit**

```powershell
git add main.go main_test.go
git commit -m "feat: expose CPA plugin RPC ABI"
```

---

### Task 13: Makefile builds and cross-compile guard

**Files:**
- Create: `Makefile`
- Modify: `.gitignore` if Task 0 missed a generated pattern

**Interfaces:**
- Produces commands: `make test`, `make build-windows-amd64`, `make build-linux-amd64`, `make build`, `make package`, `make install-local`, `make install-linux-amd64`, `make smoke-local`, `make clean`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review build commands only: Windows primary environment, Linux cgo guard, output paths, and no global installs.

- [ ] **Step 2: Verify `make` availability**

Run:

```powershell
make --version
```

Expected: displays GNU Make or compatible Make. If missing, stop and tell the user that GNU Make is required for the requested automation; do not install it globally.

- [ ] **Step 3: Run missing target to verify RED**

Run:

```powershell
make test
```

Expected: FAIL because `Makefile` does not exist or target is missing.

- [ ] **Step 4: Create `Makefile`**

Use this structure:

```makefile
PLUGIN_NAME := model-mapper
DIST_DIR := dist
WINDOWS_AMD64_OUT := $(DIST_DIR)/windows_amd64/$(PLUGIN_NAME).dll
LINUX_AMD64_OUT := $(DIST_DIR)/linux_amd64/$(PLUGIN_NAME).so
VERSION ?=
LINUX_AMD64_CC ?=

.PHONY: test build-windows-amd64 build-linux-amd64 build package install-local install-linux-amd64 smoke-local clean

test:
	go test ./...

build-windows-amd64:
	mkdir -p $(DIST_DIR)/windows_amd64
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -buildmode=c-shared -o $(WINDOWS_AMD64_OUT) .

build-linux-amd64:
	@if [ -z "$(LINUX_AMD64_CC)" ]; then echo "LINUX_AMD64_CC is required for linux amd64 cgo cross-compile on Windows"; exit 1; fi
	@if ! command -v "$(LINUX_AMD64_CC)" >/dev/null 2>&1; then echo "Linux amd64 cross compiler not found: $(LINUX_AMD64_CC)"; exit 1; fi
	mkdir -p $(DIST_DIR)/linux_amd64
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC=$(LINUX_AMD64_CC) go build -buildmode=c-shared -o $(LINUX_AMD64_OUT) .

build: build-windows-amd64 build-linux-amd64

package:
	go run .github/scripts/package-release.go -version "$(VERSION)" -dist $(DIST_DIR) -out $(DIST_DIR)/release

install-local: build-windows-amd64
	@if [ -z "$(CPA_PLUGINS_DIR)" ]; then echo "CPA_PLUGINS_DIR is required"; exit 1; fi
	mkdir -p "$(CPA_PLUGINS_DIR)"
	cp $(WINDOWS_AMD64_OUT) "$(CPA_PLUGINS_DIR)/$(PLUGIN_NAME).dll"

install-linux-amd64: build-linux-amd64
	@if [ -z "$(CPA_PLUGINS_DIR)" ]; then echo "CPA_PLUGINS_DIR is required"; exit 1; fi
	mkdir -p "$(CPA_PLUGINS_DIR)"
	cp $(LINUX_AMD64_OUT) "$(CPA_PLUGINS_DIR)/$(PLUGIN_NAME).so"

smoke-local: build-windows-amd64
	go run .github/scripts/smoke-local.go

clean:
	rm -rf $(DIST_DIR)
```

This Makefile intentionally uses POSIX shell commands because Git Bash is available in the project environment. If `make` uses a non-POSIX shell locally, document the failure and use direct `go test` / `go build` commands until the user provides a compatible Make.

- [ ] **Step 5: Verify GREEN for test and Windows build**

Run:

```powershell
make test
make build-windows-amd64
```

Expected: `make test` PASS and `dist/windows_amd64/model-mapper.dll` exists. If CGO compiler is missing, stop and report the exact compiler error; do not install a compiler.

- [ ] **Step 6: Verify Linux guard**

Run:

```powershell
make build-linux-amd64
```

Expected on Windows without configured cross compiler: FAIL with `LINUX_AMD64_CC is required...`. If `LINUX_AMD64_CC` is configured, expected output is `dist/linux_amd64/model-mapper.so`.

- [ ] **Step 7: Commit**

```powershell
git add Makefile .gitignore
git commit -m "build: add plugin build targets"
```

---

### Task 14: Release packaging script

**Files:**
- Create: `.github/scripts/package-release.go`
- Modify: `Makefile` only if the package command needs a path correction

**Interfaces:**
- Produces CLI: `go run .github/scripts/package-release.go -version <version> -dist dist -out dist/release`.
- Requires artifacts: `dist/windows_amd64/model-mapper.dll`, `dist/linux_amd64/model-mapper.so`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review package safety only: whitelist contents, version rule, checksum bytes, and no secrets/configs in zips.

- [ ] **Step 2: Write failing package script test through command execution**

Run:

```powershell
go run .github/scripts/package-release.go -version 0.1.0 -dist dist -out dist/release
```

Expected: FAIL because the script does not exist.

- [ ] **Step 3: Implement stdlib-only packager**

Create `.github/scripts/package-release.go` with these concrete behaviors:

- Flags: `-version`, `-dist`, `-out`.
- Version resolution: use `-version` if non-empty; else `VERSION` env; else current git tag from `git describe --tags --exact-match`; if still empty, fail with a clear message.
- Required input files:
  - `<dist>/windows_amd64/model-mapper.dll`
  - `<dist>/linux_amd64/model-mapper.so`
- Output files:
  - `<out>/model-mapper_<version>_windows_amd64.zip`
  - `<out>/model-mapper_<version>_linux_amd64.zip`
  - matching `.sha256` files.
- Zip contents: exactly the platform binary at zip root plus `README.md` if present plus `LICENSE` if present.
- Use only `archive/zip`, `crypto/sha256`, `encoding/hex`, `flag`, `os`, `os/exec`, `path/filepath`, `io`.

- [ ] **Step 4: Verify missing artifact failure**

Run:

```powershell
go run .github/scripts/package-release.go -version 0.1.0 -dist dist -out dist/release
```

Expected before Linux artifact exists: FAIL naming `dist/linux_amd64/model-mapper.so`. This confirms the packager will not silently ship incomplete release assets.

- [ ] **Step 5: Verify package success when artifacts exist**

If `dist/linux_amd64/model-mapper.so` exists from a configured cross compiler, run:

```powershell
make package VERSION=0.1.0
```

Expected: both zip files and `.sha256` files exist under `dist/release`. If Linux artifact does not exist because cross compiler is unavailable, record the guard output and continue; do not fake the `.so`.

- [ ] **Step 6: Commit**

```powershell
git add .github/scripts/package-release.go Makefile
git commit -m "build: package release artifacts"
```

---

### Task 15: GitHub CI workflow

**Files:**
- Create: `.github/workflows/build.yml`

**Interfaces:**
- Produces CI for tests, platform builds, package assets, and tag release.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review CI secret safety. It must flag any live smoke on fork PRs or any direct use of `CPA_SMOKE_API_KEY` in default PR jobs.

- [ ] **Step 2: Create workflow file**

Create `.github/workflows/build.yml` with:

```yaml
name: build

on:
  push:
    branches: [main]
    tags: ['v*']
  pull_request:
  workflow_dispatch:
    inputs:
      run_smoke:
        description: 'Run live CPA smoke test when secrets and CPA binary are available'
        required: false
        default: 'false'

permissions:
  contents: write

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.x'
      - run: go test ./...
      - run: go vet ./...

  build-package:
    needs: test
    strategy:
      matrix:
        os: [windows-latest, ubuntu-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.x'
      - name: Build Windows artifact
        if: runner.os == 'Windows'
        shell: pwsh
        run: |
          New-Item -ItemType Directory -Force dist/windows_amd64 | Out-Null
          $env:CGO_ENABLED='1'; $env:GOOS='windows'; $env:GOARCH='amd64'
          go build -buildmode=c-shared -o dist/windows_amd64/model-mapper.dll .
      - name: Build Linux artifact
        if: runner.os == 'Linux'
        run: |
          mkdir -p dist/linux_amd64
          CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildmode=c-shared -o dist/linux_amd64/model-mapper.so .
      - uses: actions/upload-artifact@v4
        with:
          name: artifact-${{ runner.os }}
          path: dist/**

  release:
    if: startsWith(github.ref, 'refs/tags/v')
    needs: build-package
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.x'
      - uses: actions/download-artifact@v4
        with:
          pattern: artifact-*
          merge-multiple: true
          path: dist
      - run: go run .github/scripts/package-release.go -version "${GITHUB_REF_NAME#v}" -dist dist -out dist/release
      - uses: softprops/action-gh-release@v2
        with:
          files: |
            dist/release/*.zip
            dist/release/*.sha256
```

- [ ] **Step 3: Verify workflow syntax by local inspection**

Run:

```powershell
git diff --check -- .github/workflows/build.yml
```

Expected: no whitespace errors. CI is fully verified by GitHub after push; do not claim remote CI passed until the run completes.

- [ ] **Step 4: Commit**

```powershell
git add .github/workflows/build.yml
git commit -m "ci: build plugin release artifacts"
```

---

### Task 16: README and deferred model-list docs consistency

**Files:**
- Create: `README.md`
- Modify: `docs/model-list-modification-plan.md` only if it conflicts with current implementation scope

**Interfaces:**
- Documents exact config, rule grammar, endpoint precedence, build commands, deployment paths, smoke env vars, and unsupported model-list module.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review docs against production code names. It must flag spaces in rule examples, wrong default `enabled`, wrong artifact paths, or any claim that model-list modification is implemented.

- [ ] **Step 2: Create README**

`README.md` must include these exact facts:

```markdown
# CPA Model Mapper Plugin

`model-mapper` is a CLIProxyAPI (CPA) native plugin. It maps text-generation request model names before CPA selects the upstream execution path, then restores the top-level response `model` field back to the client-requested model only when a rule matched and changed the request.

## Configuration

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

The plugin's own `enabled` field defaults to `true`. Empty rule fields mean the request is skipped and CPA behaves normally.

## Rule syntax

Rules are separated by `;`. A rule is `find=>replace`. Spaces and quotes are invalid.

- `*` captures text.
- `$1`, `$2`, and later numbers reuse captures.
- `\` escapes special characters.
- The selected ruleset runs left to right exactly once.

Example:

```text
deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>gpt-5.4-mini
```

Endpoint-specific rules override `global_rules` and do not stack with it:

- `claude` uses `claude_messages_rules` when non-empty.
- `openai-response` uses `codex_responses_rules` when non-empty.
- `openai` uses `openai_completions_rules` when non-empty.
- Other formats use `global_rules`.

## Build

```powershell
make test
make build-windows-amd64
make build-linux-amd64 LINUX_AMD64_CC=<cross-compiler>
make package VERSION=0.1.0
```

Artifacts:

- `dist/windows_amd64/model-mapper.dll`
- `dist/linux_amd64/model-mapper.so`

## Deploy

Windows CPA:

```text
<CPA directory>/plugins/windows/amd64/model-mapper.dll
```

Linux amd64 CPA:

```text
<CPA directory>/plugins/linux/amd64/model-mapper.so
```

## Smoke test

Live smoke uses only local ignored state under `.test-cpa/`.

Required environment variables:

- `CPA_SMOKE_API_KEY`
- `CPA_SMOKE_CPA_BIN`

Optional:

- `CPA_SMOKE_BASE_URL` defaults to `https://a3.awsl.app/v1`
- `CPA_SMOKE_PORT` defaults to `18080`

Run:

```powershell
make smoke-local
```

Do not commit `.test-cpa/`, `.env`, generated config, logs, or `dist/` artifacts.

## Model list modification

Model-list modification is not implemented in this release. See `docs/model-list-modification-plan.md` for the required future CPA host hook.
```

- [ ] **Step 3: Verify docs for forbidden secret and rule-space examples**

Run:

```powershell
if ($env:CPA_SMOKE_API_KEY) { git grep -n -- $env:CPA_SMOKE_API_KEY README.md docs/model-list-modification-plan.md docs/superpowers/specs/2026-06-30-cpa-model-mapper-design.md } else { "skip exact secret grep: CPA_SMOKE_API_KEY not set" }
git grep -n "a =>\| =>\|=> " -- README.md docs/model-list-modification-plan.md docs/superpowers/specs/2026-06-30-cpa-model-mapper-design.md
```

Expected: no match for the real key and no spaced request-mapping rule examples in README. If the future model-list doc has list examples with commas/hyphens only, keep them.

- [ ] **Step 4: Commit**

```powershell
git add README.md docs/model-list-modification-plan.md
git commit -m "docs: document model mapper plugin"
```

---

### Task 17: Local CPA smoke automation

**Files:**
- Create: `.github/scripts/smoke-local.go`
- Modify: `Makefile` only if the smoke target path changes
- Generated ignored: `.test-cpa/**`

**Interfaces:**
- Consumes env: `CPA_SMOKE_API_KEY`, `CPA_SMOKE_CPA_BIN`, optional `CPA_SMOKE_BASE_URL`, optional `CPA_SMOKE_PORT`.
- Produces command: `make smoke-local`.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask the subagent to review smoke safety only: env secrets, `.test-cpa/` isolation, process cleanup, no brittle upstream error text assertions.

- [ ] **Step 2: Run missing smoke script to verify RED**

Run:

```powershell
go run .github/scripts/smoke-local.go
```

Expected: FAIL because script does not exist.

- [ ] **Step 3: Implement smoke runner with fail-fast env checks**

Create `.github/scripts/smoke-local.go` using only Go stdlib. Required behavior:

- If `CPA_SMOKE_API_KEY` is empty, print `CPA_SMOKE_API_KEY is required for live smoke` and exit non-zero.
- If `CPA_SMOKE_CPA_BIN` is empty, print `CPA_SMOKE_CPA_BIN is required for live smoke` and exit non-zero.
- Default `CPA_SMOKE_BASE_URL` to `https://a3.awsl.app/v1`.
- Default `CPA_SMOKE_PORT` to `18080`.
- Create `.test-cpa/plugins/windows/amd64`, `.test-cpa/logs`, and `.test-cpa/tmp`.
- Copy `dist/windows_amd64/model-mapper.dll` to `.test-cpa/plugins/windows/amd64/model-mapper.dll`.
- Generate `.test-cpa/config.yaml` from env; never write the key outside `.test-cpa`.
- Start CPA with `CPA_SMOKE_CPA_BIN --config .test-cpa/config.yaml --no-browser` from working directory `.test-cpa`.
- Poll `http://127.0.0.1:<port>/v1/models` until ready or 30 seconds elapse.
- Run these live cases with `net/http`:
  1. No rules: request with model `deepseek-v4-flash`; assert HTTP status is not a plugin config failure and response body is not force-restored by plugin.
  2. OpenAI dedicated chain `deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>gpt-5.4-mini`; assert client-visible response contains `deepseek-v4-pro` in top-level `model` for non-streaming success.
  3. Unmatched model: assert plugin does not force top-level response `model` to the unmatched request model.
  4. Bad rules: assert CPA/plugin rejects config or request instead of silently using the bad rule.
  5. Nonexistent mapped upstream model: assert non-2xx or upstream error is returned and not replaced with a fake success.
  6. Wrong API key: assert CPA rejects the request.
  7. Streaming request: assert complete SSE JSON events expose original model after mapping and `data: [DONE]` passes through.
- Stop CPA in `defer`; kill the process if graceful stop fails.

Because CPA config shape can drift, inspect `upstream/CLIProxyAPI/config.example.yaml` and server flags immediately before implementing this script. Encode the exact config keys from that file in the generated `.test-cpa/config.yaml` and keep the key under `${CPA_SMOKE_API_KEY}` expansion or direct generated value only inside `.test-cpa`.

- [ ] **Step 4: Verify env guard GREEN without secrets**

Run with no smoke env:

```powershell
go run .github/scripts/smoke-local.go
```

Expected: non-zero exit with `CPA_SMOKE_API_KEY is required for live smoke` or `CPA_SMOKE_CPA_BIN is required for live smoke`. This does not prove live smoke passed; it proves the guard is safe.

- [ ] **Step 5: Run live smoke only when env exists**

Run:

```powershell
if ($env:CPA_SMOKE_API_KEY -and $env:CPA_SMOKE_CPA_BIN) { make smoke-local } else { "skip smoke: CPA_SMOKE_API_KEY or CPA_SMOKE_CPA_BIN not set" }
```

Expected with both env vars set: live smoke cases pass or report a concrete CPA/upstream failure. Expected without either env var: skip message only.

- [ ] **Step 6: Commit**

```powershell
git add .github/scripts/smoke-local.go Makefile
git commit -m "test: add isolated CPA smoke runner"
```

---

### Task 18: Final verification and release-readiness gate

**Files:**
- Review all tracked files.
- Generated ignored artifacts may exist under `dist/` and `.test-cpa/`.

**Interfaces:**
- Verifies all deliverables and records exact failures instead of claiming success.

- [ ] **Step 1: Run a subagent/workflow checkpoint**

Ask a final reviewer to inspect the diff against this plan and the design spec. It must return only blocking gaps.

- [ ] **Step 2: Run unit, vet, race, and coverage checks**

Run:

```powershell
go test ./...
go vet ./...
go test -v -race -cover ./...
```

Expected: all commands PASS. If any command fails, fix through a new TDD cycle before continuing.

- [ ] **Step 3: Run build checks**

Run:

```powershell
make test
make build-windows-amd64
make build-linux-amd64
```

Expected:

- `make test` PASS.
- `dist/windows_amd64/model-mapper.dll` exists.
- `make build-linux-amd64` either creates `dist/linux_amd64/model-mapper.so` or fails with the planned cross-compiler guard. If it fails because no cross compiler is configured, tell the user the exact missing tool/env and do not claim Linux artifact exists.

- [ ] **Step 4: Run package check when both artifacts exist**

Run:

```powershell
if (Test-Path dist/windows_amd64/model-mapper.dll -and Test-Path dist/linux_amd64/model-mapper.so) { make package VERSION=0.1.0 } else { "skip package: one or more platform artifacts missing" }
```

Expected with both artifacts: release zips and `.sha256` files exist and checksums match. Expected with missing Linux artifact: skip message only.

- [ ] **Step 5: Verify zip contents and checksums**

Run after successful package:

```powershell
Get-ChildItem dist/release/*.zip | ForEach-Object { tar -tf $_.FullName }
Get-ChildItem dist/release/*.sha256 | ForEach-Object { Get-Content $_.FullName }
```

Expected: each zip contains only the intended platform binary plus `README.md` and `LICENSE` if present; each checksum file contains one SHA-256 line for the matching zip.

- [ ] **Step 6: Run live smoke when env exists**

Run:

```powershell
if ($env:CPA_SMOKE_API_KEY -and $env:CPA_SMOKE_CPA_BIN) { make smoke-local } else { "skip smoke: CPA_SMOKE_API_KEY or CPA_SMOKE_CPA_BIN not set" }
```

Expected with env: smoke cases pass or report a concrete external failure. Expected without env: skip message only. Do not claim live smoke passed from a skip.

- [ ] **Step 7: Verify no secrets or generated artifacts are tracked**

Run:

```powershell
git status --short --ignored
git ls-files .test-cpa .env dist
if ($env:CPA_SMOKE_API_KEY) { git grep -n -- $env:CPA_SMOKE_API_KEY . } else { "skip exact secret grep: CPA_SMOKE_API_KEY not set" }
```

Expected:

- `.test-cpa/`, `.env`, and `dist/` appear only as ignored/generated state, not tracked files.
- `git ls-files .test-cpa .env dist` prints nothing.
- `git grep` prints nothing.

- [ ] **Step 8: Commit final docs or workflow fixes**

Only if Step 1-7 produced tracked changes:

```powershell
git add .
git commit -m "chore: prepare model mapper release"
```

- [ ] **Step 9: Report final status with evidence**

Report only commands actually run in this task and their exit status. If Linux cross-build or live smoke was skipped, state the exact missing env/tool instead of calling the release complete.
