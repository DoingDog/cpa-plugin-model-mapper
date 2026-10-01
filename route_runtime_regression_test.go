package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestFunctionalRouteRuntimeEmpty(t *testing.T) {
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
	cases := []struct {
		name         string
		cfg          Config
		wantHandled  bool
		runtimeEmpty bool
	}{
		{"global", Config{GlobalRules: "prefix*=>$1"}, true, true},
		{"specific first", Config{GlobalRules: "unused=>target", OpenAICompletionsRules: "prefix*=>$1", RulesStackMode: "specific_first"}, true, true},
		{"second slice", Config{GlobalRules: "prefix=>middle", OpenAICompletionsRules: "middle*=>$1", RulesStackMode: "global_first"}, true, true},
		{"normal mapping", Config{GlobalRules: "prefix=>upstream"}, true, false},
		{"identity", Config{GlobalRules: "prefix=>prefix"}, false, false},
		{"unmatched", Config{GlobalRules: "unused=>upstream"}, false, false},
		{"two slices return original", Config{GlobalRules: "prefix=>middle", OpenAICompletionsRules: "middle=>prefix", RulesStackMode: "global_first"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setLoadedConfigForTest(tc.cfg)
			raw, err := json.Marshal(pluginapi.ModelRouteRequest{SourceFormat: "openai", RequestedModel: "prefix"})
			if err != nil {
				t.Fatal(err)
			}
			out, err := handleModelRoute(raw)
			if err != nil {
				t.Fatalf("router returned error: %v", err)
			}
			var route pluginapi.ModelRouteResponse
			if err := json.Unmarshal(out, &route); err != nil {
				t.Fatal(err)
			}
			if route.Handled != tc.wantHandled {
				t.Fatalf("route=%s, want Handled=%v", out, tc.wantHandled)
			}
			if tc.wantHandled && (route.TargetKind != pluginapi.ModelRouteTargetSelf || route.TargetModel != "") {
				t.Fatalf("route=%s, want self without TargetModel", out)
			}
			if !tc.runtimeEmpty {
				return
			}
			calls := 0
			host := func(string, any) (json.RawMessage, error) {
				calls++
				return nil, fmt.Errorf("unexpected upstream call")
			}
			req := executorRPCRequest{Model: "prefix", SourceFormat: "openai", Format: "openai", OriginalRequest: []byte(`{"model":"prefix"}`), StreamID: "runtime-empty"}
			encoded, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := handleExecutorExecute(encoded, host); err == nil || !strings.Contains(err.Error(), "empty mapped model") {
				t.Fatalf("execute error=%v", err)
			}
			if _, _, err := prepareExecutorStream(&req, host); err == nil || !strings.Contains(err.Error(), "empty mapped model") {
				t.Fatalf("stream error=%v", err)
			}
			if calls != 0 {
				t.Fatalf("upstream calls=%d, want 0", calls)
			}
		})
	}
}
