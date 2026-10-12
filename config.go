package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	rulesStackModeOff           = "off"
	rulesStackModeSpecificFirst = "specific_first"
	rulesStackModeGlobalFirst   = "global_first"
)

type Config struct {
	GlobalRules            string `json:"global_rules"`
	ClaudeMessagesRules    string `json:"claude_messages_rules"`
	CodexResponsesRules    string `json:"codex_responses_rules"`
	OpenAICompletionsRules string `json:"openai_completions_rules"`
	RulesStackMode         string `json:"rules_stack_mode"`

	compiled               bool
	globalRules            []rule
	claudeMessagesRules    []rule
	codexResponsesRules    []rule
	openAICompletionsRules []rule
}

func decodeConfig(raw json.RawMessage) (Config, error) {
	cfg := defaultConfig()
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("{}")) {
		return compileConfig(cfg)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Config{}, err
	}
	for key, value := range fields {
		for _, name := range []string{
			"global_rules",
			"claude_messages_rules",
			"codex_responses_rules",
			"openai_completions_rules",
			"rules_stack_mode",
		} {
			if strings.EqualFold(key, name) {
				value = bytes.TrimSpace(value)
				if len(value) == 0 || value[0] != '"' {
					return Config{}, fmt.Errorf("%s must be a string", name)
				}
				break
			}
		}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	return compileConfig(cfg)
}

func compileConfig(cfg Config) (Config, error) {
	if cfg.RulesStackMode == "" {
		cfg.RulesStackMode = rulesStackModeOff
	}
	switch cfg.RulesStackMode {
	case rulesStackModeOff, rulesStackModeSpecificFirst, rulesStackModeGlobalFirst:
	default:
		return Config{}, fmt.Errorf("invalid rules_stack_mode %q", cfg.RulesStackMode)
	}

	var err error
	if cfg.globalRules, err = compileRuleSet(cfg.GlobalRules); err != nil {
		return Config{}, err
	}
	if cfg.claudeMessagesRules, err = compileRuleSet(cfg.ClaudeMessagesRules); err != nil {
		return Config{}, err
	}
	if cfg.codexResponsesRules, err = compileRuleSet(cfg.CodexResponsesRules); err != nil {
		return Config{}, err
	}
	if cfg.openAICompletionsRules, err = compileRuleSet(cfg.OpenAICompletionsRules); err != nil {
		return Config{}, err
	}
	cfg.compiled = true
	return cfg, nil
}

func compileRuleSet(raw string) ([]rule, error) {
	if raw == "" {
		return nil, nil
	}
	return parseRules(raw)
}

var (
	loadedConfigMu sync.RWMutex
	loadedCfg      = defaultConfig()
)

func loadedConfig() Config {
	loadedConfigMu.RLock()
	defer loadedConfigMu.RUnlock()
	return loadedCfg
}

func publishLoadedConfig(cfg Config) {
	loadedConfigMu.Lock()
	loadedCfg = cfg
	loadedConfigMu.Unlock()
}

func setLoadedConfigForTest(cfg Config) {
	compiled, err := compileConfig(cfg)
	if err != nil {
		panic(err)
	}
	resetCallerPatternCache()
	publishLoadedConfig(compiled)
}

func applyLifecycleConfig(raw []byte) error {
	cfgRaw, _, err := decodeLifecycleConfig(raw)
	if err != nil {
		return err
	}
	cfg, err := decodeConfig(cfgRaw)
	if err != nil {
		return err
	}
	publishLoadedConfig(cfg)
	return nil
}

type lifecycleYAMLConfig struct {
	GlobalRules            yaml.Node `yaml:"global_rules"`
	ClaudeMessagesRules    yaml.Node `yaml:"claude_messages_rules"`
	CodexResponsesRules    yaml.Node `yaml:"codex_responses_rules"`
	OpenAICompletionsRules yaml.Node `yaml:"openai_completions_rules"`
	RulesStackMode         yaml.Node `yaml:"rules_stack_mode"`
}

func yamlStringConfigField(node yaml.Node, name string) (string, error) {
	if node.Kind == 0 {
		return "", nil
	}
	for node.Kind == yaml.AliasNode {
		if node.Alias == nil {
			return "", fmt.Errorf("%s must be a string", name)
		}
		node = *node.Alias
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("%s must be a string", name)
	}
	return node.Value, nil
}

func decodeLifecycleConfig(raw []byte) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	var lifecycle map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &lifecycle); err != nil {
		return append(json.RawMessage(nil), trimmed...), false, nil
	}
	encoded, ok := lifecycle["config_yaml"]
	if !ok {
		return append(json.RawMessage(nil), trimmed...), false, nil
	}
	var configYAML *string
	if err := json.Unmarshal(encoded, &configYAML); err != nil || configYAML == nil {
		if err != nil {
			return nil, true, fmt.Errorf("config_yaml must be a string: %w", err)
		}
		return nil, true, fmt.Errorf("config_yaml must be a string")
	}
	decoded, err := base64.StdEncoding.DecodeString(*configYAML)
	if err != nil {
		return nil, true, err
	}
	var yamlConfig lifecycleYAMLConfig
	decoder := yaml.NewDecoder(bytes.NewReader(decoded))
	if err := decoder.Decode(&yamlConfig); err != nil && err != io.EOF {
		return nil, true, err
	} else if err == nil {
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			if err == nil {
				return nil, true, fmt.Errorf("config_yaml must contain one YAML document")
			}
			return nil, true, fmt.Errorf("trailing YAML: %w", err)
		}
	}
	globalRules, err := yamlStringConfigField(yamlConfig.GlobalRules, "global_rules")
	if err != nil {
		return nil, true, err
	}
	claudeMessagesRules, err := yamlStringConfigField(yamlConfig.ClaudeMessagesRules, "claude_messages_rules")
	if err != nil {
		return nil, true, err
	}
	codexResponsesRules, err := yamlStringConfigField(yamlConfig.CodexResponsesRules, "codex_responses_rules")
	if err != nil {
		return nil, true, err
	}
	openAICompletionsRules, err := yamlStringConfigField(yamlConfig.OpenAICompletionsRules, "openai_completions_rules")
	if err != nil {
		return nil, true, err
	}
	rulesStackMode, err := yamlStringConfigField(yamlConfig.RulesStackMode, "rules_stack_mode")
	if err != nil {
		return nil, true, err
	}
	cfgRaw, err := json.Marshal(Config{
		GlobalRules:            globalRules,
		ClaudeMessagesRules:    claudeMessagesRules,
		CodexResponsesRules:    codexResponsesRules,
		OpenAICompletionsRules: openAICompletionsRules,
		RulesStackMode:         rulesStackMode,
	})
	if err != nil {
		return nil, true, err
	}
	return cfgRaw, true, nil
}

func defaultConfig() Config {
	return Config{RulesStackMode: rulesStackModeOff, compiled: true}
}
