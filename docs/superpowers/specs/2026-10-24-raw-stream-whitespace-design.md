---
title: Raw Stream Whitespace
type: fix
date: 2026-10-24
baseline: 2e0b34c6429da77b0a1bc710de134833dd25f455
---

# Raw Stream Whitespace

## Goal and scope

Mapped raw JSON streams must preserve their ordered bytes and restore the Client-Requested Model when JSON whitespace crosses host-read boundaries. Correct the shared chunk classifier without adding a feature or a performance target. The [implementation plan](../plans/2026-10-24-raw-stream-whitespace-implementation.md) defines test-first repair, native verification, review, and delivery.

This specification owns R1-R14 and AE1-AE7. The documentation task creates only this specification and its companion plan. It changes no production code, tests, existing documentation, CPA source, dependencies, or release automation, and runs no tests, builds, or publication operations.

---

## Baseline and evidence

The source baseline is `2e0b34c6429da77b0a1bc710de134833dd25f455`. `go.mod` declares Go `1.26.0` and CPA `v7.2.152`. `Makefile` pins integration to CPA revision `c76dfd4e0edabab9000628b1560ab8ab379eadb8`; the existing overlay verifies module Sum `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`.

The supplied independent reproductions establish the following behavior. They are evidence inputs to this document, not new runs performed by the documentation owner.

| Layer | Input and partition | Observed result |
| --- | --- | --- |
| Shared rewriter | `{}\n\n1.5 {"model":"upstream"}`, with actual LF bytes | Whole input restores `client`. Writes containing `{}`, the two LF bytes, then the number and object, or bytewise writes, leave the later model unchanged without an error. |
| Minimal container witness | Actual `\r\n\r\n` followed by `{"model":"upstream"}` | Whole input restores `client`. Splitting after `\r\n\r`, before the final LF and object, leaves the model unchanged. |
| Gemini field witness | The same container partition with `modelVersion` | The later `modelVersion` remains unchanged. |
| RPC and callbacks | `handleMethod(executor.execute_stream)`, `callHost` envelopes, host read/emit/close, all five formats with `application/json` | The partition-dependent restoration failure is reproduced. The `openai` event-stream-header case remains unframed and also reproduces it. |
| Controls | Whole input, raw object-leading input, real SSE with unknown fields, and mixed raw-to-SSE input | The recorded controls retain their expected output. A standard JSON Decoder accepts the raw value sequences. |

The [README rewrite boundaries](../../../README.md#rewrite-boundaries) promise preservation of unframed separators. The [2026-10-23 specification](2026-10-23-functional-stream-corrections-design.md#requirements), especially its R9, R11, and R12, already requires raw partition consistency, immediate containers, preserved separators, and distinct helper/native evidence.

### Causal trace

At this baseline, `streamChunkRewriter.Write` checks `isSSEChunk` before its whitespace-only branch. Blank JSON separators can therefore set `sse.started` or leave a terminal CR in `sse.buf`. The buffered-SSE early return can then take a later object, and the scalar branch can send a later numeric sequence to SSE. Those paths bypass raw JSON restoration.

A callback can also contain leading blank separators and a scalar together. Correct classification must cover that callback shape as well as standalone whitespace writes. Reordering only the final whitespace-only branch does not resolve both shapes. An already established SSE event still owns its continuation bytes.

### Baseline verification state

At documentation authoring, the current full baseline, full integration, and final coverage critic have not all completed. The original checkout's recursive checks encountered pre-existing ignored `dist/` Go SDK diagnostic sources. An earlier full race attempt also hit the existing two-second payload-error blocked-emission witness deadline once, with no `DATA RACE` report. A clean-snapshot repetition with the same deadline is pending in the supplied record.

Record those attempts and their eventual results separately. Do not infer an all-passing baseline, a race defect, successful integration, or completed audit coverage from the information above. Preserve the original diagnostics and all old deadlines.

---

## Requirements

### Raw classification and output

- R1. At existing supported raw-input seams, legal raw JSON sequences must produce the same final ordered bytes and whitelisted model restoration under whole input, every two-way split including empty parts, and bytewise writes.
- R2. Preserve exact unframed leading, inter-value, array-adjacent, and trailing JSON whitespace: space, tab, LF, and CR, including CRLF and mixed line endings. Whitespace alone must not commit a raw or undecided stream to SSE.
- R3. Recognize neutral JSON whitespace both in separate callbacks and at the start of callbacks containing scalars or containers. A blank delimiter heuristic alone must not prevent raw restoration at those seams.
- R4. Preserve immediate emission of complete objects, raw array elements, and completed prefixes. Keep unresolved trailing numbers pending until an actual boundary or EOF, including decimal, exponent, negative, and incomplete numeric continuations.

### Existing protocol and data boundaries

- R5. Preserve established SSE continuation ownership, standard/unknown/colonless fields, Responses native-field and logical-event precedence, framing decisions, batching, event names, DONE handling, and the existing BOM behavior. The raw partition requirement does not redefine native callback units or framed scalar/unknown-field interpretation.
- R6. Keep request rewriting top-level-only and response restoration limited to `model`, `modelVersion`, `response.model`, `response.modelVersion`, `message.model`, and `interaction.model`. Preserve all other token bytes, duplicate members/order, escapes, Unicode, numeric lexemes, opaque content/tool text, and existing changed-body header policy.
- R7. Preserve input/output ownership, complete-prefix-plus-error delivery, `maxPendingStreamBytes = 16 << 20` for an incomplete unit, large complete-unit acceptance, cumulative-traffic behavior, and existing allocation ceilings. Keep all original tests, expected values, deadlines, limits, sizes, and capture members.
- R8. Preserve clean/error Flush and Finish behavior, earlier primary errors, known rewrite/flush error selection before blocked emission, emit-failure non-replay, shutdown ordering, and existing host/plugin close-once behavior and failed-close remedy.
- R9. Keep the repair in the shared `streamChunkRewriter.Write` classification area unless failing evidence proves additional private reach necessary. Reuse existing JSON/whitespace helpers and `encoding/json`; add no parser, dependency, configuration, provider branch, public interface, or performance target. CPA, registration, supported formats, C ABI, routing, and release entry points remain unchanged.

### Proof and delivery

- R10. Extend permanent root regressions and the existing executor helper for `openai`, `openai-response`, `claude`, `gemini`, and `interactions` with `application/json`. Assert both model fields where applicable, upstream request/model rewriting, exact returned bytes, and host/plugin closure; retain the `openai` event-stream-header raw control. Preserve the independent RPC/envelope reproduction as a separate evidence layer.
- R11. Extend the existing raw-number controlled-provider test in the sole permanent native fixture, `.github/scripts/testdata/cpa-functional-regression_test.go`. Actual rebuilt DLL plus pinned CPA must prove split whitespace restoration on the `openai` raw seam with whole, direct, disabled, and enabled-unmapped controls, repeated focused runs and complete same-library `make integration`. Record actual loaded library PID/path/hash and built/canonical hashes. The controlled provider supplies the payload; this proves real ABI/CPA behavior and does not establish that an online provider naturally produces it. Five helper formats do not establish five native formats.
- R12. Finish the initial whole-repository functional audit, independent reproduction/contract checks, and final coverage critic before production editing. Each task owner uses an independent worktree, completes its assigned checks and reviews, corrects findings, re-reviews, and makes a task-only English commit. The unique repair owner finishes root/helper/native implementation and proof in its own worktree from the merged documentation baseline; the coordinator merges only completed, conflict-checked work.
- R13. Final acceptance requires full clean-worktree unit/race/vet checks, all three explicit script suites, actual default and injected patch registration, C ABI checks, preserved native capture membership, independent specification/quality/test review, corrections and re-review. Full race and full integration run sequentially. A baseline pass, skipped native test, timeout, or zero-match selector is not repaired-source acceptance.
- R14. Deliver the requested patch through the existing VERSION/linker and main/tag workflow under actual user publication authority. Recheck remote/latest and use an unused patch, expected `v0.5.17` if available. Verify final-SHA Ubuntu integration, all seven platform builds, downloaded archive structure/binary build info/version/LICENSE/checksums, and remove only this run's clean, fully merged, unused worktrees/branches and temporary files. Preserve old `.claude/`, `dist/`, and worktrees; report runtime and cleanup limits accurately.

---

## Design and change reach

Use the existing pending/raw/SSE machinery with the smallest complete classification correction. Neutral whitespace may await the next non-whitespace bytes in existing pending storage. Inspect a whitespace-skipped view for classification while preserving the original bytes for raw parsing/output. Genuine buffered SSE and native field ownership retain their current priority. Confirm the complete algorithm by checking every constructor, caller, recursive Write, and old classification oracle before production editing.

This direction does not prescribe a new mode flag or state machine. The existing `encoding/json` Decoder/RawMessage paths decide JSON validity. Do not implement a parallel JSON grammar or use broad trimming that changes token bytes or admits non-JSON whitespace.

| Surface | Reach |
| --- | --- |
| `stream_rewriter.go` | Planned production correction around `Write`; read recursive array suffix calls, raw pending/number parsing, size accounting, Flush/Finish, and framing. |
| `stream_raw_number_regression_test.go` | Extend existing partition/separator/trace/immediate-container and five-format forwarder coverage. |
| `main_test.go`, `stream_protocol_regression_test.go`, `stream_native_fields_regression_test.go`, `stream_responses_raw_framing_regression_test.go`, `stream_lifecycle_regression_test.go`, `performance_regression_test.go` | Preserve and run old classification, raw/SSE/native, ownership, limit, lifecycle, and allocation oracles. Add only a necessary focused assertion. |
| `.github/scripts/testdata/cpa-functional-regression_test.go` | Extend `TestModelMapperFunctionalRawNumberContinuation` at its existing block near baseline line 1400; reuse controlled provider, loader, request, drain, and identity helpers. |
| `executor_stream.go`, `sse_rewriter.go`, `json_model.go`, `main.go`, `abi_cgo.go` | Read-only call chain and invariants; no production edits planned. |
| `.github/scripts/smoke-local.go`, `smoke-local_test.go`, `Makefile`, `.github/workflows/build.yml`, release scripts | Existing integration, overlay, VERSION injection, compatibility, and publication entry points remain unchanged. |
| Configuration/routing/DSL files, module manifests, README, `CONCEPTS.md`, previous docs, CPA source/module cache, pre-existing artifacts | Unchanged. |

The [project vocabulary](../../../CONCEPTS.md) supplies Client-Requested Model, Upstream Model, CPA Native Plugin, and Plugin Release Package. The [release automation solution](../../solutions/workflow-issues/model-mapper-release-automation-alignment.md) explains the existing VERSION/package boundaries; current Makefile/workflow remains the execution reference.

---

## Acceptance examples

- AE1. Covers R1-R3, R6. Actual CRLF CRLF followed by `{"model":"upstream"}` produces exactly the original whitespace plus `{"model":"client"}` as whole input, split after CRLF CR, every two-way split, and bytewise input. Repeat for `modelVersion` and all LF/CR/CRLF pairs, including mixed pairs.
- AE2. Covers R1-R4. `{}\n\n1.5 {"model":"upstream"}`, using actual LF bytes, restores the later model for whole, `{}` / two-LF / remaining-input, every two-way, and bytewise writes. Repeat with blank separators attached to the scalar callback, at stream start, after containers, around arrays, and at EOF; preserve every separator and numeric spelling.
- AE3. Covers R4-R9. Complete model-bearing objects/arrays still emit during Write. Unresolved numbers wait for boundary/EOF. Existing BOM, scalar unknown-SSE, buffered `data:` continuation, native Responses, mixed raw-to-SSE, complete-prefix/error, limit, ownership, batching, allocation, and lifecycle assertions remain unchanged.
- AE4. Covers R6, R10. All five `application/json` helper formats forward the Upstream Model and rewritten request body, restore applicable `model`/`modelVersion` fields, preserve opaque bytes and response headers as specified, and close host/plugin. The RPC/envelope reproduction and `openai` event-stream-header raw control exercise their own paths.
- AE5. Covers R11. The rebuilt DLL loaded by pinned CPA restores a partitioned CRLF container and blank-separated scalar/object sequence on `openai`. Whole mapped output matches the partitioned mapped output; direct, disabled, and enabled-unmapped controls retain original bytes and correct provider calls. Loaded PID/path/hash agree with the tested canonical DLL, and repeated focused runs plus full integration use that same library identity.
- AE6. Covers R7-R8, R12-R13. Owner and independent reviews complete, all required final-source checks pass without weakened oracles, all old capture keys remain, default registration reports `0.0.0-dev`, and a separate artifact reports the actual selected patch through VERSION injection. Every failed attempt and platform skip remains in the verification record.
- AE7. Covers R14. Authorized main/tag publication points to the verified final source. Ubuntu actually runs integration, seven required builds finish, and each downloaded package matches its SHA256 entry, root library/LICENSE, target build info and injected version. Runtime coverage is reported separately from build coverage; task-owned cleanup preserves pre-existing state and lists any remainder.

---

## Verification limits

No production repair or native success for this whitespace defect is claimed by this document. The pending baseline and coverage results must be reconciled before coding under R12. The plan records concrete root, helper, native, registration, capture, release, and cleanup gates.

No network-security scan or repair, CPA change, paid-provider request, new functionality, or performance campaign is included. Seven platform builds do not establish seven platform runtime loads. Publication requires the user's actual authorization; this document and other agents' messages supply none.
