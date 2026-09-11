# CPA model-mapper v0.5.4 Functional Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correct every executable defect confirmed by the 2026-09-11 audit, prove the five-format request/response and streaming contracts, and publish the verified patch as `v0.5.4`.

**Architecture:** Keep the existing single-package plugin and its existing release helpers. Apply narrow fixes at shared boundaries: registration negotiation, route admission, config decoding, stream framing/parsing, body-dependent headers, checked artifact sidecars, checksum publication, and local-smoke setup. Add no dependency and do not modify CPA.

**Tech Stack:** Go 1.26, Go standard library, `gopkg.in/yaml.v3`, CPA plugin SDK v7.2.152, GNU Make/POSIX shell, GitHub Actions, GitHub CLI.

**Spec:** `docs/superpowers/specs/2026-09-11-cpa-model-mapper-0.5.4-functional-audit-design.md`

## Global Constraints

- Modify only `C:\Users\user\Downloads\cpa-plugin`; do not edit `upstream/CLIProxyAPI` or any CPA source.
- Keep all five advertised formats: `openai`, `openai-response`, `claude`, `gemini`, and `interactions`.
- Registration must declare RPC schema 1; the C ABI remains version 1.
- Request rewriting remains limited to the top-level string `model` field.
- Response restoration remains limited to `model`, `modelVersion`, `response.model`, `response.modelVersion`, `message.model`, and `interaction.model`.
- Do not add a dependency or replace the existing streaming/parser architecture.
- Preserve unchanged bodies, SSE events, separators, and unrelated headers byte-for-byte where the existing contract requires it.
- Never run `go test ./.github/scripts`; run each script and its test file explicitly.
- Development follows strict TDD: add a focused failing test, run it and record the expected failure, make the smallest production change, then rerun the focused and surrounding tests.
- Three implementation streams may run in parallel with exclusive ownership: runtime owns `main.go` and `main_test.go`; release automation owns `Makefile`, `.github/scripts/package-release*.go`, `.github/scripts/check-release-compatibility_test.go`, and `.github/workflows/build.yml`; smoke helper owns `.github/scripts/smoke-local*.go`. Documentation runs after those streams finish. Agents must not read or edit another stream's files.
- Implementation agents use `claude-sonnet-5[1m]` with `xhigh` effort. Review and plan checks use `claude-opus-5[1m]` with `xhigh` effort.
- Preserve the task-start untracked `.claude/` directory and never stage it.
- Do not read any earlier spec or plan while executing this plan; only this plan and its linked 2026-09-11 spec are in scope.

## File Map

- `main.go`: registration, route RPC, lifecycle config decoding, request/response header normalization, SSE rewriting, raw JSON stream rewriting, and Gemini framing.
- `main_test.go`: permanent runtime and wire-contract regression tests.
- `.github/scripts/package-release.go`: single-platform and aggregate packaging safety.
- `.github/scripts/package-release_test.go`: path-alias, checksum, aggregate, and workflow-source assertions.
- `.github/scripts/check-release-compatibility_test.go`: Make compatibility/sidecar and inspector-exit tests.
- `.github/scripts/smoke-local.go`: host-platform path derivation, CPA launch, and streamed-response validation.
- `.github/scripts/smoke-local_test.go`: local-smoke helper regressions.
- `Makefile`: sidecar provenance, inspector status propagation, and host-platform smoke build.
- `.github/workflows/build.yml`: conditional GitHub prerelease metadata.
- `README.md`: v0.5.4 commands and the corrected route, stream, header, and packaging boundaries.
- `docs/superpowers/specs/2026-09-11-cpa-model-mapper-0.5.4-functional-audit-design.md`: approved design; no implementation edits unless a factual contradiction is discovered.
- `docs/superpowers/plans/2026-09-11-cpa-model-mapper-0.5.4-functional-audit-implementation.md`: this checklist; mark completed steps during execution.

---

### Task 1: Registration and Interactions route admission

**Files:**
- Modify: `main.go:848-891,1094-1129`
- Test: `main_test.go:25-65` and route tests near the existing model-router suite

**Interfaces:**
- Consumes: `pluginapi.ModelRouteRequest.Body []byte` and existing `pluginRegistration() registration`.
- Produces: `const pluginRPCSchemaVersion uint32 = 1`, `interactionsUsesAgent([]byte) bool`, and an unchanged `handleModelRoute([]byte) ([]byte, error)` signature.

- [ ] **Step 1: Change the registration test first**

In `TestPluginRegistrationMetadataAndConfigFields`, replace only the schema assertion:

```go
if got.SchemaVersion != 1 {
	t.Fatalf("schema version=%d, want 1", got.SchemaVersion)
}
```

Add a lifecycle request carrying a newer host schema field:

```go
func TestPluginRegisterKeepsSchemaOneForNewerHost(t *testing.T) {
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
	rawYAML := base64.StdEncoding.EncodeToString([]byte("global_rules: a=>b\n"))
	raw, err := json.Marshal(map[string]any{"config_yaml": rawYAML, "schema_version": 6})
	if err != nil {
		t.Fatal(err)
	}
	response, err := handlePluginRegister(raw)
	if err != nil {
		t.Fatal(err)
	}
	var got registration
	if err := json.Unmarshal(response, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 {
		t.Fatalf("schema version=%d, want 1", got.SchemaVersion)
	}
}
```

- [ ] **Step 2: Run the schema tests and confirm the red state**

Run:

```powershell
go test . -run 'TestPluginRegistrationMetadataAndConfigFields|TestPluginRegisterKeepsSchemaOneForNewerHost' -count=1
```

Expected: FAIL because registration reports dependency schema 5.

- [ ] **Step 3: Declare the plugin's actual RPC schema**

Add next to the registration constants and use it in `pluginRegistration`:

```go
const pluginRPCSchemaVersion uint32 = 1
```

Set the existing registration literal field to:

```go
SchemaVersion: pluginRPCSchemaVersion,
```

Remove the `pluginabi.SchemaVersion` use only if it becomes unused; do not change other SDK constants.

- [ ] **Step 4: Add failing Interactions route tests**

Add a table that proves only non-empty string `agent` requests bypass this executor:

```go
func TestHandleModelRouteLeavesInteractionsAgentsNative(t *testing.T) {
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	for _, tt := range []struct {
		name    string
		body    string
		handled bool
	}{
		{name: "agent", body: `{"agent":"client"}`, handled: false},
		{name: "empty agent with model", body: `{"agent":"","model":"client"}`, handled: true},
		{name: "model", body: `{"model":"client"}`, handled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(pluginapi.ModelRouteRequest{
				SourceFormat: "interactions", RequestedModel: "client", Body: []byte(tt.body),
			})
			if err != nil { t.Fatal(err) }
			responseRaw, err := handleModelRoute(raw)
			if err != nil { t.Fatal(err) }
			var response pluginapi.ModelRouteResponse
			if err := json.Unmarshal(responseRaw, &response); err != nil { t.Fatal(err) }
			if response.Handled != tt.handled {
				t.Fatalf("Handled=%v, want %v", response.Handled, tt.handled)
			}
		})
	}
}
```

- [ ] **Step 5: Run the Interactions test and confirm the red state**

Run:

```powershell
go test . -run TestHandleModelRouteLeavesInteractionsAgentsNative -count=1
```

Expected: FAIL for `agent` because `modelRouteRPCRequest` discards `Body` and routing returns handled.

- [ ] **Step 6: Retain the route body and add the native-agent guard**

Use this minimal shape:

```go
type modelRouteRPCRequest struct {
	SourceFormat   string
	RequestedModel string
	Headers        http.Header
	Query          url.Values
	Body           []byte
	Metadata       map[string]any
}

func interactionsUsesAgent(body []byte) bool {
	var request struct {
		Agent string `json:"agent"`
	}
	return json.Unmarshal(body, &request) == nil && request.Agent != ""
}

// Insert immediately after the existing json.Unmarshal guard in handleModelRoute.
if req.SourceFormat == "interactions" && interactionsUsesAgent(req.Body) {
	return json.Marshal(pluginapi.ModelRouteResponse{Handled: false})
}
```

- [ ] **Step 7: Run focused and model-router tests**

Run:

```powershell
go test . -run 'TestPluginRegistration|TestHandleModelRoute|TestRoute|TestRuleSelection' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 1**

```powershell
git add -- main.go main_test.go
git commit -m "fix: preserve baseline plugin routing compatibility"
```

---

### Task 2: Reject null rule fields in JSON and YAML

**Files:**
- Modify: `main.go:834-913,1668-1731`
- Test: `main_test.go:67-368`

**Interfaces:**
- Consumes: existing `decodeConfig(json.RawMessage)` and `decodeLifecycleConfig([]byte)`.
- Produces: `yamlStringConfigField(yaml.Node, string) (string, error)`; public/internal call signatures remain unchanged.

- [ ] **Step 1: Add table-driven JSON and YAML type tests**

```go
func TestRuleConfigFieldsRequireStrings(t *testing.T) {
	fields := []string{"global_rules", "claude_messages_rules", "codex_responses_rules", "openai_completions_rules"}
	for _, field := range fields {
		t.Run(field+"/json-null", func(t *testing.T) {
			if _, err := decodeConfig(json.RawMessage(`{"` + field + `":null}`)); err == nil || !strings.Contains(err.Error(), field+" must be a string") {
				t.Fatalf("decodeConfig error=%v", err)
			}
		})
		t.Run(field+"/yaml-null", func(t *testing.T) {
			rawYAML := []byte(field + ": null\n")
			raw, err := json.Marshal(map[string]string{"config_yaml": base64.StdEncoding.EncodeToString(rawYAML)})
			if err != nil { t.Fatal(err) }
			if _, _, err := decodeLifecycleConfig(raw); err == nil || !strings.Contains(err.Error(), field+" must be a string") {
				t.Fatalf("decodeLifecycleConfig error=%v", err)
			}
		})
	}
}
```

Add these positive assertions to the same test so the new validation does not reject omission or explicit empty strings:

```go
if _, err := decodeConfig(json.RawMessage(`{}`)); err != nil {
	t.Fatalf("omitted direct fields: %v", err)
}
if _, err := decodeConfig(json.RawMessage(`{"global_rules":"","claude_messages_rules":"","codex_responses_rules":"","openai_completions_rules":""}`)); err != nil {
	t.Fatalf("empty direct fields: %v", err)
}
for name, source := range map[string]string{
	"omitted": "rules_stack_mode: off\n",
	"empty":   "global_rules: ''\nclaude_messages_rules: ''\ncodex_responses_rules: ''\nopenai_completions_rules: ''\n",
} {
	t.Run(name+"/yaml-valid", func(t *testing.T) {
		raw, err := json.Marshal(map[string]string{"config_yaml": base64.StdEncoding.EncodeToString([]byte(source))})
		if err != nil { t.Fatal(err) }
		if _, _, err := decodeLifecycleConfig(raw); err != nil {
			t.Fatalf("decodeLifecycleConfig error=%v", err)
		}
	})
}
```

- [ ] **Step 2: Prove both null paths fail before implementation**

Run:

```powershell
go test . -run TestRuleConfigFieldsRequireStrings -count=1
```

Expected: FAIL because JSON and YAML `null` decode to empty strings.

- [ ] **Step 3: Validate present direct-JSON string fields**

After decoding `fields`, use one loop before unmarshalling `cfg`:

```go
for _, name := range []string{
	"global_rules",
	"claude_messages_rules",
	"codex_responses_rules",
	"openai_completions_rules",
	"rules_stack_mode",
} {
	if value, ok := fields[name]; ok {
		value = bytes.TrimSpace(value)
		if len(value) == 0 || value[0] != '"' {
			return Config{}, fmt.Errorf("%s must be a string", name)
		}
	}
}
```

Remove the old one-field `rules_stack_mode` branch now covered by the shared loop.

- [ ] **Step 4: Decode YAML rule fields as typed nodes**

Change all five YAML-owned fields in `lifecycleYAMLConfig` to `yaml.Node`, then decode with:

```go
func yamlStringConfigField(node yaml.Node, name string) (string, error) {
	if node.Kind == 0 {
		return "", nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("%s must be a string", name)
	}
	return node.Value, nil
}
```

Call it for each field and build `Config` from the returned strings. Return immediately on the first type error. Keep the one-document YAML check unchanged.

- [ ] **Step 5: Add and run atomic reconfiguration coverage**

Add the atomic reconfiguration test:

```go
func TestInvalidLifecycleRuleTypeKeepsActiveConfig(t *testing.T) {
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
	setLoadedConfigForTest(Config{GlobalRules: "old=>target"})
	raw, err := json.Marshal(map[string]string{
		"config_yaml": base64.StdEncoding.EncodeToString([]byte("global_rules: null\n")),
	})
	if err != nil { t.Fatal(err) }
	if _, err := handlePluginReconfigure(raw); err == nil {
		t.Fatal("handlePluginReconfigure error=nil")
	}
	decision, err := routeModel(loadedConfig(), "openai", "old", "", "")
	if err != nil { t.Fatal(err) }
	if !decision.Handled || decision.UpstreamModel != "target" {
		t.Fatalf("decision=%#v, want old config route to target", decision)
	}
}
```

Run:

```powershell
go test . -run 'TestRuleConfigFieldsRequireStrings|TestInvalidLifecycleRuleTypeKeepsActiveConfig|TestDecodeLifecycleConfig' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 2**

```powershell
git add -- main.go main_test.go
git commit -m "fix: reject non-string rule configuration"
```

---

### Task 3: Make SSE rewriting partition-invariant and valid

**Files:**
- Modify: `main.go:219-305,455-543`
- Test: `main_test.go` near existing SSE multi-data and partition tests

**Interfaces:**
- Consumes: `sseRewriter.rewriteMultiDataEvent` and `streamChunkRewriter.Write`.
- Produces: no new exported interface; changed multi-data JSON is one compact `data:` field.

- [ ] **Step 1: Add a leading-whitespace partition regression**

```go
func TestStreamChunkRewriterLeadingWhitespacePartitionInvariant(t *testing.T) {
	input := []byte(" data: {\"model\":\"upstream\"}\n\n")
	rewrite := func(parts ...[]byte) []byte {
		r := newStreamChunkRewriter("client")
		r.frameRawJSONAsSSE = true
		var chunks [][]byte
		for _, part := range parts {
			written, err := r.Write(part)
			if err != nil { t.Fatal(err) }
			chunks = append(chunks, written...)
		}
		flushed, err := r.Flush()
		if err != nil { t.Fatal(err) }
		return bytes.Join(append(chunks, flushed...), nil)
	}
	whole := rewrite(input)
	for split := 0; split <= len(input); split++ {
		if got := rewrite(input[:split], input[split:]); !bytes.Equal(got, whole) {
			t.Fatalf("split %d output=%q, want %q", split, got, whole)
		}
	}
}
```

- [ ] **Step 2: Confirm the whitespace test is red**

Run:

```powershell
go test . -run TestStreamChunkRewriterLeadingWhitespacePartitionInvariant -count=1
```

Expected: FAIL when the first write contains only the leading space.

- [ ] **Step 3: Buffer whitespace-only reads in both modes**

Change only the guard at the end of `Write`:

```go
if len(p) > 0 && len(bytes.Trim(p, " \t\r\n")) == 0 {
	if owned {
		r.pending = p
	} else {
		r.pending = append(r.pending, p...)
	}
	return nil, nil
}
```

Retain the existing ownership branches to avoid aliasing host buffers.

- [ ] **Step 4: Add a changed multi-data array regression**

```go
func TestSSERewriterChangedMultiDataArrayRemainsValidSSE(t *testing.T) {
	input := []byte("data: [\ndata: {\"model\":\"upstream\"}\ndata: ]\n\n")
	chunks, err := newSSERewriter("client").Write(input)
	if err != nil { t.Fatal(err) }
	output := bytes.Join(chunks, nil)
	var data [][]byte
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("data:")) {
			data = append(data, bytes.Clone(sseFieldValue(line)))
		}
	}
	joined := bytes.Join(data, []byte{'\n'})
	if !json.Valid(joined) || bytes.Contains(joined, []byte("upstream")) || !bytes.Contains(joined, []byte("client")) {
		t.Fatalf("output=%q joined-data=%q", output, joined)
	}
}
```

- [ ] **Step 5: Confirm the multi-data test is red**

Run:

```powershell
go test . -run TestSSERewriterChangedMultiDataArrayRemainsValidSSE -count=1
```

Expected: FAIL because only the first physical line has a `data:` prefix.

- [ ] **Step 6: Compact only a changed multi-data JSON value**

Immediately after `changed` is confirmed, compact into a buffer:

```go
var compact bytes.Buffer
if err := json.Compact(&compact, restored); err != nil {
	return nil, err
}
restored = compact.Bytes()
```

Do not compact the unchanged path. Keep non-data fields and the event delimiter behavior unchanged.

- [ ] **Step 7: Run the focused SSE suite**

```powershell
go test . -run 'TestSSERewriter|TestStreamChunkRewriter.*(Partition|Boundary|BOM|SSE)' -count=1
```

Expected: PASS, including unchanged-event byte-preservation tests.

- [ ] **Step 8: Commit Task 3**

```powershell
git add -- main.go main_test.go
git commit -m "fix: preserve SSE data across stream partitions"
```

---

### Task 4: Incrementally rewrite unframed top-level JSON arrays

**Files:**
- Modify: `main.go:455-724`
- Test: `main_test.go:4015-4048,4415-4498` and adjacent raw JSON tests

**Interfaces:**
- Consumes: `skipTopLevelModelJSONSpace`, `skipTopLevelModelJSONValue`, and `sseRewriter.restoreResponseModel`.
- Produces: `streamChunkRewriter.rawJSONArray bool` and `(*streamChunkRewriter).writeRawJSONArray([]byte, bool) ([][]byte, error)`.

- [ ] **Step 1: Add an incremental-delivery regression**

```go
func TestStreamChunkRewriterEmitsCompletedRawJSONArrayElements(t *testing.T) {
	r := newStreamChunkRewriter("client")
	r.format = "gemini"
	first, err := r.Write([]byte(`[{"modelVersion":"upstream","id":1},`))
	if err != nil { t.Fatal(err) }
	firstOutput := bytes.Join(first, nil)
	if len(firstOutput) == 0 || !bytes.Contains(firstOutput, []byte(`"modelVersion":"client"`)) {
		t.Fatalf("first output=%q", firstOutput)
	}
	second, err := r.Write([]byte(`{"modelVersion":"upstream","id":2}]`))
	if err != nil { t.Fatal(err) }
	all := bytes.Join(append(first, second...), nil)
	if !json.Valid(all) || bytes.Contains(all, []byte("upstream")) {
		t.Fatalf("output=%q", all)
	}
}
```

- [ ] **Step 2: Confirm the incremental test is red**

Run:

```powershell
go test . -run TestStreamChunkRewriterEmitsCompletedRawJSONArrayElements -count=1
```

Expected: FAIL because the first write emits nothing.

- [ ] **Step 3: Add edge-case tests before parser code**

Add table-driven tests for:

```go
[]string{
	` [ {"modelVersion":"upstream","text":"] , } ["}, {"modelVersion":"upstream"} ] `,
	`["scalar",[ {"modelVersion":"must-stay-upstream"} ],{"modelVersion":"upstream"}]`,
	`[{"modelVersion":"upstream"}, {"unfinished":"value`,
}
```

Assertions:

- Every complete direct object is restored before the final bracket arrives.
- Brackets and commas inside strings do not split an element.
- Nested-array elements remain byte-identical.
- Joining all output preserves whitespace and separators.
- `Flush` on the incomplete case returns every received byte and does not add `]`, `}`, a newline, or SSE framing.
- For each short fixture above, all two-way split positions and every ordered three-way split pair `(first, second)` with `0 <= first <= second <= len(input)` produce the same joined result.

Run the new edge test and retain its expected failure until Step 5.

- [ ] **Step 4: Add array state and dispatch only for unframed arrays**

Add this field to `streamChunkRewriter` after `pending`:

```go
rawJSONArray bool
```

In `Write`, after combining `pending` and before generic raw-JSON parsing:

```go
if r.rawJSONArray {
	return r.writeRawJSONArray(p, false)
}
start := skipTopLevelModelJSONSpace(p, 0)
if !r.frameRawJSONAsSSE && start < len(p) && p[start] == '[' {
	return r.writeRawJSONArray(p, true)
}
```

Keep framed top-level arrays on the existing complete-value path so one JSON value becomes one valid SSE event.

- [ ] **Step 5: Implement the minimum array-element scanner**

`writeRawJSONArray` must follow this exact state machine:

```go
func (r *streamChunkRewriter) writeRawJSONArray(p []byte, opening bool) ([][]byte, error) {
	var out [][]byte
	cursor := 0
	if opening {
		start := skipTopLevelModelJSONSpace(p, 0)
		out = append(out, bytes.Clone(p[:start+1]))
		cursor = start + 1
		r.rawJSONArray = true
	}
	for cursor < len(p) {
		start := skipTopLevelModelJSONSpace(p, cursor)
		if start == len(p) {
			out = append(out, bytes.Clone(p[cursor:start]))
			return out, nil
		}
		if p[start] == ',' {
			out = append(out, bytes.Clone(p[cursor:start+1]))
			cursor = start + 1
			continue
		}
		if p[start] == ']' {
			out = append(out, bytes.Clone(p[cursor:start+1]))
			r.rawJSONArray = false
			if start+1 < len(p) {
				suffix, err := r.Write(p[start+1:])
				return append(out, suffix...), err
			}
			return out, nil
		}

		end := skipTopLevelModelJSONValue(p, start)
		next := skipTopLevelModelJSONSpace(p, end)
		complete := next < len(p)
		if !complete && (p[start] == '{' || p[start] == '[' || p[start] == '"') {
			complete = json.Valid(p[start:end])
		}
		if !complete {
			r.pending = bytes.Clone(p[cursor:])
			return out, nil
		}
		if !json.Valid(p[start:end]) || next < len(p) && p[next] != ',' && p[next] != ']' {
			out = append(out, bytes.Clone(p[cursor:]))
			r.rawJSONArray = false
			return out, nil
		}

		restored := bytes.Clone(p[start:end])
		if p[start] == '{' {
			var err error
			restored, _, err = r.sse.restoreResponseModel(p[start:end])
			if err != nil {
				return nil, err
			}
		}
		capacity := start - cursor + len(restored) + next - end
		if next < len(p) {
			capacity++
		}
		chunk := make([]byte, 0, capacity)
		chunk = append(chunk, p[cursor:start]...)
		chunk = append(chunk, restored...)
		chunk = append(chunk, p[end:next]...)
		cursor = next
		if cursor < len(p) {
			chunk = append(chunk, p[cursor])
			cursor++
		}
		out = append(out, chunk)
		if next < len(p) && p[next] == ']' {
			r.rawJSONArray = false
			if cursor < len(p) {
				suffix, err := r.Write(p[cursor:])
				return append(out, suffix...), err
			}
			return out, nil
		}
	}
	return out, nil
}
```

The `json.Valid` branch emits a syntactically complete object, nested array, or string even when it ends exactly at the read boundary. Primitive tokens remain pending until their comma or `]` arrives because a number can continue in the next read. Every stored tail uses `bytes.Clone`, so no retained state aliases a host-owned read buffer.

- [ ] **Step 6: Make Flush preserve an open-array tail**

At the start of `Flush`, handle `rawJSONArray` before generic `pending` parsing:

```go
if r.rawJSONArray {
	r.rawJSONArray = false
	pending := bytes.Clone(r.pending)
	r.pending = nil
	flushed, err := r.sse.Flush()
	if len(pending) > 0 {
		return append([][]byte{pending}, flushed...), err
	}
	return flushed, err
}
```

Do not synthesize a closing bracket.

- [ ] **Step 7: Update the old split-array expectation**

The existing `TestStreamChunkRewriterBuffersSplitRawJSON` expects no output for `[` followed by an incomplete string. Change only that array subcase to expect the already emitted `[` after the first write, then join first and second outputs and require `json.Valid` plus exact `['"partial"']` bytes.

- [ ] **Step 8: Run raw stream tests and a bounded allocation check**

```powershell
go test . -run 'Test(StreamChunkRewriter|HandleExecutorExecuteStreamRestoresGeminiJSONStreamArray)' -count=1
go test . -run TestStreamChunkRewriterEmitsCompletedRawJSONArrayElements -count=20
```

Add the bounded-buffer regression:

```go
func TestStreamChunkRewriterRawJSONArrayBuffersOnlyCurrentElement(t *testing.T) {
	r := newStreamChunkRewriter("client")
	input := []byte(`[{"modelVersion":"upstream","payload":"` + strings.Repeat("x", 4<<20) + `"},{"unfinished":"`)
	chunks, err := r.Write(input)
	if err != nil {
		t.Fatal(err)
	}
	if emitted := len(bytes.Join(chunks, nil)); emitted < 4<<20 {
		t.Fatalf("emitted bytes=%d, want completed 4 MiB element", emitted)
	}
	if got, want := string(r.pending), `{"unfinished":"`; got != want {
		t.Fatalf("pending=%q, want %q", got, want)
	}
}
```

Expected: PASS.

- [ ] **Step 9: Commit Task 4**

```powershell
git add -- main.go main_test.go
git commit -m "fix: stream raw JSON arrays incrementally"
```

---

### Task 5: Correct Gemini framing and body-dependent headers

**Files:**
- Modify: `main.go:1335-1438,1570-1624`
- Test: `main_test.go` near executor header and stream setup suites

**Interfaces:**
- Consumes: existing `prepareExecutorStream`, `handleExecutorExecute`, and `http.Header` clones.
- Produces: `removeChangedBodyHeaders(http.Header, bool)` where the boolean selects response-only validators.

- [ ] **Step 1: Add Gemini setup regressions**

Use the existing fake-host stream helper and cover these rows:

```go
[]struct {
	name, alt, hostContentType, wantContentType string
}{
	{name: "default SSE", alt: "", wantContentType: "text/event-stream"},
	{name: "direct JSON", alt: "json", wantContentType: "application/json"},
	{name: "explicit upstream SSE", alt: "", hostContentType: "text/event-stream", wantContentType: "text/event-stream"},
}
```

For every row, require `stream.frameRawJSONAsSSE == false`. Feed `{"modelVersion":"upstream"}` through the rewriter and require raw JSON containing `client`, with no `data:` or `event:` prefix.

- [ ] **Step 2: Confirm the Gemini test is red**

Run:

```powershell
go test . -run TestPrepareExecutorStreamGeminiKeepsCoreChunksRaw -count=1
```

Expected: FAIL because empty headers and explicit SSE both enable synthetic framing.

- [ ] **Step 3: Decouple Gemini body framing from Content-Type**

In `prepareExecutorStream`:

```go
if headers.Get("Content-Type") == "" {
	contentType := "text/event-stream"
	if req.Format == "gemini" && req.Alt != "" {
		contentType = "application/json"
	}
	headers.Set("Content-Type", contentType)
}
frameRawJSONAsSSE := req.Format != "gemini" && isEventStreamContentType(headers.Get("Content-Type"))
```

Retain the existing behavior for the other four formats.

- [ ] **Step 4: Add changed-body header tables**

Define test headers:

```go
var staleBodyHeaders = []string{
	"Content-Digest", "Repr-Digest", "Digest", "Content-MD5",
}
```

Add three tests:

1. Changed nonstream and stream requests remove `Content-Length` and all four digest fields, but preserve request `ETag`, `If-Match`, and `X-Keep`.
2. Changed nonstream responses remove `Content-Length`, all digest fields, `ETag`, and `Content-Range`, but preserve `X-Keep`.
3. Prepared stream responses remove the same response fields plus existing `Transfer-Encoding` before the first chunk, but preserve `Content-Type` and `X-Keep`.

Extend the existing unchanged-body response test to include every stale field and prove they are retained when restoration changes no bytes.

- [ ] **Step 5: Confirm all changed-body rows are red**

```powershell
go test . -run 'TestHandleExecutorExecute.*BodyHeaders|TestPrepareExecutorStream.*BodyHeaders' -count=1
```

Expected: FAIL on at least `Content-Digest`; current code deletes only `Content-Length` and stream `Transfer-Encoding`.

- [ ] **Step 6: Add one shared header-removal helper**

```go
func removeChangedBodyHeaders(headers http.Header, response bool) {
	for _, name := range []string{
		"Content-Length",
		"Content-Digest",
		"Repr-Digest",
		"Digest",
		"Content-MD5",
	} {
		headers.Del(name)
	}
	if response {
		headers.Del("ETag")
		headers.Del("Content-Range")
	}
}
```

Use it only when request/nonstream response bytes changed. Use it unconditionally on prepared mapped-stream response headers because future chunks may change after headers are returned. Keep stream `Transfer-Encoding` deletion next to it.

- [ ] **Step 7: Run all executor and header tests**

```powershell
go test . -run 'TestHandleExecutor|TestPrepareExecutorStream|TestStreamResponse|Test.*ContentLength' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 5**

```powershell
git add -- main.go main_test.go
git commit -m "fix: align stream framing and rewritten body headers"
```

---

### Task 6: Lock the RPC wire and cross-protocol contracts

**Files:**
- Test: `main_test.go` near existing all-format executor tests
- Production: none expected

**Interfaces:**
- Consumes: `handleMethod`, `handleExecutorExecute`, `handleExecutorExecuteStream`, and test host-callback setters.
- Produces: regression coverage only.

- [ ] **Step 1: Add a real outer-envelope body test**

Create a request whose `OriginalRequest` contains:

```json
{
  "model": "client",
  "opaque": "{\"model\":\"must-stay-client-text\"}",
  "nested": {"model": "must-stay-client-nested"},
  "tools": [{"arguments": "{\"model\":\"must-stay-tool-text\"}"}]
}
```

Marshal the actual executor RPC request, call `handleMethod(pluginabi.MethodExecutorExecute, raw)`, unwrap `pluginabi.Envelope`, and capture the nested host callback request. Require:

- the RPC `[]byte` field passed through one base64 JSON layer and arrives as valid provider JSON;
- only top-level `model` becomes `upstream`;
- the escaped JSON strings and nested model remain exactly unchanged;
- only whitelisted response model fields restore to `client`.

- [ ] **Step 2: Add stream cross-protocol coverage**

Use `SourceFormat: "claude"`, `Format: "openai"`, two header values, two query values, and a non-empty `HostCallbackID`. Capture `hostModelExecutePayload` and require:

```go
forwarded.EntryProtocol == "claude"
forwarded.ExitProtocol == "openai"
forwarded.HostCallbackID == "callback-cross-stream"
reflect.DeepEqual(forwarded.Headers.Values("X-Test"), []string{"one", "two"})
reflect.DeepEqual(forwarded.Query["q"], []string{"one", "two"})
```

Add a second nonstream subtest with the same source/output formats, multi-value headers/query, and callback ID. Capture `hostModelExecutePayload` and require the five assertions above before returning a valid `pluginapi.HostModelExecutionResponse`.

- [ ] **Step 3: Run the tests before changing production**

```powershell
go test . -run 'TestHandleMethodExecutorBodyIsDecodedOnce|TestHandleExecutorExecuteStreamCrossProtocol' -count=1
```

Expected: PASS. If either fails, stop and trace the exact field before changing production; do not add a second rewrite layer.

- [ ] **Step 4: Commit regression coverage**

```powershell
git add -- main_test.go
git commit -m "test: lock executor wire protocol boundaries"
```

---

### Task 7: Make compatibility sidecars and inspector status trustworthy

**Files:**
- Modify: `Makefile:29-84`
- Test: `.github/scripts/check-release-compatibility_test.go:65-179`

**Interfaces:**
- Consumes: `build-platform`, `package-platform`, `.version`, `READELF`, and `OTOOL`.
- Produces: `.version` only after successful `package-platform` compatibility inspection, plus a `smoke-local` target that invokes `build-platform` with values read from `go env GOOS` and `go env GOARCH`.

- [ ] **Step 1: Add a failed-inspector test**

Create a fake `readelf` that prints `Name: GLIBC_2.17` and exits 7. Invoke `make package-platform` with `MAKE=true` and a prepared fake library. Require a nonzero command status and absence of archive and `.version`.

Use the existing repo-root and Make lookup pattern in this test file. The fake script body is:

```sh
#!/bin/sh
printf '%s\n' 'Name: GLIBC_2.17'
exit 7
```

- [ ] **Step 2: Add aggregate-rejection coverage for a failed gate**

Extend `TestPackagePlatformStopsAfterCompatibilityFailure` after preparing the library and stale `.version`. First run a raw build with `GO=true`; this preserves the fake library while exercising sidecar invalidation:

```go
build := exec.Command(makePath,
	"--no-print-directory", "build-platform",
	"VERSION=0.5.1", "GOOS=linux", "GOARCH=amd64",
	"DIST_DIR="+filepath.ToSlash(distDir), "GO=true",
)
build.Dir = repoRoot
if output, err := build.CombinedOutput(); err != nil {
	t.Fatalf("fake raw build: %v\n%s", err, output)
}
```

Then run the existing `package-platform` command with `MAKE=true` and the GLIBC 2.28 inspector. After it fails, require:

```go
if _, statErr := os.Stat(libraryPath + ".version"); !errors.Is(statErr, os.ErrNotExist) {
	t.Fatalf("version sidecar exists after failed compatibility check: %v", statErr)
}
```

Current code recreates the sidecar in the raw build, so this assertion is red. Do not call package-release helpers from this test file; absence of the sidecar is the aggregate admission failure that `packageExistingArtifacts` already tests.

- [ ] **Step 3: Confirm both Make tests are red**

```powershell
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -run 'TestPackagePlatform.*(Inspector|Compatibility)' -count=1
```

Expected: the failed-inspector case packages successfully, and the failed-compatibility case leaves `.version`.

- [ ] **Step 4: Invalidate stale sidecars before every raw build**

Change `build-platform` so it removes the sidecar before invoking `go build` and does not recreate it:

```make
out="$(DIST_DIR)/$(GOOS)_$(GOARCH)/$(PLUGIN_NAME)$$ext"; \
mkdir -p "$$(dirname "$$out")"; \
rm -f "$$out.version"; \
# existing CC/deployment-target setup
CGO_ENABLED=1 ... $(GO) build ... -o "$$out" .
```

Update the dry-run test: `build-platform` must contain `rm -f "$out.version"` and must not contain the old `printf` sidecar write.

- [ ] **Step 5: Capture inspector output only after a successful exit**

In `package-platform`, after computing `library`:

```make
compatibility_output="$$("$(READELF)" --version-info "$$library")"; \
printf '%s\n' "$$compatibility_output" | GOOS= GOARCH= CGO_ENABLED= $(GO) run .github/scripts/check-release-compatibility.go -format glibc -max "$(GLIBC_MAX_VERSION)"
```

Use this macOS branch:

```make
compatibility_output="$$("$(OTOOL)" -l "$$library")"; \
printf '%s\n' "$$compatibility_output" | GOOS= GOARCH= CGO_ENABLED= $(GO) run .github/scripts/check-release-compatibility.go -format macos -max "$(MACOSX_DEPLOYMENT_TARGET)"
```

Each assignment carries the inspector's exit status under `set -e`; no shell-specific `pipefail` is needed.

- [ ] **Step 6: Write the sidecar only after the case block succeeds**

Immediately after the compatibility `case`:

```make
printf '%s\n' "$(BUILD_VERSION)" > "$$library.version"; \
```

Then run the existing single-platform packager. Windows and FreeBSD pass through the empty case and receive their sidecar at the same point.

- [ ] **Step 7: Add a failing source assertion for host-native smoke builds**

Add this test using the same local repository-root lookup as the existing Make tests:

```go
func TestMakeSmokeLocalBuildsCurrentHostPlatform(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := wd
	if _, err := os.Stat(filepath.Join(repoRoot, "Makefile")); err != nil {
		repoRoot = filepath.Clean(filepath.Join(wd, "..", ".."))
	}
	body, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"go env GOOS", "go env GOARCH", "build-platform", `GOOS="$$host_goos"`, `GOARCH="$$host_goarch"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("Makefile missing %q", want)
		}
	}
	if strings.Contains(text, "smoke-local: build-windows-amd64") {
		t.Fatal("smoke-local still has a fixed Windows amd64 prerequisite")
	}
}
```

Run:

```powershell
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -run TestMakeSmokeLocalBuildsCurrentHostPlatform -count=1
```

Expected: FAIL because `smoke-local` has a fixed `build-windows-amd64` prerequisite.

- [ ] **Step 8: Make `smoke-local` build the host platform**

Replace the target with:

```make
smoke-local:
	@host_goos="$$( $(GO) env GOOS )"; \
	host_goarch="$$( $(GO) env GOARCH )"; \
	$(MAKE) --no-print-directory build-platform GOOS="$$host_goos" GOARCH="$$host_goarch" GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)"
	$(GO) run .github/scripts/smoke-local.go
```

- [ ] **Step 9: Run compatibility and Make tests**

```powershell
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit Task 7**

```powershell
git add -- Makefile .github/scripts/check-release-compatibility_test.go
git commit -m "fix: attest release builds and select host smoke target"
```

---

### Task 8: Make checksum publication fail closed and recheck aliases

**Files:**
- Modify: `.github/scripts/package-release.go:34-69,208-261,402-433`
- Test: `.github/scripts/package-release_test.go:91-612`

**Interfaces:**
- Consumes: existing `validateDistinctPaths`, `packageLibrary`, `writeChecksum`, and `writeChecksums`.
- Produces: no new public CLI; `var writeChecksumFile = os.WriteFile` is a package-local test seam; old checksum outputs are absent whenever packaging fails after archive bytes change.

- [ ] **Step 1: Add a changed-archive stale-checksum regression**

Add a deterministic write-failure test. It intentionally does not call `t.Parallel` because it replaces a package variable:

```go
func TestSinglePlatformChecksumFailureRemovesStaleChecksum(t *testing.T) {
	dir := t.TempDir()
	library := filepath.Join(dir, "model-mapper.dll")
	archive := filepath.Join(dir, "model-mapper.zip")
	checksum := archive + ".sha256"
	if err := os.WriteFile(library, []byte("old library"), 0o644); err != nil { t.Fatal(err) }
	args := []string{"-library", library, "-archive", archive, "-checksum", checksum}
	if err := run(args); err != nil { t.Fatal(err) }
	if err := os.WriteFile(library, []byte("new library"), 0o644); err != nil { t.Fatal(err) }

	original := writeChecksumFile
	writeChecksumFile = func(path string, data []byte, perm os.FileMode) error {
		if path == checksum {
			if err := os.WriteFile(path, []byte("partial"), perm); err != nil { return err }
			return errors.New("injected checksum failure")
		}
		return os.WriteFile(path, data, perm)
	}
	t.Cleanup(func() { writeChecksumFile = original })
	if err := run(args); err == nil || !strings.Contains(err.Error(), "injected checksum failure") {
		t.Fatalf("run error=%v", err)
	}
	if _, err := os.Stat(checksum); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checksum remains after failure: %v", err)
	}
}
```

The test initially fails to compile because `writeChecksumFile` does not exist. That is the red state.

- [ ] **Step 2: Strengthen the aggregate checksum-failure test**

Replace `TestPackageExistingArtifactsPreservesStaleArchivesWhenChecksumWriteFails`. Keep its current fixture setup for checked Linux and Windows artifacts, then inject the same write seam only for `filepath.Join(outDir, "checksums.txt")`. After the first successful package, change the Linux library bytes, retain its matching `.version`, remove the Windows binary and sidecar, run `packageExistingArtifacts` again, and assert:

```go
if err := packageExistingArtifacts(version, distDir, outDir); err == nil || !strings.Contains(err.Error(), "injected checksums failure") {
	t.Fatalf("packageExistingArtifacts error=%v", err)
}
manifest := filepath.Join(outDir, "checksums.txt")
if _, err := os.Stat(manifest); !errors.Is(err, os.ErrNotExist) {
	t.Fatalf("checksum manifest remains after failure: %v", err)
}
```

The injected function first writes `partial` to the manifest and then returns `errors.New("injected checksums failure")`. Restore `writeChecksumFile` with `t.Cleanup`. Stale archives may remain because no manifest authenticates them. Do not abstract any other file operation.

- [ ] **Step 3: Confirm checksum tests are red**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -run 'Test.*Checksum.*(Mismatch|Fail)' -count=1
```

Expected: FAIL because the old checksum/manifest remains after archive bytes change.

- [ ] **Step 4: Remove old checksum outputs before archive mutation**

In single-platform mode, after initial path validation and before `packageLibrary`:

```go
if err := os.Remove(*checksumPath); err != nil && !os.IsNotExist(err) {
	return fmt.Errorf("remove old checksum %s: %w", filepath.ToSlash(*checksumPath), err)
}
```

In aggregate mode, after artifact/version validation and `MkdirAll`, but before creating any zip, remove `filepath.Join(outDir, "checksums.txt")` with the same fail-before-mutation behavior.

- [ ] **Step 5: Remove partial checksum output on write failure**

Declare exactly one seam next to `releaseVersionPattern`:

```go
var writeChecksumFile = os.WriteFile
```

In `writeChecksum`:

```go
if err := writeChecksumFile(checksumPath, []byte(line), 0o644); err != nil {
	_ = os.Remove(checksumPath)
	return fmt.Errorf("write checksum %s: %w", filepath.ToSlash(checksumPath), err)
}
```

In `writeChecksums`:

```go
if err := writeChecksumFile(path, []byte(builder.String()), 0o644); err != nil {
	_ = os.Remove(path)
	return fmt.Errorf("write checksums %s: %w", filepath.ToSlash(path), err)
}
```

- [ ] **Step 6: Revalidate archive/checksum identity after archive creation**

In single-platform `run`:

```go
if err := packageLibrary(*libraryPath, *archivePath); err != nil {
	return err
}
if err := validateDistinctPaths(*archivePath, *checksumPath); err != nil {
	return err
}
return writeChecksum(*checksumPath, *archivePath)
```

Keep the first three-path validation before any write. Add a regression that runs only when the test temp filesystem is case-insensitive; use `t.Skip` otherwise. On such a filesystem, `Archive.zip` and `archive.zip` must return the distinct-path error and must not become a checksum text file.

- [ ] **Step 7: Run all package-release tests**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 8**

```powershell
git add -- .github/scripts/package-release.go .github/scripts/package-release_test.go
git commit -m "fix: publish release checksums fail closed"
```

---

### Task 9: Make local smoke host-native and error-sensitive

**Files:**
- Modify: `.github/scripts/smoke-local.go:1-168,281-309,483-528`
- Test: `.github/scripts/smoke-local_test.go`

**Interfaces:**
- Consumes: `runtime.GOOS`, `runtime.GOARCH`, `smokeEnv.repoRoot`, and parsed `openAIResponse.Error`.
- Produces: `smokePluginPaths(string, string) (string, string)`; existing smoke environment variables remain unchanged.

- [ ] **Step 1: Add host-platform path coverage**

```go
func TestSmokePluginPathsUseHostPlatform(t *testing.T) {
	repo := filepath.Join("repo")
	dir := filepath.Join(repo, ".test-cpa")
	source, destination := smokePluginPaths(repo, dir)
	ext := ".so"
	if runtime.GOOS == "windows" { ext = ".dll" }
	if runtime.GOOS == "darwin" { ext = ".dylib" }
	name := "model-mapper" + ext
	if source != filepath.Join(repo, "dist", runtime.GOOS+"_"+runtime.GOARCH, name) {
		t.Fatalf("source=%q", source)
	}
	if destination != filepath.Join(dir, "plugins", runtime.GOOS, runtime.GOARCH, name) {
		t.Fatalf("destination=%q", destination)
	}
}
```

This is red at compile time until the helper exists.

- [ ] **Step 2: Implement current-host plugin paths**

```go
func smokePluginPaths(repoRoot, smokeDir string) (string, string) {
	ext := ".so"
	switch runtime.GOOS {
	case "windows":
		ext = ".dll"
	case "darwin":
		ext = ".dylib"
	}
	name := "model-mapper" + ext
	return filepath.Join(repoRoot, "dist", runtime.GOOS+"_"+runtime.GOARCH, name),
		filepath.Join(smokeDir, "plugins", runtime.GOOS, runtime.GOARCH, name)
}
```

Use it in `run`; set `env.plugin` from the destination and copy from the returned source. In `prepareDirs`, create `filepath.Dir(env.plugin)` instead of the Windows literal.

- [ ] **Step 3: Add the relative-executable regression**

Use `root := t.TempDir()`, copy `os.Executable()` to `filepath.Join(root, "tools", "cpa-helper"+helperExecutableExtension())`, and set `repoRoot: root`, `dir: filepath.Join(root, ".test-cpa")`, and `cpaBin: filepath.Join("tools", "cpa-helper"+helperExecutableExtension())`. Create `logsDir` and `logFile`, then call `startCPA`. The copied Go test binary exits on the unknown CPA flags. If `startCPA` returns a process before that exit is observed, wait on `proc.waitDone` and close its log. Require that the result is a `cpaStartedExitError` or a started process exit, never an error containing `start CPA` or `file not found`. Add:

```go
func helperExecutableExtension() string {
	if runtime.GOOS == "windows" { return ".exe" }
	return ""
}
```

Keep this helper in the test file.

- [ ] **Step 4: Resolve only path-like relative executable names**

At the top of `startCPA`:

```go
cpaBin := env.cpaBin
if !filepath.IsAbs(cpaBin) && strings.ContainsAny(cpaBin, `/\`) {
	cpaBin = filepath.Join(env.repoRoot, cpaBin)
}
cmd := exec.Command(cpaBin, "--config", "config.yaml", "--no-browser")
```

Do not convert bare names such as `cli-proxy-api`; `exec.Command` must continue using `PATH` for them.

- [ ] **Step 5: Add an in-band stream error regression**

Serve:

```text
data: {"model":"client"}

data: {"error":{"message":"upstream failed"}}

data: [DONE]

```

Call `runStreamCase`. Require a non-nil error containing `upstream failed`.

- [ ] **Step 6: Reject non-null streamed error payloads**

After unmarshalling each data payload:

```go
hasError := len(parsed.Error) != 0 && !bytes.Equal(bytes.TrimSpace(parsed.Error), []byte("null"))
if hasError {
	return fmt.Errorf("stream returned error: %s", parsed.Error)
}
```

Retain model and `[DONE]` checks.

- [ ] **Step 7: Run all smoke helper tests**

```powershell
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 9**

```powershell
git add -- .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go
git commit -m "fix: run local smoke on the host platform"
```

---

### Task 10: Publish SemVer prereleases as GitHub prereleases

**Files:**
- Modify: `.github/workflows/build.yml:282-296`
- Test: `.github/scripts/package-release_test.go`

**Interfaces:**
- Consumes: an already validated `GITHUB_REF_NAME` matching `vMAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]`.
- Produces: conditional `--prerelease` arguments for `gh release create` and `gh release edit`.

- [ ] **Step 1: Add a workflow-source regression**

Read `../workflows/build.yml` from the script test working directory. Require all of these exact fragments:

```text
if [[ "${GITHUB_REF_NAME#v}" == *-* ]]
prerelease_args+=(--prerelease)
gh release edit "${GITHUB_REF_NAME}" "${prerelease_args[@]}"
gh release create
"${prerelease_args[@]}"
```

Also require that the condition is inside the publish step rather than the build matrix.

- [ ] **Step 2: Confirm the workflow test is red**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -run TestReleaseWorkflowMarksPrereleaseTags -count=1
```

Expected: FAIL because the workflow contains no `--prerelease`.

- [ ] **Step 3: Add conditional GitHub CLI arguments**

At the start of the publish shell:

```bash
prerelease_args=()
if [[ "${GITHUB_REF_NAME#v}" == *-* ]]; then
  prerelease_args+=(--prerelease)
fi
```

For an existing release, run `gh release edit ... --prerelease` only when the array is non-empty, then upload with `--clobber`. For a new release, append `"${prerelease_args[@]}"` to `gh release create`. Do not mark stable tags as prereleases.

Use:

```bash
if ((${#prerelease_args[@]})); then
  gh release edit "${GITHUB_REF_NAME}" "${prerelease_args[@]}" --repo "${GITHUB_REPOSITORY}"
fi
```

- [ ] **Step 4: Run package and workflow-source tests**

```powershell
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 10**

```powershell
git add -- .github/workflows/build.yml .github/scripts/package-release_test.go
git commit -m "fix: mark semantic prereleases in GitHub"
```

---

### Task 11: Update v0.5.4 user contracts

**Files:**
- Modify: `README.md:35-39,180-225,256-276`
- Modify: `CLAUDE.md:35-64`

**Interfaces:**
- Consumes: completed runtime, packaging, and smoke behavior.
- Produces: user documentation matching the tested code.

- [ ] **Step 1: Update rewrite boundaries**

State that:

- non-empty Interactions `agent` requests remain native and are not model-mapped;
- request body changes remove length/digest metadata;
- response body changes remove length/digest/ETag/range metadata;
- mapped streams remove stale response metadata before forwarding;
- Gemini raw core chunks are not double-framed.

Keep the model-field whitelist unchanged.

- [ ] **Step 2: Update checked packaging commands and version examples**

Replace `0.5.3` examples with `0.5.4`. Show release-preparation commands that pass through `package-platform`, for example:

```powershell
make package VERSION=0.5.4 GOOS=windows GOARCH=amd64
make package VERSION=0.5.4 GOOS=linux GOARCH=amd64 BUILD_CC="zig cc -target x86_64-linux-gnu.2.17"
make package VERSION=0.5.4
```

Explain that raw `build-platform` invalidates `.version`, while successful `package-platform` writes it after compatibility inspection; aggregate packaging accepts only matching checked sidecars.

- [ ] **Step 3: Clarify current-host smoke**

State that `make smoke-local` builds and copies the current `go env GOOS/GOARCH` artifact, and that relative path-like `CPA_SMOKE_CPA_BIN` values are resolved from the repository root.

- [ ] **Step 4: Update the checked-in contributor invariants**

In `CLAUDE.md`, replace the two statements that say only `Content-Length` is removed. List the exact request-side and response-side fields from Task 5, state that mapped stream headers are cleaned before forwarding, and state that `build-platform` removes `.version` while `package-platform` creates it only after compatibility succeeds. Update the `smoke-local` description from Windows amd64 to current-host `GOOS/GOARCH`. Do not change the rule DSL, caller identity, or model-field whitelist sections.

- [ ] **Step 5: Check documentation identifiers and formatting**

Run:

```powershell
git diff --check
git diff -- README.md CLAUDE.md
```

Expected: no whitespace errors, no removed five-format capability, and no claim that aggregate packaging validates a raw unchecked build.

- [ ] **Step 6: Commit Task 11**

```powershell
git add -- README.md CLAUDE.md
git commit -m "docs: document v0.5.4 safety boundaries"
```

---

### Task 12: Review, simplify, and verify the complete implementation

**Files:**
- Review: every path changed since spec commit `83352e3`
- Update: only defects found by review
- Create: `docs/superpowers/verification/2026-09-11-cpa-model-mapper-0.5.4.md`

**Interfaces:**
- Consumes: all prior task commits.
- Produces: a reviewed, minimal, fully verified release candidate and an exact verification record.

- [ ] **Step 1: Run an Opus correctness review with mutually exclusive file scopes**

Use `claude-opus-5[1m]`, `xhigh`. One reviewer reads only `main.go` and its diff; one reads only `main_test.go`; one reads only release/Make/workflow files; one reads only smoke files; one final synthesizer reads the reviewer reports but not repository files. Verify findings before editing. Do not ask multiple reviewers to read the same file.

Required review questions:

- Can any valid read partition lose, duplicate, reorder, or invent a byte?
- Can an incomplete JSON array produce a synthesized close token or invalid SSE event?
- Is Gemini framing correct at both `Alt == ""` and `Alt != ""`?
- Are header deletions conditional where the final body is known and conservative where stream headers precede future body changes?
- Can raw builds retain a prior compatibility sidecar?
- Can any failure leave a checksum that authenticates different archive bytes?
- Are all paths resolved before `Cmd.Dir` changes their meaning?

- [ ] **Step 2: Apply only confirmed review fixes with TDD**

For each confirmed issue, add one failing focused test, run it red, apply the smallest fix, and rerun the focused suite. Commit each independent correction with `fix:`. If review finds no defect, make no source change.

- [ ] **Step 3: Run a Sonnet simplification pass on the changed code only**

Use `claude-sonnet-5[1m]`, `xhigh`. Preserve behavior and tests. Remove only duplication introduced by this implementation; do not refactor preexisting adjacent code. Rerun focused tests after any simplification and commit only if the diff is smaller or clearer.

- [ ] **Step 4: Run the complete test matrix in parallel where commands do not share outputs**

Run these independent groups concurrently:

```powershell
go test ./... -count=1
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
```

Then run the expensive root-package checks sequentially:

```powershell
go test -race ./... -count=1
go test ./... -count=20
```

Expected: every command exits 0. Do not claim verification if any command was skipped or failed.

- [ ] **Step 5: Build host Windows amd64 c-shared artifact**

Use a temporary output outside tracked paths or `dist/windows_amd64`:

```powershell
make build-platform VERSION=0.5.4 GOOS=windows GOARCH=amd64
```

Expected: `dist/windows_amd64/model-mapper.dll` exists and `.version` does not exist after a raw build.

Inspect the generated library with the installed GNU `objdump`:

```powershell
$exports = objdump -p dist/windows_amd64/model-mapper.dll
foreach ($name in 'cliproxy_plugin_init','cliproxyPluginCall','cliproxyPluginFree','cliproxyPluginShutdown') {
    if ($exports -notmatch [regex]::Escape($name)) { throw "missing export $name" }
}
```

Expected: all four names are present.

- [ ] **Step 6: Build and check Linux amd64 with Zig**

Run:

```powershell
make package VERSION=0.5.4 GOOS=linux GOARCH=amd64 BUILD_CC="zig cc -target x86_64-linux-gnu.2.17"
```

If `readelf` is unavailable, use an installed compatible inspector by setting `READELF`; do not bypass the compatibility check. Expected: `.so`, `.so.version`, zip, and `.sha256` exist, and the sidecar contains `0.5.4`.

- [ ] **Step 7: Package Windows and aggregate checked artifacts**

Run:

```powershell
make package VERSION=0.5.4 GOOS=windows GOARCH=amd64
make package VERSION=0.5.4
```

Expected: both platform archives and aggregate release checksums are produced. Inspect each ZIP and require only the platform library plus optional root `LICENSE`. Recompute SHA-256 and compare every published line by basename.

- [ ] **Step 8: Execute a host-native C ABI harness**

Compile a temporary harness outside the repository or in ignored `dist/`. It must load `model-mapper.dll`, resolve all four exported symbols, call `cliproxy_plugin_init` with ABI 1 and a host callback/free pair, invoke `plugin.register` with lifecycle `schema_version` 1 and 6, parse both successful envelopes, require registration schema 1, free every returned plugin buffer, and call shutdown. Record allocation/free counters and require one free per returned host/plugin buffer.

Do not commit the temporary harness or generated binaries.

- [ ] **Step 9: Run live smoke only when authorized inputs already exist**

Check whether `CPA_SMOKE_API_KEY` and `CPA_SMOKE_CPA_BIN` are non-empty without printing either value. If both exist:

```powershell
make smoke-local
```

Otherwise record `not run: CPA_SMOKE_API_KEY and/or CPA_SMOKE_CPA_BIN unavailable`; this is the only permitted verification omission.

- [ ] **Step 10: Write the verification record directly to Markdown**

Create the verification file with:

- exact commit under test;
- each command and exit status;
- root/race/repetition results;
- explicit script-test results;
- Windows/Linux artifact paths and SHA-256 values;
- compatibility checker result;
- ZIP entry lists;
- ABI export/load/register/free/shutdown result;
- live-smoke result or the exact unavailable-input note;
- remaining CPA-only limitations from the spec.

Do not include secrets, raw credentials, or unrelated CPA source details.

- [ ] **Step 11: Commit verification artifacts**

```powershell
git add -- docs/superpowers/verification/2026-09-11-cpa-model-mapper-0.5.4.md
git commit -m "test: verify v0.5.4 release candidate"
```

- [ ] **Step 12: Confirm plan completeness and clean source state**

Run:

```powershell
git status --short --branch
git diff --check 83352e3..HEAD
git log --oneline --decorate 83352e3..HEAD
```

Expected: only the task-start `.claude/` remains untracked; no temporary diagnostic test, harness source, generated archive, or CPA source change is staged or tracked. Every task above is checked.

---

### Task 13: Integrate main and publish v0.5.4

**Files:**
- Git refs only; no new source edits unless final verification exposes a defect

**Interfaces:**
- Consumes: verified release-candidate commits and the existing `origin` remote.
- Produces: local `main`, pushed `origin/main`, annotated `v0.5.4`, and a completed GitHub Actions Build run.

- [ ] **Step 1: Confirm local integration target**

```powershell
git branch --show-current
git status --short --branch
git fetch origin --prune --tags
git rev-list --left-right --count main...origin/main
```

Expected: work is on local `main`, local `main` contains spec commit `83352e3`, there is no unexpected remote divergence, and no tracked dirt exists. Preserve `.claude/`.

- [ ] **Step 2: Confirm every isolated implementation commit was integrated**

The execution orchestrator must cherry-pick each successful runtime, release, and smoke commit onto `main` before Task 12 starts. Verify that condition without changing the tree:

```powershell
if ((git branch --show-current) -ne 'main') { throw 'verified tree is not on main' }
git merge-base --is-ancestor 83352e3 HEAD
if ($LASTEXITCODE -ne 0) { throw 'main does not contain the audit spec' }
git status --porcelain --untracked-files=no
```

Expected: the ancestor check exits 0 and the tracked-status output is empty. If an isolated commit is absent, return to the orchestrator's recorded commit IDs and cherry-pick it before rerunning Task 12; do not tag a different tree.

- [ ] **Step 3: Re-run the release gate on final main**

```powershell
go test ./... -count=1
go vet ./...
go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1
go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go -count=1
go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go -count=1
```

Expected: all exit 0 at the exact commit to tag.

- [ ] **Step 4: Create the patch tag**

First prove the tag is absent locally and remotely:

```powershell
git tag --list v0.5.4
git ls-remote --tags origin refs/tags/v0.5.4
```

Both outputs must be empty. Then:

```powershell
git tag -a v0.5.4 -m "v0.5.4"
```

- [ ] **Step 5: Push main and tag**

The user explicitly authorized these outward-facing operations for this task:

```powershell
git push origin main
git push origin v0.5.4
```

Do not force-push and do not bypass hooks.

- [ ] **Step 6: Confirm workflow trigger and completion**

Use GitHub CLI against `DoingDog/cpa-plugin-model-mapper`:

```powershell
$tagCommit = git rev-list -n 1 v0.5.4
$runId = gh run list --repo DoingDog/cpa-plugin-model-mapper --workflow build.yml --commit $tagCommit --event push --limit 5 --json databaseId --jq '.[0].databaseId'
if (-not $runId) { throw 'no v0.5.4 Build run found' }
gh run watch $runId --repo DoingDog/cpa-plugin-model-mapper --exit-status
```

Expected: the tag-triggered Build run exists and ends `success`. Then verify release assets:

```powershell
gh release view v0.5.4 --repo DoingDog/cpa-plugin-model-mapper --json tagName,isPrerelease,assets
```

Require `tagName == "v0.5.4"`, `isPrerelease == false`, seven platform ZIP assets, and `checksums.txt`.

- [ ] **Step 7: Report exact completion state**

Report final commit, tag, pushed refs, workflow URL/status, release asset count, all verification results, and any explicitly skipped live smoke. If workflow fails, report the failing job and continue diagnosis; do not claim the release complete until it succeeds or an external blocker is proven.
