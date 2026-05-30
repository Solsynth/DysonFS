package server

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	docs "src.solsynth.dev/sosys/filesystem/docs"
	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
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

			if isWebDAVRequest(c.Request) {
				if accountID, ok := checkWebDAVToken(c.Request, files); ok {
					c.Set(handler.WebDAVAccountIDKey, accountID)
					c.Next()
					return
				}
				convertWebDAVBasicAuth(c.Request)
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

func isWebDAVRequest(r *http.Request) bool {
	return r != nil && r.URL != nil && strings.HasPrefix(r.URL.Path, "/webdav/")
}

func convertWebDAVBasicAuth(r *http.Request) {
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if authz == "" {
		return
	}
	if strings.HasPrefix(strings.ToLower(authz), "basic ") {
		decoded, err := base64.StdEncoding.DecodeString(authz[6:])
		if err != nil {
			return
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) == 2 {
			r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(parts[1]))
		}
	}
}

func checkWebDAVToken(r *http.Request, files *service.FileService) (string, bool) {
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if authz == "" || !strings.HasPrefix(strings.ToLower(authz), "basic ") {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(authz[6:])
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", false
	}
	rawToken := strings.TrimSpace(parts[1])
	if rawToken == "" {
		return "", false
	}

	hash := sha256.Sum256([]byte(rawToken))
	hashHex := fmt.Sprintf("%x", hash)

	var token database.WebDAVToken
	if err := files.DB().Where("token_hash = ?", hashHex).First(&token).Error; err != nil {
		return "", false
	}

	now := time.Now()
	_ = files.DB().Model(&token).Update("last_used_at", &now)

	return token.AccountID.String(), true
}
