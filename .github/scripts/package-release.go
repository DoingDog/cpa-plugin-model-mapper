package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	versionFlag := flag.String("version", "", "release version")
	distDir := flag.String("dist", "dist", "artifact directory")
	outDir := flag.String("out", filepath.Join("dist", "release"), "output directory")
	flag.Parse()

	version, err := resolveVersion(*versionFlag)
	if err != nil {
		return err
	}

	artifacts := []struct {
		osName     string
		arch       string
		binaryPath string
	}{
		{osName: "windows", arch: "amd64", binaryPath: filepath.Join(*distDir, "windows_amd64", "model-mapper.dll")},
		{osName: "linux", arch: "amd64", binaryPath: filepath.Join(*distDir, "linux_amd64", "model-mapper.so")},
	}

	for _, artifact := range artifacts {
		if err := ensureReadableFile(artifact.binaryPath, "required artifact"); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %s: %w", *outDir, err)
	}

	zipPaths := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		zipName := fmt.Sprintf("model-mapper_%s_%s_%s.zip", version, artifact.osName, artifact.arch)
		zipPath := filepath.Join(*outDir, zipName)
		if err := writeZip(zipPath, artifact.binaryPath); err != nil {
			return err
		}
		zipPaths = append(zipPaths, zipPath)
	}
	if err := writeChecksums(filepath.Join(*outDir, "checksums.txt"), zipPaths); err != nil {
		return err
	}

	return nil
}

func resolveVersion(versionFlag string) (string, error) {
	if version := normalizeReleaseVersion(versionFlag); version != "" {
		return version, nil
	}
	if version := normalizeReleaseVersion(os.Getenv("VERSION")); version != "" {
		return version, nil
	}
	cmd := exec.Command("git", "describe", "--tags", "--exact-match")
	output, err := cmd.Output()
	if err == nil {
		if version := normalizeReleaseVersion(string(output)); version != "" {
			return version, nil
		}
	}
	return "", fmt.Errorf("version is required: use -version, set VERSION, or run from an exact git tag")
}

func normalizeReleaseVersion(version string) string {
	version = strings.TrimSpace(version)
	return strings.TrimPrefix(version, "v")
}

func writeZip(zipPath, binaryPath string) error {
	file, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip %s: %w", zipPath, err)
	}
	defer file.Close()

	zipWriter := zip.NewWriter(file)

	if err := addFile(zipWriter, binaryPath, filepath.Base(binaryPath)); err != nil {
		return err
	}
	if _, err := os.Stat("README.md"); err == nil {
		if err := addFile(zipWriter, "README.md", "README.md"); err != nil {
			return err
		}
	}
	if _, err := os.Stat("LICENSE"); err == nil {
		if err := addFile(zipWriter, "LICENSE", "LICENSE"); err != nil {
			return err
		}
	}

	if err := zipWriter.Close(); err != nil {
		return fmt.Errorf("close zip %s: %w", zipPath, err)
	}
	return nil
}

func addFile(zipWriter *zip.Writer, srcPath, zipName string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer src.Close()

	entry, err := zipWriter.Create(zipName)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", zipName, err)
	}
	if _, err := io.Copy(entry, src); err != nil {
		return fmt.Errorf("write zip entry %s: %w", zipName, err)
	}
	return nil
}

func ensureReadableFile(path, label string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s missing: %s", label, filepath.ToSlash(path))
		}
		return fmt.Errorf("open %s %s: %w", label, filepath.ToSlash(path), err)
	}
	return file.Close()
}

func writeChecksums(path string, zipPaths []string) error {
	var builder strings.Builder
	for _, zipPath := range zipPaths {
		checksum, err := sha256File(zipPath)
		if err != nil {
			return err
		}
		builder.WriteString(checksum)
		builder.WriteString("  ")
		builder.WriteString(filepath.Base(zipPath))
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		return fmt.Errorf("write checksums %s: %w", path, err)
	}
	return nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open zip for checksum %s: %w", path, err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash zip %s: %w", path, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
