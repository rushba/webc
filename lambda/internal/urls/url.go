package urls

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
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
