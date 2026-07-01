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

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %s: %w", *outDir, err)
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
		if _, err := os.Stat(artifact.binaryPath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("required artifact missing: %s", filepath.ToSlash(artifact.binaryPath))
			}
			return fmt.Errorf("stat required artifact %s: %w", filepath.ToSlash(artifact.binaryPath), err)
		}

		zipName := fmt.Sprintf("model-mapper_%s_%s_%s.zip", version, artifact.osName, artifact.arch)
		zipPath := filepath.Join(*outDir, zipName)
		if err := writeZip(zipPath, artifact.binaryPath); err != nil {
			return err
		}
		if err := writeSHA256(zipPath, filepath.Join(*outDir, zipName+".sha256")); err != nil {
			return err
		}
	}

	return nil
}

func resolveVersion(versionFlag string) (string, error) {
	if strings.TrimSpace(versionFlag) != "" {
		return versionFlag, nil
	}
	if version := strings.TrimSpace(os.Getenv("VERSION")); version != "" {
		return version, nil
	}
	cmd := exec.Command("git", "describe", "--tags", "--exact-match")
	output, err := cmd.Output()
	if err == nil {
		if version := strings.TrimSpace(string(output)); version != "" {
			return version, nil
		}
	}
	return "", fmt.Errorf("version is required: use -version, set VERSION, or run from an exact git tag")
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

func writeSHA256(zipPath, shaPath string) error {
	file, err := os.Open(zipPath)
	if err != nil {
		return fmt.Errorf("open zip for checksum %s: %w", zipPath, err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("hash zip %s: %w", zipPath, err)
	}
	checksum := hex.EncodeToString(hasher.Sum(nil))
	content := checksum + "  " + filepath.Base(zipPath) + "\n"
	if err := os.WriteFile(shaPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write checksum %s: %w", shaPath, err)
	}
	return nil
}
