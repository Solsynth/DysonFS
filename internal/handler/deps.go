package handler

import (
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/core"
	"src.solsynth.dev/sosys/filesystem/internal/dispatch"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/service"

	"github.com/gin-gonic/gin"
)

// Deps is the handler dependency set. Every Register* function takes it, so a
// module mounts exactly the surface it owns and a disabled module never
// registers a route.
type Deps struct {
	Config *config.Config
	Mode   string
	Files  *service.FileService
	Wopi   *service.WOPIService
	Tasks  *service.TaskService
	Quota  *service.QuotaService

	Bus        *eventbus.Bus
	Dispatcher dispatch.Dispatcher

	// Analysis is nil when the analysis module is not compiled in or disabled.
	Analysis *core.AnalysisRegistry
	// Derivatives is nil when the derivative module is not compiled in or
	// disabled.
	Derivatives *core.DerivativeRegistry

	// Enabled maps module IDs to their resolved state.
	Enabled map[string]bool
}

// FromCore builds the dependency set from the shared graph.
func FromCore(services *core.Services) Deps {
	return Deps{
		Config:     services.Config,
		Mode:       services.Mode,
		Files:      services.Files,
		Wopi:       services.Wopi,
		Tasks:      services.Tasks,
		Quota:      services.Quota,
		Bus:        services.Bus,
		Dispatcher: services.Dispatcher,
		Analysis:    services.Analysis,
		Derivatives: services.Derivatives,
		Enabled:    services.Enabled,
	}
}

// registerBusStash keeps the historical "bus" context value for handlers that
// read it, and is a no-op when NATS is not configured.
func registerBusStash(r *gin.Engine, bus *eventbus.Bus) {
	if bus == nil {
		return
	}
	r.Use(func(c *gin.Context) {
		c.Set("bus", bus)
		c.Next()
	})
}
