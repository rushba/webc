package main

import (
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
