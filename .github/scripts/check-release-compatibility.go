package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var glibcVersionPattern = regexp.MustCompile(`\bGLIBC_([0-9]+(\.[0-9]+)*)\b`)

func main() {
	if err := run(os.Stdin, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(input io.Reader, args []string) error {
	flags := flag.NewFlagSet("check-release-compatibility", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("format", "", "readelf or otool output format")
	maximum := flags.String("max", "", "maximum supported version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *maximum == "" {
		return fmt.Errorf("max is required")
	}
	switch *format {
	case "glibc":
		return checkGLIBCCompatibility(input, *maximum)
	case "macos":
		return checkMacOSCompatibility(input, *maximum)
	default:
		return fmt.Errorf("unsupported format %q", *format)
	}
}

func checkGLIBCCompatibility(input io.Reader, maximum string) error {
	body, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("read readelf output: %w", err)
	}
	matches := glibcVersionPattern.FindAllStringSubmatch(string(body), -1)
	versions := make([]string, 0, len(matches))
	for _, match := range matches {
		versions = append(versions, match[1])
	}
	return checkMaximumVersion("GLIBC", versions, maximum)
}

func checkMacOSCompatibility(input io.Reader, maximum string) error {
	scanner := bufio.NewScanner(input)
	var versions []string
	wantField := ""
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "cmd" {
			switch fields[1] {
			case "LC_BUILD_VERSION":
				wantField = "minos"
			case "LC_VERSION_MIN_MACOSX":
				wantField = "version"
			default:
				wantField = ""
			}
			continue
		}
		if wantField != "" && len(fields) >= 2 && fields[0] == wantField {
			versions = append(versions, fields[1])
			wantField = ""
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read otool output: %w", err)
	}
	return checkMaximumVersion("macOS", versions, maximum)
}

func checkMaximumVersion(name string, versions []string, maximum string) error {
	if len(versions) == 0 {
		return fmt.Errorf("%s compatibility version not found", name)
	}
	highest := versions[0]
	for _, version := range versions[1:] {
		comparison, err := compareDottedVersions(version, highest)
		if err != nil {
			return fmt.Errorf("parse %s version: %w", name, err)
		}
		if comparison > 0 {
			highest = version
		}
	}
	comparison, err := compareDottedVersions(highest, maximum)
	if err != nil {
		return fmt.Errorf("compare %s version: %w", name, err)
	}
	if comparison > 0 {
		return fmt.Errorf("%s requirement %s exceeds supported maximum %s", name, highest, maximum)
	}
	return nil
}

func compareDottedVersions(left, right string) (int, error) {
	leftParts, err := parseDottedVersion(left)
	if err != nil {
		return 0, err
	}
	rightParts, err := parseDottedVersion(right)
	if err != nil {
		return 0, err
	}
	count := max(len(leftParts), len(rightParts))
	for i := range count {
		var leftPart, rightPart int
		if i < len(leftParts) {
			leftPart = leftParts[i]
		}
		if i < len(rightParts) {
			rightPart = rightParts[i]
		}
		if leftPart < rightPart {
			return -1, nil
		}
		if leftPart > rightPart {
			return 1, nil
		}
	}
	return 0, nil
}

func parseDottedVersion(version string) ([]int, error) {
	parts := strings.Split(version, ".")
	parsed := make([]int, len(parts))
	for i, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("invalid version %q", version)
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return nil, fmt.Errorf("invalid version %q", version)
		}
		parsed[i] = value
	}
	return parsed, nil
}
