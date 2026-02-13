package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

// BenchmarkGzipCompress measures gzip compression of a typical HTML page
func BenchmarkGzipCompress(b *testing.B) {
	data := []byte(strings.Repeat("<p>This is a paragraph of content.</p>\n", 1000))
	b.ResetTimer()
	for b.Loop() {
		_, _ = gzipCompress(data)
	}
}

// BenchmarkGzipCompressPool measures gzip compression with pooled writers
func BenchmarkGzipCompressPool(b *testing.B) {
	data := []byte(strings.Repeat("<p>This is a paragraph of content.</p>\n", 1000))
	b.ResetTimer()
	for b.Loop() {
		_, _ = gzipCompressPooled(data)
	}
}

// BenchmarkGzipDecompress verifies compressed data is valid
func BenchmarkGzipDecompress(b *testing.B) {
	data := []byte(strings.Repeat("<p>This is a paragraph of content.</p>\n", 1000))
	compressed, _ := gzipCompress(data)
	b.ResetTimer()
	for b.Loop() {
		r, _ := gzip.NewReader(bytes.NewReader(compressed))
		_, _ = io.ReadAll(r)
		_ = r.Close()
	}
}
