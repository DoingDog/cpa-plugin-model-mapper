# Functional Stream Corrections: Verification Record

## Acceptance and inputs

U4 completed corrected-fixture acceptance on Windows/amd64 with Go 1.26.5 at combined commit `351a682ecd646f21094b7fb13299f43b36de6371`. The fixture SHA256 is `c815e9c82ace742d65d7e091cc039d0d4432e6ab47b38b6c1334747ddf3b1046`. All fixture-sensitive gates were executed again, including full same-library integration, native repetitions, and actual default/patch registration. Complete input comparisons support reuse of the unchanged root unit/vet/race, benchmark, packager, and compatibility results from initial combination `8b6dcbf2d87e7284f57192525e8836d77ed3ea3a`. This verification-only update changes no runtime input. Main owns the separate independent follow-up review receipt; U5 owns main integration, final-SHA CI, publication, downloaded release assets, and merged-worktree cleanup.

The [canonical specification](../specs/2026-10-23-functional-stream-corrections-design.md) defines R1-R17 and AE1-AE8. The [implementation plan](../plans/2026-10-23-functional-stream-corrections-implementation.md) defines the acceptance and ownership boundaries.

| Input | Commit |
| --- | --- |
| Original main and frozen production baseline | `908db0a416cd0ef8db734e04114491700a941c8a` |
| Canonical documents | `2afb029ecd74f1f9ef7a0f0a733f8e28c649ca4f` |
| Completed C1 | `72b31f1b29eeb09b49e15cfc11cd048b2a115c2f` |
| Completed C3, descended from C1 | `5f92f524970811255737605b5f7c3d541104e342` |
| Completed C2 | `774d253367dda46c42c651418c7883e72096e743` |
| Initial U4 combination | `8b6dcbf2d87e7284f57192525e8836d77ed3ea3a` |
| Initial U4 verification record | `6cdab82715116e92827a529b00ecb2e92307b1b1` |
| C1 shared identity helper prerequisite | `804ca2a1c44d2e0323f311e135ea54bf469d87a6` |
| Completed C2 helper consumer, including prerequisite merge `d8db4c6` | `699cd06190f80f46f0b27395d5e8855ee8a3b571` |
| Completed C3 helper consumer, including prerequisite merge `2cb70fa` | `cc85b757379777cda6e6e467d8e59ecf26583130` |
| U4 C2 correction merge | `3557625b15a197ee6a8078ee26b678b2edca756b` |
| Current U4 combination after C3 correction merge | `351a682ecd646f21094b7fb13299f43b36de6371` |

The independent U4 worktree is `C:/Users/user/Downloads/cpa-plugin/.claude/worktrees/functional-integration-897b632d`, branch `verify/functional-streams-897b632d`. The initial combination was created from C3 and merged C2 after actual scope/ancestry checks and focused tests. Corrected-fixture acceptance started clean at `6cdab82`, inspected all three correction receipts and actual diffs, then merged only the two completed consumer refs without conflict. The original C1 repair and its helper prerequisite each occur once in ancestry. No repair implementation was performed by U4.

Local evidence is retained under `C:/Users/user/Downloads/cpa-plugin/dist/functional-audit-20261023-897b632d/integration/`. The fresh `review-corrected/` subtree contains this round's receipts, snapshots, artifacts, exact capture comparisons, and `evidence-index.json`. Initial evidence, DLLs, packages, test-first observations, and successful/failed attempts remain unchanged outside that subtree. These are ignored local evidence, separate from the tracked record.

## Corrected-fixture acceptance at 351a682

### Scope, equivalence, and reused gates

The complete difference from `6cdab82` before this record update is one file, `.github/scripts/testdata/cpa-functional-regression_test.go`, with 24 insertions and 51 deletions. Relative to `8b6dcbf`, the only additional path is the initial verification record already committed as `6cdab82`. `review-corrected/diff-6cdab82.patch` and `diff-8b6dcbf.patch` retain those exact pre-record-update comparisons.

C1 supplied `eRequireLoadedLibrary(t, host, evidenceName)` under plan line 158. The three new tests now call it immediately after `eNativeHost`. Standard `go/parser` and `go/ast` verified each original Windows guard against the helper body with its original evidence argument substituted. Reversing precisely the three call replacements and removing the helper reproduces the entire initial fixture byte-for-byte, including CRLF checkout bytes. Every other function, imports, comments, assertions, evidence fields, limits, and deadlines are preserved. `TestModelMapperFunctionalNativeSSEBoundaries` and its existing identity block remain byte-identical. Normalized initial/current input is independently gofmt-clean. `fixture-ast.json` and `fixture-ast.log` retain the command, temporary diagnostic path, and complete declaration/caller checks.

`input-impact-and-reuse.json` compares all 85 tracked paths against both initial refs using Git blobs and actual checkout hashes, recording the expected fixture and verification-record differences separately. All ten production files, eleven root test files, module files, Makefile, script files, CI, and other build/runtime inputs are unchanged. Current `go list` production, cgo, test, embed, other-language file lists, and imports equal the initial `build-input-location.json` lists. `package-discovery.log` confirms recursive discovery still selects only the original root package. The sole fixture is outside root compilation and root runtime-file reads. Every initial gate receipt's complete input map differs only at that fixture. These checks support reuse of full unit, vet, full race, changed-path five-repeat race, benchmarks, release packager, compatibility checker, and their registration/C ABI observations. No performance comparison is inferred. Smoke-helper/overlay/native checks consume the changed fixture and were executed again.

### Commands and exact runtime membership

All commands used the assigned worktree, `CGO_ENABLED=1`, `GOWORK=off`, and unchanged build policy. Full race was reused on identical actual inputs; no race execution overlapped this integration.

| Current gate | Command or selector | Observed result |
| --- | --- | --- |
| Full integration | `make integration VERSION= DIST_DIR=C:/Users/user/Downloads/cpa-plugin/dist/functional-audit-20261023-897b632d/integration/review-corrected/default-dist` | Exit 0, 315.31 seconds; fresh `CPA_FUNCTIONAL_EVIDENCE=.../review-corrected/full-native`; `full-integration.json` and `full-native/cpa-overlay.log` |
| Affected smoke helper/overlay suite | `go test -json -count=1 .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go` | Exit 0; `TestPrepareFunctionalCPAOverlayEvidencePath` ran and passed; `script-smoke.json` |
| Native repetitions | Current sole-fixture test binary, `-test.run '^TestModelMapperFunctional(ResponsesRawPrettyJSON\|NativeFlushErrorAtBackpressure\|RawNumberContinuation\|ReconfigureReload)$' -test.timeout 120s` | Two additional executions, both exit 0; `native-repeat-1.json`, `native-repeat-2.json` |
| Actual default registration | Current-fixture logging-only overlay; existing `ReconfigureReload` and C1 | Exit 0; actual `0.0.0-dev`; `default-registration.json` |
| Patch build/package | `make package VERSION=0.5.16 GOOS=windows GOARCH=amd64 DIST_DIR=C:/Users/user/Downloads/cpa-plugin/dist/functional-audit-20261023-897b632d/integration/review-corrected/patch-dist` | Exit 0; `patch-package.json` |
| Actual patch registration | Current-fixture overlay changes only the existing version expectation plus logging; existing `ReconfigureReload` and C1 | Exit 0; actual `0.5.16`; `patch-registration.json` |
| Default package | Existing `package-release.go` single-library/archive/checksum entry, using the integration DLL without rebuilding it | Exit 0; `default-package.json` |
| Scope/input/membership/identity checks | Standard Go parser/AST, `go list`, `go tool test2json`, standard JSON/ZIP decoding, existing PE export inspection | Exit 0; corresponding command receipts and snapshots in `review-corrected/` |

`command-rollup.json` retains full arguments, working directories, environment, timestamps, exits, and log paths; each command receipt retains its actual complete source/build-input map. Native compile receipts bind the current fixture and overlays to the exact binaries. No permanent driver, fixture, registration option, default-version constant, dependency, or build-policy change was introduced.

`runtime-membership.json` checks exact names and multiplicity. This full fixture ran the same 24,149 Test/subtest RUN/PASS members as the initial successful composition, exactly 26 old functional top-level tests, three new tests, and two bridge controls. No required native test was skipped, missing, duplicated, failed, or selected zero times. Each of the three new tests ran once in this full integration and twice more on byte-identical copies of its actual default DLL. C1 additionally ran during default and patch registration checks. The helper actually produced `responses-raw-pretty-loaded.json`, `raw-number-loaded.json`, and all three `flush-loaded-<mode>.json` files with exactly `pid`, `path`, `sha256`, and `canonical` fields. The original boundary checks also produced their established evidence filenames and fields.

All four capture arrays were decoded with duplicate-object-key rejection and checked record-by-record against U1's frozen exact key sets. Their 1,536 / 1,800 / 1,440 / 1,680 keys respectively match `native-binary-captures.json`, `mixed-native-captures.json`, `mixed-binary-captures.json`, and `mixed-shape-controls.json`, with empty missing/unknown/duplicate/error lists and one call per record. Counts supplement the saved exact key comparisons. The complete real translator/validator unit ran again at 16,842,937 bytes with SHA256 `649180ca25a1be756800b35264d48c7c338e4d5ff94c6a3eb90c9d88bf17b9ae`; both mapped and unmapped large HTTP cases passed. No old limit or timeout was changed.

The ordinary smoke-helper suite again skipped only `TestWriteSmokeConfigTightensPermissions` on Windows and `TestCPAPluginIntegration` without its explicit environment. Actual `make integration` executed the latter and the complete native fixture without a required skip. U4 support failures changed no source or artifacts: an input-comparison diagnostic applied text newline normalization to binary `logo.png`, an inline evidence-JSON update failed with a Python Unicode-escape syntax error, and a long inline receipt/index command failed shell parsing before Python execution. The corrected byte-first/text-checkout comparison passed for every tracked path; the JSON update used a precise file edit, and finalization used an isolated script with a short invocation. `support-attempts.json` preserves these failures and the non-mutating worktree-attachment limitation. They supply no functional RED. All current functional test, build, integration, and registration commands exited 0.

### Current identities, actual loading, registration, and package bytes

`review-corrected/combined-identity.json` retains current source snapshots and Git blobs, fixture/overlay/test-binary hashes, CPA identity, build policy, registered artifacts, and package checks. Its source-manifest digest is `dbb9f1a8fe9b84db3dd8d437689cdc520d83cfc622eaf7ea4b9069e7139a33e9`.

| Current input/artifact | SHA256 |
| --- | --- |
| Sole corrected native fixture | `c815e9c82ace742d65d7e091cc039d0d4432e6ab47b38b6c1334747ddf3b1046` |
| Default built, canonical, and loaded DLL | `e124252fc9939d6745a89ab1edf56e8d4fbf5ffdb3f19929beaef463069bc6e2` |
| Patch built, canonical, and loaded DLL | `ed9659959065b8dbd27a3dd55ba194b412e4064bd419a5d8f4b3443d23699537` |
| Current sole-fixture native test binary | `79db8c79016f303eb5b24cc1093b5f1650722d199f985bcbfc404ee9d99c9c11` |
| Current default-registration test binary | `e61f75eee4cff74ee3510c37f833c84dc1031cece62807984851a6a23948b0ac` |
| Current patch-registration test binary | `35cd2ae62a33c0c69171efaca6bdbbe2c937569c51d05c919cb8df93852c8dc3` |
| Actual CPA server binary | `1baf675218159704eb364ee28fee7c9b7852bf3101656cb9f5daf7ce7345df12` |
| Current default ZIP, 2,185,927 bytes | `4323913ee5e50d524a9bbf3b05940b3d8f63b50fdf24368e188b50c80088f641` |
| Current patch ZIP, 2,185,912 bytes | `a09cdca681b71235ddb6ef633550cd4cb2dd71c2a18cbf714a685a2ad6d60cb8` |

The fresh default and patch DLL bytes equal their initial counterparts. This does not substitute for rerunning the changed fixture: all current native/integration/registration checks above actually executed. The default DLL was not rebuilt after full integration; both focused repetitions consumed copies of that actual artifact. The patch used the unchanged `-X main.pluginVersion=0.5.16` injection. Each current ZIP's archive-root DLL equals the actual registered artifact, `LICENSE` equals the repository file, and its checksum line names only the archive basename. Initial ZIPs remain separate and are not claimed to have the current archive hashes. Both current DLLs expose exactly the original four C ABI exports.

| Current execution | Child PID | Loaded shadow filename under `C:/Users/user/AppData/Local/Temp/cliproxy-pluginhost/pid-<PID>/` |
| --- | --- | --- |
| Full native fixture | 10048 | `cliproxy-plugin-model-mapper-e124252fc9939d6745a89ab1edf56e8d.dll` |
| Native repetition 1 | 17196 | `cliproxy-plugin-model-mapper-e124252fc9939d6745a89ab1edf56e8d.dll` |
| Native repetition 2 | 24164 | `cliproxy-plugin-model-mapper-e124252fc9939d6745a89ab1edf56e8d.dll` |
| Default registration/C1 | 8452 | `cliproxy-plugin-model-mapper-e124252fc9939d6745a89ab1edf56e8d.dll` |
| Patch registration/C1 | 10028 | `cliproxy-plugin-model-mapper-ed9659959065b8dbd27a3dd55ba194b4.dll` |

The fixture checked each live DLL handle and loaded/canonical byte equality. `loaded-identities.json` retains every exact canonical/shadow path and PID plus independently rechecked post-execution shadow-byte hashes. Full integration's parent-lifetime canonical copy was removed by its existing test cleanup; it was compared while live. Repetition and registration canonical copies remain in their separate evidence directories. CPA remains `v7.2.152`, Sum `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`, with a clean pinned server checkout at `c76dfd4e0edabab9000628b1560ab8ab379eadb8` and that actual server revision in build metadata.

Both fresh plugin DLLs still report VCS revision `908db0a416cd0ef8db734e04114491700a941c8a`, `vcs.modified=true`, and `v0.5.15+dirty`, as the initial DLLs did. The cause remains unestablished. Current `go list` package/module location, all ten production-input hashes, command working directories, worktree Git SHA `351a682`, and actual passing repaired behavior identify this round's tested source. No VCS stamp is used as source proof, no build-policy change or system investigation was performed, and final-SHA clean-checkout CI remains U5's responsibility.

Current logging-only/version overlays produced three actual registration observations per artifact, including reconfigure and reload. Schema 1, all metadata and capability fields, static scope, input/output formats `openai`, `openai-response`, `claude`, `gemini`, `interactions`, and the original five config fields match the initial actual observations after normalizing only process addresses. Default version is `0.0.0-dev`; patch version is `0.5.16`. `actual-registration.json` retains the complete observations. Read-only local/remote tag and Release checks at 11:36 UTC found latest Release `v0.5.15` and no `v0.5.16` occupancy. U5 must recheck immediately before publication.

### Review boundary

The completed independent initial review is retained at `C:/Users/user/Downloads/cpa-plugin/dist/functional-audit-20261023-897b632d/independent-review/initial/review.json`. Its verdict was `Ready with fixes`, with only finding #1 confirmed: three task-added copies of the DLL identity check. Its immutable review refs and their binding to the initial combination remain in `main-review-scope-binding.json`. That receipt does not review the later helper corrections.

The three correction owners completed passing before/after characterization, affected checks, scope/preservation checks, owner specification/quality re-reviews, and clean task-only commits, with receipts under `review-correction/C1`, `C2`, and `C3`. The original functional TDD evidence below remains applicable and untouched; this behavior-preserving helper extraction introduces no new functional RED claim. U4 reviewed the correction, `eNativeHost`/`gCPALoadNative`/`hCPASaveEvidence`, the three callers, the protected boundary block, overlay/registration seams, and this updated record. Its AST/inverse-byte proof and actual same-library checks found no remaining correction within U4's scope. `review-corrected/review-receipt.json` records that owner/integration review and record re-review. Main owns the separately dispatched independent follow-up receipt; this record supplies no verdict for that review.

## Initial commands and observed results

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

## Initial preserved source and runtime membership

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

## Initial source, artifacts, and actual loading

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

## Initial specification and connected-code review

U4 performed an initial integration review independently of the three coding owners, covering the entire baseline-to-combined diff and connected callers, followed by a verification-record correction/re-review. That review identified no runtime correction. The subsequent formal independent review confirmed the task-added repetition as finding #1; its later owner corrections and current acceptance are recorded above. The initial `review-receipt.json` retains its original reviewed scope and observations and is distinct from both formal independent review receipts.

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

After main reconciles the independent follow-up receipt and U4 handoff, U5 can integrate the verified branch into original main after checking current state. Recheck `0.5.16` occupancy before publication; if it changes, rebuild, register, and package the newly selected patch through the existing injection path. Check final-SHA Ubuntu execution of `make integration`, all seven build/package jobs, tag Release, downloaded archive/checksum/version identity, Linux GLIBC <=2.17, and macOS deployment target 12.0. This U4 record authorizes no outward operation.

Actual runtime loading in this record covers Windows/amd64. Controlled providers supply the supported SDK/ABI witnesses; stock paid-provider production of C1's pretty input remains unproven. Numeric continuation is covered at the established raw-input seams, with unresolved numeric tails allowed to wait for a boundary or EOF. This work performed no publication, PR creation, network-security scan/repair, CPA development, or unrelated source change.

At 08:48 UTC, original main remained `908db0a`, with only the initial untracked `.claude/` visible. U4 preserved existing user files, ignored artifacts, and all owner worktrees. The formerly busy `dist/baseline-validation-20261023-4d02c37a1f6b/evidence-retry/cpa-77b51d043864851f/cpa-v7.2.152/internal/pluginhost` and its recorded top-level parent were absent; U4 made no deletion attempt. The U4 branch/worktree and its ignored artifacts remain for U5. Remove only task-owned, clean, fully merged, unused worktrees/branches after main integration and successful delivery.

Corrected-fixture cleanup at 11:57 UTC removed only this round's five hash-verified shadow DLLs and their empty PID directories and its four temporary Python drivers. It also confirmed that the OS-temporary Go diagnostic directory had already been removed. Exact driver source snapshots remain under `review-corrected/diagnostic-snapshots/`, and the Go diagnostic source remains a non-discoverable `.sourcebytes` evidence input. `review-corrected/cleanup.json` records no temporary cleanup remainder. Current evidence, native test binaries, default/patch libraries and packages, and the unmerged U4 branch/worktree remain for the handoff. Initial evidence, user files, and other worktrees/branches were not deleted.
