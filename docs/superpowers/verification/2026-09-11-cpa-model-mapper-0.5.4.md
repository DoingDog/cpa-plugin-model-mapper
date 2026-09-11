# CPA Model Mapper v0.5.4 Verification

Date: 2026-09-11

Verified commit: `2e18bc1ebbcd711762dace4c871b2b22cf37d28e`

This record covers the local pre-release gate for the v0.5.4 functional audit. It does not claim the tag, remote workflow, or GitHub release existed when these checks ran; those are the next release gate.

## Review gates

- Task-scoped reviews for Tasks 1 through 11 completed.
- The final whole-branch review initially returned `CHANGES_REQUIRED`.
- Controller adjudication rejected findings that contradicted the inspected CPA Gemini framing chain, the binding non-empty Interactions `agent` rule, the specified header-removal set, or the authorized release scope.
- Final fix round 1 addressed 16 retained runtime, release, test, and documentation items.
- Its scoped re-review confirmed all 16 addressed and found one new Minor: atomic checksum temp files retained mode `0600` on Unix.
- Final fix round 2 reproduced that issue in Debian WSL, changed the completed temp file to mode `0644` before rename, and passed a scoped Opus re-review with zero findings.
- The Sonnet simplification pass inspected only task-changed code and produced no diff because it found no clearly smaller behavior-preserving implementation.

## Fresh Go verification

| Command | Result |
|---|---|
| `go test ./... -count=1` | PASS, 8.951 s |
| `go vet ./...` | PASS, no output |
| `go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1` | PASS, 0.218 s |
| `go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1` | PASS, 3.791 s |
| `go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1` | PASS, 0.220 s |
| `go test -race ./... -count=1` | PASS, 14.355 s |
| `go test -count=20 ./...` | PASS, 183.242 s |
| Debian WSL package-release suite | PASS, 0.027 s |
| `go test . -run '^(TestHandleExecutorExecuteAllFormats|TestHandleExecutorExecuteStreamAllFormats)$' -count=1` | PASS, 0.061 s |
| `git diff --check ca7d712..HEAD` | PASS, no output |

The focused protocol command covers `openai`, `openai-response`, `claude`, `gemini`, and `interactions` in both nonstream and stream executor paths. The complete suite also covers one-layer request body decoding, top-level-only request model rewriting, response-path whitelisting, SSE partitions and EOF, raw JSON value separators and arrays, header invalidation, caller scope, config validation, and host callback IDs.

## TDD evidence from final review fixes

The final fix workers recorded focused red failures before each production correction:

- A lifecycle YAML string alias failed with `claude_messages_rules must be a string`.
- A chunked incomplete array element lost spare pending capacity after every write.
- Seeded single-platform archives and checksums survived failed compatibility and inspector paths.
- Aggregate packaging wrote `checksums.txt` before a failing stale-archive cleanup.
- Single and aggregate checksum directory paths were deleted and accepted rather than rejected.
- Injected failed checksum writes received final paths instead of temporary siblings.
- Exported target `GOOS` and `GOARCH` overrode the intended host smoke target.
- Debian WSL reported atomic checksum mode `0600`, expected `0644`.

Each focused test passed after its corresponding minimal implementation, and the integrated suites above reran all permanent regressions.

## Native builds and packaging

The following commands completed with exit code 0:

```plaintext
make --no-print-directory package VERSION=0.5.4 GOOS=windows GOARCH=amd64
make --no-print-directory package VERSION=0.5.4 GOOS=linux GOARCH=amd64 "BUILD_CC=zig cc -target x86_64-linux-gnu.2.17" READELF=readelf
make --no-print-directory package VERSION=0.5.4
```

Built outputs:

| Artifact | Size |
|---|---:|
| `dist/windows_amd64/model-mapper.dll` | 4,770,816 bytes |
| `dist/linux_amd64/model-mapper.so` | 5,542,680 bytes |
| `dist/model-mapper_0.5.4_windows_amd64.zip` | 2,090,128 bytes |
| `dist/model-mapper_0.5.4_linux_amd64.zip` | 2,206,833 bytes |

Both `.version` sidecars contain exactly `0.5.4`. The Linux package compatibility gate passed:

```plaintext
readelf --version-info dist/linux_amd64/model-mapper.so |
  go run .github/scripts/check-release-compatibility.go -format glibc -max 2.17
```

`objdump -p` found all required Windows exports:

- `cliproxy_plugin_init`
- `cliproxyPluginCall`
- `cliproxyPluginFree`
- `cliproxyPluginShutdown`

`readelf --wide --dyn-syms` found the same four public Linux symbols.

Each platform ZIP contains only the dynamic library and `LICENSE` at archive root. Individual checksum verification passed:

```plaintext
model-mapper_0.5.4_windows_amd64.zip: OK
model-mapper_0.5.4_linux_amd64.zip: OK
```

Recorded SHA-256 values:

- Windows: `23d5acccdbe5ada3a091efa6fc6580047efc86d429088c762318b24bed99e9c5`
- Linux: `ba8c67e25e00e70929f120e7a540b56e6706542c563138b96e8a3e7db4e20e00`

Aggregate packaging created the two locally available v0.5.4 archives under `dist/release/`; `sha256sum -c checksums.txt` reported both `OK`. The tag-triggered GitHub workflow remains responsible for building and publishing the full seven-platform matrix.

## Host-native ABI verification

A temporary C harness was compiled with Zig for Windows amd64 and loaded `dist/windows_amd64/model-mapper.dll` through `LoadLibrary` and `GetProcAddress`. The final run reported:

```plaintext
ABI smoke PASS: schema 1/6, registration schema 1, callback/free, direct exports, shutdown
```

The harness verified:

- all four exports resolve;
- ABI version 1 initialization succeeds;
- returned function pointers match the direct exports;
- lifecycle requests carrying schema 1 and schema 6 both register successfully;
- both registration responses advertise schema 1;
- release metadata reports version `0.5.4`;
- `executor.execute` calls `host.model.execute` with the rewritten upstream body;
- the host response is restored to the client model;
- the plugin invokes the host free callback exactly once for the host-owned response;
- every plugin-owned response buffer is freed through the plugin free function;
- direct exported call/free works independently of the returned API table;
- shutdown completes.

The first two temporary harness attempts used lowercase or snake_case spellings where these SDK structs serialize PascalCase fields. Those harness assertions were corrected to the actual wire names before the successful run; they did not expose a plugin failure.

## Live smoke gate

Live smoke was skipped because neither `CPA_SMOKE_API_KEY` nor `CPA_SMOKE_CPA_BIN` was set. No secret value was printed or recorded.

## Repository boundaries

- Local branch at verification time: `main`, 23 commits ahead of `origin/main`.
- The only untracked path is the task-start `.claude/`; it was not added or modified as source.
- `git diff --check ca7d712..HEAD` passed.
- The ignored CPA checkout remained clean at `956ce7cf785a0676aa7cc24719927bbfa03c69af`.
- No CPA source was modified.
