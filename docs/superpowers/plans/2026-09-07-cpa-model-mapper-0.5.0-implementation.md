# CPA Model Mapper 0.5.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 发布 `v0.5.0`，完整支持 CPA 五种 canonical format、三态规则叠加、规则项注释和插件 Logo，同时修复已确认的 stream、header、配置编译和 ASCII case 性能问题。

**Architecture:** 保留单 package 和现有 ModelRouter -> static-scope Executor -> host callback 结构。配置在 lifecycle 边界一次性校验并编译为 immutable rule slices，request path 只选择并顺序执行至多两组规则。请求和响应继续使用现有精确 JSON 重写边界，stream 继续复用 `streamChunkRewriter`。

**Tech Stack:** Go 1.26、CLIProxyAPI SDK `v7.2.152`、标准库 `encoding/json`、`net/http`、cgo、`gopkg.in/yaml.v3`、Go test、race detector、checkptr、GitHub Actions。

**Spec:** `docs/superpowers/specs/2026-09-07-cpa-model-mapper-0.5.0-design.md`

## Global Constraints

- 只修改 `cpa-plugin-model-mapper` 仓库，不读取或修改与插件无关的 CLIProxyAPI 源码。
- production behavior change 必须先有 focused failing Go test，再写最小实现，再运行 focused GREEN。
- `ExecutorInputFormats` 与 `ExecutorOutputFormats` 都使用 `openai`、`openai-response`、`claude`、`gemini`、`interactions` 的精确顺序。
- `rules_stack_mode` 只接受 `off`、`specific_first`、`global_first`；省略或空字符串归一为 `off`。
- Gemini 与 Interactions 只使用 `global_rules`，不增加专属配置。
- request 只改顶层 string `model`；response 只恢复 `model`、`modelVersion`、`message.model`、`response.model`、`response.modelVersion`。
- 不拼接 raw rules，不向共享 compiled slice `append`，不增加 request-path rules allocation。
- 不实现 arbitrary raw JSON 跨 callback buffering、format-specific wire framing、recursive response rewrite 或 caller cache policy。
- 不运行 `go test ./.github/scripts`。
- `.claude/`、`.test-cpa/` 和 `dist/` 不得提交。
- 所有提交使用当前 feature branch `feature/v0.5.0`，完成全量验证后才 fast-forward 合并和发布。

---

### Task 1: Freeze Baseline

**Files:**
- Read-only: repository source and tests
- Do not modify tracked files

**Interfaces:**
- Consumes: current `v0.4.4` behavior at `d53c30d111f804887eb9721efb1f1e32bffe1193`
- Produces: correctness and performance baseline for later comparison

- [x] **Step 1: Verify branch and refs**

Run:

```powershell
git status --short --branch
git rev-parse HEAD
git rev-parse main
git rev-parse origin/main
git describe --tags --exact-match HEAD
git tag --list v0.5.0
```

Expected: local and remote `main` equal `d53c30d111f804887eb9721efb1f1e32bffe1193`, HEAD has tag `v0.4.4`, `v0.5.0` is absent, and only `.claude/` is untracked.

- [x] **Step 2: Create feature branch**

```powershell
git switch -c feature/v0.5.0
```

- [x] **Step 3: Run correctness baseline**

```powershell
go test ./... -count=1
go vet ./...
go test -race ./... -count=1
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
```

Expected: all commands pass.

- [x] **Step 4: Record performance baseline**

```powershell
go test . -run '^$' -bench 'Benchmark(ApplyRules|RestoreResponse|SSE|StreamChunk)' -benchmem -benchtime=500ms -count=5
```

Expected: benchmark output records `ns/op`, `B/op` and `allocs/op` for later same-toolchain comparison.

---

### Task 2: Commit The Durable Design And Plan

**Files:**
- Create: `docs/superpowers/specs/2026-09-07-cpa-model-mapper-0.5.0-design.md`
- Create: `docs/superpowers/plans/2026-09-07-cpa-model-mapper-0.5.0-implementation.md`

**Interfaces:**
- Consumes: approved `.claude/plan/cozy-churning-flurry.md`
- Produces: committed release contract and executable TDD plan

- [x] **Step 1: Write and self-review the design spec**

The design spec must define the exact five-format registration, Logo URL, config enum, stacking matrix, comment grammar, rewrite whitelist, bug fixes, performance gates, non-goals and release success conditions.

Run:

```powershell
git diff --check -- docs/superpowers/specs/2026-09-07-cpa-model-mapper-0.5.0-design.md
```

Search the file for unfinished markers and ambiguous placeholder phrases prohibited by the writing-plans skill. Expected: no matches.

- [x] **Step 2: Commit the design spec**

```powershell
git add -- docs/superpowers/specs/2026-09-07-cpa-model-mapper-0.5.0-design.md
git commit -m "docs: specify model mapper v0.5.0"
```

- [x] **Step 3: Write and self-review this implementation plan**

The plan must use checkbox steps, focused RED/GREEN commands, exact files and commit boundaries. It must cover every requirement in the spec without production placeholders.

Run:

```powershell
git diff --check -- docs/superpowers/plans/2026-09-07-cpa-model-mapper-0.5.0-implementation.md
```

Search for every unfinished marker prohibited by the writing-plans skill and for undefined helper names. Expected: no matches.

- [ ] **Step 4: Commit this implementation plan**

```powershell
git add -- docs/superpowers/plans/2026-09-07-cpa-model-mapper-0.5.0-implementation.md
git commit -m "docs: plan model mapper v0.5.0"
```

---

### Task 3: Upgrade The CPA SDK And Registration Metadata

**Files:**
- Modify: `main_test.go:21-55`
- Modify: `main.go:708-731`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: `pluginRegistration() registration`
- Produces: registration with exact five-format direct executor capabilities, Logo, and enum schema

- [ ] **Step 1: Write the registration RED test**

Replace the existing registration expectations with exact assertions equivalent to:

```go
func TestPluginRegistrationMetadataAndConfigFields(t *testing.T) {
    got := pluginRegistration()
    wantFormats := []string{"openai", "openai-response", "claude", "gemini", "interactions"}
    if !reflect.DeepEqual(got.Capabilities.ExecutorInputFormats, wantFormats) {
        t.Fatalf("input formats = %v, want %v", got.Capabilities.ExecutorInputFormats, wantFormats)
    }
    if !reflect.DeepEqual(got.Capabilities.ExecutorOutputFormats, wantFormats) {
        t.Fatalf("output formats = %v, want %v", got.Capabilities.ExecutorOutputFormats, wantFormats)
    }
    if got.Metadata.Logo != "https://raw.githubusercontent.com/DoingDog/cpa-plugin-model-mapper/refs/heads/main/logo.png" {
        t.Fatalf("logo = %q", got.Metadata.Logo)
    }
    if len(got.Metadata.ConfigFields) != 5 {
        t.Fatalf("config fields = %d, want 5", len(got.Metadata.ConfigFields))
    }
    field := got.Metadata.ConfigFields[4]
    if field.Name != "rules_stack_mode" || field.Type != pluginapi.ConfigFieldTypeEnum {
        t.Fatalf("stack field = %#v", field)
    }
    if !reflect.DeepEqual(field.EnumValues, []string{"off", "specific_first", "global_first"}) {
        t.Fatalf("enum values = %v", field.EnumValues)
    }
    if !strings.Contains(field.Description, "off") || !strings.Contains(strings.ToLower(field.Description), "default") {
        t.Fatalf("description = %q", field.Description)
    }
}
```

Keep assertions for name, author, repository, version, schema version, model router, executor and static scope.

- [ ] **Step 2: Confirm RED**

```powershell
go test . -run '^TestPluginRegistrationMetadataAndConfigFields$' -count=1
```

Expected: failure reports missing `interactions`, incomplete output formats, empty Logo or missing fifth field.

- [ ] **Step 3: Upgrade only the CPA SDK**

```powershell
go get github.com/router-for-me/CLIProxyAPI/v7@v7.2.152
go mod tidy
go mod verify
```

Inspect:

```powershell
git diff -- go.mod go.sum
```

Expected: CLIProxyAPI changes from `v7.2.48` to `v7.2.152`; YAML remains the only other required module and no unrelated direct dependency is added.

- [ ] **Step 4: Implement registration metadata**

In `pluginRegistration`, add:

```go
Logo: "https://raw.githubusercontent.com/DoingDog/cpa-plugin-model-mapper/refs/heads/main/logo.png",
```

Append this config field after the existing four fields:

```go
{
    Name:        "rules_stack_mode",
    Type:        pluginapi.ConfigFieldTypeEnum,
    Description: "Controls global and endpoint-specific rule order; default off preserves endpoint-specific override behavior.",
    EnumValues:  []string{"off", "specific_first", "global_first"},
},
```

Use one local five-format slice only if doing so does not make the returned input and output slices share mutable storage; otherwise use two literals:

```go
[]string{"openai", "openai-response", "claude", "gemini", "interactions"}
```

- [ ] **Step 5: Run GREEN and repository checks**

```powershell
go test . -run '^TestPluginRegistrationMetadataAndConfigFields$' -count=1
go test ./... -count=1
go vet ./...
```

Expected: all pass.

- [ ] **Step 6: Commit SDK and registration changes**

```powershell
git add -- main.go main_test.go go.mod go.sum
git commit -m "build: update CPA SDK and plugin metadata"
```

---

### Task 4: Validate And Publish `rules_stack_mode`

**Files:**
- Modify: `main_test.go:57-220,1969-2088`
- Modify: `main.go:682-815,1236-1272,1635-1637`

**Interfaces:**
- Consumes: `decodeConfig(json.RawMessage) (Config, error)`, `decodeLifecycleConfig([]byte)`, `compileConfig(Config)`, `loadedConfig()`
- Produces: `Config.RulesStackMode string`, private `Config.compiled bool`, `publishLoadedConfig(Config)`

- [ ] **Step 1: Write JSON mode table tests**

Add a table covering these exact cases:

```go
cases := []struct {
    name    string
    raw     string
    want    string
    wantErr bool
}{
    {"omitted", `{}`, "off", false},
    {"empty", `{"rules_stack_mode":""}`, "off", false},
    {"off", `{"rules_stack_mode":"off"}`, "off", false},
    {"specific first", `{"rules_stack_mode":"specific_first"}`, "specific_first", false},
    {"global first", `{"rules_stack_mode":"global_first"}`, "global_first", false},
    {"unknown", `{"rules_stack_mode":"both"}`, "", true},
    {"uppercase", `{"rules_stack_mode":"OFF"}`, "", true},
    {"hyphen", `{"rules_stack_mode":"specific-first"}`, "", true},
    {"non string", `{"rules_stack_mode":true}`, "", true},
}
```

For successful cases assert `cfg.compiled` and exact normalized value.

- [ ] **Step 2: Write YAML and atomic reconfigure tests**

Encode lifecycle YAML using the existing helper and cover omitted, empty, all three valid values and invalid `OFF`. Add a reconfigure test that:

1. publishes `global_rules: old=>target` with `rules_stack_mode: off`;
2. attempts invalid `rules_stack_mode: OFF`;
3. asserts the call returns an error;
4. asserts `loadedConfig()` still routes `old` to `target` and retains `off`.

- [ ] **Step 3: Confirm RED**

```powershell
go test . -run 'Test(DecodeConfig.*RulesStackMode|DecodeLifecycleConfig.*RulesStackMode|Reconfigure.*RulesStackMode)' -count=1
```

Expected: tests fail because the field, normalization or validation is absent.

- [ ] **Step 4: Add config fields and constants**

Add:

```go
const (
    rulesStackModeOff           = "off"
    rulesStackModeSpecificFirst = "specific_first"
    rulesStackModeGlobalFirst   = "global_first"
)
```

Extend `Config`:

```go
RulesStackMode string `json:"rules_stack_mode"`
compiled       bool
```

Extend `lifecycleYAMLConfig` with:

```go
RulesStackMode string `yaml:"rules_stack_mode"`
```

and include it in the YAML-to-JSON `Config` value.

- [ ] **Step 5: Normalize and validate once**

Make `defaultConfig` return a compiled empty snapshot:

```go
func defaultConfig() Config {
    return Config{RulesStackMode: rulesStackModeOff, compiled: true}
}
```

At the start of `compileConfig`:

```go
if cfg.RulesStackMode == "" {
    cfg.RulesStackMode = rulesStackModeOff
}
switch cfg.RulesStackMode {
case rulesStackModeOff, rulesStackModeSpecificFirst, rulesStackModeGlobalFirst:
default:
    return Config{}, fmt.Errorf("invalid rules_stack_mode %q", cfg.RulesStackMode)
}
```

After all four rulesets compile successfully, set `cfg.compiled = true`.

Make `decodeConfig` call `compileConfig(cfg)` even for empty input and `{}` so one path owns normalization and validation.

- [ ] **Step 6: Separate compile from publish**

Add a publish-only helper:

```go
func publishLoadedConfig(cfg Config) {
    loadedConfigMu.Lock()
    callerPatternCacheMu.Lock()
    loadedCfg = cfg
    callerPatternCache = make(map[callerPatternCacheKey]bool)
    callerPatternCacheMu.Unlock()
    loadedConfigMu.Unlock()
}
```

Make `applyLifecycleConfig` call `decodeConfig` once and then `publishLoadedConfig(cfg)`. Make `setLoadedConfigForTest` call `compileConfig` at most once and publish only the result. Tests only pass valid raw configs through this helper, so an invalid test fixture should panic rather than publish a partially compiled snapshot:

```go
func setLoadedConfigForTest(cfg Config) {
    compiled, err := compileConfig(cfg)
    if err != nil {
        panic(err)
    }
    publishLoadedConfig(compiled)
}
```

- [ ] **Step 7: Run GREEN, cache regressions and race**

```powershell
go test . -run 'Test(DecodeConfig|DecodeLifecycleConfig|PluginRegister|Reconfigure|SetLoadedConfig)' -count=20
go test -race . -run 'Test(DecodeConfig|Reconfigure|SetLoadedConfig)' -count=1
```

Expected: all pass, invalid reconfigure does not publish, and cache reset tests remain green.

- [ ] **Step 8: Commit config validation**

```powershell
git add -- main.go main_test.go
git commit -m "feat: validate rule stack mode"
```

---

### Task 5: Skip Commented Rule Entries Before Validation

**Files:**
- Modify: `main_test.go:404-545`
- Modify: `main.go:1639-1861`

**Interfaces:**
- Consumes: `parseRules`, `parseFind`, `parseReplace`, existing `splitEscaped` for caller `#`
- Produces: `splitRuleEntries(string) []string`, `hasUnescapedCommentMarker(string) bool`

- [ ] **Step 1: Write comment behavior table tests**

Add one parse table whose accepted inputs include:

```go
[]string{
    `!whole entry`,
    `a=>b!trailing`,
    `a!middle=>b`,
    `a=>b;! ignored ;c=>d`,
    `\a;nihao!gpt***==>>>;*=>gpt-4`,
    `! contains whitespace and "quotes" and '$99' and #bad##scope and trailing\`,
    `first=>second;!bad=>=>rule;second=>third`,
}
```

Because unescaped `;` always delimits entries, construct comments containing a literal semicolon with `\;`, not an unescaped semicolon. Assert the active-comment-active example compiles exactly two rules and maps `first` to `third`. Assert an all-comment ruleset compiles to zero rules without error.

- [ ] **Step 2: Write literal `!` and backslash parity tests**

Cover:

```go
cases := []struct {
    rules string
    model string
    key   string
    want  string
}{
    {`hello\!world=>mapped`, `hello!world`, "", `mapped`},
    {`hello=>mapped\!model`, `hello`, "", `mapped!model`},
    {`key\!*#hello=>mapped`, `hello`, `key!value`, `mapped`},
}
```

Add direct comment-marker parity cases:

```text
\!       escaped literal marker, active entry
\\!     unescaped marker, comment entry
\\\!   escaped literal marker, active entry
\\\\! unescaped marker, comment entry
```

Represent these cases with raw Go strings so the number of backslashes is unambiguous.

- [ ] **Step 3: Preserve empty-entry failures**

Add or retain rejection cases for:

```text
;a=>b
a=>b;
a=>b;;b=>c
;
```

Also keep every existing active-entry invalid syntax case.

- [ ] **Step 4: Confirm RED**

```powershell
go test . -run 'Test(ParseRules|ApplyRules).*Comment|TestParseRules.*Bang|TestParseRulesRejectsInvalidRules' -count=1
```

Expected: comment cases fail before implementation while old invalid-entry cases remain rejected.

- [ ] **Step 5: Implement delimiter-only entry splitting**

Add:

```go
func splitRuleEntries(raw string) []string {
    entries := make([]string, 0, 1+strings.Count(raw, ";"))
    start := 0
    backslashes := 0
    for i := 0; i < len(raw); i++ {
        if raw[i] == '\\' {
            backslashes++
            continue
        }
        if raw[i] == ';' && backslashes%2 == 0 {
            entries = append(entries, raw[start:i])
            start = i + 1
        }
        backslashes = 0
    }
    return append(entries, raw[start:])
}
```

This helper does not reject escapes. Active-entry parsers remain responsible for dangling and invalid escape errors.

Add:

```go
func hasUnescapedCommentMarker(entry string) bool {
    backslashes := 0
    for i := 0; i < len(entry); i++ {
        if entry[i] == '\\' {
            backslashes++
            continue
        }
        if entry[i] == '!' && backslashes%2 == 0 {
            return true
        }
        backslashes = 0
    }
    return false
}
```

- [ ] **Step 6: Reorder `parseRules` validation**

Replace the global whitespace/quote scan and `splitEscaped(raw, ';')` with:

1. `parts := splitRuleEntries(raw)`;
2. for each part, skip immediately when `hasUnescapedCommentMarker(part)` is true;
3. reject `part == ""`;
4. run the existing whitespace and quote scan on that active part only;
5. continue through existing caller scope, case operation and mapping parsing.

Do not special-case malformed comment content after it has been classified as a comment.

- [ ] **Step 7: Decode `\!` only where literals are parsed**

In `parseFind`, add `'!'` to the existing one-byte escaped literal cases:

```go
case '*', ';', '$', '#', '!', '\\':
```

In `parseReplace`, accept `\!` beside the existing `\#` case. Do not broaden replacement escapes beyond the explicit DSL.

- [ ] **Step 8: Run GREEN and repeated parser regressions**

```powershell
go test . -run 'Test(ParseRules|ApplyRules|Caller).*' -count=20
```

Expected: all comment, literal, caller and existing parser tests pass.

- [ ] **Step 9: Commit comment support**

```powershell
git add -- main.go main_test.go
git commit -m "feat: support commented rule entries"
```

---

### Task 6: Select And Apply Two Ordered Rule Sets

**Files:**
- Modify: `main_test.go:343-404,700-980,1825-2172`
- Modify: `main.go:682-692,843-925,1017-1027,1146-1160`

**Interfaces:**
- Consumes: compiled rule slices, `applyRules`, caller credential recovery and cache
- Produces: `ruleSelection{first, second []rule}`, two-stage `routeModel`

- [ ] **Step 1: Add the complete selection matrix test**

Define cases for all three dedicated formats:

```go
specificFormats := []struct {
    format string
    set    func(*Config, string)
}{
    {"openai", func(c *Config, raw string) { c.OpenAICompletionsRules = raw }},
    {"claude", func(c *Config, raw string) { c.ClaudeMessagesRules = raw }},
    {"openai-response", func(c *Config, raw string) { c.CodexResponsesRules = raw }},
}
```

For each format and mode, compile config with G `client=>global` and D `client=>specific`. Assert selected rule counts and route result:

| mode | first | second | result |
|---|---|---|---|
| `off` | D | empty | `specific` |
| `specific_first` | D | G | `specific`, because G does not match it |
| `global_first` | G | D | `global`, because D does not match it |

Use an additional chained pair `client=>specific` and `specific=>global` to prove D -> G, and `client=>global` with `global=>specific` to prove G -> D.

- [ ] **Step 2: Add fallback, global-only and net-identity tests**

For every dedicated format and mode, verify empty D and comments-only D both use G. For `gemini` and `interactions`, verify all three modes use only G even if unrelated dedicated fields are populated.

Add:

```text
D: client=>middle
G: middle=>client
mode: specific_first
```

Expected: a rule matched but final model equals original, so `Handled` is false.

- [ ] **Step 3: Add caller tests across both groups**

For `specific_first`, place a wildcard positive or inverse caller rule only in G. For `global_first`, place it only in D. For each location assert:

- matching authenticated credential routes;
- a client header whose digest does not equal `caller_scope` cannot route;
- missing `caller_scope` skips scoped rules;
- after the route cache is warm, executor still routes when an interceptor changes credential headers.

- [ ] **Step 4: Add no-allocation selection assertion**

Compile an exact-rule config before measurement. Use `testing.AllocsPerRun(1000, ...)` around `selectRules` and two-stage route with an already compiled exact literal mapping. Assert selection itself is zero allocations; retain existing route allocation expectations instead of requiring zero where replacement output must allocate.

- [ ] **Step 5: Confirm RED**

```powershell
go test . -run 'Test(RuleSelection|RouteModel.*Stack|Caller.*Stack|Executor.*Stack|SelectRules.*Alloc)' -count=1
```

Expected: stacking and global-only `interactions` cases fail because current selection returns one ruleset.

- [ ] **Step 6: Add `ruleSelection` and compiled-set selection**

Add:

```go
type ruleSelection struct {
    first  []rule
    second []rule
}
```

Make `selectRules(cfg Config, format string) ruleSelection` select the dedicated compiled slice by format. Treat `len(specific) == 0` as absent. Return G only for Gemini, Interactions and unknown formats.

For dedicated formats:

```go
switch cfg.RulesStackMode {
case rulesStackModeSpecificFirst:
    return ruleSelection{first: specific, second: global}
case rulesStackModeGlobalFirst:
    return ruleSelection{first: global, second: specific}
default:
    return ruleSelection{first: specific}
}
```

Collapse empty sides without copying slices, so a selection never contains an empty first group followed by a non-empty second group.

- [ ] **Step 7: Keep direct test configs compatible without a hot-path compile**

At the start of `routeModel`, compile only when `cfg.compiled` is false:

```go
if !cfg.compiled {
    var err error
    cfg, err = compileConfig(cfg)
    if err != nil {
        return routeDecision{}, err
    }
}
```

Production `loadedConfig()` snapshots are already compiled, so this branch does no request-path parsing or allocation.

- [ ] **Step 8: Apply both groups in order**

In `routeModel`, apply `selection.first` and then `selection.second` to the previous output. Combine match results with OR. Return unhandled if no rule matched or final model equals the original.

Use the existing `applyRules` twice rather than duplicating its loop.

- [ ] **Step 9: Scan caller patterns in both groups**

Change `callerAPIKeyForSelectedRules` to inspect both selected slices. Call `callerAPIKey` only if either group contains at least one wildcard `callerPattern`. Exact digest-only scopes still do not need the raw credential.

If a test passes an uncompiled config directly, compile it in the test before calling this helper. Do not add request-path parsing to the helper.

- [ ] **Step 10: Run GREEN, regressions and race**

```powershell
go test . -run 'Test(SelectRules|RouteModel|Caller|HandleModelRoute|ExecutorReusesCaller|HandleExecutorExecuteUsesCaller)' -count=20
go test ./... -count=1
go test -race ./... -count=1
```

Expected: all pass and existing auth boundaries remain unchanged.

- [ ] **Step 11: Commit ordered stacking**

```powershell
git add -- main.go main_test.go
git commit -m "feat: support ordered global rule stacking"
```

---

### Task 7: Cover Direct Executor Behavior For All Five Formats

**Files:**
- Modify: `main_test.go:1825-1968,2173-2354,3131-3215`
- Modify: `main.go` only if a test exposes a format gate after Task 3 registration

**Interfaces:**
- Consumes: `handleExecutorExecute`, `runStreamForward`, fake host callback helpers
- Produces: table-driven direct-path contracts for five canonical formats

- [ ] **Step 1: Add nonstream format table**

Create a subtest for each of:

```go
formats := []string{"openai", "openai-response", "claude", "gemini", "interactions"}
```

Use `global_rules: client-model=>upstream-model` so Gemini and Interactions use their required global-only path. For OpenAI, OpenAI Responses and Claude, also retain existing dedicated-field tests.

The fake `MethodHostModelExecute` callback must assert:

```go
req.EntryProtocol == format
req.ExitProtocol == format
req.Model == "upstream-model"
```

For four rows, use a JSON request containing top-level `model`, nested `model`, content text and tool argument text. Assert only the top-level field changes. For Gemini, use a request body without top-level `model` and assert bytes are unchanged while `req.Model` is mapped.

Return a response containing all five whitelisted paths plus opaque nested/content/tool fields. Assert only the whitelist is restored to `client-model`.

- [ ] **Step 2: Add stream format table**

Use the existing fake host stream sequence for each format. Assert the execute-stream request has matching EntryProtocol, ExitProtocol and mapped Model. Feed one complete SSE event or one complete raw JSON chunk already supported by `streamChunkRewriter`; assert whitelist restoration and opaque content preservation.

Do not add a format switch or invent Gemini/Interactions framing.

- [ ] **Step 3: Run the table before production edits**

```powershell
go test . -run 'TestHandleExecutorExecute(AllFormats|StreamAllFormats)' -count=1
```

Expected after Task 3 and Task 6: tests may already pass. A passing focused test is acceptable because this task adds coverage for generic code; do not force a production change to manufacture RED.

- [ ] **Step 4: Fix only an observed format gate**

If a row fails, trace the exact existing condition and remove only the gate that excludes a canonical format. Do not add per-format request or response transformation logic.

- [ ] **Step 5: Run executor and response regressions**

```powershell
go test . -run 'Test(HandleExecutor|RewriteRequest|RestoreResponse|StreamChunk|SSE)' -count=20
go test -race . -run 'TestHandleExecutorExecute(AllFormats|StreamAllFormats)' -count=1
```

- [ ] **Step 6: Commit format coverage**

If only tests changed:

```powershell
git add -- main_test.go
git commit -m "test: cover direct mapping for all CPA formats"
```

If a production format gate changed, include `main.go` and use:

```powershell
git commit -m "feat: support all CPA executor formats"
```

---

### Task 8: Remove Stale `Content-Length` Only After Payload Changes

**Files:**
- Modify: `main_test.go:1825-1910,2355-2474`
- Modify: `main.go:1017-1043,1146-1192`

**Interfaces:**
- Consumes: changed boolean from `rewriteRequestModel` and `restoreResponseModel`
- Produces: correct host/client headers after payload length changes

- [ ] **Step 1: Write request-header RED tests**

For nonstream and stream executor requests, set:

```go
Headers: http.Header{"Content-Length": []string{"999"}}
```

Use a body whose top-level `model` changes length. In the host callback assert `payload.Headers.Get("Content-Length") == ""`.

Add no-body-change controls using a Gemini-like body without top-level `model`; assert the host still receives `Content-Length: 999`.

- [ ] **Step 2: Write response-header RED tests**

Return a nonstream host response with `Content-Length: 999` and a body whose model fields are restored. Assert the client `ExecutorResponse.Headers` no longer contains the header.

Return a host response without any whitelisted field and assert `Content-Length: 999` is preserved.

- [ ] **Step 3: Confirm RED**

```powershell
go test . -run 'TestHandleExecutor.*ContentLength' -count=1
```

Expected: changed request and response cases expose the stale value; no-change controls pass.

- [ ] **Step 4: Delete request header on actual rewrite**

In both executor paths, capture `changed`:

```go
body, changed, err := rewriteRequestModel(req.OriginalRequest, decision.UpstreamModel)
if err != nil { /* existing error */ }
if changed {
    req.Headers.Del("Content-Length")
}
```

`http.Header.Del` is safe on nil maps. Do not delete any other header.

- [ ] **Step 5: Delete response header on actual restore**

In `handleExecutorExecute`:

```go
payload, changed, err := restoreResponseModel(hostResp.Body, decision.OriginalModel)
if err != nil { /* existing error */ }
if changed {
    hostResp.Headers.Del("Content-Length")
}
```

- [ ] **Step 6: Run GREEN and executor regressions**

```powershell
go test . -run 'TestHandleExecutor' -count=20
go test ./... -count=1
```

- [ ] **Step 7: Commit the header fix**

```powershell
git add -- main.go main_test.go
git commit -m "fix: drop stale content length after model rewrites"
```

---

### Task 9: Flush Pending Stream Bytes On Host Read-Call Errors

**Files:**
- Modify: `main_test.go:3069-3245`
- Modify: `main.go:1065-1144`

**Interfaces:**
- Consumes: `streamChunkRewriter.Flush`, `emitRewritten`, host stream close callbacks
- Produces: a shared local flush+emit seam used on read-call error, `chunk.Error` and normal EOF

- [ ] **Step 1: Write the read-error regression**

Add `TestRunStreamForwardFlushesPendingBytesOnReadError` using a fake host callback with this sequence:

1. `MethodHostModelExecuteStream` returns status 200, stream ID and `Content-Type: text/event-stream`.
2. First `MethodHostModelStreamRead` returns an unterminated payload `data: {"model":"upstream"}`.
3. Second read returns `sentinelReadError` as the callback error.
4. `MethodHostStreamEmit` records payloads.
5. `MethodHostModelStreamClose` records host closure.

Call `runStreamForward` directly. Assert:

```go
errors.Is(err, sentinelReadError)
strings.Contains(joinedEmits, `"model":"client"`)
hostClosed == true
```

Use a bounded channel or context timeout consistent with existing stream tests so a hang fails deterministically.

- [ ] **Step 2: Confirm RED**

```powershell
go test . -run '^TestRunStreamForwardFlushesPendingBytesOnReadError$' -count=1
```

Expected: the returned error wraps the sentinel and host closes, but no pending restored payload was emitted.

- [ ] **Step 3: Extract a local flush+emit closure**

After `rewriter` and `emit` are defined, add:

```go
flushAndEmit := func() error {
    flushed, err := rewriter.Flush()
    if err != nil {
        return fmt.Errorf("flush stream rewriter: %w", err)
    }
    if err := emitRewritten(flushed, rewriter.frameRawJSONAsSSE, emit); err != nil {
        return fmt.Errorf("emit flushed stream chunk: %w", err)
    }
    return nil
}
```

Use this closure for normal EOF and the existing `chunk.Error` path to remove duplicate behavior without changing close order.

- [ ] **Step 4: Flush before returning a read-call error**

On `call(MethodHostModelStreamRead, ...)` error:

```go
readErr := fmt.Errorf("read host stream: %w", err)
if flushErr := flushAndEmit(); flushErr != nil {
    _ = closeHost()
    return fmt.Errorf("%v; %w", flushErr, readErr)
}
if closeErr := closeHost(); closeErr != nil {
    return fmt.Errorf("close host stream: %v; %w", closeErr, readErr)
}
return readErr
```

This preserves `errors.Is(err, sentinelReadError)` even when cleanup also fails. The outer stream starter remains responsible for closing the plugin stream with the returned error.

- [ ] **Step 5: Run GREEN and all close-path regressions**

```powershell
go test . -run 'Test(RunStreamForward|HandleExecutorExecuteStream.*(Error|Close|Flush|Unterminated))' -count=20
go test -race . -run 'Test(RunStreamForward|HandleExecutorExecuteStream.*(Error|Close|Flush))' -count=1
```

- [ ] **Step 6: Commit the stream fix**

```powershell
git add -- main.go main_test.go
git commit -m "fix: flush pending bytes on host stream read errors"
```

---

### Task 10: Remove Proven Redundant Work

**Files:**
- Modify: `main_test.go:1600-1824,1969-2010`
- Modify: `main.go:792-815,1863-1878,300-330`

**Interfaces:**
- Consumes: `applyASCIIModelCase`, lifecycle compile/publish split from Task 4, `restoreResponseModelCandidate`
- Produces: zero-allocation ASCII no-op, one lifecycle compile, benchmark-gated SSE marker path

- [ ] **Step 1: Add ASCII no-op allocation test and benchmarks**

Add:

```go
func TestApplyASCIIModelCaseNoOpAllocations(t *testing.T) {
    tests := []struct {
        model string
        op    caseOperation
    }{
        {"already-lower-123", caseOperationLower},
        {"ALREADY-UPPER-123", caseOperationUpper},
        {"模型-123", caseOperationLower},
    }
    for _, tt := range tests {
        if got := testing.AllocsPerRun(1000, func() {
            _ = applyASCIIModelCase(tt.model, tt.op)
        }); got != 0 {
            t.Fatalf("allocations = %v, want 0", got)
        }
    }
}
```

Add benchmarks named `BenchmarkApplyASCIIModelCaseAlreadyLower`, `AlreadyUpper`, `LowerChanged` and `UpperChanged`.

- [ ] **Step 2: Confirm ASCII RED and record baseline**

```powershell
go test . -run '^TestApplyASCIIModelCaseNoOpAllocations$' -count=1
go test . -run '^$' -bench '^BenchmarkApplyASCIIModelCase' -benchmem -benchtime=500ms -count=5
```

Expected: at least one no-op case allocates before the lazy-copy implementation.

- [ ] **Step 3: Implement lazy copy**

Scan first without converting:

```go
needsChange := false
for i := 0; i < len(model); i++ {
    c := model[i]
    if operation == caseOperationLower && c >= 'A' && c <= 'Z' ||
        operation == caseOperationUpper && c >= 'a' && c <= 'z' {
        needsChange = true
        break
    }
}
if !needsChange {
    return model
}
```

Only then create `converted := []byte(model)` and run the existing ASCII conversion loop. Return non-ASCII bytes unchanged.

- [ ] **Step 4: Verify ASCII GREEN and changed output**

```powershell
go test . -run 'TestApplyASCIIModelCase' -count=20
go test . -run '^$' -bench '^BenchmarkApplyASCIIModelCase' -benchmem -benchtime=500ms -count=5
```

Expected: no-op benchmarks report `0 B/op` and `0 allocs/op`; changed outputs match existing tests without allocation regression beyond measurement noise.

- [ ] **Step 5: Add lifecycle compile benchmark and invariant test**

Add `BenchmarkApplyLifecycleConfig` using a long but valid ruleset encoded through the existing lifecycle helper. Add `TestApplyLifecycleConfigPublishesCompiledSnapshot` and assert `loadedConfig().compiled` is true and route does not need lazy compilation.

The baseline before Task 4 can be reconstructed from `v0.4.4` only if needed; the code invariant is the authoritative proof that `applyLifecycleConfig` calls `decodeConfig` followed by `publishLoadedConfig`, not `setLoadedConfigForTest`.

- [ ] **Step 6: Verify lifecycle config and race**

```powershell
go test . -run 'TestApplyLifecycleConfigPublishesCompiledSnapshot|Test(SetLoadedConfig|Reconfigure)' -count=20
go test -race . -run 'TestApplyLifecycleConfigPublishesCompiledSnapshot|Test(SetLoadedConfig|Reconfigure)' -count=1
go test . -run '^$' -bench '^BenchmarkApplyLifecycleConfig$' -benchmem -benchtime=500ms -count=5
```

- [ ] **Step 7: Add two SSE marker comparison benchmarks**

Use the same complete SSE event with a whitelisted model marker for both benchmarks:

```go
func BenchmarkSSEMarkerGuardRestore(b *testing.B) {
    // call mightContainResponseModelField(value), then restoreResponseModel(value)
}
func BenchmarkSSEMarkerGuardCandidate(b *testing.B) {
    // call mightContainResponseModelField(value), then restoreResponseModelCandidate(value)
}
```

Both benchmark loops must validate the same output before `b.ResetTimer()` and report allocations.

- [ ] **Step 8: Run five paired samples and apply the gate**

```powershell
go test . -run '^$' -bench '^BenchmarkSSEMarkerGuard' -benchmem -benchtime=500ms -count=5
```

Retain the production change only when every candidate sample is faster than the corresponding guard+restore sample and candidate `B/op` and `allocs/op` do not increase.

If the gate passes, change the already guarded single-data branch in `rewriteEvent` from:

```go
restored, changed, err := r.restoreResponseModel(value)
```

to:

```go
restored, changed, _, err := r.restoreResponseModelCandidate(value)
```

If the gate fails, leave production code unchanged and keep only benchmarks that provide useful regression coverage.

- [ ] **Step 9: Run performance-related correctness tests**

```powershell
go test . -run 'Test(ApplyASCIIModelCase|SSE|StreamChunk|ApplyLifecycleConfig|Reconfigure)' -count=20
go test -race ./... -count=1
```

- [ ] **Step 10: Commit only retained optimizations**

```powershell
git add -- main.go main_test.go
git commit -m "perf: avoid redundant model mapping work"
```

The commit description and release report must state whether the SSE candidate passed; do not claim an optimization that was not retained.

---

### Task 11: Add CI Coverage And Update Maintainer And User Documentation

**Files:**
- Modify: `.github/workflows/build.yml:32-42`
- Modify: `README.md`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: finalized `v0.5.0` behavior
- Produces: CI gate and documentation matching the implementation

- [ ] **Step 1: Add the smoke helper CI step**

Insert after release compatibility checker tests:

```yaml
      - name: Test local smoke helper
        run: go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

Do not alter triggers, permissions, build matrix, release dependencies or release commands.

- [ ] **Step 2: Update README format and metadata documentation**

Document exact input/output support for `openai`, `openai-response`, `claude`, `gemini`, `interactions`. State that Logo is built into registration and needs no config.

Update config examples to include:

```yaml
rules_stack_mode: off
```

Document the three exact enum values and the matrix from the spec. State that Gemini and Interactions always use only `global_rules`.

- [ ] **Step 3: Update README DSL documentation**

Document:

```text
!ignored entry
active=>mapped;anything!ignored;next=>result
literal\!bang=>mapped
```

State that comment detection precedes syntax validation, comments-only dedicated rules fall back to global, and leading/trailing/double semicolon remains invalid unless the corresponding entry itself contains an unescaped `!`.

- [ ] **Step 4: Update README rewrite boundaries**

State request rewriting changes only top-level string `model`. List the exact five response restoration paths. State opaque content and tool text are not recursively rewritten.

- [ ] **Step 5: Update `CLAUDE.md`**

Add the smoke helper test command to Common commands. Update Architecture overview to five formats and five plugin-owned fields. Replace the old non-stacking statement with the three-mode selection semantics and comment grammar. Record both selected slices in caller credential recovery, stale `Content-Length` deletion and read-error pending flush invariants.

- [ ] **Step 6: Run documentation-adjacent gates**

```powershell
git diff --check
go test ./... -count=1
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
```

- [ ] **Step 7: Commit CI and docs separately**

```powershell
git add -- .github/workflows/build.yml
git commit -m "ci: run smoke helper tests"
git add -- README.md CLAUDE.md
git commit -m "docs: document model mapper v0.5.0"
```

---

### Task 12: Run Partitioned Opus Review And Fix Verified Findings

**Files:**
- Review partition A: `main.go`, `main_test.go`
- Review partition B: `go.mod`, `go.sum`, `README.md`, `CLAUDE.md`, `.github/workflows/build.yml`, current spec and plan
- Modify only files implicated by verified findings

**Interfaces:**
- Consumes: diff `v0.4.4...HEAD`
- Produces: verified finding list and focused TDD fixes

- [ ] **Step 1: Request two non-overlapping reviews**

Use Opus with xhigh effort. Reviewer A checks correctness, security, concurrency, request/response boundaries, DSL semantics and test gaps only in `main.go` and `main_test.go`. Reviewer B checks dependency exactness, documentation consistency, workflow behavior and release contract only in its assigned files.

Neither reviewer reads the other partition. Both compare against the current spec and report only concrete failure scenarios with file and line.

- [ ] **Step 2: Verify findings before editing**

For each finding, reproduce it with a focused test or command. Reject findings that require changing a non-goal, lack protocol evidence, duplicate an existing test, or concern CPA code outside this repository.

- [ ] **Step 3: Fix each surviving behavior finding with TDD**

For each confirmed behavior bug:

1. add one focused failing test;
2. run it and record the expected failure;
3. make the smallest production change;
4. run focused GREEN and relevant regressions;
5. commit with `fix:` and a concrete subject.

Documentation-only mismatches may be corrected without a Go RED test, followed by `git diff --check` and the relevant command gate.

- [ ] **Step 4: Audit changed paths**

```powershell
git diff --name-only v0.4.4...HEAD
git status --short
```

Expected: no CLIProxyAPI source, module cache, `upstream/`, `.test-cpa/`, `dist/`, `.claude/` or unrelated file appears in commits.

---

### Task 13: Verify The Feature Branch Completely

**Files:**
- Verify all tracked changes
- Do not modify source unless a gate exposes a defect

**Interfaces:**
- Consumes: reviewed feature branch
- Produces: clean, releasable `feature/v0.5.0` HEAD

- [ ] **Step 1: Invoke verification-before-completion and run static gates**

```powershell
git diff --check
git diff --check v0.4.4...HEAD
go mod verify
go vet ./...
```

- [ ] **Step 2: Run correctness, concurrency and unsafe gates**

```powershell
go test ./... -count=1
go test -race ./... -count=1
go test -gcflags=all=-d=checkptr=2 ./... -count=1
```

- [ ] **Step 3: Run each script test set explicitly**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
```

- [ ] **Step 4: Build both locally required platforms**

```powershell
make build-windows-amd64
make build-linux-amd64 LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"
```

Expected:

```text
dist/windows_amd64/model-mapper.dll
dist/linux_amd64/model-mapper.so
```

- [ ] **Step 5: Package version `0.5.0`**

```powershell
make package VERSION=0.5.0
```

Inspect every locally produced zip. Its root may contain only the platform dynamic library and optional `LICENSE`. Recompute SHA-256 and compare it with the corresponding sha256sum-format line, whose filename must be the archive basename.

- [ ] **Step 6: Re-run retained performance benchmarks**

```powershell
go test . -run '^$' -bench 'Benchmark(ApplyASCIIModelCase|ApplyLifecycleConfig|SSEMarkerGuard|ApplyRules|RestoreResponse|SSE|StreamChunk)' -benchmem -benchtime=500ms -count=5
```

Compare the same benchmark names and toolchain. Confirm no-op ASCII is zero allocation and every retained optimization satisfies its gate.

- [ ] **Step 7: Run live smoke only with both required variables**

Check `CPA_SMOKE_API_KEY` and `CPA_SMOKE_CPA_BIN`. If both are non-empty:

```powershell
make smoke-local
```

If either is missing, record the exact missing variable as skipped. Do not present skipped live smoke as passed.

- [ ] **Step 8: Confirm clean tracked state**

```powershell
git status --short
git diff --name-only v0.4.4...HEAD
git log --oneline --decorate v0.4.4..HEAD
```

Expected: `.claude/` may remain untracked until final release cleanup; all intended source and docs changes are committed; `dist/` remains ignored.

---

### Task 14: Reconcile Remote, Merge, Tag And Publish

**Files:**
- Git refs and GitHub release assets
- Delete only: `.claude/plan/cozy-churning-flurry.md` after remote release verification

**Interfaces:**
- Consumes: verified feature HEAD
- Produces: local and remote `main`, annotated `v0.5.0`, successful GitHub release with verified assets

- [ ] **Step 1: Fetch remote state and reject tag conflicts**

```powershell
git fetch --prune --tags
git ls-remote --tags origin refs/tags/v0.5.0 refs/tags/v0.5.0^{}
git rev-parse origin/main
```

If either remote tag ref exists, stop without moving or overwriting it and report the conflict. If `origin/main` advanced, rebase the unpublished feature branch on the new remote main and rerun every Task 13 gate.

- [ ] **Step 2: Finish the development branch by fast-forward**

Invoke `superpowers:finishing-a-development-branch`, then:

```powershell
git switch main
git merge --ff-only feature/v0.5.0
```

No merge commit and no force operation.

- [ ] **Step 3: Re-run release gates on local `main`**

Run exactly:

```powershell
git diff --check
git diff --check v0.4.4...HEAD
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

- [ ] **Step 4: Create the annotated tag**

```powershell
git tag -a v0.5.0 -m "v0.5.0"
git rev-parse main
git rev-list -n 1 v0.5.0
```

Expected: commit IDs are identical.

- [ ] **Step 5: Push branch and tag atomically**

```powershell
git push --atomic origin main refs/tags/v0.5.0
```

This action is explicitly authorized by the release request. Do not retry with force if it fails.

- [ ] **Step 6: Locate and watch the tag workflow**

```powershell
$head = git rev-parse main
$runs = gh run list --workflow Build --branch v0.5.0 --limit 5 --json databaseId,event,headBranch,headSha,status,conclusion,url | ConvertFrom-Json
$run = $runs | Where-Object { $_.event -eq "push" -and $_.headBranch -eq "v0.5.0" -and $_.headSha -eq $head } | Select-Object -First 1
if ($null -eq $run) { throw "v0.5.0 tag workflow not found" }
gh run watch $run.databaseId --exit-status
```

Select the run whose event is `push`, head branch is `v0.5.0` and head SHA equals local `main`. If the workflow fails, diagnose the failing job. A transient failure may rerun against the same `v0.5.0` tag and commit. If source changes are required after the tag push, keep `v0.5.0` immutable, stop its publication, and require a new version such as `v0.5.1`.

- [ ] **Step 7: Verify release metadata and asset set**

```powershell
gh release view v0.5.0 --json url,isDraft,isPrerelease,tagName,targetCommitish,assets
```

Expected: not draft, not prerelease, exact tag `v0.5.0`, and these eight assets:

```text
model-mapper_0.5.0_linux_amd64.zip
model-mapper_0.5.0_linux_arm64.zip
model-mapper_0.5.0_darwin_amd64.zip
model-mapper_0.5.0_darwin_arm64.zip
model-mapper_0.5.0_windows_amd64.zip
model-mapper_0.5.0_windows_arm64.zip
model-mapper_0.5.0_freebsd_amd64.zip
checksums.txt
```

- [ ] **Step 8: Download and verify every asset**

Create a new temporary directory outside tracked source and run:

```powershell
$releaseDir = Join-Path ([IO.Path]::GetTempPath()) ("model-mapper-v0.5.0-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $releaseDir | Out-Null
gh release download v0.5.0 --dir $releaseDir
```

Parse `checksums.txt`, recompute SHA-256 for all seven zip files and require exact matches. For each zip, list root entries and require exactly one platform dynamic library plus optional root `LICENSE`; reject nested directories or extra files.

- [ ] **Step 9: Confirm all published refs agree**

```powershell
git rev-parse main
git rev-parse origin/main
git rev-list -n 1 v0.5.0
git ls-remote origin refs/heads/main refs/tags/v0.5.0^{}
```

Expected: local main, remote main and peeled annotated tag commit are identical.

- [ ] **Step 10: Remove only the temporary plan file**

Delete:

```text
.claude/plan/cozy-churning-flurry.md
```

Do not delete or modify any other `.claude/` file. Confirm the temporary plan is absent and no `.claude/` path is tracked.

- [ ] **Step 11: Report the verified release**

Report:

- release URL;
- exact commit and annotated tag;
- local test, race, checkptr, vet, script, build and package results;
- benchmark results for retained optimizations;
- live smoke result or exact skipped variables;
- GitHub Actions run URL and conclusion;
- seven zip assets, checksum verification and zip-root verification.
