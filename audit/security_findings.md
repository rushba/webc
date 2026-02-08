# Security Findings

## Finding 1: TOCTOU DNS Rebinding in SSRF Protection (CRITICAL)

**File**: `lambda/fetch.go`
**Severity**: Critical
**Status**: Fixed (commit a8aa6bb)

### Description
`validateHost()` resolved DNS and checked IPs, but `httpClient.Do()` resolved DNS again independently. This created a Time-of-Check-to-Time-of-Use (TOCTOU) window where an attacker could perform DNS rebinding:

1. Attacker's DNS returns 8.8.8.8 (public IP) for first resolution -> `validateHost` passes
2. Attacker's DNS returns 169.254.169.254 (AWS metadata) for second resolution -> `httpClient.Do` connects to metadata endpoint
3. Attacker exfiltrates IAM role credentials via SSRF

### Fix
Added `ssrfSafeTransport()` which creates an `http.Transport` with a custom `net.Dialer.Control` function. The Control function validates the resolved IP at TCP connection time (after DNS resolution but before the connection is established), eliminating the TOCTOU window entirely.

`validateHost()` is retained as defense-in-depth for early rejection of obvious private IPs without the overhead of a full connection attempt.

### Tests
- `TestSSRFSafeTransportBlocksPrivateIPs` - blocks loopback, link-local, 10.x, 192.168.x, 172.16.x
- `TestSSRFSafeTransportBlocksLocalhostServer` - blocks real loopback server
- `TestSSRFDialerControlFunction` - validates control function behavior

---

## Finding 2: Unbounded Robots Cache (MEDIUM)

**File**: `lambda/robots.go`, `lambda/main.go`
**Severity**: Medium
**Status**: Fixed (commit 31fc118)

### Description
`robotsCache` was a plain `map[string]*robotstxt.RobotsData` with no size limit. When crawling thousands of domains, this map would grow without bound, eventually causing Out-of-Memory (OOM) in the Lambda function (which has only 128MB memory).

### Fix
Added `maxRobotsCacheSize` constant (1000 entries) and `evictRobotsCacheIfFull()` which removes a random entry when the cache reaches capacity. Random eviction via Go map iteration is O(1) and simple.

### Tests
- `TestEvictRobotsCacheIfFull` - verifies eviction removes one entry at max size
- `TestEvictRobotsCacheDoesNothingWhenNotFull` - verifies no-op below threshold
- `TestRobotsCacheNeverExceedsMax` - stress test with 1100 insertions

---

## Audit Notes (No Fix Required)

### SSRF protection on robots.txt fetches
`getRobots()` already calls `validateHost()` before fetching robots.txt, and shares the same `httpClient` with the SSRF-safe transport. No additional fix needed.

### HTTP client redirect handling
The HTTP client uses `CheckRedirect: func(...) { return http.ErrUseLastResponse }` which prevents following redirects. This is safe from open redirect attacks but means redirect responses (3xx) are treated as successful fetches. This is a correctness concern, not a security one.

### Input validation on SQS message body
URL from SQS message body is used directly. SQS enforces a 256KB message size limit, and the URL is validated by `http.NewRequestWithContext` and `validateHost`. No additional validation needed.
