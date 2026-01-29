package main

import (
	"testing"
)

func TestIsHTML(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{"text/html", "text/html", true},
		{"text/html with charset", "text/html; charset=utf-8", true},
		{"application/xhtml", "application/xhtml+xml", true},
		{"text/plain", "text/plain", false},
		{"application/json", "application/json", false},
		{"image/png", "image/png", false},
		{"empty", "", false},
		{"case insensitive", "Text/HTML", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isHTML(tt.contentType)
			if got != tt.want {
				t.Errorf("isHTML(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestGzipCompress(t *testing.T) {
	data := []byte("hello world")
	compressed, err := gzipCompress(data)
	if err != nil {
		t.Fatalf("gzipCompress() error = %v", err)
	}

	if len(compressed) == 0 {
		t.Error("gzipCompress() returned empty result")
	}

	// Compressed data should have gzip magic number
	if compressed[0] != 0x1f || compressed[1] != 0x8b {
		t.Error("gzipCompress() output missing gzip magic number")
	}

	// Empty input should work
	compressed, err = gzipCompress([]byte{})
	if err != nil {
		t.Fatalf("gzipCompress(empty) error = %v", err)
	}
	if len(compressed) == 0 {
		t.Error("gzipCompress(empty) returned empty result")
	}
}
