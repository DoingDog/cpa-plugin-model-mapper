package main

import (
	"strings"
	"testing"
)

func TestCheckGLIBCCompatibility(t *testing.T) {
	for _, tt := range []struct {
		name    string
		output  string
		wantErr bool
	}{
		{
			name:   "at baseline",
			output: "Name: GLIBC_2.2.5\nName: GLIBC_2.9\nName: GLIBC_2.17\n",
		},
		{
			name:    "newer than baseline",
			output:  "Name: GLIBC_2.2.5\nName: GLIBC_2.28\n",
			wantErr: true,
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
		})
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
