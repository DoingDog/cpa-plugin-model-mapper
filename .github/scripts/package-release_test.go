package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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
		{version: "vv1.2.3"},
		{version: "vv0.5.2"},
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

func TestSinglePlatformChecksumFailureRemovesStaleChecksum(t *testing.T) {
	dir := t.TempDir()
	library := filepath.Join(dir, "model-mapper.dll")
	archive := filepath.Join(dir, "model-mapper.zip")
	checksum := archive + ".sha256"
	if err := os.WriteFile(library, []byte("old library"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-library", library, "-archive", archive, "-checksum", checksum}
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(library, []byte("new library"), 0o644); err != nil {
		t.Fatal(err)
	}

	original := writeChecksumFile
	writeChecksumFile = func(path string, data []byte, perm os.FileMode) error {
		if path == checksum {
			if err := os.WriteFile(path, []byte("partial"), perm); err != nil {
				return err
			}
			return errors.New("injected checksum failure")
		}
		return os.WriteFile(path, data, perm)
	}
	t.Cleanup(func() { writeChecksumFile = original })
	if err := run(args); err == nil || !strings.Contains(err.Error(), "injected checksum failure") {
		t.Fatalf("run error=%v", err)
	}
	if _, err := os.Stat(checksum); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checksum remains after failure: %v", err)
	}
}

func TestRunRejectsAliasedSinglePlatformPathsBeforeWriting(t *testing.T) {
	for _, tt := range []struct {
		name  string
		paths func(string) []string
	}{
		{
			name: "library equals archive",
			paths: func(dir string) []string {
				library := filepath.Join(dir, "model-mapper.so")
				return []string{library, library, filepath.Join(dir, "checksums.txt")}
			},
		},
		{
			name: "library equals checksum",
			paths: func(dir string) []string {
				library := filepath.Join(dir, "model-mapper.so")
				return []string{library, filepath.Join(dir, "archive.zip"), library}
			},
		},
		{
			name: "archive equals checksum",
			paths: func(dir string) []string {
				archive := filepath.Join(dir, "archive.zip")
				return []string{filepath.Join(dir, "model-mapper.so"), archive, archive}
			},
		},
		{
			name: "archive normalized alias checksum",
			paths: func(dir string) []string {
				archive := filepath.Join(dir, "x.zip")
				checksum := dir + string(filepath.Separator) + "." + string(filepath.Separator) + "x.zip"
				return []string{filepath.Join(dir, "model-mapper.so"), archive, checksum}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			paths := tt.paths(t.TempDir())
			sentinels := writeDistinctSentinels(t, paths...)

			err := run([]string{"-library", paths[0], "-archive", paths[1], "-checksum", paths[2]})
			if err == nil || !strings.Contains(err.Error(), "must be distinct") {
				t.Errorf("run error = %v, want distinct path error", err)
			}
			assertSentinelsUnchanged(t, sentinels, paths...)
		})
	}

	t.Run("hardlink alias", func(t *testing.T) {
		dir := t.TempDir()
		library := filepath.Join(dir, "model-mapper.so")
		archive := filepath.Join(dir, "archive.zip")
		checksum := filepath.Join(dir, "checksums.txt")
		sentinels := writeDistinctSentinels(t, library, archive)
		if err := os.Link(archive, checksum); err != nil {
			t.Skipf("hard links unsupported: %v", err)
		}
		sentinels[checksum] = sentinels[archive]

		err := run([]string{"-library", library, "-archive", archive, "-checksum", checksum})
		if err == nil || !strings.Contains(err.Error(), "must be distinct") {
			t.Errorf("run error = %v, want distinct path error", err)
		}
		assertSentinelsUnchanged(t, sentinels, library, archive, checksum)
	})
}

func writeDistinctSentinels(t *testing.T, paths ...string) map[string][]byte {
	t.Helper()
	sentinels := make(map[string][]byte)
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("absolute path %s: %v", path, err)
		}
		if _, ok := sentinels[absolute]; ok {
			continue
		}
		sentinel := []byte("sentinel-" + string(rune('a'+len(sentinels))))
		if err := os.WriteFile(path, sentinel, 0o644); err != nil {
			t.Fatalf("write sentinel %s: %v", path, err)
		}
		sentinels[absolute] = sentinel
	}
	return sentinels
}

func assertSentinelsUnchanged(t *testing.T, sentinels map[string][]byte, paths ...string) {
	t.Helper()
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("absolute path %s: %v", path, err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read sentinel %s: %v", path, err)
		}
		if want := sentinels[absolute]; !bytes.Equal(got, want) {
			t.Fatalf("sentinel %s = %q, want %q", path, got, want)
		}
	}
}

func TestRunRejectsAliasedAbsentOutputsBeforeWriting(t *testing.T) {
	for _, tt := range []struct {
		name  string
		paths func(*testing.T, string) (string, string)
	}{
		{
			name: "symlinked parent",
			paths: func(t *testing.T, dir string) (string, string) {
				t.Helper()
				realParent := filepath.Join(dir, "real")
				aliasParent := filepath.Join(dir, "alias")
				if err := os.Mkdir(realParent, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(realParent, aliasParent); err != nil {
					t.Skipf("create parent symlink: %v", err)
				}
				return filepath.Join(realParent, "archive.zip"), filepath.Join(aliasParent, "archive.zip")
			},
		},
		{
			name: "dangling checksum symlink",
			paths: func(t *testing.T, dir string) (string, string) {
				t.Helper()
				out := filepath.Join(dir, "out")
				if err := os.Mkdir(out, 0o755); err != nil {
					t.Fatal(err)
				}
				archive := filepath.Join(out, "archive.zip")
				checksum := filepath.Join(out, "checksums.txt")
				if err := os.Symlink(filepath.Base(archive), checksum); err != nil {
					t.Skipf("create checksum symlink: %v", err)
				}
				return archive, checksum
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			library := filepath.Join(dir, "model-mapper.so")
			if err := os.WriteFile(library, []byte("plugin"), 0o644); err != nil {
				t.Fatal(err)
			}
			archive, checksum := tt.paths(t, dir)

			err := run([]string{"-library", library, "-archive", archive, "-checksum", checksum})
			if err == nil || !strings.Contains(err.Error(), "must be distinct") {
				t.Fatalf("run error = %v, want distinct path error", err)
			}
			if _, err := os.Lstat(archive); !os.IsNotExist(err) {
				t.Fatalf("archive exists after rejected run: %v", err)
			}
		})
	}
}

func TestRunRevalidatesCaseInsensitiveArchiveChecksumAliases(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "Archive.zip")
	checksum := filepath.Join(dir, "archive.zip")
	if err := os.WriteFile(archive, []byte("case probe"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe, err := os.ReadFile(checksum)
	if err != nil || !bytes.Equal(probe, []byte("case probe")) {
		t.Skip("temporary filesystem is case-sensitive")
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}

	library := filepath.Join(dir, "model-mapper.so")
	if err := os.WriteFile(library, []byte("plugin"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"-library", library, "-archive", archive, "-checksum", checksum})
	if err == nil || !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("run error = %v, want distinct path error", err)
	}
	archiveBytes, err := os.ReadFile(archive)
	if err == nil {
		if _, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes))); err != nil {
			t.Fatalf("archive became a checksum text file: %v", err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("read archive: %v", err)
	}
}

func TestRunRejectsWindowsRootRelativeDanglingSymlinkTargetBeforeWriting(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows root-relative symlink targets are Windows-specific")
	}

	dir := t.TempDir()
	library := filepath.Join(dir, "model-mapper.dll")
	archive := filepath.Join(dir, "out", "archive.zip")
	checksum := filepath.Join(dir, "links", "checksums.txt")
	if err := os.WriteFile(library, []byte("plugin"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(checksum), 0o755); err != nil {
		t.Fatal(err)
	}

	target := strings.TrimPrefix(archive, filepath.VolumeName(archive))
	if filepath.IsAbs(target) || filepath.VolumeName(target) != "" {
		t.Fatalf("root-relative target = %q, want no volume and filepath.IsAbs false", target)
	}
	if err := os.Symlink(target, checksum); err != nil {
		if errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("create checksum symlink requires Windows symlink privilege: %v", err)
		}
		t.Fatalf("create checksum symlink: %v", err)
	}

	err := run([]string{"-library", library, "-archive", archive, "-checksum", checksum})
	if err == nil || !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("run error = %v, want distinct path error", err)
	}
	if _, err := os.Lstat(archive); !os.IsNotExist(err) {
		t.Fatalf("archive exists after rejected run: %v", err)
	}
}

func TestRunRejectsAliasedOutputsThroughSymlinkThenParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX path resolution applies a symlink before a following ..")
	}

	dir := t.TempDir()
	library := filepath.Join(dir, "model-mapper.so")
	realChild := filepath.Join(dir, "real", "child")
	out := filepath.Join(dir, "out")
	hop := filepath.Join(out, "hop")
	archive := filepath.Join(dir, "real", "archive.zip")
	// Preserve the raw spelling: POSIX resolves hop before the following `..`.
	checksum := hop + string(filepath.Separator) + ".." + string(filepath.Separator) + "archive.zip"
	if err := os.WriteFile(library, []byte("plugin"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(realChild, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realChild, hop); err != nil {
		t.Skipf("create hop symlink: %v", err)
	}

	err := run([]string{"-library", library, "-archive", archive, "-checksum", checksum})
	if err == nil || !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("run error = %v, want distinct path error", err)
	}
	if _, err := os.Lstat(archive); !os.IsNotExist(err) {
		t.Fatalf("archive exists after rejected run: %v", err)
	}
}

func TestPackageExistingArtifactsRejectsUnverifiedVersionsBeforeWriting(t *testing.T) {
	for _, tt := range []struct {
		name         string
		version      string
		writeSidecar bool
	}{
		{name: "missing sidecar"},
		{name: "empty sidecar", writeSidecar: true},
		{name: "development version", version: "0.0.0-dev\n", writeSidecar: true},
		{name: "different release version", version: "0.5.1\n", writeSidecar: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			dist := filepath.Join(dir, "dist")
			out := filepath.Join(dir, "release")
			validPath := filepath.Join(dist, "linux_amd64", "model-mapper.so")
			invalidPath := filepath.Join(dist, "windows_amd64", "model-mapper.dll")
			for _, path := range []string{validPath, invalidPath} {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("plugin"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(validPath+".version", []byte("0.5.2\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tt.writeSidecar {
				if err := os.WriteFile(invalidPath+".version", []byte(tt.version), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			err := packageExistingArtifacts("0.5.2", dist, out)
			if err == nil {
				t.Fatal("packageExistingArtifacts error = nil, want unverified artifact error")
			}
			if !strings.Contains(err.Error(), filepath.ToSlash(invalidPath)) || !strings.Contains(err.Error(), "0.5.2") {
				t.Fatalf("packageExistingArtifacts error = %q, want offending artifact and expected version", err)
			}
			entries, readErr := os.ReadDir(out)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("output directory contains files: %v", entries)
			}
		})
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
	linuxPath := filepath.Join(linuxDir, "model-mapper.so")
	if err := os.WriteFile(linuxPath, []byte("linux"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linuxPath+".version", []byte("0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	windowsPath := filepath.Join(windowsDir, "model-mapper.dll")
	if err := os.WriteFile(windowsPath, []byte("windows"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(windowsPath+".version", []byte("0.1.0\n"), 0o644); err != nil {
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

func TestPackageExistingArtifactsChecksumFailureRemovesStaleManifest(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	out := filepath.Join(dir, "release")
	writeArtifact := func(path, contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".version", []byte("0.5.3\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	linuxPath := filepath.Join(dist, "linux_amd64", "model-mapper.so")
	windowsPath := filepath.Join(dist, "windows_amd64", "model-mapper.dll")
	writeArtifact(linuxPath, "linux")
	writeArtifact(windowsPath, "windows")
	if err := packageExistingArtifacts("0.5.3", dist, out); err != nil {
		t.Fatalf("first packageExistingArtifacts error = %v", err)
	}

	if err := os.WriteFile(linuxPath, []byte("new linux"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{windowsPath, windowsPath + ".version"} {
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove Windows artifact %s: %v", path, err)
		}
	}

	checksumsPath := filepath.Join(out, "checksums.txt")
	original := writeChecksumFile
	writeChecksumFile = func(path string, data []byte, perm os.FileMode) error {
		if path == checksumsPath {
			if err := os.WriteFile(path, []byte("partial"), perm); err != nil {
				return err
			}
			return errors.New("injected checksums failure")
		}
		return os.WriteFile(path, data, perm)
	}
	t.Cleanup(func() { writeChecksumFile = original })
	if err := packageExistingArtifacts("0.5.3", dist, out); err == nil || !strings.Contains(err.Error(), "injected checksums failure") {
		t.Fatalf("packageExistingArtifacts error=%v", err)
	}
	manifest := filepath.Join(out, "checksums.txt")
	if _, err := os.Stat(manifest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checksum manifest remains after failure: %v", err)
	}
}

func TestPackageExistingArtifactsRemovesOnlyStaleCurrentVersionArchives(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	out := filepath.Join(dir, "release")
	writeArtifact := func(path, contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".version", []byte("0.5.3\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	linuxPath := filepath.Join(dist, "linux_amd64", "model-mapper.so")
	windowsPath := filepath.Join(dist, "windows_amd64", "model-mapper.dll")
	writeArtifact(linuxPath, "linux")
	writeArtifact(windowsPath, "windows")
	if err := packageExistingArtifacts("0.5.3", dist, out); err != nil {
		t.Fatalf("first packageExistingArtifacts error = %v", err)
	}

	linuxArchive := filepath.Join(out, "model-mapper_0.5.3_linux_amd64.zip")
	windowsArchive := filepath.Join(out, "model-mapper_0.5.3_windows_amd64.zip")
	fixtures := map[string][]byte{
		filepath.Join(out, "model-mapper_0.5.2_windows_amd64.zip"): []byte("other version"),
		filepath.Join(out, "model-mapper_0.5.3_unknown_riscv.zip"): []byte("unknown platform"),
		filepath.Join(out, "notes.txt"):                            []byte("notes"),
	}
	for path, contents := range fixtures {
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{windowsPath, windowsPath + ".version"} {
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove Windows artifact %s: %v", path, err)
		}
	}

	if err := packageExistingArtifacts("0.5.3", dist, out); err != nil {
		t.Fatalf("second packageExistingArtifacts error = %v", err)
	}
	if _, err := os.Stat(linuxArchive); err != nil {
		t.Fatalf("stat Linux archive: %v", err)
	}
	if _, err := os.Stat(windowsArchive); err == nil {
		t.Fatalf("stale Windows archive %s still exists", windowsArchive)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat stale Windows archive: %v", err)
	}
	checksums, err := os.ReadFile(filepath.Join(out, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(checksums), "model-mapper_0.5.3_windows_amd64.zip") {
		t.Fatalf("checksums.txt contains stale Windows archive: %q", checksums)
	}
	for path, want := range fixtures {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read fixture %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("fixture %s = %q, want %q", path, got, want)
		}
	}
}
