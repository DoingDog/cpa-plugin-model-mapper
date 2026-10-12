package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"

	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

type callerPatternCacheKey struct {
	scope   string
	pattern string
}

const callerPatternCacheGenerationSize = 16 << 10

type callerPatternCacheState struct {
	current  map[callerPatternCacheKey]bool
	previous map[callerPatternCacheKey]bool
}

var (
	// ponytail: two fixed generations cap cross-RPC caller state; remove this cache when CPA carries route decisions into executor calls.
	callerPatternCacheMu sync.RWMutex
	callerPatternCache   = callerPatternCacheState{current: make(map[callerPatternCacheKey]bool, callerPatternCacheGenerationSize)}
)

func resetCallerPatternCache() {
	callerPatternCacheMu.Lock()
	callerPatternCache = callerPatternCacheState{current: make(map[callerPatternCacheKey]bool, callerPatternCacheGenerationSize)}
	callerPatternCacheMu.Unlock()
}

type routeDecision struct {
	Handled       bool
	OriginalModel string
	UpstreamModel string
}

type ruleSelection struct {
	first  []rule
	second []rule
}

func selectRules(cfg Config, format string) ruleSelection {
	var specific []rule
	switch format {
	case "claude":
		specific = cfg.claudeMessagesRules
	case "openai-response":
		specific = cfg.codexResponsesRules
	case "openai":
		specific = cfg.openAICompletionsRules
	case "gemini", "interactions":
		return ruleSelection{first: cfg.globalRules}
	default:
		return ruleSelection{}
	}
	if len(specific) == 0 {
		return ruleSelection{first: cfg.globalRules}
	}

	selection := ruleSelection{first: specific}
	switch cfg.RulesStackMode {
	case rulesStackModeSpecificFirst:
		selection.second = cfg.globalRules
	case rulesStackModeGlobalFirst:
		selection.first = cfg.globalRules
		selection.second = specific
	}
	if len(selection.first) == 0 {
		selection.first = selection.second
		selection.second = nil
	}
	return selection
}

func callerAPIKeyForSelectedRules(cfg Config, format string, headers http.Header, query url.Values, scope string) string {
	selection := selectRules(cfg, format)
	for i := range selection.first {
		if len(selection.first[i].callerPattern) > 0 {
			return callerAPIKey(headers, query, scope)
		}
	}
	for i := range selection.second {
		if len(selection.second[i].callerPattern) > 0 {
			return callerAPIKey(headers, query, scope)
		}
	}
	return ""
}

type modelRouteRPCRequest struct {
	SourceFormat   string
	RequestedModel string
	Headers        http.Header
	Query          url.Values
	Body           []byte
	Metadata       map[string]any
}

func interactionsUsesAgent(body []byte) bool {
	return gjson.ValidBytes(body) && strings.TrimSpace(gjson.GetBytes(body, "agent").String()) != ""
}

func handleModelRoute(raw []byte) ([]byte, error) {
	var req modelRouteRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if req.Metadata["request_path"] == "/v1/messages/count_tokens" {
		return json.Marshal(pluginapi.ModelRouteResponse{Handled: false})
	}
	if req.SourceFormat == "interactions" && interactionsUsesAgent(req.Body) {
		return json.Marshal(pluginapi.ModelRouteResponse{Handled: false})
	}
	canonicalizeHeaders(req.Headers)
	scope := callerScopeFromMetadata(req.Metadata)
	cfg := loadedConfig()
	decision, err := routeModel(cfg, req.SourceFormat, req.RequestedModel, scope, callerAPIKeyForSelectedRules(cfg, req.SourceFormat, req.Headers, req.Query, scope))
	if err != nil {
		return json.Marshal(pluginapi.ModelRouteResponse{
			Handled:    true,
			TargetKind: pluginapi.ModelRouteTargetSelf,
			Reason:     "runtime model mapping error",
		})
	}
	if !decision.Handled {
		return json.Marshal(pluginapi.ModelRouteResponse{Handled: false})
	}
	return json.Marshal(pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetSelf, Reason: "model mapped by model-mapper"})
}

func routeModel(cfg Config, format, model, scope, key string) (routeDecision, error) {
	if !cfg.compiled {
		var err error
		cfg, err = compileConfig(cfg)
		if err != nil {
			return routeDecision{}, err
		}
	}
	selection := selectRules(cfg, format)
	if len(selection.first) == 0 {
		return routeDecision{}, nil
	}
	mapped, matched, err := applyRules(model, scope, key, selection.first)
	if err != nil {
		return routeDecision{}, err
	}
	if len(selection.second) > 0 {
		var secondMatched bool
		mapped, secondMatched, err = applyRules(mapped, scope, key, selection.second)
		if err != nil {
			return routeDecision{}, err
		}
		matched = matched || secondMatched
	}
	if !matched || mapped == model {
		return routeDecision{}, nil
	}
	return routeDecision{Handled: true, OriginalModel: model, UpstreamModel: mapped}, nil
}

const (
	callerScopeMetadataKey = "caller_scope"
	callerScopeDomain      = "cli-proxy-api:caller-scope:v1\x00"
)

func callerScope(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(callerScopeDomain + apiKey))
	return hex.EncodeToString(sum[:])
}

func callerScopeFromMetadata(metadata map[string]any) string {
	scope, _ := metadata[callerScopeMetadataKey].(string)
	return scope
}

func callerAPIKey(headers http.Header, query url.Values, scope string) string {
	if scope == "" {
		return ""
	}
	matchesScope := func(candidate string) string {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && callerScope(candidate) == scope {
			return candidate
		}
		return ""
	}
	for _, authorization := range headers.Values("Authorization") {
		parts := strings.SplitN(authorization, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			authorization = parts[1]
		}
		if candidate := matchesScope(authorization); candidate != "" {
			return candidate
		}
	}
	for _, values := range [][]string{
		headers.Values("X-Goog-Api-Key"),
		headers.Values("X-Api-Key"),
		query["key"],
		query["auth_token"],
	} {
		for _, candidate := range values {
			if candidate := matchesScope(candidate); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func callerPatternMatch(r *rule, scope, key string) (bool, bool) {
	if key == "" || callerScope(key) != scope {
		return false, false
	}
	cacheKey := callerPatternCacheKey{scope: scope, pattern: r.callerPatternText}
	callerPatternCacheMu.RLock()
	matched, ok := callerPatternCache.current[cacheKey]
	if !ok {
		matched, ok = callerPatternCache.previous[cacheKey]
	}
	callerPatternCacheMu.RUnlock()
	if ok {
		return matched, true
	}
	_, matched = matchTokens(key, r.callerPattern)
	callerPatternCacheMu.Lock()
	if cached, exists := callerPatternCache.current[cacheKey]; exists {
		matched = cached
	} else if cached, exists := callerPatternCache.previous[cacheKey]; exists {
		matched = cached
	} else {
		if len(callerPatternCache.current) >= callerPatternCacheGenerationSize {
			callerPatternCache.previous = callerPatternCache.current
			callerPatternCache.current = make(map[callerPatternCacheKey]bool, callerPatternCacheGenerationSize)
		}
		callerPatternCache.current[cacheKey] = matched
	}
	callerPatternCacheMu.Unlock()
	return matched, true
}
