# CPA model-mapper v0.5.4 functional audit fixes

Date: 2026-09-11
Repository: `github.com/DoingDog/cpa-plugin-model-mapper`
Target release: `v0.5.4`

## Purpose

Fix every repository-local defect confirmed by the 2026-09-11 functional audit. The implementation must preserve CPA's request and response contracts, keep streaming output partition-invariant, support the five advertised formats, and make release tooling fail closed when it cannot prove artifact correctness.

This work changes only the plugin repository. It does not modify CPA.

## Confirmed defects

### Registration compatibility

The plugin currently copies `pluginabi.SchemaVersion` from the build dependency into its registration response. The current dependency reports schema 5, although the plugin implements only schema-1 model router and executor capabilities. CPA versions whose host schema is 1 through 4 reject this binary before activating any capability.

The plugin will declare RPC schema 1 explicitly. It will continue accepting lifecycle requests with newer host schema fields because lifecycle decoding already ignores unknown fields. The C ABI version remains unchanged.

### Interactions agent routing

CPA forces a non-empty Interactions `agent` request through its native Interactions provider. Returning a self-executor route for such a request causes CPA to reject the request instead of running the native agent.

The route RPC decoder will retain `Body`. For `SourceFormat == "interactions"`, a valid body containing a non-empty string `agent` will return `Handled: false` before rule evaluation. Interactions requests using `model` remain eligible for mapping.

### Configuration type validation

The four rule fields are registered as strings, but explicit JSON or YAML `null` currently becomes an empty Go string. This silently disables rules instead of rejecting an invalid configuration.

When present, each of these fields must be a string in both direct JSON and lifecycle YAML:

- `global_rules`
- `claude_messages_rules`
- `codex_responses_rules`
- `openai_completions_rules`

Omitted fields and explicit empty strings remain valid. Reconfiguration remains atomic: an invalid value must not replace the active compiled configuration.

### Streaming partition invariance

A whitespace-only host read is currently buffered only for unframed streams. In framed mode, a leading whitespace read can be dropped, so the same byte stream produces different output depending on network read boundaries.

Whitespace-only reads will be buffered in both modes. Recombining the subsequent bytes must produce exactly the same output as a single write.

### Multi-data SSE validity

When several SSE `data:` fields join into one JSON value and model restoration changes a top-level array, the restored JSON can contain physical newlines. The current code places that multiline value after one `data:` prefix, making later lines non-SSE fields and changing the event data to an invalid JSON prefix.

After a changed multi-data value is restored, it will be compacted with `encoding/json` before being written as one `data:` field. Unchanged events remain byte-identical, including field order and line endings.

### Incremental raw JSON arrays

For an unframed top-level JSON array, the rewriter buffers the entire response until the final `]`. A valid Gemini JSON stream therefore has no incremental delivery and memory grows with the complete response.

The rewriter will keep explicit top-level array state. It will:

1. Emit the opening bracket and preserved leading whitespace immediately.
2. Buffer at most the current incomplete array element.
3. Restore whitelisted model fields in each complete direct object element.
4. Preserve original whitespace, commas, and the closing bracket.
5. Leave scalar and nested-array elements unchanged.
6. Flush an incomplete tail without inventing delimiters or dropping bytes.

This incremental mode applies only to unframed raw JSON. A JSON value that must be synthesized as one SSE event remains buffered until complete.

### Gemini stream framing

CPA's Gemini stream translator supplies raw `GenerateContentResponse` JSON chunks to the Gemini HTTP handler. The handler adds SSE framing when `Alt == ""`; a non-empty `Alt` requests direct JSON. With upstream-header passthrough disabled, `host.model.execute_stream` can validly return no `Content-Type`.

The plugin currently defaults every missing stream `Content-Type` to `text/event-stream` and uses that header to frame raw chunks. For Gemini this can add an inner SSE layer before CPA adds the outer layer.

Gemini output chunks will never be synthetically SSE-framed by the plugin. If the host omitted `Content-Type`, the prepared response will use:

- `text/event-stream` when `Alt == ""`, because CPA's outer Gemini handler will frame the raw chunks.
- `application/json` when `Alt != ""`, because CPA's outer handler writes raw JSON.

Other advertised output formats retain their current SSE fallback and framing behavior.

### Body-dependent HTTP metadata

Changing body bytes while retaining a digest, validator, or byte-range header makes the forwarded HTTP metadata describe the old body.

When request rewriting changes the body, remove:

- `Content-Length`
- `Content-Digest`
- `Repr-Digest`
- `Digest`
- `Content-MD5`

When nonstream response restoration changes the body, remove the same body-length and digest fields plus:

- `ETag`
- `Content-Range`

A mapped stream can change any later payload after response headers are committed, so its prepared response headers will remove all response-side fields above in advance. Existing stream removal of `Transfer-Encoding` remains. Unrelated headers remain unchanged. Nonstream responses whose body bytes are unchanged retain all original headers.

### Compatibility gate provenance

`build-platform` writes `<library>.version` before Linux or macOS compatibility inspection. Aggregate packaging treats that sidecar as sufficient proof, so it can package a binary that never passed, or already failed, the compatibility gate.

The sidecar will become the marker for a successfully release-checked platform artifact:

- `build-platform` removes any stale sidecar before building and does not create a new one.
- `package-platform` builds, runs the applicable compatibility check, then writes the version sidecar only after the check succeeds.
- Aggregate packaging continues requiring the matching sidecar.

This also prevents a newly rebuilt binary from inheriting an earlier successful marker. README build examples will use the checked `package` path when preparing aggregate inputs.

### Inspector exit propagation

The current `readelf | checker` and `otool | checker` pipelines run under `set -e` without `pipefail`. An inspector can print an acceptable record, exit nonzero, and still allow packaging because only the checker status controls the pipeline.

Each inspector will run to completion in command substitution before its output is piped to the Go checker. A nonzero inspector status must terminate the recipe before the sidecar, archive, or checksum is created. This stays portable to the recipe's POSIX `/bin/sh`.

### Archive and checksum path aliases

On a case-insensitive macOS filesystem, differently cased archive and checksum paths can both be absent during the initial path check. After archive creation they identify the same file, and checksum writing truncates the archive.

Single-platform packaging will repeat the existing distinct-path check after archive creation and before checksum writing. At that point `os.SameFile` detects the alias on the actual filesystem.

### Fail-closed checksum updates

If an existing checksum file cannot be replaced after its archive was overwritten, the command returns an error but leaves a checksum that authenticates the previous archive. Aggregate packaging has the same risk for `checksums.txt` after updating one or more archives.

Before changing archives, packaging will remove the corresponding old checksum file or manifest; inability to remove it stops the operation. A failed checksum write will remove any partial checksum output. Packaging may leave an archive without a checksum after an error, but it must not leave a stale or partial checksum that appears valid for different bytes.

### Host-platform local smoke

`smoke-local` always builds and copies `windows/amd64/model-mapper.dll`. It cannot validate the plugin on Linux, Darwin, Windows arm64, or another supported host architecture.

The Make target will build `go env GOOS` and `go env GOARCH`. The helper will derive both the source artifact and CPA plugin destination from `runtime.GOOS`, `runtime.GOARCH`, and the platform library extension. The smoke remains a current-host test, not a cross-platform runner.

### Relative CPA executable paths

A relative `CPA_SMOKE_CPA_BIN` containing a path separator is passed to `exec.Command` and then executed with `Cmd.Dir = .test-cpa`, so a repository-relative path is resolved under the wrong directory.

`startCPA` will resolve path-like relative values against `repoRoot` before creating the command. Bare executable names remain eligible for normal `PATH` lookup.

### In-band smoke stream errors

The live stream checker accepts a valid model event, an `error` event, and `[DONE]` as success because it ignores the parsed `error` field.

Any non-null `error` in a JSON SSE payload will fail the smoke case immediately. Existing malformed-payload, model-restoration, and `[DONE]` checks remain.

### GitHub prerelease metadata

The accepted release grammar includes SemVer prerelease tags, but the workflow creates every `v*` tag as a normal GitHub Release.

For a validated tag whose version contains a prerelease component, the workflow will pass `--prerelease` to `gh release create`. If that release already exists, it will run `gh release edit --prerelease` before uploading replacement assets. Stable tags retain normal release behavior.

## Tests

### Permanent runtime regression tests

Tests in `main_test.go` will cover:

- registration always reports schema 1, including lifecycle requests carrying newer host schema fields;
- Interactions `agent` routes are unhandled while Interactions `model` routes still map;
- all four rule fields reject JSON and YAML `null`, while omitted and empty strings remain valid and failed reconfiguration is atomic;
- framed stream output is invariant when leading whitespace is isolated in a read;
- changed multi-data arrays remain valid SSE event data;
- raw arrays emit each completed object before the closing bracket, preserve separators, handle nested delimiters inside strings, remain partition-invariant, and flush incomplete tails without data loss;
- Gemini chunks remain raw for empty and non-empty `Alt`, with correct missing-header defaults;
- changed request, nonstream response, and stream response headers remove stale body metadata while unchanged bodies preserve it;
- a raw RPC envelope decodes body bytes once and leaves JSON-looking strings and non-whitelisted nested fields opaque;
- nonstream and stream cross-protocol callbacks preserve `EntryProtocol`, `ExitProtocol`, headers, query values, and `host_callback_id`.

### Permanent release and smoke regression tests

Explicit script tests will cover:

- failed compatibility leaves no version sidecar and cannot be accepted by aggregate packaging;
- an inspector that prints an acceptable version and exits nonzero prevents packaging;
- case-folded archive/checksum aliases are rejected on a case-insensitive filesystem;
- checksum failure cannot leave a stale checksum for changed archive bytes;
- host-platform smoke paths use the current runtime platform;
- a valid repository-relative CPA executable is launched from the smoke working directory;
- in-band stream errors fail the smoke;
- the workflow contains conditional prerelease creation and correction.

### Verification commands

The final verification will run:

```powershell
go test ./...
go test -race ./...
go test -count=20 ./...
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
```

It will also build Windows amd64 and Linux amd64 `c-shared` artifacts, inspect required exports, exercise single-platform and aggregate packaging with version `0.5.4`, validate ZIP roots and checksum contents, and load the host-native artifact through an executable ABI harness. A live external-provider smoke runs only if both required secrets are already available; missing credentials do not justify inventing or exposing them.

## Explicit exclusions

The audit did not find recursive replacement of arbitrary content or tool JSON. Response restoration remains limited to the existing whitelist.

The nonstream cross-protocol direction, headers, query values, and callback ID are already correct; tests will retain that contract without changing production behavior.

The caller-pattern cache can evict a route-time result before executor replay, but no stable per-request identity is available in the current CPA RPC. Removing its bound would permit authenticated callers to grow process memory without limit. No safe repository-local fix is included.

CPA v7.2.48 lacks `caller_scope` and Interactions, and its older stream bridge has a host-side close race. CPA Home can reject self-executor routes. These are CPA capabilities or defects and will not be changed in this repository.

`executor.count_tokens` remains unsupported because the plugin advertises executor model scope rather than a separate token-count capability, and the audit found no confirmed host path that requires this plugin to implement it.

No speculative recursive rewriting, new dependency, custom release service, or CPA source change will be added.

## Release completion

After all tests and artifact checks pass:

1. Update user-facing version examples to `0.5.4` and document the changed rewrite/header, Interactions agent, and checked packaging boundaries.
2. Commit the implementation and verification documents on the current branch.
3. Ensure the completed work is on local `main`.
4. Create annotated tag `v0.5.4`.
5. Push `main` and `v0.5.4` to `origin`.
6. Confirm the GitHub Actions Build workflow was triggered and report its final status without modifying CPA.
