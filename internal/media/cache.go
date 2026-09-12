package media

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

// cacheStore is the derivative cache tier contract. Implementations must never
// let read/write errors escape: a cache failure degrades to a fresh render.
type cacheStore interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, data []byte) error
}

// noopCache disables server-side caching. It is the default when no cache kind
// is configured; HTTP caching headers still apply.
type noopCache struct{}

func (noopCache) Get(ctx context.Context, key string) ([]byte, bool, error) { return nil, false, nil }
func (noopCache) Put(ctx context.Context, key string, data []byte) error    { return nil }

// localCache is a disk LRU: cache keys map to sharded paths, reads touch the
// file (recency), and a periodic sweep evicts idle entries beyond the TTL and
// the oldest entries beyond the byte budget.
type localCache struct {
	dir      string
	maxBytes int64
	ttl      time.Duration
}

func (c *localCache) path(key string) string {
	return filepath.Join(c.dir, key[:2], key[2:4], key)
}

func (c *localCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
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

func (c *localCache) Put(ctx context.Context, key string, data []byte) error {
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
func (c *localCache) sweep(ctx context.Context) {
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

// sweepOnce evicts entries idle beyond the TTL, then, if the cache still
// exceeds the byte budget, the oldest entries (by last access) until it fits.
func (c *localCache) sweepOnce() {
	now := time.Now()
	cutoff := now.Add(-c.ttl)
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
		if info.ModTime().Before(cutoff) {
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

// storageCache stores derivatives in object storage under a fixed prefix,
// sharing the default pool backend. It has no eviction: the operator manages
// bucket lifecycle rules. This keeps user S3 listings clean since the cache
// prefix never overlaps per-file pool buckets.
type storageCache struct {
	backend  storage.Backend
	prefix   string
	maxBytes int64
}

func (c *storageCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
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
		logging.Log.Error().Err(err).Str("key", key).Msg("media storage cache read failed")
		return nil, false, nil
	}
	if int64(len(out)) > c.maxBytes {
		return nil, false, nil
	}
	return out, true, nil
}

func (c *storageCache) Put(ctx context.Context, key string, data []byte) error {
	return c.backend.Put(ctx, c.prefix+key, bytes.NewReader(data), int64(len(data)), "application/octet-stream")
}
