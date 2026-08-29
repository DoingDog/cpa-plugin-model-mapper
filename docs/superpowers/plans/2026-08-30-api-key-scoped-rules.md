# API-Key-Scoped Model Rules Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow each model-mapping entry to carry an optional exact inbound CPA client API-key condition while preserving old unscoped rules, ordered fallthrough, every current ruleset, every current inbound format, stream/non-stream behavior, and literal `#` model names.

**Architecture:** Parse `api-key#body` before the existing operation/mapping parser and immediately convert the configured key to CPA's authenticated `caller_scope` digest. At request time, compare that digest with `Metadata["caller_scope"]`; never parse raw request credential headers or query values. Continue using the existing shared `routeModel`/`applyRules` path in `model.route`, non-stream Executor, and stream Executor so `global_rules`, `claude_messages_rules`, `codex_responses_rules`, and `openai_completions_rules` receive identical semantics.

**Tech Stack:** Go 1.26, Go standard library `crypto/sha256` and `encoding/hex`, CLIProxyAPI native plugin ABI, GNU Make, existing live CPA smoke harness.

**Spec:** User requirements dated 2026-08-30; existing DSL invariants are recorded in `docs/superpowers/specs/2026-07-13-ordered-ascii-case-operations-design.md`.

## Global Constraints

- Work on `feat/api-key-scoped-rules`, created from `origin/main@76c8b616ed1be04a85a2dcb7d490e331d78dff71`.
- Grammar is `entry := [api-key '#'] ('\a' | '\A' | find '=>' replace)`; key matching is exact and case-sensitive.
- A scoped entry whose key does not match must only be skipped; later scoped and unscoped entries still run.
- `#` is the only new delimiter. A literal `#` in `find` or `replace` must be written as `\#`.
- The API key portion must be nonempty and cannot contain whitespace, quotes, `;`, `#`, or `\`; do not add key wildcards or a second escape grammar.
- Hash configured keys exactly as CPA v7.2.145 does: lowercase hex of `SHA-256("cli-proxy-api:caller-scope:v1" || NUL || strings.TrimSpace(apiKey))`.
- Read only `Metadata["caller_scope"]`. Do not infer authentication from `Authorization`, `X-Api-Key`, `X-Goog-Api-Key`, `key`, `auth_token`, body fields, or logs.
- CPA v7.2.145 is the minimum verified runtime for scoped rules. If metadata is absent or malformed, scoped entries skip and unscoped fallback remains available.
- Preserve current endpoint-specific override semantics: a nonempty dedicated ruleset replaces, rather than stacks with, `global_rules`.
- Preserve the registered format surface: `openai`, `claude`, and `openai-response`; do not add Gemini or count-token support.
- Do not add dependencies, change `go.mod`, modify `upstream/`, expose keys in errors/logs/responses, or persist real keys in tracked files.
- Update README with exactly the three requested usage scenarios and common examples.
- Use the current-folder CPA runtime and remembered real endpoint/key for live smoke. Generated config, logs, keys, `.test-cpa/`, and `dist/` remain untracked.
- Create a final local commit after all verification. Do not push, publish, tag, or open a PR.

---

### Task 1: Lock the baseline and add API-key/hash grammar

**Files:**
- Modify: `main.go:3-17,869-1081`
- Test: `main_test.go:136-224,248-297`

**Interfaces:**
- Produces: `callerScope(apiKey string) string`
- Produces: `callerScopeFromMetadata(metadata map[string]any) string`
- Produces: `rule.callerScope string`, empty for unscoped entries
- Preserves: existing `parseRules(raw string) ([]rule, error)` API

- [ ] **Step 1: Run the clean baseline**

Run:

```powershell
go test ./... -count=1
go vet ./...
```

Expected: both commands exit 0 before feature tests are added.

- [ ] **Step 2: Write failing caller-scope and parser tests**

Add these focused behaviors to `main_test.go`:

```go
func TestCallerScopeMatchesCPA(t *testing.T) {
    const want = "c512aa71887f77cd6b915a60aed04e1d0de0ed0721f041e51d1002402b3901db"
    if got := callerScope("sk-test"); got != want {
        t.Fatalf("callerScope(sk-test)=%q, want %q", got, want)
    }
    if got := callerScope("  sk-test  "); got != want {
        t.Fatalf("trimmed caller scope=%q, want %q", got, want)
    }
    if got := callerScope("   "); got != "" {
        t.Fatalf("blank caller scope=%q, want empty", got)
    }
}

func TestParseRulesAcceptsAPIKeyScopesAndEscapedHash(t *testing.T) {
    rules := mustParseRules(t, `sk-test#\a;sk-test#vendor\#model=>mapped\#name;plain\#model=>plain`)
    if len(rules) != 3 {
        t.Fatalf("len(rules)=%d, want 3", len(rules))
    }
    if rules[0].callerScope != callerScope("sk-test") || rules[0].caseOperation != caseOperationLower {
        t.Fatalf("scoped operation=%#v", rules[0])
    }
    if rules[1].callerScope != callerScope("sk-test") || rules[2].callerScope != "" {
        t.Fatalf("scopes=%q %q", rules[1].callerScope, rules[2].callerScope)
    }
}
```

Extend invalid-rule coverage with:

```go
`#a=>b`,
`sk-test#`,
`sk-test#a=>b#c`,
`sk\-test#a=>b`,
```

Add character-semantics rows proving `vendor\#model` matches `vendor#model` and `mapped\#name` emits `mapped#name`.

- [ ] **Step 3: Run the focused tests and observe RED**

Run:

```powershell
go test . -run 'TestCallerScopeMatchesCPA|TestParseRulesAcceptsAPIKeyScopesAndEscapedHash|TestParseRulesRejectsInvalidRules|TestApplyRulesCharacterSemantics' -count=1
```

Expected: compile failure because `callerScope` and `rule.callerScope` do not exist, or parser failure because `\#` is currently invalid. This is the required RED state.

- [ ] **Step 4: Implement only the hash and grammar support**

In `main.go`, add standard-library imports and constants/helpers:

```go
import (
    "crypto/sha256"
    "encoding/hex"
)

const (
    callerScopeMetadataKey = "caller_scope"
    callerScopeDomain      = "cli-proxy-api:caller-scope:v1\x00"
)

func callerScope(apiKey string) string {
    apiKey = strings.TrimSpace(apiKey)
    if apiKey == "" {
        return ""
    }
    sum := sha256.Sum256([]byte(callerScopeDomain + apiKey))
    return hex.EncodeToString(sum[:])
}

func callerScopeFromMetadata(metadata map[string]any) string {
    scope, _ := metadata[callerScopeMetadataKey].(string)
    return scope
}
```

Extend `rule`:

```go
type rule struct {
    callerScope      string
    patternTokens     []token
    replacementTokens []token
    captureCount      int
    caseOperation     caseOperation
}
```

Inside the existing `parseRules` entry loop, parse scope before recognizing `\a`/`\A`:

```go
scopeParts, err := splitEscaped(part, '#')
if err != nil || len(scopeParts) > 2 {
    return nil, fmt.Errorf("invalid rule")
}
body := scopeParts[0]
scope := ""
if len(scopeParts) == 2 {
    if strings.ContainsRune(scopeParts[0], '\\') {
        return nil, fmt.Errorf("invalid API key scope")
    }
    scope = callerScope(scopeParts[0])
    body = scopeParts[1]
}
```

Use `body` in the existing operation/mapping parser and write `callerScope: scope` into every emitted rule. Add `'#'` to the accepted escaped literals in both `parseFind` and `parseReplace`; change no other escape.

- [ ] **Step 5: Run focused and parser regression tests**

Run:

```powershell
gofmt -w main.go main_test.go
go test . -run 'TestCallerScopeMatchesCPA|TestParseRules|TestApplyRulesCharacterSemantics' -count=1
```

Expected: PASS, including all old grammar tests.

---

### Task 2: Apply exact key scopes with ordered fallthrough

**Files:**
- Modify: `main.go:386-448,1100-1120`
- Test: `main_test.go:217-452,1271-1319`

**Interfaces:**
- Changes: `applyRules(model, scope string, rules []rule) (string, bool, error)`
- Changes: `routeModel(cfg Config, format, model, scope string) (routeDecision, error)`
- Consumes: `callerScope(apiKey string)` and `rule.callerScope`

- [ ] **Step 1: Write the exact user-example test first**

Add:

```go
func TestApplyRulesAPIKeyScopeExactAndFallsThrough(t *testing.T) {
    rules := mustParseRules(t, `sk-test#\a;sk-test#claude-haiku-*=>gpt-5.6-luna;claude-haiku-*=>gpt-5.6-sol`)
    tests := []struct {
        name, key, model, want string
        matched                bool
    }{
        {name: "exact key runs scoped operation and mapping", key: "sk-test", model: "CLAUDE-HAIKU-X", want: "gpt-5.6-luna", matched: true},
        {name: "different key skips scoped entries and uses fallback", key: "sk-other", model: "claude-haiku-X", want: "gpt-5.6-sol", matched: true},
        {name: "missing key skips scoped entries and uses fallback", model: "claude-haiku-X", want: "gpt-5.6-sol", matched: true},
        {name: "key comparison is case sensitive", key: "SK-TEST", model: "CLAUDE-HAIKU-X", want: "CLAUDE-HAIKU-X", matched: false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, matched, err := applyRules(tt.model, callerScope(tt.key), rules)
            if err != nil || got != tt.want || matched != tt.matched {
                t.Fatalf("applyRules=(%q,%v,%v), want (%q,%v,nil)", got, matched, err, tt.want, tt.matched)
            }
        })
    }
}
```

Add a table test for `routeModel` covering:

```plaintext
global_rules + openai
claude_messages_rules + claude
codex_responses_rules + openai-response
openai_completions_rules + openai
```

For each row, the exact scope maps `client-model` to `scoped-target`; a wrong/missing scope skips it and reaches an unscoped `client-model=>fallback-target` entry.

- [ ] **Step 2: Run the focused tests and observe RED**

Run:

```powershell
go test . -run 'TestApplyRulesAPIKeyScopeExactAndFallsThrough|TestRouteModelAPIKeyScopeAcrossRuleSets' -count=1
```

Expected: compile failure because `applyRules` and `routeModel` do not accept scope, or wrong behavior because scoped rules execute unconditionally.

- [ ] **Step 3: Implement the one-line execution guard and pass scope through routing**

Change the rule loop:

```go
for _, r := range rules {
    if r.callerScope != "" && r.callerScope != scope {
        continue
    }
    // existing operation/mapping behavior
}
```

Change `routeModel` to accept scope and call:

```go
mapped, matched, err := applyRules(model, scope, rules)
```

Mechanically add `""` to every old test/caller that intentionally exercises unscoped rules. Do not add wrapper functions or duplicate execution loops.

- [ ] **Step 4: Verify the example, every ruleset, and all old ordering behavior**

Run:

```powershell
gofmt -w main.go main_test.go
go test . -run 'TestApplyRules|TestRouteModel|TestSelectRules|TestHandleMethodDispatchesRegisterReconfigureAndUnknown' -count=1
```

Expected: PASS. Existing full-chain, case-operation, single-pass, net-identity, and endpoint override assertions remain unchanged.

---

### Task 3: Consume authenticated scope in route and both Executors

**Files:**
- Modify: `main.go:413-448,497-505,625-637`
- Test: `main_test.go:454-493,708-1269`

**Interfaces:**
- Consumes: `callerScopeFromMetadata(metadata map[string]any) string`
- Preserves: public CPA request/response structs and current response-restoration behavior

- [ ] **Step 1: Write failing model.route metadata tests**

Add a table test that sends actual `pluginapi.ModelRouteRequest` JSON with:

```go
Metadata: map[string]any{"caller_scope": callerScope("sk-test")}
```

Use `sk-test#a=>b` and assert `Handled=true`. Repeat with absent metadata, a wrong digest, and a non-string metadata value; assert scoped-only rules are unhandled. Add `sk-test#a=>b;a=>c` and prove missing/wrong scope still produces a handled unscoped fallback.

- [ ] **Step 2: Write failing non-stream and stream Executor tests**

For non-stream, configure:

```text
sk-test#client-model=>scoped-target;client-model=>fallback-target
```

Pass correct `caller_scope` metadata and capture `HostModelExecutionRequest.Model == "scoped-target"`. Pass a wrong scope and capture `fallback-target`. Repeat in a table for `global_rules`, `claude_messages_rules`, `codex_responses_rules`, and `openai_completions_rules` with their matching source formats.

For stream, use the same scoped/unscoped chain with an OpenAI stream request and assert the captured/returned stream uses the scoped upstream target while emitted model fields are restored to `client-model`.

- [ ] **Step 3: Run focused route/Executor tests and observe RED**

Run:

```powershell
go test . -run 'TestHandleModelRouteUsesCallerScope|TestHandleExecutorExecuteUsesCallerScopeAcrossRuleSets|TestHandleExecutorExecuteStreamUsesCallerScope' -count=1
```

Expected: scoped-only requests are unhandled or the fallback target is selected because request metadata is not yet passed to `routeModel`.

- [ ] **Step 4: Pass only authenticated metadata scope into existing routing calls**

Update exactly three paths:

```go
routeModel(loadedConfig(), req.SourceFormat, req.RequestedModel, callerScopeFromMetadata(req.Metadata))
routeModel(loadedConfig(), req.SourceFormat, req.Model, callerScopeFromMetadata(req.Metadata))
```

The first form belongs to `handleModelRoute`; the second belongs to non-stream and stream Executors. Do not read request headers/query and do not include scope in host callback payloads.

- [ ] **Step 5: Verify route, non-stream, stream, and format/ruleset coverage**

Run:

```powershell
gofmt -w main.go main_test.go
go test . -run 'TestHandleModelRoute|TestHandleExecutorExecute|TestStreamChunkRewriter|TestSSERewriter' -count=1
go test ./... -count=1
```

Expected: PASS. Existing OpenAI, Claude, Responses/Codex, SSE, raw JSON/WebSocket-like, error, and restoration tests remain green.

---

### Task 4: Extend real CPA smoke across inbound formats and rulesets

**Files:**
- Modify: `.github/scripts/smoke-local.go:21-52,116-203,302-410`

**Interfaces:**
- Consumes: CPA v7.2.145+ `caller_scope` metadata
- Produces: live requests for OpenAI Chat Completions, Claude Messages, and OpenAI Responses/Codex-compatible ingress
- Preserves: existing `CPA_SMOKE_API_KEY`, `CPA_SMOKE_CPA_BIN`, `CPA_SMOKE_BASE_URL`, and `CPA_SMOKE_PORT` contract

- [ ] **Step 1: Add a second valid local client key and ruleset/format selection**

Add:

```go
const fallbackAPIKey = "fallback-local-smoke-key"
```

Extend `caseConfig` with exact internal fields:

```go
requestFormat string
rulesField    string
```

Accepted `requestFormat` values are `openai`, `claude`, and `openai-response`. Accepted `rulesField` values are `global_rules`, `claude_messages_rules`, `codex_responses_rules`, and `openai_completions_rules`. Empty values preserve the current OpenAI/default behavior for old cases.

Change `buildConfig` to receive the whole case, add both valid local keys under `api-keys`, and place `pluginRules` into only the requested field. Keep the other three fields empty. Generate rule values with YAML single quotes because `#` and `\#` belong to the DSL and no real key may be written by the generator.

- [ ] **Step 2: Add live scoped-hit/fallthrough/hash cases**

For each of the four rulesets, add two cases using a distinct client model:

```text
local-smoke-key#<client-model>=>definitely-not-a-real-upstream-model;<client-model>=>deepseek-v4-flash
```

- Exact `local-smoke-key`: expect a real non-2xx/upstream failure, proving the scoped branch selected the nonexistent model.
- Valid `fallback-local-smoke-key`: expect 2xx and restored client model, proving the scoped branch skipped and the unscoped entry ran.

Use these format/ruleset pairs:

```plaintext
openai -> global_rules
openai -> openai_completions_rules
claude -> claude_messages_rules
openai-response -> codex_responses_rules
```

Add a successful literal-hash case:

```text
client\#hash=>deepseek-v4-flash
```

Request model `client#hash` and require restored `client#hash`.

Change the existing stream case to use scoped entries only, so it fails on a CPA runtime that omits `caller_scope`.

- [ ] **Step 3: Send format-correct live requests**

Keep the existing OpenAI Chat request for `openai`.

For `claude`, call `/v1/messages`, send `X-Api-Key`, `Anthropic-Version: 2023-06-01`, and body:

```json
{"model":"<model>","max_tokens":16,"messages":[{"role":"user","content":"say ok"}],"stream":false}
```

For `openai-response`, call `/v1/responses`, send `Authorization: Bearer <key>`, and body:

```json
{"model":"<model>","input":"say ok","stream":false}
```

Reuse existing generic status/error/model assertions. Do not print request credentials.

- [ ] **Step 4: Format and compile the smoke runner before live execution**

Run:

```powershell
gofmt -w .github/scripts/smoke-local.go
go test ./... -count=1
go test .github/scripts/smoke-local.go -run '^$'
```

Expected: both commands exit 0; the second command compiles the standalone runner without executing live traffic.

---

### Task 5: Document grammar, compatibility, and exactly three use cases

**Files:**
- Modify: `README.md:24-123,164-184`
- Modify: `CLAUDE.md:24-39`

**Interfaces:**
- Documents: the same grammar and runtime contract implemented in Tasks 1-4
- Preserves: existing terms `Client-Requested Model`, `Upstream Model`, `Claude Code`, and CPA format/ruleset names

- [ ] **Step 1: Extend Rule syntax without duplicating execution semantics**

Document:

```text
entry := [api-key#](\a|\A|find=>replace)
```

State that the key is the authenticated inbound CPA client API key, not an upstream provider credential. CPA exposes only its digest to the plugin. A key mismatch skips the entry and continues later entries. Matching remains left-to-right single-pass, not first-match. Literal model-name `#` is `\#` in the rule. API keys are present in plugin config as plaintext, so protect the config.

Add the exact requested example in YAML single quotes:

```yaml
claude_messages_rules: 'sk-test#\a;sk-test#claude-haiku-*=>gpt-5.6-luna;claude-haiku-*=>gpt-5.6-sol'
```

Explain the two key outcomes and clarify that a different key with an uppercase model skips the scoped lowercase operation, so the case-sensitive fallback does not match.

State: scoped rules require CPA v7.2.145 or a later compatible runtime that publishes authenticated `caller_scope`; on older runtimes scoped entries skip while unscoped entries continue.

- [ ] **Step 2: Replace Common use cases with exactly three numbered subsections**

1. Use another upstream model from Claude Code and similar clients without changing the client model name.
2. Use a wildcard such as `claude-*=>gpt-5.4-mini` to map several requested models to one upstream model.
3. Route one authenticated inbound client API key to an administrator-selected lower-cost model, with an unscoped fallback for other keys.

For scenario 3, say the rewrite happens inside CPA and supported response model fields are restored. Do not claim all logs/errors/fields hide the upstream model, and do not promise automatic or guaranteed cost savings.

- [ ] **Step 3: Update the repository architecture note**

In `CLAUDE.md`, extend the existing DSL line to mention optional authenticated API-key scope, `caller_scope`, and `\#`; state that route/non-stream/stream must pass the same metadata-derived scope.

- [ ] **Step 4: Verify documentation examples against tests**

Run:

```powershell
go test . -run 'TestCallerScopeMatchesCPA|TestParseRulesAcceptsAPIKeyScopesAndEscapedHash|TestApplyRulesAPIKeyScopeExactAndFallsThrough' -count=1
git diff --check
```

Expected: PASS and no whitespace errors.

---

### Task 6: Live test, review, full verification, and one local commit

**Files:**
- Review: `main.go`
- Review: `main_test.go`
- Review: `.github/scripts/smoke-local.go`
- Review: `README.md`
- Review: `CLAUDE.md`
- Review: `docs/superpowers/plans/2026-08-30-api-key-scoped-rules.md`

**Interfaces:**
- Produces: one verified local Git commit
- Prohibits: push, publish, tag, and PR creation

- [ ] **Step 1: Run the complete local quality gate**

Run:

```powershell
gofmt -w main.go main_test.go .github/scripts/smoke-local.go
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
git diff --check
make build-windows-amd64
```

If `LINUX_AMD64_CC` is already configured, also run:

```powershell
make build-linux-amd64 LINUX_AMD64_CC="$env:LINUX_AMD64_CC"
```

Do not install a compiler merely for this task.

- [ ] **Step 2: Run current-folder CPA live smoke**

Resolve `CPA_SMOKE_CPA_BIN` to the actual current-folder CPA executable and use the real endpoint/key from existing session state without printing either value. Run:

```powershell
make smoke-local
```

Required evidence: all old cases plus scoped hit/fallback for global, OpenAI, Claude, and Responses/Codex rulesets; literal `#`; scoped streaming; wrong key remains rejected. A skipped smoke is not a pass.

- [ ] **Step 3: Check generated state and secrets**

Run:

```powershell
git status --short
git status --ignored --short .test-cpa dist
```

Confirm `.test-cpa/` and `dist/` are ignored, no generated config/log/artifact is tracked, and no real endpoint key appears in the diff. Do not print the real key while checking.

- [ ] **Step 4: Run independent implementation and security review**

Review the final diff for:

```plaintext
exact grammar and \# handling
scope mismatch fallthrough
caller_scope algorithm parity with CPA v7.2.145
missing/malformed metadata fail-closed behavior
all four rulesets and three formats
route/non-stream/stream consistency
no raw request credential parsing
no secret output or tracked generated state
README accuracy and exactly three use cases
```

Apply only verified findings, then rerun every affected focused test and the complete quality gate.

- [ ] **Step 5: Create one commit and do not push**

Run:

```powershell
git add main.go main_test.go .github/scripts/smoke-local.go README.md CLAUDE.md docs/superpowers/plans/2026-08-30-api-key-scoped-rules.md
git commit -m "feat: scope model rules by API key"
git status --short --branch
git log -1 --oneline
```

Expected: commit succeeds, worktree is clean, branch remains local/tracking `origin/main`, and no `git push` command is executed.
