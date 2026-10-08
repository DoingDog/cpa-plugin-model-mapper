package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	claudehandlers "github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers/claude"
	geminihandlers "github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers/gemini"
	openaihandlers "github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers/openai"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"gopkg.in/yaml.v3"
)

const gCPAOutput = `[{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}]`
const gCPACompleted = `{"type":"response.completed","response":{"id":"resp-issue8","object":"response","status":"completed","model":"grok-4.7","output":` + gCPAOutput + `}}`

func gCPAPayloads(model string, count int) []string {
	values := []string{
		`{"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`,
		`{"type":"response.in_progress","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"ordinary grok-4.7 opaque 中文 output"}`,
		`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"ordinary grok-4.7 opaque 中文 output"}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}}`,
		gCPACompleted,
	}
	for i := range values {
		values[i] = strings.Replace(values[i], `"model":"grok-4.7"`, `"model":"`+model+`"`, 1)
	}
	if count == 1 {
		return values[8:]
	}
	return values
}

type gCPAFieldsFixture struct {
	name, eol, contentType, metadata string
	step                             int
	dataOnly                         bool
	count                            int
}

func gCPAFieldsFixtures() []gCPAFieldsFixture {
	var out []gCPAFieldsFixture
	for _, dataOnly := range []bool{false, true} {
		for _, count := range []int{1, 9} {
			for _, transport := range []gCPAFieldsFixture{
				{name: "LF-whole", eol: "\n", contentType: "text/event-stream"},
				{name: "CRLF-whole", eol: "\r\n", contentType: "text/event-stream"},
				{name: "LF-7bytes", eol: "\n", contentType: "text/event-stream", step: 7},
				{name: "CRLF-bytewise", eol: "\r\n", contentType: "text/event-stream", step: 1},
				{name: "LF-charset", eol: "\n", contentType: "text/event-stream; charset=utf-8", step: 11},
				{name: "CRLF-charset", eol: "\r\n", contentType: "text/event-stream; charset=utf-8", step: 7},
			} {
				for _, metadata := range []string{"", "id: event-1", "retry: 100", ": heartbeat"} {
					f := transport
					f.dataOnly, f.count, f.metadata = dataOnly, count, metadata
					label := metadata
					if label == "" {
						label = "none"
					}
					f.name = fmt.Sprintf("dataOnly=%v/count=%d/%s/metadata=%s", dataOnly, count, f.name, label)
					out = append(out, f)
				}
			}
		}
	}
	return out
}

func gCPAFields(f gCPAFieldsFixture, model string) ([]string, []byte) {
	var fields []string
	var wire bytes.Buffer
	for _, payload := range gCPAPayloads(model, f.count) {
		var event struct{ Type string }
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			panic(err)
		}
		if f.metadata != "" {
			fields = append(fields, f.metadata)
			fmt.Fprintf(&wire, "%s%s", f.metadata, f.eol)
		}
		if !f.dataOnly {
			fields = append(fields, "event: "+event.Type)
			fmt.Fprintf(&wire, "event: %s%s", event.Type, f.eol)
		}
		fields = append(fields, "data: "+payload)
		fmt.Fprintf(&wire, "data: %s%s%s", payload, f.eol, f.eol)
	}
	return fields, wire.Bytes()
}

type gCPAUpstreamRecord struct {
	body, response []byte
	model, path    string
	headers        http.Header
}

type gCPALocalProducer struct {
	mu       sync.Mutex
	fixture  gCPAFieldsFixture
	wire     []byte
	records  []gCPAUpstreamRecord
	provider coreauth.ProviderExecutor
	auth     *coreauth.Auth
	base     *handlers.BaseAPIHandler
	cfg      *config.Config
}

func gCPANewLocalProducer(t *testing.T, name string) *gCPALocalProducer {
	t.Helper()
	p := &gCPALocalProducer{cfg: &config.Config{}}
	p.provider = runtimeexecutor.NewXAIExecutor(p.cfg)
	if name == "codex" {
		p.provider = runtimeexecutor.NewCodexExecutor(p.cfg)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		var request struct {
			Model  string
			Stream bool
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		if r.URL.Path != "/v1/responses" || !request.Stream || (request.Model != "grok-4.6" && request.Model != "grok-4.7") {
			t.Errorf("upstream path=%s request=%+v", r.URL.Path, request)
		}
		p.mu.Lock()
		f, customWire := p.fixture, bytes.Clone(p.wire)
		p.mu.Unlock()
		_, wire := gCPAFields(f, request.Model)
		if customWire != nil {
			wire = customWire
		}
		p.mu.Lock()
		p.records = append(p.records, gCPAUpstreamRecord{body: bytes.Clone(body), response: bytes.Clone(wire), model: request.Model, path: r.URL.RequestURI(), headers: r.Header.Clone()})
		p.mu.Unlock()
		w.Header().Set("Content-Type", f.contentType)
		step := f.step
		if step == 0 {
			step = len(wire)
		}
		for start := 0; start < len(wire); start += step {
			if _, err := w.Write(wire[start:min(start+step, len(wire))]); err != nil {
				t.Error(err)
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(server.Close)
	p.auth = &coreauth.Auth{ID: "g-fields-" + name, Provider: name, Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "fake-upstream-key", "base_url": server.URL + "/v1", "proxy_url": "direct"}}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.SetConfig(p.cfg)
	manager.RegisterExecutor(p.provider)
	if _, err := manager.Register(context.Background(), p.auth); err != nil {
		t.Fatal(err)
	}
	registry.GetGlobalRegistry().RegisterClient(p.auth.ID, p.auth.Provider, []*registry.ModelInfo{{ID: "grok-4.6"}, {ID: "grok-4.7"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(p.auth.ID) })
	p.base = handlers.NewBaseAPIHandlers(&p.cfg.SDKConfig, manager)
	return p
}

func (p *gCPALocalProducer) setFixture(f gCPAFieldsFixture) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fixture, p.records = f, nil
}

func gCPARequest(model string, stream bool) []byte {
	return []byte(fmt.Sprintf(`{"model":%q,"input":"say ok","stream":%v,"prompt_cache_key":"task-g-local"}`, model, stream))
}

func gCPARequireFields(t *testing.T, parts []string, f gCPAFieldsFixture, model string) {
	t.Helper()
	want, _ := gCPAFields(f, model)
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("producer fields=%q, want=%q", parts, want)
	}
	for _, part := range parts {
		if strings.ContainsAny(part, "\r\n") {
			t.Fatalf("logical field unexpectedly contains line ending: %q", part)
		}
	}
}

func gCPACoreFields(t *testing.T, p *gCPALocalProducer, ctx context.Context) []string {
	t.Helper()
	body := gCPARequest("grok-4.7", true)
	stream, err := p.provider.ExecuteStream(ctx, p.auth, coreexecutor.Request{Model: "grok-4.7", Payload: body}, coreexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: body, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	empty := 0
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		if len(chunk.Payload) > 0 {
			parts = append(parts, string(chunk.Payload))
		} else {
			empty++
		}
	}
	t.Logf("builtin core provider=%s fixture=%s fields=%q emptyChunks=%d", p.provider.Identifier(), p.fixture.name, parts, empty)
	return parts
}

func gCPAHostFields(t *testing.T, p *gCPALocalProducer, ctx context.Context) []string {
	t.Helper()
	result, errMsg := p.base.ExecuteModelStream(ctx, handlers.ModelExecutionRequest{EntryProtocol: "openai-response", ExitProtocol: "openai-response", Model: "grok-4.7", Stream: true, Body: gCPARequest("grok-4.7", true)})
	if errMsg != nil {
		t.Fatal(errMsg.Error)
	}
	var parts []string
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		if len(chunk.Payload) > 0 {
			parts = append(parts, string(chunk.Payload))
		}
	}
	t.Logf("builtin host provider=%s fixture=%s fields=%q", p.provider.Identifier(), p.fixture.name, parts)
	return parts
}

func gCPARequireResponse(t *testing.T, raw []byte, model string) {
	t.Helper()
	want := `{"id":"resp-issue8","object":"response","status":"completed","model":"` + model + `","output":` + gCPAOutput + `}`
	if string(raw) != want {
		t.Fatalf("completed response=%s want=%s", raw, want)
	}
}

func gCPARequireEvents(t *testing.T, raw []byte, f gCPAFieldsFixture, model string) {
	t.Helper()
	if f.metadata != "" {
		normalized := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
		if got := bytes.Count(append([]byte("\n"), normalized...), []byte("\n"+f.metadata+"\n")); got != f.count {
			t.Fatalf("metadata count=%d want=%d bytes=%q", got, f.count, raw)
		}
	}
	// 现有 sse.Decode 只识别 LF；仅为该 parser 统一已知 CRLF 行结束，原始 bytes 继续保留。
	events, err := sse.Decode(bytes.NewReader(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))))
	if err != nil {
		t.Fatal(err)
	}
	want := gCPAPayloads(model, f.count)
	var payloads []string
	for _, event := range events {
		payload, ok := event.Data.(string)
		if !ok || payload == "" {
			continue
		}
		if payload == "[DONE]" {
			t.Fatal("Responses must not acquire DONE")
		}
		var value struct {
			Type, Delta string
			Response    json.RawMessage
		}
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			t.Fatal(err)
		}
		if !f.dataOnly && event.Event != value.Type {
			t.Fatalf("event=%q type=%q", event.Event, value.Type)
		}
		if f.dataOnly && event.Event != "message" {
			t.Fatalf("data-only decoder event=%q, want default message", event.Event)
		}
		if value.Type == "response.output_text.delta" && value.Delta != "ordinary grok-4.7 opaque 中文 output" {
			t.Fatalf("delta=%q", value.Delta)
		}
		if value.Type == "response.completed" {
			gCPARequireResponse(t, value.Response, model)
		}
		payloads = append(payloads, payload)
	}
	if !reflect.DeepEqual(payloads, want) {
		t.Fatalf("data events=%q want=%q", payloads, want)
	}
}

func gCPALoadNative(t *testing.T, p *gCPALocalProducer, enabled bool) *Host {
	t.Helper()
	plugin := os.Getenv("CPA_SMOKE_PLUGIN")
	if plugin == "" {
		t.Fatal("CPA_SMOKE_PLUGIN is required")
	}
	plugin, err := filepath.Abs(plugin)
	if err != nil {
		t.Fatal(err)
	}
	library, err := os.ReadFile(plugin)
	if err != nil {
		t.Fatal(err)
	}
	// parent 提供的文件存活到整个 overlay 结束；顺序 owner 只读取同一动态库。
	pluginDir := filepath.Dir(filepath.Dir(filepath.Dir(plugin)))
	if plugin != filepath.Join(pluginDir, runtime.GOOS, runtime.GOARCH, "model-mapper"+filepath.Ext(plugin)) {
		t.Fatalf("unexpected native library path: %s", plugin)
	}
	data := fmt.Sprintf("plugins:\n  enabled: %v\n  dir: %q\n  configs:\n    model-mapper:\n      enabled: true\n      priority: 1\n      global_rules: 'grok-4.6=>grok-4.7'\n", enabled, filepath.ToSlash(pluginDir))
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatal(err)
	}
	host := New()
	host.SetModelExecutor(p.base)
	host.ApplyConfig(context.Background(), &cfg)
	active := host.activeRecords()
	if enabled {
		if len(active) != 1 || active[0].id != "model-mapper" || active[0].plugin.Capabilities.Executor == nil {
			t.Fatal("native executor is not active")
		}
		t.Cleanup(func() {
			if len(host.activeRecords()) != 0 && !host.UnloadPlugin("model-mapper") {
				t.Error("native unload failed")
			}
		})
	} else if len(active) != 0 {
		t.Fatal("disabled control unexpectedly has an active plugin")
	}
	t.Logf("native enabled=%v canonical=%s SHA256=%x", enabled, plugin, sha256.Sum256(library))
	return host
}

func TestModelMapperFunctionalNativeProducerFields(t *testing.T) {
	for _, name := range []string{"xai", "codex"} {
		t.Run(name, func(t *testing.T) {
			p := gCPANewLocalProducer(t, name)
			for _, f := range gCPAFieldsFixtures() {
				t.Run(f.name, func(t *testing.T) {
					p.setFixture(f)
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					gCPARequireFields(t, gCPACoreFields(t, p, ctx), f, "grok-4.7")
					gCPARequireFields(t, gCPAHostFields(t, p, ctx), f, "grok-4.7")
				})
			}
		})
	}
}

func TestModelMapperFunctionalNativeSSEFields(t *testing.T) {
	for _, name := range []string{"xai", "codex"} {
		t.Run(name, func(t *testing.T) {
			p := gCPANewLocalProducer(t, name)
			host := gCPALoadNative(t, p, true)
			for _, f := range gCPAFieldsFixtures() {
				t.Run(f.name, func(t *testing.T) {
					p.setFixture(f)
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					gCPARequireFields(t, gCPACoreFields(t, p, ctx), f, "grok-4.7")
					gCPARequireFields(t, gCPAHostFields(t, p, ctx), f, "grok-4.7")
					body := gCPARequest("grok-4.6", true)
					result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, pluginapi.ExecutorRequest{Model: "grok-4.6", Format: "openai-response", SourceFormat: "openai-response", Stream: true, Payload: body, OriginalRequest: body})
					if err != nil {
						t.Fatal(err)
					}
					var raw bytes.Buffer
					var parts []string
					for chunk := range result.Chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
						raw.Write(chunk.Payload)
						parts = append(parts, string(chunk.Payload))
					}
					t.Logf("native field input shape=%s output chunks=%q", f.name, parts)
					if f.dataOnly && f.count == 1 && f.metadata == "" {
						// 无 metadata 的单 terminal output unit 保留无最终 blank delimiter 控制。
						var event struct {
							Type     string
							Response json.RawMessage
						}
						payload := bytes.TrimSpace(bytes.TrimPrefix(raw.Bytes(), []byte("data: ")))
						if err := json.Unmarshal(payload, &event); err != nil {
							t.Fatal(err)
						}
						if event.Type != "response.completed" {
							t.Fatalf("terminal type=%q", event.Type)
						}
						gCPARequireResponse(t, event.Response, "grok-4.6")
					} else {
						gCPARequireEvents(t, raw.Bytes(), f, "grok-4.6")
					}
				})
			}
		})
	}
}

func TestModelMapperFunctionalNativeFieldsHTTP(t *testing.T) {
	for _, name := range []string{"xai", "codex"} {
		for _, enabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/enabled=%v", name, enabled), func(t *testing.T) {
				p := gCPANewLocalProducer(t, name)
				host := gCPALoadNative(t, p, enabled)
				p.base.SetPluginHost(host)
				p.base.SetModelRouterHost(host)
				router := gin.New()
				router.POST("/v1/responses", openaihandlers.NewOpenAIResponsesAPIHandler(p.base).Responses)
				server := httptest.NewServer(router)
				defer server.Close()
				for _, f := range gCPAFieldsFixtures() {
					for _, stream := range []bool{true, false} {
						var mapped *gCPAUpstreamRecord
						for _, model := range []string{"grok-4.6", "grok-4.7"} {
							t.Run(fmt.Sprintf("%s/stream=%v/model=%s", f.name, stream, model), func(t *testing.T) {
								p.setFixture(f)
								ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
								defer cancel()
								request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", bytes.NewReader(gCPARequest(model, stream)))
								if err != nil {
									t.Fatal(err)
								}
								request.Header.Set("Content-Type", "application/json")
								response, err := http.DefaultClient.Do(request)
								if err != nil {
									t.Fatal(err)
								}
								raw, err := io.ReadAll(response.Body)
								closeErr := response.Body.Close()
								if err != nil || closeErr != nil {
									t.Fatalf("read=%v close=%v", err, closeErr)
								}
								p.mu.Lock()
								records := append([]gCPAUpstreamRecord(nil), p.records...)
								p.mu.Unlock()
								wantUpstream := model
								if enabled && model == "grok-4.6" {
									wantUpstream = "grok-4.7"
								}
								if len(records) != 1 || records[0].model != wantUpstream || records[0].path != "/v1/responses" {
									t.Fatalf("upstream calls/route=%+v", records)
								}
								if enabled {
									if model == "grok-4.6" {
										snapshot := records[0]
										mapped = &snapshot
									} else if mapped == nil || !bytes.Equal(mapped.body, records[0].body) || !bytes.Equal(mapped.response, records[0].response) {
										t.Fatal("mapped/direct upstream bytes differ")
									}
								}
								t.Logf("HTTP %s stream=%v client=%s upstream=%s status=%d body=%s", f.name, stream, model, wantUpstream, response.StatusCode, raw)
								if response.StatusCode != http.StatusOK {
									t.Fatalf("native HTTP status=%d body=%s", response.StatusCode, raw)
								}
								if stream {
									if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
										t.Fatalf("Content-Type=%q", response.Header.Get("Content-Type"))
									}
									gCPARequireEvents(t, raw, f, model)
								} else {
									gCPARequireResponse(t, raw, model)
								}
							})
						}
					}
				}
			})
		}
	}
}

func TestModelMapperFunctionalNativeFieldsHTTPUnmappedControl(t *testing.T) {
	for _, name := range []string{"xai", "codex"} {
		t.Run(name, func(t *testing.T) {
			p := gCPANewLocalProducer(t, name)
			router := gin.New()
			router.POST("/v1/responses", openaihandlers.NewOpenAIResponsesAPIHandler(p.base).Responses)
			server := httptest.NewServer(router)
			defer server.Close()
			for _, f := range gCPAFieldsFixtures() {
				for _, stream := range []bool{true, false} {
					t.Run(fmt.Sprintf("%s/stream=%v", f.name, stream), func(t *testing.T) {
						p.setFixture(f)
						ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
						defer cancel()
						request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", bytes.NewReader(gCPARequest("grok-4.7", stream)))
						if err != nil {
							t.Fatal(err)
						}
						request.Header.Set("Content-Type", "application/json")
						response, err := http.DefaultClient.Do(request)
						if err != nil {
							t.Fatal(err)
						}
						raw, err := io.ReadAll(response.Body)
						closeErr := response.Body.Close()
						if err != nil || closeErr != nil {
							t.Fatalf("read=%v close=%v", err, closeErr)
						}
						if response.StatusCode != http.StatusOK {
							t.Fatalf("HTTP status=%d body=%s", response.StatusCode, raw)
						}
						if stream {
							gCPARequireEvents(t, raw, f, "grok-4.7")
						} else {
							gCPARequireResponse(t, raw, "grok-4.7")
						}
						p.mu.Lock()
						records := append([]gCPAUpstreamRecord(nil), p.records...)
						p.mu.Unlock()
						if len(records) != 1 || records[0].model != "grok-4.7" || records[0].path != "/v1/responses" {
							t.Fatalf("upstream=%+v", records)
						}
					})
				}
			}
		})
	}
}

func eNativeHost(t *testing.T, executor modelExecutor) *Host {
	t.Helper()
	host := gCPALoadNative(t, &gCPALocalProducer{}, true)
	host.SetModelExecutor(executor)
	return host
}

func eNativeRequest(model, format string, stream bool) pluginapi.ExecutorRequest {
	body := []byte(fmt.Sprintf(`{"model":%q,"stream":%v}`, model, stream))
	return pluginapi.ExecutorRequest{Model: model, Format: format, SourceFormat: format, Stream: stream, Payload: body, OriginalRequest: body}
}

func eNativeDrain(t *testing.T, chunks <-chan pluginapi.ExecutorStreamChunk) ([]byte, []string) {
	t.Helper()
	var raw []byte
	var errs []string
	deadline := time.After(30 * time.Second)
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return raw, errs
			}
			raw = append(raw, chunk.Payload...)
			if chunk.Err != nil {
				errs = append(errs, chunk.Err.Error())
			}
		case <-deadline:
			t.Fatal("native stream did not terminate")
			return nil, nil
		}
	}
}

func waitFunctionalNativeEmitBlocked(t *testing.T) {
	t.Helper()
	waitFunctionalNativeCallbackBlocked(t, "(*streamBridgeStream).emit(", "(*Host).callHostStreamEmit(")
}

func waitFunctionalNativeCallbackBlocked(t *testing.T, bridgeMethod, hostMethod string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	buffer := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buffer, true)
		if n == len(buffer) {
			t.Fatal("goroutine snapshot is truncated")
		}
		for _, stack := range bytes.Split(buffer[:n], []byte("\n\n")) {
			header, _, _ := bytes.Cut(stack, []byte("\n"))
			if bytes.Contains(header, []byte("[select")) && bytes.Contains(stack, []byte(bridgeMethod)) && bytes.Contains(stack, []byte(hostMethod)) {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("native callback did not enter %s wait", bridgeMethod)
		default:
			runtime.Gosched()
		}
	}
}

func TestModelMapperFunctionalNativeUnload(t *testing.T) {
	for _, full := range []bool{true, false} {
		t.Run(fmt.Sprintf("backpressure=%v", full), func(t *testing.T) {
			input := make(chan handlers.ModelExecutionChunk, 32)
			var closeInput sync.Once
			stopSource := func() { closeInput.Do(func() { close(input) }) }
			if full {
				for i := 0; i < 32; i++ {
					input <- handlers.ModelExecutionChunk{Payload: []byte(fmt.Sprintf(`{"model":"grok-4.7","index":%d,"text":"opaque grok-4.7"}`, i))}
				}
			}
			executor := &fakeHostModelExecutor{executeModelStream: func(ctx context.Context, req handlers.ModelExecutionRequest) (handlers.ModelExecutionStream, *interfaces.ErrorMessage) {
				if req.Model != "grok-4.7" {
					t.Errorf("upstream model=%q", req.Model)
				}
				if !full {
					input <- handlers.ModelExecutionChunk{Payload: []byte(`{"model":"grok-4.7","text":"partial grok-4.7"}`)}
				}
				// 外部 executor 收到 host cancellation 后结束上游读取。
				go func() { <-ctx.Done(); stopSource() }()
				return handlers.ModelExecutionStream{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}}, Chunks: input}, nil
			}}
			host := eNativeHost(t, executor)
			result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(context.Background(), eNativeRequest("grok-4.6", "openai", true))
			if err != nil {
				t.Fatal(err)
			}
			unloadDone := make(chan bool, 1)
			launched := false
			startUnload := func() {
				if !launched {
					launched = true
					go func() { unloadDone <- host.UnloadPlugin("model-mapper") }()
				}
			}
			finished := false
			t.Cleanup(func() {
				startUnload()
				if !finished {
					stopSource()
					// 失败时恢复消费，等待本测试首次 unload；不启动第二次卸载。
					go func() {
						for range result.Chunks {
						}
					}()
					select {
					case <-unloadDone:
					case <-time.After(10 * time.Second):
						t.Error("first unload did not finish during teardown")
					}
				}
			})
			accepted := 1
			if full {
				waitFunctionalNativeEmitBlocked(t)
				accepted = 32 - len(input) - 1
				if accepted != streamBridgeBufferSize+1 {
					t.Fatalf("accepted=%d want adapter queue+handoff=%d", accepted, streamBridgeBufferSize+1)
				}
			} else {
				select {
				case chunk := <-result.Chunks:
					if string(chunk.Payload) != `{"model":"grok-4.6","text":"partial grok-4.7"}` || chunk.Err != nil {
						t.Fatalf("partial=%q error=%v", chunk.Payload, chunk.Err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("partial payload missing")
				}
				waitFunctionalNativeCallbackBlocked(t, "(*modelStreamBridge).read(", "(*Host).callHostModelStreamRead(")
			}
			startUnload()
			select {
			case ok := <-unloadDone:
				finished = true
				if !ok {
					t.Fatal("first unload returned false")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("unload waits for stopped downstream consumer")
			}
			raw, errs := eNativeDrain(t, result.Chunks)
			if len(errs) != 1 || strings.TrimSpace(errs[0]) == "" || bytes.Contains(raw, []byte("[DONE]")) {
				t.Fatalf("terminal errors=%q output=%q", errs, raw)
			}
			if full {
				d := json.NewDecoder(bytes.NewReader(raw))
				for i := 0; i < accepted; i++ {
					var value struct {
						Model, Text string
						Index       int
					}
					if err := d.Decode(&value); err != nil {
						t.Fatal(err)
					}
					if value.Model != "grok-4.6" || value.Index != i || value.Text != "opaque grok-4.7" {
						t.Fatalf("accepted payload %d=%+v", i, value)
					}
				}
				var extra json.RawMessage
				if err := d.Decode(&extra); err != io.EOF {
					t.Fatalf("extra payload=%s error=%v", extra, err)
				}
			} else if len(raw) != 0 {
				t.Fatalf("unexpected payload after interruption=%q", raw)
			}
			host.modelStreams.mu.Lock()
			remaining := len(host.modelStreams.streams)
			host.modelStreams.mu.Unlock()
			host.streams.mu.Lock()
			downstream := len(host.streams.streams)
			host.streams.mu.Unlock()
			if remaining != 0 || downstream != 0 || len(host.activeRecords()) != 0 {
				t.Fatalf("unload state: model streams=%d downstream=%d active=%d", remaining, downstream, len(host.activeRecords()))
			}
			t.Logf("first unload returned without resumed consumer; accepted=%d errors=%q", accepted, errs)
		})
	}
}

func TestModelMapperFunctionalNativeRequestDuplicates(t *testing.T) {
	const input = `{"model":"grok-4.6","messages":[{"role":"user","content":"first"}],"model":"grok-4.6","messages":[{"role":"user","content":"second"}]}`
	const want = `{"model":"grok-4.7","messages":[{"role":"user","content":"first"}],"messages":[{"role":"user","content":"second"}]}`
	var body []byte
	calls := 0
	executor := &fakeHostModelExecutor{executeModel: func(_ context.Context, req handlers.ModelExecutionRequest) (handlers.ModelExecutionResponse, *interfaces.ErrorMessage) {
		calls++
		body = bytes.Clone(req.Body)
		if req.Model != "grok-4.7" {
			t.Errorf("upstream model=%q", req.Model)
		}
		return handlers.ModelExecutionResponse{StatusCode: 200, Body: []byte(`{"model":"grok-4.7","content":"opaque grok-4.7"}`)}, nil
	}}
	host := eNativeHost(t, executor)
	request := eNativeRequest("grok-4.6", "openai", false)
	request.Payload, request.OriginalRequest = []byte(input), []byte(input)
	response, err := host.activeRecords()[0].plugin.Capabilities.Executor.Execute(context.Background(), request)
	if err != nil || calls != 1 || string(body) != want {
		t.Fatalf("native duplicate request=%s want=%s calls=%d error=%v", body, want, calls, err)
	}
	if string(response.Payload) != `{"model":"grok-4.6","content":"opaque grok-4.7"}` || string(request.Payload) != input {
		t.Fatalf("response=%s original request=%s", response.Payload, request.Payload)
	}
	t.Logf("native ABI/host retained duplicate messages: request=%s response=%s", body, response.Payload)
}

func TestModelMapperFunctionalNativeFormatsAndHeaders(t *testing.T) {
	var calls int
	var payload []byte
	var upstreamHeaders http.Header
	executor := &fakeHostModelExecutor{
		executeModel: func(ctx context.Context, req handlers.ModelExecutionRequest) (handlers.ModelExecutionResponse, *interfaces.ErrorMessage) {
			calls++
			if req.Model != "grok-4.7" || !bytes.Equal(req.Body, []byte(`{"model":"grok-4.7","stream":false}`)) {
				t.Errorf("request model=%s body=%s", req.Model, req.Body)
			}
			for _, name := range []string{"Content-Length", "Content-Digest", "Repr-Digest", "Digest", "Content-MD5"} {
				if req.Headers.Get(name) != "" {
					t.Errorf("request retained %s", name)
				}
			}
			if req.Headers.Get("X-Opaque") != "keep" {
				t.Error("opaque request header lost")
			}
			return handlers.ModelExecutionResponse{StatusCode: 200, Headers: upstreamHeaders, Body: payload}, nil
		},
		executeModelStream: func(ctx context.Context, req handlers.ModelExecutionRequest) (handlers.ModelExecutionStream, *interfaces.ErrorMessage) {
			calls++
			chunks := make(chan handlers.ModelExecutionChunk, 1)
			chunks <- handlers.ModelExecutionChunk{Payload: payload}
			close(chunks)
			return handlers.ModelExecutionStream{StatusCode: 200, Headers: upstreamHeaders, Chunks: chunks}, nil
		},
	}
	host := eNativeHost(t, executor)
	for _, format := range []string{"openai", "openai-response", "claude", "gemini", "interactions"} {
		for _, changed := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/changed=%v", format, changed), func(t *testing.T) {
				model := "grok-4.7"
				if !changed {
					model = "grok-4.6"
				}
				payload = []byte(fmt.Sprintf(`{"model":%q,"modelVersion":%q,"response":{"model":%q,"modelVersion":%q},"message":{"model":%q,"modelVersion":"opaque"},"interaction":{"model":%q},"opaque":{"model":"grok-4.7","text":"工具 grok-4.7","n":1.00}}`, model, model, model, model, model, model))
				want := []byte(`{"model":"grok-4.6","modelVersion":"grok-4.6","response":{"model":"grok-4.6","modelVersion":"grok-4.6"},"message":{"model":"grok-4.6","modelVersion":"opaque"},"interaction":{"model":"grok-4.6"},"opaque":{"model":"grok-4.7","text":"工具 grok-4.7","n":1.00}}`)
				upstreamHeaders = http.Header{"Content-Type": {"application/json"}, "Content-Length": {"999"}, "Content-Digest": {"keep-digest"}, "Repr-Digest": {"keep-repr"}, "Digest": {"keep"}, "Content-Md5": {"keep-md5"}, "Etag": {"tag"}, "Content-Range": {"bytes 0-998/999"}, "X-Opaque": {"keep"}}
				request := eNativeRequest("grok-4.6", format, false)
				request.Headers = upstreamHeaders.Clone()
				calls = 0
				response, err := host.activeRecords()[0].plugin.Capabilities.Executor.Execute(context.Background(), request)
				if err != nil || !bytes.Equal(response.Payload, want) || calls != 1 {
					t.Fatalf("native response=%s calls=%d error=%v", response.Payload, calls, err)
				}
				for _, name := range []string{"Content-Length", "Content-Digest", "Repr-Digest", "Digest", "Content-MD5", "ETag", "Content-Range"} {
					value := response.Headers.Get(name)
					if changed && value != "" || !changed && value != upstreamHeaders.Get(name) {
						t.Fatalf("changed=%v header %s=%q", changed, name, value)
					}
				}
				if response.Headers.Get("X-Opaque") != "keep" {
					t.Fatal("opaque header lost")
				}
			})
		}
	}
	for _, header := range []string{"", "text/event-stream", "text/event-stream; charset=utf-8", "application/json"} {
		t.Run("openai-raw/header="+header, func(t *testing.T) {
			payload = []byte(`{"model":"grok-4.7","choices":[{"delta":{"content":"hello"}}]}`)
			upstreamHeaders = http.Header{"X-Opaque": {"keep"}, "Transfer-Encoding": {"chunked"}, "Etag": {"tag"}, "Content-Length": {"999"}, "Content-Digest": {"digest"}, "Repr-Digest": {"repr"}, "Digest": {"digest"}, "Content-Md5": {"md5"}, "Content-Range": {"bytes 0-998/999"}}
			if header != "" {
				upstreamHeaders.Set("Content-Type", header)
			}
			calls = 0
			result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(context.Background(), eNativeRequest("grok-4.6", "openai", true))
			if err != nil {
				t.Fatal(err)
			}
			raw, errs := eNativeDrain(t, result.Chunks)
			want := `{"model":"grok-4.6","choices":[{"delta":{"content":"hello"}}]}`
			if string(raw) != want || len(errs) != 0 || calls != 1 {
				t.Fatalf("raw=%q errors=%q calls=%d", raw, errs, calls)
			}
			for _, name := range []string{"Content-Length", "Content-Digest", "Repr-Digest", "Digest", "Content-MD5", "ETag", "Content-Range", "Transfer-Encoding"} {
				if result.Headers.Get(name) != "" {
					t.Fatalf("stream retained %s=%q", name, result.Headers.Get(name))
				}
			}
			if result.Headers.Get("X-Opaque") != "keep" {
				t.Fatalf("headers=%v", result.Headers)
			}
		})
	}
}

func TestModelMapperFunctionalNativeProtocolAndErrors(t *testing.T) {
	var parts [][]byte
	var terminal *handlers.ModelExecutionStreamError
	var calls int
	executor := &fakeHostModelExecutor{executeModelStream: func(ctx context.Context, req handlers.ModelExecutionRequest) (handlers.ModelExecutionStream, *interfaces.ErrorMessage) {
		calls++
		if req.Model != "grok-4.7" {
			t.Errorf("upstream=%s", req.Model)
		}
		chunks := make(chan handlers.ModelExecutionChunk, len(parts)+1)
		for _, part := range parts {
			chunks <- handlers.ModelExecutionChunk{Payload: part}
		}
		if terminal != nil {
			chunks <- handlers.ModelExecutionChunk{Err: terminal}
		}
		close(chunks)
		return handlers.ModelExecutionStream{StatusCode: 200, Headers: http.Header{"Content-Type": {"text/event-stream; charset=utf-8"}}, Chunks: chunks}, nil
	}}
	host := eNativeHost(t, executor)
	mixed := []byte(`{"type":"response.created","response":{"model":"grok-4.7"}}` + "\n" + `{"type":"response.in_progress","response":{"model":"grok-4.7"}}` + "\n\n" + "event: response.completed\ndata: " + `{"type":"response.completed","response":{"model":"grok-4.7"}}` + "\n\n")
	// F11 固定 220 字节输入使用原 upstream 值，不改变输入规模。
	mixed = []byte(strings.ReplaceAll(string(mixed), "grok-4.7", "upstream"))
	if len(mixed) != 220 {
		t.Fatalf("F11 fixture bytes=%d", len(mixed))
	}
	for _, tc := range []struct {
		name, format string
		parts        [][]byte
		want         []byte
		errorText    string
	}{
		{"F11-mixed", "openai-response", [][]byte{mixed}, []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"grok-4.6\"}}\n\nevent: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"response\":{\"model\":\"grok-4.6\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"grok-4.6\"}}\n\n"), ""},
		{"F14-split", "openai-response", [][]byte{[]byte("x-vendor-field\nd"), []byte("ata: {\"model\":\"upstream\"}\n\n")}, []byte("x-vendor-field\ndata: {\"model\":\"grok-4.6\"}\n\n"), ""},
		{"claude-unknown-fields", "claude", [][]byte{[]byte("true\nfalse\nnull\n123\nevent: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"grok-4.7\"},\"opaque\":\"grok-4.7\"}\n\n")}, []byte("true\nfalse\nnull\n123\nevent: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"grok-4.6\"},\"opaque\":\"grok-4.7\"}\n\n"), ""},
		{"gemini-raw-array", "gemini", [][]byte{[]byte(`[{"modelVersion":"grok-4.7","candidates":[{"content":{"model":"grok-4.7","text":"工具"}}]}]`)}, []byte(`[{"modelVersion":"grok-4.6","candidates":[{"content":{"model":"grok-4.7","text":"工具"}}]}]`), ""},
		{"terminal-error", "openai-response", [][]byte{[]byte("event: response.completed\ndata: " + gCPACompleted + "\n\n")}, []byte("event: response.completed\ndata: " + strings.Replace(gCPACompleted, `"model":"grok-4.7"`, `"model":"grok-4.6"`, 1) + "\n\n"), "probe upstream error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts, terminal, calls = tc.parts, nil, 0
			if tc.errorText != "" {
				terminal = &handlers.ModelExecutionStreamError{StatusCode: http.StatusBadGateway, Message: tc.errorText}
			}
			result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(context.Background(), eNativeRequest("grok-4.6", tc.format, true))
			if err != nil {
				t.Fatal(err)
			}
			raw, errs := eNativeDrain(t, result.Chunks)
			if !bytes.Equal(raw, tc.want) || calls != 1 {
				t.Fatalf("output=%q want=%q calls=%d", raw, tc.want, calls)
			}
			if tc.errorText == "" && len(errs) != 0 || tc.errorText != "" && (len(errs) != 1 || !strings.Contains(errs[0], tc.errorText)) {
				t.Fatalf("errors=%q", errs)
			}
		})
	}
}

func TestModelMapperFunctionalResponsesWS(t *testing.T) {
	endpoint, key := os.Getenv("CPA_FUNCTIONAL_WS_URL"), os.Getenv("CPA_FUNCTIONAL_LOCAL_KEY")
	if endpoint == "" || key == "" {
		t.Fatal("local CPA WebSocket endpoint and key are required")
	}
	for _, model := range []string{"deepseek-v4-pro", "deepseek-v4-flash"} {
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, response, err := websocket.DefaultDialer.DialContext(ctx, endpoint, http.Header{"Authorization": {"Bearer " + key}})
			if response != nil && response.Body != nil {
				defer response.Body.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.create","model":%q,"input":"say ok"}`, model))); err != nil {
				t.Fatal(err)
			}
			created, completed := false, false
			var content strings.Builder
			for !completed {
				kind, raw, err := conn.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}
				if kind != websocket.TextMessage || !json.Valid(raw) {
					t.Fatalf("WS payload=%q", raw)
				}
				var event struct {
					Type, Delta string
					Response    struct {
						Model  string
						Output json.RawMessage
					}
				}
				if err := json.Unmarshal(raw, &event); err != nil {
					t.Fatal(err)
				}
				switch event.Type {
				case "response.created", "response.completed":
					if event.Response.Model != model {
						t.Fatalf("%s model=%q", event.Type, event.Response.Model)
					}
					created = created || event.Type == "response.created"
					completed = event.Type == "response.completed"
					if completed {
						var output []struct {
							Type, Role, Status string
							Content            []struct{ Type, Text string }
						}
						if err := json.Unmarshal(event.Response.Output, &output); err != nil {
							t.Fatal(err)
						}
						if len(output) != 1 || output[0].Type != "message" || output[0].Role != "assistant" || output[0].Status != "completed" || len(output[0].Content) != 1 || output[0].Content[0].Type != "output_text" || output[0].Content[0].Text != "onetwo" {
							t.Fatalf("WS completed output=%s", event.Response.Output)
						}
					}
					t.Logf("binary WS %s complete JSON=%s", model, raw)
				case "response.output_text.delta":
					content.WriteString(event.Delta)
				}
			}
			if !created || content.String() != "onetwo" {
				t.Fatalf("created=%v content=%q", created, content.String())
			}
		})
	}
}

// 仅替代外部模型；manager、validator、native ABI 和 HTTP/WS 消费者继续使用 CPA 实现。
type eControlledProvider struct {
	parts    [][]byte
	terminal error
	body     []byte
	calls    []coreexecutor.Request
}

func (*eControlledProvider) Identifier() string { return "functional-controlled" }
func (p *eControlledProvider) Execute(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	p.calls = append(p.calls, req)
	return coreexecutor.Response{Payload: bytes.Clone(p.body), Headers: http.Header{"Content-Type": {"application/json"}}}, nil
}
func (p *eControlledProvider) ExecuteStream(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	p.calls = append(p.calls, req)
	out := make(chan coreexecutor.StreamChunk, len(p.parts)+1)
	for _, part := range p.parts {
		out <- coreexecutor.StreamChunk{Payload: bytes.Clone(part)}
	}
	if p.terminal != nil {
		out <- coreexecutor.StreamChunk{Err: p.terminal}
	}
	close(out)
	return &coreexecutor.StreamResult{Headers: http.Header{"Content-Type": {"text/event-stream"}}, Chunks: out}, nil
}
func (*eControlledProvider) Refresh(_ context.Context, a *coreauth.Auth) (*coreauth.Auth, error) {
	return a, nil
}
func (p *eControlledProvider) CountTokens(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	p.calls = append(p.calls, req)
	return coreexecutor.Response{Payload: []byte(`{"input_tokens":7}`)}, nil
}
func (*eControlledProvider) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected HttpRequest")
}

func eControlledBase(t *testing.T, p *eControlledProvider) *handlers.BaseAPIHandler {
	t.Helper()
	cfg := &config.Config{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.SetConfig(cfg)
	manager.RegisterExecutor(p)
	auth := &coreauth.Auth{ID: "functional-controlled-auth", Provider: p.Identifier(), Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "grok-4.6"}, {ID: "grok-4.7"}, {ID: "upstream"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	return handlers.NewBaseAPIHandlers(&cfg.SDKConfig, manager)
}

func eRequireData(t *testing.T, raw []byte, want []string) {
	t.Helper()
	events, err := sse.Decode(bytes.NewReader(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0)
	for _, event := range events {
		data, ok := event.Data.(string)
		if !ok {
			t.Fatalf("SSE data type=%T", event.Data)
		}
		if data == "" {
			continue
		}
		if !json.Valid([]byte(data)) {
			t.Fatalf("invalid SSE JSON=%q", data)
		}
		got = append(got, data)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SSE payloads=%q want=%q wire=%q", got, want, raw)
	}
}

func eResponsesServer(t *testing.T, base *handlers.BaseAPIHandler) *httptest.Server {
	t.Helper()
	router := gin.New()
	handler := openaihandlers.NewOpenAIResponsesAPIHandler(base)
	router.POST("/v1/responses", handler.Responses)
	router.GET("/v1/responses/ws", handler.ResponsesWebsocket)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}
func eHTTP(t *testing.T, server *httptest.Server, path string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read=%v close=%v", err, closeErr)
	}
	return response.StatusCode, response.Header, raw
}
func eWS(t *testing.T, server *httptest.Server, model string) ([]string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.create","model":%q,"input":[]}`, model))); err != nil {
		t.Fatal(err)
	}
	var out []string
	for {
		kind, raw, err := conn.ReadMessage()
		if err != nil {
			return out, err
		}
		if kind != websocket.TextMessage || !json.Valid(raw) {
			t.Fatalf("WS kind=%d payload=%q", kind, raw)
		}
		out = append(out, string(raw))
		var event struct{ Type string }
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "response.completed" || event.Type == "error" || event.Type == "response.failed" {
			return out, nil
		}
	}
}

func eMetadataParts() []string {
	return []string{"event: response.created", `data: {"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`, "event: response.completed", "data: " + gCPACompleted}
}

func TestModelMapperFunctionalMetadataBuiltin(t *testing.T) {
	for _, name := range []string{"xai", "codex"} {
		t.Run(name, func(t *testing.T) {
			p := gCPANewLocalProducer(t, name)
			host := gCPALoadNative(t, p, true)
			parts := eMetadataParts()
			joined := strings.Join(parts, "")
			p.fixture = gCPAFieldsFixture{count: 1, contentType: "text/event-stream"}
			p.wire = []byte(joined + "\n\n" + parts[3] + "\n\n")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			wantHost := []string{joined, parts[3]}
			if got := gCPACoreFields(t, p, ctx); !reflect.DeepEqual(got, wantHost) {
				t.Fatalf("core=%q want=%q", got, wantHost)
			}
			if got := gCPAHostFields(t, p, ctx); !reflect.DeepEqual(got, wantHost) {
				t.Fatalf("host=%q want=%q", got, wantHost)
			}
			wantTerminal := strings.Replace(gCPACompleted, `"model":"grok-4.7"`, `"model":"grok-4.6"`, 1)
			result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, eNativeRequest("grok-4.6", "openai-response", true))
			if err != nil {
				t.Fatal(err)
			}
			raw, errs := eNativeDrain(t, result.Chunks)
			want := joined + "\ndata: " + wantTerminal + "\n\n"
			if string(raw) != want || len(errs) != 0 {
				t.Fatalf("native=%q errors=%q want=%q", raw, errs, want)
			}
			eRequireData(t, raw, []string{wantTerminal})
			for _, mapped := range []bool{false, true} {
				t.Run(fmt.Sprintf("HTTP/mapped=%v", mapped), func(t *testing.T) {
					if mapped {
						p.base.SetPluginHost(host)
						p.base.SetModelRouterHost(host)
					}
					server := eResponsesServer(t, p.base)
					model := "grok-4.7"
					terminal := gCPACompleted
					if mapped {
						model = "grok-4.6"
						terminal = wantTerminal
					}
					for _, stream := range []bool{true, false} {
						p.mu.Lock()
						p.records = nil
						p.mu.Unlock()
						status, headers, body := eHTTP(t, server, "/v1/responses", gCPARequest(model, stream))
						if status != 200 {
							t.Fatalf("status=%d body=%s", status, body)
						}
						if stream {
							eRequireData(t, body, []string{terminal})
							if string(body) != joined+"\ndata: "+terminal+"\n\n\n" {
								t.Fatalf("metadata HTTP bytes=%q", body)
							}
						} else {
							gCPARequireResponse(t, body, model)
						}
						p.mu.Lock()
						records := append([]gCPAUpstreamRecord(nil), p.records...)
						p.mu.Unlock()
						if len(records) != 1 || records[0].model != "grok-4.7" {
							t.Fatalf("calls=%+v", records)
						}
						t.Logf("builtin metadata stream=%v status=%d headers=%v input=%q output=%q", stream, status, headers, wantHost, body)
					}
				})
			}
		})
	}
}

func TestModelMapperFunctionalValidatorAndConsumers(t *testing.T) {
	p := &eControlledProvider{}
	base := eControlledBase(t, p)
	host := eNativeHost(t, base)
	parts := eMetadataParts()
	joined := strings.Join(parts, "")
	partial := strings.Join(parts[:3], "")
	completed := gCPACompleted
	mixed := []byte(`{"type":"response.created","response":{"model":"upstream"}}` + "\n" + `{"type":"response.in_progress","response":{"model":"upstream"}}` + "\n\n" + "event: response.completed\ndata: " + `{"type":"response.completed","response":{"model":"upstream"}}` + "\n\n")
	if len(mixed) != 220 {
		t.Fatalf("F11 bytes=%d", len(mixed))
	}
	for _, tc := range []struct {
		name   string
		parts  [][]byte
		native string
		data   []string
		ws     bool
		reject bool
	}{
		{"metadata-resegmented", [][]byte{[]byte(partial), []byte(parts[3] + "\n\n")}, partial + "\n" + parts[3] + "\n\n", []string{completed}, true, false},
		{"metadata-delimited-control", [][]byte{[]byte(joined + "\n\n"), []byte(parts[3] + "\n\n")}, joined + "\n\n" + parts[3] + "\n\n", []string{completed}, true, false},
		{"F11-220bytes", [][]byte{mixed}, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"upstream\"}}\n\nevent: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"response\":{\"model\":\"upstream\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"upstream\"}}\n\n", []string{`{"type":"response.created","response":{"model":"upstream"}}`, `{"type":"response.in_progress","response":{"model":"upstream"}}`, `{"type":"response.completed","response":{"model":"upstream"}}`}, false, false},
		{"F14-validator-split", [][]byte{[]byte("x-vendor-field\nd"), []byte("ata: " + completed + "\n\n")}, "x-vendor-field\ndata: " + completed + "\n\n", []string{completed}, false, false},
		{"F13-prevalidation", [][]byte{[]byte("data: " + completed + "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"upstream\"}}\n\n")}, "", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base.SetPluginHost(nil)
			base.SetModelRouterHost(nil)
			p.parts, p.calls = tc.parts, nil
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			validated, msg := base.ExecuteModelStream(ctx, handlers.ModelExecutionRequest{EntryProtocol: "openai-response", ExitProtocol: "openai-response", Model: "grok-4.7", Stream: true, Body: gCPARequest("grok-4.7", true)})
			if tc.reject {
				if msg == nil || msg.Error == nil {
					t.Fatal("F13 mixed chunk was not rejected before ABI")
				}
				t.Logf("original validator rejection=%v calls=%d", msg.Error, len(p.calls))
				return
			}
			if msg != nil {
				t.Fatal(msg.Error)
			}
			var got [][]byte
			for chunk := range validated.Chunks {
				if chunk.Err != nil {
					t.Fatal(chunk.Err)
				}
				got = append(got, chunk.Payload)
			}
			if !reflect.DeepEqual(got, tc.parts) {
				t.Fatalf("validator changed output units=%q want=%q", got, tc.parts)
			}
			for _, mapped := range []bool{false, true} {
				t.Run(fmt.Sprintf("mapped=%v", mapped), func(t *testing.T) {
					base.SetPluginHost(nil)
					base.SetModelRouterHost(nil)
					model := "grok-4.7"
					native := tc.native
					wantData := append([]string(nil), tc.data...)
					if mapped {
						model = "grok-4.6"
						base.SetPluginHost(host)
						base.SetModelRouterHost(host)
						for i := range wantData {
							wantData[i] = strings.Replace(wantData[i], `"model":"grok-4.7"`, `"model":"grok-4.6"`, 1)
							wantData[i] = strings.Replace(wantData[i], `"model":"upstream"`, `"model":"grok-4.6"`, 1)
						}
						// metadata 字符串不解析，只有真正 data 事件中的 model 改写。
						for _, data := range tc.data {
							replacement := strings.Replace(data, `"model":"grok-4.7"`, `"model":"grok-4.6"`, 1)
							replacement = strings.Replace(replacement, `"model":"upstream"`, `"model":"grok-4.6"`, 1)
							native = strings.Replace(native, "\ndata: "+data+"\n", "\ndata: "+replacement+"\n", 1)
						}
						p.calls = nil
						result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, eNativeRequest(model, "openai-response", true))
						if err != nil {
							t.Fatal(err)
						}
						raw, errs := eNativeDrain(t, result.Chunks)
						if string(raw) != native || len(errs) != 0 || len(p.calls) != 1 || p.calls[0].Model != "grok-4.7" {
							t.Fatalf("native output=%q want=%q errors=%q calls=%+v", raw, native, errs, p.calls)
						}
					}
					server := eResponsesServer(t, base)
					p.calls = nil
					status, _, raw := eHTTP(t, server, "/v1/responses", gCPARequest(model, true))
					if status != 200 || len(p.calls) != 1 {
						t.Fatalf("HTTP=%d calls=%d body=%q", status, len(p.calls), raw)
					}
					if tc.name == "F11-220bytes" && !mapped {
						// 原 HTTP framer 仅派发 suffix；validator/ABI 仍原样保留 220 bytes。
						if !bytes.Equal(raw, append(bytes.Clone(mixed), '\n')) {
							t.Fatalf("unmapped F11 wire=%q", raw)
						}
						eRequireData(t, raw, wantData[2:])
					} else {
						eRequireData(t, raw, wantData)
					}
					if tc.ws {
						p.calls = nil
						ws, err := eWS(t, server, model)
						if err != nil || !reflect.DeepEqual(ws, wantData) || len(p.calls) != 1 {
							t.Fatalf("WS=%q want=%q error=%v calls=%d", ws, wantData, err, len(p.calls))
						}
					}
					if tc.name == "F14-validator-split" {
						p.calls = nil
						ws, err := eWS(t, server, model)
						if mapped {
							if err != nil || !reflect.DeepEqual(ws, wantData) || len(p.calls) != 1 {
								t.Fatalf("F14 mapped WS=%q want=%q error=%v calls=%d", ws, wantData, err, len(p.calls))
							}
						} else if !websocket.IsCloseError(err, websocket.CloseAbnormalClosure) || len(ws) != 0 || len(p.calls) != 1 {
							t.Fatalf("F14 original WS input-unit support: output=%q error=%v calls=%d", ws, err, len(p.calls))
						}
						t.Logf("F14 HTTP completed; WS mapped=%v output=%q error=%v", mapped, ws, err)
					}
					t.Logf("validator retained=%q native=%q HTTP=%q WS-supported=%v", got, native, raw, tc.ws)
				})
			}
		})
	}
}

func TestModelMapperFunctionalProtocolHTTP(t *testing.T) {
	p := &eControlledProvider{}
	base := eControlledBase(t, p)
	host := eNativeHost(t, base)
	base.SetPluginHost(host)
	base.SetModelRouterHost(host)
	router := gin.New()
	claude := claudehandlers.NewClaudeCodeAPIHandler(base)
	gemini := geminihandlers.NewGeminiAPIHandler(base)
	router.POST("/v1/messages", claude.ClaudeMessages)
	router.POST("/v1/messages/count_tokens", claude.ClaudeCountTokens)
	router.POST("/v1beta/models/*action", gemini.GeminiHandler)
	router.POST("/v1beta/interactions", gemini.Interactions)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	for _, model := range []string{"grok-4.6", "grok-4.7"} {
		t.Run(model, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("claude/stream=%v", stream), func(t *testing.T) {
					p.calls = nil
					p.body = []byte(`{"id":"msg-functional","type":"message","role":"assistant","model":"grok-4.7","content":[{"type":"text","text":"工具 grok-4.7 中文"}],"stop_reason":"end_turn"}`)
					values := []string{`{"type":"message_start","message":{"id":"msg-functional","model":"grok-4.7","content":[]}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool-1","name":"probe","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"text\":\"工具 grok-4.7 中文\",\"n\":1.00}"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`, `{"type":"message_stop"}`}
					var wire strings.Builder
					wire.WriteString("true\nfalse\nnull\n123\n")
					for _, value := range values {
						var event struct{ Type string }
						if err := json.Unmarshal([]byte(value), &event); err != nil {
							t.Fatal(err)
						}
						fmt.Fprintf(&wire, "event: %s\ndata: %s\n\n", event.Type, value)
					}
					p.parts = [][]byte{[]byte(wire.String())}
					body := []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"max_tokens":10,"stream":%v}`, model, stream))
					status, _, raw := eHTTP(t, server, "/v1/messages", body)
					if status != 200 || len(p.calls) != 1 || p.calls[0].Model != "grok-4.7" {
						t.Fatalf("status=%d calls=%+v body=%q", status, p.calls, raw)
					}
					if stream {
						want := append([]string(nil), values...)
						want[0] = strings.Replace(want[0], `"model":"grok-4.7"`, `"model":"`+model+`"`, 1)
						eRequireData(t, raw, want)
						expected := strings.Replace(wire.String(), `"model":"grok-4.7"`, `"model":"`+model+`"`, 1)
						if string(raw) != expected {
							t.Fatalf("Claude unknown fields/tool bytes=%q", raw)
						}
					} else {
						want := strings.Replace(string(p.body), `"model":"grok-4.7"`, `"model":"`+model+`"`, 1)
						if string(raw) != want {
							t.Fatalf("Claude body=%q want=%q", raw, want)
						}
					}
				})
			}
			for _, mode := range []string{"nonstream", "SSE", "array"} {
				t.Run("gemini/"+mode, func(t *testing.T) {
					p.calls = nil
					value := `{"modelVersion":"grok-4.7","candidates":[{"content":{"parts":[{"text":"工具 grok-4.7 中文","model":"grok-4.7"}]},"finishReason":"STOP"}]}`
					p.body = []byte(value)
					p.parts = [][]byte{[]byte(value)}
					path := "/v1beta/models/" + model + ":generateContent"
					if mode != "nonstream" {
						path = "/v1beta/models/" + model + ":streamGenerateContent"
					}
					if mode == "array" {
						path += "?alt=json"
						p.parts = [][]byte{[]byte("[" + value + "]")}
					}
					status, _, raw := eHTTP(t, server, path, []byte(fmt.Sprintf(`{"model":%q,"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, model)))
					if status != 200 || len(p.calls) != 1 || p.calls[0].Model != "grok-4.7" {
						t.Fatalf("status=%d calls=%+v output=%q", status, p.calls, raw)
					}
					want := strings.Replace(value, `"modelVersion":"grok-4.7"`, `"modelVersion":"`+model+`"`, 1)
					switch mode {
					case "SSE":
						eRequireData(t, raw, []string{want})
						if string(raw) != "data: "+want+"\n\n" {
							t.Fatalf("Gemini framing=%q", raw)
						}
					case "array":
						if string(raw) != "["+want+"]" {
							t.Fatalf("array=%q", raw)
						}
					default:
						if string(raw) != want {
							t.Fatalf("Gemini body=%q", raw)
						}
					}
				})
			}
			t.Run("interactions/proper-SSE", func(t *testing.T) {
				values := []string{`{"event_type":"interaction.start","interaction":{"model":"grok-4.7","status":"in_progress"}}`, `{"event_type":"step.start","step":{"type":"text","text":""}}`, `{"event_type":"content.delta","delta":{"type":"text","text":"工具 grok-4.7 中文"}}`, `{"event_type":"step.stop","step":{"type":"text","text":"工具 grok-4.7 中文"}}`, `{"event_type":"interaction.stop","interaction":{"model":"grok-4.7","status":"completed","outputs":[{"text":"工具 grok-4.7 中文"}]}}`}
				p.calls = nil
				p.parts = nil
				want := append([]string(nil), values...)
				for i, value := range values {
					p.parts = append(p.parts, []byte("data: "+value+"\n\n"))
					want[i] = strings.Replace(value, `"model":"grok-4.7"`, `"model":"`+model+`"`, 1)
				}
				status, _, raw := eHTTP(t, server, "/v1beta/interactions", []byte(fmt.Sprintf(`{"model":%q,"input":"hello","stream":true}`, model)))
				if status != 200 || len(p.calls) != 1 || p.calls[0].Model != "grok-4.7" {
					t.Fatalf("status=%d calls=%+v output=%q", status, p.calls, raw)
				}
				eRequireData(t, raw, want)
			})
		})
	}
	t.Run("count-tokens", func(t *testing.T) {
		p.calls = nil
		status, _, raw := eHTTP(t, server, "/v1/messages/count_tokens", []byte(`{"model":"grok-4.6","messages":[{"role":"user","content":"hello"}]}`))
		if status != 200 || string(raw) != `{"input_tokens":7}` || len(p.calls) != 1 || p.calls[0].Model != "grok-4.6" {
			t.Fatalf("guard status=%d body=%q calls=%+v", status, raw, p.calls)
		}
	})
}

func TestModelMapperFunctionalReconfigureReload(t *testing.T) {
	t.Run("canonical-lifetime", func(t *testing.T) {
		plugin := os.Getenv("CPA_SMOKE_PLUGIN")
		original, err := os.Stat(plugin)
		if err != nil {
			t.Fatal(err)
		}
		library, err := os.ReadFile(plugin)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"first-owner", "next-owner"} {
			t.Run(name, func(t *testing.T) {
				calls := 0
				executor := &fakeHostModelExecutor{executeModel: func(_ context.Context, req handlers.ModelExecutionRequest) (handlers.ModelExecutionResponse, *interfaces.ErrorMessage) {
					calls++
					if req.Model != "grok-4.7" || string(req.Body) != `{"model":"grok-4.7","stream":false}` {
						t.Errorf("current owner upstream model=%s body=%s", req.Model, req.Body)
					}
					return handlers.ModelExecutionResponse{StatusCode: 200, Body: []byte(fmt.Sprintf(`{"model":"grok-4.7","content":"%s opaque grok-4.7"}`, name))}, nil
				}}
				host := eNativeHost(t, executor)
				response, err := host.activeRecords()[0].plugin.Capabilities.Executor.Execute(context.Background(), eNativeRequest("grok-4.6", "openai", false))
				want := fmt.Sprintf(`{"model":"grok-4.6","content":"%s opaque grok-4.7"}`, name)
				if err != nil || calls != 1 || string(response.Payload) != want {
					t.Fatalf("current owner response=%s want=%s calls=%d error=%v", response.Payload, want, calls, err)
				}
				loaded, err := os.Stat(host.activeRecords()[0].path)
				if err != nil || !os.SameFile(original, loaded) {
					t.Fatalf("native library identity differs from parent: path=%s parent=%s error=%v", host.activeRecords()[0].path, plugin, err)
				}
			})
			// 子测试的 native cleanup 完成后，parent 文件仍须保持原身份和内容。
			current, err := os.Stat(plugin)
			if err != nil || !os.SameFile(original, current) {
				t.Fatalf("parent library identity changed after %s cleanup: %v", name, err)
			}
			currentBytes, err := os.ReadFile(plugin)
			if err != nil || sha256.Sum256(currentBytes) != sha256.Sum256(library) {
				t.Fatalf("parent library content changed after %s cleanup: %v", name, err)
			}
		}
	})
	executor := &fakeHostModelExecutor{executeModelStream: func(_ context.Context, req handlers.ModelExecutionRequest) (handlers.ModelExecutionStream, *interfaces.ErrorMessage) {
		chunks := make(chan handlers.ModelExecutionChunk, 1)
		chunks <- handlers.ModelExecutionChunk{Payload: []byte(fmt.Sprintf(`{"model":%q,"modelVersion":%q,"text":"opaque grok-4.7"}`, req.Model, req.Model))}
		close(chunks)
		return handlers.ModelExecutionStream{StatusCode: 200, Chunks: chunks}, nil
	}}
	host := eNativeHost(t, executor)
	path := host.activeRecords()[0].path
	dir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	apply := func(rules string) {
		t.Helper()
		var cfg config.Config
		data := fmt.Sprintf("plugins:\n  enabled: true\n  dir: %q\n  configs:\n    model-mapper:\n      enabled: true\n      global_rules: %q\n", filepath.ToSlash(dir), rules)
		if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
			t.Fatal(err)
		}
		host.ApplyConfig(context.Background(), &cfg)
		if len(host.activeRecords()) != 1 {
			t.Fatal("native register/reconfigure failed")
		}
	}
	check := func() {
		t.Helper()
		record := host.activeRecords()[0]
		if record.plugin.SchemaVersion != 1 || record.meta.Name != "model-mapper" || record.meta.Version != "0.0.0-dev" {
			t.Fatalf("metadata=%+v schema=%d", record.meta, record.plugin.SchemaVersion)
		}
		var fields []string
		for _, field := range record.meta.ConfigFields {
			fields = append(fields, field.Name)
		}
		if !reflect.DeepEqual(fields, []string{"global_rules", "claude_messages_rules", "codex_responses_rules", "openai_completions_rules", "rules_stack_mode"}) {
			t.Fatalf("config fields=%q", fields)
		}
		for _, format := range []string{"openai", "openai-response", "claude", "gemini", "interactions"} {
			result, err := record.plugin.Capabilities.Executor.ExecuteStream(context.Background(), eNativeRequest("grok-4.6", format, true))
			if err != nil {
				t.Fatal(err)
			}
			raw, errs := eNativeDrain(t, result.Chunks)
			want := `{"model":"grok-4.6","modelVersion":"grok-4.6","text":"opaque grok-4.7"}`
			if format == "openai-response" || format == "claude" || format == "interactions" {
				want = "data: " + want + "\n\n"
			}
			if string(raw) != want || len(errs) != 0 {
				t.Fatalf("%s clean EOF=%q errors=%q want=%q", format, raw, errs, want)
			}
		}
	}
	check()
	apply("grok-4.6=>grok-4.7")
	for _, guard := range []pluginapi.ModelRouteRequest{{SourceFormat: "claude", RequestedModel: "grok-4.6", Metadata: map[string]any{"request_path": "/v1/messages/count_tokens"}}, {SourceFormat: "interactions", RequestedModel: "grok-4.6", Body: []byte(`{"agent":"research-agent"}`)}} {
		route, ok := host.RouteModel(context.Background(), guard)
		if ok || route.Handled {
			t.Fatalf("guard handled=%v route=%+v", ok, route)
		}
	}
	apply("grok-4.6=>grok-4.6")
	route, ok := host.RouteModel(context.Background(), pluginapi.ModelRouteRequest{SourceFormat: "openai", RequestedModel: "grok-4.6"})
	if ok || route.Handled {
		t.Fatal("identity route must remain unhandled")
	}
	apply("grok-4.6=>grok-4.7")
	check()
	if !host.UnloadPlugin("model-mapper") {
		t.Fatal("first unload failed")
	}
	if len(host.activeRecords()) != 0 {
		t.Fatal("unload kept active record")
	}
	apply("grok-4.6=>grok-4.7")
	check()
}

func TestModelMapperFunctionalCompatLifecycle(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(fmt.Sprintf("large=%v", large), func(t *testing.T) {
			var calls int
			var upstream []byte
			count, text, id := 1, "onetwo", "chatcmpl-functional"
			if large {
				count, text, id = 257, strings.Repeat("x", 64<<10), "large-0001"
			}
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				upstream = raw
				var req struct {
					Model  string
					Stream bool
				}
				if err := json.Unmarshal(raw, &req); err != nil {
					t.Error(err)
					return
				}
				if r.URL.Path != "/v1/chat/completions" || req.Model != "grok-4.7" {
					t.Errorf("compat upstream path=%s body=%s", r.URL.Path, raw)
				}
				if !req.Stream {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"interaction-functional","object":"chat.completion","created":1,"model":"`+req.Model+`","choices":[{"index":0,"message":{"role":"assistant","content":"ordinary grok-4.7 opaque 中文 output"},"finish_reason":"stop"}]}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for i := 0; i < count; i++ {
					payload, err := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "created": 1, "model": req.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": text}}}})
					if err != nil {
						t.Error(err)
						return
					}
					if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
						return
					}
					w.(http.Flusher).Flush()
				}
				_, _ = fmt.Fprintf(w, "data: {\"id\":%q,\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"grok-4.7\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", id)
			}))
			t.Cleanup(local.Close)
			cfg := &config.Config{}
			provider := runtimeexecutor.NewOpenAICompatExecutor("functional-openai", cfg)
			manager := coreauth.NewManager(nil, nil, nil)
			manager.SetConfig(cfg)
			manager.RegisterExecutor(provider)
			auth := &coreauth.Auth{ID: "functional-openai-auth", Provider: provider.Identifier(), Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "fake-upstream-key", "base_url": local.URL + "/v1", "proxy_url": "direct"}}
			if _, err := manager.Register(context.Background(), auth); err != nil {
				t.Fatal(err)
			}
			registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "grok-4.6"}, {ID: "grok-4.7"}})
			t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
			base := handlers.NewBaseAPIHandlers(&cfg.SDKConfig, manager)
			if large {
				t.Run("large-host-units", func(t *testing.T) {
					calls = 0
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					result, msg := base.ExecuteModelStream(ctx, handlers.ModelExecutionRequest{EntryProtocol: "openai-response", ExitProtocol: "openai-response", Model: "grok-4.7", Stream: true, Body: gCPARequest("grok-4.7", true)})
					if msg != nil {
						t.Fatal(msg.Error)
					}
					units, done := 0, 0
					for chunk := range result.Chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
						units++
						if bytes.HasPrefix(chunk.Payload, []byte("event: response.output_text.done\n")) {
							done++
							_, data, ok := bytes.Cut(chunk.Payload, []byte("\ndata: "))
							var value struct{ Type, Text string }
							if !ok || json.Unmarshal(data, &value) != nil || value.Type != "response.output_text.done" || value.Text != strings.Repeat(text, count) || len(chunk.Payload) != 16842937 {
								t.Fatalf("real translator/validator done unit bytes=%d textBytes=%d", len(chunk.Payload), len(value.Text))
							}
							t.Logf("real translator/validator complete output unit bytes=%d SHA256=%x textBytes=%d", len(chunk.Payload), sha256.Sum256(chunk.Payload), len(value.Text))
						}
					}
					if calls != 1 || units != count+8 || done != 1 {
						t.Fatalf("real host units=%d done=%d calls=%d", units, done, calls)
					}
				})
			}
			host := eNativeHost(t, base)
			base.SetPluginHost(host)
			base.SetModelRouterHost(host)
			router := gin.New()
			router.POST("/v1/responses", openaihandlers.NewOpenAIResponsesAPIHandler(base).Responses)
			router.POST("/v1beta/interactions", geminihandlers.NewGeminiAPIHandler(base).Interactions)
			server := httptest.NewServer(router)
			t.Cleanup(server.Close)
			for _, model := range []string{"grok-4.6", "grok-4.7"} {
				t.Run("responses-http/"+model, func(t *testing.T) {
					calls = 0
					status, headers, raw := eHTTP(t, server, "/v1/responses", gCPARequest(model, true))
					if status != 200 || calls != 1 {
						t.Fatalf("status=%d calls=%d bytes=%d", status, calls, len(raw))
					}
					events, err := sse.Decode(bytes.NewReader(raw))
					if err != nil {
						t.Fatal(err)
					}
					var names []string
					var content strings.Builder
					var doneBytes int
					var output string
					for _, ev := range events {
						data, ok := ev.Data.(string)
						if !ok || data == "" {
							continue
						}
						var value struct {
							Type, Delta, Text string
							Response          struct {
								Model  string
								Output []struct{ Content []struct{ Text string } }
							}
						}
						if err := json.Unmarshal([]byte(data), &value); err != nil {
							t.Fatal(err)
						}
						if ev.Event != value.Type {
							t.Fatalf("event=%q type=%q", ev.Event, value.Type)
						}
						names = append(names, value.Type)
						switch value.Type {
						case "response.created", "response.in_progress", "response.completed":
							if value.Response.Model != model {
								t.Fatalf("%s model=%q", value.Type, value.Response.Model)
							}
							if value.Type == "response.completed" {
								if len(value.Response.Output) != 1 || len(value.Response.Output[0].Content) != 1 {
									t.Fatalf("completed output count=%d", len(value.Response.Output))
								}
								output = value.Response.Output[0].Content[0].Text
							}
						case "response.output_text.delta":
							content.WriteString(value.Delta)
						case "response.output_text.done":
							if value.Text != strings.Repeat(text, count) {
								t.Fatal("done text changed")
							}
							doneBytes = len("event: " + ev.Event + "\ndata: " + data + "\n\n")
						}
					}
					wantNames := []string{"response.created", "response.in_progress", "response.output_item.added", "response.content_part.added"}
					for i := 0; i < count; i++ {
						wantNames = append(wantNames, "response.output_text.delta")
					}
					wantNames = append(wantNames, "response.output_text.done", "response.content_part.done", "response.output_item.done", "response.completed")
					if !reflect.DeepEqual(names, wantNames) || content.String() != strings.Repeat(text, count) || output != strings.Repeat(text, count) {
						t.Fatalf("events=%q deltaBytes=%d outputBytes=%d", names, content.Len(), len(output))
					}
					t.Logf("compat model=%s chunks=%d deltaBytes=%d doneBytes=%d totalBytes=%d SHA256=%x headers=%v upstream=%s", model, count, content.Len(), doneBytes, len(raw), sha256.Sum256(raw), headers, upstream)
					if large && doneBytes != 16842937+2 {
						t.Fatalf("large HTTP complete event bytes=%d want=16842939", doneBytes)
					}
				})
			}
			if !large {
				for _, model := range []string{"grok-4.6", "grok-4.7"} {
					t.Run("interactions/"+model, func(t *testing.T) {
						calls = 0
						status, _, raw := eHTTP(t, server, "/v1beta/interactions", []byte(fmt.Sprintf(`{"model":%q,"input":"hello","stream":true}`, model)))
						if status != 200 || calls != 1 {
							t.Fatalf("status=%d calls=%d output=%q", status, calls, raw)
						}
						events, err := sse.Decode(bytes.NewReader(raw))
						if err != nil {
							t.Fatal(err)
						}
						var names []string
						var content strings.Builder
						for _, ev := range events {
							data, ok := ev.Data.(string)
							if !ok || data == "" {
								continue
							}
							if data == "[DONE]" {
								if ev.Event != "done" {
									t.Fatalf("done event=%q", ev.Event)
								}
								names = append(names, "done")
								continue
							}
							var value struct {
								EventType   string `json:"event_type"`
								Interaction struct{ Model string }
								Delta       struct{ Text string }
							}
							if err := json.Unmarshal([]byte(data), &value); err != nil {
								t.Fatal(err)
							}
							if ev.Event != value.EventType {
								t.Fatalf("event=%q type=%q payload=%s", ev.Event, value.EventType, data)
							}
							names = append(names, value.EventType)
							if value.Interaction.Model != "" && value.Interaction.Model != model {
								t.Fatalf("interaction model=%q", value.Interaction.Model)
							}
							content.WriteString(value.Delta.Text)
						}
						want := []string{"interaction.created", "interaction.status_update", "step.start", "step.delta", "step.stop", "interaction.completed", "done"}
						if !reflect.DeepEqual(names, want) || content.String() != "onetwo" {
							t.Fatalf("events=%q text=%q wire=%q", names, content.String(), raw)
						}
						t.Logf("OpenAI-compatible Interactions model=%s complete events=%q upstream=%s", model, names, upstream)
					})
					t.Run("interactions-nonstream/"+model, func(t *testing.T) {
						calls = 0
						status, _, raw := eHTTP(t, server, "/v1beta/interactions", []byte(fmt.Sprintf(`{"model":%q,"input":"hello","stream":false}`, model)))
						var request struct {
							Model  string
							Stream bool
						}
						if err := json.Unmarshal(upstream, &request); err != nil {
							t.Fatal(err)
						}
						want := `{"id":"interaction-functional","status":"completed","object":"interaction","model":"` + model + `","steps":[{"type":"model_output","content":[{"type":"text","text":"ordinary grok-4.7 opaque 中文 output"}]}],"finish_reason":"stop"}`
						if status != 200 || calls != 1 || request.Model != "grok-4.7" || request.Stream || string(raw) != want {
							t.Fatalf("Interactions nonstream status=%d calls=%d upstream=%s output=%s want=%s", status, calls, upstream, raw, want)
						}
						t.Logf("OpenAI-compatible Interactions nonstream model=%s status=%d calls=%d upstream=%s complete=%s", model, status, calls, upstream, raw)
					})
				}
			}
		})
	}
}

func TestModelMapperFunctionalCodexTruncatedPrefix(t *testing.T) {
	p := gCPANewLocalProducer(t, "codex")
	host := gCPALoadNative(t, p, true)
	p.fixture = gCPAFieldsFixture{count: 1, contentType: "text/event-stream"}
	created := `{"type":"response.created","response":{"model":"grok-4.7","status":"in_progress","output":[]}}`
	for _, prefix := range []string{"e", "ev", "eve", "even", "event", "event:"} {
		t.Run(prefix, func(t *testing.T) {
			p.wire = []byte("event: response.created\ndata: " + created + "\n" + prefix)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			validated, msg := p.base.ExecuteModelStream(ctx, handlers.ModelExecutionRequest{EntryProtocol: "openai-response", ExitProtocol: "openai-response", Model: "grok-4.7", Stream: true, Body: gCPARequest("grok-4.7", true)})
			if msg != nil {
				t.Fatal(msg.Error)
			}
			var inputs []string
			var original string
			for chunk := range validated.Chunks {
				if chunk.Err != nil {
					original = chunk.Err.Error()
				} else {
					inputs = append(inputs, string(chunk.Payload))
				}
			}
			if original == "" || !strings.Contains(original, "stream disconnected before completion") {
				t.Fatalf("original error=%q inputs=%q", original, inputs)
			}
			result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, eNativeRequest("grok-4.6", "openai-response", true))
			if err != nil {
				t.Fatal(err)
			}
			raw, errs := eNativeDrain(t, result.Chunks)
			if len(errs) != 1 || !strings.Contains(errs[0], original) {
				t.Fatalf("errors=%q original=%q", errs, original)
			}
			want := strings.Replace(created, `"model":"grok-4.7"`, `"model":"grok-4.6"`, 1)
			eRequireData(t, raw, []string{want})
			t.Logf("Codex prefix=%q host=%q native=%q original error=%q", prefix, inputs, raw, original)
		})
	}
}

func TestModelMapperFunctionalBinaryCaptures(t *testing.T) {
	path := os.Getenv("CPA_FUNCTIONAL_BINARY_CAPTURES")
	if path == "" {
		t.Fatal("binary capture file is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var captures []struct {
		Name, Fixture, ClientModel, UpstreamModel, Metadata string
		Stream, DataOnly                                    bool
		Status, Count, Calls                                int
		Headers                                             http.Header
		Body, Request, UpstreamResponse                     []byte
		RequestHeaders                                      http.Header
		Path                                                string
	}
	if err := json.Unmarshal(raw, &captures); err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1536 {
		t.Fatalf("binary captures=%d want=1536", len(captures))
	}
	type expectation struct {
		fixture         gCPAFieldsFixture
		model, upstream string
	}
	want := make(map[string]expectation)
	for _, provider := range []string{"xai", "codex"} {
		for _, control := range []struct{ name, model, upstream string }{
			{"mapped", "grok-4.6", "grok-4.7"},
			{"direct", "grok-4.7", "grok-4.7"},
			{"disabled-grok-4.6", "grok-4.6", "grok-4.6"},
			{"disabled-grok-4.7", "grok-4.7", "grok-4.7"},
		} {
			for _, f := range gCPAFieldsFixtures() {
				for _, stream := range []bool{false, true} {
					key := fmt.Sprintf("%s/%s/%s/stream=%v", provider, control.name, f.name, stream)
					want[key] = expectation{f, control.model, control.upstream}
				}
			}
		}
	}
	for _, capture := range captures {
		key := fmt.Sprintf("%s/stream=%v", capture.Name, capture.Stream)
		expected, ok := want[key]
		if !ok {
			t.Fatalf("unexpected or duplicate binary capture=%s", key)
		}
		delete(want, key)
		t.Run(key, func(t *testing.T) {
			f := expected.fixture
			if capture.Metadata != f.metadata || capture.Fixture != f.name || capture.DataOnly != f.dataOnly || capture.Count != f.count || capture.ClientModel != expected.model || capture.UpstreamModel != expected.upstream {
				t.Fatalf("binary capture identity=%+v want=%+v", capture, expected)
			}
			if capture.Status != 200 || capture.Calls != 1 {
				t.Fatalf("binary status=%d calls=%d body=%q", capture.Status, capture.Calls, capture.Body)
			}
			if capture.Stream {
				if !strings.HasPrefix(capture.Headers.Get("Content-Type"), "text/event-stream") {
					t.Fatalf("binary Content-Type=%q", capture.Headers.Get("Content-Type"))
				}
				gCPARequireEvents(t, capture.Body, f, expected.model)
			} else {
				gCPARequireResponse(t, capture.Body, expected.model)
			}
			var request struct {
				Model          string
				Stream         bool
				Input          json.RawMessage
				PromptCacheKey string `json:"prompt_cache_key"`
			}
			if err := json.Unmarshal(capture.Request, &request); err != nil {
				t.Fatal(err)
			}
			if request.Model != expected.upstream || !request.Stream || request.PromptCacheKey != "task-g-local" || string(request.Input) != `[{"type":"message","role":"user","content":[{"type":"input_text","text":"say ok"}]}]` {
				t.Fatalf("upstream request=%s", capture.Request)
			}
			if capture.Path != "/v1/responses" || capture.RequestHeaders.Get("Content-Length") != fmt.Sprint(len(capture.Request)) || capture.RequestHeaders.Get("Authorization") != "Bearer fake-upstream-key" {
				t.Fatalf("path=%s headers=%v bytes=%d", capture.Path, capture.RequestHeaders, len(capture.Request))
			}
			_, wire := gCPAFields(f, expected.upstream)
			if !bytes.Equal(capture.UpstreamResponse, wire) {
				t.Fatalf("upstream response=%q want=%q", capture.UpstreamResponse, wire)
			}
			t.Logf("binary %s status=%d calls=%d client=%s upstream=%s requestBytes=%d responseSHA256=%x headers=%v", key, capture.Status, capture.Calls, capture.ClientModel, capture.UpstreamModel, len(capture.Request), sha256.Sum256(capture.Body), capture.Headers)
		})
	}
	if len(want) != 0 {
		t.Fatalf("missing binary captures=%v", want)
	}
}

func TestModelMapperFunctionalInteractionsAgent(t *testing.T) {
	const body = `{"id":"interaction-agent","agent":"research-agent","status":"completed","outputs":[{"text":"opaque grok-4.6/grok-4.7 中文"}]}`
	const start = `{"event_type":"interaction.created","interaction":{"agent":"research-agent","status":"in_progress"}}`
	const completed = `{"event_type":"interaction.completed","interaction":{"agent":"research-agent","status":"completed","outputs":[{"text":"opaque grok-4.6/grok-4.7 中文"}]}}`
	wire := "event: interaction.created\ndata: " + start + "\n\nevent: interaction.completed\ndata: " + completed + "\n\nevent: done\ndata: [DONE]\n\n"
	var controls [][]byte
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			var calls int
			var requestBody []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				requestBody = raw
				var req struct {
					Agent, Model string
					Stream       bool
				}
				if err := json.Unmarshal(raw, &req); err != nil {
					t.Error(err)
					return
				}
				if req.Agent != "research-agent" || req.Model != "" || r.URL.Path != "/v1beta/interactions" || r.Header.Get("X-Goog-Api-Key") != "fake-upstream-key" {
					t.Errorf("agent upstream path=%s headers=%v body=%s", r.URL.Path, r.Header, raw)
				}
				if req.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, wire)
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, body)
				}
			}))
			t.Cleanup(upstream.Close)
			cfg := &config.Config{}
			provider := runtimeexecutor.NewGeminiInteractionsExecutor(cfg)
			manager := coreauth.NewManager(nil, nil, nil)
			manager.SetConfig(cfg)
			manager.RegisterExecutor(provider)
			auth := &coreauth.Auth{ID: "functional-agent-auth", Provider: provider.Identifier(), Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "fake-upstream-key", "base_url": upstream.URL, "proxy_url": "direct"}}
			if _, err := manager.Register(context.Background(), auth); err != nil {
				t.Fatal(err)
			}
			registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "gemini-2.5-flash"}})
			t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
			base := handlers.NewBaseAPIHandlers(&cfg.SDKConfig, manager)
			host := gCPALoadNative(t, &gCPALocalProducer{base: base}, enabled)
			base.SetPluginHost(host)
			base.SetModelRouterHost(host)
			router := gin.New()
			router.POST("/v1beta/interactions", geminihandlers.NewGeminiAPIHandler(base).Interactions)
			server := httptest.NewServer(router)
			t.Cleanup(server.Close)
			for i, stream := range []bool{false, true} {
				request := []byte(fmt.Sprintf(`{"agent":"research-agent","input":"hello","stream":%v}`, stream))
				route, ok := host.RouteModel(context.Background(), pluginapi.ModelRouteRequest{SourceFormat: "interactions", RequestedModel: "research-agent", Stream: stream, Body: request})
				if ok || route.Handled {
					t.Fatalf("agent route=%+v handled=%v", route, ok)
				}
				calls = 0
				status, _, raw := eHTTP(t, server, "/v1beta/interactions", request)
				if status != 200 || calls != 1 {
					t.Fatalf("agent HTTP=%d calls=%d body=%q", status, calls, raw)
				}
				if !stream && string(raw) != body {
					t.Fatalf("agent nonstream=%q", raw)
				}
				if stream {
					events, err := sse.Decode(bytes.NewReader(raw))
					if err != nil {
						t.Fatal(err)
					}
					var payloads []string
					for _, ev := range events {
						if value, ok := ev.Data.(string); ok && value != "" {
							payloads = append(payloads, value)
						}
					}
					if !reflect.DeepEqual(payloads, []string{start, completed, "[DONE]"}) {
						t.Fatalf("agent stream=%q", raw)
					}
				}
				if !enabled {
					controls = append(controls, bytes.Clone(raw))
				} else if !bytes.Equal(raw, controls[i]) {
					t.Fatalf("agent enabled/disabled bytes differ=%q want=%q", raw, controls[i])
				}
				t.Logf("native Interactions agent stream=%v mappingEnabled=%v upstream=%s output=%q", stream, enabled, requestBody, raw)
			}
		})
	}
}

func TestModelMapperFunctionalValidatorPrefixLimit(t *testing.T) {
	p := &eControlledProvider{}
	base := eControlledBase(t, p)
	host := eNativeHost(t, base)
	for _, format := range []string{"openai", "claude", "gemini", "openai-response"} {
		t.Run(format, func(t *testing.T) {
			prefix, tail, want := `{"model":"grok-4.7"} `, `{"text":"`, `{"model":"grok-4.6"}`
			if format == "claude" {
				prefix, tail, want = "data: {\"model\":\"grok-4.7\"}\n\n", "data: {\"text\":\"", "data: {\"model\":\"grok-4.6\"}\n\n"
			}
			if format == "gemini" {
				prefix, tail, want = `[{"modelVersion":"grok-4.7"},`, `{"text":"`, `[{"modelVersion":"grok-4.6"},`
			}
			if format == "openai-response" {
				want = "data: {\"model\":\"grok-4.6\"}\n\n"
			}
			input := []byte(prefix + tail + strings.Repeat("x", (16<<20)+1-len(tail)))
			p.parts = [][]byte{input}
			p.calls = nil
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, msg := base.ExecuteModelStream(ctx, handlers.ModelExecutionRequest{EntryProtocol: format, ExitProtocol: format, Model: "grok-4.7", Stream: true, Body: gCPARequest("grok-4.7", true)})
			if msg != nil {
				t.Fatalf("unexpected pre-ABI error=%+v", msg)
			}
			var parts [][]byte
			var original []string
			for chunk := range result.Chunks {
				if chunk.Err != nil {
					original = append(original, chunk.Err.Error())
				} else {
					parts = append(parts, chunk.Payload)
				}
			}
			if !reflect.DeepEqual(parts, p.parts) || len(original) != 0 || len(p.calls) != 1 {
				t.Fatalf("validator changed payloads: parts=%d bytes=%d errors=%q calls=%d", len(parts), len(input), original, len(p.calls))
			}
			p.calls = nil
			native, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, eNativeRequest("grok-4.6", format, true))
			if err != nil {
				t.Fatal(err)
			}
			raw, errs := eNativeDrain(t, native.Chunks)
			if string(bytes.TrimRight(raw, " ")) != want || len(errs) != 1 || !strings.Contains(errs[0], "stream pending data exceeds") || len(p.calls) != 1 {
				t.Fatalf("F07 native prefix=%q errors=%q calls=%d", raw, errs, len(p.calls))
			}
			t.Logf("F07 format=%s actual validator bytes=%d originalErrors=%q nativePrefix=%q nativeError=%q", format, len(input), original, raw, errs)
		})
	}
}

func TestModelMapperFunctionalOrdinaryMetadata(t *testing.T) {
	for _, provider := range []string{"xai", "codex"} {
		for _, f := range gCPAFieldsFixtures() {
			t.Run(provider+"/"+f.name, func(t *testing.T) {
				p := gCPANewLocalProducer(t, provider)
				p.setFixture(f)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				gCPARequireFields(t, gCPACoreFields(t, p, ctx), f, "grok-4.7")
				gCPARequireFields(t, gCPAHostFields(t, p, ctx), f, "grok-4.7")
				server := eResponsesServer(t, p.base)
				t.Run("direct", func(t *testing.T) {
					status, _, raw := eHTTP(t, server, "/v1/responses", gCPARequest("grok-4.7", true))
					if status != 200 {
						t.Fatalf("direct HTTP=%d body=%q", status, raw)
					}
					gCPARequireEvents(t, raw, f, "grok-4.7")
					ws, err := eWS(t, server, "grok-4.7")
					if err != nil || !reflect.DeepEqual(ws, gCPAPayloads("grok-4.7", f.count)) {
						t.Fatalf("direct WS=%q error=%v", ws, err)
					}
				})
				host := gCPALoadNative(t, p, true)
				canonical := f
				canonical.eol = "\n"
				_, want := gCPAFields(canonical, "grok-4.6")
				t.Run("mapped-native", func(t *testing.T) {
					result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, eNativeRequest("grok-4.6", "openai-response", true))
					if err != nil {
						t.Fatal(err)
					}
					raw, errs := eNativeDrain(t, result.Chunks)
					t.Logf("native=%q errors=%q", raw, errs)
					comparable, expected := raw, want
					if f.dataOnly && f.count == 1 {
						comparable, expected = bytes.TrimSuffix(raw, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))
					}
					if len(errs) != 0 || !bytes.Equal(comparable, expected) {
						t.Errorf("mapped native bytes=%q errors=%q want=%q", raw, errs, expected)
					}
					gCPARequireEvents(t, raw, f, "grok-4.6")
				})
				p.base.SetPluginHost(host)
				p.base.SetModelRouterHost(host)
				t.Run("mapped-http", func(t *testing.T) {
					status, _, raw := eHTTP(t, server, "/v1/responses", gCPARequest("grok-4.6", true))
					t.Logf("mapped HTTP=%d body=%q", status, raw)
					if status != 200 {
						t.Fatalf("mapped HTTP=%d want=200", status)
					}
					gCPARequireEvents(t, raw, f, "grok-4.6")
					if !bytes.Equal(raw, append(bytes.Clone(want), '\n')) {
						t.Fatalf("mapped HTTP bytes=%q want=%q", raw, append(bytes.Clone(want), '\n'))
					}
				})
				t.Run("mapped-ws", func(t *testing.T) {
					ws, err := eWS(t, server, "grok-4.6")
					t.Logf("mapped WS=%q error=%v", ws, err)
					if err != nil || !reflect.DeepEqual(ws, gCPAPayloads("grok-4.6", f.count)) {
						t.Fatalf("mapped WS=%q error=%v", ws, err)
					}
				})
			})
		}
	}
}

func TestModelMapperFunctionalOrdinaryMetadataDisabledWS(t *testing.T) {
	for _, provider := range []string{"xai", "codex"} {
		p := gCPANewLocalProducer(t, provider)
		host := gCPALoadNative(t, p, false)
		p.base.SetPluginHost(host)
		p.base.SetModelRouterHost(host)
		server := eResponsesServer(t, p.base)
		for _, f := range gCPAFieldsFixtures() {
			for _, model := range []string{"grok-4.6", "grok-4.7"} {
				t.Run(provider+"/"+f.name+"/"+model, func(t *testing.T) {
					p.setFixture(f)
					ws, err := eWS(t, server, model)
					if err != nil || !reflect.DeepEqual(ws, gCPAPayloads(model, f.count)) {
						t.Fatalf("disabled WS=%q error=%v", ws, err)
					}
					p.mu.Lock()
					calls := append([]gCPAUpstreamRecord(nil), p.records...)
					p.mu.Unlock()
					if len(calls) != 1 || calls[0].model != model {
						t.Fatalf("disabled calls=%+v", calls)
					}
				})
			}
		}
	}
}

func TestModelMapperFunctionalOrdinaryMetadataConsumerControls(t *testing.T) {
	for _, provider := range []string{"xai", "codex"} {
		for _, dataOnly := range []bool{false, true} {
			for _, count := range []int{1, 9} {
				for _, metadata := range [][]string{{"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}} {
					for _, position := range []string{"before-event", "in-event", "between-events"} {
						t.Run(fmt.Sprintf("%s/dataOnly=%v/count=%d/metadata=%q/%s", provider, dataOnly, count, metadata, position), func(t *testing.T) {
							p := gCPANewLocalProducer(t, provider)
							f := gCPAFieldsFixture{count: count, dataOnly: dataOnly, contentType: "text/event-stream", eol: "\n"}
							p.fixture = f
							var fields []string
							var wire, expectedWire strings.Builder
							original, _ := gCPAFields(gCPAFieldsFixture{count: count, dataOnly: false, eol: "\n"}, "grok-4.7")
							expectedPayloads := gCPAPayloads("grok-4.6", count)
							for i, payload := range gCPAPayloads("grok-4.7", count) {
								name := original[i*2]
								add := func(value string) {
									fields = append(fields, value)
									wire.WriteString(value + "\n")
									expectedWire.WriteString(value + "\n")
								}
								if position == "before-event" || position == "between-events" && i > 0 {
									for _, field := range metadata {
										add(field)
									}
								}
								if !dataOnly {
									add(name)
								}
								if position == "in-event" {
									for _, field := range metadata {
										add(field)
									}
								}
								fields = append(fields, "data: "+payload)
								wire.WriteString("data: " + payload + "\n\n")
								expectedWire.WriteString("data: " + expectedPayloads[i] + "\n\n")
							}
							p.wire = []byte(wire.String())
							want := []byte(expectedWire.String())
							requireMetadata := func(t *testing.T, raw []byte) {
								t.Helper()
								for _, field := range metadata {
									expected := count
									if position == "between-events" {
										expected = count - 1
									}
									if got := bytes.Count(append([]byte("\n"), raw...), []byte("\n"+field+"\n")); got != expected {
										t.Fatalf("metadata %q count=%d want=%d bytes=%q", field, got, expected, raw)
									}
								}
							}
							ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
							defer cancel()
							if got := gCPACoreFields(t, p, ctx); !reflect.DeepEqual(got, fields) {
								t.Fatalf("core=%q want=%q", got, fields)
							}
							if got := gCPAHostFields(t, p, ctx); !reflect.DeepEqual(got, fields) {
								t.Fatalf("host=%q want=%q", got, fields)
							}
							server := eResponsesServer(t, p.base)
							status, _, raw := eHTTP(t, server, "/v1/responses", gCPARequest("grok-4.7", true))
							if status != 200 {
								t.Fatalf("direct HTTP=%d body=%q", status, raw)
							}
							gCPARequireEvents(t, raw, f, "grok-4.7")
							requireMetadata(t, raw)
							ws, err := eWS(t, server, "grok-4.7")
							if err != nil || !reflect.DeepEqual(ws, gCPAPayloads("grok-4.7", count)) {
								t.Fatalf("direct WS=%q error=%v", ws, err)
							}
							t.Logf("original consumer position=%s HTTP=%q WS=%q", position, raw, ws)
							host := gCPALoadNative(t, p, true)
							t.Run("mapped-native", func(t *testing.T) {
								result, err := host.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, eNativeRequest("grok-4.6", "openai-response", true))
								if err != nil {
									t.Fatal(err)
								}
								native, errs := eNativeDrain(t, result.Chunks)
								if len(errs) != 0 {
									t.Fatalf("native errors=%q", errs)
								}
								eRequireData(t, native, expectedPayloads)
								requireMetadata(t, native)
								comparable, expected := native, want
								if dataOnly && count == 1 {
									comparable, expected = bytes.TrimSuffix(native, []byte("\n\n")), bytes.TrimSuffix(want, []byte("\n\n"))
								}
								if !bytes.Equal(comparable, expected) {
									t.Fatalf("mapped position native bytes=%q want=%q", native, expected)
								}
							})
							p.base.SetPluginHost(host)
							p.base.SetModelRouterHost(host)
							t.Run("mapped-http", func(t *testing.T) {
								status, _, mapped := eHTTP(t, server, "/v1/responses", gCPARequest("grok-4.6", true))
								if status != 200 {
									t.Fatalf("mapped position HTTP=%d body=%q", status, mapped)
								}
								gCPARequireEvents(t, mapped, f, "grok-4.6")
								requireMetadata(t, mapped)
								if !bytes.Equal(mapped, append(bytes.Clone(want), '\n')) {
									t.Fatalf("mapped position HTTP bytes=%q want=%q", mapped, append(bytes.Clone(want), '\n'))
								}
							})
							t.Run("mapped-ws", func(t *testing.T) {
								wsMapped, err := eWS(t, server, "grok-4.6")
								if err != nil || !reflect.DeepEqual(wsMapped, gCPAPayloads("grok-4.6", count)) {
									t.Fatalf("mapped position WS=%q error=%v", wsMapped, err)
								}
							})
						})
					}
				}
			}
		}
	}
}

type hCPAMixedFixture struct {
	name, eol, contentType, metadata string
	count, step                      int
}

func hCPAMixedFixtures() []hCPAMixedFixture {
	var fixtures []hCPAMixedFixture
	for _, count := range []int{2, 9} {
		for _, metadata := range []struct{ label, fields string }{{"none", ""}, {"id", "id: event-1"}, {"retry", "retry: 100"}, {"heartbeat", ": heartbeat"}, {"combined", "id: event-1\nretry: 100\n: heartbeat"}} {
			for _, transport := range []hCPAMixedFixture{{name: "LF-whole", eol: "\n", contentType: "text/event-stream"}, {name: "CRLF-whole", eol: "\r\n", contentType: "text/event-stream"}, {name: "LF-7bytes", eol: "\n", contentType: "text/event-stream", step: 7}, {name: "CRLF-bytewise", eol: "\r\n", contentType: "text/event-stream", step: 1}, {name: "LF-charset", eol: "\n", contentType: "text/event-stream; charset=utf-8", step: 11}, {name: "CRLF-charset", eol: "\r\n", contentType: "text/event-stream; charset=utf-8", step: 7}} {
				f := transport
				f.name = fmt.Sprintf("mixed/count=%d/metadata=%s/%s", count, metadata.label, transport.name)
				f.count = count
				f.metadata = metadata.fields
				fixtures = append(fixtures, f)
			}
		}
	}
	return fixtures
}

func hCPAMixedPayloads(model string, count int) []string {
	values := gCPAPayloads(model, 9)
	if count == 2 {
		return []string{values[0], values[8]}
	}
	return values
}

func hCPAMixedFields(f hCPAMixedFixture, model string) ([]string, []byte) {
	var fields []string
	var wire strings.Builder
	for i, payload := range hCPAMixedPayloads(model, f.count) {
		if i == 1 && f.metadata != "" {
			for _, field := range strings.Split(f.metadata, "\n") {
				fields = append(fields, field)
				wire.WriteString(field + f.eol)
			}
		}
		var value struct{ Type string }
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			panic(err)
		}
		if i > 0 {
			fields = append(fields, "event: "+value.Type)
			wire.WriteString("event: " + value.Type + f.eol)
		}
		fields = append(fields, "data: "+payload)
		wire.WriteString("data: " + payload + f.eol + f.eol)
	}
	return fields, []byte(wire.String())
}

// 固定 CPA 的 direct mixed HTTP 会把下一 event 名称附在前一 data 上，末 completed 不派发。
func hCPADirectHTTPWire(f hCPAMixedFixture, model string) []byte {
	values := hCPAMixedPayloads(model, f.count)
	var wire strings.Builder
	for i := 0; i < len(values)-1; i++ {
		wire.WriteString("data: " + values[i] + "\n")
		if i == 0 && f.metadata != "" {
			wire.WriteString(f.metadata + "\n")
		}
		var next struct{ Type string }
		if err := json.Unmarshal([]byte(values[i+1]), &next); err != nil {
			panic(err)
		}
		wire.WriteString("event: " + next.Type + "\n\n")
	}
	wire.WriteByte('\n')
	return []byte(wire.String())
}

func hCPARequireMixedEvents(t *testing.T, raw []byte, f hCPAMixedFixture, model string) {
	t.Helper()
	normalized := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	if f.metadata != "" {
		for _, field := range strings.Split(f.metadata, "\n") {
			if bytes.Count(append([]byte("\n"), normalized...), []byte("\n"+field+"\n")) != 1 {
				t.Errorf("metadata field=%q bytes=%q", field, raw)
			}
		}
	}
	events, err := sse.Decode(bytes.NewReader(normalized))
	if err != nil {
		t.Fatal(err)
	}
	var payloads []string
	for _, event := range events {
		payload, ok := event.Data.(string)
		if !ok || payload == "" {
			continue
		}
		var value struct{ Type string }
		if err := json.Unmarshal([]byte(payload), &value); err != nil {
			t.Errorf("mixed invalid JSON: %v payload=%q", err, payload)
			continue
		}
		name := value.Type
		if len(payloads) == 0 {
			name = "message"
		}
		if event.Event != name {
			t.Errorf("mixed event=%q want=%q", event.Event, name)
		}
		payloads = append(payloads, payload)
	}
	if !reflect.DeepEqual(payloads, hCPAMixedPayloads(model, f.count)) {
		t.Errorf("mixed payloads=%q want=%q", payloads, hCPAMixedPayloads(model, f.count))
	}
}

type hCPACapture struct {
	Name, Fixture, ClientModel, UpstreamModel, Metadata, Kind string
	Stream, DataOnly                                          bool
	Status, Count, Calls                                      int
	Headers, RequestHeaders                                   http.Header
	Body, Request, UpstreamResponse                           []byte
	Path                                                      string
	WS, Errors, Fields                                        []string
}

func hCPASaveEvidence(t *testing.T, name string, value any) {
	t.Helper()
	if dir := os.Getenv("CPA_FUNCTIONAL_EVIDENCE"); dir != "" {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("evidence already exists: %s %v", path, err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("evidence=%s SHA256=%x", path, sha256.Sum256(raw))
	}
}

func TestModelMapperFunctionalMixedMetadata(t *testing.T) {
	fixtures := hCPAMixedFixtures()
	var captures []hCPACapture
	defer func() { hCPASaveEvidence(t, "mixed-native-captures.json", captures) }()
	want := make(map[string]bool)
	for _, provider := range []string{"xai", "codex"} {
		for _, f := range fixtures {
			for _, route := range []string{"core", "host", "mapped", "direct", "disabled-grok-4.6", "disabled-grok-4.7"} {
				kinds := []string{"http-stream", "http-nonstream", "ws"}
				if route == "core" || route == "host" {
					kinds = []string{"fields"}
				}
				if route == "mapped" {
					kinds = append(kinds, "native")
				}
				for _, kind := range kinds {
					want[provider+"/"+route+"/"+f.name+"/"+kind] = true
				}
			}
		}
	}
	for _, provider := range []string{"xai", "codex"} {
		t.Run(provider, func(t *testing.T) {
			p := gCPANewLocalProducer(t, provider)
			enabled := gCPALoadNative(t, p, true)
			disabled := gCPALoadNative(t, p, false)
			server := eResponsesServer(t, p.base)
			for _, f := range fixtures {
				for _, route := range []string{"core", "host", "mapped", "direct", "disabled-grok-4.6", "disabled-grok-4.7"} {
					kinds := []string{"http-stream", "http-nonstream", "ws"}
					if route == "core" || route == "host" {
						kinds = []string{"fields"}
					}
					if route == "mapped" {
						kinds = append(kinds, "native")
					}
					for _, kind := range kinds {
						t.Run(route+"/"+f.name+"/"+kind, func(t *testing.T) {
							model, upstream := "grok-4.7", "grok-4.7"
							p.base.SetPluginHost(nil)
							p.base.SetModelRouterHost(nil)
							if route == "mapped" {
								model = "grok-4.6"
								if kind != "native" {
									p.base.SetPluginHost(enabled)
									p.base.SetModelRouterHost(enabled)
								}
							} else if strings.HasPrefix(route, "disabled-") {
								model = strings.TrimPrefix(route, "disabled-")
								upstream = model
								p.base.SetPluginHost(disabled)
								p.base.SetModelRouterHost(disabled)
							}
							fields, wire := hCPAMixedFields(f, upstream)
							p.mu.Lock()
							p.fixture = gCPAFieldsFixture{contentType: f.contentType, eol: f.eol, step: f.step, count: f.count}
							p.wire = wire
							p.records = nil
							p.mu.Unlock()
							ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
							defer cancel()
							c := hCPACapture{Name: provider + "/" + route + "/" + f.name + "/" + kind, Fixture: f.name, ClientModel: model, UpstreamModel: upstream, Metadata: f.metadata, Kind: kind, Count: f.count}
							if !want[c.Name] {
								t.Fatalf("unknown/duplicate mixed capture=%s", c.Name)
							}
							delete(want, c.Name)
							switch kind {
							case "fields":
								if route == "core" {
									c.Fields = gCPACoreFields(t, p, ctx)
								} else {
									c.Fields = gCPAHostFields(t, p, ctx)
								}
								if !reflect.DeepEqual(c.Fields, fields) {
									t.Errorf("fields=%q want=%q", c.Fields, fields)
								}
							case "native":
								result, err := enabled.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, eNativeRequest(model, "openai-response", true))
								if err != nil {
									t.Fatal(err)
								}
								c.Body, c.Errors = eNativeDrain(t, result.Chunks)
								canonical := f
								canonical.eol = "\n"
								_, expected := hCPAMixedFields(canonical, model)
								if len(c.Errors) != 0 || !bytes.Equal(c.Body, expected) {
									t.Errorf("mixed native=%q errors=%q want=%q", c.Body, c.Errors, expected)
								}
								hCPARequireMixedEvents(t, c.Body, f, model)
							case "ws":
								var err error
								c.WS, err = eWS(t, server, model)
								if err != nil {
									c.Errors = []string{err.Error()}
									t.Error(err)
								}
								if !reflect.DeepEqual(c.WS, hCPAMixedPayloads(model, f.count)) {
									t.Errorf("mixed WS=%q want=%q", c.WS, hCPAMixedPayloads(model, f.count))
								}
							default:
								c.Stream = kind == "http-stream"
								c.Status, c.Headers, c.Body = eHTTP(t, server, "/v1/responses", gCPARequest(model, c.Stream))
								if c.Status != 200 {
									t.Errorf("mixed HTTP status=%d body=%q", c.Status, c.Body)
								}
								if !c.Stream {
									gCPARequireResponse(t, c.Body, model)
								} else if route == "mapped" {
									hCPARequireMixedEvents(t, c.Body, f, model)
								} else {
									if !bytes.Equal(c.Body, hCPADirectHTTPWire(f, model)) {
										t.Errorf("existing direct HTTP boundary changed: bytes=%q want=%q", c.Body, hCPADirectHTTPWire(f, model))
									}
									t.Logf("existing direct/disabled HTTP limitation: completed absent; payloads=%d/%d body=%q", f.count-1, f.count, c.Body)
								}
							}
							p.mu.Lock()
							records := append([]gCPAUpstreamRecord(nil), p.records...)
							p.mu.Unlock()
							c.Calls = len(records)
							if len(records) != 1 || records[0].model != upstream {
								t.Errorf("mixed upstream calls=%+v", records)
							} else {
								c.Request, c.UpstreamResponse, c.RequestHeaders, c.Path = records[0].body, records[0].response, records[0].headers, records[0].path
								if !bytes.Equal(c.UpstreamResponse, wire) || c.Path != "/v1/responses" {
									t.Error("mixed upstream identity changed")
								}
							}
							captures = append(captures, c)
							t.Logf("mixed %s body=%q WS=%q fields=%q errors=%q upstream=%s", c.Name, c.Body, c.WS, c.Fields, c.Errors, c.Request)
						})
					}
				}
			}
		})
	}
	if len(want) != 0 {
		t.Errorf("missing mixed captures=%v", want)
	}
}

func TestModelMapperFunctionalMixedBinaryCaptures(t *testing.T) {
	if url := os.Getenv("CPA_FUNCTIONAL_MIXED_CLIENT_URL"); url != "" {
		values, err := hCPAMixedBinaryWS(t, url, os.Getenv("CPA_FUNCTIONAL_MIXED_CLIENT_MODEL"), os.Getenv("CPA_FUNCTIONAL_MIXED_CLIENT_KEY"))
		result := struct {
			Values []string
			Error  string
		}{Values: values}
		if err != nil {
			result.Error = err.Error()
		}
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("CPA_FUNCTIONAL_MIXED_CLIENT_RESULT"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := os.Getenv("CPA_FUNCTIONAL_MIXED_CAPTURES")
	if path == "" {
		t.Fatal("mixed binary captures required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var captures []hCPACapture
	if err := json.Unmarshal(raw, &captures); err != nil {
		t.Fatal(err)
	}
	type expected struct {
		f                                hCPAMixedFixture
		model, upstream, provider, route string
	}
	want := make(map[string]expected)
	mapped := make(map[string]hCPACapture)
	for _, provider := range []string{"xai", "codex"} {
		for _, f := range hCPAMixedFixtures() {
			for _, route := range []struct{ name, model, upstream string }{{"mapped", "grok-4.6", "grok-4.7"}, {"direct", "grok-4.7", "grok-4.7"}, {"disabled-grok-4.6", "grok-4.6", "grok-4.6"}, {"disabled-grok-4.7", "grok-4.7", "grok-4.7"}} {
				for _, kind := range []string{"http-nonstream", "http-stream", "ws"} {
					want[provider+"/"+route.name+"/"+f.name+"/"+kind] = expected{f, route.model, route.upstream, provider, route.name}
				}
			}
		}
	}
	sessionDifferences := 0
	for _, c := range captures {
		key := c.Name + "/" + c.Kind
		ex, ok := want[key]
		if !ok {
			t.Fatalf("unknown/duplicate mixed binary capture=%s", key)
		}
		delete(want, key)
		t.Run(key, func(t *testing.T) {
			if c.Fixture != ex.f.name || c.ClientModel != ex.model || c.UpstreamModel != ex.upstream || c.Metadata != ex.f.metadata || c.Count != ex.f.count || c.Calls != 1 || len(c.Errors) != 0 {
				t.Errorf("mixed binary identity/error=%+v", c)
			}
			_, wire := hCPAMixedFields(ex.f, ex.upstream)
			if !bytes.Equal(c.UpstreamResponse, wire) || c.Path != "/v1/responses" || c.RequestHeaders.Get("Content-Length") != fmt.Sprint(len(c.Request)) || c.RequestHeaders.Get("Authorization") != "Bearer fake-upstream-key" {
				t.Error("mixed binary upstream bytes/URI/headers differ")
			}
			switch c.Kind {
			case "ws":
				if !reflect.DeepEqual(c.WS, hCPAMixedPayloads(ex.model, ex.f.count)) {
					t.Errorf("mixed binary WS=%q want=%q", c.WS, hCPAMixedPayloads(ex.model, ex.f.count))
				}
			case "http-nonstream":
				if c.Status != 200 {
					t.Errorf("status=%d", c.Status)
				}
				gCPARequireResponse(t, c.Body, ex.model)
			case "http-stream":
				if c.Status != 200 {
					t.Errorf("status=%d bytes=%q", c.Status, c.Body)
				}
				if ex.route == "mapped" {
					hCPARequireMixedEvents(t, c.Body, ex.f, ex.model)
				} else {
					if !bytes.Equal(c.Body, hCPADirectHTTPWire(ex.f, ex.model)) {
						t.Errorf("direct HTTP boundary changed: body=%q want=%q", c.Body, hCPADirectHTTPWire(ex.f, ex.model))
					}
					t.Logf("existing mixed HTTP limitation: HTTP200, completed absent, payloads=%d/%d", ex.f.count-1, ex.f.count)
				}
			}
			pairKey := ex.provider + "/" + ex.f.name + "/" + c.Kind
			if ex.route == "mapped" {
				mapped[pairKey] = c
			} else if ex.route == "direct" {
				control, ok := mapped[pairKey]
				if !ok {
					t.Fatal("mapped pair absent")
				}
				strictBody := bytes.Equal(c.Request, control.Request)
				strictHeaders := reflect.DeepEqual(c.RequestHeaders, control.RequestHeaders)
				if !bytes.Equal(c.UpstreamResponse, control.UpstreamResponse) || c.Path != control.Path {
					t.Error("mapped/direct response or URI differs")
				}
				if ex.provider == "xai" && c.Kind == "ws" {
					var a, b map[string]json.RawMessage
					if err := json.Unmarshal(c.Request, &a); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(control.Request, &b); err != nil {
						t.Fatal(err)
					}
					var aID, bID string
					if err := json.Unmarshal(a["prompt_cache_key"], &aID); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(b["prompt_cache_key"], &bID); err != nil {
						t.Fatal(err)
					}
					if strictBody || strictHeaders || aID == bID || aID == "" || bID == "" || c.RequestHeaders.Get("X-Grok-Conv-Id") != aID || control.RequestHeaders.Get("X-Grok-Conv-Id") != bID {
						t.Error("expected real XAI independent WS session difference absent or inconsistent")
					}
					delete(a, "prompt_cache_key")
					delete(b, "prompt_cache_key")
					ah, bh := c.RequestHeaders.Clone(), control.RequestHeaders.Clone()
					ah.Del("X-Grok-Conv-Id")
					bh.Del("X-Grok-Conv-Id")
					if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(ah, bh) {
						t.Error("XAI WS has differences beyond confirmed session fields")
					}
					sessionDifferences++
					t.Logf("strict mapped/direct request equality=false, header equality=false, session IDs=%s/%s; response/URI equality=true", bID, aID)
				} else if !strictBody || !strictHeaders {
					t.Errorf("strict mapped/direct upstream equality failed: mapped=%s/%v direct=%s/%v", control.Request, control.RequestHeaders, c.Request, c.RequestHeaders)
				}
			}
		})
	}
	if len(want) != 0 {
		t.Errorf("missing mixed binary captures=%v", want)
	}
	if sessionDifferences != 60 {
		t.Errorf("XAI WS session differences=%d want=60", sessionDifferences)
	}
}

func hCPAMixedBinaryWS(t *testing.T, url, model, key string) ([]string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, http.Header{"Authorization": {"Bearer " + key}})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.create","model":%q,"input":[],"prompt_cache_key":"task-g-local"}`, model))); err != nil {
		return nil, err
	}
	var values []string
	for {
		kind, raw, err := conn.ReadMessage()
		if err != nil {
			return values, err
		}
		if kind != websocket.TextMessage || !json.Valid(raw) {
			return values, fmt.Errorf("WS kind=%d payload=%q", kind, raw)
		}
		values = append(values, string(raw))
		var event struct{ Type string }
		if err := json.Unmarshal(raw, &event); err != nil {
			return values, err
		}
		if event.Type == "response.completed" || event.Type == "error" || event.Type == "response.failed" {
			return values, nil
		}
	}
}

func TestModelMapperFunctionalMixedShapeControls(t *testing.T) {
	var captures []hCPACapture
	defer func() { hCPASaveEvidence(t, "mixed-shape-controls.json", captures) }()
	expected := make(map[string]bool)
	for _, provider := range []string{"xai", "codex"} {
		for _, shape := range []string{"data-to-pair", "pair-to-data", "alternating-data", "alternating-pair"} {
			for _, count := range []int{2, 9} {
				for _, metadata := range [][]string{nil, {"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}} {
					for _, position := range []string{"before-event", "in-event", "between-events"} {
						for _, kind := range []string{"core", "host", "direct-ws", "disabled-.6-ws", "disabled-.7-ws", "mapped-native", "mapped-ws"} {
							expected[fmt.Sprintf("%s/%s/count=%d/metadata=%q/%s/%s", provider, shape, count, metadata, position, kind)] = true
						}
					}
				}
			}
		}
	}
	for _, provider := range []string{"xai", "codex"} {
		t.Run(provider, func(t *testing.T) {
			p := gCPANewLocalProducer(t, provider)
			enabled := gCPALoadNative(t, p, true)
			disabled := gCPALoadNative(t, p, false)
			server := eResponsesServer(t, p.base)
			for _, shape := range []string{"data-to-pair", "pair-to-data", "alternating-data", "alternating-pair"} {
				for _, count := range []int{2, 9} {
					for _, metadata := range [][]string{nil, {"id: event-1"}, {"retry: 100"}, {": heartbeat"}, {"id: event-1", "retry: 100", `: opaque event: response.created data: {"model":"grok-4.7"} 中文`}} {
						for _, position := range []string{"before-event", "in-event", "between-events"} {
							t.Run(fmt.Sprintf("%s/count=%d/metadata=%q/%s", shape, count, metadata, position), func(t *testing.T) {
								build := func(model string) ([]string, []byte) {
									var fields []string
									var wire strings.Builder
									for i, payload := range hCPAMixedPayloads(model, count) {
										add := func(field string) { fields = append(fields, field); wire.WriteString(field + "\n") }
										if position == "before-event" || position == "between-events" && i > 0 {
											for _, field := range metadata {
												add(field)
											}
										}
										dataOnly := shape == "data-to-pair" && i == 0 || shape == "pair-to-data" && i > 0 || shape == "alternating-data" && i%2 == 0 || shape == "alternating-pair" && i%2 == 1
										var value struct{ Type string }
										if err := json.Unmarshal([]byte(payload), &value); err != nil {
											panic(err)
										}
										if !dataOnly {
											add("event: " + value.Type)
										}
										if position == "in-event" {
											for _, field := range metadata {
												add(field)
											}
										}
										add("data: " + payload)
										wire.WriteByte('\n')
									}
									return fields, []byte(wire.String())
								}
								set := func(model string) {
									_, wire := build(model)
									p.mu.Lock()
									p.fixture = gCPAFieldsFixture{contentType: "text/event-stream", eol: "\n", count: count}
									p.wire = wire
									p.records = nil
									p.mu.Unlock()
								}
								ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
								defer cancel()
								for _, kind := range []string{"core", "host", "direct-ws", "disabled-.6-ws", "disabled-.7-ws", "mapped-native", "mapped-ws"} {
									t.Run(kind, func(t *testing.T) {
										p.base.SetPluginHost(nil)
										p.base.SetModelRouterHost(nil)
										model, upstream := "grok-4.7", "grok-4.7"
										if strings.HasPrefix(kind, "mapped") {
											model = "grok-4.6"
											if kind == "mapped-ws" {
												p.base.SetPluginHost(enabled)
												p.base.SetModelRouterHost(enabled)
											}
										} else if strings.HasPrefix(kind, "disabled") {
											if kind == "disabled-.6-ws" {
												model = "grok-4.6"
											}
											upstream = model
											p.base.SetPluginHost(disabled)
											p.base.SetModelRouterHost(disabled)
										}
										set(upstream)
										fields, _ := build(upstream)
										_, want := build(model)
										c := hCPACapture{Name: fmt.Sprintf("%s/%s/count=%d/metadata=%q/%s/%s", provider, shape, count, metadata, position, kind), Kind: kind, ClientModel: model, UpstreamModel: upstream, Count: count}
										if !expected[c.Name] {
											t.Fatalf("unknown/duplicate shape control=%s", c.Name)
										}
										delete(expected, c.Name)
										switch kind {
										case "core":
											c.Fields = gCPACoreFields(t, p, ctx)
											if !reflect.DeepEqual(c.Fields, fields) {
												t.Errorf("shape core=%q want=%q", c.Fields, fields)
											}
										case "host":
											c.Fields = gCPAHostFields(t, p, ctx)
											if !reflect.DeepEqual(c.Fields, fields) {
												t.Errorf("shape host=%q want=%q", c.Fields, fields)
											}
										case "mapped-native":
											result, err := enabled.activeRecords()[0].plugin.Capabilities.Executor.ExecuteStream(ctx, eNativeRequest(model, "openai-response", true))
											if err != nil {
												t.Fatal(err)
											}
											c.Body, c.Errors = eNativeDrain(t, result.Chunks)
											if len(c.Errors) != 0 || !bytes.Equal(c.Body, want) {
												t.Errorf("shape native=%q errors=%q want=%q", c.Body, c.Errors, want)
											}
										default:
											var err error
											c.WS, err = eWS(t, server, model)
											if err != nil {
												c.Errors = []string{err.Error()}
												t.Error(err)
											}
											if !reflect.DeepEqual(c.WS, hCPAMixedPayloads(model, count)) {
												t.Errorf("shape WS=%q want=%q", c.WS, hCPAMixedPayloads(model, count))
											}
										}
										p.mu.Lock()
										records := append([]gCPAUpstreamRecord(nil), p.records...)
										p.mu.Unlock()
										c.Calls = len(records)
										if len(records) != 1 || records[0].model != upstream {
											t.Errorf("shape calls=%+v", records)
										} else {
											c.Request, c.UpstreamResponse, c.RequestHeaders, c.Path = records[0].body, records[0].response, records[0].headers, records[0].path
										}
										captures = append(captures, c)
										t.Logf("shape %s fields=%q bytes=%q WS=%q errors=%q", c.Name, c.Fields, c.Body, c.WS, c.Errors)
									})
								}
							})
						}
					}
				}
			}
		})
	}
	if len(expected) != 0 {
		t.Errorf("missing shape controls=%v", expected)
	}
}
