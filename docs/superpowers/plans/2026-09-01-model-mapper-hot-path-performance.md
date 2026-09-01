# Model Mapper Hot-Path Performance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. This execution is additionally required to use the approved Workflow orchestration. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce allocations and CPU time in exact rule matching, SSE detection, and response/raw JSON rewriting without changing routing, authentication, JSON field whitelist, stream framing, or emitted-byte behavior.

**Architecture:** Keep the existing single-package design and public ABI unchanged. Add only private fast paths that prove an input cannot need the slower behavior, otherwise fall back to the existing parser and rewriter; preserve the existing multi-value decoder as the compatibility path. Implement four independent TDD slices, review the final diff, then release the exact merged `origin/main` SHA as `v0.4.2`.

**Tech Stack:** Go 1.26, Go standard library only, existing CLIProxyAPI v7 SDK, table-driven tests, allocation assertions, Go benchmarks, `gofmt`, `go test`, race detector, `go vet`, GNU Make, Zig CC for Linux cross-build, GitHub CLI, and GitHub Actions.

**Spec:** Conversation-approved bounded design from 2026-09-01; no separate spec file was required.

## Global Constraints

- Work only on branch `perf/hot-path-follow-up` until the PR is merged.
- Use TDD for every production edit: add the smallest deterministic failing allocation test or characterization test first, run it, implement the minimum change, then run focused and broad regressions.
- Modify only `main.go`, `main_test.go`, and this plan unless verification exposes a directly related defect.
- Add no dependencies, interfaces, regexes, general caches, `sync.Map`, `atomic.Pointer`, LRU, recursive JSON rewriters, or unrelated refactors.
- Request rewriting must continue changing only top-level `model`.
- Response restoration must remain limited to `model`, `modelVersion`, `response.model`, `response.modelVersion`, and `message.model`.
- Preserve raw opaque numbers and content through `json.RawMessage`; never return to `map[string]any`.
- Preserve positive and inverse caller-scope authentication, cache reuse, rule order, single-pass application, wildcard capture behavior, ASCII-only case operations, and empty-model errors.
- Preserve LF and CRLF SSE delimiters, split prefixes, split delimiters, unterminated flush, raw JSON framing, multiple JSON values, malformed pass-through, buffer ownership, host-error flush-before-close, and current host emit boundaries.
- A fast path may have false positives that fall back to existing decoding; it must never have a false negative that skips a whitelisted model field.
- Any body containing a backslash must fall back from the no-model screen so escaped JSON keys and values retain current semantics.
- JSON single-value trimming recognizes only JSON whitespace bytes: space, Tab, CR, and LF. Do not use Unicode whitespace semantics.
- Do not change `pluginVersion = "0.0.0-dev"`; release version `0.4.2` is injected from tag `v0.4.2`.
- Do not commit `dist/`, `.test-cpa/`, benchmark output, profiles, temporary clones, caches, or generated shared libraries.
- Do not create or push `v0.4.2` until PR CI, merge, and the exact main-branch Build run are green.

## File Structure

- Modify `main.go`: allocation-free exact matching, stdlib delimiter search, byte-only incomplete-prefix detection, conservative response screening, encoded-model reuse, and valid-single-JSON fast path.
- Modify `main_test.go`: deterministic allocation guards, behavioral characterization, escaped JSON cases, differential raw JSON coverage, and persistent benchmarks.
- Create `docs/superpowers/plans/2026-09-01-model-mapper-hot-path-performance.md`: this executable plan.

---

### Task 1: Eliminate Exact Rule Allocations

**Files:**
- Modify: `main_test.go:131-164`, `main_test.go:399-520`
- Modify: `main.go:1269-1375`

**Interfaces:**
- Consumes: `applyRules(model, scope, key string, rules []rule) (string, bool, error)`, `matchTokens(s string, tokens []token) ([]string, bool)`, and `buildReplacement(tokens []token, captures []string) string`.
- Produces: unchanged `applyRules`, `matchTokens`, and `buildReplacement` signatures; `callerPatternMatch` and `callerMatchesRule` take `*rule`; exact literal rules allocate no capture slice or replacement buffer, while wildcard rules retain existing captures.

- [ ] **Step 1: Add the failing exact-rule allocation test**

Add after `TestRouteModelReusesDecodedRules`:

```go
func TestApplyRulesExactRulesDoNotAllocate(t *testing.T) {
	var raw strings.Builder
	for i := 0; i < 24; i++ {
		if i > 0 {
			raw.WriteByte(';')
		}
		fmt.Fprintf(&raw, "model-%d=>target-%d", i, i)
	}
	rules, err := parseRules(raw.String())
	if err != nil {
		t.Fatalf("parseRules error = %v", err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		mapped, matched, err := applyRules("model-23", "", "", rules)
		if err != nil || !matched || mapped != "target-23" {
			panic(fmt.Sprintf("applyRules=(%q,%v,%v)", mapped, matched, err))
		}
	})
	if allocs != 0 {
		t.Fatalf("exact-rule allocations=%v, want 0", allocs)
	}
}
```

- [ ] **Step 2: Add the persistent exact-rule benchmark**

Add near the existing benchmark:

```go
func BenchmarkApplyRulesExact24(b *testing.B) {
	var raw strings.Builder
	for i := 0; i < 24; i++ {
		if i > 0 {
			raw.WriteByte(';')
		}
		fmt.Fprintf(&raw, "model-%d=>target-%d", i, i)
	}
	rules, err := parseRules(raw.String())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mapped, matched, err := applyRules("model-23", "", "", rules)
		if err != nil || !matched || mapped != "target-23" {
			b.Fatalf("applyRules=(%q,%v,%v)", mapped, matched, err)
		}
	}
}
```

- [ ] **Step 3: Run the focused test and observe RED**

Run:

```powershell
go test . -run '^TestApplyRulesExactRulesDoNotAllocate$' -count=1
```

Expected: FAIL with `exact-rule allocations=25, want 0`, or the current Go 1.26 equivalent non-zero value.

Record the baseline benchmark without committing its output:

```powershell
go test . -run '^$' -bench '^BenchmarkApplyRulesExact24$' -benchmem -benchtime=1s -count=7
```

Expected current baseline: approximately `400 B/op`, `25 allocs/op`.

- [ ] **Step 4: Delay capture allocation until a wildcard matches**

Change the first line of `matchTokens` from eager capacity allocation to nil:

```go
func matchTokens(s string, tokens []token) ([]string, bool) {
	var captures []string
	pos := 0
```

Leave all matching and append semantics unchanged.

- [ ] **Step 5: Reuse a single literal replacement and pre-size wildcard replacements**

Replace `buildReplacement` with:

```go
func buildReplacement(tokens []token, captures []string) string {
	if len(tokens) == 1 && tokens[0].literal != "" {
		return tokens[0].literal
	}
	size := 0
	for _, tok := range tokens {
		if tok.literal != "" {
			size += len(tok.literal)
		} else {
			size += len(captures[tok.capture-1])
		}
	}
	var b strings.Builder
	b.Grow(size)
	for _, tok := range tokens {
		if tok.literal != "" {
			b.WriteString(tok.literal)
			continue
		}
		b.WriteString(captures[tok.capture-1])
	}
	return b.String()
}
```

- [ ] **Step 6: Stop copying `rule` values in the apply loop**

Change `callerPatternMatch` and `callerMatchesRule` to accept `*rule`, then iterate by index:

```go
func callerPatternMatch(r *rule, scope, key string) (bool, bool) {
	cacheKey := callerPatternCacheKey{scope: scope, pattern: r.callerPatternText}
	callerPatternCacheMu.RLock()
	matched, ok := callerPatternCache[cacheKey]
	callerPatternCacheMu.RUnlock()
	if ok {
		return matched, true
	}
	if key == "" || callerScope(key) != scope {
		return false, false
	}
	_, matched = matchTokens(key, r.callerPattern)
	callerPatternCacheMu.Lock()
	if cached, exists := callerPatternCache[cacheKey]; exists {
		matched = cached
	} else {
		callerPatternCache[cacheKey] = matched
	}
	callerPatternCacheMu.Unlock()
	return matched, true
}

func callerMatchesRule(r *rule, scope, key string) bool {
	if r.callerScope == "" && len(r.callerPattern) == 0 {
		return true
	}
	if scope == "" {
		return false
	}
	matched := r.callerScope == scope
	if len(r.callerPattern) > 0 {
		var ok bool
		matched, ok = callerPatternMatch(r, scope, key)
		if !ok {
			return false
		}
	}
	return matched != r.excludeCaller
}

func applyRules(model, scope, key string, rules []rule) (string, bool, error) {
	current := model
	matchedAny := false
	for i := range rules {
		r := &rules[i]
		if !callerMatchesRule(r, scope, key) {
			continue
		}
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
	return current, matchedAny, nil
}
```

Do not add an exact-rule branch before `callerMatchesRule`; caller authentication must execute first.

- [ ] **Step 7: Run focused tests and observe GREEN**

```powershell
go test . -run '^(TestApplyRulesExactRulesDoNotAllocate|TestApplyRules.*|TestRouteModel.*|TestHandleModelRouteUsesCallerScope)$' -count=1
go test . -run '^$' -bench '^BenchmarkApplyRulesExact24$' -benchmem -benchtime=1s -count=7
```

Expected: tests PASS; benchmark reports `0 B/op`, `0 allocs/op`; wildcard, inverse scope, unchanged-but-matched, and case-operation tests remain green.

- [ ] **Step 8: Format, review, and commit Task 1**

```powershell
gofmt -w main.go main_test.go
git diff --check
git diff -- main.go main_test.go
git add main.go main_test.go
git commit -m "perf: eliminate exact rule allocations"
```

Expected: one commit containing only Task 1 tests, benchmark, and rule hot-path changes.

---

### Task 2: Restore Stdlib SSE Search and Remove Prefix Allocation

**Files:**
- Modify: `main_test.go:919-1125`
- Modify: `main.go:50-172`, `main.go:264-283`

**Interfaces:**
- Consumes: `sseEventDelimiter(buf []byte, start int) (eventLen, delimLen, next int)` and `isIncompleteSSEPrefix(p []byte) bool`.
- Produces: identical signatures and byte behavior; delimiter search begins at `start` using `bytes.Index`, and incomplete-prefix checks allocate zero bytes.

- [ ] **Step 1: Add the failing incomplete-prefix allocation test**

Add after the existing split-prefix tests:

```go
func TestIncompleteSSEPrefixLargeRawJSONDoesNotAllocate(t *testing.T) {
	payload := []byte(`{"type":"response.delta","text":"` + strings.Repeat("x", 64<<10) + `"}`)
	allocs := testing.AllocsPerRun(100, func() {
		if isIncompleteSSEPrefix(payload) {
			panic("raw JSON classified as incomplete SSE prefix")
		}
	})
	if allocs != 0 {
		t.Fatalf("isIncompleteSSEPrefix allocations=%v, want 0", allocs)
	}
}
```

- [ ] **Step 2: Add direct delimiter characterization tests**

```go
func TestSSEEventDelimiterFromScanOffset(t *testing.T) {
	tests := []struct {
		name      string
		buf       string
		start     int
		eventLen  int
		delimLen  int
		next      int
	}{
		{name: "LF", buf: "data: one\n\ntail", start: 0, eventLen: 9, delimLen: 2},
		{name: "CRLF", buf: "data: one\r\n\r\ntail", start: 0, eventLen: 9, delimLen: 4},
		{name: "earliest mixed", buf: "a\n\nb\r\n\r\n", start: 0, eventLen: 1, delimLen: 2},
		{name: "scan offset", buf: "ignored\n\ndata: two\n\n", start: 9, eventLen: 18, delimLen: 2},
		{name: "split CRLF tail", buf: "data: one\r\n\r", start: 8, next: 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventLen, delimLen, next := sseEventDelimiter([]byte(tt.buf), tt.start)
			if eventLen != tt.eventLen || delimLen != tt.delimLen || next != tt.next {
				t.Fatalf("delimiter=(%d,%d,%d), want (%d,%d,%d)", eventLen, delimLen, next, tt.eventLen, tt.delimLen, tt.next)
			}
		})
	}
}
```

The `scan offset` delimiter starts at byte 18: the ignored event occupies bytes 0 through 8, and `data: two` occupies bytes 9 through 17.

- [ ] **Step 3: Add complete and split large-event benchmarks**

```go
func BenchmarkSSERewriterCompleteLargeEvent(b *testing.B) {
	payload := []byte("event: " + strings.Repeat("x", 64<<10) + "\n\n")
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		r := newSSERewriter("client")
		chunks, err := r.Write(payload)
		if err != nil || len(chunks) != 2 {
			b.Fatalf("Write=(%d,%v)", len(chunks), err)
		}
	}
}
```

Keep `BenchmarkSSERewriterSplitLargeEvent` as the split benchmark.

- [ ] **Step 4: Run RED and record delimiter baseline**

```powershell
go test . -run '^(TestIncompleteSSEPrefixLargeRawJSONDoesNotAllocate|TestSSEEventDelimiterFromScanOffset)$' -count=1
go test . -run '^$' -bench 'BenchmarkSSERewriter(Complete|Split)LargeEvent' -benchmem -benchtime=1s -count=7
```

Expected: allocation test FAILS with one allocation; delimiter characterization passes; current complete-event benchmark is slower than the stdlib candidate measured in the approved design.

- [ ] **Step 5: Replace the manual delimiter loop with offset-aware `bytes.Index`**

```go
func sseEventDelimiter(buf []byte, start int) (eventLen, delimLen, next int) {
	start = max(0, min(start, len(buf)))
	lf := bytes.Index(buf[start:], []byte("\n\n"))
	if lf >= 0 {
		lf += start
	}
	crlf := bytes.Index(buf[start:], []byte("\r\n\r\n"))
	if crlf >= 0 {
		crlf += start
	}
	if lf >= 0 && (crlf < 0 || lf < crlf) {
		return lf, 2, 0
	}
	if crlf >= 0 {
		return crlf, 4, 0
	}
	return 0, 0, max(0, len(buf)-3)
}
```

- [ ] **Step 6: Remove only the redundant string conversion**

Change:

```go
if len(trimmed) == 0 || strings.ContainsAny(string(trimmed), "\r\n") {
	return false
}
```

To:

```go
if len(trimmed) == 0 {
	return false
}
```

The subsequent `bytes.HasPrefix(field, trimmed)` checks already reject CR/LF and overlong inputs. Do not alter `isSSEChunk` in this task.

- [ ] **Step 7: Run focused and full stream regressions**

```powershell
go test . -run '^(TestIncompleteSSEPrefixLargeRawJSONDoesNotAllocate|TestSSEEventDelimiterFromScanOffset|TestSSERewriter.*|TestStreamChunkRewriter.*|TestHandleExecutorExecuteStream.*)$' -count=1
go test . -run '^$' -bench 'BenchmarkSSERewriter(Complete|Split)LargeEvent' -benchmem -benchtime=1s -count=7
```

Expected: all tests PASS, allocation guard reports zero, complete and split event medians improve, allocations do not increase.

- [ ] **Step 8: Format, review, and commit Task 2**

```powershell
gofmt -w main.go main_test.go
git diff --check
git add main.go main_test.go
git commit -m "perf: speed SSE delimiter detection"
```

---

### Task 3: Skip Response Decoding Only When No Model Field Is Possible

**Files:**
- Modify: `main_test.go:766-1023`
- Modify: `main.go:96-133`, `main.go:882-952`

**Interfaces:**
- Consumes: `rewriteResponseModelFields(body []byte, model string) ([]byte, bool, error)` and `rewriteRawStringField`.
- Produces: private `mightContainResponseModelField(body []byte) bool`; the existing response function retains its signature and clone-on-unchanged ownership.

- [ ] **Step 1: Add failing no-model allocation coverage**

```go
func TestRestoreResponseWithoutModelUsesCloneOnly(t *testing.T) {
	body := []byte(`{"type":"response.output_text.delta","delta":"hello"}`)
	allocs := testing.AllocsPerRun(100, func() {
		got, changed, err := restoreResponseModel(body, "client")
		if err != nil || changed || !bytes.Equal(got, body) {
			panic(fmt.Sprintf("restore=(%s,%v,%v)", got, changed, err))
		}
	})
	if allocs > 1 {
		t.Fatalf("no-model allocations=%v, want <=1 clone", allocs)
	}
}
```

- [ ] **Step 2: Add escaped-key and escaped-value behavior tests**

```go
func TestRestoreResponseModelFastPathPreservesEscapedSemantics(t *testing.T) {
	backslash := string(rune(92))
	tests := []struct {
		name    string
		body    []byte
		changed bool
		want    string
	}{
		{name: "escaped model key", body: []byte(`{"` + backslash + `u006dodel":"upstream"}`), changed: true, want: `"model":"client"`},
		{name: "escaped nested response and model keys", body: []byte(`{"res` + backslash + `u0070onse":{"` + backslash + `u006dodel":"upstream"}}`), changed: true, want: `"model":"client"`},
		{name: "escaped equal value remains unchanged", body: []byte(`{"model":"cli` + backslash + `u0065nt"}`), changed: false},
		{name: "ordinary escaped text remains unchanged", body: []byte(`{"text":"line` + backslash + `nnext"}`), changed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := restoreResponseModel(tt.body, "client")
			if err != nil || changed != tt.changed {
				t.Fatalf("restore=(%s,%v,%v)", got, changed, err)
			}
			if tt.want != "" && !strings.Contains(string(got), tt.want) {
				t.Fatalf("body=%s missing %s", got, tt.want)
			}
			if !tt.changed && !bytes.Equal(got, tt.body) {
				t.Fatalf("unchanged body=%s, want %s", got, tt.body)
			}
		})
	}
}
```

- [ ] **Step 3: Run RED**

```powershell
go test . -run '^(TestRestoreResponseWithoutModelUsesCloneOnly|TestRestoreResponseModelFastPathPreservesEscapedSemantics)$' -count=1
```

Expected: behavior rows pass; allocation test FAILS because the current implementation allocates a map and RawMessages before cloning.

- [ ] **Step 4: Add the conservative model-field screen**

```go
func mightContainResponseModelField(body []byte) bool {
	if bytes.IndexByte(body, '\\') >= 0 {
		return true
	}
	return bytes.Contains(body, []byte(`"model"`)) ||
		bytes.Contains(body, []byte(`"modelVersion"`))
}
```

False positives are safe. Any backslash forces fallback so escaped whitelist keys cannot be missed.

At the start of `rewriteResponseModelFields`:

```go
if !mightContainResponseModelField(body) {
	return bytes.Clone(body), false, nil
}
```

In `sseRewriter.rewriteEvent`, after empty and `[DONE]` checks but before response decoding, treat `!mightContainResponseModelField(value)` as unchanged and append the existing line copy. Do not return the borrowed `value` from the public response helper.

- [ ] **Step 5: Add unescaped model-string byte comparison with semantic fallback**

Inside `rewriteRawStringField`, after validating the leading quote:

```go
if bytes.IndexByte(trimmed, '\\') < 0 {
	if len(trimmed) == len(model)+2 && bytes.Equal(trimmed[1:len(trimmed)-1], []byte(model)) {
		return false
	}
	doc[key] = replacement
	return true
}
```

Keep the existing `json.Unmarshal(trimmed, &current)` path immediately afterward for escaped strings.

- [ ] **Step 6: Run focused GREEN and allocation benchmark**

```powershell
go test . -run '^(TestRestoreResponseWithoutModelUsesCloneOnly|TestRestoreResponseModelFastPathPreservesEscapedSemantics|TestModelRewriteTreatsOpaqueContentAsRawJSON|TestRestoreResponseModel.*|TestSSERewriter.*)$' -count=1
```

Expected: all tests PASS; no-model path allocates at most one clone; escaped keys and values preserve current semantics.

Add and run:

```go
func BenchmarkRestoreResponseWithoutModel(b *testing.B) {
	body := []byte(`{"type":"response.output_text.delta","delta":"hello"}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := restoreResponseModel(body, "client"); err != nil {
			b.Fatal(err)
		}
	}
}
```

```powershell
go test . -run '^$' -bench '^BenchmarkRestoreResponseWithoutModel$' -benchmem -benchtime=1s -count=7
```

- [ ] **Step 7: Format, review, and commit Task 3**

```powershell
gofmt -w main.go main_test.go
git diff --check
git add main.go main_test.go
git commit -m "perf: skip unnecessary response decoding"
```

---

### Task 4: Avoid Duplicate Single-JSON Decoding and Reuse Encoded Stream Model

**Files:**
- Modify: `main_test.go:1907-1971`, `main_test.go:2005-2085`
- Modify: `main.go:26-48`, `main.go:174-251`, `main.go:507-509`, `main.go:882-923`

**Interfaces:**
- Consumes: `streamChunkRewriter.rawJSONChunks`, `sseRewriter`, `splitJSONValues`, and response field rewriting from Task 3.
- Produces: private `sseRewriter.restoreResponseModel(body []byte)`, private `rewriteResponseModelFieldsWithReplacement`, and a fast path for exactly one valid JSON value after JSON-whitespace trimming.

- [ ] **Step 1: Add a legacy reference helper in tests**

Add this test-only helper near raw JSON stream tests:

```go
func legacyRawJSONChunksForTest(r *streamChunkRewriter, p []byte) ([][]byte, error) {
	values, ok := splitJSONValues(p)
	if !ok {
		return [][]byte{bytes.Clone(p)}, nil
	}
	if len(values) == 0 {
		return nil, nil
	}
	out := make([][]byte, 0, len(values))
	for _, value := range values {
		restored, _, err := restoreResponseModel(value, r.originalModel)
		if err != nil {
			return nil, err
		}
		if r.frameRawJSONAsSSE {
			out = append(out, frameSSEData(restored))
		} else {
			out = append(out, restored)
		}
	}
	return out, nil
}
```

The current production branch containing `if !ok && frameRawJSONAsSSE && json.Valid(p)` is unreachable for valid JSON; do not reproduce that check.

- [ ] **Step 2: Add differential corpus coverage**

```go
func TestRawJSONSingleValueFastPathMatchesLegacy(t *testing.T) {
	backslash := string(rune(92))
	inputs := [][]byte{
		nil,
		[]byte(" \r\n\t "),
		[]byte("not-json"),
		[]byte(`{}`),
		[]byte("  {\"id\":\"r1\"} \r\n"),
		[]byte(`{"model":"upstream"}`),
		[]byte(`{"response":{"model":"upstream"}}`),
		[]byte(`{"` + backslash + `u006dodel":"upstream"}`),
		[]byte("{\"model\":\"upstream\"}\n{\"model\":\"other\"}"),
		[]byte("null true 123 \"text\""),
		[]byte(`{"valid":true} trailing`),
		[]byte{0xc2, 0xa0},
	}
	for _, framed := range []bool{false, true} {
		for _, input := range inputs {
			r := newStreamChunkRewriter("client")
			r.frameRawJSONAsSSE = framed
			got, gotErr := r.rawJSONChunks(input)
			want, wantErr := legacyRawJSONChunksForTest(r, input)
			if !reflect.DeepEqual(got, want) || (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("framed=%v input=%q\ngot=%q err=%v\nwant=%q err=%v", framed, input, got, gotErr, want, wantErr)
			}
		}
	}
}
```

Add `reflect` only if not already imported; it is already used by existing tests.

- [ ] **Step 3: Add a failing relative-allocation test**

```go
func TestRawJSONSingleValueFastPathAllocatesLessThanLegacy(t *testing.T) {
	body := []byte(`{"type":"response.completed","model":"upstream","output":"` + strings.Repeat("x", 64<<10) + `"}`)
	production := newStreamChunkRewriter("client")
	reference := newStreamChunkRewriter("client")
	productionAllocs := testing.AllocsPerRun(50, func() {
		chunks, err := production.rawJSONChunks(body)
		if err != nil || len(chunks) != 1 {
			panic(fmt.Sprintf("production=(%d,%v)", len(chunks), err))
		}
	})
	referenceAllocs := testing.AllocsPerRun(50, func() {
		chunks, err := legacyRawJSONChunksForTest(reference, body)
		if err != nil || len(chunks) != 1 {
			panic(fmt.Sprintf("reference=(%d,%v)", len(chunks), err))
		}
	})
	if productionAllocs >= referenceAllocs {
		t.Fatalf("production allocations=%v, legacy allocations=%v", productionAllocs, referenceAllocs)
	}
}
```

- [ ] **Step 4: Run RED**

```powershell
go test . -run '^(TestRawJSONSingleValueFastPathMatchesLegacy|TestRawJSONSingleValueFastPathAllocatesLessThanLegacy)$' -count=1
```

Expected: differential corpus passes; allocation test FAILS because production is still the legacy path.

- [ ] **Step 5: Split response rewriting so a pre-encoded model can be reused**

Keep the public helper and move the existing map logic into a private helper:

```go
func rewriteResponseModelFields(body []byte, model string) ([]byte, bool, error) {
	if !mightContainResponseModelField(body) {
		return bytes.Clone(body), false, nil
	}
	replacement, err := json.Marshal(model)
	if err != nil {
		return nil, false, err
	}
	return rewriteResponseModelFieldsWithReplacement(body, model, replacement)
}

func rewriteResponseModelFieldsWithReplacement(body []byte, model string, replacement json.RawMessage) ([]byte, bool, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return bytes.Clone(body), false, nil
	}
	changed := rewriteRawStringField(doc, "model", model, replacement)
	changed = rewriteRawStringField(doc, "modelVersion", model, replacement) || changed
	messageChanged, err := rewriteNestedRawStringFields(doc, "message", model, replacement, "model")
	if err != nil {
		return nil, false, err
	}
	changed = messageChanged || changed
	responseChanged, err := rewriteNestedRawStringFields(doc, "response", model, replacement, "model", "modelVersion")
	if err != nil {
		return nil, false, err
	}
	changed = responseChanged || changed
	if !changed {
		return bytes.Clone(body), false, nil
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}
```

- [ ] **Step 6: Cache the encoded model lazily on the existing SSE rewriter**

Extend the existing struct without adding a new abstraction:

```go
type sseRewriter struct {
	originalModel string
	encodedModel  json.RawMessage
	buf           []byte
	scanFrom      int
}

func (r *sseRewriter) restoreResponseModel(body []byte) ([]byte, bool, error) {
	if !mightContainResponseModelField(body) {
		return bytes.Clone(body), false, nil
	}
	if r.encodedModel == nil {
		var err error
		r.encodedModel, err = json.Marshal(r.originalModel)
		if err != nil {
			return nil, false, err
		}
	}
	return rewriteResponseModelFieldsWithReplacement(body, r.originalModel, r.encodedModel)
}
```

Use this method from `sseRewriter.rewriteEvent` and from `streamChunkRewriter.rawJSONChunks` through `r.sse.restoreResponseModel`. The `streamChunkRewriter` already owns one `sseRewriter`, so SSE and raw paths share the same read-only replacement. Never append into or mutate `encodedModel`.

- [ ] **Step 7: Add the exactly-one-valid-JSON fast path**

At the start of `rawJSONChunks`:

```go
trimmed := bytes.Trim(p, " \t\r\n")
if len(trimmed) > 0 && json.Valid(trimmed) {
	restored, _, err := r.sse.restoreResponseModel(trimmed)
	if err != nil {
		return nil, err
	}
	if r.frameRawJSONAsSSE {
		return [][]byte{frameSSEData(restored)}, nil
	}
	return [][]byte{restored}, nil
}
```

Then retain the existing `splitJSONValues` path for whitespace-only input, multiple values, and invalid input. In the `!ok` branch, return one cloned chunk directly; remove the unreachable nested `json.Valid` branch.

`json.Valid(trimmed)` is the proof that there is exactly one value and no invalid suffix. `bytes.Trim` must use exactly `" \t\r\n"`, not `bytes.TrimSpace`.

- [ ] **Step 8: Add and run the raw JSON benchmark**

```go
func BenchmarkStreamChunkRewriterSingleJSON(b *testing.B) {
	body := []byte(`{"type":"response.completed","model":"upstream","output":"` + strings.Repeat("x", 64<<10) + `"}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for i := 0; i < b.N; i++ {
		r := newStreamChunkRewriter("client")
		chunks, err := r.Write(body)
		if err != nil || len(chunks) != 1 {
			b.Fatalf("Write=(%d,%v)", len(chunks), err)
		}
	}
}
```

Run:

```powershell
go test . -run '^(TestRawJSONSingleValueFastPathMatchesLegacy|TestRawJSONSingleValueFastPathAllocatesLessThanLegacy|TestStreamChunkRewriter.*|TestSplitJSONValues.*|TestHandleExecutorExecuteStream.*)$' -count=1
go test . -run '^$' -bench '^BenchmarkStreamChunkRewriterSingleJSON$' -benchmem -benchtime=1s -count=7
```

Expected: differential corpus and stream regressions PASS; production allocations are lower than legacy; benchmark improves without changing output.

- [ ] **Step 9: Format, review, and commit Task 4**

```powershell
gofmt -w main.go main_test.go
git diff --check
git add main.go main_test.go
git commit -m "perf: avoid duplicate stream JSON decoding"
```

---

### Task 5: Full Verification and Independent Review

**Files:**
- Verify: `main.go`, `main_test.go`, `abi_cgo.go`, `Makefile`, `.github/scripts/package-release.go`, `.github/workflows/build.yml`
- Do not modify unrelated files.

**Interfaces:**
- Consumes: the four green TDD commits.
- Produces: a reviewed, locally verified branch suitable for PR; no release tag yet.

- [ ] **Step 1: Run formatting and static diff checks**

```powershell
gofmt -w main.go main_test.go
git diff --check
git status --short
git diff origin/main...HEAD --stat
git diff origin/main...HEAD -- main.go main_test.go
```

Expected: only the plan plus scoped Go files differ; no generated artifacts are tracked.

- [ ] **Step 2: Run all unit, packager, race, and vet checks**

```powershell
go test ./... -count=1
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test -race . -count=1
go vet ./...
```

Expected: every command exits zero. Do not run `go test ./.github/scripts`.

- [ ] **Step 3: Run all performance benchmarks repeatedly**

```powershell
go test . -run '^$' -bench 'Benchmark(ApplyRulesExact24|SSERewriterCompleteLargeEvent|SSERewriterSplitLargeEvent|RestoreResponseWithoutModel|StreamChunkRewriterSingleJSON)' -benchmem -benchtime=1s -count=10
```

Expected stable allocation targets:

- `BenchmarkApplyRulesExact24`: `0 B/op`, `0 allocs/op`.
- incomplete-prefix allocation unit test: zero.
- no-model response benchmark: at most one clone allocation.
- single raw JSON: fewer allocations and bytes than the recorded Task 4 baseline.
- no benchmark has a repeated median regression greater than 10% without an explained trade-off and rerun.

- [ ] **Step 4: Build representative native targets**

```powershell
make build-windows-amd64
make build-linux-amd64 LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"
```

Expected: both shared libraries build successfully. Remove or leave generated `dist/` ignored; never stage it.

- [ ] **Step 5: Run an independent Workflow review**

The workflow must fan out read-only reviewers for:

1. rule/authentication semantics;
2. SSE/raw JSON byte behavior and ownership;
3. performance evidence and benchmark validity;
4. release scope and repository conventions.

Every finding must be independently verified before applying. Reject speculative refactors, SDK-tag claims that conflict with actual SDK source, and suggestions to reuse unexported upstream rewriters.

- [ ] **Step 6: Apply confirmed review fixes through TDD and repeat all gates**

For each confirmed defect, add or identify a failing test, make the smallest correction, rerun the focused test, then repeat Steps 1 through 4. If no finding survives verification, make no review-only code changes.

- [ ] **Step 7: Commit any review-driven test or fix**

```powershell
git add main.go main_test.go
git commit -m "test: harden performance fast path regressions"
```

Skip this commit when the review produces no code changes.

---

### Task 6: Commit the Plan, Push PR, Merge Main, and Release `v0.4.2`

**Files:**
- Commit: `docs/superpowers/plans/2026-09-01-model-mapper-hot-path-performance.md`
- Do not edit production version files; version comes from the tag.

**Interfaces:**
- Consumes: a clean, fully verified `perf/hot-path-follow-up` branch and GitHub authentication.
- Produces: merged `main`, annotated tag `v0.4.2`, seven platform zip assets, and `checksums.txt` in GitHub Release.

- [ ] **Step 1: Commit this implementation plan before production work if it is still uncommitted**

```powershell
git add docs/superpowers/plans/2026-09-01-model-mapper-hot-path-performance.md
git commit -m "docs: plan hot path performance follow-up"
```

Workflow execution should perform this step before Task 1 if the plan is not yet committed.

- [ ] **Step 2: Confirm branch history and push the feature branch**

```powershell
git status --short
git log --oneline --decorate origin/main..HEAD
git push -u origin perf/hot-path-follow-up
```

Expected: worktree clean; push succeeds; no tag is pushed.

- [ ] **Step 3: Create the PR with exact scope and verification evidence**

Create a temporary body with the final Task 5 benchmark medians substituted for the prototype figures when they differ materially:

```powershell
$prBody = Join-Path $env:TEMP 'model-mapper-hot-path-pr.md'
@'
## Summary
- eliminate allocations for exact literal rules without bypassing caller authentication
- use `bytes.Index` for SSE delimiters and remove the incomplete-prefix string conversion
- skip response decoding only when no whitelisted model field can be present
- avoid duplicate decoding for one valid raw JSON value and reuse the encoded stream model

## Behavior preserved
- request rewriting remains top-level `model` only
- response restoration remains limited to `model`, `modelVersion`, `response.model`, `response.modelVersion`, and `message.model`
- escaped keys and values, raw opaque JSON, caller scopes, wildcard captures, SSE framing, split input, flush behavior, and host emit boundaries retain existing behavior

## Performance evidence
- 24 exact rules: about 691 ns/op, 400 B/op, 25 allocs/op -> about 181 ns/op, 0 B/op, 0 allocs/op
- complete 64 KiB SSE event: about 64.7 us/op -> about 19.6 us/op
- response without model: about 1.44 us/op, 704 B/op, 15 allocs/op -> about 68 ns/op, 32 B/op, 1 alloc/op
- single 64 KiB raw JSON response: about 1.305 ms/op, 570 KiB/op, 48 allocs/op -> about 0.638 ms/op, 150 KiB/op, 30 allocs/op

## Verification
- `go test ./... -count=1`
- `go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1`
- `go test -race . -count=1`
- `go vet ./...`
- repeated focused benchmarks with `-benchmem -benchtime=1s -count=10`
- `make build-windows-amd64`
- `make build-linux-amd64 LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"`
'@ | Set-Content -LiteralPath $prBody -Encoding utf8
gh pr create --base main --head perf/hot-path-follow-up --title "perf: optimize model mapping hot paths" --body-file $prBody
Remove-Item -LiteralPath $prBody -Force -Confirm:$false
```

Use the actual final medians in the PR body when they differ materially from the approved prototype measurements above. Remove the temporary PR body after creation.

- [ ] **Step 4: Wait for PR Test and inspect failures**

```powershell
gh pr checks --watch --fail-fast
```

Expected: `Test` succeeds. If it fails, inspect logs, reproduce locally, fix through TDD, push, and wait again. Never bypass checks.

- [ ] **Step 5: Merge with a merge commit**

```powershell
gh pr merge --merge --delete-branch
```

Then synchronize local main without rewriting history:

```powershell
git switch main
git pull --ff-only origin main
```

Expected: local `main` equals `origin/main` and contains the PR merge commit.

- [ ] **Step 6: Wait for the exact main Build run**

Capture the merge SHA:

```powershell
$mergeSha = git rev-parse HEAD
gh run list --workflow build.yml --branch main --commit $mergeSha --limit 1
```

Wait for that run. Recompute the SHA in the same shell call and allow GitHub time to register the push event:

```powershell
$mergeSha = git rev-parse HEAD
$runId = $null
for ($attempt = 0; $attempt -lt 24 -and -not $runId; $attempt++) {
    $runId = gh run list --workflow build.yml --branch main --commit $mergeSha --event push --limit 1 --json databaseId --jq '.[0].databaseId // empty'
    if (-not $runId) {
        Start-Sleep -Seconds 5
    }
}
if (-not $runId) {
    throw "main Build run was not registered for $mergeSha"
}
gh run watch $runId --exit-status
if ($LASTEXITCODE -ne 0) {
    throw "main Build run $runId failed"
}
```

Expected: test and all seven build targets succeed; Release is skipped for the main push.

- [ ] **Step 7: Create and push annotated `v0.4.2` on the exact green main SHA**

Verify preconditions:

```powershell
git fetch origin --tags
git status --short
git rev-parse HEAD
git rev-parse origin/main
git tag -l v0.4.2
```

Expected: clean worktree, `HEAD == origin/main == $mergeSha`, and no existing `v0.4.2`.

Create and push only the tag ref:

```powershell
git tag -a v0.4.2 -m "Release v0.4.2"
git push origin refs/tags/v0.4.2
```

- [ ] **Step 8: Wait for tag Build and Release**

```powershell
$tagSha = git rev-list -n 1 v0.4.2
$releaseRunId = $null
for ($attempt = 0; $attempt -lt 24 -and -not $releaseRunId; $attempt++) {
    $releaseRunId = gh run list --workflow build.yml --commit $tagSha --event push --limit 10 --json databaseId,headBranch --jq '.[] | select(.headBranch == "v0.4.2") | .databaseId' | Select-Object -First 1
    if (-not $releaseRunId) {
        Start-Sleep -Seconds 5
    }
}
if (-not $releaseRunId) {
    throw "tag Build run was not registered for v0.4.2"
}
gh run watch $releaseRunId --exit-status
if ($LASTEXITCODE -ne 0) {
    throw "tag Build run $releaseRunId failed"
}
```

Expected: Test, five matrix builds, windows/arm64, freebsd/amd64, and Release all succeed.

- [ ] **Step 9: Verify tag and release assets**

```powershell
$tagSha = git rev-parse 'v0.4.2^{}'
$mainSha = git rev-parse origin/main
if ($tagSha -ne $mainSha) {
    throw "peeled tag $tagSha does not match origin/main $mainSha"
}
$release = gh release view v0.4.2 --json tagName,targetCommitish,assets,url | ConvertFrom-Json
$expectedAssets = @(
    'model-mapper_0.4.2_linux_amd64.zip'
    'model-mapper_0.4.2_linux_arm64.zip'
    'model-mapper_0.4.2_darwin_amd64.zip'
    'model-mapper_0.4.2_darwin_arm64.zip'
    'model-mapper_0.4.2_windows_amd64.zip'
    'model-mapper_0.4.2_windows_arm64.zip'
    'model-mapper_0.4.2_freebsd_amd64.zip'
    'checksums.txt'
) | Sort-Object
$actualAssets = @($release.assets.name) | Sort-Object
if (@(Compare-Object $expectedAssets $actualAssets).Count -ne 0) {
    throw "release assets differ: $($actualAssets -join ', ')"
}
$release.url
```

Download the assets to a unique temporary directory, verify all seven checksum lines, and confirm every zip root contains only its platform library plus optional `LICENSE`:

```powershell
$releaseDir = Join-Path $env:TEMP ("model-mapper-v0.4.2-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $releaseDir | Out-Null
try {
    gh release download v0.4.2 --dir $releaseDir
    if ($LASTEXITCODE -ne 0) {
        throw 'release asset download failed'
    }
    $checksumPath = Join-Path $releaseDir 'checksums.txt'
    $checksumLines = @(Get-Content -LiteralPath $checksumPath | Where-Object { $_ -ne '' })
    if ($checksumLines.Count -ne 7) {
        throw "checksum line count=$($checksumLines.Count), want 7"
    }
    foreach ($line in $checksumLines) {
        $match = [regex]::Match($line, '^([0-9a-fA-F]{64})  ([^\\/]+\.zip)$')
        if (-not $match.Success) {
            throw "invalid checksum line: $line"
        }
        $archivePath = Join-Path $releaseDir $match.Groups[2].Value
        $actualHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -ne $match.Groups[1].Value.ToLowerInvariant()) {
            throw "checksum mismatch: $($match.Groups[2].Value)"
        }
    }

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    foreach ($archive in Get-ChildItem -LiteralPath $releaseDir -Filter '*.zip') {
        $expectedLibrary = if ($archive.Name -match '_windows_') {
            'model-mapper.dll'
        } elseif ($archive.Name -match '_darwin_') {
            'model-mapper.dylib'
        } else {
            'model-mapper.so'
        }
        $zip = [System.IO.Compression.ZipFile]::OpenRead($archive.FullName)
        try {
            $entries = @($zip.Entries | ForEach-Object { $_.FullName })
        } finally {
            $zip.Dispose()
        }
        if ($entries -notcontains $expectedLibrary) {
            throw "$($archive.Name) is missing $expectedLibrary"
        }
        $unexpected = @($entries | Where-Object { $_ -ne $expectedLibrary -and $_ -ne 'LICENSE' })
        if ($unexpected.Count -ne 0) {
            throw "$($archive.Name) has unexpected root entries: $($unexpected -join ', ')"
        }
    }
} finally {
    Remove-Item -LiteralPath $releaseDir -Recurse -Force -Confirm:$false
}
```

- [ ] **Step 10: Final repository state**

```powershell
git status --short
git branch --show-current
git log -3 --oneline --decorate
```

Expected: clean worktree on `main`; `HEAD`, `origin/main`, and peeled `v0.4.2` point to the same merge SHA.
