package media

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

// SourceResolver is the subset of *service.FileService the media renderer
// needs. It lets the media package depend on service types only through this
// interface (no import cycle: media imports config, database, storage).
type SourceResolver interface {
	BackendForFile(file *database.CloudFile) (storage.Backend, error)
	GetFile(id string) (*database.CloudFile, error)
	ResolveStorageKey(file *database.CloudFile) string
}

// Result is the outcome of a render: the transformed bytes plus the metadata
// needed to serve them with correct HTTP caching semantics.
type Result struct {
	Bytes       []byte
	ContentType string
	Ext         string // "jpg" | "png" | "webp" | "avif"
	ETag        string // hex CacheKey
	ModTime     time.Time
	FromCache   bool
}

// Service renders on-the-fly image transforms with bounded concurrency and an
// optional cache tier.
type Service struct {
	cfg     config.MediaConfig
	backend storage.Backend // storage backend for the s3 cache tier (resolved from cache.poolId by the app)
	cache   cacheStore      // noopCache unless a cache kind is configured
	sem     chan struct{}   // nil when the concurrency limit is <= 1
}

// New validates the media config and builds the cache store and concurrency
// limiter. Failures here are configuration errors and surface at startup.
func New(cfg config.MediaConfig, backend storage.Backend) (*Service, error) {
	if len(cfg.AllowedFormats) == 0 {
		cfg.AllowedFormats = []string{"jpeg", "png", "webp", "avif"}
	}
	if cfg.QualityMin < 1 || cfg.QualityMax > 100 || cfg.QualityMin > cfg.QualityMax {
		return nil, fmt.Errorf("invalid media quality range %d..%d (must satisfy 1 <= min <= max <= 100)", cfg.QualityMin, cfg.QualityMax)
	}

	var store cacheStore = noopCache{}
	switch cfg.Cache.Kind {
	case "", "none":
		// No server-side caching; HTTP headers still apply.
	case "memory":
		store = newMemoryCache(cfg.Cache.MaxBytes, cfg.Cache.TTL)
	case "disk":
		if strings.TrimSpace(cfg.Cache.Dir) == "" {
			return nil, errors.New(`media.cache.dir is required when cache.kind = "disk"`)
		}
		store = &diskCache{dir: cfg.Cache.Dir, maxBytes: cfg.Cache.MaxBytes, ttl: cfg.Cache.TTL}
	case "s3":
		if strings.TrimSpace(cfg.Cache.PoolID) == "" {
			return nil, errors.New(`media.cache.poolId is required when cache.kind = "s3"`)
		}
		if backend == nil {
			return nil, errors.New(`media cache kind "s3" requires a storage backend`)
		}
		store = &s3Cache{backend: backend, prefix: "media-cache/", maxBytes: cfg.Cache.MaxBytes}
	default:
		return nil, fmt.Errorf("unknown media cache kind %q", cfg.Cache.Kind)
	}

	limit := cfg.MaxConcurrent
	if limit <= 0 {
		limit = runtime.NumCPU()
	}
	var sem chan struct{}
	if limit > 1 {
		sem = make(chan struct{}, limit)
	}
	return &Service{cfg: cfg, backend: backend, cache: store, sem: sem}, nil
}

// Enabled reports whether the media transform feature is on. A nil receiver is
// treated as disabled so callers can check without guarding.
func (s *Service) Enabled() bool {
	return s != nil && s.cfg.Enable
}

// Start launches background maintenance (currently only the disk-cache
// sweeper). It is a no-op otherwise and returns immediately.
func (s *Service) Start(ctx context.Context) {
	if dc, ok := s.cache.(*diskCache); ok {
		go dc.sweep(ctx)
	}
}
