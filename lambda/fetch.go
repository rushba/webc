package main

import (
	"context"
	"io"
	"lambda/internal/ssrf"
	"net/http"
	"time"
)

// FetchResult contains the result of fetching a URL
type FetchResult struct {
	Success       bool
	StatusCode    int
	ContentLength int64
	ContentType   string
	DurationMs    int64
	Error         string
	Body          []byte // For HTML pages, contains the body for link extraction
}

func (c *Crawler) fetchURL(ctx context.Context, targetURL string) FetchResult {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, http.NoBody)
	if err != nil {
		return FetchResult{
			Success:    false,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      "invalid request: " + err.Error(),
		}
	}

	// SSRF protection: block requests to private/internal IPs
	if err := ssrf.ValidateHost(req.URL.Host); err != nil {
		return FetchResult{
			Success:    false,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      "SSRF blocked: " + err.Error(),
		}
	}

	req.Header.Set("User-Agent", "MyCrawler/1.0 (learning project)")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return FetchResult{
			Success:    false,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      err.Error(),
		}
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return FetchResult{
			Success:     false,
			StatusCode:  resp.StatusCode,
			ContentType: resp.Header.Get("Content-Type"),
			DurationMs:  time.Since(start).Milliseconds(),
			Error:       "read error: " + err.Error(),
		}
	}

	success := resp.StatusCode >= 200 && resp.StatusCode < 400
	contentType := resp.Header.Get("Content-Type")

	return FetchResult{
		Success:       success,
		StatusCode:    resp.StatusCode,
		ContentLength: int64(len(body)),
		ContentType:   contentType,
		DurationMs:    time.Since(start).Milliseconds(),
		Error:         "",
		Body:          body,
	}
}

// isPermanentHTTPError returns true for HTTP status codes that will never succeed on retry.
func isPermanentHTTPError(statusCode int) bool {
	switch statusCode {
	case 400, 401, 403, 404, 405, 410, 414, 451:
		return true
	default:
		return false
	}
}
