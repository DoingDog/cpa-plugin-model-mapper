---
title: Functional Stream Corrections
type: fix
date: 2026-10-23
baseline: 908db0a416cd0ef8db734e04114491700a941c8a
---

# Functional Stream Corrections

## Goal and scope

Correct three reproduced functional defects in the existing stream path: synthetic Responses framing that loses a pretty raw JSON completion in the actual WebSocket consumer, a known flush error replaced during shutdown, and numeric continuation that bypasses later response-model restoration. The [implementation plan](../plans/2026-10-23-functional-stream-corrections-implementation.md) defines ownership, test-first execution, integration, and delivery.

The original thirteen audit assignments and three cross-module assignments have completed. The independent coverage review found no omitted production module, test category, script, or cross-module assignment and no additional candidate. C1, C2, and C3 each have dynamic native evidence against the pinned CPA. The analogous `processPayload` window remains a conditional subtask of C2, requiring its own failing behavioral test.

This specification owns R1-R17 and AE1-AE8. It and its companion plan are the canonical documents for this work. Earlier ignored drafts are evidence inputs, not competing requirements. This document-only task changes no production code and runs no tests, builds, or publication operations. A delivery plan does not grant permission to perform outward operations.

---

## Baseline and evidence

The source baseline is `908db0a416cd0ef8db734e04114491700a941c8a`. `go.mod` declares Go `1.26.0`, CPA `v7.2.152`, GJSON `v1.18.0`, and YAML `v3.0.1`. Integration pins CPA revision `c76dfd4e0edabab9000628b1560ab8ab379eadb8` and module Sum `h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=`. The recorded local runtime is Go `1.26.5`, Windows/amd64, with cgo enabled. Initial local and remote main were at the baseline; the recorded latest release was `v0.5.15`.

Local audit records are retained in the original checkout's ignored `dist/functional-audit-20261023-897b632d/`. Its `execution-plan.md`, `verified-candidates.md`, `draft-spec.md`, and `draft-implementation.md` were read together with the completed workflow results below. The candidate ledger predates C3's completion; the completed C3 result supplies its final evidence. Workflow output names identify the supplied session's task logs, not tracked repository files or files promised to exist in a fresh worktree.

| Record | Result used here |
| --- | --- |
| `w04kl39fa.output`, `result.results[0..12]` | All thirteen assigned audit results returned, `missing=[]`. The raw-stream reader supplied C3 for separate verification. |
| `wquzphc2u.output`, `result.results[0..2]` | All three cross-module results returned, `missing=[]`. The stream/client reader supplied C1 and C2; the other two found no additional actionable defect. |
| `wroiipkej.output` | Six C1/C2 verification results returned. Each candidate has a separate dynamic native reproduction and independent contract/causal checks. The read-only skeptics' `plausible` verdicts are corroboration, not additional native reproductions. |
| `w6p10yklb.output` | C3 dynamic reproduction, independent contract and causal checks, and the coverage review all returned, `missing=[]`. The dynamic result confirms C3; the read-only checks do not claim to have reproduced it. |
| C1 tree `.claude/worktrees/wf_5426b494-7fa-1` | Three preserved experiment files cover callback output and actual native HTTP/WS consumers. Two native runs lose mapped pretty completion with close 1006; disabled, enabled-unmapped, compact mapped, and mapped HTTP controls succeed. |
| C2 tree `.claude/worktrees/wf_5426b494-7fa-4` | Preserved root/native tests repeatedly expose the wrong terminal reason, including five focused race repetitions without a race report and four native runs. Natural completion and in-band-primary controls retain their expected errors. |
| C3 tree `.claude/worktrees/wf_bd8c1abf-58d-1` | Preserved root, five-format `application/json` helper, and native `openai` tests reproduce missed model restoration after split decimals/exponents. Whole-input, direct/unmapped, framed, integer, and immediate-container controls succeed. |

### Observed defects

C1 uses a complete valid raw `response.completed` value containing three actual LF bytes and empty host headers. The baseline restores its model and synthesizes multiple `data:` lines. CPA's validator accepts this SSE, but the actual Responses WebSocket consumer tests individual data lines for complete JSON and receives no completion. The same consumer accepts direct raw pretty JSON through its whole-value fallback. This is a supported SDK/ABI input; the evidence does not show a stock provider naturally producing it.

C2 uses complete raw JSON warm-up units, then `1 2 [` and clean Done. The real downstream bridge has accepted seventeen units before final-flush emission blocks. `Finish` has already returned valid scalar output and `incomplete raw JSON stream`, but shutdown selects and closes with `executor stream interrupted during shutdown`. Natural completion reports the flush error; an already selected `primary upstream error` survives the same shutdown. The observed defect is loss of the known error reason. Accepted data loss or a hang was not demonstrated.

C3 uses the unframed sequence `1.5 {"model":"upstream"}`, split after `1`. The baseline consumes `1` immediately, then classifies `.5 {"model":"upstream"}` as SSE and leaves the later model unchanged. Both the single-value fast path and a fresh Decoder's local EOF can prematurely finish a trailing number, including after a completed object. The native witness uses `openai`, which remains unframed even when its returned Content-Type is `text/event-stream`. The five-format `application/json` witness is a production executor helper test; fixed CPA did not preserve that provider header in the attempted Responses native route. These are distinct evidence layers.

### Baseline verification context

The original checkout's recursive unit, vet, and race commands failed package discovery because pre-existing ignored `dist/` diagnostics contain Go sources. Explicit root-package checks and later complete clean-worktree checks passed. Preserve those diagnostics and the failed attempts.

The first `make integration` attempt hit its outer default ten-minute timeout while a full race run was also active. A later invocation of the unchanged integration entry passed after race had finished, without changing assertions or internal deadlines. The coordination record also reports a successful clean-worktree full suite and native integration. These are baseline results, not repaired-source acceptance. Record every attempt separately; do not describe the initial attempts as all passing.

Windows conditionally skipped `TestWriteChecksumFileAtomicallySetsMode`, `TestRunRejectsAliasedOutputsThroughSymlinkThenParent`, and `TestWriteSmokeConfigTightensPermissions`. Other operating systems' runtime loading remains unverified locally. The recorded busy cleanup remainder is `dist/baseline-validation-20261023-4d02c37a1f6b/evidence-retry/cpa-77b51d043864851f/cpa-v7.2.152/internal/pluginhost` and its parents. Recheck and report its final state without forced deletion or host-system repair.

---

## Requirements

### C1: Responses synthetic raw JSON

- R1. A mapped complete raw Responses value containing insignificant LF, CR, CRLF, spaces, or tabs must reach the actual Responses WebSocket consumer as one complete JSON value, with the Client-Requested Model restored and the original response content retained.
- R2. Compact whitespace only when `format == "openai-response"` and the existing raw-to-SSE path synthesizes framing. Each complete raw value produces one physical SSE data line with the existing event-name behavior.
- R3. Preserve member order, duplicate members, string-token spellings, escapes, Unicode, and number lexemes during compaction. Value replacement remains limited to the response whitelist in R13.
- R4. The C1 framing correction must preserve exact behavior for format-empty generic framing, native SSE, unframed and `application/json` output, and all other formats. Keep `TestStreamChunkRewriterPrefersValidRawJSONOverSSEDelimiterHeuristic` and every existing expected value unchanged.

### C2: Known errors before blocked emission

- R5. Register a non-nil wrapped `Finish`/`Flush` error through existing terminal selection before emitting its completed output. Shutdown overlapping that blocked emit must preserve this already selected reason.
- R6. Preserve the first selected terminal outcome and earlier primary errors. Nil natural completion is selected only after output finishes; early error registration must not set `outputFinished`.
- R7. Preserve completed-output ordering, stopping without replay after emit failure, normal emit -> host-close -> plugin-close order, forced shutdown's downstream-unblock order, host close-once, successful downstream close-once, and the existing remedy after a failed close.
- R8. Add a focused behavioral test for the analogous `processPayload` window before changing it. If a real rewrite error is lost during blocked emission and shutdown, repair that ordering within C2 and prove the same test passes; otherwise leave the production branch unchanged and record the non-reproduction.

### C3: Raw numeric continuation

- R9. At the existing supported raw-input seams, legal raw JSON sequences must have the same final ordered bytes and whitelisted model restoration under whole input, every two-way split, and bytewise input, including decimals, exponents, negative values, and a preceding completed object.
- R10. At non-EOF input, retain a trailing numeric value whose end is not yet established by a real boundary. Apply this rule to both the `json.Valid` single-value fast path and the Decoder multi-value path, preserving the pending number and any retained separator until a boundary or EOF resolves it.
- R11. Keep complete objects, arrays, and completed prefixes immediately deliverable, with original unframed separators and opaque content. Preserve framed scalar handling, established SSE/unknown-field and native-type interpretation, pending accounting, and batching.
- R12. Prove C3 through the existing executor helper for all five formats with `application/json` and through the real native `openai` unframed route. An unterminated trailing number may wait for a confirmed boundary or EOF; no numeric immediate-emission guarantee is introduced.

### Shared behavior and interfaces

- R13. Preserve top-level-only request-model rewriting and response restoration limited to `model`, `modelVersion`, `response.model`, `response.modelVersion`, `message.model`, and `interaction.model`. Preserve changed-body header policy, input/output ownership, complete-prefix-plus-error delivery, `maxPendingStreamBytes = 16 << 20`, EOF/terminal/error handling, existing allocation ceilings, and all old test assertions, sizes, and deadlines.
- R14. Keep CPA, dependencies, configuration, DSL/routing/caller behavior, supported formats, registration/schema/capabilities, and C ABI unchanged. Add no provider-specific production branch, parser, mode, retry, completion/error event, permanent smoke entry, or duplicate native fixture.

### Execution and delivery

- R15. Each coding owner completes focused failing proof, the minimal repair, regression checks, specification and quality review, corrections, re-review, and a task-only English commit in its own worktree. Run independent nonconflicting work concurrently and integrate only completed, reviewed commits.
- R16. Final acceptance requires full unit/vet/race checks, all three explicit script suites, registration/C ABI checks, actual default and patch-version registration, repeated new native regressions, complete same-library `make integration`, and capture-membership verification. Full race and full integration run sequentially; a prior baseline, skipped integration, or helper-only test does not satisfy this gate.
- R17. Delivery uses an unused patch version through existing VERSION/linker injection and the existing main/tag workflow, subject to actual publication authority. Verify final-SHA Ubuntu integration, all seven builds and downloaded release packages, then remove only this run's clean, fully merged, unused worktrees/branches and report remaining runtime or cleanup limits.

---

## Design

### C1: Compact at the private synthetic framing boundary

Use standard `encoding/json.Compact` in `streamChunkRewriter.frameRawJSON`, after whitelist restoration and only for R2's path. The existing `sseRewriter.rewriteMultiDataEvent` already uses this operation. Compaction removes insignificant whitespace without decoding and re-marshaling values, so R3's token bytes survive.

Leave `frameSSEEvent` unchanged because it also implements generic multiline framing. Keep native SSE classification and ordinary SSE restoration unchanged. An unexpected compaction error must return through the existing chunks-plus-error path. A private `frameRawJSON` return-value change is permitted, with checked handling at its two direct `tryRawJSONChunks` call sites. Preserve complete chunks already produced before an error; do not ignore the error or substitute unmodified output.

### C2: Select the known error before calling the host

In `executorStream.flushAndEmit`, wrap the non-nil local error as today, then record it with `selectTerminal(..., false)` before `emitRewritten`. Keep the existing local error and return ordering. The lifecycle mutex protects selection; no host callback runs under it.

Keep `finish`, shutdown, close helpers, and terminal state structure intact. Early selection records the reason only. Existing shutdown can use that selected reason to close downstream and unblock emission, while output completion and cleanup retain their current owners. This preserves a known error; it does not promise that cleanup errors discovered after a successful terminal close can amend that close.

Use the same narrow ordering for `processPayload` only when R8's own test proves the failure. The gate is part of the same task and requires no new user choice or fourth defect classification.

### C3: Retain an unresolved trailing number

Use the existing `encoding/json` validation, Decoder, RawMessage, and offset information to distinguish a complete local value from a stream value whose numeric token can still continue. Retain the unresolved tail through existing pending storage, then reconsider it with later bytes or EOF. A completed prefix remains available for emission.

Both `tryRawJSONChunks` and `splitJSONValues` must honor R10. Removing only the fast path is insufficient because the next Decoder also sees a local Reader EOF. Do not add arbitrary `.`, `e`, or `E` as new JSON starts, implement a separate JSON parser, or change the SSE classifier to reinterpret genuine unknown fields. Preserve `splitJSONValues`' other callers: pending-size accounting, framed scalar-prefix bookkeeping, and the existing test decoder helpers.

---

## Change reach and ownership

| Surface | Required reach |
| --- | --- |
| `stream_rewriter.go` | C1 owns `frameRawJSON` and its two checked call sites. C3 subsequently owns trailing-number handling in `tryRawJSONChunks` and `splitJSONValues`; preserve C1's result and adjacent Write/array/Flush/Finish behavior. |
| `executor_stream.go` | C2 owns early non-nil error selection in `flushAndEmit`, and `processPayload` only under R8. Other lifecycle and framing setup functions remain regression targets. |
| `sse_rewriter.go`, `json_model.go` | Read-only reuse of existing framing, compaction example, JSON restoration, and whitelist boundaries. No production edits planned. |
| `stream_responses_raw_framing_regression_test.go` | C1's permanent root/callback regressions, adapted from its retained experiment. |
| `stream_lifecycle_regression_test.go` | C2's existing experimental regression, its controls, and the R8 focused behavioral test. |
| `stream_raw_number_regression_test.go` | C3's preserved partition/separator/immediate-container/helper regressions, migrated precisely from its reproducer tree. |
| `.github/scripts/testdata/cpa-functional-regression_test.go` | The sole permanent native fixture. C1 inserts its test/adapter before `TestModelMapperFunctionalResponsesWS`; C2 inserts before `TestModelMapperFunctionalNativeRequestDuplicates`; C3 inserts before `TestModelMapperFunctionalMetadataBuiltin`. C1 alone owns any necessary import edit; all currently used experiment imports already exist. |
| `.github/scripts/smoke-local.go`, `smoke-local_test.go` | Existing `TestCPAPluginIntegration` and overlay discovery remain unchanged. Temporary focused drivers and C1's separate experiment fixture are not permanent deliverables. |
| Existing root regression files and benchmarks | Preserve all old cases and oracles, including generic pretty JSON, raw/SSE/native boundaries, lifecycle ordering, limit, ownership, batching, allocation, and immediate-container checks. |
| `main.go`, `abi_cgo.go`, `config.go`, `model_router.go`, `rules.go`, `executor.go`, `go.mod`, `go.sum` | No edits. Exercise registration, schema 1, default version, exports, configuration, routing, headers, and cross-format execution through regression. |
| `Makefile`, `.github/workflows/build.yml`, release scripts | No edits. Reuse the pinned integration, VERSION injection, compatibility inspection, packaging, and release flow. |
| README, `CONCEPTS.md`, earlier docs, CPA source/module cache, initial user artifacts | Read-only context. No migration, host change, or unrelated cleanup. |

Reuse the verified C1 and C2 worktrees only after their owners check branch/HEAD, changed files, experiment ownership, and absence of another live writer. Preserve all present work. C1 and C2 can merge the documentation-only commit before coding; production remains at the source baseline. C3 starts a fresh independent worktree from C1's completed commit because their source ranges overlap. Read C3's three retained test files and migrate only its owned additions; never replace the entire shared fixture.

---

## Acceptance examples

- AE1. Covers R1-R2. With `grok-4.6=>grok-4.7`, empty host headers, and the retained three-LF completion, the actual native Responses WS client receives exactly one complete `response.completed`, with model `grok-4.6`, expected id/status/output, and normal turn completion/close. Direct, disabled, enabled-unmapped, compact mapped, and mapped pretty HTTP controls retain their healthy results.
- AE2. Covers R3-R4, R13. LF/CRLF/CR pretty JSON, blank lines, tabs, escaped strings and member names, duplicate members, Unicode, `1.00`, `1e+09`, and `9007199254740993` retain exact token bytes and opaque content except for allowed model replacement. Generic format-empty pretty framing, native SSE, other formats, and unframed/application-json retain their existing exact results.
- AE3. Covers R5-R7. After seventeen accepted native indexed raw units and terminal consumption of `1 2 [`, observe final-flush emission blocked before triggering unload. Unload returns without consumer progress; the consumer receives the accepted restored units in order and exactly one terminal reason beginning `flush stream rewriter: incomplete raw JSON stream`, without generic shutdown replacing it. Unaccepted forced-close output is not promised.
- AE4. Covers R6-R7, R13. Natural completion drains valid scalar output before the parse error; an earlier `primary upstream error` remains primary. Shutdown-first, natural-success-first, close failures, cleanup errors, emit-failure stopping, and close counts/order keep their existing assertions and deadlines.
- AE5. Covers R8. A real `processPayload` rewrite-error-plus-complete-prefix input reaches blocked emission before shutdown. Its own baseline wrong-reason assertion must fail before a corresponding production edit, then pass with natural/primary controls. A non-reproduction leaves that branch unchanged and is reported without an implementation claim.
- AE6. Covers R9-R11. For `1.5`, `1e+3`, `1E-3`, `-1.5`, `-1e+3`, `12.5`, and integer controls `1`/`-1`, whole input, every two-way split including empty parts, and bytewise input produce identical ordered output. Repeat with a preceding completed model-bearing object and exact leading/inter-value/trailing whitespace. Restore only whitelisted fields in the later object; retain numeric spellings and opaque content.
- AE7. Covers R10-R12. An unresolved trailing number waits until a boundary or EOF; a complete object/array still emits during Write. All five `application/json` executor-helper formats restore the model and close correctly. The actual native `openai` split-number test passes with healthy whole/direct/enabled-unmapped controls; framed and established unknown-SSE/native-type controls remain unchanged.
- AE8. Covers R14-R17. Reviewed owner commits combine without overwritten fixture ranges. Full repaired-source tests and same-library native integration retain all old cases/captures. Actual default registration reports `0.0.0-dev`; a separate injected artifact reports the selected patch. Authorized final-SHA CI/release and checked seven-platform packages agree, with actual runtime coverage and cleanup remainders stated separately.

---

## Verification and limits

The plan maps every R-ID and AE-ID to a responsible unit and observable checks. Owners must turn the preserved expected failures into permanent regressions, preserving their assertions across baseline/fixed runs. Current experiments are not repaired-source success. Setup failure, timeout, zero selected tests, and skipped native loading do not establish a failing functional oracle.

Preserve the complete existing test/benchmark/fixture membership and expanded cases. Keep the four capture sets and exact keys: `native-binary-captures.json` (1536), `mixed-native-captures.json` (1800), `mixed-binary-captures.json` (1440), and `mixed-shape-controls.json` (1680). Retain the complete 16,842,937-byte translator unit and every original deadline/limit. Counts alone do not establish membership.

Use existing native loaders, controlled providers, host callbacks, bridge helpers, the `github.com/gin-contrib/sse` decoder, and `encoding/json`. Test doubles supply external producer output; the native ABI, validator, bridge, HTTP handler, and WS consumer still execute. Keep exact wire bytes separately from semantic decoding. Record source/fixture identity, CPA revision/Sum, built/canonical library hashes, and actual loaded Windows shadow path/PID/hash. Canonical-path hashes alone cannot prove which library the child loaded.

No network-security scanning or repair, CPA development, new dependency, parser, performance campaign, real paid-provider request, or other-platform runtime claim is included. Final-SHA Ubuntu integration and seven-platform builds remain delivery gates; seven successful builds do not prove seven runtime loads. The numeric emission delay in R12 is limited to an unresolved trailing token. `processPayload` remains an execution-time evidence gate, not an unresolved planning decision.
