package server

import (
	"net/http"
	"strings"

	docs "src.solsynth.dev/sosys/filesystem/docs"
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/dispatch"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/handler"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	dyauth "src.solsynth.dev/sosys/go/pkg/auth"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func NewRouter(cfg *config.Config, mode string, files *service.FileService, wopi *service.WOPIService, tasks *service.TaskService, quota *service.QuotaService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	if cfg.Auth.Target != "" {
		log.Info().Str("target", cfg.Auth.Target).Bool("useTLS", cfg.Auth.UseTLS).Msg("auth client enabled")
		authenticator, err := dyauth.NewGrpcTokenAuthenticator(dyauth.GrpcAuthDialConfig{Target: cfg.Auth.Target, UseTLS: cfg.Auth.UseTLS, TLSSkipVerify: cfg.Auth.TLSSkipVerify})
		if err != nil {
			log.Fatal().Err(err).Msg("failed to init authenticator")
		}
		r.Use(func(c *gin.Context) {
			if isWOPICallbackRequest(c.Request) {
				c.Next()
				return
			}

			tokenInfo, ok := dyauth.ExtractToken(c.Request)
			if ok {
				log.Debug().
					Str("method", c.Request.Method).
					Str("path", c.Request.URL.Path).
					Str("tokenType", string(tokenInfo.Type)).
					Msg("auth token extracted")
			} else {
				log.Debug().
					Str("method", c.Request.Method).
					Str("path", c.Request.URL.Path).
					Msg("no auth token extracted")
			}

			result, err := dyauth.AuthenticateRequest(c.Request.Context(), authenticator, c.Request)
			if err != nil {
				if ok {
					log.Warn().
						Err(err).
						Str("method", c.Request.Method).
						Str("path", c.Request.URL.Path).
						Str("tokenType", string(tokenInfo.Type)).
						Msg("auth token present but request was not authenticated")
				}
				c.Next()
				return
			}

			dyauth.WithAuth(c, result, tokenInfo)
			log.Debug().
				Str("method", c.Request.Method).
				Str("path", c.Request.URL.Path).
				Str("accountId", result.Account.GetId()).
				Str("sessionId", result.Session.GetId()).
				Str("tokenType", string(tokenInfo.Type)).
				Msg("request authenticated")
			_ = dyauth.HydrateAndTouch(c.Request.Context(), nil, nil, result)
			c.Next()
		})
	}

	docs.SwaggerInfo.BasePath = "/"
	docs.SwaggerInfo.Schemes = []string{"http"}
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))

	handler.RegisterRoutes(r, cfg, files, wopi, tasks, quota, bus, dispatcher)
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true, "mode": mode}) })
	return r
}

func isWOPICallbackRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if !strings.HasPrefix(r.URL.Path, "/wopi/") {
		return false
	}
	if strings.TrimSpace(r.URL.Query().Get("access_token")) != "" {
		return true
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	return strings.HasPrefix(strings.ToLower(authz), "bearer ")
}
