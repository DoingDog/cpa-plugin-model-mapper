//go:build cgo

package main

import "testing"

func TestCIntLengthBounds(t *testing.T) {
	for _, tt := range []struct {
		name string
		size uint64
		ok   bool
	}{
		{name: "zero", size: 0, ok: true},
		{name: "maximum", size: maxCIntLength, ok: true},
		{name: "oversized", size: maxCIntLength + 1, ok: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := cIntLength(tt.size)
			if ok != tt.ok {
				t.Fatalf("cIntLength(%d) ok=%v, want %v", tt.size, ok, tt.ok)
			}
			if ok && uint64(got) != tt.size {
				t.Fatalf("cIntLength(%d)=%d", tt.size, got)
			}
		})
	}
}

func TestPluginResponseLengthBounds(t *testing.T) {
	for _, tt := range []struct {
		size uint64
		ok   bool
	}{
		{size: 0, ok: true},
		{size: maxPluginResponseBytes, ok: true},
		{size: maxPluginResponseBytes + 1, ok: false},
	} {
		if got := pluginResponseLength(tt.size); got != tt.ok {
			t.Fatalf("pluginResponseLength(%d)=%v, want %v", tt.size, got, tt.ok)
		}
	}
}

func TestCopyPluginRequestRejectsNullWithLength(t *testing.T) {
	if got, ok := copyPluginRequest(nil, 1); ok || got != nil {
		t.Fatalf("copyPluginRequest(nil,1)=(%v,%v), want (nil,false)", got, ok)
	}
	if got, ok := copyPluginRequest(nil, 0); !ok || got != nil {
		t.Fatalf("copyPluginRequest(nil,0)=(%v,%v), want (nil,true)", got, ok)
	}
	if _, ok := copyPluginRequest(nil, maxCIntLength+1); ok {
		t.Fatal("oversized request accepted")
	}
}
