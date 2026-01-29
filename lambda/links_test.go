package main

import (
	"net/url"
	"testing"
)

func TestExtractLinks(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		baseURL string
		want    []string
	}{
		{
			name:    "single absolute link",
			html:    `<html><body><a href="https://example.com/page">Link</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/page"},
		},
		{
			name:    "relative link",
			html:    `<html><body><a href="/about">About</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/about"},
		},
		{
			name:    "multiple links",
			html:    `<html><body><a href="/a">A</a><a href="/b">B</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/a", "https://example.com/b"},
		},
		{
			name:    "deduplicates links",
			html:    `<html><body><a href="/a">A</a><a href="/a">A again</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/a"},
		},
		{
			name:    "skips javascript links",
			html:    `<html><body><a href="javascript:void(0)">JS</a><a href="/real">Real</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/real"},
		},
		{
			name:    "skips mailto links",
			html:    `<html><body><a href="mailto:user@example.com">Email</a><a href="/real">Real</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/real"},
		},
		{
			name:    "skips fragment-only links",
			html:    `<html><body><a href="#section">Anchor</a><a href="/real">Real</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/real"},
		},
		{
			name:    "removes fragments from links",
			html:    `<html><body><a href="/page#section">Page</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/page"},
		},
		{
			name:    "no links",
			html:    `<html><body><p>No links here</p></body></html>`,
			baseURL: "https://example.com",
			want:    nil,
		},
		{
			name:    "empty href",
			html:    `<html><body><a href="">Empty</a></body></html>`,
			baseURL: "https://example.com",
			want:    nil,
		},
		{
			name:    "external link",
			html:    `<html><body><a href="https://other.com/page">Other</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://other.com/page"},
		},
		{
			name:    "skips data URIs",
			html:    `<html><body><a href="data:text/html,hello">Data</a><a href="/real">Real</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/real"},
		},
		{
			name:    "skips tel links",
			html:    `<html><body><a href="tel:+1234567890">Call</a><a href="/real">Real</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/real"},
		},
		{
			name:    "nested elements",
			html:    `<html><body><div><p><a href="/deep">Deep</a></p></div></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/deep"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLinks([]byte(tt.html), tt.baseURL)
			if len(got) != len(tt.want) {
				t.Fatalf("extractLinks() returned %d links, want %d\ngot:  %v\nwant: %v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("link[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractText(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "simple paragraph",
			html: `<html><body><p>Hello World</p></body></html>`,
			want: "Hello World",
		},
		{
			name: "strips script tags",
			html: `<html><body><script>var x = 1;</script><p>Visible</p></body></html>`,
			want: "Visible",
		},
		{
			name: "strips style tags",
			html: `<html><body><style>body { color: red; }</style><p>Visible</p></body></html>`,
			want: "Visible",
		},
		{
			name: "strips noscript tags",
			html: `<html><body><noscript>Enable JS</noscript><p>Content</p></body></html>`,
			want: "Content",
		},
		{
			name: "strips head content",
			html: `<html><head><title>Title</title></head><body><p>Body</p></body></html>`,
			want: "Body",
		},
		{
			name: "multiple text nodes joined with spaces",
			html: `<html><body><p>Hello</p><p>World</p></body></html>`,
			want: "Hello World",
		},
		{
			name: "trims whitespace",
			html: `<html><body><p>  Hello  </p></body></html>`,
			want: "Hello",
		},
		{
			name: "empty body",
			html: `<html><body></body></html>`,
			want: "",
		},
		{
			name: "mixed content",
			html: `<html><head><title>T</title></head><body><h1>Header</h1><script>bad()</script><p>Text here</p><style>.x{}</style></body></html>`,
			want: "Header Text here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractText([]byte(tt.html))
			if got != tt.want {
				t.Errorf("extractText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeURL(t *testing.T) {
	base, _ := url.Parse("https://example.com/dir/page")

	tests := []struct {
		name string
		href string
		want string
	}{
		{"absolute https", "https://other.com/page", "https://other.com/page"},
		{"absolute http", "http://other.com/page", "http://other.com/page"},
		{"relative path", "/about", "https://example.com/about"},
		{"relative to current dir", "sibling", "https://example.com/dir/sibling"},
		{"with fragment removed", "/page#section", "https://example.com/page"},
		{"empty string", "", ""},
		{"fragment only", "#top", ""},
		{"javascript", "javascript:void(0)", ""},
		{"mailto", "mailto:user@example.com", ""},
		{"tel", "tel:+1234567890", ""},
		{"data uri", "data:text/html,hello", ""},
		{"ftp scheme rejected", "ftp://files.example.com/file", ""},
		{"with query string", "/search?q=test", "https://example.com/search?q=test"},
		{"whitespace trimmed", "  /page  ", "https://example.com/page"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeURL(tt.href, base)
			if got != tt.want {
				t.Errorf("normalizeURL(%q) = %q, want %q", tt.href, got, tt.want)
			}
		})
	}
}
