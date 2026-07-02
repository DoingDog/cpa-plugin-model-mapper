package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveVersionStripsLeadingV(t *testing.T) {
	got, err := resolveVersion("v0.1.0")
	if err != nil {
		t.Fatalf("resolveVersion error = %v", err)
	}
	if got != "0.1.0" {
		t.Fatalf("version = %q, want 0.1.0", got)
	}
}

func TestWriteChecksumsUsesSha256sumFormat(t *testing.T) {
	dir := t.TempDir()
	zipA := filepath.Join(dir, "model-mapper_0.1.0_windows_amd64.zip")
	zipB := filepath.Join(dir, "model-mapper_0.1.0_linux_amd64.zip")
	if err := os.WriteFile(zipA, []byte("windows"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zipB, []byte("linux"), 0o644); err != nil {
		t.Fatal(err)
	}
	checksumsPath := filepath.Join(dir, "checksums.txt")
	if err := writeChecksums(checksumsPath, []string{zipA, zipB}); err != nil {
		t.Fatalf("writeChecksums error = %v", err)
	}
	gotBytes, err := os.ReadFile(checksumsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(gotBytes)
	for _, tc := range []struct {
		path string
		body string
	}{
		{zipA, "windows"},
		{zipB, "linux"},
	} {
		sum := sha256.Sum256([]byte(tc.body))
		wantLine := hex.EncodeToString(sum[:]) + "  " + filepath.Base(tc.path)
		if !strings.Contains(got, wantLine+"\n") {
			t.Fatalf("checksums.txt = %q, missing %q", got, wantLine)
		}
	}
}
