# Task 4 report

Implemented `selectRules` and `routeModel` in `main.go` with endpoint precedence and zero-decision gating.

Checks:
- `F:\go-sdk\go1.26.2\bin\go.exe test ./... -run "TestSelectRules|TestRouteModel"` ✅
- `F:\go-sdk\go1.26.2\bin\go.exe test ./...` ✅

Notes:
- Endpoint-specific rules do not stack with global rules.
- `routeModel` returns a zero decision for disabled, no rules, unmatched, and unchanged cases.
- Bad selected rules and empty mapped output still return errors.
