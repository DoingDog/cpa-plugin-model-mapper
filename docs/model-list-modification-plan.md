# CPA Model List Modification Future Plan

**Status:** Deferred. This module is not part of the first implementation.

## Goal

Add a CPA plugin module that can modify model-list endpoint responses by deleting and adding model IDs from left to right using a comma-separated rule string.

## Why It Is Deferred

Current CPA plugin ABI lets standalone plugins contribute models through model provider/registrar hooks, but it does not let a plugin mutate the final aggregated `/models` response. A standalone plugin cannot reliably delete built-in or other-provider models from the final list.

## Required CPA Host Change

CPA needs a final model-list interception hook called after the model list is assembled and before the HTTP response is written.

Suggested interface shape:

```go
type ModelListInterceptor interface {
    InterceptModelList(context.Context, ModelListInterceptRequest) (ModelListInterceptResponse, error)
}

type ModelListInterceptRequest struct {
    SourceFormat string
    Models       []map[string]any
    Headers      http.Header
    Query        url.Values
}

type ModelListInterceptResponse struct {
    Models []map[string]any
}
```

The hook must cover all model-list surfaces, not only OpenAI `/v1/models`:

- OpenAI-compatible models.
- Claude-formatted models.
- Gemini models.
- Home-enabled models.
- Codex client-version model catalog paths.

## Plugin Configuration

When the host hook exists, add these fields to `model-mapper`:

```yaml
models_list_enabled: false
models_list_rules: ""
```

## Rule Syntax

- Rule string is comma-separated.
- Leading/trailing commas are ignored.
- Consecutive commas collapse to one separator.
- An item starting with one or more `-` deletes matching models.
- `*` matches zero or more characters.
- A non-delete item adds that model ID.
- Rules are applied from left to right.

Examples:

```text
-*
-claude-opus-4.6
gpt-5
-claude*,gpt-5
--opus-4
```

Expected behavior:

- `-*` deletes all models.
- `-claude-opus-4.6` deletes exactly that model.
- `gpt-5` appends `gpt-5` if absent.
- `-claude*,gpt-5` deletes Claude-prefixed models, then adds `gpt-5`.
- `--opus-4` is treated as `-opus-4`.

## Implementation Steps After Host Support Exists

1. Add the new CPA hook to host plugin SDK and endpoint handlers.
2. Add plugin config fields for `models_list_enabled` and `models_list_rules`.
3. Implement a small model-list parser separate from request mapping rules because the grammar is different.
4. Apply rules to final model IDs in order.
5. Preserve each model object's original fields for retained models.
6. Add new model objects with the smallest endpoint-compatible shape for the current `SourceFormat`.
7. Test deletion, wildcard deletion, addition, duplicate addition, multiple hyphens, empty comma segments, and rule ordering.

## Acceptance Criteria

- The module is disabled by default.
- It changes only model-list endpoints.
- It never affects text generation requests.
- It can delete all models with `-*`.
- It can add `gpt-5` after deleting Claude-prefixed models with `-claude*,gpt-5`.
- It treats `--opus-4` as `-opus-4`.
