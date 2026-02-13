package parser

import (
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
		Extract(body, "https://example.com")
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
