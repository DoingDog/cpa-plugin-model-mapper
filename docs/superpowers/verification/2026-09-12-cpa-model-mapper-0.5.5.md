# CPA Model Mapper v0.5.5 Verification

Date: 2026-09-12

## Commit under test

`ec579202533f0b50434e5131873240a0742023ef`

This commit contains the five plugin-repository fixes and the implementation plan. The design spec is `docs/superpowers/specs/2026-09-12-cpa-model-mapper-0.5.5-functional-audit-design.md`.

## TDD evidence

The focused regressions were run before their production changes and failed for the reproduced behaviors:

- Both positive and inverse wildcard caller rules reused a cache result during a later unbound `model.route` request.
- The every-byte raw JSON plus SSE partition test failed at split 1 with unframed upstream-model JSON.
- The incomplete raw JSON flush test received a complete invalid `data:` event and no error.
- The smoke helper accepted two `data:` lines without an event boundary and rejected a legal multi-data JSON event.
- The fake Go helper observed `GOOS=windows GOARCH=arm64 CGO_ENABLED=1` instead of a host execution environment.

After the minimum fixes, all focused suites passed, including executor caller-cache reuse, bounded cache coverage, valid pending SSE read-error flush, unframed incomplete JSON preservation, stream partition invariants, logical SSE event parsing, and Makefile environment capture.

## Integrated review

The coordinator reviewed the complete diff against the design and plan. Only the eight planned implementation files and the new plan changed. `git diff --check` reported no error. No Critical, Important, or Minor finding remained.

## Local verification

Each command below was run against the commit under test and exited with status 0:

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

Observed outcomes:

- `go test ./...`: `ok github.com/DoingDog/cpa-plugin-model-mapper 10.143s`
- `go vet ./...`: no diagnostics
- `go test -race ./...`: `ok github.com/DoingDog/cpa-plugin-model-mapper 14.567s`
- `go test -count=10 ./...`: `ok github.com/DoingDog/cpa-plugin-model-mapper 91.837s`
- package-release tests: `ok command-line-arguments 0.252s`
- smoke-local helper tests: `ok command-line-arguments 0.258s`
- release compatibility tests: `ok command-line-arguments 3.965s`
- Windows amd64 package build: exit status 0; created `dist/model-mapper_0.5.5_windows_amd64.zip` and `dist/model-mapper_0.5.5_windows_amd64.zip.sha256`
- final worktree state before this record: clean at the commit under test

## Reported Responses failure

The reported `claude-opus-5 -> Kimi` Responses 502 remains outside the plugin fix set. A package-level reproduction against the exact CPA v7.2.152 dependency showed that CPA's OpenAI Chat Completions -> Responses translator emits adjacent events without blank-line delimiters. CPA's own validator rejects the resulting combined payload as `invalid SSE data JSON` before `host.model.stream_read` can expose it to this plugin. No `}event:` normalization was added to the plugin.

## Live smoke limitation

A live upstream `make smoke-local` run was not executed. Fresh environment checks returned:

```plaintext
CPA_SMOKE_API_KEY=False
CPA_SMOKE_CPA_BIN=False
CPA_SMOKE_CONFIG=False
```

No live Kimi or other provider result is claimed.

## Release checks

After this record is committed, the branch will be synchronized and fast-forwarded into local `main`, tagged with annotated `v0.5.5`, and pushed. The separate GitHub Actions runs for `main` and `v0.5.5` will be watched to completion. The release must contain seven platform archives and `checksums.txt` before release verification is complete.
