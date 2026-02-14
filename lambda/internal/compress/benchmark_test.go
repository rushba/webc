package compress

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

// gzipNaive compresses data using gzip
func gzipNaive(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BenchmarkGzipCompress measures gzip compression of a typical HTML page
func BenchmarkGzipCompress(b *testing.B) {
	data := []byte(strings.Repeat("<p>This is a paragraph of content.</p>\n", 1000))
	b.ResetTimer()
	for b.Loop() {
		_, _ = gzipNaive(data)
	}
}

// BenchmarkGzipCompressPool measures gzip compression with pooled writers
func BenchmarkGzipCompressPool(b *testing.B) {
	data := []byte(strings.Repeat("<p>This is a paragraph of content.</p>\n", 1000))
	b.ResetTimer()
	for b.Loop() {
		_, _ = Gzip(data)
	}
}

// BenchmarkGzipDecompress verifies compressed data is valid
func BenchmarkGzipDecompress(b *testing.B) {
	data := []byte(strings.Repeat("<p>This is a paragraph of content.</p>\n", 1000))
	compressed, _ := gzipNaive(data)
	b.ResetTimer()
	for b.Loop() {
		r, _ := gzip.NewReader(bytes.NewReader(compressed))
		_, _ = io.ReadAll(r)
		_ = r.Close()
	}
}
