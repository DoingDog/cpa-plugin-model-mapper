---
title: Native Responses SSE Field Boundaries
type: fix
date: 2026-10-22
baseline: f80b97505edfb50c4d72a634da7c54a44bbe75e8
---

# Native Responses SSE Field Boundaries

## Goal and scope

Fix F-SSE-001 in the plugin's shared stream rewriter. Mapped Responses requests must deliver complete, valid terminal data when CPA supplies native SSE fields without line endings. Preserve the Client-Requested Model in the existing response whitelist and preserve metadata, opaque content, normal framed streams, and stream termination.

The implementation plan is [Native Responses SSE Field Boundaries - Plan](../plans/2026-10-22-native-sse-field-boundaries-implementation.md). This delivery adds only these two planning documents. Production work starts after the coordinator has collected all twelve audit scopes, resolved their candidates, received the independent verification, and reviewed these documents. Any additional confirmed plugin-owned defect must be incorporated before implementation starts. This document does not certify that gate as complete.

## Baseline and evidence

The documentation worktree started clean at `f80b97505edfb50c4d72a634da7c54a44bbe75e8`. The project uses Go `1.26.0`, CPA `v7.2.152`, GJSON `v1.18.0`, and YAML `v3.0.1`. `Makefile` pins integration to CPA revision `c76dfd4e0edabab9000628b1560ab8ab379eadb8`; the smoke helper checks module Sum `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`.

Audit paths below refer to the retained, ignored `dist/` in the original checkout. They are local evidence, not tracked fixtures or files assumed to exist in each worktree.

| Evidence | Observed result and reach |
| --- | --- |
| `dist/audit-20261021/F-sse-protocol/audit_sse_native_scratch_test.go` and `native-baseline-output.txt` | Real XAI and Codex executors supply separate `id`, `retry`, or `x-request-id: trace-123`, then `event: response.completed`, then typed JSON `data:`. Direct CPA HTTP succeeds. The baseline rewriter concatenates the fields, restores no model, and yields zero completed data events. `id: cursor` succeeds as a control. |
| `dist/audit-20261021/F-sse-protocol/audit_sse_native_more_test.go` and `native-empty-fields-baseline-output.txt` | Both producers expose bare `data`, bare `event`, and empty `event:`; these lose the completion. Codex exposes empty `data:` as `data: `, which becomes invalid JSON after concatenation. XAI filters this empty `data:` case before it reaches the plugin. Ordinary `id: cursor` and `: heartbeat` controls succeed. |
| `dist/audit-20261021/verify-F-sse-protocol-1/independent-repro.go` and `independent-http-output.txt` | Real `ExecuteModelStream` -> `runStreamForward` -> replay into the real Responses HTTP consumer reproduces direct HTTP 200 -> mapped HTTP 502 for bare `id`, bare `retry`, and the extension. The error is `upstream stream closed before first payload`. The `id: cursor` control remains HTTP 200 with restored model. This is consumer verification through replay; full rebuilt-DLL acceptance remains an implementation gate. |
| `dist/audit-20261021/F-sse-protocol/audit_sse_scratch_test.go` and `unit-candidate-baseline-output.txt` | Split ordinary comment lines acquire an inserted LF in the current Responses path. These controls expose the same premature metadata-boundary decision. |

CPA's pinned `internal/runtime/executor/xai_executor_stream.go` and `codex_executor_stream.go` scan upstream lines, then translate them to output units. XAI additionally buffers event headers and filters empty colon-form data. CPA's `internal/pluginhost/host_model_stream_callbacks.go` and model-stream bridge preserve non-empty output-unit bytes and expose `Payload`, `Error`, and `Done`; an empty callback is not a surviving SSE blank-line signal. Network Write/Flush boundaries pass through the producer Scanner and are distinct from these units.

The plugin currently recognizes only `id:`, `retry:`, and `:` as native metadata in `isResponsesMetadataField`. `sseRewriter.Write` immediately adds LF to matching no-line-ending callbacks. The delimiterless scanner otherwise expects `event:` or `data:` and a complete typed JSON value. Merely widening the metadata predicate keeps the premature-LF problem: callbacks `id` then `: cursor\n...` must still form `id: cursor\n...`; `retry` has the same constraint.

## Requirements

### Native field recovery

- R1. Recover every confirmed native field shape in the table below for `openai-response`, including metadata before an event, between its fields, between events, and standalone empty-field units before a later completion.
- R2. Preserve the bytes and order of metadata and empty fields, adding only confirmed missing field/event separators. Empty fields must not swallow a later event, become non-empty JSON data, or create a synthetic model response.
- R3. For single completion, created/completed, and the existing nine-event lifecycle, deliver each real data value exactly once, with valid JSON, correct discriminator/status, complete `response.output`, and the Client-Requested Model in every whitelisted field. Pair, data-only, and alternating shapes remain supported.
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

- R5. Standard framed SSE preserves field names, values, field order, line endings, and event boundaries under whole input, every two-part split, bytewise input, and irregular multi-part input. Cover LF, CRLF, bare CR, mixed endings, and a split leading UTF-8 BOM. Apply only the established model edits and leading-BOM handling.
- R6. Retain ambiguous fragments until actual framing or a validated native logical sequence establishes the boundary. A callback end, field-like substring, empty callback, transport flush, or matching prefix alone is insufficient evidence for inserting LF. Unresolved fragments follow existing ordinary-SSE/EOF behavior without speculative repair.
- R7. Keep the existing Responses type validation, exact error/keepalive cases, and one-way `event: response.done` / `type: response.completed` alias. Keep genuine mismatches and inactive-format controls opaque. Interactions continues using its own `event_type` validation and `done`/`[DONE]` handling.
- R8. Request rewriting remains top-level `model` only. Response restoration remains limited to `model`, `modelVersion`, `response.model`, `response.modelVersion`, `message.model`, and `interaction.model`. Preserve duplicate/escaped members, unknown containers, tool arguments, numeric spellings, Unicode, and embedded `event:`/`data:`/model text.

### Resources, interfaces, and delivery

- R9. Preserve the per-incomplete-unit `maxPendingStreamBytes = 16 << 20` policy, complete-prefix-plus-error delivery, buffer/output ownership, and existing allocation ceilings. Complete units and cumulative complete traffic may exceed 16 MiB; unresolved candidate bytes must not evade the incomplete-unit bound.
- R10. Preserve raw JSON values/sequences/arrays, separator bytes, raw-to-SSE framing by exit format, Gemini raw core output, OpenAI host-owned framing/DONE, batching, and unchanged-versus-changed header behavior.
- R11. Preserve natural EOF, Payload+Done, Payload+Error+Done, callback/decode/rewrite/emit/cleanup errors, flush-before-close, error precedence, close-once, cancellation, and shutdown synchronization. Emit failures stop output without replay; error paths add no successful completion marker.
- R12. Use the existing stream rewriter, RPC, C ABI, native loader, and real CPA HTTP/WS seams. Keep dependencies, plugin configuration, registration/schema/capabilities, ABI exports, DSL/routing/caller rules, and supported formats unchanged. Add no provider-specific production branch, new parser, mode, retry, or host change.
- R13. Complete vertical TDD, full regression/performance checks, review and corrections in the implementation owner's independent worktree before its commit is merged. Verify a separately versioned patch artifact, final-SHA CI, and release assets through the existing release flow when publication is authorized.

## Boundary design

Extend the existing shared scanner and its pending state. Keep original bytes while a possible native field boundary is tentative. Represent needed boundary positions with bounded state/offsets into the existing buffer; reuse the incremental JSON cursor, `encoding/json` validation, `drain`, `rewritePendingEvent`, and the whitelist restorer. Recognition of an empty or extension field must not permanently disable recognition of a later valid Responses unit.

A native boundary is committed only within the established Responses output-unit grammar: candidate field units occur at admissible field positions and lead to a complete, valid recognized data value, with an accepted event/type relationship where a non-empty logical header is used. Field recognition and boundary confirmation are separate decisions. Existing complete framed input uses its real line/event separators. Metadata, empty fields, and partial prefixes by themselves do not authorize an irreversible byte edit or data dispatch.

| Buffered input and continuation | Decision |
| --- | --- |
| `id` + `: cursor\n...`, `retry` + `: 100\n...`, or `id:` + ` cursor\n...` | Retain and join the original standard field. No LF at the callback boundary. |
| A split comment, extension name/value, `event:` prefix/value, or `data:` prefix/value | Retain until a real line ending or validated native sequence resolves the candidate. |
| Native `id` -> `event: response.completed` -> complete matching `data:` JSON | Restore the model and emit ordered, separated fields plus a complete SSE data event. |
| Native empty field -> later accepted event/data sequence | Keep the empty field separate and deliver the typed data exactly once. |
| A recognized complete data-only value -> metadata -> next event/data | Preserve the completed value and metadata association according to R4; metadata never becomes part of its JSON. |
| Unknown/mismatched logical data, opaque header text containing canonical field strings, or an unresolved candidate at EOF | Preserve the established opaque/EOF result. Do not infer fields by searching inside text. |

The ABI has no explicit wire-versus-native-unit marker. Preserve ambiguous bytes and test the supported input shapes at their actual seams. A demonstrated conflict for the same accepted callback sequence must stop production edits and return concrete producer/validator/consumer evidence to the coordinator; do not resolve it with a timeout, provider-name guess, arbitrary lookahead count, or relaxed oracle.

The current `TestFunctionalNativeSSEFieldDoneAlias/logical-metadata-callback-characterization` expects `id:\n event-1` from a framed `id:` + ` event-1\n...` split. R5 requires preserving `id: event-1`. Correct this specific oracle during the framed-partition TDD slice, retaining its BOM, error-header, payload, and model assertions. Expand Responses coverage in existing tests that currently exercise arbitrary metadata splits only with `claude`. All other established assertions remain in force.

## Change reach

| Surface | Planned reach |
| --- | --- |
| `sse_rewriter.go`: `sseRewriter`, `delimiterlessResponsesEventScanner`, `Write`, `findDelimiterlessResponsesEventEnd`, reset/drain/rewrite helpers, metadata recognition | Primary implementation scope. Keep one shared incremental mechanism and bounded pending state. |
| `stream_rewriter.go`: `streamChunkRewriter.Write`, classification helpers, `Flush`, `Finish` | Change only if needed to retain/classify the new candidates consistently. Preserve raw JSON priority, pending ownership, and format isolation. |
| `executor_stream.go`: `prepareExecutorStream`, `runStreamForward`, `processPayload`, `flushAndEmit`, `finish`, `emitRewritten` | Callers verified through forwarder/RPC/native tests; production behavior unchanged. |
| `main.go` dispatch: `handleMethod`, `callHost`, `executor.execute_stream` | Exercise actual envelopes and callbacks; no method/schema change. |
| `abi_cgo.go` and `abi_cgo_test.go` | Keep exports `cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`, `cliproxyPluginShutdown`, ABI version, bounds, ownership, and init/shutdown order unchanged. Run cgo tests and load the rebuilt library through real CPA. |
| `json_model.go`, `executor.go`, `model_router.go`, `rules.go`, `config.go` | No production changes; preserve model whitelist, headers, caller identity, routing, guards, DSL, and lifecycle config through existing tests. |
| `stream_native_fields_regression_test.go` | Add behavioral native/boundary/forwarder/RPC cases; correct only the specified framed-field oracle. Reuse `gNativeParts`, payload fixtures, and existing callback patterns. |
| `main_test.go`, `stream_protocol_regression_test.go`, `performance_regression_test.go`, `stream_lifecycle_regression_test.go`, JSON/route/Interactions regression files | Run complete baseline suites. Add a case or include Responses in an existing partition table only where the new boundary behavior requires it; preserve unrelated tests and thresholds. |
| `.github/scripts/testdata/cpa-functional-regression_test.go` | Extend the sole permanent CPA fixture with one separately named boundary matrix. Reuse `gCPANewLocalProducer`, custom `p.wire`, `gCPACoreFields`, `gCPAHostFields`, `gCPALoadNative`, `eNativeDrain`, `eResponsesServer`, `eHTTP`, and `eWS`. Retain all existing fixture functions and capture keys. |
| `.github/scripts/smoke-local_test.go`, `Makefile`, `.github/workflows/build.yml`, packager/compatibility scripts | Existing integration, build, CI, and packaging entry points unchanged. The overlay regex already discovers `TestModelMapperFunctional*`. |
| Configuration, dependencies, metadata, exports, user data | No new fields, environment-dependent product behavior, dependencies, RPC/ABI fields, or data migration. Existing five configuration fields and five formats remain identical. |
| README, `CONCEPTS.md`, previous specs/plans, `docs/solutions/` | Read as context; no edits required. The two new documents contain this fix's behavior and implementation instructions. |

The entire shared stream scope belongs to one coding owner because scanner state, classification, root tests, and the single CPA fixture overlap. Keep the parent-lifetime canonical library, sequential enabled Hosts, fresh init/reconfigure/unload/reload, and actual current-host callback binding. The integration helper's canonical hash alone does not establish the actual loaded Windows shadow DLL; capture the actual child PID/path/hash when recording native evidence.

## Acceptance examples

- AE1. Covers R1-R3, R8. For each confirmed field, actual XAI/Codex producer capture -> rebuilt native plugin -> real mapped HTTP produces one valid `response.completed` with restored model, completed status, and full opaque output. Direct, enabled-unmapped, and disabled controls use their respective actual models. XAI empty colon-form data is recorded as filtered; Codex's surviving `data: ` is exercised.
- AE2. Covers R1-R4. Place the fields before event, between event/data, between created/completed, and in standalone empty-field groups. Pair, data-only, and alternating shapes preserve sequence and metadata bytes without extra non-empty data. Repeat Flush/Finish and late delimiters without duplicate completion.
- AE3. Covers R5-R7. `id` + `: cursor\n`, `retry` + `: 100\n`, split `id:`, split comments/extensions, and split empty fields retain the same standard wire under all partitions. Opaque field values containing canonical event/data strings remain opaque; genuine logical mismatches remain unchanged.
- AE4. Covers R7-R10. Existing Responses error/keepalive/done-alias, Grok created/comment/completed, Interactions, raw JSON, Gemini arrays/raw core, markerless/escaped/multidata SSE, and unchanged-byte fast paths retain their established results and performance assertions.
- AE5. Covers R9, R11. Valid pending data is emitted before host/plugin close on read or terminal errors. Complete prefix survives an oversized incomplete suffix; the suffix is never emitted. Original errors, no-extra-read behavior, close order/counts, and emit-failure stopping remain observable through RPC and the native bridge.
- AE6. Covers R12-R13. Actual init/register on default and separately versioned libraries verifies registration, schema, configuration fields, formats, and version. Full tests, vet, race, scripts, native integration, final-SHA CI, and authorized patch packaging/release satisfy the implementation plan.

## Verification and limits

The plan defines exact TDD commands and acceptance gates. Freeze the complete baseline test/benchmark/fixture membership and expanded cases, then account separately for new boundary cases and the one corrected oracle. Preserve the four existing capture sets and their full key membership: `native-binary-captures.json` (1536), `mixed-native-captures.json` (1800), `mixed-binary-captures.json` (1440), and `mixed-shape-controls.json` (1680). Counts alone do not establish coverage.

Gemini `countTokens` lacks a reliable operation signal/host count callback in the current ABI. Host error information already omitted before reaching the plugin cannot be reconstructed safely; asynchronous close exposes only an error string. Keep these host limitations outside this development scope, while preserving the structured synchronous fields that do reach `callHost`. Fixed CPA's existing error-only WS behavior and mixed direct/disabled consumer controls remain separate from successful mapped completion acceptance.

This documentation task reads local sources and retained evidence and performs document/Git checks. Runtime TDD, rebuilt C ABI/native HTTP verification, benchmarks, remote CI, and publication are future implementation gates. No external provider account or paid model request is needed for the local real-CPA matrix.
