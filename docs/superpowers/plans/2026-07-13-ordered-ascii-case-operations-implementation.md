# Ordered ASCII Case Operations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the existing ordered model-mapping DSL with exact standalone `\a` and `\A` entries that lowercase or uppercase only ASCII English letters in the current model name at their written positions.

**Architecture:** Keep the current semicolon splitter, mapping parser, wildcard matcher, route decision, Executor, SSE, and WebSocket paths unchanged. Add one private operation discriminator to `rule`, recognize exact standalone operations after existing semicolon splitting, and execute a byte-wise ASCII transform inside the existing single left-to-right `applyRules` loop.

**Tech Stack:** Go 1.26.2, Go standard library only, existing CPA plugin SDK, table-driven Go tests, `gofmt`, `go test`, `go vet`, GNU Make, Windows CGO toolchain, and Zig CC for Linux amd64 cross-builds.

## Global Constraints

- Work only in `C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation`.
- Use TDD: add the smallest failing proof before each production behavior change, observe RED, implement only enough for GREEN, then refactor without changing behavior.
- `\a` converts only ASCII `A-Z` to `a-z`; `\A` converts only ASCII `a-z` to `A-Z`.
- Digits, punctuation, separators, UTF-8 non-ASCII letters, CJK text, and every byte outside the relevant ASCII range remain unchanged.
- Recognize operations only when a post-split entry is exactly `\a` or `\A`.
- Preserve all existing mapping, wildcard, capture, escape, validation, endpoint selection, route, request rewrite, response restoration, SSE, and WebSocket semantics.
- A mapping match or operation execution makes `applyRules` return its boolean as true, including no-op operations; `routeModel` still handles only when the final model differs from the original.
- Do not add dependencies, interfaces, transform closures, regex behavior, Unicode casing, case-insensitive mapping, JSON aliases, or model-list changes.
- Do not modify historical design/plan files. The new design at `docs/superpowers/specs/2026-07-13-ordered-ascii-case-operations-design.md` supersedes their old blanket `find=>replace`-only statement.
- Keep local caches and generated artifacts inside the worktree. Never commit `.test-cpa/`, `.env`, logs, `dist/`, `.gocache/`, `.gomodcache/`, `.gopath/`, or `.gotmp/`.
- Do not push or publish a release.

## File Structure

- Modify `main.go`: private operation representation, exact operation parsing, ASCII conversion helper, ordered operation execution.
- Modify `main_test.go`: parser, application, ordering, route, no-op, lifecycle YAML, and regression coverage.
- Modify `README.md`: public ordered-entry syntax, ASCII-only semantics, YAML quoting, and chained example.
- Modify `CLAUDE.md`: architecture vocabulary and routing invariant.
- Create `docs/superpowers/specs/2026-07-13-ordered-ascii-case-operations-design.md`: approved feature contract.
- Create `docs/superpowers/plans/2026-07-13-ordered-ascii-case-operations-implementation.md`: this executable plan.

---

### Task 1: Parse Exact Standalone Case Operations

**Files:**
- Modify: `main_test.go:107-160`
- Modify: `main.go:869-918`

**Interfaces:**
- Consumes: existing `parseRules(raw string) ([]rule, error)` and `splitEscaped(raw string, sep byte) ([]string, error)`.
- Produces: private `caseOperation` type and `rule.caseOperation`; parsed operation entries preserve list order while mapping entries retain all existing token fields.

- [ ] **Step 1: Add a failing parser representation test**

Add after `TestParseRulesAcceptsValidRules`:

```go
func TestParseRulesAcceptsCaseOperations(t *testing.T) {
	rules, err := parseRules(`a=>b;\a;\A;c=>d`)
	if err != nil {
		t.Fatalf("parseRules error = %v", err)
	}
	if len(rules) != 4 {
		t.Fatalf("len(rules) = %d, want 4", len(rules))
	}
	want := []caseOperation{
		caseOperationNone,
		caseOperationLower,
		caseOperationUpper,
		caseOperationNone,
	}
	for i, operation := range want {
		if rules[i].caseOperation != operation {
			t.Fatalf("rules[%d].caseOperation = %v, want %v", i, rules[i].caseOperation, operation)
		}
	}
}
```

The test intentionally references the not-yet-defined type and constants so the first run proves the representation is absent.

- [ ] **Step 2: Add parser boundary rows**

Append these exact raw-string rows to `TestParseRulesRejectsInvalidRules`:

```go
`\x`,
`\a=>x`,
`\A=>x`,
`x=>\a`,
`x=>\A`,
```

Do not duplicate existing leading, trailing, or doubled-semicolon rows; they already protect `;\a`, `\a;`, and `\a;;\A` through the shared splitter.

- [ ] **Step 3: Run the focused parser tests and observe RED**

Run in PowerShell:

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
$env:GOMODCACHE = "$root\.gomodcache"
$env:GOCACHE = "$root\.gocache"
$env:GOPATH = "$root\.gopath"
$env:GOTMPDIR = "$root\.gotmp"
$env:CGO_ENABLED = "0"
& "F:\go-sdk\go1.26.2\bin\go.exe" -C $root test . -run 'TestParseRulesAcceptsCaseOperations|TestParseRulesRejectsInvalidRules' -count=1
```

Expected: FAIL to compile because `caseOperation`, `caseOperationNone`, `caseOperationLower`, `caseOperationUpper`, and `rule.caseOperation` do not exist yet. Do not weaken the test.

- [ ] **Step 4: Add the minimal operation representation**

Insert between `token` and `rule` in `main.go`:

```go
type caseOperation uint8

const (
	caseOperationNone caseOperation = iota
	caseOperationLower
	caseOperationUpper
)
```

Extend `rule` exactly as follows:

```go
type rule struct {
	patternTokens     []token
	replacementTokens []token
	captureCount      int
	caseOperation     caseOperation
}
```

Do not use nil token slices or `captureCount` as an operation sentinel.

- [ ] **Step 5: Recognize only exact standalone operation entries**

At the start of the existing `for _, part := range parts` loop in `parseRules`, before `findRuleSeparator`, add:

```go
switch part {
case `\a`:
	out = append(out, rule{caseOperation: caseOperationLower})
	continue
case `\A`:
	out = append(out, rule{caseOperation: caseOperationUpper})
	continue
}
```

Leave all other entries on the existing mandatory `find=>replace` path. Do not change `splitEscaped`, `findRuleSeparator`, `parseFind`, or `parseReplace`.

- [ ] **Step 6: Run the focused parser tests and observe GREEN**

Run the same focused command from Step 3.

Expected:

```text
ok github.com/DoingDog/cpa-plugin-model-mapper
```

- [ ] **Step 7: Run existing parser and character-semantic regressions**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
$env:GOMODCACHE = "$root\.gomodcache"
$env:GOCACHE = "$root\.gocache"
$env:GOPATH = "$root\.gopath"
$env:GOTMPDIR = "$root\.gotmp"
$env:CGO_ENABLED = "0"
& "F:\go-sdk\go1.26.2\bin\go.exe" -C $root test . -run 'TestParseRules|TestApplyRulesCharacterSemantics' -count=1
```

Expected: all selected tests PASS. In particular, existing escaped `*`, `;`, `$`, `\`, and `=>` behavior must remain unchanged.

- [ ] **Step 8: Format and commit Task 1**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
& "F:\go-sdk\go1.26.2\bin\gofmt.exe" -w "$root\main.go" "$root\main_test.go"
git -C $root diff --check
git -C $root add main.go main_test.go
git -C $root commit -m "feat(dsl): parse ordered ASCII case operations"
```

Expected: `gofmt` and `git diff --check` exit successfully; the commit contains only parser representation and parser tests.

---

### Task 2: Execute ASCII Operations in Order

**Files:**
- Modify: `main_test.go:193-263`
- Modify: `main_test.go:296-329`
- Modify: `main.go:1066-1080`

**Interfaces:**
- Consumes: `rule.caseOperation` from Task 1 and existing `applyRules(model string, rules []rule) (string, bool, error)`.
- Produces: private `applyASCIIModelCase(model string, operation caseOperation) string`; route decisions automatically reuse the transformed final model without executor-specific logic.

- [ ] **Step 1: Add failing operation behavior cases**

Append these rows to the table in `TestApplyRulesCharacterSemantics`:

```go
{name: "lowercase ASCII letters only", raw: `\a`, model: `AbC-Z_19/éΩ中`, want: `abc-z_19/éΩ中`, wantMatched: true},
{name: "uppercase ASCII letters only", raw: `\A`, model: `aBc-z_19/éω中`, want: `ABC-Z_19/éω中`, wantMatched: true},
{name: "operation before case-sensitive mapping", raw: `\a;gpt-x=>mapped`, model: `GPT-X`, want: `mapped`, wantMatched: true},
{name: "mapping before operation", raw: `foo=>bar-v2;\A`, model: `foo`, want: `BAR-V2`, wantMatched: true},
{name: "later mapping remains case-sensitive", raw: `\a;GPT-X=>wrong`, model: `GPT-X`, want: `gpt-x`, wantMatched: true},
{name: "full ordered case-operation chain", raw: `\a;gpt-*=>deepseek-V3;\A;DEEPSEEK-*=>gpt-5.5;\A`, model: `GPT-X`, want: `GPT-5.5`, wantMatched: true},
{name: "literal backslash lowercase text remains mappable", raw: `\\a=>mapped`, model: `\a`, want: `mapped`, wantMatched: true},
{name: "literal backslash uppercase text remains mappable", raw: `\\A=>mapped`, model: `\A`, want: `mapped`, wantMatched: true},
{name: "repeated lowercase operations stay ordered", raw: `\a;\a`, model: `ABC`, want: `abc`, wantMatched: true},
{name: "lowercase no-op still executes", raw: `\a`, model: `already-lower/é`, want: `already-lower/é`, wantMatched: true},
{name: "uppercase no-op still executes", raw: `\A`, model: `ALREADY-UPPER/Ω`, want: `ALREADY-UPPER/Ω`, wantMatched: true},
{name: "case operations can return to original", raw: `\a;\A`, model: `ABC`, want: `ABC`, wantMatched: true},
```

These rows distinguish ASCII-only conversion from Go's Unicode-aware case conversion and verify exact ordering.

- [ ] **Step 2: Add the empty-model operation guard test**

Add after `TestApplyRulesCharacterSemantics`:

```go
func TestApplyRulesCaseOperationRejectsEmptyModel(t *testing.T) {
	for _, raw := range []string{`\a`, `\A`} {
		t.Run(raw, func(t *testing.T) {
			mapped, matched, err := applyRules("", mustParseRules(t, raw))
			if err == nil || err.Error() != "empty mapped model" {
				t.Fatalf("mapped=%q matched=%v err=%v, want empty mapped model", mapped, matched, err)
			}
			if !matched {
				t.Fatalf("matched=false, want true for executed operation")
			}
		})
	}
}
```

- [ ] **Step 3: Add route changed/no-op/net-identity tests**

Add after `TestRouteModelHandlesOnlyMatchedChanged`:

```go
func TestRouteModelCaseOperationChanged(t *testing.T) {
	cfg := Config{Enabled: true, GlobalRules: `\A`}
	decision, err := routeModel(cfg, "openai", "model-v2")
	if err != nil {
		t.Fatalf("routeModel error = %v", err)
	}
	if !decision.Handled || decision.OriginalModel != "model-v2" || decision.UpstreamModel != "MODEL-V2" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestRouteModelCaseOperationNoChangeIsUnhandled(t *testing.T) {
	tests := []struct {
		name  string
		rules string
		model string
	}{
		{name: "lowercase no-op", rules: `\a`, model: "model-v2"},
		{name: "uppercase no-op", rules: `\A`, model: "MODEL-V2"},
		{name: "net identity", rules: `\a;\A`, model: "ABC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := routeModel(Config{Enabled: true, GlobalRules: tt.rules}, "openai", tt.model)
			if err != nil {
				t.Fatalf("routeModel error = %v", err)
			}
			if decision.Handled || decision.OriginalModel != "" || decision.UpstreamModel != "" {
				t.Fatalf("decision=%#v, want unhandled with empty models", decision)
			}
		})
	}
}
```

Do not add duplicate SSE, WebSocket, or response-body cases: existing executor tests consume the same handled `routeDecision` seam.

- [ ] **Step 4: Run operation tests and observe RED**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
$env:GOMODCACHE = "$root\.gomodcache"
$env:GOCACHE = "$root\.gocache"
$env:GOPATH = "$root\.gopath"
$env:GOTMPDIR = "$root\.gotmp"
$env:CGO_ENABLED = "0"
& "F:\go-sdk\go1.26.2\bin\go.exe" -C $root test . -run 'TestApplyRulesCharacterSemantics|TestApplyRulesCaseOperationRejectsEmptyModel|TestRouteModelCaseOperation' -count=1
```

Expected: FAIL because parsed operation rules are not yet executed as transformations; the lower/upper output assertions and changed-route assertion must fail.

- [ ] **Step 5: Add the byte-wise ASCII conversion helper**

Insert before `applyRules`:

```go
func applyASCIIModelCase(model string, operation caseOperation) string {
	converted := []byte(model)
	for i, c := range converted {
		switch operation {
		case caseOperationLower:
			if c >= 'A' && c <= 'Z' {
				converted[i] = c + ('a' - 'A')
			}
		case caseOperationUpper:
			if c >= 'a' && c <= 'z' {
				converted[i] = c - ('a' - 'A')
			}
		}
	}
	return string(converted)
}
```

Do not replace this with `strings.ToLower`, `strings.ToUpper`, regular expressions, lookup tables, or a dependency.

- [ ] **Step 6: Execute operations inside the existing ordered loop**

Replace only the loop body of `applyRules` with this structure:

```go
for _, r := range rules {
	if r.caseOperation != caseOperationNone {
		current = applyASCIIModelCase(current, r.caseOperation)
		matchedAny = true
	} else {
		captures, ok := matchTokens(current, r.patternTokens)
		if !ok {
			continue
		}
		current = buildReplacement(r.replacementTokens, captures)
		matchedAny = true
	}
	if current == "" {
		return "", true, fmt.Errorf("empty mapped model")
	}
}
```

Keep the function signature and final return unchanged.

- [ ] **Step 7: Run operation tests and observe GREEN**

Run the same command from Step 4.

Expected:

```text
ok github.com/DoingDog/cpa-plugin-model-mapper
```

- [ ] **Step 8: Run all rule and routing tests**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
$env:GOMODCACHE = "$root\.gomodcache"
$env:GOCACHE = "$root\.gocache"
$env:GOPATH = "$root\.gopath"
$env:GOTMPDIR = "$root\.gotmp"
$env:CGO_ENABLED = "0"
& "F:\go-sdk\go1.26.2\bin\go.exe" -C $root test . -run 'TestParseRules|TestApplyRules|TestRouteModel|TestHandleModelRoute' -count=1
```

Expected: all selected tests PASS. Existing identity mapping and finite net-identity chain behavior must remain intact.

- [ ] **Step 9: Format and commit Task 2**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
& "F:\go-sdk\go1.26.2\bin\gofmt.exe" -w "$root\main.go" "$root\main_test.go"
git -C $root diff --check
git -C $root add main.go main_test.go
git -C $root commit -m "feat(dsl): execute ordered ASCII case operations"
```

Expected: commit contains the ASCII helper, ordered execution branch, and operation behavior/route tests only.

---

### Task 3: Lock the Lifecycle Configuration Boundary

**Files:**
- Modify: `main_test.go:55-95`

**Interfaces:**
- Consumes: existing `decodeLifecycleConfig`, `decodeConfig`, and `parseRules` behavior plus Task 1 operation parsing.
- Produces: regression proof that the documented single-quoted YAML representation preserves one DSL backslash and that unknown standalone operations reject configuration.

- [ ] **Step 1: Add the single-quoted YAML lifecycle test**

Add after `TestDecodeLifecycleConfigUnquotesYAMLEmptyRuleStrings`:

```go
func TestDecodeLifecycleConfigPreservesCaseOperations(t *testing.T) {
	rawYAML := []byte("enabled: true\nglobal_rules: '\\a;gpt-*=>deepseek-V3;\\A'\nclaude_messages_rules: \"\"\ncodex_responses_rules: \"\"\nopenai_completions_rules: \"\"\n")
	rawReq, err := json.Marshal(map[string]string{"config_yaml": base64.StdEncoding.EncodeToString(rawYAML)})
	if err != nil {
		t.Fatalf("marshal lifecycle: %v", err)
	}
	cfgRaw, _, err := decodeLifecycleConfig(rawReq)
	if err != nil {
		t.Fatalf("decodeLifecycleConfig error = %v", err)
	}
	cfg, err := decodeConfig(cfgRaw)
	if err != nil {
		t.Fatalf("decodeConfig error = %v", err)
	}
	if cfg.GlobalRules != `\a;gpt-*=>deepseek-V3;\A` {
		t.Fatalf("global rules = %q", cfg.GlobalRules)
	}
}
```

This test must use two backslashes in the interpreted Go source for each one-backslash DSL operation; do not write a literal BEL escape.

- [ ] **Step 2: Add an unknown-operation configuration rejection test**

Extend `TestDecodeConfigDefaultAndBadRules` with JSON generated by `json.Marshal`:

```go
badOperation, err := json.Marshal(map[string]any{
	"enabled":      true,
	"global_rules": `\x`,
})
if err != nil {
	t.Fatalf("marshal bad operation config: %v", err)
}
if _, err := decodeConfig(badOperation); err == nil {
	t.Fatalf("decodeConfig unknown operation error = nil")
}
```

Using `json.Marshal` proves DSL validation rather than accidentally feeding invalid JSON.

- [ ] **Step 3: Run lifecycle/config tests**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
$env:GOMODCACHE = "$root\.gomodcache"
$env:GOCACHE = "$root\.gocache"
$env:GOPATH = "$root\.gopath"
$env:GOTMPDIR = "$root\.gotmp"
$env:CGO_ENABLED = "0"
& "F:\go-sdk\go1.26.2\bin\go.exe" -C $root test . -run 'TestDecodeLifecycleConfig|TestDecodeConfig' -count=1
```

Expected: PASS. This is an integration/contract proof over already-green parser behavior; no production change should be necessary. If it fails, fix only the exact operation boundary rather than broadening YAML parsing.

- [ ] **Step 4: Format and commit Task 3**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
& "F:\go-sdk\go1.26.2\bin\gofmt.exe" -w "$root\main_test.go"
git -C $root diff --check
git -C $root add main_test.go
git -C $root commit -m "test(config): cover case-operation YAML boundary"
```

Expected: commit contains only lifecycle/config regression tests.

---

### Task 4: Document the Ordered Entry Contract

**Files:**
- Modify: `README.md:3-42`
- Modify: `README.md:51-98`
- Modify: `CLAUDE.md:24-37`
- Verify: `docs/superpowers/specs/2026-07-13-ordered-ascii-case-operations-design.md`
- Verify: `docs/superpowers/plans/2026-07-13-ordered-ascii-case-operations-implementation.md`

**Interfaces:**
- Consumes: implemented parser/application behavior and approved design.
- Produces: public syntax contract and repository guidance matching runtime behavior.

- [ ] **Step 1: Update README terminology and routing condition**

Change the opening description so response restoration occurs when either a mapping matched or an operation executed and the final model differs from the original.

Replace the Rule syntax introduction with this contract:

```markdown
Each ruleset is a `;`-separated ordered list of entries. An entry is either a `find=>replace` mapping or an exact standalone case operation: `\a` lowercases ASCII English letters and `\A` uppercases them. Whitespace and quotes are invalid inside the decoded rule value.

- Mappings remain case-sensitive and apply to the complete current model name; later mappings see the value produced by every earlier entry.
- `\a` changes only `A` through `Z` to `a` through `z`; `\A` changes only `a` through `z` to `A` through `Z`. Non-ASCII bytes, digits, punctuation, and separators are unchanged.
- Case operations must be complete standalone entries. They are not additional backslash escapes for `find` or `replace`.
```

Retain all existing wildcard, capture, literal-character, escape, endpoint-selection, and order statements, editing only references that incorrectly say every entry is a mapping rule.

- [ ] **Step 2: Add the mixed operation example and safe YAML form**

Add this example in the Examples section:

````markdown
Ordered ASCII case operations can normalize an incoming alias, feed a case-sensitive mapping, transform its output, and continue mapping:

```text
\a;gpt-*=>deepseek-V3;\A;DEEPSEEK-*=>gpt-5.5;\A
```

For `GPT-X`, the values are processed as:

```text
GPT-X -> gpt-x -> deepseek-V3 -> DEEPSEEK-V3 -> gpt-5.5 -> GPT-5.5
```

Use YAML single quotes so the DSL backslashes are preserved:

```yaml
global_rules: '\a;gpt-*=>deepseek-V3;\A;DEEPSEEK-*=>gpt-5.5;\A'
```
````

Ensure the nested fences are rendered as separate Markdown blocks rather than literally nesting triple backticks.

- [ ] **Step 3: Update CLAUDE.md architecture guidance**

Replace the parser/application bullet with:

```markdown
- `parseRules` / `applyRules` implement an ordered entry DSL: entries are `find=>replace` mappings or exact standalone `\a` / `\A` ASCII case operations; `*` captures, `$1` references captures, and entries run left-to-right exactly once.
```

Replace the routing bullet with:

```markdown
- `handleModelRoute` routes only when a mapping matched or case operation executed and the final requested model differs from the original; changed requests are routed back to this plugin executor.
```

Add an invariant that operations change ASCII English letters only and do not make later mappings case-insensitive.

- [ ] **Step 4: Review documentation against runtime semantics**

Verify all of the following manually:

- README no longer claims every semicolon-separated entry must contain `=>`.
- README explicitly says ASCII English letters only.
- README explicitly says operations are position-sensitive and later mappings remain case-sensitive.
- README uses single-quoted YAML for operation examples.
- README and CLAUDE.md distinguish “mapping matched or operation executed” from “final model changed.”
- Historical design/plan files remain unchanged.

- [ ] **Step 5: Commit the public documentation updates**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
git -C $root diff --check
git -C $root add README.md CLAUDE.md
git -C $root commit -m "docs(dsl): document ordered ASCII case operations"
```

Expected: documentation commit contains only `README.md` and `CLAUDE.md`; the approved design and implementation plan were committed before execution began.

---

### Task 5: Full Verification, Build, and Review

**Files:**
- Verify: `main.go`
- Verify: `main_test.go`
- Verify: `README.md`
- Verify: `CLAUDE.md`
- Verify: `docs/superpowers/specs/2026-07-13-ordered-ascii-case-operations-design.md`
- Verify: `docs/superpowers/plans/2026-07-13-ordered-ascii-case-operations-implementation.md`

**Interfaces:**
- Consumes: all completed tasks.
- Produces: evidence that behavior, documentation, Windows build, Linux Zig cross-build, and repository hygiene meet the approved contract.

- [ ] **Step 1: Run formatting and whitespace checks**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
& "F:\go-sdk\go1.26.2\bin\gofmt.exe" -w "$root\main.go" "$root\main_test.go"
git -C $root diff --check
```

Expected: no `gofmt` diff after the final pass and no whitespace errors.

- [ ] **Step 2: Run the full Go test suite**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
$env:GOMODCACHE = "$root\.gomodcache"
$env:GOCACHE = "$root\.gocache"
$env:GOPATH = "$root\.gopath"
$env:GOTMPDIR = "$root\.gotmp"
$env:CGO_ENABLED = "0"
& "F:\go-sdk\go1.26.2\bin\go.exe" -C $root test ./... -count=1
```

Expected:

```text
ok github.com/DoingDog/cpa-plugin-model-mapper
```

No test may be skipped or weakened to obtain GREEN.

- [ ] **Step 3: Run Go vet**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
$env:GOMODCACHE = "$root\.gomodcache"
$env:GOCACHE = "$root\.gocache"
$env:GOPATH = "$root\.gopath"
$env:GOTMPDIR = "$root\.gotmp"
$env:CGO_ENABLED = "0"
& "F:\go-sdk\go1.26.2\bin\go.exe" -C $root vet ./...
```

Expected: exit code 0 and no diagnostics.

- [ ] **Step 4: Build Windows amd64**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
make -C $root build-windows-amd64
```

Expected: `dist/windows_amd64/model-mapper.dll` is produced. Do not stage it.

- [ ] **Step 5: Cross-build Linux amd64 with Zig CC**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
make -C $root build-linux-amd64 LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"
```

Expected: `dist/linux_amd64/model-mapper.so` is produced. Do not stage it. Do not skip this step because the host is Windows.

- [ ] **Step 6: Run a structured code review**

Review the complete change from the commit before Task 1 through HEAD. Verify:

- exact standalone operation recognition after `splitEscaped`;
- no ambiguity with `\\a=>...` or `\\A=>...` literal-backslash mappings;
- ASCII-only byte conversion;
- no Unicode case conversion;
- no change to wildcard/backtracking semantics;
- no change to endpoint-specific/global precedence;
- no-op and net-identity routes remain unhandled;
- changed routes still use existing Executor/restoration paths;
- no operation-specific SSE/WebSocket duplication;
- public docs match runtime behavior.

Use independent correctness and test-coverage reviewers. Apply only findings that are verified against the diff and rerun affected tests after each fix.

- [ ] **Step 7: Inspect repository hygiene and sensitive information**

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
git -C $root status --short
git -C $root diff --check ba46b18..HEAD
git -C $root diff --name-only ba46b18..HEAD
```

Expected committed source paths:

```text
CLAUDE.md
README.md
docs/superpowers/plans/2026-07-13-ordered-ascii-case-operations-implementation.md
docs/superpowers/specs/2026-07-13-ordered-ascii-case-operations-design.md
main.go
main_test.go
```

Confirm no secrets, API keys, credentials, private URLs, `.env`, `.test-cpa`, cache files, logs, or `dist` artifacts are tracked.

- [ ] **Step 8: Create a final fix commit only if review changed files**

If verified review findings required edits:

```powershell
$root = "C:\Users\user\Downloads\cpa-plugin\.claude\worktrees\model-mapper-implementation"
git -C $root add main.go main_test.go README.md CLAUDE.md docs/superpowers/specs/2026-07-13-ordered-ascii-case-operations-design.md docs/superpowers/plans/2026-07-13-ordered-ascii-case-operations-implementation.md
git -C $root commit -m "fix(review): tighten ASCII case-operation semantics"
```

If review required no edits, do not create an empty commit.

- [ ] **Step 9: Report completion without pushing or releasing**

Report:

- commits created;
- files changed;
- RED observations and GREEN results;
- full `go test ./...` and `go vet ./...` results;
- Windows and Linux build results;
- review findings applied or rejected;
- repository status and sensitive-information result;
- explicit confirmation that no push or release occurred.
