package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestSmokePluginPathsUseHostPlatform(t *testing.T) {
	t.Setenv("CPA_SMOKE_DIST_DIR", "")
	repo := filepath.Join("repo")
	dir := filepath.Join(repo, ".test-cpa")
	source, destination := smokePluginPaths(repo, dir)
	ext := ".so"
	if runtime.GOOS == "windows" {
		ext = ".dll"
	}
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	name := "model-mapper" + ext
	if source != filepath.Join(repo, "dist", runtime.GOOS+"_"+runtime.GOARCH, name) {
		t.Fatalf("source=%q", source)
	}
	if destination != filepath.Join(dir, "plugins", runtime.GOOS, runtime.GOARCH, name) {
		t.Fatalf("destination=%q", destination)
	}
}

func TestSmokePluginPathsCustomDist(t *testing.T) {
	for _, tc := range []struct {
		name    string
		distDir string
	}{
		{name: "relative", distDir: "custom builds"},
		{name: "absolute", distDir: filepath.Join(t.TempDir(), "absolute builds")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CPA_SMOKE_DIST_DIR", tc.distDir)
			repo := t.TempDir()
			dir := filepath.Join(repo, ".test-cpa")
			ext := ".so"
			if runtime.GOOS == "windows" {
				ext = ".dll"
			}
			if runtime.GOOS == "darwin" {
				ext = ".dylib"
			}
			name := "model-mapper" + ext
			platform := runtime.GOOS + "_" + runtime.GOARCH
			customDir := tc.distDir
			if !filepath.IsAbs(customDir) {
				customDir = filepath.Join(repo, customDir)
			}
			oldSource := filepath.Join(repo, "dist", platform, name)
			wantSource := filepath.Join(customDir, platform, name)
			for _, file := range []struct {
				path string
				data string
			}{
				{oldSource, "old bytes"},
				{wantSource, "new bytes"},
			} {
				if err := os.MkdirAll(filepath.Dir(file.path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file.path, []byte(file.data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			source, destination := smokePluginPaths(repo, dir)
			if source != wantSource {
				t.Fatalf("source=%q, want %q", source, wantSource)
			}
			if wantDestination := filepath.Join(dir, "plugins", runtime.GOOS, runtime.GOARCH, name); destination != wantDestination {
				t.Fatalf("destination=%q, want %q", destination, wantDestination)
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := copyFile(source, destination); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "new bytes" {
				t.Fatalf("copied %q, want new bytes", got)
			}
		})
	}
}

func TestStartCPAResolvesRelativeExecutableFromRepoRoot(t *testing.T) {
	root := t.TempDir()
	cpaBin := filepath.Join(root, "tools", "cpa-helper"+helperExecutableExtension())
	if err := os.MkdirAll(filepath.Dir(cpaBin), 0o755); err != nil {
		t.Fatalf("create helper directory: %v", err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("get test executable: %v", err)
	}
	if err := copyFile(executable, cpaBin); err != nil {
		t.Fatalf("copy test executable: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(cpaBin, 0o755); err != nil {
			t.Fatalf("make test executable runnable: %v", err)
		}
	}

	dir := filepath.Join(root, ".test-cpa")
	env := smokeEnv{
		repoRoot: root,
		dir:      dir,
		cpaBin:   filepath.Join("tools", "cpa-helper"+helperExecutableExtension()),
		logsDir:  filepath.Join(dir, "logs"),
		logFile:  filepath.Join(dir, "logs", "cpa.log"),
	}
	if err := os.MkdirAll(env.logsDir, 0o755); err != nil {
		t.Fatalf("create log directory: %v", err)
	}

	proc, err := startCPA(env)
	if proc != nil {
		waitErr := <-proc.waitDone
		if closeErr := proc.logFile.Close(); closeErr != nil {
			t.Fatalf("close log file: %v", closeErr)
		}
		if waitErr == nil {
			t.Fatal("started process exit error = nil")
		}
		return
	}
	if err == nil {
		t.Fatal("startCPA result = nil")
	}
	if strings.Contains(err.Error(), "start CPA") || strings.Contains(strings.ToLower(err.Error()), "file not found") {
		t.Fatalf("startCPA error = %v, want started process exit", err)
	}
	var startedExit *cpaStartedExitError
	if !errors.As(err, &startedExit) {
		t.Fatalf("startCPA error = %v, want cpaStartedExitError", err)
	}
}

func helperExecutableExtension() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

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

func TestStopCPAReturnsFailureAfterSuccessfulInterrupt(t *testing.T) {
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
	if _, err := logFile.WriteString("CPA crashed after interrupt"); err != nil {
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
			waitDone <- errors.New("exit status 2")
			return nil
		},
	}

	err = stopCPA(proc)
	if err == nil || !strings.Contains(err.Error(), "exit status 2") || !strings.Contains(err.Error(), "CPA crashed after interrupt") {
		t.Fatalf("stopCPA error = %v, want failure after successful interrupt", err)
	}
}

func TestStopCPAReturnsFailureWhenKillFindsExitedProcess(t *testing.T) {
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
		signal:   func(os.Signal) error { return nil },
		kill: func() error {
			waitDone <- errors.New("exit status 2")
			return os.ErrProcessDone
		},
	}

	err = stopCPA(proc)
	if err == nil || !strings.Contains(err.Error(), "exit status 2") || !strings.Contains(err.Error(), "CPA crashed before kill") {
		t.Fatalf("stopCPA error = %v, want failure when kill finds exited process", err)
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

func TestRunStreamCaseRejectsInBandError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("data: {\"model\":\"client\"}\n\ndata: {\"error\":{\"message\":\"upstream failed\"}}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	err := runStreamCase(port, caseConfig{requestModel: "client", requestAPIKey: localAPIKey, wantOriginalModel: "client"})
	if err == nil {
		t.Fatal("runStreamCase error = nil")
	}
	if !strings.Contains(err.Error(), "upstream failed") {
		t.Fatalf("runStreamCase error = %v, want in-band stream error", err)
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

func TestRunStreamCaseRejectsDataLinesWithoutEventBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"model\":\"client\"}\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	if err := runStreamCase(port, caseConfig{requestModel: "client", requestAPIKey: localAPIKey, wantOriginalModel: "client"}); err == nil {
		t.Fatal("runStreamCase accepted two data fields as separate events")
	}
}

func TestRunStreamCaseAcceptsMultiDataJSONEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"model\":\ndata: \"client\"}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	if err := runStreamCase(port, caseConfig{requestModel: "client", requestAPIKey: localAPIKey, wantOriginalModel: "client"}); err != nil {
		t.Fatalf("runStreamCase error = %v", err)
	}
}

func requireFunctionalOpenAIStream(t *testing.T, body []byte, model string, completions bool) {
	t.Helper()
	if err := validateOpenAIStream(body, caseConfig{wantOriginalModel: model}); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(body, []byte("data: [DONE]")) != 1 || bytes.Contains(body, []byte("data: data:")) {
		t.Fatalf("invalid framing: %q", body)
	}
	var content strings.Builder
	for _, line := range bytes.Split(body, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		raw := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data: ")))
		if bytes.Equal(raw, []byte("[DONE]")) {
			continue
		}
		var event struct {
			Model   string
			Choices []struct {
				Text  string
				Delta struct{ Content string }
			}
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if event.Model != model {
			t.Fatalf("model=%q want=%q", event.Model, model)
		}
		for _, choice := range event.Choices {
			if completions {
				content.WriteString(choice.Text)
			} else {
				content.WriteString(choice.Delta.Content)
			}
		}
	}
	if content.String() != "onetwo" {
		t.Fatalf("content=%q want=onetwo", content.String())
	}
}

func functionalCPAProcess(t *testing.T, repoRoot, cpaBin, upstreamURL, rules, extra string, enabled bool) smokeEnv {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	env := smokeEnv{repoRoot: repoRoot, cpaBin: cpaBin, dir: dir, port: port, baseURL: upstreamURL + "/v1", apiKey: "fake-upstream-key", config: filepath.Join(dir, "config.yaml"), logsDir: filepath.Join(dir, "logs"), logFile: filepath.Join(dir, "logs", "cpa.log")}
	_, env.plugin = smokePluginPaths(repoRoot, dir)
	if err := prepareDirs(env); err != nil {
		t.Fatal(err)
	}
	source := os.Getenv("CPA_SMOKE_PLUGIN")
	if source == "" {
		t.Fatal("CPA_SMOKE_PLUGIN is required")
	}
	if err := copyFile(source, env.plugin); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(env.plugin)
	if err != nil || sha256.Sum256(original) != sha256.Sum256(copied) {
		t.Fatalf("DLL copy: %v", err)
	}
	config := buildConfig(env, caseConfig{pluginRules: rules, rulesField: "global_rules"}) + "remote-management:\n  secret-key: local-integration-management-key\n  disable-control-panel: true\n" + extra
	if !enabled {
		config = strings.Replace(config, "plugins:\n  enabled: true\n", "plugins:\n  enabled: false\n", 1)
	}
	if extra != "" {
		var nativeConfig map[string]any
		if err := yaml.Unmarshal([]byte(config), &nativeConfig); err != nil {
			t.Fatal(err)
		}
		delete(nativeConfig, "openai-compatibility")
		encoded, err := yaml.Marshal(nativeConfig)
		if err != nil {
			t.Fatal(err)
		}
		config = string(encoded)
	}
	t.Logf("CPA process enabled=%v DLL source=%s copy=%s SHA256=%x config=%s", enabled, source, env.plugin, sha256.Sum256(original), config)
	if err := writeSmokeConfig(env.config, []byte(config)); err != nil {
		t.Fatal(err)
	}
	proc, err := startCPA(env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stopCPA(proc); err != nil {
			t.Error(err)
		}
	})
	if err := waitReady(proc, port, localAPIKey); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		shadowDir := filepath.Join(env.dir, "tmp", "cliproxy-pluginhost", fmt.Sprintf("pid-%d", proc.cmd.Process.Pid))
		entries, err := os.ReadDir(shadowDir)
		if err != nil && (enabled || !errors.Is(err, os.ErrNotExist)) {
			t.Fatalf("shadow directory: %v", err)
		}
		shadows := 0
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".dll" {
				continue
			}
			shadow, err := os.ReadFile(filepath.Join(shadowDir, entry.Name()))
			if err != nil || sha256.Sum256(shadow) != sha256.Sum256(original) {
				t.Fatalf("shadow identity: %v", err)
			}
			shadows++
			t.Logf("DLL source=%s copy=%s shadow=%s SHA256=%x", source, env.plugin, entry.Name(), sha256.Sum256(shadow))
		}
		wantShadows := 0
		if enabled {
			wantShadows = 1
		}
		if shadows != wantShadows {
			t.Fatalf("native DLL shadows=%d want=%d", shadows, wantShadows)
		}
		t.Logf("CPA process enabled=%v native DLL shadows=%d", enabled, shadows)
	}
	return env
}

func functionalHTTPRequest(t *testing.T, port int, path string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+localAPIKey)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("HTTP read=%v close=%v", readErr, closeErr)
	}
	if len(raw) > 1<<20 {
		t.Logf("HTTP path=%s request=%s status=%d headers=%v responseBytes=%d SHA256=%x", path, body, resp.StatusCode, resp.Header, len(raw), sha256.Sum256(raw))
	} else {
		t.Logf("HTTP path=%s request=%s status=%d headers=%v response=%s", path, body, resp.StatusCode, resp.Header, raw)
	}
	return resp.StatusCode, resp.Header, raw
}

func requireFunctionalCPARegistration(t *testing.T, env smokeEnv, enabled bool) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v0/management/plugins", env.port), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Management-Key", "local-integration-management-key")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var listing struct {
		PluginsEnabled bool `json:"plugins_enabled"`
		Plugins        []struct {
			ID               string
			Configured       bool
			Registered       bool
			EffectiveEnabled bool `json:"effective_enabled"`
		}
	}
	if err := json.NewDecoder(response.Body).Decode(&listing); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || listing.PluginsEnabled != enabled || len(listing.Plugins) != 1 || listing.Plugins[0].ID != "model-mapper" || !listing.Plugins[0].Configured || listing.Plugins[0].Registered != enabled || listing.Plugins[0].EffectiveEnabled != enabled {
		t.Fatalf("registration enabled=%v status=%d listing=%+v", enabled, response.StatusCode, listing)
	}
	t.Logf("CPA registration enabled=%v status=%d listing=%+v", enabled, response.StatusCode, listing)
}

func prepareFunctionalCPAOverlay(t *testing.T, repoRoot string) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-mod=readonly", "-m", "-json", "github.com/router-for-me/CLIProxyAPI/v7")
	cmd.Dir, cmd.Env = repoRoot, append(os.Environ(), "GOWORK=off")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list CPA: %v\n%s", err, raw)
	}
	var module struct{ Path, Version, Dir, Sum string }
	if err := json.Unmarshal(raw, &module); err != nil {
		t.Fatal(err)
	}
	if module.Path != "github.com/router-for-me/CLIProxyAPI/v7" || module.Version != "v7.2.152" || module.Dir == "" || module.Sum != "h1:FkvGzpOCvuDGswaOyoVfbY5Ua7OlP/wMXw3agiNMUQI=" {
		t.Fatalf("unexpected CPA module: %+v", module)
	}
	work := t.TempDir()
	if evidence := os.Getenv("CPA_FUNCTIONAL_EVIDENCE"); evidence != "" {
		work = filepath.Join(evidence, fmt.Sprintf("cpa-overlay-%x", sha256.Sum256([]byte(t.Name()))))
		if err := os.Mkdir(work, 0700); err != nil {
			t.Fatal(err)
		}
	}
	checkout := filepath.Join(work, "cpa-v7.2.152")
	err = filepath.WalkDir(module.Dir, func(source string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(module.Dir, source)
		if err != nil {
			return err
		}
		target := filepath.Join(checkout, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected CPA source entry %s", source)
		}
		return copyFile(source, target)
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(repoRoot, ".github", "scripts", "testdata", "cpa-functional-regression_test.go")
	if info, err := os.Stat(fixture); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("fixture %s: %v", fixture, err)
	}
	target := filepath.Join(checkout, "internal", "pluginhost", "model_mapper_functional_test.go")
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overlay target already exists: %s %v", target, err)
	}
	encoded, err := json.Marshal(struct{ Replace map[string]string }{Replace: map[string]string{target: fixture}})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(work, "overlay.json")
	if err := os.WriteFile(overlay, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("CPA version=%s Sum=%s source=%s copy=%s overlay=%s", module.Version, module.Sum, module.Dir, checkout, overlay)
	return checkout, overlay
}

func runFunctionalCPAOverlay(t *testing.T, repoRoot string, env []string) {
	t.Helper()
	checkout, overlay := prepareFunctionalCPAOverlay(t, repoRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 660*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "-C", checkout, "test", "-mod=readonly", "-overlay", overlay, "-count=1", "-v", "./internal/pluginhost", "-run", "^TestModelMapperFunctional|^TestStreamBridge(CloseUnblocksPendingEmit|ClosePreservesTerminalErrorWhenBufferIsFull)$", "-timeout", "600s")
	cmd.Dir, cmd.Env = repoRoot, append(append(os.Environ(), "GOWORK=off"), env...)
	output, err := cmd.CombinedOutput()
	functionalSaveEvidence(t, "cpa-overlay.log", output)
	t.Logf("CPA overlay command=%v\n%s", cmd.Args, output)
	if err != nil {
		t.Fatalf("CPA overlay: %v", err)
	}
}

func TestCPAPluginIntegration(t *testing.T) {
	cpaBin := os.Getenv("CPA_SMOKE_CPA_BIN")
	if cpaBin == "" || os.Getenv("CPA_SMOKE_INTEGRATION") != "1" {
		t.Skip("make integration is required for CPA integration")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	info, err := buildinfo.ReadFile(cpaBin)
	if err != nil {
		t.Fatal(err)
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if info.Main.Version != "v7.2.152" || settings["vcs.revision"] != "c76dfd4e0edabab9000628b1560ab8ab379eadb8" || settings["vcs.modified"] != "false" {
		t.Fatalf("unexpected CPA binary: %+v", info)
	}
	binary, err := os.ReadFile(cpaBin)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CPA binary=%s SHA256=%x revision=%s Go=%s", cpaBin, sha256.Sum256(binary), settings["vcs.revision"], info.GoVersion)
	var calls atomic.Int32
	var upstreamMode atomic.Int32
	var duplicateRequest atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		t.Logf("upstream path=%s headers=%v body=%s", r.URL.Path, r.Header, raw)
		var request struct {
			Model    string
			Stream   bool
			Messages []struct{ Role, Content string }
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || request.Model != "deepseek-v4-flash" || r.Header.Get("Authorization") != "Bearer fake-upstream-key" {
			http.Error(w, "unexpected upstream request", 400)
			return
		}
		if duplicateRequest.Load() {
			want := `{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"first"}],"messages":[{"role":"user","content":"second"}]}`
			if string(raw) != want {
				t.Errorf("duplicate request=%s want=%s", raw, want)
			}
		} else if len(request.Messages) != 1 || request.Messages[0].Role != "user" || request.Messages[0].Content != "say ok" {
			t.Errorf("ordinary upstream messages=%+v want one user message with content=say ok", request.Messages)
			http.Error(w, "unexpected ordinary upstream messages", http.StatusBadRequest)
			return
		}
		if !request.Stream {
			w.Header().Set("Content-Type", "application/json")
			if upstreamMode.Load() == 2 {
				_, _ = io.WriteString(w, `{"model":"deepseek-v4-flash","choices":[{"text":"first"}],"model":"deepseek-v4-pro","choices":[{"text":"second"}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"message":{"role":"assistant","content":"upstream deepseek-v4-flash remains"},"finish_reason":"stop"}]}`)
			return
		}
		contentTypes := []string{"text/event-stream", "", "text/event-stream; charset=utf-8", "application/json"}
		if header := contentTypes[upstreamMode.Load()]; header != "" {
			w.Header().Set("Content-Type", header)
		}
		for _, payload := range []string{
			`{"id":"chatcmpl-functional","object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"content":"one"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-functional","object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"content":"two"},"finish_reason":"stop"}]}`,
		} {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	env := functionalCPAProcess(t, repoRoot, cpaBin, upstream.URL, "deepseek-v4-pro=>deepseek-v4-flash", "", true)
	t.Run("registration", func(t *testing.T) {
		requireFunctionalCPARegistration(t, env, true)
	})
	for _, endpoint := range []string{"chat", "completions"} {
		for _, model := range []string{"deepseek-v4-pro", "deepseek-v4-flash"} {
			for _, stream := range []bool{false, true} {
				for header := 0; header < 4; header++ {
					if !stream && header != 0 {
						continue
					}
					t.Run(fmt.Sprintf("%s/model=%s/stream=%v/header=%d", endpoint, model, stream, header), func(t *testing.T) {
						upstreamMode.Store(int32(header))
						calls.Store(0)
						path, field := "/v1/chat/completions", `"messages":[{"role":"user","content":"say ok"}]`
						if endpoint == "completions" {
							path, field = "/v1/completions", `"prompt":"say ok"`
						}
						body := []byte(fmt.Sprintf(`{"model":%q,%s,"stream":%v}`, model, field, stream))
						status, _, raw := functionalHTTPRequest(t, env.port, path, body)
						if status != 200 || calls.Load() != 1 {
							t.Fatalf("status=%d calls=%d", status, calls.Load())
						}
						if stream {
							requireFunctionalOpenAIStream(t, raw, model, endpoint == "completions")
							return
						}
						var value struct {
							Model   string
							Choices []struct {
								Text    string
								Message struct{ Content string }
							}
						}
						if err := json.Unmarshal(raw, &value); err != nil {
							t.Fatal(err)
						}
						if value.Model != model || len(value.Choices) != 1 {
							t.Fatalf("completion=%s", raw)
						}
						content := value.Choices[0].Message.Content
						if endpoint == "completions" {
							content = value.Choices[0].Text
						}
						if content != "upstream deepseek-v4-flash remains" {
							t.Fatalf("content=%q", content)
						}
					})
				}
			}
		}
	}
	t.Run("request-duplicate-content", func(t *testing.T) {
		duplicateRequest.Store(true)
		defer duplicateRequest.Store(false)
		upstreamMode.Store(1)
		calls.Store(0)
		status, _, _ := functionalHTTPRequest(t, env.port, "/v1/chat/completions", []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"first"}],"model":"deepseek-v4-pro","messages":[{"role":"user","content":"second"}]}`))
		if status != 200 || calls.Load() != 1 {
			t.Fatalf("status=%d calls=%d", status, calls.Load())
		}
	})
	t.Run("response-duplicate-content", func(t *testing.T) {
		upstreamMode.Store(2)
		calls.Store(0)
		status, _, raw := functionalHTTPRequest(t, env.port, "/v1/chat/completions", []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"say ok"}]}`))
		want := `{"model":"deepseek-v4-pro","choices":[{"text":"first"}],"model":"deepseek-v4-pro","choices":[{"text":"second"}]}`
		if status != 200 || string(raw) != want || calls.Load() != 1 {
			t.Fatalf("status=%d calls=%d response=%s want=%s", status, calls.Load(), raw, want)
		}
	})
	upstreamMode.Store(0)
	t.Run("route-runtime-empty", func(t *testing.T) {
		empty := functionalCPAProcess(t, repoRoot, cpaBin, upstream.URL, "deepseek-v4-flash*=>$1", "", true)
		for _, stream := range []bool{false, true} {
			calls.Store(0)
			status, _, raw := functionalHTTPRequest(t, empty.port, "/v1/chat/completions", []byte(fmt.Sprintf(`{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"say ok"}],"stream":%v}`, stream)))
			if status < 400 || !bytes.Contains(raw, []byte("empty mapped model")) || calls.Load() != 0 {
				t.Errorf("stream=%v status=%d calls=%d response=%s", stream, status, calls.Load(), raw)
			}
		}
	})
	binaryCaptures := functionalNativeBinaryCaptures(t, repoRoot, cpaBin, false)
	mixedCaptures := functionalNativeBinaryCaptures(t, repoRoot, cpaBin, true)
	t.Run("native-host-and-responses-ws", func(t *testing.T) {
		runFunctionalCPAOverlay(t, repoRoot, []string{"CPA_SMOKE_PLUGIN=" + env.plugin, fmt.Sprintf("CPA_FUNCTIONAL_WS_URL=ws://127.0.0.1:%d/v1/responses", env.port), "CPA_FUNCTIONAL_LOCAL_KEY=" + localAPIKey, "CPA_FUNCTIONAL_BINARY_CAPTURES=" + binaryCaptures, "CPA_FUNCTIONAL_MIXED_CAPTURES=" + mixedCaptures})
	})
}

func functionalNativeBinaryCaptures(t *testing.T, repoRoot, cpaBin string, mixed bool) string {
	t.Helper()
	wsClient := filepath.Join(t.TempDir(), "mixed-ws-client.exe")
	if mixed {
		t.Run("mixed-ws-client-build", func(t *testing.T) {
			checkout, overlay := prepareFunctionalCPAOverlay(t, repoRoot)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "-C", checkout, "test", "-mod=readonly", "-overlay", overlay, "-c", "-o", wsClient, "./internal/pluginhost")
			cmd.Env = append(os.Environ(), "GOWORK=off")
			raw, err := cmd.CombinedOutput()
			functionalSaveEvidence(t, "mixed-ws-client-build.log", raw)
			if err != nil {
				t.Fatalf("CPA WS client build: %v\n%s", err, raw)
			}
		})
	}
	const output = `[{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}]`
	payloads := []string{`{"type":"response.created","response":{"model":"MODEL","status":"in_progress","output":[]}}`, `{"type":"response.in_progress","response":{"model":"MODEL","status":"in_progress","output":[]}}`, `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}`, `{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"ordinary grok-4.7 opaque 中文 output"}`, `{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"ordinary grok-4.7 opaque 中文 output"}`, `{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}}`, `{"type":"response.output_item.done","output_index":0,"item":{"id":"msg-issue8","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ordinary grok-4.7 opaque 中文 output","annotations":[]}]}}`, `{"type":"response.completed","response":{"id":"resp-issue8","object":"response","status":"completed","model":"MODEL","output":` + output + `}}`}
	type fixture struct {
		name, eol, contentType, metadata string
		step, count                      int
		dataOnly                         bool
	}
	var fixtures []fixture
	for _, dataOnly := range []bool{false, true} {
		for _, count := range []int{1, 9} {
			for _, f := range []fixture{
				{name: "LF-whole", eol: "\n", contentType: "text/event-stream"},
				{name: "CRLF-whole", eol: "\r\n", contentType: "text/event-stream"},
				{name: "LF-7bytes", eol: "\n", contentType: "text/event-stream", step: 7},
				{name: "CRLF-bytewise", eol: "\r\n", contentType: "text/event-stream", step: 1},
				{name: "LF-charset", eol: "\n", contentType: "text/event-stream; charset=utf-8", step: 11},
				{name: "CRLF-charset", eol: "\r\n", contentType: "text/event-stream; charset=utf-8", step: 7},
			} {
				metadataValues := []string{"", "id: event-1", "retry: 100", ": heartbeat"}
				if mixed {
					if !dataOnly {
						continue
					}
					metadataValues = append(metadataValues, "id: event-1\nretry: 100\n: heartbeat")
				}
				for _, metadata := range metadataValues {
					variant := f
					variant.dataOnly, variant.count, variant.metadata = dataOnly, count, metadata
					label := metadata
					if label == "" {
						label = "none"
					}
					variant.name = fmt.Sprintf("dataOnly=%v/count=%d/%s/metadata=%s", dataOnly, count, f.name, label)
					if mixed {
						if count == 1 {
							variant.count = 2
						}
						labels := map[string]string{"": "none", "id: event-1": "id", "retry: 100": "retry", ": heartbeat": "heartbeat", "id: event-1\nretry: 100\n: heartbeat": "combined"}
						variant.name = fmt.Sprintf("mixed/count=%d/metadata=%s/%s", variant.count, labels[metadata], f.name)
					}
					fixtures = append(fixtures, variant)
				}
			}
		}
	}
	type capture struct {
		Name, Fixture, ClientModel, UpstreamModel, Metadata string
		Kind                                                string
		WS, Errors                                          []string
		Stream, DataOnly                                    bool
		Status, Count, Calls                                int
		Headers                                             http.Header
		Body, Request, UpstreamResponse                     []byte
		RequestHeaders                                      http.Header
		Path                                                string
	}
	var captures []capture
	for _, provider := range []string{"xai", "codex"} {
		mapped := make(map[string]capture)
		for _, enabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/enabled=%v", provider, enabled), func(t *testing.T) {
				var mu sync.Mutex
				var current fixture
				var records []capture
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					var req struct {
						Model  string
						Stream bool
					}
					if err := json.Unmarshal(body, &req); err != nil {
						t.Error(err)
						return
					}
					if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || !req.Stream || (req.Model != "grok-4.6" && req.Model != "grok-4.7") || r.Header.Get("Authorization") != "Bearer fake-upstream-key" {
						t.Errorf("native upstream path=%s headers=%v body=%s", r.URL.Path, r.Header, body)
					}
					mu.Lock()
					f := current
					mu.Unlock()
					values := payloads
					if f.count == 1 {
						values = values[len(values)-1:]
					} else if f.count == 2 {
						values = []string{values[0], values[len(values)-1]}
					}
					var wire strings.Builder
					for i, payload := range values {
						payload = strings.Replace(payload, `"model":"MODEL"`, `"model":"`+req.Model+`"`, 1)
						var event struct{ Type string }
						if err := json.Unmarshal([]byte(payload), &event); err != nil {
							t.Error(err)
							return
						}
						if f.metadata != "" && (!mixed || i == 1) {
							fmt.Fprintf(&wire, "%s%s", strings.ReplaceAll(f.metadata, "\n", f.eol), f.eol)
						}
						if !f.dataOnly || mixed && i > 0 {
							fmt.Fprintf(&wire, "event: %s%s", event.Type, f.eol)
						}
						fmt.Fprintf(&wire, "data: %s%s%s", payload, f.eol, f.eol)
					}
					mu.Lock()
					records = append(records, capture{UpstreamModel: req.Model, Request: bytes.Clone(body), RequestHeaders: r.Header.Clone(), Path: r.URL.RequestURI(), UpstreamResponse: []byte(wire.String())})
					mu.Unlock()
					w.Header().Set("Content-Type", f.contentType)
					step := f.step
					if step == 0 {
						step = wire.Len()
					}
					for start := 0; start < wire.Len(); start += step {
						if _, err := io.WriteString(w, wire.String()[start:min(start+step, wire.Len())]); err != nil {
							t.Error(err)
							return
						}
						w.(http.Flusher).Flush()
					}
				}))
				defer upstream.Close()
				extra := fmt.Sprintf("%s-api-key:\n  - api-key: fake-upstream-key\n    base-url: %q\n    proxy-url: direct\n    models:\n      - name: grok-4.6\n        alias: grok-4.6\n      - name: grok-4.7\n        alias: grok-4.7\n", provider, upstream.URL+"/v1")
				env := functionalCPAProcess(t, repoRoot, cpaBin, upstream.URL, "grok-4.6=>grok-4.7", extra, enabled)
				requireFunctionalCPARegistration(t, env, enabled)
				t.Cleanup(func() { functionalSaveProcessEvidence(t, env) })
				for _, f := range fixtures {
					kinds := []string{"http-nonstream", "http-stream"}
					if mixed {
						kinds = append(kinds, "ws")
					}
					for _, kind := range kinds {
						stream := kind != "http-nonstream"
						for _, model := range []string{"grok-4.6", "grok-4.7"} {
							t.Run(fmt.Sprintf("%s/kind=%s/model=%s", f.name, kind, model), func(t *testing.T) {
								mu.Lock()
								current, records = f, nil
								mu.Unlock()
								body := []byte(fmt.Sprintf(`{"model":%q,"input":"say ok","stream":%v,"prompt_cache_key":"task-g-local"}`, model, stream))
								var status int
								var headers http.Header
								var raw []byte
								var ws []string
								var wsErr error
								if kind == "ws" {
									ws, wsErr = functionalMixedBinaryWS(t, wsClient, env.port, model)
								} else {
									status, headers, raw = functionalHTTPRequest(t, env.port, "/v1/responses", body)
								}
								mu.Lock()
								got := append([]capture(nil), records...)
								mu.Unlock()
								variant, wantUpstream := "direct", model
								if !enabled {
									variant = "disabled-" + model
								} else if model == "grok-4.6" {
									variant, wantUpstream = "mapped", "grok-4.7"
								}
								if len(got) != 1 || got[0].UpstreamModel != wantUpstream {
									t.Fatalf("upstream calls=%+v", got)
								}
								record := got[0]
								record.Name, record.Fixture, record.ClientModel = provider+"/"+variant+"/"+f.name, f.name, model
								record.Metadata = f.metadata
								record.Stream, record.DataOnly, record.Count, record.Calls = stream, f.dataOnly, f.count, len(got)
								record.Status, record.Headers, record.Body = status, headers, raw
								record.Kind, record.WS = kind, ws
								if wsErr != nil {
									record.Errors = []string{wsErr.Error()}
									t.Error(wsErr)
								}
								captures = append(captures, record)
								if kind != "ws" && status != 200 {
									t.Errorf("native %s stream=%v status=%d error=%s", record.Name, stream, status, raw)
								}
								key := fmt.Sprintf("%s/stream=%v", f.name, stream)
								if mixed {
									key = f.name + "/" + kind
								}
								if mixed && kind == "ws" {
									// 独立 WS 的真实会话字段由永久 CPA fixture 精确核对，保留原始 bytes。
									t.Logf("mixed binary WS %s payloads=%q errors=%q upstreamRequest=%s headers=%v", record.Name, record.WS, record.Errors, record.Request, record.RequestHeaders)
								} else if variant == "mapped" {
									mapped[key] = record
								} else {
									control, ok := mapped[key]
									if !ok {
										t.Fatalf("mapped capture missing for %s", key)
									}
									want := record.Request
									if !enabled && model == "grok-4.6" {
										want = bytes.Replace(want, []byte(`"model":"grok-4.6"`), []byte(`"model":"grok-4.7"`), 1)
									}
									if !bytes.Equal(want, control.Request) {
										t.Errorf("non-model upstream bytes changed: mapped=%s control=%s", control.Request, record.Request)
									}
									if variant == "direct" && (!reflect.DeepEqual(control.RequestHeaders, record.RequestHeaders) || control.Path != record.Path || !bytes.Equal(control.UpstreamResponse, record.UpstreamResponse)) {
										t.Errorf("mapped/direct upstream headers, URI or response bytes differ: mapped=%+v direct=%+v", control, record)
									}
								}
								t.Logf("native binary %s stream=%v upstreamRequest=%s requestHeaders=%v upstreamSHA256=%x", record.Name, stream, record.Request, record.RequestHeaders, sha256.Sum256(record.UpstreamResponse))
							})
						}
					}
				}
			})
		}
	}
	encoded, err := json.Marshal(captures)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("binary captures JSON=%s", encoded)
	name := "native-binary-captures.json"
	if mixed {
		name = "mixed-binary-captures.json"
	}
	functionalSaveEvidence(t, name, encoded)
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func functionalMixedBinaryWS(t *testing.T, client string, port int, model string) ([]string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "ws-result.json")
	cmd := exec.CommandContext(ctx, client, "-test.run=^TestModelMapperFunctionalMixedBinaryCaptures$", "-test.timeout=12s")
	cmd.Env = append(os.Environ(), fmt.Sprintf("CPA_FUNCTIONAL_MIXED_CLIENT_URL=ws://127.0.0.1:%d/v1/responses", port), "CPA_FUNCTIONAL_MIXED_CLIENT_MODEL="+model, "CPA_FUNCTIONAL_MIXED_CLIENT_KEY="+localAPIKey, "CPA_FUNCTIONAL_MIXED_CLIENT_RESULT="+path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("CPA WS client: %w: %s", err, output)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result struct {
		Values []string
		Error  string
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Error != "" {
		return result.Values, errors.New(result.Error)
	}
	return result.Values, nil
}

// CPA_FUNCTIONAL_EVIDENCE 只控制测试证据的保留位置，不参与产品配置。
func functionalSaveEvidence(t *testing.T, name string, raw []byte) {
	t.Helper()
	if dir := os.Getenv("CPA_FUNCTIONAL_EVIDENCE"); dir != "" {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("evidence already exists: %s %v", path, err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("evidence=%s SHA256=%x", path, sha256.Sum256(raw))
	}
}

func functionalSaveProcessEvidence(t *testing.T, env smokeEnv) {
	t.Helper()
	if os.Getenv("CPA_FUNCTIONAL_EVIDENCE") == "" {
		return
	}
	label := fmt.Sprintf("process-%x", sha256.Sum256([]byte(t.Name())))
	for name, source := range map[string]string{"config.yaml": env.config, "cpa.log": env.logFile, "loaded-copy.dll": env.plugin} {
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Error(err)
			continue
		}
		functionalSaveEvidence(t, filepath.Join(label, name), raw)
	}
	if runtime.GOOS == "windows" {
		matches, err := filepath.Glob(filepath.Join(env.dir, "tmp", "cliproxy-pluginhost", "pid-*", "*.dll"))
		if err != nil {
			t.Fatal(err)
		}
		for i, source := range matches {
			raw, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			functionalSaveEvidence(t, filepath.Join(label, fmt.Sprintf("shadow-%d.dll", i)), raw)
		}
	}
}
