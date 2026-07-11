# CPA Model Mapper Plugin

`model-mapper` is a CLIProxyAPI (CPA) native plugin. It maps text-generation request model names before CPA selects the upstream execution path, then restores supported response model fields back to the client-requested model only when a rule matched and changed the request.

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

Each ruleset is a `;`-separated list of `find=>replace` rules. Whitespace and quotes are invalid inside the decoded rule value.

- Matching is case-sensitive and applies to the complete model name; it is not substring or regular-expression matching.
- In `find`, `*` captures zero or more characters, including `/`, and captures are numbered from left to right. Wildcard matching does not backtrack: each capture stops at the first occurrence of the next literal. `$` is literal.
- In `replace`, `$1`, `$2`, and later numbers reuse captures. `*` is literal.
- Characters such as `@`, `/`, `[`, `]`, parentheses, dots, hyphens, and underscores are literal and need no escaping.
- Rules are order-sensitive: the selected ruleset runs left to right exactly once, and later rules see the model produced by earlier rules.
- Put more specific wildcard rules before broader fallback rules.
- In `find`, `\` escapes `*`, `;`, `$`, `\`, or `=>`; escaping `$` is accepted but unnecessary. In `replace`, `\=>` is the only backslash escape. Literal `\`, `;`, and `$` cannot be written directly in a replacement, but captures can carry them into the output.

YAML may single-quote the whole rule value. The outer quotes are removed before rule parsing and preserve backslashes; quote characters inside the decoded value remain invalid:

```yaml
global_rules: '@cf/zai-org/glm-4.7-flash=>glm-4.7-flash;deepseek-v4-pro[1m]=>deepseek-v4-pro'
```

Endpoint-specific rules override `global_rules` and do not stack with it:

- `claude` uses `claude_messages_rules` when non-empty.
- `openai-response` uses `codex_responses_rules` when non-empty.
- `openai` uses `openai_completions_rules` when non-empty.
- Other formats use `global_rules`.

### Examples

Claude-family fallback rules, useful in `claude_messages_rules`:

```text
claude-haiku-*=>gpt-5.4-mini;claude-sonnet-*=>gpt-5.4;claude-*=>gpt-5.5
```

Effects:

- `claude-haiku-4.5` -> `gpt-5.4-mini`
- `claude-sonnet-5` -> `gpt-5.4`
- `claude-opus-4` -> `gpt-5.5`

Compact OpenAI alias removal, useful in `codex_responses_rules` or `openai_completions_rules`:

```text
gpt-*-openai-compact=>gpt-$1
```

Effects:

- `gpt-5.5-openai-compact` -> `gpt-5.5`
- `gpt-5.4-mini-openai-compact` -> `gpt-5.4-mini`

Chained mapping runs in the written order:

```text
deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>gpt-5.4-mini
```

Effects:

- `deepseek-v4-pro` -> `gpt-5.4-mini`
- `deepseek-v4-flash` -> `gpt-5.4-mini`

Reversing those rules changes the result because rules do not loop back:

```text
deepseek-v4-flash=>gpt-5.4-mini;deepseek-v4-pro=>deepseek-v4-flash
```

Effects:

- `deepseek-v4-pro` -> `deepseek-v4-flash`
- `deepseek-v4-flash` -> `gpt-5.4-mini`

## Common use cases

- Use GPT or other upstream models from Claude-compatible clients, such as Claude Code, without changing the client-requested Claude model names.
- Keep client configuration stable while moving execution to newer, cheaper, or provider-specific model names.
- Expose compact or local aliases to clients, then strip the alias suffix before upstream execution.
- Chain temporary migrations, for example routing an old provider model name through an intermediate alias before its final upstream model.

## Build

```powershell
make test
make vet
make build-windows-amd64
make build-linux-amd64 LINUX_AMD64_CC=<cross-compiler>
make package VERSION=0.1.0
```

Full-platform release builds run in GitHub Actions for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`
- `freebsd/amd64`

Local artifacts commonly used for smoke checks:

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

## License

The Unlicense.

## Model list modification

Model-list modification is not implemented in this release. See `docs/model-list-modification-plan.md` for the required future CPA host hook.
