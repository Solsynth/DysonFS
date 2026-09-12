package media

import (
	"bytes"
	"container/list"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

// cacheStore is the derivative cache tier contract. Implementations must never
// let read/write errors escape: a cache failure degrades to a fresh render.
// contentType is the derivative's MIME type; tiers that store opaque bytes
// (memory/disk) ignore it, the s3 tier stores it as object metadata.
type cacheStore interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, data []byte, contentType string) error
}

// noopCache disables server-side caching. It is the default when no cache kind
// is configured; HTTP caching headers still apply.
type noopCache struct{}

func (noopCache) Get(ctx context.Context, key string) ([]byte, bool, error) { return nil, false, nil }
func (noopCache) Put(ctx context.Context, key string, data []byte, contentType string) error {
	return nil
}

// memoryCache is an in-process LRU bounded by maxBytes, with optional idle TTL.
// Eviction is inline: Put evicts least-recently-used entries beyond the byte
// budget; Get drops entries idle past the TTL. No background sweeper is needed
// (entries never referenced are reclaimed under byte pressure).
type memoryCache struct {
	mu       sync.Mutex
	maxBytes int64
	ttl      time.Duration
	entries  map[string]*list.Element // key -> element (value *memEntry)
	lru      *list.List
	size     int64
}

type memEntry struct {
	key     string
	data    []byte
	modTime time.Time
}

func newMemoryCache(maxBytes int64, ttl time.Duration) *memoryCache {
	return &memoryCache{
		maxBytes: maxBytes,
		ttl:      ttl,
		entries:  make(map[string]*list.Element),
		lru:      list.New(),
	}
}

func (c *memoryCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return nil, false, nil
	}
	e := el.Value.(*memEntry)
	if c.ttl > 0 && time.Since(e.modTime) > c.ttl {
		c.removeElement(el)
		return nil, false, nil
	}
	e.modTime = time.Now()
	c.lru.MoveToFront(el)
	return e.data, true, nil
}

func (c *memoryCache) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if int64(len(data)) > c.maxBytes {
		return nil // an entry larger than the whole budget would evict everything
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		e := el.Value.(*memEntry)
		c.size -= int64(len(e.data))
		e.data = data
		e.modTime = time.Now()
		c.lru.MoveToFront(el)
	} else {
		c.entries[key] = c.lru.PushFront(&memEntry{key: key, data: data, modTime: time.Now()})
	}
	c.size += int64(len(data))
	for c.size > c.maxBytes {
		if last := c.lru.Back(); last != nil {
			c.removeElement(last)
		} else {
			break
		}
	}
	return nil
}

func (c *memoryCache) removeElement(el *list.Element) {
	e := el.Value.(*memEntry)
	c.lru.Remove(el)
	delete(c.entries, e.key)
	c.size -= int64(len(e.data))
}

// diskCache is a disk LRU: cache keys map to sharded paths, reads touch the
// file (recency), and a periodic sweep evicts idle entries beyond the TTL and
// the oldest entries beyond the byte budget.
type diskCache struct {
	dir      string
	maxBytes int64
	ttl      time.Duration
}

func (c *diskCache) path(key string) string {
	return filepath.Join(c.dir, key[:2], key[2:4], key)
}

func (c *diskCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	path := c.path(key)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		logging.Log.Error().Err(err).Str("key", key).Msg("media cache read failed")
		return nil, false, nil
	}
	if int64(len(data)) > c.maxBytes {
		return nil, false, nil
	}
	// Touch for LRU ordering; failures are not worth surfacing.
	_ = os.Chtimes(path, time.Now(), time.Now())
	return data, true, nil
}

func (c *diskCache) Put(ctx context.Context, key string, data []byte, contentType string) error {
	path := c.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic within the same filesystem
}

// sweep runs the periodic eviction loop until ctx is cancelled.
func (c *diskCache) sweep(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sweepOnce()
		}
	}
}

// sweepOnce evicts entries idle beyond the TTL (when ttl > 0), then, if the
// cache still exceeds the byte budget, the oldest entries (by last access)
// until it fits. With ttl = 0 there is no time-based expiry; cleanup is purely
// budget-driven.
func (c *diskCache) sweepOnce() {
	var cutoff time.Time
	if c.ttl > 0 {
		cutoff = time.Now().Add(-c.ttl)
	}
	type entry struct {
		path string
		mod  time.Time
		size int64
	}
	var entries []entry
	var total int64
	deleted := 0

	_ = filepath.WalkDir(c.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // skip unreadable entries and dirs
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if c.ttl > 0 && info.ModTime().Before(cutoff) {
			if os.Remove(path) == nil {
				deleted++
			}
			return nil
		}
		entries = append(entries, entry{path: path, mod: info.ModTime(), size: info.Size()})
		total += info.Size()
		return nil
	})

	if total > c.maxBytes {
		sort.Slice(entries, func(i, j int) bool { return entries[i].mod.Before(entries[j].mod) })
		for _, e := range entries {
			if total <= c.maxBytes {
				break
			}
			if os.Remove(e.path) == nil {
				deleted++
				total -= e.size
			}
		}
	}

	if deleted > 0 {
		logging.Log.Info().Int("deleted", deleted).Str("dir", c.dir).Msg("media cache sweep evicted entries")
	}
}

// s3Cache stores derivatives in object storage under a fixed prefix in a
// dedicated file pool's bucket (media.cache.poolId). It has no eviction: the
// operator manages bucket lifecycle rules. The media-cache/ prefix keeps the
// cache keys distinct from any user data in the same bucket.
type s3Cache struct {
	backend  storage.Backend
	prefix   string
	maxBytes int64
}

func (c *s3Cache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	rc, info, err := c.backend.Get(ctx, c.prefix+key)
	if err != nil {
		return nil, false, nil
	}
	defer rc.Close()
	if info.Size > c.maxBytes {
		return nil, false, nil
	}
	out, err := io.ReadAll(io.LimitReader(rc, c.maxBytes+1))
	if err != nil {
		logging.Log.Error().Err(err).Str("key", key).Msg("media s3 cache read failed")
		return nil, false, nil
	}
	if int64(len(out)) > c.maxBytes {
		return nil, false, nil
	}
	return out, true, nil
}

func (c *s3Cache) Put(ctx context.Context, key string, data []byte, contentType string) error {
	return c.backend.Put(ctx, c.prefix+key, bytes.NewReader(data), int64(len(data)), contentType)
}
