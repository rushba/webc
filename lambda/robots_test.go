package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/rs/zerolog"
	"github.com/temoto/robotstxt"
)

func TestEvictRobotsCacheIfFull(t *testing.T) {
	c := &Crawler{
		robotsCache: make(map[string]*robotstxt.RobotsData),
		log:         zerolog.Nop(),
	}

	// Fill cache to max
	for i := range maxRobotsCacheSize {
		domain := "https://domain" + string(rune('A'+i%26)) + string(rune('0'+i/26)) + ".com"
		c.robotsCache[domain] = nil
	}

	if len(c.robotsCache) != maxRobotsCacheSize {
		t.Fatalf("expected cache size %d, got %d", maxRobotsCacheSize, len(c.robotsCache))
	}

	// Evict should remove one entry
	c.evictRobotsCacheIfFull()
	if len(c.robotsCache) != maxRobotsCacheSize-1 {
		t.Fatalf("expected cache size %d after eviction, got %d", maxRobotsCacheSize-1, len(c.robotsCache))
	}
}

func TestEvictRobotsCacheDoesNothingWhenNotFull(t *testing.T) {
	c := &Crawler{
		robotsCache: make(map[string]*robotstxt.RobotsData),
		log:         zerolog.Nop(),
	}

	c.robotsCache["https://example.com"] = nil
	c.robotsCache["https://other.com"] = nil

	c.evictRobotsCacheIfFull()
	if len(c.robotsCache) != 2 {
		t.Fatalf("expected cache size 2, got %d", len(c.robotsCache))
	}
}

func TestRobotsCacheNeverExceedsMax(t *testing.T) {
	c := &Crawler{
		robotsCache: make(map[string]*robotstxt.RobotsData),
		log:         zerolog.Nop(),
	}

	// Simulate adding entries beyond max
	for i := range maxRobotsCacheSize + 100 {
		domain := "https://domain-" + string(rune(i))
		c.evictRobotsCacheIfFull()
		c.robotsCache[domain] = nil
	}

	if len(c.robotsCache) > maxRobotsCacheSize {
		t.Fatalf("cache size %d exceeds max %d", len(c.robotsCache), maxRobotsCacheSize)
	}
}

func TestGetRobotsFromCache(t *testing.T) {
	c := newTestCrawler()
	c.httpClient = testHTTPClient()

	// Pre-populate cache
	robotsData, _ := robotstxt.FromString("User-agent: *\nDisallow: /secret")
	c.robotsCache["https://example.com"] = robotsData

	got := c.getRobots(context.Background(), "https://example.com/page")
	if got == nil {
		t.Fatal("getRobots() returned nil, expected cached data")
	}
	if got.TestAgent("/secret", robotsUserAgent) {
		t.Error("expected /secret to be disallowed")
	}
}

func TestGetRobotsFetchesRemote(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = fmt.Fprint(w, "User-agent: *\nDisallow: /private")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	c := newTestCrawler()
	c.httpClient = testHTTPClientWith(handler)

	got := c.getRobots(context.Background(), "https://example.com/page")
	if got == nil {
		t.Fatal("getRobots() returned nil")
	}
	if got.TestAgent("/private", robotsUserAgent) {
		t.Error("expected /private to be disallowed")
	}

	// Should be cached now
	if _, ok := c.robotsCache["https://example.com"]; !ok {
		t.Error("expected robots data to be cached")
	}
}

func TestGetRobotsNotFound(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	c := newTestCrawler()
	c.httpClient = testHTTPClientWith(handler)

	got := c.getRobots(context.Background(), "https://example.com/page")
	if got != nil {
		t.Error("getRobots() expected nil for 404 robots.txt")
	}
}

func TestGetRobotsInvalidURL(t *testing.T) {
	c := newTestCrawler()
	c.httpClient = testHTTPClient()

	got := c.getRobots(context.Background(), "://invalid")
	if got != nil {
		t.Error("getRobots() expected nil for invalid URL")
	}
}

func TestGetRobotsSSRFProtection(t *testing.T) {
	c := newTestCrawler()
	c.httpClient = testHTTPClient()

	got := c.getRobots(context.Background(), "http://127.0.0.1/page")
	if got != nil {
		t.Error("getRobots() should block SSRF attempt")
	}
	// Should cache the nil result
	if _, ok := c.robotsCache["http://127.0.0.1"]; !ok {
		t.Error("expected failed SSRF attempt to be cached")
	}
}

func TestIsAllowedByRobotsNoRobotsFile(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	c := newTestCrawler()
	c.httpClient = testHTTPClientWith(handler)

	// No robots.txt = allow everything
	got := c.isAllowedByRobots(context.Background(), "https://example.com/page")
	if !got {
		t.Error("isAllowedByRobots() = false, want true (no robots.txt)")
	}
}

func TestIsAllowedByRobotsBlocked(t *testing.T) {
	c := newTestCrawler()
	c.httpClient = testHTTPClient()

	robotsData, _ := robotstxt.FromString("User-agent: *\nDisallow: /blocked")
	c.robotsCache["https://example.com"] = robotsData

	got := c.isAllowedByRobots(context.Background(), "https://example.com/blocked")
	if got {
		t.Error("isAllowedByRobots() = true, want false")
	}
}

func TestIsAllowedByRobotsAllowed(t *testing.T) {
	c := newTestCrawler()
	c.httpClient = testHTTPClient()

	robotsData, _ := robotstxt.FromString("User-agent: *\nDisallow: /blocked")
	c.robotsCache["https://example.com"] = robotsData

	got := c.isAllowedByRobots(context.Background(), "https://example.com/allowed")
	if !got {
		t.Error("isAllowedByRobots() = false, want true")
	}
}

func TestIsAllowedByRobotsInvalidURL(t *testing.T) {
	c := newTestCrawler()
	c.httpClient = testHTTPClient()

	// Invalid URL should be allowed (fail-open)
	got := c.isAllowedByRobots(context.Background(), "://invalid")
	if !got {
		t.Error("isAllowedByRobots() = false for invalid URL, want true (fail-open)")
	}
}
