# CPA Model Mapper WebSocket Follow-up Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support model rewrite/restore for CPA WebSocket-backed execution, especially Codex Responses over WebSocket, while still covering messages/responses/completions over non-streaming HTTP and SSE.

**Architecture:** CPA v7.2.48 exposes WebSocket-backed Codex through the existing `host.model.execute` / `host.model.execute_stream` callbacks, not a separate plugin WebSocket ABI. The plugin will keep rewriting `HostModelExecutionRequest.Model` and top-level request `model`, then expand stream response restoration from SSE-only to SSE-or-raw-JSON chunks so downstream WebSocket JSON events can have their top-level `model` restored. The plugin registers no management/resource routes, so `/v0/resource/plugins/` is not used.

**Tech Stack:** Go 1.26.2, CPA SDK v7.2.48, Go stdlib only, CGO c-shared builds, GNU Make, Zig 0.16 `zig cc` for Linux amd64 cross-build.

## Global Constraints

- Do not add a plugin-owned `/v0/resource/plugins/` route or any management/resource handler.
- Do not add dependencies.
- Keep response restoration limited to top-level JSON string field `model`.
- SSE events remain event-buffered; raw JSON chunks are rewritten per complete chunk only.
- If a chunk is neither SSE nor a complete JSON object, pass it through unchanged rather than buffering arbitrary WebSocket data.
- Linux amd64 build must use the locally available `zig cc`; do not skip Linux build/package unless `zig cc` invocation itself is verified to fail.
- Keep generated artifacts under ignored `dist/`; do not commit binaries, zips, checksums, `.test-cpa`, or secrets.

---

### Task WS1: Restore top-level model in raw JSON stream chunks

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `runStreamForward`, `sseRewriter`, `restoreResponseModel`.
- Produces: `streamChunkRewriter` with `Write([]byte) ([][]byte, error)` and `Flush() ([][]byte, error)`.

- [ ] Write failing tests for raw JSON WebSocket-like chunks: host stream emits `{"type":"response.completed","model":"gpt-5.4-mini"}` and plugin emits `model":"deepseek-v4-pro"`.
- [ ] Write failing test that existing SSE chunk behavior still restores model and preserves `data: [DONE]`.
- [ ] Implement a small `streamChunkRewriter` that delegates SSE chunks to `sseRewriter` when chunk starts with `data:`/`event:`/`:` or contains SSE event delimiters; otherwise tries `restoreResponseModel` on the complete chunk and emits rewritten JSON if changed, original chunk if not JSON/no model.
- [ ] Use `streamChunkRewriter` in `runStreamForward`.
- [ ] Run `go test ./...` with local Go/cache env.
- [ ] Commit `feat: restore websocket stream model chunks`.

### Task WS2: Document WebSocket coverage and resource-route safety

**Files:**
- Modify: `README.md`
- Create: `.superpowers/sdd/ws-followup-report.md` (untracked report only)

**Interfaces:**
- Produces documentation that WebSocket-backed Codex Responses is supported through CPA's existing stream callback bridge and that the plugin registers no `/v0/resource/plugins/` endpoints.

- [ ] Update README protocol coverage: non-streaming HTTP, SSE, and WebSocket-backed CPA streams are covered when CPA invokes plugin executor callbacks.
- [ ] Add safety note: `model-mapper` does not register management/resource routes and does not use `/v0/resource/plugins/` for business logic or state-changing actions.
- [ ] Run `git grep -n "ResourceRoute\|ManagementRoute\|MethodManagement\|/v0/resource/plugins" -- main.go README.md` and verify only README safety text appears outside SDK/docs.
- [ ] Run `git diff --check -- README.md`.
- [ ] Commit `docs: document websocket coverage`.

### Task WS3: Build Windows and Linux amd64 artifacts with Zig

**Files:**
- Generated ignored: `dist/windows_amd64/model-mapper.dll`
- Generated ignored: `dist/linux_amd64/model-mapper.so`
- Generated ignored: `dist/release/*.zip`, `dist/release/*.sha256`

**Interfaces:**
- Produces Windows amd64 and Linux amd64 plugin binaries plus release zips.

- [ ] Run `zig version` and `zig cc --version`.
- [ ] Run `make test GO="F:/go-sdk/go1.26.2/bin/go.exe"`.
- [ ] Run `make build-windows-amd64 GO="F:/go-sdk/go1.26.2/bin/go.exe"`.
- [ ] Run `make build-linux-amd64 GO="F:/go-sdk/go1.26.2/bin/go.exe" LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"`. If Make cannot handle a CC value with spaces, use the equivalent direct Go command with `CC="zig cc -target x86_64-linux-gnu"` and record why.
- [ ] Verify both artifact files exist.
- [ ] Run `make package VERSION=0.1.0 GO="F:/go-sdk/go1.26.2/bin/go.exe"`.
- [ ] Verify zip contents and `.sha256` files.
- [ ] Do not commit generated artifacts.
