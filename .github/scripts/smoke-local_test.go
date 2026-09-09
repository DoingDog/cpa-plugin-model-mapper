package main

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCheckPortAvailableRejectsOccupiedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	address := listener.Addr().String()
	if err := checkPortAvailable(address); err == nil {
		t.Fatal("occupied port was accepted")
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if err := checkPortAvailable(address); err != nil {
		t.Fatalf("released port rejected: %v", err)
	}
}

func TestWaitReadyReturnsCurrentProcessExit(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "cpa.log")
	if err := os.WriteFile(logPath, []byte("current CPA failed"), 0o600); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	proc := &cpaProcess{logFile: logFile, waitDone: make(chan error, 1)}
	proc.waitDone <- errors.New("exit status 1")
	close(proc.waitDone)

	started := time.Now()
	err = waitReady(proc, 1, "key")
	if err == nil || !strings.Contains(err.Error(), "current CPA failed") {
		t.Fatalf("waitReady error=%v, want current process log", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("waitReady did not return promptly after process exit")
	}
}

func TestStopCPANilIsNoop(t *testing.T) {
	if err := stopCPA(nil); err != nil {
		t.Fatalf("stopCPA(nil) error = %v", err)
	}
}

func TestStopCPATerminatesRunningProcess(t *testing.T) {
	if os.Getenv("CPA_SMOKE_HELPER_PROCESS") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
	if os.Getenv("CPA_SMOKE_HELPER_PROCESS") == "exit" {
		_, _ = os.Stdout.Write([]byte("CPA crashed before stop"))
		os.Exit(3)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestStopCPATerminatesRunningProcess")
	cmd.Env = append(os.Environ(), "CPA_SMOKE_HELPER_PROCESS=1")
	logFile, err := os.Create(filepath.Join(t.TempDir(), "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	proc := &cpaProcess{cmd: cmd, logFile: logFile, waitDone: make(chan error, 1)}
	go func() {
		proc.waitDone <- cmd.Wait()
		close(proc.waitDone)
	}()
	if err := stopCPA(proc); err != nil {
		t.Fatalf("stopCPA error = %v", err)
	}
}

func TestStopCPAReturnsPreexistingFailure(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestStopCPATerminatesRunningProcess")
	cmd.Env = append(os.Environ(), "CPA_SMOKE_HELPER_PROCESS=exit")
	logFile, err := os.Create(filepath.Join(t.TempDir(), "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	proc := &cpaProcess{cmd: cmd, logFile: logFile, waitDone: make(chan error, 1)}
	go func() {
		proc.waitDone <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-proc.waitDone:
	case <-time.After(time.Second):
		t.Fatal("process did not exit")
	}
	if waitErr == nil {
		t.Fatal("process exit error = nil")
	}
	proc.waitDone <- waitErr

	err = stopCPA(proc)
	if err == nil {
		t.Fatal("stopCPA error = nil")
	}
	if !strings.Contains(err.Error(), "CPA crashed before stop") {
		t.Fatalf("stopCPA error = %v, want process log", err)
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("stopCPA error = %v, want non-zero exit status", err)
	}
}

func TestStopCPAReturnsUnexpectedExitAfterKillRace(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestStopCPATerminatesRunningProcess")
	cmd.Env = append(os.Environ(), "CPA_SMOKE_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	logFile, err := os.Create(filepath.Join(t.TempDir(), "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logFile.WriteString("CPA crashed before kill"); err != nil {
		t.Fatal(err)
	}
	if err := logFile.Sync(); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	proc := &cpaProcess{
		cmd:      cmd,
		logFile:  logFile,
		waitDone: waitDone,
		signal: func(os.Signal) error {
			waitDone <- errors.New("exit status 3")
			close(waitDone)
			return errors.New("interrupt unavailable")
		},
		kill: func() error { return os.ErrProcessDone },
	}

	err = stopCPA(proc)
	if err == nil {
		t.Fatal("stopCPA error = nil")
	}
	if !strings.Contains(err.Error(), "exit status 3") || !strings.Contains(err.Error(), "CPA crashed before kill") {
		t.Fatalf("stopCPA error = %v, want raced process failure", err)
	}
}

func TestStopCPAReturnsExitQueuedAfterFailedInterrupt(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestStopCPATerminatesRunningProcess")
	cmd.Env = append(os.Environ(), "CPA_SMOKE_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	logFile, err := os.Create(filepath.Join(t.TempDir(), "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logFile.WriteString("CPA crashed before kill"); err != nil {
		t.Fatal(err)
	}
	if err := logFile.Sync(); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	killCalled := false
	proc := &cpaProcess{
		cmd:      cmd,
		logFile:  logFile,
		waitDone: waitDone,
		signal: func(os.Signal) error {
			waitDone <- errors.New("exit status 3")
			close(waitDone)
			return errors.New("interrupt unavailable")
		},
		kill: func() error {
			killCalled = true
			return nil
		},
	}

	err = stopCPA(proc)
	if err == nil || !strings.Contains(err.Error(), "exit status 3") || !strings.Contains(err.Error(), "CPA crashed before kill") {
		t.Fatalf("stopCPA error = %v, want queued process failure", err)
	}
	if killCalled {
		t.Fatal("kill hook was called after queued process exit")
	}
}

func TestRunCaseRemovesPartiallyWrittenConfig(t *testing.T) {
	dir := t.TempDir()
	env := smokeEnv{
		apiKey:  "smoke-config-secret-sentinel",
		config:  filepath.Join(dir, "config.yaml"),
		logFile: filepath.Join(dir, "cpa.log"),
	}
	originalWriteFile := writeSmokeConfigFile
	writeSmokeConfigFile = func(path string, data []byte, perm os.FileMode) error {
		if err := os.WriteFile(path, data[:len(data)/2], perm); err != nil {
			return err
		}
		return errors.New("partial write")
	}
	t.Cleanup(func() { writeSmokeConfigFile = originalWriteFile })

	err := runCase(env, caseConfig{})
	if err == nil || !strings.Contains(err.Error(), "write config") {
		t.Fatalf("runCase error = %v, want config write error", err)
	}
	if _, err := os.Stat(env.config); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partially written smoke config still exists")
	}
}

func TestRunCaseDoesNotDeleteExistingConfig(t *testing.T) {
	dir := t.TempDir()
	env := smokeEnv{
		apiKey:  "smoke-config-secret-sentinel",
		cpaBin:  filepath.Join(dir, "missing-cpa"),
		config:  filepath.Join(dir, "config.yaml"),
		logFile: filepath.Join(dir, "cpa.log"),
	}
	const userConfig = "user-owned-config"
	if err := os.WriteFile(env.config, []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runCase(env, caseConfig{}); err == nil {
		t.Fatal("runCase error = nil")
	}
	body, err := os.ReadFile(env.config)
	if err != nil {
		t.Fatalf("read existing config: %v", err)
	}
	if string(body) != userConfig {
		t.Fatalf("existing config = %q, want %q", body, userConfig)
	}
}

func TestRunCaseRemovesConfigAfterStartFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	env := smokeEnv{
		apiKey:  "smoke-config-secret-sentinel",
		cpaBin:  filepath.Join(dir, "missing-cpa"),
		port:    port,
		dir:     dir,
		config:  filepath.Join(dir, "config.yaml"),
		logFile: filepath.Join(dir, "cpa.log"),
	}
	if err := runCase(env, caseConfig{}); err == nil {
		t.Fatal("runCase error = nil")
	}
	if _, err := os.Stat(env.config); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("smoke config still exists after start failure")
	}
}

func TestWriteSmokeConfigTightensPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not report Unix file permissions")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("existing config"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeSmokeConfig(path, []byte("smoke config")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestRunCaseRejectsLaunchFailureWithConfigMarkerInExecutablePath(t *testing.T) {
	dir := t.TempDir()
	env := smokeEnv{
		apiKey:  "smoke-config-secret-sentinel",
		cpaBin:  filepath.Join(dir, "missing-invalid rule-cpa"),
		config:  filepath.Join(dir, "config.yaml"),
		logFile: filepath.Join(dir, "cpa.log"),
	}

	err := runCase(env, caseConfig{wantConfigFailureContains: "invalid rule"})
	if err == nil {
		t.Fatal("runCase error = nil")
	}
	if !strings.Contains(err.Error(), "start CPA") {
		t.Fatalf("runCase error = %v, want launch error", err)
	}
}

func TestRunCaseReturnsUnavailablePortBeforeConfigFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	dir := t.TempDir()
	env := smokeEnv{
		apiKey:  "smoke-config-secret-sentinel",
		cpaBin:  filepath.Join(dir, "missing-cpa"),
		port:    listener.Addr().(*net.TCPAddr).Port,
		dir:     dir,
		config:  filepath.Join(dir, "config.yaml"),
		logFile: filepath.Join(dir, "cpa.log"),
	}
	if err := os.WriteFile(env.logFile, []byte("invalid rule"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = runCase(env, caseConfig{wantConfigFailureContains: "invalid rule"})
	if err == nil {
		t.Fatal("runCase error = nil")
	}
	if !strings.Contains(err.Error(), "CPA port") || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("runCase error = %v, want unavailable port", err)
	}
}

func TestRequireConfigFailureRequiresMarker(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "cpa.log")
	env := smokeEnv{logFile: logPath}
	for _, tc := range []struct {
		name     string
		log      string
		err      error
		accepted bool
	}{
		{name: "log marker", log: "plugin config: invalid rule", err: errors.New("CPA exited early"), accepted: true},
		{name: "error marker", err: errors.New("invalid rule"), accepted: true},
		{name: "unrelated error", log: "model not found", err: errors.New("model not found")},
		{name: "empty log", err: errors.New("CPA readiness timeout")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(logPath, []byte(tc.log), 0o600); err != nil {
				t.Fatal(err)
			}
			err := requireConfigFailure(env, tc.err, "invalid rule")
			if (err == nil) != tc.accepted {
				t.Fatalf("requireConfigFailure error = %v, accepted = %t", err, tc.accepted)
			}
		})
	}
}

func TestRunStreamCaseRejectsMalformedData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("data: {\"model\":\"client\"}\n\ndata: {broken\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	err := runStreamCase(port, caseConfig{requestModel: "client", requestAPIKey: localAPIKey, wantOriginalModel: "client"})
	if err == nil {
		t.Fatal("runStreamCase error = nil")
	}
	if !strings.Contains(err.Error(), "decode streamed data") || !strings.Contains(err.Error(), "{broken") {
		t.Fatalf("runStreamCase error = %v, want malformed payload", err)
	}
}

func TestRunStreamCaseAcceptsValidData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("data: {\"model\":\"client\"}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	if err := runStreamCase(port, caseConfig{requestModel: "client", requestAPIKey: localAPIKey, wantOriginalModel: "client"}); err != nil {
		t.Fatalf("runStreamCase error = %v", err)
	}
}
