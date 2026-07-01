package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultBaseURL = "https://a3.awsl.app/v1"
	defaultPort    = 18080
	localAPIKey    = "local-smoke-key"
	wrongAPIKey    = "wrong-local-smoke-key"
)

type smokeEnv struct {
	repoRoot string
	baseURL  string
	apiKey   string
	cpaBin   string
	port     int
	dir      string
	config   string
	plugin   string
	logsDir  string
	logFile  string
}

type caseConfig struct {
	name               string
	pluginRules        string
	requestModel       string
	requestAPIKey      string
	stream             bool
	wantSuccess        bool
	wantOriginalModel  string
	forbidModel        string
	allowStartFailure  bool
	allowConfigFailure bool
}

type openAIResponse struct {
	Model string          `json:"model"`
	Error json.RawMessage `json:"error"`
}

type cpaProcess struct {
	cmd      *exec.Cmd
	logFile  *os.File
	waitDone chan error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	apiKey := strings.TrimSpace(os.Getenv("CPA_SMOKE_API_KEY"))
	if apiKey == "" {
		return errors.New("CPA_SMOKE_API_KEY is required for live smoke")
	}
	cpaBin := strings.TrimSpace(os.Getenv("CPA_SMOKE_CPA_BIN"))
	if cpaBin == "" {
		return errors.New("CPA_SMOKE_CPA_BIN is required for live smoke")
	}
	baseURL := strings.TrimSpace(os.Getenv("CPA_SMOKE_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	port := defaultPort
	if raw := strings.TrimSpace(os.Getenv("CPA_SMOKE_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("invalid CPA_SMOKE_PORT %q", raw)
		}
		port = parsed
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	env := smokeEnv{
		repoRoot: repoRoot,
		baseURL:  strings.TrimRight(baseURL, "/"),
		apiKey:   apiKey,
		cpaBin:   cpaBin,
		port:     port,
		dir:      filepath.Join(repoRoot, ".test-cpa"),
	}
	env.config = filepath.Join(env.dir, "config.yaml")
	env.plugin = filepath.Join(env.dir, "plugins", "windows", "amd64", "model-mapper.dll")
	env.logsDir = filepath.Join(env.dir, "logs")
	env.logFile = filepath.Join(env.logsDir, "cpa.log")
	if err := prepareDirs(env); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(repoRoot, "dist", "windows_amd64", "model-mapper.dll"), env.plugin); err != nil {
		return err
	}

	cases := []caseConfig{
		{name: "no-rules", requestModel: "client-visible-no-rules", requestAPIKey: localAPIKey, wantSuccess: true, forbidModel: "client-visible-no-rules"},
		{name: "openai-dedicated-chain", requestModel: "deepseek-v4-pro", requestAPIKey: localAPIKey, pluginRules: "deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>gpt-5.4-mini", wantSuccess: true, wantOriginalModel: "deepseek-v4-pro", forbidModel: "gpt-5.4-mini"},
		{name: "unmatched-model", requestModel: "client-visible-unmatched", requestAPIKey: localAPIKey, pluginRules: "deepseek-v4-pro=>gpt-5.4-mini", wantSuccess: true, forbidModel: "client-visible-unmatched"},
		{name: "bad-rules", requestModel: "deepseek-v4-pro", requestAPIKey: localAPIKey, pluginRules: "bad rule", wantSuccess: false, allowStartFailure: true, allowConfigFailure: true},
		{name: "nonexistent-upstream-model", requestModel: "deepseek-v4-pro", requestAPIKey: localAPIKey, pluginRules: "deepseek-v4-pro=>definitely-not-a-real-upstream-model", wantSuccess: false},
		{name: "wrong-api-key", requestModel: "deepseek-v4-flash", requestAPIKey: wrongAPIKey, wantSuccess: false},
		{name: "streaming", requestModel: "deepseek-v4-pro", requestAPIKey: localAPIKey, pluginRules: "deepseek-v4-pro=>deepseek-v4-flash;deepseek-v4-flash=>gpt-5.4-mini", stream: true, wantSuccess: true, wantOriginalModel: "deepseek-v4-pro", forbidModel: "gpt-5.4-mini"},
	}
	for _, tc := range cases {
		if err := runCase(env, tc); err != nil {
			return fmt.Errorf("%s: %w", tc.name, err)
		}
		fmt.Printf("ok: %s\n", tc.name)
	}
	return nil
}

func prepareDirs(env smokeEnv) error {
	for _, dir := range []string{
		filepath.Join(env.dir, "plugins", "windows", "amd64"),
		env.logsDir,
		filepath.Join(env.dir, "tmp"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return nil
}

func runCase(env smokeEnv, tc caseConfig) error {
	if err := os.WriteFile(env.config, []byte(buildConfig(env, tc.pluginRules)), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	proc, err := startCPA(env)
	if err != nil {
		if tc.allowStartFailure {
			return nil
		}
		return err
	}
	defer stopCPA(proc)
	if err := waitReady(env.port, localAPIKey); err != nil {
		return err
	}
	if tc.stream {
		return runStreamCase(env.port, tc)
	}
	return runJSONCase(env.port, tc)
}

func buildConfig(env smokeEnv, pluginRules string) string {
	var b strings.Builder
	// ponytail: upstream/CLIProxyAPI config.example.yaml is read from the module cache, so keep this generator to the fields this smoke needs.
	fmt.Fprintf(&b, "host: 127.0.0.1\n")
	fmt.Fprintf(&b, "port: %d\n", env.port)
	b.WriteString("auth-dir: ./tmp/auth\n")
	b.WriteString("api-keys:\n")
	fmt.Fprintf(&b, "  - %q\n", localAPIKey)
	b.WriteString("debug: false\n")
	b.WriteString("usage-statistics-enabled: false\n")
	b.WriteString("openai-compatibility:\n")
	b.WriteString("  - name: upstream\n")
	fmt.Fprintf(&b, "    base-url: %q\n", env.baseURL)
	b.WriteString("    api-key-entries:\n")
	fmt.Fprintf(&b, "      - api-key: %q\n", env.apiKey)
	b.WriteString("    models:\n")
	b.WriteString("      - name: deepseek-v4-flash\n")
	b.WriteString("        alias: deepseek-v4-flash\n")
	b.WriteString("      - name: deepseek-v4-flash\n")
	b.WriteString("        alias: client-visible-no-rules\n")
	b.WriteString("      - name: deepseek-v4-flash\n")
	b.WriteString("        alias: client-visible-unmatched\n")
	b.WriteString("      - name: gpt-5.4-mini\n")
	b.WriteString("        alias: gpt-5.4-mini\n")
	b.WriteString("plugins:\n")
	b.WriteString("  enabled: true\n")
	b.WriteString("  dir: ./plugins\n")
	b.WriteString("  configs:\n")
	b.WriteString("    model-mapper:\n")
	b.WriteString("      enabled: true\n")
	b.WriteString("      priority: 1\n")
	fmt.Fprintf(&b, "      global_rules: %q\n", "")
	fmt.Fprintf(&b, "      claude_messages_rules: %q\n", "")
	fmt.Fprintf(&b, "      codex_responses_rules: %q\n", "")
	fmt.Fprintf(&b, "      openai_completions_rules: %q\n", pluginRules)
	return b.String()
}

func startCPA(env smokeEnv) (*cpaProcess, error) {
	logFile, err := os.Create(env.logFile)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}
	cmd := exec.Command(env.cpaBin, "--config", "config.yaml", "--no-browser")
	cmd.Dir = env.dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start CPA: %w", err)
	}
	proc := &cpaProcess{cmd: cmd, logFile: logFile, waitDone: make(chan error, 1)}
	go func() {
		proc.waitDone <- cmd.Wait()
		close(proc.waitDone)
	}()
	select {
	case err := <-proc.waitDone:
		return nil, earlyExitError(env.logFile, err)
	case <-time.After(300 * time.Millisecond):
		return proc, nil
	}
}

func stopCPA(proc *cpaProcess) {
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return
	}
	defer func() {
		if proc.logFile != nil {
			_ = proc.logFile.Close()
		}
	}()
	_ = proc.cmd.Process.Signal(os.Interrupt)
	select {
	case <-proc.waitDone:
		return
	case <-time.After(2 * time.Second):
	}
	_ = proc.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-proc.waitDone:
		return
	case <-time.After(2 * time.Second):
	}
	_ = proc.cmd.Process.Kill()
	<-proc.waitDone
}

func earlyExitError(logPath string, waitErr error) error {
	body, readErr := os.ReadFile(logPath)
	if readErr != nil {
		return fmt.Errorf("CPA exited early: %w", waitErr)
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return fmt.Errorf("CPA exited early: %w", waitErr)
	}
	return fmt.Errorf("CPA exited early: %s", trimmed)
}

func waitReady(port int, apiKey string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, _, err := getModels(port, apiKey)
		if err == nil && status/100 == 2 {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("CPA readiness timeout")
}

func getModels(port int, apiKey string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/models", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("build models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("send models request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read models response: %w", err)
	}
	return resp.StatusCode, body, nil
}

func runJSONCase(port int, tc caseConfig) error {
	status, body, err := sendChatRequest(port, tc.requestModel, tc.requestAPIKey, false)
	if err != nil {
		return err
	}
	var parsed openAIResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		if tc.allowConfigFailure && status/100 != 2 {
			return nil
		}
		return fmt.Errorf("decode response: %w", err)
	}
	if tc.wantSuccess {
		if status/100 != 2 || len(parsed.Error) != 0 {
			return fmt.Errorf("want success, got status=%d body=%s", status, body)
		}
		if tc.wantOriginalModel != "" && parsed.Model != tc.wantOriginalModel {
			return fmt.Errorf("want top-level model %q, got %q", tc.wantOriginalModel, parsed.Model)
		}
		if tc.forbidModel != "" && parsed.Model == tc.forbidModel {
			return fmt.Errorf("forbid top-level model %q, body=%s", tc.forbidModel, body)
		}
		return nil
	}
	if status/100 != 2 || len(parsed.Error) != 0 {
		if tc.forbidModel != "" && parsed.Model == tc.forbidModel {
			return fmt.Errorf("forbid top-level model %q in failure body=%s", tc.forbidModel, body)
		}
		return nil
	}
	if tc.forbidModel != "" && parsed.Model == tc.forbidModel {
		return fmt.Errorf("unexpected top-level model %q in success body=%s", tc.forbidModel, body)
	}
	return fmt.Errorf("expected failure, got status=%d body=%s", status, body)
}

func runStreamCase(port int, tc caseConfig) error {
	status, body, err := sendChatRequest(port, tc.requestModel, tc.requestAPIKey, true)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("want stream success, got status=%d body=%s", status, body)
	}
	sawDone := false
	sawOriginal := false
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			sawDone = true
			continue
		}
		var parsed openAIResponse
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			continue
		}
		if parsed.Model == tc.wantOriginalModel {
			sawOriginal = true
		}
		if tc.forbidModel != "" && parsed.Model == tc.forbidModel {
			return fmt.Errorf("forbid streamed model %q in payload=%s", tc.forbidModel, payload)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan stream: %w", err)
	}
	if !sawOriginal {
		return fmt.Errorf("missing original streamed model %q in body=%s", tc.wantOriginalModel, body)
	}
	if !sawDone {
		return fmt.Errorf("missing data: [DONE] in body=%s", body)
	}
	return nil
}

func sendChatRequest(port int, model string, apiKey string, stream bool) (int, []byte, error) {
	payload := map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "say ok"}},
		"stream":   stream,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}
	return nil
}
