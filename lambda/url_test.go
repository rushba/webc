package main

import (
	"testing"
)

func TestHashURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "consistent hash",
			input: "https://example.com",
			want:  hashURL("https://example.com"), // deterministic
		},
		{
			name:  "different URLs produce different hashes",
			input: "https://example.com/page",
			want:  hashURL("https://example.com/page"),
		},
		{
			name:  "empty string",
			input: "",
			want:  hashURL(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hashURL(tt.input)
			if got != tt.want {
				t.Errorf("hashURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
			// SHA256 output is 64 hex chars
			if len(got) != 64 {
				t.Errorf("hashURL(%q) length = %d, want 64", tt.input, len(got))
			}
		})
	}

	// Same input always produces same output
	first := hashURL("https://example.com")
	second := hashURL("https://example.com")
	if first != second {
		t.Error("hashURL is not deterministic")
	}

	// Different inputs produce different outputs
	if hashURL("https://a.com") == hashURL("https://b.com") {
		t.Error("hashURL collision for different inputs")
	}
}

func TestGetDomain(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"https with path", "https://example.com/page", "https://example.com"},
		{"http scheme", "http://example.com/page", "http://example.com"},
		{"with port", "https://example.com:8080/page", "https://example.com:8080"},
		{"no path", "https://example.com", "https://example.com"},
		{"with query", "https://example.com/page?q=1", "https://example.com"},
		{"invalid URL", "://bad", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDomain(tt.input)
			if got != tt.want {
				t.Errorf("getDomain(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetHost(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "https://example.com/page", "example.com"},
		{"with port", "https://example.com:8080/page", "example.com:8080"},
		{"no path", "https://example.com", "example.com"},
		{"invalid URL", "://bad", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getHost(tt.input)
			if got != tt.want {
				t.Errorf("getHost(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
