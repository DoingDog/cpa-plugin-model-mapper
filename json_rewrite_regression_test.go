package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestFunctionalRequestDuplicateContent(t *testing.T) {
	backslash := string(rune(92))
	cases := []struct {
		name, input, want string
		changed           bool
	}{
		{"content duplicates", `{"model":"a","messages":[{"content":"first"}],"model":"b","messages":[{"content":"second"}]}`, `{"model":"upstream","messages":[{"content":"first"}],"messages":[{"content":"second"}]}`, true},
		{"escaped first key", `{"` + backslash + `u006dodel":"a","x":1e+09,"model":"b","x":9007199254740993}`, `{"` + backslash + `u006dodel":"upstream","x":1e+09,"x":9007199254740993}`, true},
		{"last null", `{"model":"a","messages":[],"model":null}`, `{"model":"a","messages":[],"model":null}`, false},
		{"last number", `{"model":"a","messages":[],"model":1}`, `{"model":"a","messages":[],"model":1}`, false},
		{"last already target", `{"model":"a","messages":[],"model":"upstream"}`, `{"model":"upstream","messages":[]}`, true},
		{"adjacent duplicates in middle", `{"x":1,"model":"a","model":"b","model":"c","y":2}`, `{"x":1,"model":"upstream","y":2}`, true},
		{"first non-string last string", `{"model":null,"opaque":{"model":"keep","x":1,"x":2},"model":"b"}`, `{"model":"upstream","opaque":{"model":"keep","x":1,"x":2}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := []byte(tc.input)
			out, changed, err := rewriteTopLevelModel(input, "upstream")
			if err != nil || changed != tc.changed || string(out) != tc.want {
				t.Fatalf("rewrite=(%s,%v,%v), want %s", out, changed, err, tc.want)
			}
			if !bytes.Equal(input, []byte(tc.input)) {
				t.Fatal("input changed")
			}
			if len(out) > 0 && &out[0] == &input[0] {
				t.Fatal("output aliases input")
			}
			if tc.changed {
				models := topLevelSemanticModelValues(t, out)
				if len(models) != 1 || models[0] != "upstream" {
					t.Fatalf("models=%q", models)
				}
			}
		})
	}
}

func TestFunctionalResponseAllOccurrences(t *testing.T) {
	backslash := string(rune(92))
	cases := []struct {
		name, input, want string
		changed           bool
	}{
		{"model and content duplicates", `{"model":"upstream","choices":[{"text":"first"}],"model":"client","choices":[{"text":"second"}]}`, `{"model":"client","choices":[{"text":"first"}],"model":"client","choices":[{"text":"second"}]}`, true},
		{"duplicate parents", `{"response":{"model":"upstream","x":1},"response":{"modelVersion":"upstream","x":2},"message":{"model":"upstream","modelVersion":"opaque"},"interaction":{"model":"upstream"}}`, `{"response":{"model":"client","x":1},"response":{"modelVersion":"client","x":2},"message":{"model":"client","modelVersion":"opaque"},"interaction":{"model":"client"}}`, true},
		{"escaped keys", `{"res` + backslash + `u0070onse":{"` + backslash + `u006dodel":"upstream"},"opaque":{"model":"upstream"}}`, `{"res` + backslash + `u0070onse":{"` + backslash + `u006dodel":"client"},"opaque":{"model":"upstream"}}`, true},
		{"immediate array objects", `[{"modelVersion":"upstream","opaque":{"model":"keep"}},[{"model":"keep"}],null]`, `[{"modelVersion":"client","opaque":{"model":"keep"}},[{"model":"keep"}],null]`, true},
		{"escaped equal value", `{"model":"cl` + backslash + `u0069ent","model":"client","response":{"model":null}}`, `{"model":"cl` + backslash + `u0069ent","model":"client","response":{"model":null}}`, false},
		{"all duplicate parents end in client", `{"response":{"model":"upstream"},"response":{"model":"client"},"message":{"model":"upstream"},"message":{"model":"client"},"interaction":{"model":"upstream"},"interaction":{"model":"client"}}`, `{"response":{"model":"client"},"response":{"model":"client"},"message":{"model":"client"},"message":{"model":"client"},"interaction":{"model":"client"},"interaction":{"model":"client"}}`, true},
		{"all modelVersion occurrences", `{"modelVersion":"upstream","modelVersion":"client","response":{"modelVersion":"upstream","modelVersion":"client","model":"upstream","model":"client"}}`, `{"modelVersion":"client","modelVersion":"client","response":{"modelVersion":"client","modelVersion":"client","model":"client","model":"client"}}`, true},
		{"non-string fields and opaque containers", `{"model":null,"modelVersion":1,"response":{"model":false,"modelVersion":null},"message":{"model":[],"modelVersion":"opaque"},"interaction":{"model":{}},"opaque":{"model":"upstream"}}`, `{"model":null,"modelVersion":1,"response":{"model":false,"modelVersion":null},"message":{"model":[],"modelVersion":"opaque"},"interaction":{"model":{}},"opaque":{"model":"upstream"}}`, false},
		{"whitespace numbers and opaque duplicates", " { \"model\" : \"upstream\", \"x\" : 1e+09, \"x\" : 9007199254740993, \"opaque\" : {\"x\":1,\"x\":2}, \"response\" : { \"model\" : \"upstream\", \"tool\" : {\"model\":\"keep\"} } } \n", " { \"model\" : \"client\", \"x\" : 1e+09, \"x\" : 9007199254740993, \"opaque\" : {\"x\":1,\"x\":2}, \"response\" : { \"model\" : \"client\", \"tool\" : {\"model\":\"keep\"} } } \n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := []byte(tc.input)
			out, changed, err := restoreResponseModel(input, "client")
			if err != nil || changed != tc.changed || !bytes.Equal(out, []byte(tc.want)) {
				t.Fatalf("restore=(%s,%v,%v), want %s", out, changed, err, tc.want)
			}
			if !bytes.Equal(input, []byte(tc.input)) {
				t.Fatal("input changed")
			}
			if len(out) > 0 && &out[0] == &input[0] {
				t.Fatal("output aliases input")
			}
		})
	}
}

func TestFunctionalJSONValueSpans(t *testing.T) {
	body := []byte(` { "model" : "a", "opaque" : {"x":1,"x":2}, "` + string(rune(92)) + `u006dodel" : "b" } `)
	d := json.NewDecoder(bytes.NewReader(body))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		t.Fatalf("start=(%v,%v)", first, err)
	}
	var values []string
	for d.More() {
		key, err := d.Token()
		if err != nil {
			t.Fatal(err)
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		end := int(d.InputOffset())
		start := end - len(raw)
		if !bytes.Equal(body[start:end], raw) {
			t.Fatal("span differs from original bytes")
		}
		if key == "model" {
			values = append(values, string(raw))
		}
	}
	last, err := d.Token()
	if err != nil || last != json.Delim('}') {
		t.Fatalf("end=(%v,%v)", last, err)
	}
	if len(values) != 2 || values[0] != `"a"` || values[1] != `"b"` {
		t.Fatalf("values=%q", values)
	}
}

func TestFunctionalJSONCachedReplacementOwnership(t *testing.T) {
	r := newSSERewriter("client")
	for _, tc := range []struct{ input, want string }{
		{`{"model":"upstream","response":{"modelVersion":"upstream"}}`, `{"model":"client","response":{"modelVersion":"client"}}`},
		{`[{"message":{"model":"upstream"}},null]`, `[{"message":{"model":"client"}},null]`},
	} {
		input := []byte(tc.input)
		out, changed, valid, err := r.restoreResponseModelCandidate(input)
		if err != nil || !changed || !valid || string(out) != tc.want {
			t.Fatalf("restore=(%s,%v,%v,%v), want %s", out, changed, valid, err, tc.want)
		}
		for i := range out {
			out[i] = 'x'
		}
		if string(input) != tc.input || string(r.encodedModel) != `"client"` {
			t.Fatalf("output mutation changed input=%s or replacement=%s", input, r.encodedModel)
		}
	}
}

func TestFunctionalJSONExecutorHeaders(t *testing.T) {
	setLoadedConfigForTest(Config{GlobalRules: "client=>upstream"})
	backslash := string(rune(92))
	for _, tc := range []struct {
		name, request, wantRequest, response, wantResponse string
		changed                                            bool
	}{
		{"duplicate models", `{"model":"client","messages":[],"model":"upstream"}`, `{"model":"upstream","messages":[]}`, `{"model":"upstream","model":"client"}`, `{"model":"client","model":"client"}`, true},
		{"null request and equal response", `{"model":"client","messages":[],"model":null}`, `{"model":"client","messages":[],"model":null}`, `{"model":"cl` + backslash + `u0069ent","response":{"model":null}}`, `{"model":"cl` + backslash + `u0069ent","response":{"model":null}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
				Model: "client", Format: "openai", SourceFormat: "openai",
				OriginalRequest: []byte(tc.request), Headers: headersWithStaleBodyFields(),
			}})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			responseRaw, err := handleExecutorExecute(raw, func(method string, payload any) (json.RawMessage, error) {
				calls++
				if method != pluginabi.MethodHostModelExecute {
					t.Fatalf("method=%s", method)
				}
				req := payload.(hostModelExecutePayload)
				if req.Model != "upstream" || string(req.Body) != tc.wantRequest {
					t.Fatalf("request=(%s,%s), want (upstream,%s)", req.Model, req.Body, tc.wantRequest)
				}
				for _, name := range append([]string{"Content-Length"}, staleBodyHeaders...) {
					if tc.changed {
						requireNoHeader(t, req.Headers, name)
					} else if req.Headers.Get(name) != headersWithStaleBodyFields().Get(name) {
						t.Fatalf("unchanged request header %s was removed", name)
					}
				}
				if req.Headers.Get("ETag") != "etag" || req.Headers.Get("X-Keep") != "keep" {
					t.Fatalf("request validators or X-Keep changed: %v", req.Headers)
				}
				return json.Marshal(pluginapi.HostModelExecutionResponse{
					StatusCode: http.StatusOK, Headers: headersWithStaleBodyFields(), Body: []byte(tc.response),
				})
			})
			if err != nil || calls != 1 {
				t.Fatalf("execute=(%d,%v), want one host call", calls, err)
			}
			var response pluginapi.ExecutorResponse
			if err := json.Unmarshal(responseRaw, &response); err != nil {
				t.Fatal(err)
			}
			if string(response.Payload) != tc.wantResponse {
				t.Fatalf("payload=%s, want %s", response.Payload, tc.wantResponse)
			}
			for _, name := range append(append([]string{"Content-Length"}, staleBodyHeaders...), "ETag", "Content-Range") {
				if tc.changed {
					requireNoHeader(t, response.Headers, name)
				} else if response.Headers.Get(name) != headersWithStaleBodyFields().Get(name) {
					t.Fatalf("unchanged response header %s was removed", name)
				}
			}
			if response.Headers.Get("Accept-Ranges") != "bytes" || response.Headers.Get("X-Keep") != "keep" {
				t.Fatalf("response Accept-Ranges or X-Keep changed: %v", response.Headers)
			}
		})
	}
}
