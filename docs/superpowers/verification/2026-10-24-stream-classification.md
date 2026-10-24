---
title: Raw/SSE Stream Classification Verification
type: verification
date: 2026-10-24
baseline: 2e0b34c6429da77b0a1bc710de134833dd25f455
source_commit: f7c5c682a87ea37e05cc30ffd8d186b17c774cd5
selected_patch: 0.5.17
specification: ../specs/2026-10-24-stream-classification-design.md
plan: ../plans/2026-10-24-stream-classification-implementation.md
---

# Raw/SSE Stream Classification Verification

## Acceptance and source identity

Independent U4 acceptance passed on Windows/amd64 at source commit `f7c5c682a87ea37e05cc30ffd8d186b17c774cd5`. Both classifier families passed permanent root/helper/RPC tests, focused actual native/Claude HTTP repetitions, and full same-library integration. Actual default and separately injected patch registration passed. Paired measurements retain material CPU costs; this record makes no performance-equivalence claim. R14/AE7 delivery remains U5 work.

The [canonical specification](../specs/2026-10-24-stream-classification-design.md) owns R1-R15 and AE1-AE8. The [implementation plan](../plans/2026-10-24-stream-classification-implementation.md) defines the U4 checks and U5 delivery boundary. This document adds no executable input. Its containing commit has a new VCS SHA; the runtime results below remain attributed to tested source `f7c5c682a87ea37e05cc30ffd8d186b17c774cd5`. Final clean-checkout CI must run on the delivered descendant SHA.

The assigned worktree is `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/u4-ef6a16`, branch `verify/u4-stream-ef6a16`. Acceptance and documentation intake both found it clean at that source commit. Its parent is `4b62f13c2b7daa9a0863d5892bbed41a381fb94f`; frozen production baseline is `2e0b34c6429da77b0a1bc710de134833dd25f455`. U3's completed commit changes only `stream_rewriter.go`, `stream_raw_number_regression_test.go`, `stream_protocol_regression_test.go`, and the sole native fixture `.github/scripts/testdata/cpa-functional-regression_test.go`. The other production files, dependencies, configuration, routing, C ABI, supported formats, and build/release entry points are unchanged.

The following path prefixes abbreviate exact absolute directories in commands and evidence references. Command receipts retain literal argv, cwd, environment, timestamps, numeric exits, and before/after source hashes.

```plaintext
W   = C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/u4-ef6a16
E   = C:/Users/user/Downloads/cpa-plugin/dist/functional-audit-20261024-coordination-ef6a16a7/u4-ef6a16-ebff4e33dde9
U3  = C:/Users/user/Downloads/cpa-plugin/dist/functional-audit-20261024-coordination-ef6a16a7/u3-completed/evidence
U3W = C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/wf_20f33b6b-24a-1
U3E = C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/wf_20f33b6b-24a-1/dist/u3-wf_20f33b6b-24a-1/correction-78575ce863cb
B   = C:/Users/user/Downloads/cpa-plugin/dist/functional-audit-20261024-baseline-c3d75821
C   = C:/Users/user/AppData/Local/Temp/cpa-audit-baseline-c3d75821-__ldam33
```

`E/manifest.json` has SHA256 `6f845b53e09cb448a08b38754d36adc5263833de9aa996764ce13fcf371adac8`. Its `evidence-index.json` has SHA256 `9344efbebfc6f5099802ecc4e23d4be5067fdfcdc27f870b3c904ee8a81d9cf4`. Standard JSON decoding with duplicate-key rejection and individual file SHA256/size checks verified the frozen index, command receipts, and tracked-input map. The index excludes the pinned CPA Git checkout; its revision and clean status have separate receipts. `acceptance-checks.json`, `final-membership.json`, `final-capture-keysets.json`, and `final-identities.json` contain the actual acceptance comparisons. U3's copied manifest SHA256 is `ee72c7291f76c296f21a01388d49eb3732012da4cebc758bc9d19ca6642f8413`; its original absolute paths remain unchanged in copied receipts.

U4 checked Git blobs and actual checkout bytes separately. Its four changed Go files use CRLF checkout bytes; their LF-normalized bytes equal the committed blobs and U3's tested final files. No newline conversion was made during acceptance.

| Input | Committed LF SHA256 | U4 actual checkout SHA256 |
| --- | --- | --- |
| `stream_rewriter.go` | `314ab20a80aa53ee68d601bc2587fc3db9dc482e6b3de332101cff07cae46873` | `254bc7c48572925dcbef86e118b0161fd0764a84e6ed92557f09ace10d39fd13` |
| `stream_raw_number_regression_test.go` | `413dcb35542c954807f6b4e3728f1909ccd061d7a05dd9bbce885c08b707ead0` | `c438347f04d5bde7a24a1d2e31c017abaeb29890ad9709d9cf1c88dda5ec38ca` |
| `stream_protocol_regression_test.go` | `3237080d81c4868196c38ed948a72c40a49032740043894d06dfa7fa516c234b` | `21a5c79b52e5a4831748cedfd32ed7ccb27fd9f9b0c806e665b3845cbbd8622f` |
| Sole native fixture | `cd656aeac7fc42e717345f337cb77b7bbd85263f544a51665e1532a6233a6227` | `a9bc735c13a79c3d8cedcdc9643d707c6dffb18ffdf15e2ebbae5067b8a3ef70` |

`entry.json`, `source-scope-review.json`, `tracked-inputs.json`, and `frozen-final/source.json` retain the complete maps. Every completed acceptance receipt binds the same U4 source map before and after its command. U4's final runtime results are independent executions, not reused U3 passes.

### Toolchain and pinned host

The actual toolchain was Go `1.26.5`, `GOOS=windows`, `GOARCH=amd64`, `GOAMD64=v1`, `CGO_ENABLED=1`, `CC=gcc`, and `GOWORK=off`. GCC reports `16.1.0`, MSYS2 Rev5. `go.mod` still declares Go `1.26.0` and CPA `v7.2.152`. The actual CPA server is pinned to `c76dfd4e0edabab9000628b1560ab8ab379eadb8`, module Sum `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`, binary SHA256 `1baf675218159704eb364ee28fee7c9b7852bf3101656cb9f5daf7ce7345df12`. Its build info reports that revision with `vcs.modified=false`; its copied Git checkout is clean. The existing writable-copy/fixture-overlay mechanism leaves CPA source and the module cache unchanged.

The default plugin used the existing `-trimpath -buildmode=c-shared -ldflags='-s -w '` policy. The separate patch build adds `-X main.pluginVersion=0.5.17` through existing `VERSION` handling. `final-toolchain`, `final-gcc-version`, `final-cpa-buildinfo`, `final-cpa-revision`, `final-cpa-clean`, `final-buildinfo`, and `final-patch-buildinfo` preserve commands and output.

The plugin DLLs embed parent-checkout revision `4b62f13c2b7daa9a0863d5892bbed41a381fb94f`, `vcs.modified=true`, and module version `v0.5.17-0.20261023182439-4b62f13c2b7d+dirty`. This stamp does not identify the tested worktree. The saved Go 1.26.5 sources show `vcsGit.RootNames` requiring a `.git` directory; this worktree has a `.git` file, so VCS discovery reaches the original checkout. `go-vcs-stamping-source-evidence.json` preserves the exact source snippets and hashes. `build-effective-inputs` records W as the actual package/module directory and its selected Go files. A separate forced `go build -a -x -trimpath -buildmode=c-shared '-ldflags=-s -w ' -o E/provenance-default/model-mapper.dll .` in W exited 0 and reproduced the actual tested default DLL SHA256. `build-provenance.json` binds those paths, frozen hashes, clean `f7c5c682` HEAD, and identical bytes. No source, build policy, toolchain, or Git configuration was changed to alter the stamp.

## Permanent baseline RED and repaired GREEN

### Root assertions and genuine-SSE controls

U3's final baseline replay and U4's independent replay use permanent repaired tests with frozen `2e0b34c6` production through a Go overlay. The overlaid `stream_rewriter.go` equals the baseline Git blob; other production inputs remain baseline-equivalent. Compilation failure, setup failure, timeout, skip, and zero selection are excluded from behavioral RED.

| Owner / evidence directory | Cwd | Exit / observed result |
| --- | --- | --- |
| U3 `baseline-root-red` | U3W | 1, both families reach explicit wrong-output assertions |
| U3 `baseline-controls` | U3W | 0, correct controls remain GREEN |
| U4 `baseline-root-red` | W | 1, the same permanent defect assertions fail |
| U4 `baseline-controls` | W | 0, genuine-SSE/established-SSE controls remain GREEN |

The actual commands, in table order with the defined absolute path prefixes, are:

```plaintext
go test -json -count=1 -overlay U3E/baseline/overlay.json -run '^TestRawNeutralWhitespace(Document|Sequence)$|^TestJSONShapedSSEEmitsDuringWrite$' .
go test -json -count=1 -overlay U3E/baseline/overlay.json -run '^TestRawScalarLookingSSEKeepsUnknownObject$|^TestEstablishedSSEWhitespaceContinuation$|^TestJSONShapedSSEPrettyCRContinuation$' .
go test -json -count=1 -overlay E/baseline/overlay.json -run '^TestRawNeutralWhitespaceDocument$|^TestRawNeutralWhitespaceSequence$|^TestJSONShapedSSEEmitsDuringWrite$' .
go test -json -count=1 -overlay E/baseline/overlay.json -run '^TestRawScalarLookingSSEKeepsUnknownObject$|^TestEstablishedSSEWhitespaceContinuation$' .
```

For A, `TestRawNeutralWhitespaceDocument` receives `\r\n\r\n{"model":"upstream"}`, split after byte 3, and baseline output retains `upstream` instead of `client`. `TestRawNeutralWhitespaceSequence` receives `{}` / two LF bytes / `1.5 {"model":"upstream"}`, or the blank prefix attached to that final callback; baseline output again retains `upstream`. For B, `TestJSONShapedSSEEmitsDuringWrite` receives `{` and the remaining real SSE event. Baseline Write emits no event where the permanent assertion requires the complete event with `message.model=client` before EOF. U4's full unit/race and focused repeated race pass these unchanged assertions.

`TestRawScalarLookingSSEKeepsUnknownObject` permanently checks number, `true`, `false`, `null`, and JSON-string first fields with LF, CR, and CRLF, framed and unframed. Its explicit oracle leaves the unknown `{"model":"upstream"}` line unchanged and restores only the `data:` JSON to `client`. It is baseline GREEN and repaired GREEN. Existing scalar-SSE interpretation is preserved.

### Native and Claude HTTP assertion-level proof

`U3/baseline-inputs.json` binds the frozen production overlay and the final permanent fixture SHA256 `cd656aeac7fc42e717345f337cb77b7bbd85263f544a51665e1532a6233a6227`. `U3/baseline-build/receipt.json` records `make build-platform GOOS=windows GOARCH=amd64 VERSION= DIST_DIR=U3E/baseline/artifacts`, cwd U3W, `GOFLAGS=-overlay=U3E/baseline/overlay.json`, exit 0. The resulting built/canonical/loaded baseline DLL SHA256 is `b581eb4ae828e50ee5bd63418d967d4c2600a2a1e34024bcfe9ce0e426f1b840`. Actual loading used PID 32836 and `C:/Users/user/AppData/Local/Temp/cliproxy-pluginhost/pid-32836/cliproxy-plugin-model-mapper-b581eb4ae828e50ee5bd63418d967d4c.dll`. The saved baseline canonical copy is under `U3/baseline-native-red-native/plugins/windows/amd64/model-mapper.dll`.

U3's baseline native command was `go test -json -count=1 -overlay U3E/_tools/smoke-overlay.json .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -run '^TestU3Native$' -timeout=15m`, cwd U3W, exit 1. It sets `CPA_SMOKE_PLUGIN` to the baseline canonical DLL, `CPA_FUNCTIONAL_EVIDENCE=U3E/baseline-native-red-native`, `U3_FIXTURE=U3E/baseline-native-red-native/_inputs/fixture.go`, and this exact inner selector:

```plaintext
^(TestModelMapperFunctionalRawNumberContinuation|TestModelMapperFunctionalNativeProtocolAndErrors|TestModelMapperFunctionalProtocolHTTP)$/^classification
```

The raw native log, not parent-test totals, establishes these permanent assertion failures:

- A: `TestModelMapperFunctionalRawNumberContinuation/classification/document/split=3/mapped-native`, `classification/sequence/split=-1/mapped-native`, and `classification/attached/parts=2` and `parts=3` return `grok-4.7` where the expected mapped output contains `grok-4.6`. Provider request/model assertions still reach the expected upstream path.
- B native: `TestModelMapperFunctionalNativeProtocolAndErrors/classification-claude-split` and `classification-claude-bytewise` return the complete realistic Claude stream with `message.model=grok-4.7` instead of `grok-4.6`.
- B HTTP: `TestModelMapperFunctionalProtocolHTTP/classification-claude/field={/split=1/mapped` and `split=-1/mapped` reach HTTP 200 with `text/event-stream` and fail the exact wire comparison on that same model field. These are explicit wire mismatches. The separate native `classification-claude/field={/split=1` and `split=-1` pre-EOF guards report that the complete start event waited for upstream EOF; those deadline failures remain recorded but are excluded from assertion-level RED.

Whole mapped, direct, disabled, enabled-unmapped, and ordinary `x-vendor-field` controls pass on the baseline DLL. The same final fixture assertions pass on U3's repaired DLL and independently on U4's actual integration DLL in both focused repetitions and full integration. `U3/final-red-green.json`, the raw baseline log, and `E/final-red-green.json` retain the distinct attribution. U3's precommit repaired DLL identities do not substitute for U4's newly built/tested identity.

## Independent U4 commands

All commands in this section ran from W with `CGO_ENABLED=1`, `GOWORK=off`, and the unchanged source map. Each named directory in E contains `receipt.json` and `output.log`. The table reports actual process exit and elapsed seconds, not inferred success from a summary.

| Gate / receipt directory | Command | Exit | Seconds |
| --- | --- | --- | --- |
| Full recursive unit, `final-unit` | `go test -json -count=1 ./...` | 0 | 72.48 |
| Full recursive race, `final-race` | `go test -json -race -count=1 -timeout=30m ./...` | 0 | 844.12 |
| Focused repeated race, `final-focused-race` | `go test -json -race -count=5 -timeout=20m -run '<selector below>' .` | 0 | 95.53 |
| Vet, `final-vet` | `go vet ./...` | 0 | 1.15 |
| Packager tests, `final-package` | `go test -json -count=1 .github/scripts/package-release.go .github/scripts/package-release_test.go` | 0 | 1.72 |
| Compatibility tests, `final-compatibility` | `go test -json -count=1 .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go` | 0 | 5.52 |
| Smoke-helper tests, `final-smoke` | `go test -json -count=1 .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go` | 0 | 5.80 |
| All existing benchmarks, `final-benchmarks` | `go test . -run '^$' -bench . -benchtime=1x -count=1 -benchmem` | 0 | 4.47 |
| Registration/C ABI, `final-registration-abi` | `go test -json -count=1 -run '<registration selector below>' .` | 0 | 2.11 |
| Actual full integration, `final-integration` | `make integration VERSION= DIST_DIR=E/final-default` | 0 | 318.14 |
| Focused native/HTTP repetition 1, `final-native-1` | Native command below, attempt `final-native-1-native` | 0 | 15.45 |
| Focused native/HTTP repetition 2, `final-native-2` | Native command below, attempt `final-native-2-native` | 0 | 13.59 |
| Paired measurements, `final-paired-1` | `go test -json -count=1 -overlay E/final-paired-1-inputs/overlay.json -run '^TestU3Paired$' -benchtime=200ms .` | 0 | 61.82 |
| Unchanged paired rerun, `final-paired-2` | Same command with `E/final-paired-2-inputs/overlay.json` | 0 | 58.84 |

The focused race selector is exactly:

```plaintext
^TestEstablishedSSEWhitespaceContinuation$|^TestJSONShapedSSEPrettyCRContinuation$|^TestRawNeutralWhitespace(Document|Sequence|Forwarder|LinearAllocations|PendingLimit)$|^TestJSONShapedSSE(EmitsDuringWrite|LeadingWhitespace|AfterRawPrefix|Partitions|Forwarder|UnterminatedEOF|PrettyLinearAllocations|HostErrorFlush)$|^TestStreamClassificationRPCEnvelopes$|^TestFunctionalShutdownPreserves(Flush|Payload)ErrorAtBackpressure$
```

The registration/C ABI selector is exactly:

```plaintext
^(TestPluginRegistrationMetadataAndConfigFields|TestPluginRegisterKeepsSchemaOneForNewerHost|TestCIntLengthBounds|TestPluginResponseLengthBounds|TestCopyPluginRequestRejectsNullWithLength|TestPluginCallClearsResponseWhenMethodNil)$
```

Each named registration/C ABI case also runs in full unit and full race. Full unit, full race, and focused race logs contain no required skip, failure, or `DATA RACE` report; every selected repetition has its matching PASS. Vet emits no diagnostics. All original benchmark declarations were individually matched to benchmark output names; the one-iteration run checks execution and preflight correctness, not timing equivalence.

Full race ended at `2026-10-24T22:44:07.248977+08:00`; focused race ended at `22:45:42.921272+08:00`; full integration began at `22:46:04.417015+08:00`. They ran sequentially. The old internal two-second witnesses, integration deadlines, data sizes, pending limit, and allocation ceilings were not increased.

Windows skips are exactly `TestWriteChecksumFileAtomicallySetsMode`, `TestRunRejectsAliasedOutputsThroughSymlinkThenParent`, and `TestWriteSmokeConfigTightensPermissions`. The ordinary helper suite also skips `TestCPAPluginIntegration` without its explicit environment. Actual `make integration` separately executes that entry and the full native fixture; its required native cases are not skipped.

### Focused native/HTTP invocation and layer attribution

Both focused repetitions use `go test -json -count=1 -overlay E/_tools/smoke-overlay.json .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -run '^TestU3Native$' -timeout=15m`. For each attempt directory, `CPA_SMOKE_PLUGIN` is its `plugins/windows/amd64/model-mapper.dll`, `CPA_FUNCTIONAL_EVIDENCE` is that directory, and `U3_FIXTURE` is its `_inputs/fixture.go`. `U3_SELECTOR` is exactly:

```plaintext
^(TestModelMapperFunctionalRawNumberContinuation|TestModelMapperFunctionalNativeProtocolAndErrors|TestModelMapperFunctionalProtocolHTTP|TestModelMapperFunctionalReconfigureReload)$
```

Both raw native logs execute and pass all selected expanded cases. Full integration uses `CPA_FUNCTIONAL_EVIDENCE=E/final-integration-evidence`, rebuilds the default library, and executes the same fixture plus the existing binary/HTTP/WS and bridge checks. Focused repetitions run after integration against copies of that actual DLL; there is no focused/full hash discrepancy.

A's five `application/json` formats (`openai`, `openai-response`, `claude`, `gemini`, `interactions`) and the unframed OpenAI event-stream-header control are executor-helper evidence. Both families also have separate `handleMethod`/`callHost` JSON-envelope proof. The new native A witnesses exercise OpenAI raw behavior; B exercises generic Claude native execution and real `/v1/messages` HTTP handling. Its realistic `message_start.message` includes id/type/role/model/content/stop/usage, followed by normal `message_delta` and `message_stop`. The permanent fixture checks exact wire bytes, existing gin SSE Decoder results, timely complete start output, upstream request/model, Calls=1, route controls, and normal termination. Controlled providers replace external output; DLL, ABI, CPA bridge/validator, callbacks, and HTTP consumer execute for real. These results do not establish five new native formats, stock-provider natural chunking, or external typed SDK coverage.

## Actual tested libraries and registration

| Artifact | Retained built path | SHA256 |
| --- | --- | --- |
| Actual integration/default DLL | `E/final-default/windows_amd64/model-mapper.dll` | `30ff61205c31d1aef32e188e6fd89d9328e1f76c63d963b94b081b6d273723f1` |
| Separate injected patch DLL | `E/final-patch/windows_amd64/model-mapper.dll` | `1a2945dba5d69d8361c8b47dea33e8e0c1467723626a202c889296ff9976b984` |
| Actual CPA server | `E/final-default/integration/cpa.exe` | `1baf675218159704eb364ee28fee7c9b7852bf3101656cb9f5daf7ce7345df12` |

The fixture checks the live Windows module handle and loaded/canonical byte equality. `final-identities.json` retains every identity receipt, including A's `raw-number-loaded.json`, B's `classification-claude-*-loaded.json`, `classification-native-eof-loaded.json`, and `classification-http-loaded.json`.

| Execution | PID | Canonical path | Actual loaded shadow path |
| --- | --- | --- | --- |
| Full native fixture, default hash above | 16704 | `C:/Users/user/AppData/Local/Temp/TestCPAPluginIntegration3557110860/001/plugins/windows/amd64/model-mapper.dll` | `C:/Users/user/AppData/Local/Temp/cliproxy-pluginhost/pid-16704/cliproxy-plugin-model-mapper-30ff61205c31d1aef32e188e6fd89d93.dll` |
| Focused repetition 1, same default hash | 30704 | `E/final-native-1-native/plugins/windows/amd64/model-mapper.dll` | `C:/Users/user/AppData/Local/Temp/cliproxy-pluginhost/pid-30704/cliproxy-plugin-model-mapper-30ff61205c31d1aef32e188e6fd89d93.dll` |
| Focused repetition 2, same default hash | 19204 | `E/final-native-2-native/plugins/windows/amd64/model-mapper.dll` | `C:/Users/user/AppData/Local/Temp/cliproxy-pluginhost/pid-19204/cliproxy-plugin-model-mapper-30ff61205c31d1aef32e188e6fd89d93.dll` |
| Actual default registration | 15580 | `E/final-registration-default-native/plugins/windows/amd64/model-mapper.dll` | `C:/Users/user/AppData/Local/Temp/cliproxy-pluginhost/pid-15580/cliproxy-plugin-model-mapper-30ff61205c31d1aef32e188e6fd89d93.dll` |
| Actual patch registration, patch hash above | 18324 | `E/final-registration-patch-native/plugins/windows/amd64/model-mapper.dll` | `C:/Users/user/AppData/Local/Temp/cliproxy-pluginhost/pid-18324/cliproxy-plugin-model-mapper-1a2945dba5d69d8361c8b47dea33e8e0.dll` |

The focused canonical copies and actual fixture/overlay inputs remain in their attempt directories. Full integration's existing cleanup removed its temporary canonical file, after live comparison. Its saved actual process copies remain under `E/final-integration-evidence/process-*/loaded-copy.dll` and `shadow-*.dll`; `integration-process-tested-copies.json` names every copy and hash. Each retained copy was rehashed and equals the tested default DLL. Neither a current filename nor a later rebuild is used to inherit an earlier runtime pass.

`final-patch-build` records `make build-platform GOOS=windows GOARCH=amd64 VERSION=0.5.17 DIST_DIR=E/final-patch`, cwd W, exit 0. `final-registration-default` and `final-registration-patch` use the focused native command above with `U3_SELECTOR=^TestModelMapperFunctionalReconfigureReload$` and separate attempt directories. Both exit 0. Their verification-only fixture overlays record actual registration/capabilities and loaded identity; the patch overlay additionally changes the existing expected version to `0.5.17`. The permanent fixture and default version constant are unchanged.

Actual loaded registration reports `Metadata.Version=0.0.0-dev` for default and `0.5.17` for patch, schema 1, `model_router=true`, `executor=true`, and `executor_model_scope=static`. Input and output formats are exactly `openai`, `openai-response`, `claude`, `gemini`, `interactions`. The five config fields are exactly `global_rules`, `claude_messages_rules`, `codex_responses_rules`, `openai_completions_rules`, `rules_stack_mode`; the last retains `off`, `specific_first`, and `global_first`. CPA still owns enabled/priority. Both `objdump -p` receipts list exactly `cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`, and `cliproxyPluginShutdown`. Focused C ABI tests and actual loading exercise the existing buffer/lifetime behavior.

Saved read-only checks at `2026-10-24T22:31+08:00` used `git -C W tag --list`, `git -C W ls-remote --tags origin`, and `gh api repos/DoingDog/cpa-plugin-model-mapper/releases?per_page=100`, each exit 0. Latest Release was `v0.5.16`; `v0.5.17` was unused locally, remotely, and in Releases. The later saved remote-main read at `22:54+08:00` still returned baseline `2e0b34c6`. These are observations at those times, not current publication authority or a reserved tag. U5 must recheck occupancy before delivery. No local archive/package or release publication is claimed by this U4 stage.

## Preserved oracles, runtime members, and exact capture keys

`final-preserved-oracles.json` records standard Go parser/AST comparisons against baseline for every original test/benchmark declaration and retained assertion/control structure. All `Preserved` results are true, including the original expected values, timeouts, allocation/size boundaries, imports, and fixture controls. Extended existing functions retain their original assertions. `final-membership.json` separately compares actual execution names against the old root declarations, all old benchmark declarations, and `B/verification-013f38ed5c/native-events.jsonl`. Every old native member is present and passes; added members occur only in the assigned classification case families. No required member is unknown, duplicated, missing, failed, or skipped. Exact membership and multiplicity, rather than totals, determine acceptance.

All four capture files were decoded with duplicate-object-key rejection. Keys were compared record-by-record against B's frozen `native-evidence` captures, rejecting duplicate, missing, and unknown identities. `native-binary` keys use `Name + /stream= + lowercase Stream`; `mixed-binary` keys use `Name + / + Kind`; the other sets use `Name`. Every final record has Calls=1 and no Errors. The following digest is SHA256 of the sorted key list encoded as compact ASCII-escaped JSON, without a trailing newline; it matches baseline and final independently.

| Capture file | Exact members | Baseline/final key-list SHA256 |
| --- | --- | --- |
| `native-binary-captures.json` | 1,536 | `626945808b48444bc9d8b9e0b5b2ff7c27d3dfb2abffab538b059c8d3a28ad1b` |
| `mixed-native-captures.json` | 1,800 | `4d87c2eb98f6fe03be3594176b6c34fd2c8c7843a598058c2be89d90ac605b7b` |
| `mixed-binary-captures.json` | 1,440 | `1ea5f8740f58f3a8cf538d6e903abbeb2df73c82f480c2185739324b3dc7d904` |
| `mixed-shape-controls.json` | 1,680 | `60d2c817ccf28855fa930c60b23333fc164b8827daba97340bc91ae2921a5686` |

`final-capture-keysets.json` retains the complete old/final key lists and empty missing/unknown/duplicate/error lists. Full integration also executed the complete real translator/validator unit at 16,842,937 bytes, SHA256 `649180ca25a1be756800b35264d48c7c338e4d5ff94c6a3eb90c9d88bf17b9ae`, and the existing mapped/unmapped large HTTP paths. The incomplete-unit limit remains `maxPendingStreamBytes = 16 << 20`; complete-unit and cumulative-traffic behavior, output ownership, non-replay, shutdown/error selection, close-once, and failed-close remedy retain their old assertions.

## Paired performance measurements and correctness cost

U4 independently compared frozen `2e0b34c6` and final variants in one Go process, generated with standard Go AST tooling. Each workload has eight paired samples per variant in each of two rounds, alternating baseline/final then final/baseline order. Exact output preflight runs outside measured work. `final-paired-1-inputs` and `final-paired-2-inputs` retain the variant sources and overlays; `final-paired-*-samples.json.*.input.json` and `.partition.json` retain actual payload bytes, framing, and callback lengths. Raw samples reproduce every median below. Ratios are final median ns/op divided by baseline median ns/op; `final-paired-metrics.json` also retains median paired ratios.

| Workload | Round 1 median ns/op, baseline -> final | Ratio | Round 2 median ns/op, baseline -> final | Ratio |
| --- | --- | --- | --- | --- |
| `fragmented-64KiB-32` | 688,442.5 -> 720,269.0 | 1.046230 | 693,869.0 -> 865,787.5 | 1.247768 |
| `fragmented-1MiB-8192` | 18,086,988.0 -> 19,381,485.5 | 1.071571 | 22,710,040.0 -> 24,599,873.5 | 1.083216 |
| `pretty-bytewise` | 263,584.5 -> 275,440.5 | 1.044980 | 301,458.0 -> 332,803.5 | 1.103980 |
| `pretty-8192` | 29,059,114.5 -> 31,437,282.5 | 1.081839 | 27,684,349.5 -> 29,436,907.0 | 1.063305 |
| `colon-rich-bytewise` | 114,282.5 -> 131,307.0 | 1.148969 | 110,640.0 -> 124,757.5 | 1.127599 |
| `line-ending-CR-1024` | 335,975.5 -> 358,659.0 | 1.067515 | 316,125.5 -> 341,163.5 | 1.079203 |
| `line-ending-mixed-1024` | 339,470.0 -> 392,850.0 | 1.157245 | 322,732.0 -> 369,735.0 | 1.145641 |
| `line-ending-CR-8192` | 2,556,027.5 -> 2,716,213.5 | 1.062670 | 2,505,156.5 -> 2,751,805.5 | 1.098457 |
| `line-ending-mixed-8192` | 2,575,304.0 -> 2,981,878.0 | 1.157874 | 3,115,277.5 -> 3,484,356.5 | 1.118474 |
| `comma-first-64` | 23,157.0 -> 24,648.0 | 1.064387 | 25,208.5 -> 26,459.0 | 1.049606 |
| `comma-first-128` | 48,631.5 -> 54,401.0 | 1.118637 | 45,178.5 -> 47,835.0 | 1.058800 |
| `comma-first-1024` | 351,847.5 -> 396,453.5 | 1.126777 | 337,886.0 -> 356,093.0 | 1.053885 |

Allocation medians use the same samples. Fractional values arise from the median of eight integer observations.

| Workload | Round 1 allocs/op, baseline -> final | Round 1 B/op, baseline -> final | Round 2 allocs/op, baseline -> final | Round 2 B/op, baseline -> final |
| --- | --- | --- | --- | --- |
| `fragmented-64KiB-32` | 55.5 -> 55.0 | 684,544.5 -> 684,548.5 | 55.0 -> 56.0 | 684,540.0 -> 684,570.0 |
| `fragmented-1MiB-8192` | 92.0 -> 92.0 | 16,667,417.0 -> 16,667,433.5 | 92.0 -> 92.0 | 16,667,416.5 -> 16,667,433.5 |
| `pretty-bytewise` | 5,165.0 -> 5,165.0 | 126,547.5 -> 126,563.0 | 5,165.0 -> 5,165.0 | 126,547.0 -> 126,563.0 |
| `pretty-8192` | 655,412.0 -> 655,412.0 | 16,228,080.0 -> 16,228,100.0 | 655,412.0 -> 655,412.0 | 16,228,086.0 -> 16,228,083.5 |
| `colon-rich-bytewise` | 1,454.0 -> 1,454.0 | 54,816.5 -> 54,833.0 | 1,454.0 -> 1,454.0 | 54,816.0 -> 54,832.5 |
| `line-ending-CR-1024` | 6,168.0 -> 6,168.0 | 145,055.0 -> 145,070.0 | 6,168.0 -> 6,168.0 | 145,054.5 -> 145,071.5 |
| `line-ending-mixed-1024` | 6,168.0 -> 6,168.0 | 153,251.0 -> 153,269.0 | 6,168.0 -> 6,168.0 | 153,251.0 -> 153,268.0 |
| `line-ending-CR-8192` | 49,177.0 -> 49,177.0 | 1,150,684.5 -> 1,150,707.0 | 49,177.0 -> 49,177.0 | 1,150,689.5 -> 1,150,691.0 |
| `line-ending-mixed-8192` | 49,177.0 -> 49,177.0 | 1,199,866.0 -> 1,199,873.0 | 49,177.0 -> 49,177.0 | 1,199,872.0 -> 1,199,896.0 |
| `comma-first-64` | 413.0 -> 413.0 | 12,920.0 -> 12,936.0 | 413.0 -> 413.0 | 12,919.5 -> 12,936.0 |
| `comma-first-128` | 799.0 -> 799.0 | 24,191.0 -> 24,206.5 | 799.0 -> 799.0 | 24,191.0 -> 24,207.0 |
| `comma-first-1024` | 6,180.0 -> 6,180.0 | 168,536.5 -> 168,554.0 | 6,180.0 -> 6,180.0 | 168,536.5 -> 168,553.0 |

The [existing hot-path validation rule](../plans/2026-09-01-model-mapper-hot-path-performance.md#task-5-full-verification-and-independent-review), baseline line 844, requires explanation and rerun for repeated-median regressions greater than 10%. `performance-rerun-rationale.json` records the explanation before round 2, which repeats identical inputs and alternating order. Colon-rich bytewise and both mixed-line-ending workloads remain above 10% in both rounds, about 11.8%-15.8%. Fragmented 64 KiB also measures 24.8% in round 2 versus 4.6% in round 1; pretty bytewise reaches 10.4% in round 2, and the two larger comma-first cases exceed 10% in round 1. All samples and small allocation/B-op variation remain visible.

The accepted correctness cost is incremental real-SSE-line evidence checking, needed to release an impossible first raw candidate and emit complete SSE before EOF. Valid JSON comma/colon/closing-punctuation lines skip full Decoder probes. Monotonic `lineFrom`/`lineScan` cursors and one earliest-CR/LF `bytes.IndexAny` search remove unnecessary repeated suffix searches; cursor/line checks still consume CPU. `final-performance-tradeoff.json` preserves this explanation and both rounds. There is no zero-extra-CPU or blanket no-regression conclusion.

Hard checks passed independently with unchanged limits:

- `final-complexity`: `go test -json -count=1 -overlay E/_tools/complexity-overlay.json -run '^TestU3ComplexityProof$' .`, cwd W, exit 0. At 64/128/1024 comma-first lines, final full Decoder probes and decoded bytes are zero; the diagnostic intermediate control performs 64/128/1024 probes over 20,064/76,992/4,744,704 bytes.
- `final-line-search`: `go test -json -count=1 -overlay E/_tools/line-search-final-overlay.json -run '^TestU3LineSearchWork$' .`, cwd W, exit 0. LF/CR/CRLF/mixed inputs at 1024/2048/4096/8192 lines and whole-suffix/8192-byte/bytewise callbacks retain monotonic progress. Saved maximum searched-bytes/input-bytes is 1.0, within the instrumented `3 * input_bytes` bound.
- `final-neutral-proof`: `go test -json -count=1 -overlay E/_tools/whitespace-overlay.json -run '^TestU3NeutralGrowth$' .`, cwd W, exit 0. For 8192/16384/32768 bytewise neutral bytes, final whitespace visits are 24,580/49,156/98,308, allocations 45/52/54, and allocated bytes 75,936/163,536/322,000. Permanent allocation assertions and old complete/incomplete-unit boundaries also pass.

The Decoder/neutral-growth `entry` controls are frozen U3 intermediate diagnostics. The paired timing baseline is actual `2e0b34c6` production. They serve different comparisons and are not interchanged.

## Original baseline attempts and retained failures

These are completed historical baseline executions, separate from repaired-source U4 acceptance. `B/baseline-commands.json`, `clean-commands.json`, `integration-command.json`, the actual JSONL logs, and snapshot-input hashes retain their cwd/exits. Old ignored SDK sources under `dist/functional-audit-20261023-897b632d/release/verification-tools/go1.26.5-download/sdk/go/test` entered recursive discovery in the original checkout. They were preserved, not repaired or deleted.

| Baseline attempt | Actual command | Cwd | Exit / result |
| --- | --- | --- | --- |
| Original recursive unit | `go test -json -count=1 ./...` | `C:/Users/user/Downloads/cpa-plugin` | 1, ignored SDK negative-test discovery/build failures; root package passes |
| Original recursive vet | `go vet ./...` | Original checkout | 1, SDK mixed packages/relative imports and other negative-test diagnostics |
| Original recursive race | `go test -json -race -count=1 -timeout=30m ./...` | Original checkout | 1, SDK discovery/build failures plus the witness failure below; no `DATA RACE` report |
| Exact clean snapshot unit | `go test -json -count=1 ./...` | C | 0 |
| Exact clean snapshot vet | `go vet ./...` | C | 0 |
| Unchanged focused race repetition | `go test -json -race -count=5 -timeout=10m . -run '^TestFunctionalShutdownPreservesPayloadErrorAtBackpressure$'` | C | 0, five unchanged repetitions |
| Exact clean snapshot full race | `go test -json -race -count=1 -timeout=30m ./...` | C | 0 |
| Original-checkout actual integration | `make integration VERSION= DIST_DIR=B` | Original checkout | 0, 317.78 seconds; `CPA_FUNCTIONAL_EVIDENCE=B/native` |

The original race's `TestFunctionalShutdownPreservesPayloadErrorAtBackpressure/in-band` reports `rewrite-error output did not reach blocked emission` at its unchanged two-second witness deadline. That attempt is retained. The five clean-snapshot repetitions and later full clean race pass without changing that witness, deadline, input size, or production source. Full race and actual integration were sequential; mutable historical indexes do not override individual command or loaded-identity receipts.

Two historical baseline DLL identities remain distinct: `B/native/raw-number-loaded.json` records PID 10020 and SHA256 `d1bcd3b3ba8060cafed231e13cc7709c0f17aa9e5876b1520e3bcb28ac8580c0`; `B/verification-013f38ed5c/loaded-identities.json` records PID 14280 and SHA256 `de8be72f4e5f43b658366962e3a6ee516c5c4a1df1fb63ccd7d7a15d3bdc0470`. Their actual paths are the corresponding `cliproxy-pluginhost/pid-<PID>/cliproxy-plugin-model-mapper-<first 32 hash characters>.dll` paths, with canonical paths and saved tested copies recorded separately. Neither receipt proves one common DLL across those two baseline runs. The independent baseline membership/capture/registration checks belong to their own recorded identity.

Other retained attempts do not count as behavioral RED or final-source passes:

- U3's two confirmed review findings were established-SSE whitespace entering the neutral-whitespace fast path and repeated CR-only suffix searches. `review-disposition.json`, `established-sse-red` (exit 1), and `line-search-red` (exit 1) retain the failures. The final `!r.sse.started` guard and earliest-CR/LF search correction passed affected permanent/instrumented checks, the complete owner matrix, and subsequent review. No old assertion was relaxed.
- U3's final evidence retains a diagnostic Go source-generation/gofmt failure, exit 2, before measurements; only temporary input escaping was corrected. Its commit-stage receipt also preserves support-verifier failures with exits 1/1/2/1: shell-transport quoting, incorrect evidence-inventory exclusions, shell parsing before execution, and a local second-interpreter launch failure. The corrected read-only checks passed; no production/test change occurred during that commit stage.
- U4's `attempt-issues.json` retains entry disk-hash and blob-hash assertion failures caused by LF/CRLF attribution, an inline Python quoting SyntaxError, and `verification-audit-attempt-1` exit 1 from examining its own still-open receipt before `source_after` existed. Completed receipts, normalized source/Git blobs, and the corrected audit passed without changing source or rerunning to different conditions.
- Documentation-check preparation initially exited 1 because PyYAML was unavailable. A subsequent nested Python/Node invocation exited 1 when shell transport converted a JavaScript newline escape before validation. Direct stdin execution with `String.fromCharCode(10)` passed using installed Node `yaml`; installed Pandoc supplied the Markdown checks. A rendered-argv check then found literal backslashes in table-escaped regex pipes; moving those commands into a plaintext block preserved their exact selectors for rechecking. Neither preparation failure changed files, dependencies, or environment configuration.

All original failed receipts, raw logs, passing repeats, and prior evidence remain in their owned directories. No failed attempt is erased by a later success.

## Review coverage and record checks

U3's commit-stage receipt records completed specification, quality, and test reviews with no unresolved finding after corrections and re-review. Its supplied review completion is distinct from U4's independent owner review. `E/independent-review.json` records U4's three lenses against the complete diff and connected classifier/forwarder/fixture seams, followed by actual acceptance and evidence reconciliation.

| Requirements / examples | Independently checked result |
| --- | --- |
| R1-R4 / AE1-AE3 | Standalone and attached neutral whitespace, exact separators, arrays/scalars/prefixes, explicit expected bytes, every two-way including empty parts and bytewise partitions; complete containers/prefixes emit during Write, unresolved numbers retain their existing boundary/EOF policy. |
| R5-R8 / AE3, AE6, AE8 | Baseline-GREEN genuine SSE, established continuation/native priority, framing/BOM/DONE behavior, six-path whitelist/opaque token preservation, original ownership/size/allocation/lifecycle/error/close assertions and full old member sets. |
| R9, R15 / AE3, AE8 | Production scope is shared `stream_rewriter.go`; necessary boolean/cursors retain incremental scanning. Reclassification requires real SSE-field evidence and standard first-value Decoder `*json.SyntaxError`; `io.ErrUnexpectedEOF` waits and successful Decode retains raw-prefix priority. Write/Flush/Finish, prefix-plus-error and non-replay controls pass. `sseRewriter.Write` remains unchanged. |
| R10 / AE4 | Five-format application/json helper plus OpenAI header control, generic/Claude event-stream helper, and both separate RPC/envelope witnesses have distinct attribution. |
| R11 / AE5 | Same permanent native/Claude HTTP assertions fail on frozen baseline, then pass in both actual U4 focused repetitions and full same-library integration, with explicit wire/decoded events, route controls, timely output, and retained loaded identities. |
| R12-R13 / AE6 | Completed owner corrections/re-review/commit, independent U4 full matrix, all old benchmarks, paired explanation/rerun, hard gates, exact capture membership, and actual default/patch registration/C ABI are recorded. |
| R14 / AE7 | Not executed by U4; final-SHA CI, seven-platform builds/packages, authorized main/tag/Release delivery, downloaded asset checks, and merged-worktree cleanup remain U5. |

The documentation phase read both canonical documents completely and checked raw receipts/logs/samples rather than accepting only summaries. Standard JSON parsing recomputed paired medians and capture-key digests, verified the source/fixture/artifact hashes, and reconciled actual member names and multiplicity. `go tool test2json` converted the saved full-native log for comparison; no tests were executed by that conversion. Frontmatter and repository links are checked with installed parsers, and the document is self-reviewed, corrected, and re-reviewed against these primary inputs. `git diff --check` and task-only path checks govern the documentation commit. No full tests are repeated for this documentation-only change.

## U5 handoff and remaining limits

U5 must integrate only completed, conflict-checked commits after inspecting original-checkout state and rechecking version occupancy. It must verify the delivered final SHA's Ubuntu job actually runs `make integration`, then verify linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64, and freebsd/amd64 build/package jobs. Authorized main/tag/Release identities and downloaded archives must agree on source/version, root library/LICENSE, target build info, injected version, basename-only SHA256 checksums, GLIBC <=2.17, and macOS deployment target 12.0. None of those U5 actions is complete in this record.

Actual native/HTTP runtime coverage here is Windows/amd64 controlled-provider replay. Stock-provider natural chunking, paid live-provider calls, external typed SDK behavior, and seven-platform runtime loading were not tested. Build coverage and runtime coverage must remain separate. No publication, push, tag, PR, network-security scan/repair, CPA development, or source/test/dependency change belongs to this documentation phase.

U4 acceptance cleanup removed only its recorded loaded-shadow files, empty owned PID directories, and generated Python cache; its temporary integration canonical file was already absent. `E/cleanup.json` records no temporary cleanup remainder. Evidence, actual tested copies, and the unmerged U4 branch/worktree remain for U5. The documentation phase leaves existing artifacts and other worktrees untouched. U5 must remove only this run's clean, fully merged, unused worktrees/branches and disposable files after integration, preserving pre-existing state and reporting any cleanup remainder.
