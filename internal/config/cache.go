package config

import (
	"errors"
	"os"
	"sync"
	"time"
)

// cacheEntry caches a parsed file along with its mtime at parse-time.
// On a get, callers re-stat the file and consider the cache stale if
// mtime advances.
type cacheEntry struct {
	mtime time.Time
	data  any // *Rules or *Policy
}

var (
	cacheMu sync.RWMutex
	cache   = map[string]*cacheEntry{}
)

// CachedLoadRules returns a cached parse of rules.md if its mtime
// hasn't changed; otherwise re-parses and updates the cache.
//
// Concurrent safe.
func CachedLoadRules(path string) (*Rules, error) {
	hit, err := getCached[*Rules](path)
	if err != nil {
		return nil, err
	}
	if hit != nil {
		return hit, nil
	}
	r, err := LoadRules(path)
	if err != nil {
		return nil, err
	}
	store(path, r.ModTime, r)
	return r, nil
}

// CachedLoadPolicy is the policy.yaml counterpart to CachedLoadRules.
func CachedLoadPolicy(path string) (*Policy, error) {
	hit, err := getCached[*Policy](path)
	if err != nil {
		return nil, err
	}
	if hit != nil {
		return hit, nil
	}
	p, err := LoadPolicy(path)
	if err != nil {
		return nil, err
	}
	store(path, p.ModTime, p)
	return p, nil
}

// ClearCache empties the cache. Mostly for tests.
func ClearCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for k := range cache {
		delete(cache, k)
	}
}

// getCached returns the cached value for path if its mtime is
// current. Returns (nil, nil) on cache miss / stale entry.
//
// Returns (nil, err) when the file can't be stat'd for reasons other
// than not-existing — the caller should propagate.
func getCached[T any](path string) (T, error) {
	var zero T
	info, err := os.Stat(path)
	if err != nil {
		// File missing — surface so callers can return os.ErrNotExist.
		if errors.Is(err, os.ErrNotExist) {
			return zero, err
		}
		return zero, err
	}
	cacheMu.RLock()
	entry, ok := cache[path]
	cacheMu.RUnlock()
	if !ok || !entry.mtime.Equal(info.ModTime()) {
		return zero, nil
	}
	v, ok := entry.data.(T)
	if !ok {
		// Type mismatch (shouldn't happen in normal use); ignore cache.
		return zero, nil
	}
	return v, nil
}

func store(path string, mtime time.Time, data any) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache[path] = &cacheEntry{mtime: mtime, data: data}
}
