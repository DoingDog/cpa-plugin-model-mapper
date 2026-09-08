# Model Mapper v0.5.1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent split raw JSON stream corruption, make compatibility rejection stop packaging, and remove two measured hot-path costs without changing the plugin contract.

**Architecture:** Extend the existing `streamChunkRewriter` result classification so syntactically incomplete raw JSON remains in its existing `pending` buffer until another host chunk or `Flush`. Keep SSE parsing and response-field restoration unchanged. Apply two local fast paths in the existing scanner and emitter, and add shell fail-fast behavior to the existing package recipe.

**Tech Stack:** Go 1.26, `encoding/json`, `io`, table-driven Go tests, Go benchmarks with `-benchmem`, GNU Make, POSIX shell, GitHub Actions, GitHub CLI.

**Spec:** `docs/superpowers/specs/2026-09-08-cpa-model-mapper-0.5.1-design.md`

## Global Constraints

- Change only plugin code, plugin tests, and plugin release tooling. Do not modify CLIProxyAPI.
- Do not add dependencies, formats, rule syntax, configuration fields, or public APIs.
- Request rewriting remains limited to the top-level `model` field.
- Response restoration remains limited to `model`, `modelVersion`, `response.model`, `response.modelVersion`, and `message.model`.
- Preserve the existing owned-chunk rule. Returned rewriter bytes must not alias host input bytes.
- Preserve LF, CRLF, bare CR, BOM, multi-`data:`, split SSE prefix, unterminated SSE, raw JSON sequence, and host read error behavior.
- Delete `Content-Length` only when existing request or response rewrite code changes body bytes. This plan does not alter that code.
- Preserve all five formats: `openai`, `openai-response`, `claude`, `gemini`, and `interactions`.
- Keep GLIBC maximum 2.17 and macOS minimum 12.0.
- Use Sonnet 1M with xhigh reasoning for implementation. Use the current Opus 1M xhigh session for plan review, diff review, verification decisions, integration, and release.
- One Sonnet worker owns `main.go`, `main_test.go`, and `performance_regression_test.go` for Tasks 1 through 3. A separate Sonnet worker owns `Makefile` and `.github/scripts/check-release-compatibility_test.go` for Task 4. They must not read or edit each other's files and must not commit.
- Do not add `.claude/`, `.test-cpa/`, `dist/`, profiles, test binaries, or temporary files to Git.

## File Map

- `main.go`: raw JSON completion classification, pending flush selection, marker scanner quote gate, and single-chunk batch emission.
- `main_test.go`: raw JSON split and flush regression coverage plus existing scanner correctness coverage.
- `performance_regression_test.go`: focused single-chunk batch benchmark and existing batching behavior test.
- `Makefile`: fail-fast compatibility-and-package logical recipe.
- `.github/scripts/check-release-compatibility_test.go`: behavioral regression test for package target failure propagation.
- `docs/superpowers/specs/2026-09-08-cpa-model-mapper-0.5.1-design.md`: committed design and scope boundary.
- `docs/superpowers/plans/2026-09-08-cpa-model-mapper-0.5.1-implementation.md`: this execution checklist.

---

### Task 1: Buffer Incomplete Raw JSON Across Host Chunks

**Files:**
- Modify: `main_test.go:3833-3883`
- Modify: `main.go:462-614`

**Interfaces:**
- Consumes: `streamChunkRewriter.Write([]byte) ([][]byte, error)`, `streamChunkRewriter.Flush() ([][]byte, error)`, `rawJSONChunks`, and `splitJSONValues`.
- Produces: `tryRawJSONChunks([]byte) (chunks [][]byte, ok bool, incomplete bool, err error)` and `splitJSONValues([]byte) (values [][]byte, ok bool, incomplete bool)`.
- Outcome meanings: `ok` means complete raw JSON was parsed; `incomplete` means more bytes can complete the sequence; both false means preserve the existing SSE or unchanged fallback path.

- [ ] **Step 1: Replace the early-emission regression test with buffering and ownership assertions**

Replace `TestRawJSONClearlyIncompleteContainerUsesCloneOnly` with tests that retain its direct allocation guard and add the new stream behavior. Use the following structure:

```go
func TestStreamChunkRewriterBuffersSplitRawJSON(t *testing.T) {
	for _, framed := range []bool{false, true} {
		t.Run(fmt.Sprintf("framed=%v", framed), func(t *testing.T) {
			first := []byte(`{"model":"up`)
			r := newStreamChunkRewriter("client")
			r.frameRawJSONAsSSE = framed

			chunks, err := r.Write(first)
			if err != nil || len(chunks) != 0 {
				t.Fatalf("first Write = (%q, %v), want no output", chunks, err)
			}
			first[0] = 'x'

			chunks, err = r.Write([]byte(`stream","id":"r1"}`))
			if err != nil || len(chunks) != 1 {
				t.Fatalf("second Write = (%q, %v), want one output", chunks, err)
			}
			got := chunks[0]
			if framed {
				if !bytes.HasPrefix(got, []byte("data: ")) || !bytes.HasSuffix(got, []byte("\n\n")) {
					t.Fatalf("framed output = %q", got)
				}
				got = bytes.TrimSuffix(bytes.TrimPrefix(got, []byte("data: ")), []byte("\n\n"))
			}
			if !json.Valid(got) || bytes.Contains(got, []byte(`"upstream"`)) || !bytes.Contains(got, []byte(`"client"`)) {
				t.Fatalf("rewritten output = %q", got)
			}
		})
	}
}
```

Add exact array reconstruction and non-JSON passthrough checks:

```go
arrayRewriter := newStreamChunkRewriter("client")
chunks, err := arrayRewriter.Write([]byte(`["partial`))
if err != nil || len(chunks) != 0 {
	t.Fatalf("array first Write = (%q, %v), want no output", chunks, err)
}
chunks, err = arrayRewriter.Write([]byte(`"]`))
if err != nil || len(chunks) != 1 || !json.Valid(chunks[0]) || string(chunks[0]) != `["partial"]` {
	t.Fatalf("array second Write = (%q, %v)", chunks, err)
}

passthrough := newStreamChunkRewriter("client")
chunks, err = passthrough.Write([]byte("not-json"))
if err != nil || len(chunks) != 1 || string(chunks[0]) != "not-json" {
	t.Fatalf("non-JSON Write = (%q, %v)", chunks, err)
}
```

Rename the large-container part to `TestRawJSONIncompleteContainerFlushesUnchangedOwnedChunk`. For each object and array input:

1. `Write` must emit nothing.
2. Mutate the caller input after `Write`.
3. `Flush` must return one chunk equal to the original bytes before mutation.
4. Keep the existing `rawJSONChunks` versus direct-clone allocation comparison so direct fallback stays within one allocation of `bytes.Clone`.
5. Keep the complete multi-value assertions unchanged.

- [ ] **Step 2: Run the focused tests and record the red state**

Run:

```bash
go test . -run 'Test(StreamChunkRewriterBuffersSplitRawJSON|RawJSONIncompleteContainerFlushesUnchangedOwnedChunk)$' -count=1
```

Expected before the production change: FAIL because the first incomplete object or array is emitted immediately instead of being buffered.

- [ ] **Step 3: Add an incomplete result to the existing JSON split functions**

Change the signatures and return paths in `main.go`:

```go
func (r *streamChunkRewriter) tryRawJSONChunks(p []byte) ([][]byte, bool, bool, error)
func splitJSONValues(p []byte) ([][]byte, bool, bool)
```

For the existing cheap container checks, return `nil, false, true, nil` when an object has no `}` or an array has no `]`.

In `splitJSONValues`, preserve whitespace and complete-value behavior, then distinguish decoder truncation:

```go
if err != nil {
	return nil, false, err == io.ErrUnexpectedEOF
}
```

Return `values, len(values) > 0, false` after complete decoding. In `tryRawJSONChunks`, propagate the third result and use `nil, false, true, nil` for an incomplete sequence. Every complete success returns `incomplete=false`; every rewrite error returns `incomplete=false`.

Update `rawJSONChunks` to ignore the incomplete flag and keep its current clone fallback:

```go
chunks, ok, _, err := r.tryRawJSONChunks(p)
```

- [ ] **Step 4: Buffer only the incomplete result in `Write`**

Replace the raw JSON branch with:

```go
if couldStartJSONValue(p) {
	chunks, ok, incomplete, err := r.tryRawJSONChunks(p)
	if ok || err != nil {
		return chunks, err
	}
	if incomplete {
		r.pending = append(r.pending, p...)
		return nil, nil
	}
}
```

Leave SSE detection and ordinary raw fallback in their current order.

- [ ] **Step 5: Route JSON-looking pending bytes through raw fallback at `Flush`**

Inside the existing `len(r.pending) > 0` branch, clone and clear `pending` as today. Before calling `r.sse.Write`, add:

```go
if couldStartJSONValue(pending) {
	chunks, err := r.rawJSONChunks(pending)
	if err != nil {
		return nil, err
	}
	flushed, err := r.sse.Flush()
	if err != nil {
		return nil, err
	}
	return append(chunks, flushed...), nil
}
```

Keep the existing SSE pending-prefix path after this branch.

- [ ] **Step 6: Run focused and neighboring stream tests**

Run:

```bash
go test . -run 'Test(StreamChunkRewriterBuffersSplitRawJSON|RawJSONIncompleteContainerFlushesUnchangedOwnedChunk|RawJSONSingleValueFastPathMatchesLegacy|RawJSONSingleValueFastPathAllocatesLessThanLegacy|SSERewriterBuffersSplitJSONUntilComplete|RunStreamForwardFlushesPendingBytesOnReadError)$' -count=1
```

Expected: PASS. The allocation test must retain its existing threshold.

- [ ] **Step 7: Format the touched Go files**

Run:

```bash
gofmt -w main.go main_test.go
```

Expected: no formatting error and no unrelated formatting diff.

### Task 2: Gate Marker Suffix Checks by the Only Possible Terminal Byte

**Files:**
- Modify: `main.go:1494-1505`
- Test: existing scanner and response restoration tests in `main_test.go`
- Benchmark: existing `BenchmarkStreamChunkRewriterCompleteSSEBatch` in `performance_regression_test.go`

**Interfaces:**
- Consumes and preserves: `responseModelMarkerScanner.feed(byte) bool`.
- Produces: identical marker decisions with fewer `bytes.HasSuffix` calls.

- [ ] **Step 1: Record a same-machine pre-change benchmark sample**

Run before changing the scanner:

```bash
go test . -run '^$' -bench '^BenchmarkStreamChunkRewriterCompleteSSEBatch$' -benchmem -count=5
```

Expected reference: approximately 706046 ns/op median, 65711 B/op, and 3 allocs/op on the audit machine. Save the five result lines outside the repository for comparison.

- [ ] **Step 2: Apply the one-condition scanner fast path**

Change only the literal-marker check:

```go
if b == '"' && (bytes.HasSuffix(tail, []byte(`"model"`)) || bytes.HasSuffix(tail, []byte(`"modelVersion"`))) {
	return true
}
```

Do not change the tail, escaped-key state, marker strings, or response whitelist.

- [ ] **Step 3: Run scanner correctness tests**

Run:

```bash
go test . -run 'Test(MightContainResponseModelFieldIgnoresOrdinaryEscapes|RestoreResponseModelFastPathPreservesEscapedSemantics|SSERewriterRestoresCompleteJSONEvent|SSERewriterBuffersSplitJSONUntilComplete)$' -count=1
```

Expected: PASS for ordinary escapes, literal keys, escaped keys, complete events, and split events.

- [ ] **Step 4: Measure the post-change scanner benchmark**

Run:

```bash
go test . -run '^$' -bench '^BenchmarkStreamChunkRewriterCompleteSSEBatch$' -benchmem -count=5
```

Keep the change only if the five-run median is at least 20% lower than the recorded pre-change median, `allocs/op` is at most 3, and `B/op` remains within one owned payload allocation. Otherwise revert this scanner-only change and document the measured result in the implementation commit message body.

### Task 3: Remove Single-Chunk Batch Copy

**Files:**
- Modify: `performance_regression_test.go:47-61`
- Modify: `main.go:1027-1050`

**Interfaces:**
- Consumes and preserves: `emitRewritten(chunks [][]byte, batch bool, emit func([]byte) error) error`.
- Produces: direct synchronous callback for one non-empty batch chunk; existing concatenation for two or more chunks.

- [ ] **Step 1: Add a focused benchmark before changing production code**

Add:

```go
func BenchmarkEmitRewrittenSingleChunkBatch(b *testing.B) {
	chunk := bytes.Repeat([]byte("x"), 64<<10)
	chunks := [][]byte{chunk}
	var emitted []byte
	emit := func(p []byte) error {
		emitted = p
		return nil
	}
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := emitRewritten(chunks, true, emit); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if len(emitted) != len(chunk) {
		b.Fatalf("emitted length=%d, want %d", len(emitted), len(chunk))
	}
}
```

- [ ] **Step 2: Record the red performance state**

Run:

```bash
go test . -run '^$' -bench '^BenchmarkEmitRewrittenSingleChunkBatch$' -benchmem -count=5
```

Expected before the fix: approximately one 64 KiB allocation and one full-size copy per operation.

- [ ] **Step 3: Add the minimal batch fast path**

After the `if !batch` branch and before the total-size loop, add:

```go
if len(chunks) == 1 {
	if len(chunks[0]) == 0 {
		return nil
	}
	return emit(chunks[0])
}
```

Do not change multi-chunk concatenation or non-batch callback boundaries.

- [ ] **Step 4: Run batching behavior tests**

Run:

```bash
go test . -run '^TestEmitRewrittenBatchesSSEChunks$' -count=1
```

Expected: PASS with one callback and ordered bytes for the existing two-chunk case.

- [ ] **Step 5: Measure the green performance state**

Run:

```bash
go test . -run '^$' -bench '^BenchmarkEmitRewrittenSingleChunkBatch$' -benchmem -count=5
```

Expected: zero per-operation payload allocation and zero per-operation copy; callback count and bytes remain unchanged.

- [ ] **Step 6: Format and run all tests owned by the stream worker**

Run:

```bash
gofmt -w main.go main_test.go performance_regression_test.go
go test . -count=1
```

Expected: PASS. The Sonnet stream worker stops here, reports the exact red and green outputs, and does not commit.

### Task 4: Propagate Compatibility Checker Failure from Make

**Files:**
- Modify: `.github/scripts/check-release-compatibility_test.go`
- Modify: `Makefile:46-61`

**Interfaces:**
- Consumes: GNU Make `package-platform`, the existing GLIBC checker CLI, and the existing release packager CLI.
- Produces: nonzero target status and no archive when the checker rejects the library.

- [ ] **Step 1: Add a behavioral Make regression test**

Add imports for `errors`, `os`, `os/exec`, and `path/filepath`. Add this test, keeping path arguments slash-normalized for the POSIX recipe:

```go
func TestPackagePlatformStopsAfterCompatibilityFailure(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not available")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := wd
	if _, err := os.Stat(filepath.Join(repoRoot, "Makefile")); err != nil {
		repoRoot = filepath.Clean(filepath.Join(wd, "..", ".."))
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "Makefile")); err != nil {
		t.Fatalf("locate repository Makefile: %v", err)
	}

	tempDir := t.TempDir()
	distDir := filepath.Join(tempDir, "dist")
	libraryDir := filepath.Join(distDir, "linux_amd64")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libraryDir, "model-mapper.so"), []byte("test library"), 0o644); err != nil {
		t.Fatal(err)
	}
	readelf := filepath.Join(tempDir, "readelf")
	if err := os.WriteFile(readelf, []byte("#!/bin/sh\nprintf '%s\\n' 'Name: GLIBC_2.28'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readelf, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(makePath,
		"--no-print-directory", "package-platform",
		"VERSION=0.5.1", "GOOS=linux", "GOARCH=amd64",
		"DIST_DIR="+filepath.ToSlash(distDir),
		"READELF="+filepath.ToSlash(readelf),
		"MAKE=true",
	)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("package-platform succeeded after compatibility rejection:\n%s", output)
	}
	archive := filepath.Join(distDir, "model-mapper_0.5.1_linux_amd64.zip")
	if _, statErr := os.Stat(archive); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("archive exists after compatibility rejection: %v", statErr)
	}
}
```

The test uses the real checker and packager. Only compilation is replaced with a successful no-op; the temporary dummy library exercises the package boundary.

- [ ] **Step 2: Run the behavioral test and record the red state**

Run from the repository root:

```bash
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -run '^TestPackagePlatformStopsAfterCompatibilityFailure$' -count=1
```

Expected before the Makefile change: FAIL with `package-platform succeeded after compatibility rejection`, and the captured output includes `GLIBC requirement 2.28 exceeds supported maximum 2.17`.

If `make` is unavailable, the worker must report the skip rather than claiming a red state; the coordinator will run the test in the configured MSYS2 environment before release.

- [ ] **Step 3: Make the logical recipe fail fast**

Change the first line of the logical shell recipe at `Makefile:50` from:

```make
	@case "$(GOOS)" in windows) ext=".dll" ;; darwin) ext=".dylib" ;; *) ext=".so" ;; esac; \
```

to:

```make
	@set -e; \
	case "$(GOOS)" in windows) ext=".dll" ;; darwin) ext=".dylib" ;; *) ext=".so" ;; esac; \
```

Do not change the compatibility thresholds, commands, pipeline ordering, archive command, or workflow shell.

- [ ] **Step 4: Run the behavioral and checker unit tests**

Run:

```bash
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
```

Expected: PASS. The new test must observe a nonzero Make result and no archive.

- [ ] **Step 5: Run release packager tests**

Run:

```bash
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
```

Expected: PASS. The Sonnet release worker stops here, reports exact red and green output, and does not commit.

### Task 5: Opus Review, Integration Verification, and Implementation Commit

**Files:**
- Review all modified files listed in the File Map.
- Do not read unrelated CPA source or old plan/spec files.

**Interfaces:**
- Consumes: uncommitted changes from the two Sonnet workers.
- Produces: one reviewed implementation commit whose tests and benchmark evidence satisfy the spec.

- [ ] **Step 1: Inspect the exact diff and working tree**

Run:

```bash
git status --short
git diff -- main.go main_test.go performance_regression_test.go Makefile .github/scripts/check-release-compatibility_test.go
git diff --check
```

Expected: only the five planned implementation files are modified; `.claude/` remains untracked and unstaged; no whitespace errors.

- [ ] **Step 2: Review each changed line against the specification**

Check all of the following directly in the diff:

- Incomplete JSON returns a distinct result and is the only raw path added to `pending`.
- Complete and invalid raw JSON retain existing outputs.
- `Flush` chooses raw handling only for JSON-looking pending bytes and preserves SSE prefix handling.
- Every returned chunk remains owned.
- The marker optimization changes only when suffix checks execute.
- Single-chunk batching keeps synchronous error propagation and leaves two-or-more concatenation unchanged.
- The Make test invokes real compatibility and package tools and verifies that no archive exists.
- `set -e` is in the same logical shell recipe as checker and packager.
- No unrelated cleanup, refactor, dependency, generated file, or formatting change is present.

Correct any review finding with the smallest possible edit, then rerun its focused test.

- [ ] **Step 3: Run focused regression tests**

Run:

```bash
go test . -run 'Test(StreamChunkRewriterBuffersSplitRawJSON|RawJSONIncompleteContainerFlushesUnchangedOwnedChunk|RawJSONSingleValueFastPathMatchesLegacy|RawJSONSingleValueFastPathAllocatesLessThanLegacy|SSERewriterBuffersSplitJSONUntilComplete|RunStreamForwardFlushesPendingBytesOnReadError|MightContainResponseModelFieldIgnoresOrdinaryEscapes|RestoreResponseModelFastPathPreservesEscapedSemantics|EmitRewrittenBatchesSSEChunks)$' -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
```

Expected: PASS.

- [ ] **Step 4: Run the complete correctness suite**

Run:

```bash
go test ./... -count=1
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
go test -race ./... -count=1
go test ./... -shuffle=on -count=5
```

Expected: every command exits 0. Do not run `go test ./.github/scripts`, because the standalone `package main` scripts collide.

- [ ] **Step 5: Run the focused and complete benchmark sets**

Run:

```bash
go test . -run '^$' -bench '^(BenchmarkStreamChunkRewriterCompleteSSEBatch|BenchmarkEmitRewrittenSingleChunkBatch)$' -benchmem -count=5
go test . -run '^$' -bench . -benchmem -count=5
```

Expected:

- Complete SSE batch median is at least 20% below its 706046 ns/op audit baseline, no more than 3 allocs/op, and still approximately one payload copy.
- Single-chunk batch emission has zero per-operation payload allocation and copy.
- Unchanged benchmark allocation counts remain stable; no median regression above 5% is accepted without a profile-supported explanation.

If normal system noise makes timing inconclusive, alternate the parent commit and feature branch benchmark five times for only the affected benchmark. Do not weaken functional tests or allocation gates.

- [ ] **Step 6: Build and package locally**

Run:

```bash
make build-windows-amd64 VERSION=0.5.1
make package VERSION=0.5.1 GOOS=windows GOARCH=amd64
```

Expected: `dist/windows_amd64/model-mapper.dll`, its platform archive, and checksum are created successfully.

If `zig` and `readelf` are available, also run:

```bash
make package VERSION=0.5.1 GOOS=linux GOARCH=amd64 BUILD_CC='zig cc -target x86_64-linux-gnu'
```

Expected: the Linux compatibility check accepts no GLIBC requirement above 2.17 and packaging succeeds. If either tool is unavailable, record the exact skipped prerequisite; CI remains the required Linux release check.

Run live smoke only when both `CPA_SMOKE_API_KEY` and `CPA_SMOKE_CPA_BIN` are set:

```bash
make smoke-local
```

Otherwise record that the credential or binary prerequisite is absent. Do not synthesize secrets or download an unrelated CPA binary.

- [ ] **Step 7: Commit only reviewed implementation files**

Run:

```bash
git add -- main.go main_test.go performance_regression_test.go Makefile .github/scripts/check-release-compatibility_test.go
git diff --cached --check
git commit -m "fix: preserve split JSON streams and release checks"
```

Expected: the commit excludes `.claude/`, `dist/`, `.test-cpa/`, profiles, and binaries.

### Task 6: Final Review and Release Readiness

**Files:**
- Review: `docs/superpowers/specs/2026-09-08-cpa-model-mapper-0.5.1-design.md`
- Review: `docs/superpowers/plans/2026-09-08-cpa-model-mapper-0.5.1-implementation.md`
- Review: committed implementation diff against `v0.5.0`.

**Interfaces:**
- Consumes: all feature-branch commits and verification output.
- Produces: an approved, clean `feature/v0.5.1` branch ready for fast-forward merge.

- [ ] **Step 1: Invoke the required completion and review skills**

Use `superpowers:verification-before-completion`, then `superpowers:requesting-code-review`, and finally `superpowers:finishing-a-development-branch`. Because multiple subagents may not reread the same implementation files, perform the code review in the current Opus session rather than dispatching another reader.

- [ ] **Step 2: Compare the complete release diff**

Run:

```bash
git diff --check v0.5.0..HEAD
git diff --stat v0.5.0..HEAD
git log --oneline --decorate v0.5.0..HEAD
git status --short --branch
```

Expected: the diff contains only the v0.5.1 spec, plan, three Go implementation/test files, Makefile, and one release-script test file. The only allowed untracked path is the pre-existing `.claude/` directory.

- [ ] **Step 3: Re-run the release-critical tests after the final diff**

Run:

```bash
go test ./... -count=1
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
```

Expected: every command exits 0 on the committed tree.

### Task 7: Merge, Tag, Push, and Verify GitHub Release v0.5.1

**Files:**
- No source edits.
- Local ignored verification downloads may use a system temporary directory, not the repository.

**Interfaces:**
- Consumes: clean reviewed `feature/v0.5.1` and GitHub remote `origin`.
- Produces: fast-forwarded local and remote `main`, annotated tag `v0.5.1`, successful tag workflow, and verified GitHub Release assets.

- [ ] **Step 1: Fast-forward local `main`**

Run:

```bash
git switch main
git merge --ff-only feature/v0.5.1
git status --short --branch
```

Expected: local `main` points at the reviewed implementation commit and differs from `origin/main` only by the planned v0.5.1 commits. `.claude/` remains untracked and unstaged.

- [ ] **Step 2: Create the annotated release tag**

First confirm it does not exist:

```bash
git tag --list v0.5.1
git ls-remote --tags origin refs/tags/v0.5.1
```

Both outputs must be empty. Then run:

```bash
git tag -a v0.5.1 -m "v0.5.1"
git show --no-patch --decorate v0.5.1
```

Expected: `v0.5.1` points at local `main`.

- [ ] **Step 3: Push main and the release tag**

Run:

```bash
git push origin main
git push origin v0.5.1
```

Expected: both pushes succeed without force. This is the authorized outward-facing publication step.

- [ ] **Step 4: Locate and wait for the tag workflow**

Use `gh run list --workflow build.yml --limit 20 --json databaseId,headBranch,headSha,event,status,conclusion,url,createdAt` and select the new run whose `headBranch` is `v0.5.1` and whose `headSha` equals `git rev-parse v0.5.1^{}`. Then run:

```bash
gh run watch <run-id> --exit-status
```

Expected: Test, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64, freebsd/amd64, and Release all succeed. Do not infer success from the workflow trigger alone.

- [ ] **Step 5: Verify release metadata and asset set**

Run:

```bash
gh release view v0.5.1 --json tagName,isDraft,isPrerelease,url,assets
```

Expected: published, not draft, not prerelease, with exactly these eight release assets:

```plaintext
model-mapper_0.5.1_linux_amd64.zip
model-mapper_0.5.1_linux_arm64.zip
model-mapper_0.5.1_darwin_amd64.zip
model-mapper_0.5.1_darwin_arm64.zip
model-mapper_0.5.1_windows_amd64.zip
model-mapper_0.5.1_windows_arm64.zip
model-mapper_0.5.1_freebsd_amd64.zip
checksums.txt
```

- [ ] **Step 6: Download and verify every published asset**

Download the release into a new system temporary directory with `gh release download v0.5.1 --dir <temporary-directory>`. In that directory:

1. Run `sha256sum -c checksums.txt`; all seven archives must report `OK`.
2. List each zip. Its root must contain exactly the platform dynamic library plus optional `LICENSE`, with no parent directory.
3. Confirm extensions: `.so` for Linux and FreeBSD, `.dylib` for macOS, `.dll` for Windows.
4. Run `go version -m` on at least one extracted library per OS family where the local Go tool can inspect it, and confirm the recorded linker flags include `-X main.pluginVersion=0.5.1`. If `go version -m` cannot inspect a foreign shared library, use a binary string inspection tool and require the literal `0.5.1`; report the exact tool limitation rather than claiming an unchecked version.

Expected: every checksum and archive-layout check passes, and the injected version is observed in the binaries.

- [ ] **Step 7: Confirm final repository state**

Run:

```bash
git status --short --branch
git rev-parse main
git rev-parse origin/main
git rev-parse 'v0.5.1^{}'
```

Expected: all three revisions match. The tracked working tree is clean; `.claude/` may remain as the pre-existing untracked directory.

- [ ] **Step 8: Remove the temporary task memory**

After every release check passes, delete `C:\Users\user\.claude\projects\C--Users-user-Downloads-cpa-plugin\memory\active-0-5-1-audit-task.md` and remove its one-line entry from `C:\Users\user\.claude\projects\C--Users-user-Downloads-cpa-plugin\memory\MEMORY.md`. Do not remove the persistent TDD preference memory.
