package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
)

func hashURL(u string) string {
	h := sha256.Sum256([]byte(u))
	return hex.EncodeToString(h[:])
}

// getDomain extracts the domain (scheme + host) from a URL
func getDomain(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// getHost extracts just the host from a URL (without scheme)
func getHost(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	return parsed.Host
}
