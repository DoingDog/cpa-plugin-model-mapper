---
title: Native Responses SSE Field Boundaries
type: fix
date: 2026-10-22
baseline: f80b97505edfb50c4d72a634da7c54a44bbe75e8
---

# Native Responses SSE Field Boundaries

## Goal and scope

Fix F-SSE-001 in the plugin's shared stream rewriter. Mapped Responses requests must deliver complete, valid terminal data when CPA supplies native SSE fields without line endings. Preserve the Client-Requested Model in the existing response whitelist and preserve metadata, opaque content, normal framed streams, and stream termination.

The [implementation plan](../plans/2026-10-22-native-sse-field-boundaries-implementation.md) defines the vertical TDD and delivery gates. The twelve-scope audit and the focused verification are complete; F-SSE-001 is the sole implementation scope. Continue with the shared-scanner fix and ordinary regression after these documents are reviewed. This document-only revision changes no production code or tests.

Do not perform cybersecurity scanning or repair, authentication/credential or digest-integrity investigations, vulnerability or resource-exhaustion investigations, unrelated fixes, or CPA changes. Preserve existing tests and protections through ordinary regression. Use existing performance checks without adding a performance investigation or new benchmark matrix.

## Baseline and evidence

The documentation worktree started clean at `f80b97505edfb50c4d72a634da7c54a44bbe75e8`. `go.mod` declares Go `1.26.0`, CPA `v7.2.152`, GJSON `v1.18.0`, and YAML `v3.0.1`. `Makefile` pins integration to CPA revision `c76dfd4e0edabab9000628b1560ab8ab379eadb8`; the smoke helper checks module Sum `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`.

Audit paths below refer to the retained, ignored `dist/` in the original checkout. They are local evidence, not tracked fixtures or files assumed to exist in each worktree.

| Evidence | Observed result and reach |
| --- | --- |
| `dist/audit-20261021/F-sse-protocol/` | Initial producer and root reproductions identify bare `id`/`retry`, `x-request-id: trace-123`, bare `data`/`event`, and empty `event:`/`data:`. Use the verified native/consumer records below for acceptance; the initial field list alone does not establish a healthy direct HTTP control. |
| `dist/audit-20261021/verify-F-reproduction/conclusion.json` and `results.json` | Twenty cases use actual XAI/Codex producers, the baseline native DLL, and the real Responses HTTP handler. Six event/data cases with bare `id`, bare `retry`, or the extension change healthy direct HTTP 200 into mapped HTTP 502, with `upstream stream closed before first payload`. Twelve root/native cases lose data; the repeated-metadata data-only direct controls are unhealthy and do not establish plugin-only HTTP regressions. No-metadata and `id: cursor` controls succeed. |
| `dist/audit-20261021/verify-F-empty-native/final.json`, `minimal-witnesses.json`, and `coverage.json` | The complete 222-case baseline DLL/HTTP matrix confirms 98 regressions with healthy direct semantics: 58 explicitly named-event cases and 40 other valid semantic cases. It records event names, JSON type/status/model, opaque model, output text, actual producer units, and identical direct/mapped upstream requests. Keep all case identities and classifications. Codex empty `data:`/`data: ` is independently confirmed in data-only streams at six positions for each spelling; its original explicit-event control changes the event name in direct CPA and is excluded from plugin-only acceptance. XAI filters colon-form empty data. |
| `dist/audit-20261021/feasibility-P-observable-signal/evidence.json` | The ABI/RPC has no trusted wire-versus-native marker. A custom SDK raw `ProviderExecutor` can reproduce the same callback sequence as native fields with a conflicting raw-wire interpretation. The plugin cannot support both interpretations of that identical unmarked sequence. |
| `C:/Users/user/AppData/Local/Temp/q-existing-contract-_kuwu1xz/result.json` and `evidence.zip`; `dist/audit-20261021/feasibility-R-runtime-collision/conclusion.json` and `runtime-results.json` | Existing tests establish Responses native-unit precedence. Actual stock XAI/Codex whole and bytewise upstream writes produce the same native units; opaque lines containing event/data text remain whole units. The actual Responses HTTP consumer already gives native-shaped callback arrays native meaning. These records establish the supported seams and limits, not a repaired implementation. |

CPA's pinned `internal/runtime/executor/xai_executor_stream.go` and `codex_executor_stream.go` scan upstream lines, then translate them to output units. XAI additionally buffers event headers and filters empty colon-form data. CPA's `internal/pluginhost/host_model_stream_callbacks.go` and model-stream bridge preserve non-empty output-unit bytes and expose `Payload`, `Error`, and `Done`; an empty callback is not a surviving SSE blank-line signal. Network Write/Flush boundaries pass through the producer Scanner and are distinct from these units.

The plugin currently recognizes only `id:`, `retry:`, and `:` as native metadata in `isResponsesMetadataField`. `sseRewriter.Write` adds LF to matching no-line-ending callbacks at recognized field positions. The delimiterless scanner otherwise expects `event:` or `data:` and a complete typed JSON value. Retain those existing behaviors and assertions. For newly recognized bare fields, merely widening the predicate and immediately inserting LF would break `id` then `: cursor\n...` or `retry` then `: 100\n...`; retain these fragments until their continuation or a validated native sequence resolves them.

## Requirements

### Native field recovery

- R1. Recover every confirmed native field shape in the table below for `openai-response`, including metadata before an event, between its fields, between events, and standalone empty-field units before a later completion.
- R2. Preserve field bytes, order, and empty-field semantics, adding only confirmed missing field/event separators. Repeated event fields use the last name; a last empty event field resets the name to the decoder's default `message`, while a later named event overrides an earlier empty field. Empty data must not swallow later JSON, become non-empty JSON data, or create a synthetic response. Do not force every valid data value to have an explicit event name.
- R3. For single completion, created/completed, and the existing nine-event lifecycle, deliver each real non-empty data value exactly once, with valid JSON, the expected event name, correct discriminator/status, complete `response.output`, and the Client-Requested Model in every whitelisted field. Pair, data-only, and alternating shapes remain supported. Use the fixed healthy-direct case set for HTTP acceptance; root/native coverage of other shapes does not establish healthy direct HTTP semantics.
- R4. Preserve existing dispatch semantics: confirmed native field-pairs dispatch on their complete data unit; ordinary data-only values wait for the next confirmed boundary or EOF. Pending metadata remains in order, complete comments after verified data-only values retain the established flush behavior, and late LF/CRLF delimiters do not duplicate data.

| Native input field/unit | Required interpretation |
| --- | --- |
| `id`, `retry` | Colonless fields with empty values. Preserve their spelling and separate them from the following native fields. |
| `x-request-id: trace-123` | Opaque extension field. Preserve its name/value without interpreting or rewriting embedded text. |
| `data`, `event` | Colonless empty fields. Preserve empty-field semantics and allow the subsequent typed completion to be recognized. |
| `event:`, including its original optional space | Empty event-name field. Preserve it across standalone units and repeated event fields; do not merge it into the next header. |
| `data:` / actual Codex `data: ` | Empty data field. Keep it separate from later JSON. XAI's filtered-empty-data case is a producer control. |
| Existing `id: cursor`, `retry: 100`, `: heartbeat`, and complete comments | Preserve their current native metadata function, opaque bytes, positions, and dispatch controls. |

### Boundary decisions and content preservation

- R5. Preserve all established framed/partition assertions at their actual formats and seams, including LF, CRLF, bare CR, mixed endings, and leading-BOM handling. Generic literal-event and arbitrary wire-partition controls in `claude` remain unchanged. Responses retains whole opaque units, existing metadata-resegmented cases, and native event-unit + typed-data-unit precedence, including data units ending in real delimiters. Do not extend the generic controls into an unconditional Responses arbitrary-wire-partition guarantee or change an old oracle.
- R6. For newly recognized fields, retain original bytes and candidate callback offsets until real framing or a complete validated native logical sequence confirms a boundary. A callback end or prefix alone does not authorize a new LF; an empty callback or network flush supplies no boundary. Preserve `id` + `: cursor\n`, `retry` + `: 100\n`, and partial field/value continuations without eager insertion. Never find new boundaries by searching inside a complete opaque unit or JSON text. Unresolved new candidates use existing ordinary-SSE/EOF behavior, subject to the established native precedence.
- R7. Keep the existing Responses type validation, exact error/keepalive cases, and one-way `event: response.done` / `type: response.completed` alias. Keep genuine mismatches and inactive-format controls opaque. Interactions continues using its own `event_type` validation and `done`/`[DONE]` handling.
- R8. Request rewriting remains top-level `model` only. Response restoration remains limited to `model`, `modelVersion`, `response.model`, `response.modelVersion`, `message.model`, and `interaction.model`. Preserve duplicate/escaped members, unknown containers, tool arguments, numeric spellings, Unicode, and embedded `event:`/`data:`/model text.

### Resources, interfaces, and delivery

- R9. Preserve the per-incomplete-unit `maxPendingStreamBytes = 16 << 20` policy, complete-prefix-plus-error delivery, buffer/output ownership, incremental cursors, and existing allocation ceilings. Complete units and cumulative complete traffic may exceed 16 MiB. New candidate state remains subject to the same policy. Run existing limit/continuation/ownership checks as ordinary regression; add no resource-exhaustion investigation.
- R10. Preserve raw JSON values/sequences/arrays, separator bytes, raw-to-SSE framing by exit format, Gemini raw core output, OpenAI host-owned framing/DONE, batching, and unchanged-versus-changed header behavior.
- R11. Preserve natural EOF, Payload+Done, Payload+Error+Done, callback/decode/rewrite/emit/cleanup errors, flush-before-close, error precedence, close-once, cancellation, and shutdown synchronization. Emit failures stop output without replay; error paths add no successful completion marker.
- R12. Use the existing stream rewriter, RPC, C ABI, native loader, and real CPA HTTP/WS seams. Keep dependencies, plugin configuration, registration/schema/capabilities, ABI exports, DSL/routing/caller rules, and supported formats unchanged. Add no provider-specific production branch, new parser, mode, retry, or host change.
- R13. Complete vertical TDD, ordinary root/script/vet/race checks, existing performance regression, review, corrections, and re-review in the implementation owner's independent worktree before its commit is merged. Verify a separately versioned patch artifact, final-SHA CI, and seven-platform release assets through the existing release flow when publication is authorized. Clean task-created worktrees/branches only after successful integration and confirmation that they are clean and fully merged.

## Boundary design

Extend the existing shared scanner and its pending state. Keep original bytes while a possible native field boundary is tentative. Represent needed boundary positions with bounded state/offsets into the existing buffer; reuse the incremental JSON cursor, `encoding/json` validation, `drain`, `rewritePendingEvent`, and the whitelist restorer. Recognition of an empty or extension field must not permanently disable recognition of a later valid Responses unit.

New candidate boundaries are committed only within the established Responses output-unit grammar: candidate field units occur at admissible positions and lead to a complete, valid recognized typed data value, with the existing event/type checks and one-way alias applied to a non-empty active header. Preserve last-event-field and empty-field semantics. Record only observed callback offsets; do not discover interior field boundaries from text. Recognition of a candidate and confirmation of its missing separator are separate decisions. Preserve the existing recognized-metadata behavior and per-Write dispatch assertions.

| Buffered input and continuation | Decision |
| --- | --- |
| `id` + `: cursor\n...` or `retry` + `: 100\n...` | Retain and join the original field. Do not insert LF when the bare prefix arrives. |
| A newly recognized partial extension, empty-field prefix, or field-value continuation | Retain original bytes until real framing or the accepted native sequence resolves the candidate. |
| Native `id` -> `event: response.completed` -> complete matching `data:` JSON | Restore the model and emit ordered, separated fields plus a complete SSE data event. |
| Native empty field -> later accepted event/data sequence | Preserve the empty field and last-header/default-name semantics; deliver typed data exactly once. |
| A recognized complete data-only value -> metadata -> next event/data | Preserve the completed value and metadata association according to R4; metadata never becomes part of its JSON. |
| One opaque callback containing `event:`/`data:` text, or an existing metadata-resegmented header followed by real data | Preserve the complete opaque bytes and the existing tests' interpretation. Only the subsequent real data unit may be restored; never split embedded text into fields. |
| Unknown/mismatched logical data or an unresolved new candidate at EOF | Preserve the established opaque/EOF result without speculative separators. |

The ABI/RPC has no trusted wire-versus-native-unit marker. At the Responses entry point, native-shaped callback sequences retain their established native interpretation. A custom raw `ProviderExecutor` can supply an identical sequence intending a single wire field; that conflicting raw interpretation cannot also be guaranteed. Whole opaque units and generic-format partition controls remain supported at their existing seams. Do not claim the two interpretations are equivalent supported inputs, infer a source from provider names/headers, add a mode, or reopen the completed source-signal investigation.

Keep every old oracle, including `TestFunctionalNativeSSEFieldNativeDelimiter`, `TestFunctionalNativeSSEFieldResponsesMetadataControl`, the metadata-resegmented cases, and `TestFunctionalNativeSSEFieldDoneAlias/logical-metadata-callback-characterization`. The latter retains its existing `id:\n event-1` expectation, BOM, error-header, payload, and model assertions. Generic literal-event and all-split tables remain at their current formats; add only focused new continuation controls compatible with the existing Responses precedence.

## Change reach

| Surface | Planned reach |
| --- | --- |
| `sse_rewriter.go`: `sseRewriter`, `delimiterlessResponsesEventScanner`, `Write`, `findDelimiterlessResponsesEventEnd`, reset/drain/rewrite helpers, metadata recognition | Primary implementation scope. Keep one shared incremental mechanism and bounded pending state. |
| `stream_rewriter.go`: `streamChunkRewriter.Write`, classification helpers, `Flush`, `Finish` | Change only if needed to retain/classify the new candidates consistently. Preserve raw JSON priority, pending ownership, and format isolation. |
| `executor_stream.go`: `prepareExecutorStream`, `runStreamForward`, `processPayload`, `flushAndEmit`, `finish`, `emitRewritten` | Callers verified through forwarder/RPC/native tests; production behavior unchanged. |
| `main.go` dispatch: `handleMethod`, `callHost`, `executor.execute_stream` | Exercise actual envelopes and callbacks; no method/schema change. |
| `abi_cgo.go` and `abi_cgo_test.go` | Keep exports `cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`, `cliproxyPluginShutdown`, ABI version, bounds, ownership, and init/shutdown order unchanged. Run cgo tests and load the rebuilt library through real CPA. |
| `json_model.go`, `executor.go`, `model_router.go`, `rules.go`, `config.go` | No production changes; preserve model whitelist, headers, caller identity, routing, guards, DSL, and lifecycle config through existing tests. |
| `stream_native_fields_regression_test.go` | Add focused native-field, continuation, dispatch, forwarder, and RPC cases. Reuse `gNativeParts`, payload fixtures, and existing callback patterns. Preserve every old assertion and oracle. |
| `main_test.go`, `stream_protocol_regression_test.go`, `performance_regression_test.go`, `stream_lifecycle_regression_test.go`, JSON/route/Interactions regression files | Run complete baseline suites unchanged, including existing performance thresholds. Add a focused new-field case only where necessary; retain generic partition tables at their existing formats. |
| `.github/scripts/testdata/cpa-functional-regression_test.go` | Extend the sole permanent CPA fixture with one separately named boundary matrix. Reuse `gCPANewLocalProducer`, custom `p.wire`, `gCPACoreFields`, `gCPAHostFields`, `gCPALoadNative`, `eNativeDrain`, `eResponsesServer`, `eHTTP`, and `eWS`. Retain all existing fixture functions and capture keys. |
| `.github/scripts/smoke-local_test.go`, `Makefile`, `.github/workflows/build.yml`, packager/compatibility scripts | Existing integration, build, CI, and packaging entry points unchanged. The overlay regex already discovers `TestModelMapperFunctional*`. |
| Configuration, dependencies, metadata, exports, user data | No new fields, environment-dependent product behavior, dependencies, RPC/ABI fields, or data migration. Existing five configuration fields and five formats remain identical. |
| README, `CONCEPTS.md`, previous specs/plans, `docs/solutions/` | Read as context; no edits required. The two new documents contain this fix's behavior and implementation instructions. |

The entire shared stream scope belongs to one coding owner because scanner state, classification, root tests, and the single CPA fixture overlap. Keep the parent-lifetime canonical library, sequential enabled Hosts, fresh init/reconfigure/unload/reload, and actual current-host callback binding. The integration helper's canonical hash alone does not establish the actual loaded Windows shadow DLL; capture the actual child PID/path/hash when recording native evidence.

## Acceptance examples

- AE1. Covers R1-R3, R8. Actual XAI/Codex producer capture -> rebuilt native plugin -> real mapped HTTP delivers exactly one completed JSON value with restored model, completed status, and full opaque output for every healthy-direct case. Preserve all 98 confirmed empty-field regressions by archived case identity, including Codex data-only empty-data at its six confirmed positions for both spellings. Decode event names as well as JSON; a valid default `message` name must remain default. Direct, enabled-unmapped, and disabled controls use their respective actual models. Keep XAI's filtered-empty-data control and the archived unhealthy-direct cases separately classified; neither establishes a new mapped-success requirement.
- AE2. Covers R1-R4. Place fields before event, between event/data, between created/completed, and in standalone empty-field groups at applicable producer positions. Pair, data-only, and alternating shapes preserve sequence and bytes without extra non-empty data. Include repeated event headers, last-empty-header reset, and later-named-header override. Repeat Flush/Finish and late delimiters without duplicate completion.
- AE3. Covers R5-R7. New `id` + `: cursor\n`, `retry` + `: 100\n`, extension and empty-field continuation controls retain their original field bytes until resolved. Preserve whole opaque units, existing Responses metadata-resegmented assertions, all old native tests and per-Write dispatch, and generic-format all-split controls at their current formats. Opaque values and JSON text containing canonical field strings remain intact; genuine logical mismatches remain unchanged.
- AE4. Covers R7-R10. Existing Responses error/keepalive/done-alias, Grok created/comment/completed, Interactions, raw JSON, Gemini arrays/raw core, markerless/escaped/multidata SSE, and unchanged-byte fast paths retain their established results and performance assertions.
- AE5. Covers R9, R11. Valid pending data is emitted before host/plugin close on read or terminal errors. Complete prefix survives an oversized incomplete suffix; the suffix is never emitted. Original errors, no-extra-read behavior, close order/counts, and emit-failure stopping remain observable through RPC and the native bridge.
- AE6. Covers R12-R13. Actual init/register on default and separately versioned libraries verifies registration, schema, configuration fields, formats, and version. Full tests, vet, race, scripts, native integration, final-SHA CI, and authorized patch packaging/release satisfy the implementation plan.

## Verification and limits

The plan defines exact TDD commands and acceptance gates. Freeze the complete baseline test/benchmark/fixture membership and expanded cases; list new boundary cases separately and preserve every old assertion. Preserve the four existing capture sets and their full key membership: `native-binary-captures.json` (1536), `mixed-native-captures.json` (1800), `mixed-binary-captures.json` (1440), and `mixed-shape-controls.json` (1680). Map the complete 222-case empty-field archive to its permanent acceptance/control cases, including the fixed set of 98 healthy-direct regressions. Counts alone do not establish coverage.

Use the mature `github.com/gin-contrib/sse` decoder already available in the CPA fixture, together with `encoding/json`, to check event name, JSON type/status/model, complete output, opaque values, and text. Keep original bytes for exact metadata/empty-field assertions; HTTP 200 and substring checks are insufficient. Normalize known line endings only for the decoder, whose existing implementation is LF-only. Do not introduce a new SSE parser or root dependency.

Preserve existing HTTP/WS success and error controls at the pinned CPA's actual behavior. A direct consumer case that is already unhealthy cannot establish a plugin-only HTTP regression. The unmarked custom raw/native collision described above remains an input-interpretation limit.

This documentation task reads local sources and retained evidence and performs document/Git checks. Runtime TDD, rebuilt C ABI/native HTTP/WS verification, existing benchmarks, remote CI, and publication remain implementation/delivery gates. No external provider account or paid model request is needed for the local real-CPA matrix.
