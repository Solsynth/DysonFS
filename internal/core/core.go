// Package core holds the shared service graph handed to modules. It is built
// once by package app and never imports the module registry, which keeps the
// dependency direction one-way: app -> core + module, modules -> core + module.
//
// Optional capabilities are interfaces. A disabled module leaves its slot nil
// (or a no-op) and every consumer must degrade instead of assuming it exists.
package core

import (
	"context"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

// Identity is the account resolution surface. The auth module fills it from
// Stargate; a build without the auth module leaves Synthesized set and every
// request resolves to that single account with all permission checks skipped.
type Identity struct {
	// Synthesized is non-nil when no authentication provider is compiled in.
	// Middleware then attaches it to every request.
	Synthesized *SynthesizedIdentity
}

// SynthesizedIdentity describes the account every request belongs to when the
// auth module is absent.
type SynthesizedIdentity struct {
	ID          string
	Name        string
	Nick        string
	Superuser   bool
	SessionID   string
	WorkspaceID string
}

// Server is an auxiliary listener owned by a module.
type Server interface {
	Shutdown(context.Context) error
}

// WorkerHandle is the processing loop a derivative module registers against.
// It is satisfied by *worker.Worker without importing the worker package.
type WorkerHandle interface {
	ProcessImage(ctx context.Context, file *database.CloudFile, path string) error
	ProcessVideo(ctx context.Context, file *database.CloudFile, path string) error
}

// Services is the shared, already-constructed core graph.
type Services struct {
	Config *config.Config
	Mode   string
	DB     *database.DB
	Redis  *redis.Client
	Bus    *eventbus.Bus
	Files  *service.FileService
	Tasks  *service.TaskService
	Quota  *service.QuotaService
	Wopi   *service.WOPIService

	// Dispatcher is the in-process upload sink (bundled mode). Nil when uploads
	// are dispatched over NATS.
	Dispatcher interface {
		PublishFileUploaded(context.Context, eventbus.FileUploadedEvent) error
		PublishFileAction(context.Context, eventbus.FileActionEvent) error
	}
	// Publisher is Dispatcher when set, otherwise Bus. Upload handlers use it
	// so a bundled deployment never needs NATS.
	Publisher Publisher

	Identity Identity
	// Enabled is the resolved module set.
	Enabled map[string]bool

	// Analysis is nil unless the analysis module is enabled. Every method is
	// nil-safe and reports "not handled", so callers never branch on nil.
	Analysis *AnalysisRegistry
	// Derivatives is nil unless the derivative module is enabled.
	Derivatives *DerivativeRegistry
	// Worker is the processing loop owner. Nil in a master-only process that
	// dispatches uploads over NATS.
	Worker WorkerHandle

	// Servers are auxiliary listeners (S3 gateways, storage node) that the
	// process shuts down with the main HTTP server.
	Servers []Server
	Log     zerolog.Logger

	authenticator atomic.Pointer[gin.HandlerFunc]
}

// SetAuthenticator installs the request authentication middleware. The auth
// module calls it during Configure; a build without that module leaves the
// slot empty and every request resolves to the synthesized account.
func (s *Services) SetAuthenticator(fn gin.HandlerFunc) {
	if s == nil {
		return
	}
	if fn == nil {
		s.authenticator.Store(nil)
		return
	}
	s.authenticator.Store(&fn)
}

// Authenticator returns the installed middleware, or nil.
func (s *Services) Authenticator() gin.HandlerFunc {
	if s == nil {
		return nil
	}
	fn := s.authenticator.Load()
	if fn == nil {
		return nil
	}
	return *fn
}

// AuthDisabled reports whether requests resolve to the synthesized account.
func (s *Services) AuthDisabled() bool {
	return s == nil || s.Identity.Synthesized != nil
}
