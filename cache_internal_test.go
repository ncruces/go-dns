package dns

import (
	"testing"
	"time"
)

func TestCacheLen(t *testing.T) {
	c := NewCache()

	// A fresh cache is empty.
	if got := c.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0", got)
	}

	// Len counts only non-expired entries.
	now := time.Now()
	c.entries = map[string]cacheEntry{
		"live1":   {deadline: now.Add(time.Minute)},
		"live2":   {deadline: now.Add(time.Hour)},
		"expired": {deadline: now.Add(-time.Minute)},
	}
	if got := c.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2 (expired entries excluded)", got)
	}
}

func TestNewCacheDefaults(t *testing.T) {
	c := NewCache()
	if c.maxEntries != DefaultMaxCacheEntries {
		t.Errorf("maxEntries = %d, want %d", c.maxEntries, DefaultMaxCacheEntries)
	}
	if !c.negative {
		t.Error("negative = false, want true")
	}
}
