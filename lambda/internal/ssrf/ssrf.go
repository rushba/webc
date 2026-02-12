package ssrf

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// IsPrivateIP checks if an IP is loopback, private, or link-local
func IsPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// ValidateHost resolves a hostname and checks that none of its IPs are private/internal.
// Blocks SSRF attempts targeting AWS metadata (169.254.169.254), localhost, or internal networks.
// Note: This provides early rejection only. The SSRF-safe dialer (ssrfSafeDialer) provides
// defense-in-depth against DNS rebinding by validating IPs at connection time.
func ValidateHost(hostname string) error {
	host, _, err := net.SplitHostPort(hostname)
	if err != nil {
		host = hostname // no port
	}

	// Check literal IP addresses
	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateIP(ip) {
			return fmt.Errorf("blocked: private IP %s", ip)
		}
		return nil
	}

	// Resolve hostname and check all results
	addrs, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("DNS lookup failed for %s: %w", host, err)
	}

	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil && IsPrivateIP(ip) {
			return fmt.Errorf("blocked: %s resolves to private IP %s", host, ip)
		}
	}

	return nil
}

// NewTransport returns an http.Transport with a Control function on the dialer
// that checks the resolved IP at connection time, preventing DNS rebinding attacks.
// This is defense-in-depth: validateHost provides early rejection, and this transport
// ensures the actual TCP connection never reaches a private IP even if DNS changes
// between the validateHost call and the connection.
func NewTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control: func(network, address string, c syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return fmt.Errorf("SSRF dialer: invalid address %s: %w", address, err)
				}
				ip := net.ParseIP(host)
				if ip != nil && IsPrivateIP(ip) {
					return fmt.Errorf("SSRF dialer: blocked connection to private IP %s", ip)
				}
				return nil
			},
		}).DialContext,
	}
}
