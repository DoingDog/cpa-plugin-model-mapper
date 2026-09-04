package main

import (
	"archive/zip"
	"bytes"
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

func TestResolveVersionValidatesReleaseGrammar(t *testing.T) {
	for _, tt := range []struct {
		version string
		want    string
		valid   bool
	}{
		{version: "1.2.3", want: "1.2.3", valid: true},
		{version: "v1.2.3", want: "1.2.3", valid: true},
		{version: "0.0.0-dev", want: "0.0.0-dev", valid: true},
		{version: "v1.2.3-rc.1", want: "1.2.3-rc.1", valid: true},
		{version: "1.2.3+build.7", want: "1.2.3+build.7", valid: true},
		{version: "vbeta"},
		{version: "beta"},
		{version: "v1"},
		{version: "1.2"},
		{version: "01.2.3"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			got, err := resolveVersion(tt.version)
			if tt.valid {
				if err != nil || got != tt.want {
					t.Fatalf("resolveVersion(%q)=(%q,%v), want (%q,nil)", tt.version, got, err, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("resolveVersion(%q)=%q, want error", tt.version, got)
			}
		})
	}
}

func TestRunValidateOnly(t *testing.T) {
	t.Setenv("VERSION", "")
	if err := run([]string{"-validate-only", "-version", "v1.2.3"}); err != nil {
		t.Fatalf("valid version rejected: %v", err)
	}
	if err := run([]string{"-validate-only", "-version", "vbeta"}); err == nil {
		t.Fatal("invalid version accepted")
	}
}

func TestArtifactSpecsCoverFullPlatformMatrix(t *testing.T) {
	got := map[string]bool{}
	for _, spec := range artifactSpecs() {
		got[spec.osName+"/"+spec.arch] = true
	}
	for _, want := range []string{
		"linux/amd64",
		"linux/arm64",
		"darwin/amd64",
		"darwin/arm64",
		"windows/amd64",
		"windows/arm64",
		"freebsd/amd64",
	} {
		if !got[want] {
			t.Fatalf("artifactSpecs missing %s", want)
		}
	}
}

func TestPackageLibraryWritesRootLibraryEntryAndChecksum(t *testing.T) {
	dir := t.TempDir()
	libraryPath := filepath.Join(dir, "model-mapper.so")
	archivePath := filepath.Join(dir, "model-mapper_0.1.0_linux_amd64.zip")
	checksumPath := archivePath + ".sha256"

	if err := os.WriteFile(libraryPath, []byte("plugin-binary"), 0o644); err != nil {
		t.Fatalf("write library: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("license"), 0o644); err != nil {
		t.Fatalf("write license: %v", err)
	}
	t.Chdir(dir)

	if err := packageLibrary(libraryPath, archivePath); err != nil {
		t.Fatalf("packageLibrary error = %v", err)
	}
	if err := writeChecksum(checksumPath, archivePath); err != nil {
		t.Fatalf("writeChecksum error = %v", err)
	}
	archiveData, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(archiveData), int64(len(archiveData)))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	if len(reader.File) != 2 {
		t.Fatalf("zip entry count = %d, want library and LICENSE", len(reader.File))
	}
	entries := map[string]*zip.File{}
	for _, entry := range reader.File {
		entries[entry.Name] = entry
	}
	entry := entries["model-mapper.so"]
	if entry == nil {
		t.Fatalf("zip entries = %v, missing model-mapper.so", entries)
	}
	if entries["LICENSE"] == nil {
		t.Fatalf("zip entries = %v, missing LICENSE", entries)
	}
	if entry.FileInfo().Mode().Perm() != 0o755 {
		t.Fatalf("zip entry mode = %v, want 0755", entry.FileInfo().Mode().Perm())
	}

	checksumRaw, err := os.ReadFile(checksumPath)
	if err != nil {
		t.Fatalf("read checksum: %v", err)
	}
	sum := sha256.Sum256(archiveData)
	wantLine := hex.EncodeToString(sum[:]) + "  model-mapper_0.1.0_linux_amd64.zip\n"
	if string(checksumRaw) != wantLine {
		t.Fatalf("checksum line = %q, want %q", string(checksumRaw), wantLine)
	}
	if strings.Contains(string(checksumRaw), string(filepath.Separator)+"model-mapper_0.1.0_linux_amd64.zip") {
		t.Fatalf("checksum line includes a path: %q", string(checksumRaw))
	}
}

func TestPackageExistingArtifactsUsesSha256sumFormat(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	out := filepath.Join(dir, "release")
	linuxDir := filepath.Join(dist, "linux_amd64")
	windowsDir := filepath.Join(dist, "windows_amd64")
	if err := os.MkdirAll(linuxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(windowsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linuxDir, "model-mapper.so"), []byte("linux"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(windowsDir, "model-mapper.dll"), []byte("windows"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := packageExistingArtifacts("0.1.0", dist, out); err != nil {
		t.Fatalf("packageExistingArtifacts error = %v", err)
	}
	checksumsPath := filepath.Join(out, "checksums.txt")
	gotBytes, err := os.ReadFile(checksumsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(gotBytes)
	for _, name := range []string{
		"model-mapper_0.1.0_linux_amd64.zip",
		"model-mapper_0.1.0_windows_amd64.zip",
	} {
		zipBytes, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(zipBytes)
		wantLine := hex.EncodeToString(sum[:]) + "  " + name
		if !strings.Contains(got, wantLine+"\n") {
			t.Fatalf("checksums.txt = %q, missing %q", got, wantLine)
		}
	}
}
