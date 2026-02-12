package main

import (
	"context"
	"io"
	"lambda/internal/ssrf"
	"net/http"
	"net/url"

	"github.com/temoto/robotstxt"
)

// getRobots fetches and caches robots.txt for a domain
func (c *Crawler) getRobots(ctx context.Context, urlStr string) *robotstxt.RobotsData {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return nil
	}

	domain := parsed.Scheme + "://" + parsed.Host

	// Check cache first
	if robots, ok := c.robotsCache[domain]; ok {
		return robots
	}

	// Fetch robots.txt
	robotsURL := domain + "/robots.txt"

	// SSRF protection: block requests to private/internal IPs
	if err := ssrf.ValidateHost(parsed.Host); err != nil {
		c.log.Warn().Str("domain", domain).Err(err).Msg("SSRF blocked for robots.txt")
		c.robotsCache[domain] = nil
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, http.NoBody)
	if err != nil {
		c.robotsCache[domain] = nil // Cache the failure
		return nil
	}
	req.Header.Set("User-Agent", robotsUserAgent+"/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.log.Debug().Str("domain", domain).Err(err).Msg("Failed to fetch robots.txt")
		c.robotsCache[domain] = nil
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	// If not found or error, allow all
	if resp.StatusCode != http.StatusOK {
		c.log.Debug().Str("domain", domain).Int("status", resp.StatusCode).Msg("robots.txt not found, allowing all")
		c.robotsCache[domain] = nil
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRobotsTxtSize))
	if err != nil {
		c.robotsCache[domain] = nil
		return nil
	}

	robots, err := robotstxt.FromBytes(body)
	if err != nil {
		c.log.Warn().Str("domain", domain).Err(err).Msg("Failed to parse robots.txt")
		c.robotsCache[domain] = nil
		return nil
	}

	c.log.Info().Str("domain", domain).Msg("Loaded robots.txt")
	c.evictRobotsCacheIfFull()
	c.robotsCache[domain] = robots
	return robots
}

// evictRobotsCacheIfFull removes a random entry when the cache reaches max size.
// Using random eviction (Go map iteration order) keeps it simple and O(1).
func (c *Crawler) evictRobotsCacheIfFull() {
	if len(c.robotsCache) < maxRobotsCacheSize {
		return
	}
	// Delete one random entry (Go map iteration is randomized)
	for k := range c.robotsCache {
		delete(c.robotsCache, k)
		break
	}
}

// isAllowedByRobots checks if a URL is allowed by robots.txt
func (c *Crawler) isAllowedByRobots(ctx context.Context, urlStr string) bool {
	robots := c.getRobots(ctx, urlStr)
	if robots == nil {
		// No robots.txt or failed to fetch - allow by default
		return true
	}

	parsed, err := url.Parse(urlStr)
	if err != nil {
		return true
	}

	// Check if the path is allowed for our user agent
	return robots.TestAgent(parsed.Path, robotsUserAgent)
}
