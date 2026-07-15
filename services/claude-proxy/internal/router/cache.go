package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"time"

	"claude-proxy/internal/logger"
	"claude-proxy/internal/metrics"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/redis/go-redis/v9"
)

// DecisionCache defines the cache interface for model routing decisions.
type DecisionCache interface {
	Load(key string) (string, bool)
	Store(key, value string)
	Delete(key string)
	Len() int
	Clear()
}

// ttlCache wraps expirable.LRU for in-memory cache.
type ttlCache struct {
	lru *expirable.LRU[string, string]
}

// ClearCache clears all items from the routing decision cache.
func ClearCache() {
	decisionCache.Clear()
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

func (c *ttlCache) Load(key string) (string, bool) {
	return c.lru.Get(key)
}

func (c *ttlCache) Store(key, value string) {
	c.lru.Add(key, value)
}

func (c *ttlCache) Delete(key string) {
	c.lru.Remove(key)
}

func (c *ttlCache) Len() int {
	return c.lru.Len()
}

func (c *ttlCache) Clear() {
	c.lru.Purge()
}

// evict is kept for compatibility with tests.
func (c *ttlCache) evict() {
	// expirable.LRU handles TTL evictions automatically, so no-op is perfectly fine here.
}

// redisCache implements DecisionCache using Redis client.
type redisCache struct {
	client *redis.Client
	ttl    time.Duration
}

func getRedisKey(key string) string {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) < 2 {
		return "claude-proxy:cache:" + key
	}
	h := sha256.Sum256([]byte(parts[1]))
	return "claude-proxy:cache:" + parts[0] + ":" + hex.EncodeToString(h[:])
}

func (r *redisCache) Load(key string) (string, bool) {
	rkey := getRedisKey(key)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	val, err := r.client.Get(ctx, rkey).Result()
	if err != nil {
		if err != redis.Nil {
			logger.Errorf("[Redis Cache] Error loading key %s: %v", key, err)
		}
		return "", false
	}
	return val, true
}

func (r *redisCache) Store(key, value string) {
	rkey := getRedisKey(key)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := r.client.Set(ctx, rkey, value, r.ttl).Err()
	if err != nil {
		logger.Errorf("[Redis Cache] Error storing key %s: %v", key, err)
	}
}

func (r *redisCache) Delete(key string) {
	rkey := getRedisKey(key)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := r.client.Del(ctx, rkey).Err()
	if err != nil {
		logger.Errorf("[Redis Cache] Error deleting key %s: %v", key, err)
	}
}

func (r *redisCache) Len() int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	keys, err := r.client.Keys(ctx, "claude-proxy:cache:*").Result()
	if err != nil {
		return 0
	}
	return len(keys)
}

func (r *redisCache) Clear() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cursor uint64
	for {
		var keys []string
		var err error
		keys, cursor, err = r.client.Scan(ctx, cursor, "claude-proxy:cache:*", 100).Result()
		if err != nil {
			logger.Errorf("[Redis Cache] Error clearing keys: %v", err)
			return
		}
		if len(keys) > 0 {
			if err := r.client.Del(ctx, keys...).Err(); err != nil {
				logger.Errorf("[Redis Cache] Error deleting cleared keys: %v", err)
			}
		}
		if cursor == 0 {
			break
		}
	}
}

// defaultCacheTTL is the default routing decision cache TTL.
const defaultCacheTTLMinutes = 30

func initDecisionCache() DecisionCache {
	ttlMinutes := defaultCacheTTLMinutes
	if v := os.Getenv("ROUTER_CACHE_TTL_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ttlMinutes = n
		}
	}
	ttl := time.Duration(ttlMinutes) * time.Minute

	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		opt, err := redis.ParseURL(redisURL)
		if err == nil {
			client := redis.NewClient(opt)
			// Ping Redis to ensure connection works, otherwise fallback to local cache
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := client.Ping(ctx).Err(); err == nil {
				logger.Infof("[Redis Cache] Connected to Redis successfully: %s", redisURL)
				return &redisCache{client: client, ttl: ttl}
			}
			logger.Errorf("[Redis Cache] Failed to ping Redis: %v. Falling back to local cache.", err)
		} else {
			logger.Errorf("[Redis Cache] Failed to parse REDIS_URL '%s': %v. Falling back to local cache.", redisURL, err)
		}
	}

	return newTTLCache(ttl, 5*time.Minute)
}

// decisionCache caches LLM routing decisions (model+prompt → target model).
var decisionCache = initDecisionCache()
