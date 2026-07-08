package router

import (
	"testing"
	"time"
)

func TestTTLCache_MaxEntriesEviction(t *testing.T) {
	t.Setenv("ROUTER_CACHE_MAX_ENTRIES", "3")
	c := newTTLCache(time.Minute, time.Minute)

	c.Store("key1", "val1")
	c.Store("key2", "val2")
	c.Store("key3", "val3")

	if c.Len() != 3 {
		t.Fatalf("expected len 3, got %d", c.Len())
	}

	// Storing 4th entry should evict one entry
	c.Store("key4", "val4")

	if c.Len() != 3 {
		t.Fatalf("expected len 3 after eviction, got %d", c.Len())
	}

	// Verify that key4 is present and at least one of key1, key2, key3 is gone
	if _, ok := c.Load("key4"); !ok {
		t.Fatalf("expected key4 to be present")
	}

	missing := 0
	for _, k := range []string{"key1", "key2", "key3"} {
		if _, ok := c.Load(k); !ok {
			missing++
		}
	}
	if missing != 1 {
		t.Fatalf("expected exactly 1 missing entry from the original 3, got %d", missing)
	}
}

func TestTTLCache_TTLExpiry(t *testing.T) {
	// Cache with 10ms TTL
	c := newTTLCache(10*time.Millisecond, time.Minute)
	c.Store("key1", "val1")

	val, ok := c.Load("key1")
	if !ok || val != "val1" {
		t.Fatalf("expected val1, got %q (ok=%v)", val, ok)
	}

	// Wait for expiry
	time.Sleep(20 * time.Millisecond)

	_, ok = c.Load("key1")
	if ok {
		t.Fatalf("expected key1 to be expired")
	}
}

func TestTTLCache_Delete(t *testing.T) {
	c := newTTLCache(time.Minute, time.Minute)
	c.Store("key1", "val1")
	if _, ok := c.Load("key1"); !ok {
		t.Fatalf("expected key1 to be stored")
	}
	c.Delete("key1")
	if _, ok := c.Load("key1"); ok {
		t.Fatalf("expected key1 to be deleted")
	}
}

func TestTTLCache_Evict(t *testing.T) {
	// No-op method check
	c := newTTLCache(time.Minute, time.Minute)
	c.evict()
}

func TestTTLCache_StoreExistingKey(t *testing.T) {
	c := newTTLCache(time.Minute, time.Minute)
	c.Store("key1", "val1")
	c.Store("key1", "val2")
	val, _ := c.Load("key1")
	if val != "val2" {
		t.Errorf("expected val2, got %q", val)
	}
}

func TestTTLCache_LRUEviction(t *testing.T) {
	t.Setenv("ROUTER_CACHE_MAX_ENTRIES", "3")
	c := newTTLCache(time.Minute, time.Minute)

	c.Store("key1", "val1")
	c.Store("key2", "val2")
	c.Store("key3", "val3")

	// Access key1 to make it most recently used
	c.Load("key1")

	// Store key4 (capacity is 3, so one must be evicted. Since key1 was accessed,
	// and key2 is the oldest unaccessed, key2 should be evicted!).
	c.Store("key4", "val4")

	if _, ok := c.Load("key2"); ok {
		t.Fatalf("expected key2 (least recently used) to be evicted")
	}
	if _, ok := c.Load("key1"); !ok {
		t.Fatalf("expected key1 to be kept because it was accessed")
	}
	if _, ok := c.Load("key3"); !ok {
		t.Fatalf("expected key3 to be kept")
	}
	if _, ok := c.Load("key4"); !ok {
		t.Fatalf("expected key4 to be kept")
	}
}
