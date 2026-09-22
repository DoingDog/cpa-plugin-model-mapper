---
title: Keep OpenAI chat/completions stream payloads unframed
date: 2026-09-22
category: stream-framing
module: stream forwarding
problem_type: integration_error
component: stream_chunk_rewriter
severity: high
applies_when:
  - Mapping a model for an OpenAI chat/completions client that streams
  - Deciding whether the plugin or CLIProxyAPI owns SSE framing for a protocol
symptoms:
  - Client receives "data: data: {...}" instead of "data: {...}"
  - Client receives two "data: [DONE]" terminators on one mapped stream
  - OpenAI-compatible downstream parsers fail every streamed turn with a JSON parse error
root_cause: wrong_responsibility_boundary
resolution_type: bug_fix
related_components:
  - host_stream_bridge
  - openai_handler
tags:
  - model-mapper
  - cpa-plugin
  - sse
  - framing
  - openai
  - streaming
---

# Keep OpenAI chat/completions stream payloads unframed

## Context

The rewriter selected SSE framing with `req.Format != "gemini" && isEventStreamContentType(headers.Get("Content-Type"))`, so every OpenAI chat/completions stream framed its raw JSON payloads as `data: %s\n\n` and appended `data: [DONE]` at `Finish()`.

CLIProxyAPI's OpenAI handler writes that framing itself. In `sdk/api/handlers/openai/openai_handlers.go` it calls `setSSEHeaders()` and then writes one `data: %s\n\n` per payload, plus `data: [DONE]\n\n` when the upstream stream ends. The host bridge hands the plugin the payloads, not the client framing, so a payload framed by the plugin is framed a second time on the way out.

Observed on a mapped `gpt-5.6-sol` request against CPA v7.3.4 with v0.5.6 loaded:

| Signal | v0.5.6 | after the fix |
| --- | --- | --- |
| first 7 bytes | `64 61 74 61 3a 20 64 61 74 61 3a 20` (`data: data: `) | `64 61 74 61 3a 20 7b` (`data: {`) |
| `data: data:` occurrences | 19 | 0 |
| `[DONE]` frames | 2 | 1 |

The same framing exists in the CPA version this plugin pins (`v7.2.152`), so this is a responsibility boundary, not a version difference.

## Guidance

Decide framing per protocol, in one place:

```go
stream.frameRawJSONAsSSE = executorStreamNeedsSSEFraming(req.Format) && isEventStreamContentType(headers.Get("Content-Type"))
```

`openai` and `gemini` return false: CPA frames both. `claude`, `openai-response`, and `interactions` return true because their clients receive the payloads verbatim.

Keep the rule in a named helper rather than an inline format comparison. The comparison reads like a Gemini special case, which is how OpenAI framing was introduced in the first place.

## Why This Matters

The double frame is not a cosmetic defect. An OpenAI-compatible parser strips one `data: ` prefix, sees `data: {...}` where it expects JSON, and fails the whole turn. A second `[DONE]` can end a client stream early or trip strict stream validators. Both failures only appear on mapped requests, so an unmapped control request looks healthy.

## When to Apply

- Adding a protocol to `pluginRegistration().Metadata.SupportedFormats`.
- Changing the framing decision in `prepareExecutorStream`.
- Reading a bug report about `data: data:` or duplicated `[DONE]`.

## Examples

Framing stays on for the protocols whose clients see the payloads directly:

```go
func TestExecutorStreamNeedsSSEFraming(t *testing.T) {
	// openai and gemini: false, CPA frames them.
	// claude, openai-response, interactions: true.
}
```

`TestPrepareExecutorStreamOpenAIChatKeepsRawJSONChunksUnframed` covers the end-to-end shape: the prepared stream reports `frameRawJSONAsSSE == false`, the restored model reaches the client, and `Finish()` appends no `[DONE]`.

## Related

- `README.md`, stream behavior section
- `sdk/api/handlers/openai/openai_handlers.go` in CLIProxyAPI
