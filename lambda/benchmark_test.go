package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

// BenchmarkExtractLinks measures link extraction from HTML
func BenchmarkExtractLinks(b *testing.B) {
	html := generateBenchHTML(100)
	body := []byte(html)
	b.ResetTimer()
	for b.Loop() {
		extractLinks(body, "https://example.com")
	}
}

// BenchmarkExtractText measures text extraction from HTML
func BenchmarkExtractText(b *testing.B) {
	html := generateBenchHTML(100)
	body := []byte(html)
	b.ResetTimer()
	for b.Loop() {
		extractText(body)
	}
}

// BenchmarkExtractLinksAndText measures the combined cost of both extractions
func BenchmarkExtractLinksAndText(b *testing.B) {
	html := generateBenchHTML(100)
	body := []byte(html)
	b.ResetTimer()
	for b.Loop() {
		extractLinks(body, "https://example.com")
		extractText(body)
	}
}

// BenchmarkParseAndExtract measures the single-pass combined extraction
func BenchmarkParseAndExtract(b *testing.B) {
	html := generateBenchHTML(100)
	body := []byte(html)
	b.ResetTimer()
	for b.Loop() {
		parseAndExtract(body, "https://example.com")
	}
}

// BenchmarkNormalizeURL measures URL normalization
func BenchmarkNormalizeURL(b *testing.B) {
	base := mustParseURL("https://example.com/dir/page")
	b.ResetTimer()
	for b.Loop() {
		normalizeURL("/some/path?q=test#fragment", base)
	}
}

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

// BenchmarkHashURL measures URL hashing
func BenchmarkHashURL(b *testing.B) {
	for b.Loop() {
		hashURL("https://example.com/some/very/long/path?with=params&and=more")
	}
}

func generateBenchHTML(numLinks int) string {
	var sb strings.Builder
	sb.WriteString("<html><head><title>Test Page</title></head><body>")
	for i := range numLinks {
		sb.WriteString("<div><p>Some paragraph text here with content.</p>")
		sb.WriteString("<a href=\"/page/" + string(rune('a'+i%26)))
		sb.WriteString("\">Link text</a></div>\n")
	}
	sb.WriteString("</body></html>")
	return sb.String()
}
