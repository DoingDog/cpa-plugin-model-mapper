# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common commands

- Run all unit tests: `make test` or `go test ./...`
- Run vet: `make vet` or `go vet ./...`
- Run one plugin test: `go test . -run TestName`
- Run release packager tests: `go test .github/scripts/package-release.go .github/scripts/package-release_test.go`
- Build Windows amd64 plugin: `make build-windows-amd64`
- Build Linux amd64 plugin from Windows with Zig: `make build-linux-amd64 LINUX_AMD64_CC="zig cc -target x86_64-linux-gnu"`
- Build/package one platform: `make package VERSION=0.1.2 GOOS=windows GOARCH=amd64`
- Package already-built artifacts into `dist/release/`: `make package VERSION=0.1.2`
- Run live local smoke: set `CPA_SMOKE_API_KEY` and `CPA_SMOKE_CPA_BIN`, then `make smoke-local`
- Clean build output: `make clean`

Do not run `go test ./.github/scripts`; that directory contains multiple `package main` scripts and will collide on duplicate `main`/`run` symbols. Test script files explicitly as shown above.

## Architecture overview

This is a single-package Go `c-shared` CLIProxyAPI native plugin. `abi_cgo.go` is the C ABI bridge: it exports `cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`, and `cliproxyPluginShutdown`, then forwards plugin method calls into `handleMethod` in `main.go`.

`main.go` contains the plugin logic:

- `pluginRegistration` advertises `model_router`, `executor`, and `executor.execute_stream` support for `openai`, `claude`, and `openai-response` formats.
- `decodeLifecycleConfig` and `decodeConfig` load plugin config from CPA lifecycle payloads. Config fields are `enabled`, `global_rules`, `claude_messages_rules`, `codex_responses_rules`, and `openai_completions_rules`.
- `selectRules` chooses an endpoint-specific ruleset when non-empty; otherwise it falls back to `global_rules`. Endpoint-specific rules do not stack with global rules.
- `parseRules` / `applyRules` implement an ordered entry DSL: entries are `find=>replace` mappings or exact standalone `\a` / `\A` ASCII case operations; `*` captures, `$1` references captures, and entries run left-to-right exactly once.
- `handleModelRoute` routes only when a mapping matched or case operation executed and the final requested model differs from the original; changed requests are routed back to this plugin executor.
- `handleExecutorExecute` and `runStreamForward` rewrite the outbound request body to the upstream model, call CPA host execution callbacks, then restore selected response model fields to the client-requested model.

Important model-rewrite invariants:

- Request rewriting intentionally changes only the top-level JSON `model` field.
- Response restoration is deliberately whitelisted to `model`, `modelVersion`, `response.model`, `response.modelVersion`, and `message.model`. Do not replace recursively through arbitrary content/tool text.
- Case operations change ASCII English letters only and do not make later mappings case-insensitive.
- Streaming responses pass through `streamChunkRewriter`, which handles complete SSE events, split SSE prefixes, unterminated SSE data at flush time, raw JSON chunks, line/space-delimited JSON values, and raw JSON that must be framed as SSE for Responses SSE clients.
- On host stream errors, pending rewritten bytes are flushed before closing the plugin stream so clients do not hang waiting for buffered output.

## Release and packaging

`pluginVersion` defaults to `0.0.0-dev` and is injected during release builds with `-X main.pluginVersion=$(VERSION)`. Keep `go.mod` module path and `pluginRegistration().Metadata.GitHubRepository` aligned with `github.com/DoingDog/cpa-plugin-model-mapper`.

`.github/scripts/package-release.go` is the packaging boundary. It supports:

- single-platform mode with `-library`, `-archive`, and `-checksum`
- aggregate mode with `-version`, `-dist`, and `-out`

Release zip files are named `model-mapper_<version>_<goos>_<goarch>.zip`, contain the dynamic library at zip root plus optional root `LICENSE`, and use sha256sum-format checksum lines with only the archive basename.

The GitHub Actions workflow runs tests/vet on PRs, builds all release platforms on non-PR events, and publishes only for `v*` tags. Global workflow permissions are `contents: read`; only the release job uses `contents: write`.

## Local state and documentation

Live smoke creates ignored local state under `.test-cpa/`; builds create ignored artifacts under `dist/`. Do not treat either directory as source.

`docs/solutions/` stores documented solutions to past project problems, organized by category with YAML frontmatter. Search it before changing documented areas such as release automation or model/stream rewriting. `CONCEPTS.md` defines project-specific vocabulary used by these docs.
