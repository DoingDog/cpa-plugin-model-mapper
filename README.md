# CPA Model Mapper Plugin

`model-mapper` is a CLIProxyAPI (CPA) native plugin. It maps text-generation request model names before CPA selects the upstream execution path, then restores supported response model fields back to the client-requested model only when a mapping matched or a case operation executed and the final model differs from the original.

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

Each ruleset is a `;`-separated ordered list of entries. Its grammar is `entry := [api-key#](\a|\A|find=>replace)`: an optional `api-key#` prefix scopes an entry to the authenticated inbound CPA client API key, not an upstream provider credential. Configured keys remain plaintext, so protect the plugin configuration. CPA exposes the authenticated `caller_scope` digest to the plugin; the plugin hashes configured plaintext keys using CPA's algorithm and compares digests. A mismatch skips only that entry and later entries continue. An unscoped entry is either a `find=>replace` mapping or an exact standalone case operation: `\a` lowercases ASCII English letters and `\A` uppercases them. Whitespace and quotes are invalid inside the decoded rule value.

Scoped rules require CPA v7.2.145 or a later compatible runtime that publishes authenticated `caller_scope`; when metadata is missing, scoped entries skip and unscoped fallback can continue.

- Mappings remain case-sensitive and apply to the complete current model name; later mappings see the value produced by every earlier entry.
- `\a` changes only `A` through `Z` to `a` through `z`; `\A` changes only `a` through `z` to `A` through `Z`. Non-ASCII bytes, digits, punctuation, and separators are unchanged.
- Case operations must be complete standalone entries. They are not additional backslash escapes for `find` or `replace`.
- In `find`, `*` captures zero or more characters, including `/`, and captures are numbered from left to right. Wildcard matching does not backtrack: each capture stops at the first occurrence of the next literal. `$` is literal.
- In `replace`, `$1`, `$2`, and later numbers reuse captures. `*` is literal.
- Characters such as `@`, `/`, `[`, `]`, parentheses, dots, hyphens, and underscores are literal and need no escaping.
- Entries are order-sensitive: the selected ruleset runs left to right exactly once, and later entries see the model produced by earlier entries.
- Put more specific wildcard rules before broader fallback rules.
- In `find`, `\` escapes `*`, `;`, `$`, `\`, `#`, or `=>`; escaping `$` is accepted but unnecessary. In `replace`, `\=>` and `\#` are backslash escapes. A literal model-name `#` in `find` or `replace` must be written as `\#`. Literal `\`, `;`, and `$` cannot be written directly in a replacement, but captures can carry them into the output.

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

Ordered ASCII case operations can normalize an incoming alias, feed a case-sensitive mapping, transform its output, and continue mapping:

```text
\a;gpt-*=>deepseek-V3;\A;DEEPSEEK-*=>gpt-5.5;\A
```

For `GPT-X`, the values are processed as:

```text
GPT-X -> gpt-x -> deepseek-V3 -> DEEPSEEK-V3 -> gpt-5.5 -> GPT-5.5
```

Use YAML single quotes so the DSL backslashes are preserved:

```yaml
global_rules: '\a;gpt-*=>deepseek-V3;\A;DEEPSEEK-*=>gpt-5.5;\A'
```

Authenticated API-key scope with an unscoped fallback, useful in `claude_messages_rules`:

```yaml
claude_messages_rules: 'sk-test#\a;sk-test#claude-haiku-*=>gpt-5.6-luna;claude-haiku-*=>gpt-5.6-sol'
```

Only the exact inbound client API key `sk-test` matches the scoped entries: `\a` lowercases the requested model and the scoped mapping selects `gpt-5.6-luna`. A different key or missing authenticated metadata skips the scoped entries; a lowercase `claude-haiku-*` then matches the unscoped fallback to `gpt-5.6-sol`. With a different key and an uppercase requested model, the scoped lowercase operation is skipped, so the case-sensitive fallback does not match.

## Common use cases

### 1. Use another Upstream Model from Claude Code and similar clients without changing the Client-Requested Model

Map a Client-Requested Model to an Upstream Model while keeping the client configuration unchanged.

### 2. Use wildcard, e.g. `claude-*=>gpt-5.4-mini`, to map several requested models to one Upstream Model

Use wildcard mappings when several Client-Requested Models should execute as one Upstream Model.

### 3. Route one authenticated inbound client API key to an administrator-selected lower-cost model, with an unscoped fallback for other keys

Add a scoped mapping for that inbound client API key before an unscoped mapping. The rewrite happens inside CPA and supported response model fields are restored to the Client-Requested Model. Other response fields, logs, and errors can still contain upstream model information. Actual cost depends on provider and model pricing.

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
