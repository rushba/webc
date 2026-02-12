package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestIsPermanentHTTPError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		// Permanent errors — should not retry
		{"400 Bad Request", 400, true},
		{"401 Unauthorized", 401, true},
		{"403 Forbidden", 403, true},
		{"404 Not Found", 404, true},
		{"405 Method Not Allowed", 405, true},
		{"410 Gone", 410, true},
		{"414 URI Too Long", 414, true},
		{"451 Unavailable For Legal Reasons", 451, true},

		// Retriable errors — should retry
		{"429 Too Many Requests", 429, false},
		{"500 Internal Server Error", 500, false},
		{"502 Bad Gateway", 502, false},
		{"503 Service Unavailable", 503, false},
		{"504 Gateway Timeout", 504, false},

		// Success codes — not permanent errors
		{"200 OK", 200, false},
		{"301 Moved Permanently", 301, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPermanentHTTPError(tt.statusCode)
			if got != tt.want {
				t.Errorf("isPermanentHTTPError(%d) = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestFetchURLSuccess(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "<html><body>Hello</body></html>")
	})

	c := newTestCrawler()
	c.httpClient = testHTTPClientWith(handler)

	result := c.fetchURL(context.Background(), "https://example.com/page")
	if !result.Success {
		t.Fatalf("fetchURL() success = false, error: %s", result.Error)
	}
	if result.StatusCode != 200 {
		t.Errorf("fetchURL() statusCode = %d, want 200", result.StatusCode)
	}
	if result.ContentType != "text/html" {
		t.Errorf("fetchURL() contentType = %q, want text/html", result.ContentType)
	}
	if !strings.Contains(string(result.Body), "Hello") {
		t.Error("fetchURL() body doesn't contain expected content")
	}
	if result.DurationMs < 0 {
		t.Errorf("fetchURL() durationMs = %d, want >= 0", result.DurationMs)
	}
}

func TestFetchURL404(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	c := newTestCrawler()
	c.httpClient = testHTTPClientWith(handler)

	result := c.fetchURL(context.Background(), "https://example.com/missing")
	if result.Success {
		t.Fatal("fetchURL() success = true for 404")
	}
	if result.StatusCode != 404 {
		t.Errorf("fetchURL() statusCode = %d, want 404", result.StatusCode)
	}
}

func TestFetchURL500(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	c := newTestCrawler()
	c.httpClient = testHTTPClientWith(handler)

	result := c.fetchURL(context.Background(), "https://example.com/error")
	if result.Success {
		t.Fatal("fetchURL() success = true for 500")
	}
	if result.StatusCode != 500 {
		t.Errorf("fetchURL() statusCode = %d, want 500", result.StatusCode)
	}
}

func TestFetchURLSSRFBlocked(t *testing.T) {
	c := newTestCrawler()
	c.httpClient = &http.Client{}

	result := c.fetchURL(context.Background(), "http://169.254.169.254/latest/meta-data")
	if result.Success {
		t.Fatal("fetchURL() should block SSRF attempt")
	}
	if !strings.Contains(result.Error, "SSRF") {
		t.Errorf("fetchURL() error = %q, want SSRF-related error", result.Error)
	}
}

func TestFetchURLInvalidURL(t *testing.T) {
	c := newTestCrawler()
	c.httpClient = &http.Client{}

	result := c.fetchURL(context.Background(), "://invalid")
	if result.Success {
		t.Fatal("fetchURL() should fail for invalid URL")
	}
}

func TestFetchURLSetsUserAgent(t *testing.T) {
	var capturedUA string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	})

	c := newTestCrawler()
	c.httpClient = testHTTPClientWith(handler)

	c.fetchURL(context.Background(), "https://example.com")
	if !strings.Contains(capturedUA, "MyCrawler") {
		t.Errorf("expected User-Agent containing MyCrawler, got %q", capturedUA)
	}
}
