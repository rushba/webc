package urls

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

func Hash(u string) string {
	h := sha256.Sum256([]byte(u))
	return hex.EncodeToString(h[:])
}

// GetDomain extracts the domain (scheme + host) from a URL
func GetDomain(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// GetHost extracts just the host from a URL (without scheme)
func GetHost(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// normalizeURL converts a potentially relative URL to an absolute URL
// Returns empty string for URLs we don't want to crawl
func Normalize(href string, baseURL *url.URL) string {
	href = strings.TrimSpace(href)

	// Skip empty, fragments, javascript, mailto, tel, etc.
	if href == "" ||
		strings.HasPrefix(href, "#") ||
		strings.HasPrefix(href, "javascript:") ||
		strings.HasPrefix(href, "mailto:") ||
		strings.HasPrefix(href, "tel:") ||
		strings.HasPrefix(href, "data:") {
		return ""
	}

	// Parse the href
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}

	// Resolve relative URLs against base
	resolved := baseURL.ResolveReference(parsed)

	// Only keep http/https
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}

	// Remove fragment
	resolved.Fragment = ""

	// Note: Same-domain filter removed - domain allowlist checked in enqueueLinks()

	return resolved.String()
}
