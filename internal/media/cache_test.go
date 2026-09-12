package media

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMediaMemoryCacheGetPut(t *testing.T) {
	c := newMemoryCache(1024, 0)
	ctx := context.Background()
	if err := c.Put(ctx, "a", []byte("alpha")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	data, ok, err := c.Get(ctx, "a")
	if err != nil || !ok || string(data) != "alpha" {
		t.Fatalf("Get() = %q, %v, %v; want alpha, true, nil", data, ok, err)
	}
	if _, ok, _ := c.Get(ctx, "missing"); ok {
		t.Fatal("Get(missing) hit, want miss")
	}
}

func TestMediaMemoryCachePutReplacesExisting(t *testing.T) {
	c := newMemoryCache(1024, 0)
	ctx := context.Background()
	_ = c.Put(ctx, "a", []byte("one"))
	_ = c.Put(ctx, "a", []byte("two"))
	data, ok, _ := c.Get(ctx, "a")
	if !ok || string(data) != "two" {
		t.Fatalf("Get() = %q, %v; want two, true", data, ok)
	}
	if c.size != 3 {
		t.Fatalf("size = %d, want 3 (replaced entry must not be double-counted)", c.size)
	}
}

func TestMediaMemoryCacheByteBudgetEvictsLRU(t *testing.T) {
	c := newMemoryCache(100, 0)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := c.Put(ctx, string(rune('a'+i)), make([]byte, 40)); err != nil {
			t.Fatalf("Put(%d) error = %v", i, err)
		}
	}
	// 120 bytes in a 100-byte budget -> oldest ('a') evicted.
	if _, ok, _ := c.Get(ctx, "a"); ok {
		t.Fatal("oldest entry still present after budget eviction")
	}
	if _, ok, _ := c.Get(ctx, "b"); !ok {
		t.Fatal("second entry evicted unexpectedly")
	}
	if _, ok, _ := c.Get(ctx, "c"); !ok {
		t.Fatal("newest entry evicted unexpectedly")
	}
	if c.size > c.maxBytes {
		t.Fatalf("size = %d exceeds budget %d", c.size, c.maxBytes)
	}
}

func TestMediaMemoryCacheAccessRefreshesLRU(t *testing.T) {
	c := newMemoryCache(90, 0)
	ctx := context.Background()
	// Three 40-byte items exceed the 90-byte budget; access order decides eviction.
	_ = c.Put(ctx, "a", make([]byte, 40))
	_ = c.Put(ctx, "b", make([]byte, 40))
	if _, _, err := c.Get(ctx, "a"); err != nil {
		t.Fatalf("Get(a) error = %v", err)
	}
	_ = c.Put(ctx, "c", make([]byte, 40))
	if _, ok, _ := c.Get(ctx, "a"); !ok {
		t.Fatal("touched entry evicted, want LRU 'b' evicted")
	}
	if _, ok, _ := c.Get(ctx, "b"); ok {
		t.Fatal("LRU entry not evicted")
	}
	if _, ok, _ := c.Get(ctx, "c"); !ok {
		t.Fatal("newest entry evicted unexpectedly")
	}
}

func TestMediaMemoryCacheTTLExpiry(t *testing.T) {
	c := newMemoryCache(1024, 5*time.Millisecond)
	ctx := context.Background()
	if err := c.Put(ctx, "a", []byte("alpha")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if _, ok, _ := c.Get(ctx, "a"); !ok {
		t.Fatal("fresh entry missed")
	}
	time.Sleep(50 * time.Millisecond) // 10x TTL margin
	if _, ok, _ := c.Get(ctx, "a"); ok {
		t.Fatal("entry past TTL still present")
	}
}

func TestMediaMemoryCacheOversizeItemSkipped(t *testing.T) {
	c := newMemoryCache(10, 0)
	ctx := context.Background()
	if err := c.Put(ctx, "big", make([]byte, 100)); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if _, ok, _ := c.Get(ctx, "big"); ok {
		t.Fatal("oversize entry cached")
	}
	if c.size != 0 {
		t.Fatalf("size = %d, want 0", c.size)
	}
}

func TestMediaDiskCacheSweepNoTTLKeepsEntries(t *testing.T) {
	dir := t.TempDir()
	c := &diskCache{dir: dir, maxBytes: 1024, ttl: 0}
	ctx := context.Background()
	keyA := strings.Repeat("a", 64) // cache keys are sha256 hex
	keyB := strings.Repeat("b", 64)
	if err := c.Put(ctx, keyA, []byte("alpha")); err != nil {
		t.Fatalf("Put(a) error = %v", err)
	}
	if err := c.Put(ctx, keyB, []byte("beta")); err != nil {
		t.Fatalf("Put(b) error = %v", err)
	}
	c.sweepOnce()
	// ttl = 0 must disable time-based expiry entirely; nothing may be deleted.
	if _, ok, _ := c.Get(ctx, keyA); !ok {
		t.Fatal("entry 'a' evicted with ttl = 0")
	}
	if _, ok, _ := c.Get(ctx, keyB); !ok {
		t.Fatal("entry 'b' evicted with ttl = 0")
	}
}

func TestMediaDiskCacheSweepBudgetEvictsOldest(t *testing.T) {
	dir := t.TempDir()
	c := &diskCache{dir: dir, maxBytes: 10, ttl: 0}
	ctx := context.Background()
	bigKey := strings.Repeat("b", 64)
	smallKey := strings.Repeat("s", 64)
	if err := c.Put(ctx, bigKey, []byte("0123456789abcdef")); err != nil {
		t.Fatalf("Put(big) error = %v", err)
	}
	time.Sleep(2 * time.Millisecond) // distinct mtimes make eviction order deterministic
	if err := c.Put(ctx, smallKey, []byte("hi")); err != nil {
		t.Fatalf("Put(small) error = %v", err)
	}
	c.sweepOnce()
	// 16 + 2 bytes against a 10-byte budget: the oldest ('big') is evicted,
	// leaving 'small' within budget.
	if _, ok, _ := c.Get(ctx, bigKey); ok {
		t.Fatal("oldest entry still present after budget eviction")
	}
	if _, ok, _ := c.Get(ctx, smallKey); !ok {
		t.Fatal("newest entry evicted unexpectedly")
	}
}
