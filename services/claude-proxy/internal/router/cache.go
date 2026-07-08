package router

import (
	"os"
	"strconv"
	"time"

	"claude-proxy/internal/metrics"

	"github.com/hashicorp/golang-lru/v2/expirable"
)

// ttlCache wraps expirable.LRU to provide a string->string map with per-entry TTL expiry and strict LRU eviction.
type ttlCache struct {
	lru *expirable.LRU[string, string]
}

// ClearCache clears all items from the routing decision cache.
func ClearCache() {
	decisionCache.lru.Purge()
}

// newTTLCache creates a ttlCache with the given TTL and capacity limit.
func newTTLCache(ttl, evictInterval time.Duration) *ttlCache {
	maxEntries := 10000
	if v := os.Getenv("ROUTER_CACHE_MAX_ENTRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxEntries = n
		}
	}
	onEvict := func(key string, value string) {
		metrics.CacheEvictions.Inc()
	}
	lru := expirable.NewLRU[string, string](maxEntries, onEvict, ttl)
	return &ttlCache{lru: lru}
}

// Load returns the cached value for key if it exists and has not expired.
func (c *ttlCache) Load(key string) (string, bool) {
	return c.lru.Get(key)
}

// Store adds or refreshes a key with a new TTL. If size limit is exceeded, least recently used entries are evicted.
func (c *ttlCache) Store(key, value string) {
	c.lru.Add(key, value)
}

// Delete removes a key from the cache.
func (c *ttlCache) Delete(key string) {
	c.lru.Remove(key)
}

// Len returns the number of active entries currently in the cache.
func (c *ttlCache) Len() int {
	return c.lru.Len()
}

// evict is kept for compatibility with tests.
func (c *ttlCache) evict() {
	// expirable.LRU handles TTL evictions automatically, so no-op is perfectly fine here.
}

// defaultCacheTTL is the default routing decision cache TTL.
// Override via ROUTER_CACHE_TTL_MINUTES env var.
const defaultCacheTTLMinutes = 30

func initDecisionCache() *ttlCache {
	ttlMinutes := defaultCacheTTLMinutes
	if v := os.Getenv("ROUTER_CACHE_TTL_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ttlMinutes = n
		}
	}
	ttl := time.Duration(ttlMinutes) * time.Minute
	return newTTLCache(ttl, 5*time.Minute)
}

// decisionCache caches LLM routing decisions (model+prompt → target model).
// Only LLM-confirmed decisions are stored; keyword/fallback results are not cached
// so that subsequent requests can retry the LLM after a transient failure.
var decisionCache = initDecisionCache()
