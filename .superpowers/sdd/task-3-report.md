# Task 3 Report

status: done

changed files:
- main.go
- main_test.go

commit: pending

RED:
- `go test ./... -run TestApplyRules` failed with `undefined: applyRules` in `main_test.go`.

GREEN:
- `go test ./... -run TestApplyRules` passed.

full tests:
- `go test ./...` passed.

self-review:
- `applyRules` walks rules once in slice order, updates current after each match, and does not loop.
- Pattern matching is minimal and only supports literal segments plus `*` captures.
- Replacement only expands literal text and `$n` captures.

concerns:
- `matchTokens` is intentionally minimal; if future patterns need richer wildcard behavior, this will need expansion.
