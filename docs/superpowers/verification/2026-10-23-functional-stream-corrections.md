# Functional Stream Corrections: Verification Record

## Acceptance and inputs

U4 completed the local acceptance gates for C1, C2, and C3 on Windows/amd64 with Go 1.26.5. The combined runtime inputs are commit `8b6dcbf2d87e7284f57192525e8836d77ed3ea3a`. This verification-only commit preserves those production, test, fixture, and build inputs. U5 owns main integration, final-SHA CI, publication, downloaded release assets, and merged-worktree cleanup.

The [canonical specification](../specs/2026-10-23-functional-stream-corrections-design.md) defines R1-R17 and AE1-AE8. The [implementation plan](../plans/2026-10-23-functional-stream-corrections-implementation.md) defines the acceptance and ownership boundaries.

| Input | Commit |
| --- | --- |
| Original main and frozen production baseline | `908db0a416cd0ef8db734e04114491700a941c8a` |
| Canonical documents | `2afb029ecd74f1f9ef7a0f0a733f8e28c649ca4f` |
| Completed C1 | `72b31f1b29eeb09b49e15cfc11cd048b2a115c2f` |
| Completed C3, descended from C1 | `5f92f524970811255737605b5f7c3d541104e342` |
| Completed C2 | `774d253367dda46c42c651418c7883e72096e743` |
| U4 combination | `8b6dcbf2d87e7284f57192525e8836d77ed3ea3a` |

The independent U4 worktree is `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-integration-897b632d`, branch `verify/functional-streams-897b632d`. It was created from C3, then merged C2 without conflict after checking actual paths, ancestry, source ranges, fixture anchors, and unchanged imports. C1 occurs once in the ancestry. The merge's focused C1/C2/C3 regressions passed before the merge commit.

Local evidence is retained under `C:/Users/user/Downloads/cpa-plugin/dist/functional-audit-20261023-897b632d/integration/`. This ignored directory contains command logs and receipts, source snapshots, actual artifacts, captures, review receipts, and `evidence-index.json`. It is local evidence, separate from the tracked record.

## Commands and observed results

Acceptance commands used the U4 worktree, `CGO_ENABLED=1`, and `GOWORK=off`. Full race finished before full integration started. Existing internal deadlines, input sizes, and allocation ceilings were retained.

| Gate | Command or selector | Result and evidence |
| --- | --- | --- |
| Full unit | `go test -json -count=1 ./...` | Exit 0; `full-unit.log`, 4,280 individual RUN/PASS events, no skip or failure |
| Vet | `go vet ./...` | Exit 0, no diagnostics; `full-vet.log` |
| Full race | `go test -json -race -count=1 -timeout=30m ./...` | Exit 0, no race report, all 4,280 cases passed; `full-race.log`, 987.39 seconds |
| Release packager | `go test -json -count=1 .github/scripts/package-release.go .github/scripts/package-release_test.go` | Exit 0; two Windows skips listed below; `script-package.log` |
| Compatibility checker | `go test -json -count=1 .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go` | Exit 0, no skip; `script-compat.log` |
| Smoke helper | `go test -json -count=1 .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go` | Exit 0; permission and ordinary integration skips listed below; `script-smoke.log` |
| Changed-path repetition | `go test -json -race -count=5 -timeout=10m . -run <changed-path selector>` | Exit 0; all 4,170 selected events passed; `changed-race-five.json` contains the exact selector |
| Existing benchmarks | `go test . -run '^$' -bench . -benchtime=1x -count=1 -benchmem` | Exit 0, correctness prechecks passed; all 29 old benchmark declarations observed individually in `benchmark-membership.json`; one sample supplies no speed-equivalence estimate |
| Full native integration | `make integration VERSION= DIST_DIR=C:/Users/user/Downloads/cpa-plugin/dist/functional-audit-20261023-897b632d/integration/default-dist` | Exit 0 in 313.04 seconds; `full-integration.log` and `full-native/cpa-overlay.log` |
| Repeated native regressions | Existing sole-fixture binary, `-test.run '^TestModelMapperFunctional(ResponsesRawPrettyJSON\|NativeFlushErrorAtBackpressure\|RawNumberContinuation\|ReconfigureReload)$' -test.timeout 120s` | Two additional executions, each exit 0 without skip; `native-repeat-1.log`, `native-repeat-2.log` |
| Actual default registration | Existing `TestModelMapperFunctionalReconfigureReload`, with logging-only verification overlay | Exit 0; `default-registration.log` records actual `0.0.0-dev` metadata |
| Patch build/package | `make package VERSION=0.5.16 GOOS=windows GOARCH=amd64 DIST_DIR=C:/Users/user/Downloads/cpa-plugin/dist/functional-audit-20261023-897b632d/integration/patch-dist` | Exit 0; `patch-package.log` |
| Actual patch registration | Existing `ReconfigureReload` and `ResponsesRawPrettyJSON`, verification-only version assertion/logging overlay | Exit 0; `patch-registration.log` records actual `0.5.16` metadata and loaded DLL |

The changed-path selector includes both new C1 tests, all five C3 root tests, C2 flush and payload tests, existing shutdown/primary/cleanup/emit-failure/close controls, and the unchanged generic pretty-JSON oracle. `command-rollup.json` records every full command, working directory, environment, timestamps, exit, and log path. Each original command receipt additionally records every tracked Go/build-input hash.

The following named registration/C ABI tests each produced RUN and PASS in both full unit and full race:

- `TestPluginRegistrationMetadataAndConfigFields`
- `TestPluginRegisterKeepsSchemaOneForNewerHost`
- `TestCIntLengthBounds`
- `TestPluginResponseLengthBounds`
- `TestCopyPluginRequestRejectsNullWithLength`
- `TestPluginCallClearsResponseWhenMethodNil`

`CPA_FUNCTIONAL_EVIDENCE` for full integration was the fresh absolute `.../integration/full-native` directory. The integration entry and its existing 600-second native timeout were unchanged. The script suites were invoked by their explicit file pairs.

## Preserved source and runtime membership

`source-membership.json` compares U1's frozen source bytes and manifests with the combined tree. Standard ast-grep 0.51.0 queries checked all 456 old Test/Benchmark declarations individually. Every declaration's normalized source hash matched. All 295 subtest calls, 1,478 table-literal spans, and 776 loop spans retained their exact original source and multiplicity. This preserves their assertion text, expected values, limits, sizes, deadlines, and generated-case inputs. Existing helpers and fixture imports retained their bytes; additions occur only at the three assigned fixture anchors and in the owned root regression locations.

Standard `go tool test2json` and JSON decoding checked actual execution membership. All 341 old root test declarations ran and passed. The saved historical root log's 3,050 expanded names and historical native log's 17,508 expanded names are all present in the final execution sets. Current frozen declarations and source spans cover subsequent baseline additions. `runtime-membership.json` stores the complete compared name sets, individual events, and empty missing/duplicate/unknown lists.

The full native overlay executed exactly the 26 old functional top-level tests, three new functional tests, and two bridge controls. All 24,149 test/subtest events settled as PASS, with no required native skip, duplicate, missing case, or failure. Each new native regression therefore executed three times on the same default DLL: once in full integration and twice in focused repetition. The patch-specific C1 execution is recorded separately.

The four final capture files were decoded with standard JSON duplicate-object-key rejection. Every record's identity key was compared with U1's frozen exact keys. Record errors were empty and call counts were checked. The full fixture also executed its existing semantic, wire-byte, full-response, and request assertions.

| Final capture file | Exact keys retained | Missing / unknown / duplicate |
| --- | --- | --- |
| `native-binary-captures.json` | 1,536 | None |
| `mixed-native-captures.json` | 1,800 | None |
| `mixed-binary-captures.json` | 1,440 | None |
| `mixed-shape-controls.json` | 1,680 | None |

`TestModelMapperFunctionalCompatLifecycle/large=true/large-host-units` passed with the complete real translator/validator unit of 16,842,937 bytes, SHA256 `649180ca25a1be756800b35264d48c7c338e4d5ff94c6a3eb90c9d88bf17b9ae`. Both mapped and unmapped large HTTP paths passed. The original `maxPendingStreamBytes = 16 << 20`, complete-unit assertions, allocation ceilings, and all old oracle bodies are unchanged.

## Source, artifacts, and actual loading

`combined-identity.json` records the ten production files, module files, Makefile, integration helpers, sole fixture, Git blob identities, actual worktree hashes, LF-normalized hashes, and retained `.sourcebytes` snapshots. Its canonical input-map digest is `183206574c22821431a677c1cb7074aff56a1c1ae61e57debfd7cc19cce8320c`. Every command receipt's source/build-input map matched the full-unit map.

| Runtime input | Actual SHA256 |
| --- | --- |
| Combined `stream_rewriter.go` | `c136d4da7a1c540f703da764d63cf6deb761082b1529a108b6f1fb1c7383739c` |
| Combined `executor_stream.go` | `128164bc9c570feea1e9f14970fbfee35c211d415bc3fd64650a8743b4f161e2` |
| Sole native fixture | `39526afc5adf7bcfdadf43b785ca9dc36fe3c4539bc43eaa86facf7c114e2ca4` |
| Default built, canonical, and loaded DLL | `e124252fc9939d6745a89ab1edf56e8d4fbf5ffdb3f19929beaef463069bc6e2` |
| Injected patch built, canonical, and loaded DLL | `ed9659959065b8dbd27a3dd55ba194b412e4064bd419a5d8f4b3443d23699537` |
| Actual CPA server binary | `1baf675218159704eb364ee28fee7c9b7852bf3101656cb9f5daf7ce7345df12` |
| Sole-fixture focused native test binary | `4553b565dd0ffadbb0ea06fae8fc0e8c50719747d1863ad09cd0dc1c6f254957` |

The plugin build policy was the unchanged Makefile policy: `-trimpath -buildmode=c-shared -ldflags='-s -w '`. The patch added only `-X main.pluginVersion=0.5.16`. Full integration built the final default artifact first; both focused repetitions then consumed byte-identical copies of that actual artifact. There was no focused/full artifact-hash discrepancy to reconcile.

CPA's module version is `v7.2.152`, Sum `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`. The actual server build reports revision `c76dfd4e0edabab9000628b1560ab8ab379eadb8` and `vcs.modified=false`. Native tests used the existing module-copy overlay helper and the sole permanent fixture. Overlay and copied test-binary hashes are retained in `combined-identity.json`.

The DLLs' own Go VCS stamps report original-main revision `908db0a416cd0ef8db734e04114491700a941c8a`, `vcs.modified=true`, and module version `v0.5.15+dirty`. The cause of that stamping discrepancy was not established. `build-input-location.json` independently records the actual package/module directory, selected ten production files, and worktree Git HEAD `8b6dcbf`; the captured source hashes and three repaired native behaviors identify the tested combined inputs. The VCS stamp is not used as proof of tested source identity. U5 must rebuild and check its final-SHA release artifacts.

Each native regression inspected a live Windows DLL handle and shadow path, compared loaded bytes with the canonical library, and saved PID/path/hash evidence. `loaded-identities.json` retains the exact records.

| Execution | Child PID | Observed loaded shadow path |
| --- | --- | --- |
| Full fixture | 10536 | `C:/Users/user/AppData/Local/Temp/cliproxy-pluginhost/pid-10536/cliproxy-plugin-model-mapper-e124252fc9939d6745a89ab1edf56e8d.dll` |
| Focused repetition 1 | 36420 | `C:/Users/user/AppData/Local/Temp/cliproxy-pluginhost/pid-36420/cliproxy-plugin-model-mapper-e124252fc9939d6745a89ab1edf56e8d.dll` |
| Focused repetition 2 | 32984 | `C:/Users/user/AppData/Local/Temp/cliproxy-pluginhost/pid-32984/cliproxy-plugin-model-mapper-e124252fc9939d6745a89ab1edf56e8d.dll` |
| Patch registration/C1 | 11928 | `C:/Users/user/AppData/Local/Temp/cliproxy-pluginhost/pid-11928/cliproxy-plugin-model-mapper-ed9659959065b8dbd27a3dd55ba194b4.dll` |

Actual registration logs report default `Metadata.Version=0.0.0-dev` and separate patch `Metadata.Version=0.5.16`, schema 1, model router and executor, static model scope, and input/output formats `openai`, `openai-response`, `claude`, `gemini`, `interactions`. The config fields remain `global_rules`, `claude_messages_rules`, `codex_responses_rules`, `openai_completions_rules`, `rules_stack_mode`. All other capability entries retain their existing values. Existing reconfigure/unload/reload and five-format execution assertions passed. `actual-registration.json` preserves all three actual registration observations per artifact. Verification overlays only changed the existing patch version assertion and added logging; the permanent fixture and default constant remain unchanged.

Existing `objdump` PE inspection lists exactly `cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`, and `cliproxyPluginShutdown` for both DLLs. Existing C ABI tests and actual native loading exercised their ownership/lifetime behavior.

Read-only remote release/tag checks at 08:25 UTC found latest release `v0.5.15` and no local tag, remote tag, or Release occupying `v0.5.16`. The selected candidate is `0.5.16`. Existing packager output was checked with standard ZIP support: archive-root DLL bytes equal the registered artifact, `LICENSE` bytes equal the repository file, and the SHA256 line names only the archive basename.

| Local archive | SHA256 |
| --- | --- |
| `model-mapper_0.0.0-dev_windows_amd64.zip` | `cebb6b3ad2af15cb49d6a996e7fc289b2abdac6e2714cfa45cb238608bb98889` |
| `model-mapper_0.5.16_windows_amd64.zip` | `a7171ce3deeed797b6768b01c191811a5fcdb96a56acce7db01307d24d019634` |

## Test-first evidence and failed attempts

Owner receipts retain exact commands, source/test/fixture identities, intended assertion failures, controls, subsequent successful executions, and owner specification/quality re-reviews. U4 inspected these receipts and independently executed the combined acceptance matrix.

| Repair | Retained functional RED and GREEN evidence |
| --- | --- |
| C1 | `implementation/C1/owner-evidence.json`: `red-root`, `red-native`, final `red-root-final-retry`; repaired final root/native/owner checks passed. Actual WS completion, one physical synthetic data line, full output, normal close, HTTP, disabled/unmapped/direct controls passed. |
| C2 | `implementation/C2/owner-verification.json`: independent `red-root-flush`, `red-native-flush`, `red-root-payload`, `red-root-final-assertions`, and `red-final-root-baseline-overlay`; repaired native and final five-repeat race passed. R8 uses a real 16 MiB pending-limit error with complete scalar prefix, natural and earlier-primary controls. Its original two-second waits are unchanged. |
| C3 | `implementation/C3/owner-evidence.json` and `verification-rollup.json`: compatible frozen-baseline and completed-C1 root failures, identical native C1-DLL split failures, final reviewed baseline/C1 failures; repaired root/native and repeated checks passed. Both numeric parser paths, eight numeric lexemes, exact separators, immediate containers, and all five application/json helper formats were exercised. |

The complete failed-command receipts are retained in `failed-attempts.json`, including all owner attempts. Their classifications remain distinct:

- C1's initial `green-root` attempt failed six new arbitrary metadata-partition expectations outside the established native-unit scope. The owner corrected only those new expectations. `red-root-final` failed cgo setup with a snapshot overlay; it supplies no functional RED. The corrected `red-root-final-retry` reached the target assertions.
- C2's `focused-race-five`, `focused-race-five-preencoded`, and `focused-race-payload-staged-once` failed new-witness setup timing. The owner replaced that witness with the staged supported scalar/SSE input while retaining the 16 MiB limit and original two-second deadlines. The final witness's own baseline assertion failed for the wrong terminal reason and its fixed five-repeat race passed. The receipt also preserves an unsuccessful Python commit-message setup attempt that made no file change.
- C3's first `red-root-baseline` attempt failed compilation because a frozen one-result `frameRawJSON` met C1's two-result auxiliary test. The compatible overlay excluded that incompatible C1-only check; C3's unchanged target assertions then failed functionally. Compatible baseline, C1-base, final, and reviewed failing runs all remain in the receipt.
- U4's initial Python launch of the installed `ast-grep` command lacked its Windows Node-wrapper path and failed before parsing. Calling the existing installed wrapper through Node passed the query self-check and full comparison. U4's functional test/build/integration commands all exited 0.

The canonical baseline record also retains the original recursive discovery failures caused by pre-existing ignored Go diagnostics under `dist/`, and the first integration attempt's outer ten-minute timeout while race was active. Later unchanged, sequential baseline checks passed. U4 used those as history and ran the repaired-source matrix in its clean worktree.

Windows skipped `TestWriteChecksumFileAtomicallySetsMode`, `TestRunRejectsAliasedOutputsThroughSymlinkThenParent`, and `TestWriteSmokeConfigTightensPermissions`. The ordinary helper suite skipped `TestCPAPluginIntegration` because it lacked the explicit integration environment; the actual `make integration` invocation executed that test and the complete native fixture successfully. Full unit, full race, repeated native regressions, and full native integration had no skipped required case.

## Specification and connected-code review

U4 performed the first combined-source review independently of the three coding owners. The review covered the entire baseline-to-combined diff and connected callers, followed by a verification-record correction/re-review. No confirmed in-scope code or test correction remained. `review-receipt.json` records the reviewed scope, requirements, evidence, and outcome.

| Requirements / examples | Combined review and observed acceptance |
| --- | --- |
| R1-R4 / AE1-AE2 | Compaction occurs only at Responses synthetic raw framing; both callers check its error and retain completed prefixes. Generic/native SSE and other-format oracles remain byte-identical. Lexical, whitelist, mutation-ownership, actual WS and HTTP cases passed. |
| R5-R8 / AE3-AE5 | Both proven error branches select only a non-nil wrapped error before callbacks, with `completed=false`. Existing first-terminal selection, outputFinished timing, error joins, failed-emit non-replay, normal/forced close ordering, close-once, and failed-close remedy remain intact and passed. R8's independent behavioral gate was satisfied before its owner edit. |
| R9-R12 / AE6-AE7 | Both local-validity and Decoder paths retain only an unresolved trailing number at non-EOF. Existing pending storage/cursors retain exact separators and completed prefixes. All `splitJSONValues` consumers, framed scalar accounting, EOF/Flush/Finish, SSE/native precedence, large units, immediate containers, and ownership controls were checked. |
| R13-R14 | Request/response whitelist and changed-body header code, config/routing/DSL, dependencies, registration/ABI, Makefile/workflow, sole integration entry, old helpers, and imports are unchanged. Full old-oracle and runtime membership checks passed. |
| R15 | All three completed, task-only author commits and their owner correction/re-review receipts were inspected before combination. U4 accepted their actual diff and independently reviewed/tested the composition. |
| R16 / local AE8 | Full local matrix, same-library actual native repetitions/integration, exact captures, default/patch registration, exports, and local packages passed. |
| R17 / delivery AE8 | U5 remains responsible for version recheck, final main/tag SHA, Ubuntu integration, seven-platform builds/release assets, and precise cleanup. |

## Handoff and limits

U5 can integrate the verification branch into original main after checking current state. Recheck `0.5.16` occupancy before publication; if it changes, rebuild, register, and package the newly selected patch through the existing injection path. Check final-SHA Ubuntu execution of `make integration`, all seven build/package jobs, tag Release, downloaded archive/checksum/version identity, Linux GLIBC <=2.17, and macOS deployment target 12.0.

Actual runtime loading in this record covers Windows/amd64. Controlled providers supply the supported SDK/ABI witnesses; stock paid-provider production of C1's pretty input remains unproven. Numeric continuation is covered at the established raw-input seams, with unresolved numeric tails allowed to wait for a boundary or EOF. This work performed no publication, PR creation, network-security scan/repair, CPA development, or unrelated source change.

At 08:48 UTC, original main remained `908db0a`, with only the initial untracked `.claude/` visible. U4 preserved existing user files, ignored artifacts, and all owner worktrees. The formerly busy `dist/baseline-validation-20261023-4d02c37a1f6b/evidence-retry/cpa-77b51d043864851f/cpa-v7.2.152/internal/pluginhost` and its recorded top-level parent were absent; U4 made no deletion attempt. The U4 branch/worktree and its ignored artifacts remain for U5. Remove only task-owned, clean, fully merged, unused worktrees/branches after main integration and successful delivery.
