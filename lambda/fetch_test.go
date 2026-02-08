package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		// Private ranges
		{"loopback v4", "127.0.0.1", true},
		{"loopback v6", "::1", true},
		{"10.x.x.x", "10.0.0.1", true},
		{"172.16.x.x", "172.16.0.1", true},
		{"172.31.x.x", "172.31.255.255", true},
		{"192.168.x.x", "192.168.1.1", true},
		{"link-local", "169.254.169.254", true},
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v6", "::", true},

		// Public ranges
		{"public 8.8.8.8", "8.8.8.8", false},
		{"public 1.1.1.1", "1.1.1.1", false},
		{"public 93.x", "93.184.216.34", false},
		{"172.15.x.x (not private)", "172.15.255.255", false},
		{"172.32.x.x (not private)", "172.32.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP: %s", tt.ip)
			}
			got := isPrivateIP(ip)
			if got != tt.want {
				t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

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

func TestValidateHost(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		// Literal private IPs should be blocked
		{"blocks localhost", "127.0.0.1", true},
		{"blocks metadata endpoint", "169.254.169.254", true},
		{"blocks 10.x", "10.0.0.1", true},
		{"blocks 192.168.x", "192.168.1.1", true},
		{"blocks 172.16.x", "172.16.0.1", true},
		{"blocks 0.0.0.0", "0.0.0.0", true},
		{"blocks IPv6 loopback", "::1", true},

		// Literal private IPs with port
		{"blocks localhost with port", "127.0.0.1:8080", true},
		{"blocks metadata with port", "169.254.169.254:80", true},

		// Public IPs should pass
		{"allows 8.8.8.8", "8.8.8.8", false},
		{"allows 1.1.1.1", "1.1.1.1", false},

		// Real hostnames (resolve to public IPs)
		{"allows google.com", "google.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHost(tt.host)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateHost(%q) error = %v, wantErr %v", tt.host, err, tt.wantErr)
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
