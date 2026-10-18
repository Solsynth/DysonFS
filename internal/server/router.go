// Package server hosts the HTTP engine and the mount points modules register
// against. It owns nothing but the middleware chain, health, the API docs and
// the built console; every feature route belongs to a module.
package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	docs "src.solsynth.dev/sosys/filesystem/docs"
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/webui"

	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// Server hosts the HTTP API.
type Server struct {
	Engine *gin.Engine
	cfg    *config.Config
	api    *gin.RouterGroup
	// WebError reports why the SPA was not mounted (missing dist directory or
	// no index.html). The server stays API-only in that case.
	WebError error
}

// New builds the gin engine with the core middleware chain and the host-level
// endpoints. Module routes are mounted afterwards through Host().
//
// The auth middleware is deliberately not installed here: the auth module owns
// it, so a build without that module has no authentication middleware at all.
func New(cfg *config.Config, mode string, files *service.FileService, middleware ...gin.HandlerFunc) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(recordServerFailures(files))
	engine.Use(gin.Recovery())
	// Authentication is installed here, before any module registers a route:
	// gin only applies middleware to routes created after Use.
	for _, fn := range middleware {
		if fn != nil {
			engine.Use(fn)
		}
	}

	s := &Server{Engine: engine, cfg: cfg}

	if cfg != nil && strings.TrimSpace(cfg.Web.StaticDir) != "" {
		console, err := webui.New(cfg.Web.StaticDir)
		if err != nil {
			s.WebError = err
		} else {
			engine.NoRoute(func(c *gin.Context) {
				// Module routes own every API path; the console only serves
				// non-API GETs so an unknown API path keeps returning JSON.
				if isAPIPath(c.Request.URL.Path) {
					c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
					return
				}
				console.ServeHTTP(c.Writer, c.Request)
			})
		}
	}

	docs.SwaggerInfo.BasePath = "/"
	docs.SwaggerInfo.Schemes = []string{"http"}
	engine.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))
	engine.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true, "mode": mode}) })
	engine.GET("/alive", func(c *gin.Context) { c.Status(http.StatusOK) })

	s.api = engine.Group("/api")

	log.Info().Str("mode", mode).Msg("http host ready")
	return s
}

// ModuleInfo is one entry in GET /api/modules.
type ModuleInfo struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// ModulesResponse is the resolved module set plus its capabilities: the
// runtime answer to "what is compiled and enabled in this build".
type ModulesResponse struct {
	Modules      []ModuleInfo `json:"modules"`
	Capabilities []string     `json:"capabilities"`
}

// MountModules advertises the resolved module set. The app calls it after
// resolution so the endpoint reflects the enabled set, not the compiled set.
func (s *Server) MountModules(response ModulesResponse) {
	if s == nil || s.Engine == nil {
		return
	}
	s.Engine.GET("/api/modules", func(c *gin.Context) { c.JSON(http.StatusOK, response) })
}

// Host returns the gin engine modules mount their routes on.
func (s *Server) Host() *gin.Engine { return s.Engine }

// API returns the shared /api router group.
func (s *Server) API() *gin.RouterGroup { return s.api }

// isAPIPath reports whether a path belongs to an API surface.
func isAPIPath(upath string) bool {
	upath = strings.TrimSuffix(upath, "/")
	for _, prefix := range []string{"/api", "/wopi", "/webdav", "/_dfs", "/swagger"} {
		if upath == prefix || strings.HasPrefix(upath, prefix+"/") {
			return true
		}
	}
	return false
}