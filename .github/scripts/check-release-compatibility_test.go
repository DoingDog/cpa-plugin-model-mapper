package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckGLIBCCompatibility(t *testing.T) {
	for _, tt := range []struct {
		name      string
		output    string
		wantErr   bool
		wantError string
	}{
		{
			name: "at baseline",
			output: "0x001c:   2 (GLIBC_2.2.5)\n" +
				"Name: GLIBC_2.2.5\nName: GLIBC_2.9\nName: GLIBC_2.17\n",
		},
		{
			name:    "newer than baseline",
			output:  "Name: GLIBC_2.2.5\nName: GLIBC_2.28\n",
			wantErr: true,
		},
		{
			name:      "DT RELR ABI requirement",
			output:    "Name: GLIBC_2.2.5\nName: GLIBC_ABI_DT_RELR\n",
			wantErr:   true,
			wantError: "GLIBC_ABI_DT_RELR",
		},
		{
			name:      "private ABI requirement",
			output:    "Name: GLIBC_2.17\nName: GLIBC_PRIVATE\n",
			wantErr:   true,
			wantError: "GLIBC_PRIVATE",
		},
		{
			name:      "nonnumeric suffix",
			output:    "Name: GLIBC_2.17-TEST\n",
			wantErr:   true,
			wantError: "GLIBC_2.17-TEST",
		},
		{
			name:    "missing version requirements",
			output:  "No version information found in this file.\n",
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := checkGLIBCCompatibility(strings.NewReader(tt.output), "2.17")
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkGLIBCCompatibility error=%v, wantErr=%v", err, tt.wantErr)
			}
			if tt.wantError != "" && !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("checkGLIBCCompatibility error=%q, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestMakeDryRunNormalizesReleaseVersion(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not available")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := wd
	if _, err := os.Stat(filepath.Join(repoRoot, "Makefile")); err != nil {
		repoRoot = filepath.Clean(filepath.Join(wd, "..", ".."))
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "Makefile")); err != nil {
		t.Fatalf("locate repository Makefile: %v", err)
	}

	for _, tt := range []struct {
		target string
		wants  []string
	}{
		{
			target: "build-platform",
			wants: []string{
				"-X main.pluginVersion=0.5.2",
				"printf '%s\\n' \"0.5.2\" > \"$out.version\"",
			},
		},
		{
			target: "package-platform",
			wants: []string{
				"-X main.pluginVersion=0.5.2",
				"model-mapper_0.5.2_windows_amd64.zip",
			},
		},
	} {
		t.Run(tt.target, func(t *testing.T) {
			cmd := exec.Command(makePath, "-n", tt.target, "VERSION=v0.5.2", "GOOS=windows", "GOARCH=amd64")
			cmd.Dir = repoRoot
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make -n %s: %v\n%s", tt.target, err, output)
			}
			for _, want := range tt.wants {
				if !strings.Contains(string(output), want) {
					t.Fatalf("make -n %s output missing %q:\n%s", tt.target, want, output)
				}
			}
			if strings.Contains(string(output), "pluginVersion=v0.5.2") || strings.Contains(string(output), "model-mapper_v0.5.2_") {
				t.Fatalf("make -n %s did not normalize version:\n%s", tt.target, output)
			}
		})
	}
}

func TestPackagePlatformStopsAfterCompatibilityFailure(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not available")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := wd
	if _, err := os.Stat(filepath.Join(repoRoot, "Makefile")); err != nil {
		repoRoot = filepath.Clean(filepath.Join(wd, "..", ".."))
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "Makefile")); err != nil {
		t.Fatalf("locate repository Makefile: %v", err)
	}

	tempDir := t.TempDir()
	distDir := filepath.Join(tempDir, "dist")
	libraryDir := filepath.Join(distDir, "linux_amd64")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	libraryPath := filepath.Join(libraryDir, "model-mapper.so")
	if err := os.WriteFile(libraryPath, []byte("test library"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libraryPath+".version", []byte("0.5.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readelf := filepath.Join(tempDir, "readelf")
	if err := os.WriteFile(readelf, []byte("#!/bin/sh\nprintf '%s\\n' 'Name: GLIBC_2.28'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readelf, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(makePath,
		"--no-print-directory", "package-platform",
		"VERSION=0.5.1", "GOOS=linux", "GOARCH=amd64",
		"DIST_DIR="+filepath.ToSlash(distDir),
		"READELF="+filepath.ToSlash(readelf),
		"MAKE=true",
	)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if !strings.Contains(string(output), "GLIBC requirement 2.28 exceeds supported maximum 2.17") {
		t.Fatalf("compatibility checker output missing:\n%s", output)
	}
	if err == nil {
		t.Fatalf("package-platform succeeded after compatibility rejection:\n%s", output)
	}
	archive := filepath.Join(distDir, "model-mapper_0.5.1_linux_amd64.zip")
	if _, statErr := os.Stat(archive); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("archive exists after compatibility rejection: %v", statErr)
	}
}

func TestCheckMacOSCompatibility(t *testing.T) {
	for _, tt := range []struct {
		name    string
		output  string
		wantErr bool
	}{
		{
			name: "build version at baseline",
			output: "Load command 9\n" +
				"      cmd LC_BUILD_VERSION\n" +
				"    minos 12.0\n" +
				"      sdk 15.0\n",
		},
		{
			name: "legacy minimum below baseline",
			output: "Load command 8\n" +
				"      cmd LC_VERSION_MIN_MACOSX\n" +
				"  version 10.13\n" +
				"      sdk 10.15\n",
		},
		{
			name: "newer than baseline",
			output: "Load command 9\n" +
				"      cmd LC_BUILD_VERSION\n" +
				"    minos 15.0\n",
			wantErr: true,
		},
		{
			name:    "missing deployment target",
			output:  "Load command 1\n      cmd LC_SEGMENT_64\n",
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := checkMacOSCompatibility(strings.NewReader(tt.output), "12.0")
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkMacOSCompatibility error=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
