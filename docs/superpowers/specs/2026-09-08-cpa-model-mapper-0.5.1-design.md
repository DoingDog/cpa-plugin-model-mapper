# Model Mapper v0.5.1 Correctness and Performance Design

## Status

Approved for implementation under the unattended v0.5.1 audit and release task.

## Context

The v0.5.1 audit covered the plugin implementation, its tests and release tooling, the directly used CLIProxyAPI plugin boundary at `github.com/router-for-me/CLIProxyAPI/v7 v7.2.152`, and the relevant JSON, SSE, HTTP, and Go streaming contracts. The implementation already preserves the established request and response rewrite invariants, but one raw JSON streaming path can emit an incomplete JSON value. The release recipe can also continue after a compatibility rejection. Two hot-path operations perform work that is provably unnecessary.

This release is a surgical correction. It does not add formats, rule syntax, configuration fields, dependencies, or public APIs.

## Confirmed Problems

### Raw JSON is emitted before a complete value is available

`streamChunkRewriter.Write` tries raw JSON before SSE detection. `tryRawJSONChunks` currently treats an object with no `}` or an array with no `]` as a successful unchanged chunk. If a host stream returns one JSON value in multiple reads, the first read is emitted immediately and the continuation cannot be joined to it.

For example:

```plaintext
read 1: {"model":"up
read 2: stream"}
```

The client can receive invalid JSON and the upstream model is not restored. For an `openai-response` client whose upstream returns raw JSON, the same early emission also bypasses the required SSE framing.

A host stream payload is a byte chunk, not a JSON framing boundary. A valid JSON value must be accumulated until `encoding/json` can identify it as complete.

### Compatibility rejection does not stop packaging

The `package-platform` logical shell recipe runs the Linux or macOS compatibility checker and then the packager. Without shell fail-fast behavior, a nonzero checker status is followed by a successful packager status. GNU Make observes the last status and can report success while producing an incompatible archive.

The confirmed reproduction feeds a GLIBC 2.28 requirement to the checker with a configured maximum of 2.17. The checker returns 1, the subsequent packager succeeds, and the current logical recipe returns 0.

### Marker scanning performs suffix comparisons for every byte

`responseModelMarkerScanner.feed` updates its tail and calls two `bytes.HasSuffix` checks for every byte, even though both target markers end in `"`. A CPU profile of `BenchmarkStreamChunkRewriterCompleteSSEBatch` attributes 75.40% cumulative CPU to `mightContainResponseModelField`; its `bytes.HasSuffix -> bytes.Equal -> memeqbody` chain dominates this no-marker input.

Checking the suffixes only when the current byte is `"` is behaviorally identical and avoids the comparisons for all other bytes.

### Single-chunk batching copies an already owned chunk

`emitRewritten` concatenates all chunks when batching is required. It also allocates and copies when the slice contains exactly one non-empty chunk. Rewriter results already own their bytes, and the host emit call consumes them synchronously. Passing that one chunk directly removes one full-size allocation and copy without changing ownership or callback lifetime.

## Goals

1. Preserve complete raw JSON values across arbitrary host read boundaries.
2. Restore whitelisted response model fields only after a complete JSON value is available.
3. Frame a completed raw JSON value as SSE exactly once when the client format requires it.
4. Preserve incomplete or malformed bytes unchanged when the stream ends.
5. Stop `package-platform` immediately when a compatibility checker rejects a library.
6. Reduce marker-scanner CPU without changing marker recognition.
7. Eliminate the redundant single-chunk batch allocation and copy.
8. Retain all existing format, rule, header, model restoration, and stream-close behavior.

## Non-goals

The following audit candidates are deliberately excluded:

- `HostModelStreamReadResponse` payload plus terminal state handling. CLIProxyAPI v7.2.152 constructs payload and terminal error as separate `ModelExecutionChunk` values. Normal completion is a later empty read after the channel closes. The supported host does not produce the combined state.
- Strict rejection of malformed `config_yaml` envelope types. The supported host marshals `[]byte` as a valid base64 JSON string. Nil and empty byte slices legitimately encode empty configuration. Adding validation for types the host cannot send would add unsupported-caller behavior.
- A second response-model marker guard removal. Five benchmark repetitions show no material difference between the guarded restore and direct candidate paths, with identical bytes and allocations.
- Replacing the standard-library JSON decode and encode path. Allocation profiles show that meaningful savings require a new selective JSON parser with duplicate-key, escape, nesting, and whitelist semantics. That is not a safe micro-optimization.
- Reusing or merging per-event SSE output buffers. Existing output ownership and chunk-order contracts require independently owned rewritten event bytes. A larger representation change needs separate workload evidence and design.
- Caller wildcard cache limits or recovery changes. No behavior-equivalent bounded cache strategy was established, and the current authentication semantics take priority.
- ABI validation for malformed pointer and length descriptors that the supported host cannot construct.
- Header or format behavior changes. The audit found no supported-path defect in `Content-Length`, header preservation, or the five registered input and output formats.

## Design

### Incomplete raw JSON classification

The existing raw JSON parser will expose three outcomes in addition to an implementation error:

1. Complete JSON: return rewritten output.
2. Incomplete JSON: return no output and identify that more bytes may complete the value.
3. Not raw JSON: continue through existing SSE detection and unchanged fallback behavior.

`splitJSONValues` will distinguish `io.ErrUnexpectedEOF` from a syntax error. A syntax error remains the existing not-raw-JSON result so ordinary non-JSON bytes are not indefinitely buffered. The existing cheap object and array checks remain available for containers with no closing delimiter so the path does not introduce a second full-size decoder buffer merely to recognize a common incomplete value. That conservative fast path can also delay a malformed container with no closing delimiter until `Flush`; it still preserves the bytes unchanged and does not affect valid streams.

No custom JSON parser or new parser object is introduced. The standard library remains authoritative for complete JSON framing.

### Write behavior

`streamChunkRewriter.Write` keeps the existing BOM and pending-prefix handling. After merging pending bytes with the new host chunk, it applies these rules:

1. If the bytes can start a JSON value, try to parse one or more complete values.
2. If parsing succeeds, restore the whitelisted fields and return the same raw JSON or SSE-framed outputs used today.
3. If parsing reports `io.ErrUnexpectedEOF`, clone the bytes into `pending` and emit nothing.
4. If parsing reports a normal syntax mismatch, continue through SSE recognition and the unchanged raw fallback.

Buffering applies to one in-progress JSON sequence. There is no arbitrary byte limit because truncating or early-emitting a valid large JSON value would recreate the data-corruption bug. Memory use remains proportional to the incomplete value, which is required to restore a field that may occur anywhere in that value.

Multiple whitespace-delimited JSON values retain their current order. If a read contains complete values followed by an incomplete value, the complete prefix may remain buffered with the suffix until the next read; this delays output but prevents reordering and avoids a second incremental parser.

### Flush behavior

If `pending` can start a JSON value, `Flush` sends it through the raw JSON fallback instead of the SSE parser. A complete value is rewritten normally. A still-incomplete or malformed value is returned as an owned unchanged chunk, matching the existing compatibility policy for unrecognized bytes.

Pending SSE prefixes continue through `sseRewriter.Write` and `sseRewriter.Flush` exactly as before. BOM, LF, CRLF, bare CR, multi-`data:` events, unterminated SSE data, and split SSE field prefixes remain unchanged.

### Marker-scanner fast path

`responseModelMarkerScanner.feed` will call the two existing `bytes.HasSuffix` checks only when `b == '"'`. Both literal markers end with this byte, so the result is identical for every input. Escaped-key state tracking is unchanged and continues to recognize escaped `model` and `modelVersion` keys conservatively.

No marker set, tail layout, or response whitelist changes.

### Single-chunk batch emission

When `batch` is true:

- Zero chunks or one empty chunk emit nothing.
- One non-empty chunk is passed directly to `emit`.
- Two or more chunks retain the existing size calculation, one allocation, ordered concatenation, and one callback.

This fast path does not expose the host input buffer. Every chunk returned by the rewriters is already cloned or newly allocated under the existing ownership contract.

### Release fail-fast behavior

The logical compatibility-and-package recipe will begin with `set -e`. A nonzero Linux or macOS checker status then terminates that shell before archive creation. The checker is the final command in its pipeline, so this confirmed failure path does not require a shell-specific `pipefail` option.

The version validator and build remain separate Make recipe lines and preserve their current failure behavior. Archive names, root layout, checksums, platform matrix, GLIBC 2.17 maximum, and macOS 12.0 minimum remain unchanged.

## Error Handling

- A response-model rewrite error still terminates stream forwarding and closes the host stream.
- An incomplete JSON value is not an error while more host chunks can arrive.
- At flush, incomplete bytes are preserved instead of converted into an error or discarded.
- Host read errors still flush pending bytes before the host and plugin streams close.
- Emit and close errors retain their current propagation and ordering.
- Compatibility checker errors become the final package target result and prevent archive output.

## Testing

### Raw JSON regression tests

Tests will demonstrate the red state before production changes and then cover:

- An object split inside the `model` value emits nothing on the first write and one restored valid JSON value after continuation.
- The same split produces one correctly framed SSE event when `frameRawJSONAsSSE` is true.
- An array split across writes is buffered and reconstructed.
- A JSON-looking value that remains incomplete at `Flush` is returned unchanged and does not alias the caller input.
- Ordinary malformed non-JSON input retains the existing passthrough behavior.
- Existing multi-value, raw JSON, SSE delimiter, BOM, and model whitelist tests continue to pass.

### Release regression test

A behavioral Go test will invoke `make package-platform` with:

- a temporary prebuilt Linux library,
- the recursive build command replaced by a successful no-op,
- a temporary `readelf` replacement that reports GLIBC 2.28,
- the maximum left at GLIBC 2.17.

Before the Makefile change, the target succeeds and writes an archive. After the change, it must return nonzero and the archive must not exist. The test will skip only when the required local `make` and POSIX shell environment is unavailable.

### Performance verification

The existing correctness tests protect marker recognition. Benchmarks will measure the two changed hot paths:

- `BenchmarkStreamChunkRewriterCompleteSSEBatch`, five repetitions with `-benchmem`: median `ns/op` must decrease by at least 20% from the recorded 706046 ns/op baseline; `B/op` must remain near one owned payload and `allocs/op` must not exceed 3.
- A focused single-chunk `emitRewritten` batch benchmark: the change must remove the payload-sized allocation and copy. The callback count and bytes must remain unchanged.

The broader benchmark set will be rerun to detect material regressions. Timing comparisons use five same-machine repetitions and medians; allocation counts must be stable or lower.

## Files

The implementation is limited to:

- `main.go`
- `main_test.go`
- `performance_regression_test.go`
- `Makefile`
- `.github/scripts/check-release-compatibility_test.go`
- this specification and its implementation plan

No dependency or module-version change is required.

## Verification and Release

Before release, run the focused red and green tests, all package tests, `go vet`, explicit release-script tests, race tests, randomized repetitions, performance benchmarks, Windows amd64 build, and available Linux compatibility and packaging checks. Then merge the feature branch into local `main`, create and push `v0.5.1`, wait for the tag workflow, and verify all seven archives plus `checksums.txt` in the GitHub Release.
