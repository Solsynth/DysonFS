package server

import (
	"net/http"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/handler"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	dyauth "src.solsynth.dev/sosys/go/pkg/auth"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

func NewRouter(cfg *config.Config, files *service.FileService, tasks *service.TaskService, bus *eventbus.Bus) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(cors.New(cors.Config{AllowAllOrigins: true, AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, AllowHeaders: []string{"Origin", "Content-Type", "Authorization", "X-Forwarded-Authorization", "X-Original-Authorization"}, ExposeHeaders: []string{"X-Total"}}))

	if cfg.Auth.Target != "" {
		authenticator, err := dyauth.NewGrpcTokenAuthenticator(dyauth.GrpcAuthDialConfig{Target: cfg.Auth.Target, UseTLS: cfg.Auth.UseTLS})
		if err != nil {
			log.Fatal().Err(err).Msg("failed to init authenticator")
		}
		r.Use(dyauth.OptionalAuthMiddleware(authenticator))
	}

	handler.RegisterRoutes(r, cfg, files, tasks, bus)
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true, "mode": "master"}) })
	return r
}
