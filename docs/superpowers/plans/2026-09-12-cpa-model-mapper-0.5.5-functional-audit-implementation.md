# CPA Model Mapper v0.5.5 Functional Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix five reproduced plugin-repository defects without patching CPA-owned behavior or changing the established mapping DSL.

**Architecture:** Keep runtime fixes at the existing caller-cache and stream-classification boundaries in `main.go`. Keep smoke fixes inside the existing local helper and Make target. Two Sonnet 5 1M workers may execute the runtime and smoke tracks concurrently because their file sets do not overlap; the Opus 5 1M coordinator reviews and verifies the integrated diff.

**Tech Stack:** Go 1.26, Go standard library, GNU Make, CPA plugin ABI v7.2.152, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-12-cpa-model-mapper-0.5.5-functional-audit-design.md`

## Global Constraints

- Modify only this plugin repository. Do not edit CPA or add a `}event:` normalization workaround.
- Add no dependency, no new version file, no whole-stream buffer, and no speculative hardening.
- Preserve the five registered formats: `openai`, `openai-response`, `claude`, `gemini`, and `interactions`.
- Preserve Gemini unframed JSON behavior, valid unterminated SSE-data cleanup, response-model field allowlisting, header invalidation rules, wildcard non-backtracking, and the bounded two-generation caller cache.
- `model.route` must never use an unbound wildcard cache hit; executor callbacks must retain their existing post-interceptor cache reuse.
- Workers must not stage, commit, tag, push, or read old spec/plan files. Each worker reads and edits only its assigned source/test/documentation files.
- Runtime worker files: `main.go`, `main_test.go`, `README.md`, `CLAUDE.md`.
- Smoke worker files: `.github/scripts/smoke-local.go`, `.github/scripts/smoke-local_test.go`, `.github/scripts/check-release-compatibility_test.go`, `Makefile`.

---

### Task 1: Isolate wildcard caller-cache reuse by callback role

**Files:**
- Modify: `main_test.go` near `TestExecutorReusesCallerPatternAfterHeadersChange`
- Modify: `main.go` in `routeModel`, `callerPatternMatch`, `callerMatchesRule`, and `applyRules`

**Interfaces:**
- Consumes: existing `callerScope`, `callerAPIKeyForSelectedRules`, `callerPatternCache`, and ordered rule evaluation.
- Produces: `routeModelWithCallerCache(cfg Config, format, model, scope, key string, allowUnboundCache bool) (routeDecision, error)`, `applyRulesWithCallerCache(model, scope, key string, rules []rule, allowUnboundCache bool) (string, bool, error)`, `callerMatchesRuleWithCallerCache(r *rule, scope, key string, allowUnboundCache bool) bool`, and `callerPatternMatchWithCallerCache(r *rule, scope, key string, allowUnboundCache bool) (bool, bool)`. The existing `routeModel`, `applyRules`, `callerMatchesRule`, and `callerPatternMatch` signatures remain available as safe wrappers.

- [ ] **Step 1: Write the failing route-cache regression**

Add a table test equivalent to:

```go
func TestModelRouteDoesNotReuseCallerPatternWithoutBoundCredential(t *testing.T) {
	tests := []struct {
		name, rules, key string
	}{
		{name: "positive wildcard", rules: "sk-*#client=>target", key: "sk-team"},
		{name: "inverse wildcard", rules: "#sk-*#client=>target", key: "ak-team"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLoadedConfigForTest(Config{GlobalRules: tt.rules})
			metadata := map[string]any{callerScopeMetadataKey: callerScope(tt.key)}
			route := func(model string, headers http.Header) pluginapi.ModelRouteResponse {
				raw, err := json.Marshal(pluginapi.ModelRouteRequest{
					SourceFormat: "openai", RequestedModel: model,
					Headers: headers, Metadata: metadata,
				})
				if err != nil { t.Fatal(err) }
				responseRaw, err := handleModelRoute(raw)
				if err != nil { t.Fatal(err) }
				var response pluginapi.ModelRouteResponse
				if err := json.Unmarshal(responseRaw, &response); err != nil { t.Fatal(err) }
				return response
			}
			if route("other", http.Header{"Authorization": {"Bearer " + tt.key}}).Handled {
				t.Fatal("warm-up model unexpectedly routed")
			}
			if route("client", nil).Handled {
				t.Fatal("unbound request reused a caller-pattern cache entry")
			}
		})
	}
}
```

- [ ] **Step 2: Run the regression and verify red**

Run:

```bash
go test . -run '^TestModelRouteDoesNotReuseCallerPatternWithoutBoundCredential$' -count=1 -v
```

Expected: both subtests fail because the second route returns `Handled:true` from the warmed cache.

- [ ] **Step 3: Add the minimum role-aware cache policy**

Keep safe wrapper signatures for existing callers and tests:

```go
func routeModel(cfg Config, format, model, scope, key string) (routeDecision, error) {
	return routeModelWithCallerCache(cfg, format, model, scope, key, false)
}
```

Move the current route body into `routeModelWithCallerCache` and call `applyRulesWithCallerCache` for both selected rule slices. Define `applyRulesWithCallerCache(model, scope, key string, rules []rule, allowUnboundCache bool) (string, bool, error)` and keep `applyRules` as a `false` wrapper. Change the executor calls in `prepareExecutorStream` and `handleExecutorExecute` to call `routeModelWithCallerCache(..., true)`; `handleModelRoute` continues to call the safe `routeModel` wrapper.

At the cache boundary, compute whether the current key is bound before accepting a route-mode cache hit:

```go
func callerPatternMatchWithCallerCache(r *rule, scope, key string, allowUnboundCache bool) (bool, bool) {
	authenticated := key != "" && callerScope(key) == scope
	if !allowUnboundCache && !authenticated {
		return false, false
	}
	// Existing bounded current/previous lookup stays here.
	// On a miss, return false when authenticated is false; otherwise evaluate and cache.
}
```

Keep `callerPatternMatch` as a safe `false` wrapper for direct tests and benchmarks. Define `callerMatchesRuleWithCallerCache(r *rule, scope, key string, allowUnboundCache bool) bool`; have `applyRulesWithCallerCache` call it and keep `callerMatchesRule` as a `false` wrapper. Do not change exact-scope or inversion logic.

- [ ] **Step 4: Run cache behavior tests and verify green**

Run:

```bash
go test . -run '^(TestModelRouteDoesNotReuseCallerPatternWithoutBoundCredential|TestExecutorReusesCallerPatternAfterHeadersChange|TestExecutorReusesCallerStackAfterWarmRoute|TestCallerPatternCacheIsBounded)$' -count=1 -v
```

Expected: all pass. The new route test is safe, and both executor-reuse suites remain green.

### Task 2: Preserve raw JSON before every transport-split SSE prefix

**Files:**
- Modify: `main_test.go` at `TestStreamChunkRewriterFramesRawJSONBeforeSSEDoneInSameWrite`
- Modify: `main.go` in `streamChunkRewriter.Write`

**Interfaces:**
- Consumes: `isSSEChunk`, `isIncompleteSSEPrefix`, `sseRewriter.Write`, `restoreResponseModel`, and `frameRawJSON`.
- Produces: partition-invariant output for a raw JSON prefix followed by complete or partial SSE fields.

- [ ] **Step 1: Extend the existing mixed-stream test to every suffix partition**

Use one expected output and split the SSE suffix after every byte:

```go
func TestStreamChunkRewriterFramesRawJSONBeforeSSEDoneAcrossPartitions(t *testing.T) {
	jsonPrefix := []byte(`{"model":"upstream","choices":[]}` + "\n\n")
	sseSuffix := []byte("data: [DONE]\n\n")
	want := "data: {\"choices\":[],\"model\":\"client\"}\n\ndata: [DONE]\n\n"
	for split := 0; split <= len(sseSuffix); split++ {
		r := newStreamChunkRewriter("client")
		r.format = "openai"
		r.frameRawJSONAsSSE = true
		first, err := r.Write(append(bytes.Clone(jsonPrefix), sseSuffix[:split]...))
		if err != nil { t.Fatalf("split %d first: %v", split, err) }
		second, err := r.Write(sseSuffix[split:])
		if err != nil { t.Fatalf("split %d second: %v", split, err) }
		finished, err := r.Finish()
		if err != nil { t.Fatalf("split %d finish: %v", split, err) }
		got := string(bytes.Join(append(append(first, second...), finished...), nil))
		if got != want { t.Fatalf("split %d output=%q, want %q", split, got, want) }
	}
}
```

- [ ] **Step 2: Run the partition test and verify red**

Run:

```bash
go test . -run '^TestStreamChunkRewriterFramesRawJSONBeforeSSEDoneAcrossPartitions$' -count=1 -v
```

Expected: splits inside `data:` fail with an unframed upstream model JSON prefix.

- [ ] **Step 3: Reuse the existing mixed raw/SSE branch for partial prefixes**

In the branch that decodes one raw JSON value and classifies the suffix, accept either classifier:

```go
if len(suffix) > 0 && (isSSEChunk(suffix) || isIncompleteSSEPrefix(suffix)) {
	restored, _, err := r.sse.restoreResponseModel(raw)
	if err != nil { return nil, err }
	chunks, err := r.sse.Write(suffix)
	if err != nil { return nil, err }
	return append([][]byte{r.frameRawJSON(restored)}, chunks...), nil
}
```

Do not alter `splitJSONValues`, add chunk-boundary newlines, or normalize glued CPA events.

- [ ] **Step 4: Run stream partition coverage**

Run:

```bash
go test . -run '^(TestStreamChunkRewriterFramesRawJSONBeforeSSEDoneAcrossPartitions|TestStreamChunkRewriterDoesNotTreatReadBoundaryAsLineEnding|TestStreamChunkRewriterBuffersIncompleteIDAndRetryPrefixes|TestStreamChunkRewriterLeadingWhitespacePartitionInvariant)$' -count=1 -v
```

Expected: all pass.

### Task 3: Reject incomplete raw JSON instead of dispatching invalid SSE

**Files:**
- Modify: `main_test.go` at `TestStreamChunkRewriterFlushFramesIncompleteJSONTailWhenSSE`
- Modify: `main.go` in `streamChunkRewriter.rawJSONChunks`
- Modify: `README.md` stream semantics and build version examples
- Modify: `CLAUDE.md` model-rewrite invariants

**Interfaces:**
- Consumes: `tryRawJSONChunks` incomplete result and the existing stream error-close path.
- Produces: an `incomplete raw JSON stream` error with no SSE chunk when `frameRawJSONAsSSE` is true.

- [ ] **Step 1: Replace the obsolete invalid-frame expectation with an error expectation**

Rename and rewrite the existing test:

```go
func TestStreamChunkRewriterRejectsIncompleteRawJSONAtSSEFlush(t *testing.T) {
	r := newStreamChunkRewriter("client")
	r.format = "openai-response"
	r.frameRawJSONAsSSE = true
	if chunks, err := r.Write([]byte(`{"type":"response.output_text.delta","delta":"hel`)); err != nil || len(chunks) != 0 {
		t.Fatalf("Write=(%q,%v), want buffered", chunks, err)
	}
	chunks, err := r.Flush()
	if err == nil || !strings.Contains(err.Error(), "incomplete raw JSON stream") {
		t.Fatalf("Flush=(%q,%v), want incomplete raw JSON error", chunks, err)
	}
	if len(chunks) != 0 {
		t.Fatalf("Flush chunks=%q, want no dispatchable SSE event", chunks)
	}
}
```

- [ ] **Step 2: Run the flush regression and verify red**

Run:

```bash
go test . -run '^TestStreamChunkRewriterRejectsIncompleteRawJSONAtSSEFlush$' -count=1 -v
```

Expected: it fails because `Flush` currently returns a framed invalid data event and no error.

- [ ] **Step 3: Return an error only at the framed raw-JSON boundary**

Change the incomplete branch in `rawJSONChunks` to:

```go
if incomplete {
	if r.frameRawJSONAsSSE {
		return nil, fmt.Errorf("incomplete raw JSON stream")
	}
	return [][]byte{bytes.Clone(p)}, nil
}
```

Do not change `sseRewriter.Flush`; valid pending SSE data must still be restored and emitted on read errors. Do not reject unframed Gemini/WebSocket fragments.

- [ ] **Step 4: Run flush and lifecycle regressions**

Run:

```bash
go test . -run '^(TestStreamChunkRewriterRejectsIncompleteRawJSONAtSSEFlush|TestRawJSONIncompleteContainerFlushesUnchangedOwnedChunk|TestRunStreamForwardFlushesPendingBytesOnReadError|TestHandleExecutorExecuteStreamFlushesUnterminatedSSEDataForWebSocket)$' -count=1 -v
```

Expected: all pass.

- [ ] **Step 5: Update runtime documentation and version examples**

In `README.md`, replace the read-error sentence with:

```text
Opaque response content and tool text are not recursively rewritten. Before closing the plugin stream after a read error, the plugin flushes complete pending SSE bytes; incomplete raw JSON is rejected instead of being emitted as an invalid SSE event.
```

Change the three package examples from `0.5.4` to `0.5.5`.

In `CLAUDE.md`, retain the existing stream invariants and add that framed incomplete raw JSON returns an error and no SSE event, while valid pending SSE data is still flushed on host read errors.

- [ ] **Step 6: Run the runtime worker's full focused suite**

Run:

```bash
gofmt -w main.go main_test.go
go test . -count=1
```

Expected: pass.

### Task 4: Validate local smoke output by logical SSE event

**Files:**
- Modify: `.github/scripts/smoke-local_test.go`
- Modify: `.github/scripts/smoke-local.go` in `runStreamCase`

**Interfaces:**
- Consumes: buffered HTTP body, `caseConfig`, and `openAIResponse`.
- Produces: `validateOpenAIStream(body []byte, tc caseConfig) error`, called by `runStreamCase` after the HTTP status check.

- [ ] **Step 1: Add one rejecting and one accepting HTTP-level regression**

Add tests using the existing `httptest` pattern:

```go
func TestRunStreamCaseRejectsDataLinesWithoutEventBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"model\":\"client\"}\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	if err := runStreamCase(port, caseConfig{requestModel: "client", requestAPIKey: localAPIKey, wantOriginalModel: "client"}); err == nil {
		t.Fatal("runStreamCase accepted two data fields as separate events")
	}
}

func TestRunStreamCaseAcceptsMultiDataJSONEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"model\":\ndata: \"client\"}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	if err := runStreamCase(port, caseConfig{requestModel: "client", requestAPIKey: localAPIKey, wantOriginalModel: "client"}); err != nil {
		t.Fatalf("runStreamCase error = %v", err)
	}
}
```

Retain the path check used by neighboring tests if desired; it does not change the assertion.

- [ ] **Step 2: Run both tests and verify red**

Run:

```bash
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -run '^(TestRunStreamCaseRejectsDataLinesWithoutEventBoundary|TestRunStreamCaseAcceptsMultiDataJSONEvent)$' -count=1 -v
```

Expected: the first test is incorrectly accepted and the second fails JSON decoding.

- [ ] **Step 3: Parse blank-line-delimited events and joined data fields**

Extract the post-status validation from `runStreamCase` into `validateOpenAIStream`. Normalize CRLF and CR to LF, split into lines, and do not treat the synthetic final empty slice from one trailing line ending as a blank event. Accumulate `data` and `data:` fields; remove at most one leading ASCII space after `:`. On a real blank line, join accumulated values with `\n` and apply the existing JSON, in-band error, model, forbidden-model, and `[DONE]` checks once.

Use this control shape:

```go
normalized := bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
normalized = bytes.ReplaceAll(normalized, []byte("\r"), []byte("\n"))
lines := bytes.Split(normalized, []byte("\n"))
var data []string
for i, line := range lines {
	if i == len(lines)-1 && len(line) == 0 { break }
	if len(line) == 0 {
		if err := dispatch(strings.Join(data, "\n")); err != nil { return err }
		data = nil
		continue
	}
	if bytes.Equal(line, []byte("data")) {
		data = append(data, "")
		continue
	}
	if bytes.HasPrefix(line, []byte("data:")) {
		value := line[len("data:"):]
		if len(value) > 0 && value[0] == ' ' { value = value[1:] }
		data = append(data, string(value))
	}
}
if len(data) > 0 {
	return fmt.Errorf("unterminated SSE data event")
}
```

The `dispatch` closure must ignore an empty payload, trim only for `[DONE]` detection and JSON whitespace, and preserve the existing error messages closely enough for current tests. Remove the unused `bufio` import.

- [ ] **Step 4: Run all local smoke helper tests**

Run:

```bash
gofmt -w .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
```

Expected: pass.

### Task 5: Run the local smoke helper in the host Go environment

**Files:**
- Modify: `.github/scripts/check-release-compatibility_test.go` in `TestMakeSmokeLocalIgnoresExportedTargetVariables`
- Modify: `Makefile` in `smoke-local`

**Interfaces:**
- Consumes: existing fake `go` executable and `SMOKE_LOG` build target capture.
- Produces: `SMOKE_RUN_LOG` proving the helper's target overrides are empty.

- [ ] **Step 1: Extend the fake-Go test to capture the run environment**

Add a `run:*` branch to the fake Go script:

```sh
run:*) printf 'GOOS=%s GOARCH=%s CGO_ENABLED=%s\n' "$GOOS" "$GOARCH" "$CGO_ENABLED" > "$SMOKE_RUN_LOG" ;;
```

Set `CGO_ENABLED=1` and `SMOKE_RUN_LOG=<temp path>` in `cmd.Env`. After the existing build assertion, read the run log and require:

```text
GOOS= GOARCH= CGO_ENABLED=
```

- [ ] **Step 2: Run the Makefile regression and verify red**

Run:

```bash
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -run '^TestMakeSmokeLocalIgnoresExportedTargetVariables$' -count=1 -v
```

Expected: fail because the current `go run` sees `GOOS=windows GOARCH=arm64 CGO_ENABLED=1`.

- [ ] **Step 3: Clear target variables for the helper only**

Change the final `smoke-local` recipe line to:

```make
	GOOS= GOARCH= CGO_ENABLED= $(GO) run .github/scripts/smoke-local.go
```

Do not change the recursive host `build-platform` invocation.

- [ ] **Step 4: Run release compatibility coverage**

Run:

```bash
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
```

Expected: pass.

### Task 6: Integrate, review, and commit both disjoint tracks

**Files:**
- Review: all files listed in Tasks 1 through 5
- Review: the new spec and this plan only

**Interfaces:**
- Consumes: completed runtime and smoke worker diffs.
- Produces: one reviewed repository state with no worker-owned staging changes.

- [ ] **Step 1: Check scope and formatting**

Run:

```bash
git status --short
git diff --check
git diff --stat
git diff -- main.go main_test.go README.md CLAUDE.md
git diff -- .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go .github/scripts/check-release-compatibility_test.go Makefile
```

Expected: only planned files changed; no temporary reproduction file, CPA copy, dependency, generated binary, or unrelated cleanup.

- [ ] **Step 2: Review each root cause once**

Verify directly from the diff:

- route mode validates the current bound key before accepting a wildcard cache hit;
- executor mode alone permits unbound cache reuse;
- raw JSON is framed/restored before a partial SSE prefix;
- incomplete framed raw JSON returns no bytes;
- smoke data lines dispatch only on a blank line;
- the Make target clears target variables only for the host helper.

If a correction is required, return it to the worker that owns that file set. Do not create a second overlapping reviewer or broaden scope.

- [ ] **Step 3: Commit the integrated implementation**

```bash
git add main.go main_test.go README.md CLAUDE.md .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go .github/scripts/check-release-compatibility_test.go Makefile docs/superpowers/plans/2026-09-12-cpa-model-mapper-0.5.5-functional-audit-implementation.md
git commit -m "fix stream framing and caller cache isolation"
```

Expected: commit succeeds with hooks enabled.

### Task 7: Full verification and v0.5.5 release

**Files:**
- Create after commands pass: `docs/superpowers/verification/2026-09-12-cpa-model-mapper-0.5.5.md`
- No production code changes unless verification identifies a defect in this task's diff.

**Interfaces:**
- Consumes: reviewed implementation commit.
- Produces: verified local `main`, annotated `v0.5.5`, pushed main/tag, successful GitHub Actions main and tag runs.

- [ ] **Step 1: Run complete local verification**

Run independently, preserving each exit result:

```bash
git diff --check
go test ./...
go vet ./...
go test -race ./...
go test -count=10 ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
make package VERSION=0.5.5 GOOS=windows GOARCH=amd64
```

Expected: every command passes. Live `make smoke-local` remains unavailable when `CPA_SMOKE_API_KEY`, `CPA_SMOKE_CPA_BIN`, and `CPA_SMOKE_CONFIG` are unset; record that exact environmental limitation instead of claiming a live upstream run.

- [ ] **Step 2: Write the verification record directly**

Create `docs/superpowers/verification/2026-09-12-cpa-model-mapper-0.5.5.md` with the commit under test, exact commands and outcomes, the CPA v7.2.152 glued-event reproduction conclusion, the unavailable live-smoke variables, and the planned release checks. Run `git diff --check`, then commit:

```bash
git add docs/superpowers/verification/2026-09-12-cpa-model-mapper-0.5.5.md
git commit -m "docs: record v0.5.5 verification"
```

- [ ] **Step 3: Synchronize and fast-forward local main**

Fetch `origin`, verify whether `origin/main` advanced, and integrate it without dropping this branch's commits. Return from the worktree, then run from the original checkout:

```bash
git checkout main
git merge --ff-only worktree-functional-audit-v055
```

Expected: local `main` points at the verified release commit and the only pre-existing untracked path remains `.claude/`.

- [ ] **Step 4: Create and push the patch release**

Create the same annotated tag style as v0.5.4:

```bash
git tag -a v0.5.5 -m v0.5.5
git push origin main v0.5.5
```

Expected: both refs push successfully and trigger separate `main` and `v0.5.5` workflow runs.

- [ ] **Step 5: Observe GitHub Actions and release assets**

Use `gh run list` filtered by the final commit SHA to identify the main and tag runs, then `gh run watch <id> --exit-status` for each. After the tag run succeeds, use `gh release view v0.5.5 --json url,tagName,assets` and verify seven platform archives plus `checksums.txt` are published by the existing workflow.

Expected: both runs conclude `success`; the release URL and asset names match the repository's existing packaging contract.
