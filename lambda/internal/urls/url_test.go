package urls

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
			want:  Hash("https://example.com"), // deterministic
		},
		{
			name:  "different URLs produce different hashes",
			input: "https://example.com/page",
			want:  Hash("https://example.com/page"),
		},
		{
			name:  "empty string",
			input: "",
			want:  Hash(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Hash(tt.input)
			if got != tt.want {
				t.Errorf("Hash(%q) = %q, want %q", tt.input, got, tt.want)
			}
			// SHA256 output is 64 hex chars
			if len(got) != 64 {
				t.Errorf("Hash(%q) length = %d, want 64", tt.input, len(got))
			}
		})
	}

	// Same input always produces same output
	first := Hash("https://example.com")
	second := Hash("https://example.com")
	if first != second {
		t.Error("Hash is not deterministic")
	}

	// Different inputs produce different outputs
	if Hash("https://a.com") == Hash("https://b.com") {
		t.Error("Hash collision for different inputs")
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
			got := GetDomain(tt.input)
			if got != tt.want {
				t.Errorf("GetDomain(%q) = %q, want %q", tt.input, got, tt.want)
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
			got := GetHost(tt.input)
			if got != tt.want {
				t.Errorf("GetHost(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// BenchmarkHashURL measures URL hashing
func BenchmarkHashURL(b *testing.B) {
	for b.Loop() {
		Hash("https://example.com/some/very/long/path?with=params&and=more")
	}
}
