# CPA Model Mapper Plugin

`model-mapper` is a CLIProxyAPI (CPA) native plugin. It maps text-generation request model names before CPA selects the upstream execution path, then restores the top-level response `model` field back to the client-requested model only when a rule matched and changed the request.

When CPA invokes the plugin executor callbacks, the same mapping applies across non-streaming HTTP responses, SSE streams, and WebSocket-backed CPA streams that arrive as raw JSON chunks through the existing stream bridge.

The plugin does not register management/resource routes and does not use `/v0/resource/plugins/` for business logic or state-changing actions.

## Configuration

```yaml
plugins:
  enabled: true
  configs:
    model-mapper:
      enabled: true
      priority: 1
      global_rules: ""
      claude_messages_rules: ""
      codex_responses_rules: ""
      openai_completions_rules: ""
```

The plugin's own `enabled` field defaults to `true`. Empty rule fields mean the request is skipped and CPA behaves normally.

## Rule syntax

Rules are separated by `;`. A rule is `find=>replace`. Spaces and quotes are invalid.

- `*` captures text.
- `$1`, `$2`, and later numbers reuse captures.
- `\` escapes special characters.
- The selected ruleset runs left to right exactly once.

Example:

```text
deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>gpt-5.4-mini
```

Endpoint-specific rules override `global_rules` and do not stack with it:

- `claude` uses `claude_messages_rules` when non-empty.
- `openai-response` uses `codex_responses_rules` when non-empty.
- `openai` uses `openai_completions_rules` when non-empty.
- Other formats use `global_rules`.

## Build

```powershell
make test
make build-windows-amd64
make build-linux-amd64 LINUX_AMD64_CC=<cross-compiler>
make package VERSION=0.1.0
```

Artifacts:

- `dist/windows_amd64/model-mapper.dll`
- `dist/linux_amd64/model-mapper.so`

## Deploy

Windows CPA:

```text
<CPA directory>/plugins/windows/amd64/model-mapper.dll
```

Linux amd64 CPA:

```text
<CPA directory>/plugins/linux/amd64/model-mapper.so
```

## Smoke test

Live smoke uses only local ignored state under `.test-cpa/`.

Required environment variables:

- `CPA_SMOKE_API_KEY`
- `CPA_SMOKE_CPA_BIN`

Optional:

- `CPA_SMOKE_BASE_URL` defaults to `https://a3.awsl.app/v1`
- `CPA_SMOKE_PORT` defaults to `18080`

Run:

```powershell
make smoke-local
```

Do not commit `.test-cpa/`, `.env`, generated config, logs, or `dist/` artifacts.

## Model list modification

Model-list modification is not implemented in this release. See `docs/model-list-modification-plan.md` for the required future CPA host hook.
