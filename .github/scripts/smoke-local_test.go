package main

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
