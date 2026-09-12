# CPA Model Mapper v0.5.5 Functional Audit Design

## Status

Date: 2026-09-12

This design covers only defects reproduced in the plugin repository. It does not modify CLIProxyAPI (CPA), upstream providers, or unrelated code.

## Audit outcome and responsibility boundary

The reported `claude-opus-5 -> Kimi` Responses failure is owned by CPA v7.2.152, not this plugin. CPA's OpenAI Chat Completions -> Responses translator can return adjacent events without trailing blank-line delimiters, while CPA's own Responses stream validator runs before `host.model.stream_read` exposes those bytes to the plugin. A package-level reproduction against the exact v7.2.152 dependency produced the reported `invalid SSE data JSON` shape with `}event:` inside one payload. The plugin cannot observe or repair those bytes, so v0.5.5 will not add a `}event:` workaround.

The audit confirmed five plugin-repository defects:

1. A cached wildcard caller decision can be reused by a later `model.route` call even when that later call has no currently presented credential bound to its trusted `caller_scope`.
2. A complete raw JSON value followed in the same host read by a partial SSE field prefix can bypass raw-JSON framing and response-model restoration.
3. Flushing an incomplete raw JSON value for an SSE response creates a complete, dispatchable SSE event whose `data` is invalid JSON.
4. The local smoke validator parses each physical `data:` line as an event instead of parsing SSE blank-line event boundaries and joining multiple data fields.
5. `make smoke-local` selects a host build target but runs its Go smoke helper with exported cross-target `GOOS`, `GOARCH`, and `CGO_ENABLED` still set.

No performance optimization is included because the baseline tests, race run, repeated runs, and focused allocation coverage did not identify a performance defect. The fixes must not introduce whole-stream buffering or a new dependency.

## Caller-pattern cache isolation

Wildcard caller rules need two different cache policies:

- `model.route` must require a credential in the current headers or query whose digest equals the trusted `caller_scope`, even if another request populated the cache.
- `executor.execute` and `executor.execute_stream` may reuse the route-stage cache when request interceptors replaced or removed that credential between the two CPA callbacks.

The implementation will preserve the existing public internal helpers for tests and route evaluation, while adding an internal execution mode that permits an unbound cache lookup only for executor callbacks. In safe route mode, credential binding is checked before a cache hit is accepted. Exact caller scopes, unscoped rules, inverse wildcard behavior, cache bounds, and the two-generation eviction policy remain unchanged.

A regression test will first warm both positive and inverse wildcard decisions with a correctly bound but nonmatching model. A second `model.route` request with the same trusted scope, a matching model, and no bound credential must remain unhandled. Existing executor tests must continue to prove that interceptor-replaced credentials reuse a handled route's cached decision.

## Mixed raw JSON and partial SSE prefixes

`streamChunkRewriter.Write` already handles a decoded raw JSON prefix followed by a complete SSE suffix. The same branch will also recognize a suffix that is only a valid partial SSE field prefix, such as `d`, `da`, `event`, or `id`. It will:

1. restore the whitelisted model fields in the complete JSON value;
2. frame that JSON value according to the target format;
3. pass the suffix to the existing `sseRewriter`, which already buffers transport-split events;
4. continue parsing when the next host read completes the field.

The change stays at the shared raw-JSON/SSE classification boundary. It will not normalize CPA's glued `}event:` translator output and will not treat transport read boundaries as event boundaries.

A partition regression will split `data: [DONE]\n\n` after every byte following a raw OpenAI JSON object. Every partition must produce exactly one restored framed JSON event and one unchanged `[DONE]` event. Existing format tests continue to cover `openai-response`, `claude`, `gemini`, and `interactions`; Gemini's existing unframed core-stream behavior is unchanged.

## Incomplete raw JSON at SSE flush

An incomplete raw JSON value cannot be converted into a valid SSE data event. When `frameRawJSONAsSSE` is true, `rawJSONChunks` will return an explicit incomplete-stream error and no chunks instead of calling `frameSSEData` on the fragment. This applies to clean completion and host read-error cleanup. The surrounding stream lifecycle already closes the plugin stream with the combined error, so no additional error event format is needed.

Unframed output remains unchanged: Gemini JSON arrays and WebSocket-like `application/json` streams may still flush their original fragment without an SSE wrapper. Unterminated but syntactically valid SSE data remains handled by `sseRewriter`; the fix is limited to raw JSON that was buffered because decoding reported an incomplete value.

The regression test must assert that an incomplete Responses JSON value yields an error and no dispatchable SSE bytes. Existing read-error tests must continue to prove that complete pending SSE data is restored and emitted before the error close.

## Local smoke SSE validation

`runStreamCase` will validate logical SSE events:

- CRLF and CR line endings are normalized to LF for parsing.
- A blank line dispatches one event.
- All `data` fields in that event are joined with LF, after removing at most one optional leading space after `:`.
- JSON decoding, in-band error checks, original-model checks, forbidden-model checks, and `[DONE]` tracking run once per dispatched event.
- A final unterminated data event is rejected rather than treated as dispatched.

This makes the smoke test reject adjacent `data:` fields without a blank-line boundary and accept a legal JSON payload split across multiple `data:` fields. The helper already buffers the complete HTTP response body, so this parser does not add a new stream-sized allocation boundary.

## Host execution environment for `smoke-local`

The `smoke-local` Make target will clear `GOOS`, `GOARCH`, and `CGO_ENABLED` only for the `go run .github/scripts/smoke-local.go` command. The preceding recursive `build-platform` invocation will continue to receive `go env GOHOSTOS` and `go env GOHOSTARCH` explicitly.

The existing fake-Go Makefile test will record both build and run environments. With exported cross-target values, the build must use the reported host target and the helper invocation must receive empty target overrides.

## Documentation and version

`README.md` and `CLAUDE.md` will describe the corrected flush boundary: complete pending SSE bytes are flushed, while incomplete raw JSON is rejected instead of emitted as invalid SSE. README package examples will use version `0.5.5`.

The release will use the existing linker-injected version mechanism and an annotated `v0.5.5` tag. No new version file or dependency will be added.

## Verification

The completed change must pass:

- focused red/green tests for caller cache isolation, mixed raw JSON/SSE partitions, and incomplete JSON flush;
- local smoke helper tests;
- release compatibility and Makefile tests;
- `go test ./...`;
- `go vet ./...`;
- `go test -race ./...`;
- repeated root tests;
- all three standalone `.github/scripts` test commands required by the repository;
- a host-platform plugin build and release packaging check.

After review and verification, the branch will be fast-forwarded into local `main`, tagged `v0.5.5`, and pushed with the tag. The GitHub Actions runs for `main` and `v0.5.5` must be observed; the tag run is the existing release path.

## Explicit non-goals and retained limits

- Do not modify CPA or copy CPA translator normalization into the plugin.
- Do not repair other CPA translator findings discovered during the dependency audit.
- Do not synthesize a universal `interactions` event name or termination marker.
- Do not change the documented non-backtracking wildcard DSL.
- Do not add an unbounded or request-pinned caller cache. CPA does not provide a route decision token to the executor, so reconfiguration between callbacks and eviction after two cache generations remain fail-closed limitations.
- Do not implement `executor.count_tokens` by calling `host.model.execute`. CPA routes count operations to a selected plugin executor but exposes no `host.model.count_tokens` callback; using normal model execution could issue a generation request instead of a token-count request.
- Do not add speculative handling for invalid ABI states, response trailers, partial-content responses, or `Cache-Control: no-transform` without a reachable plugin-owned failure.
