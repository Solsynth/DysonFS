package server

import (
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
	"github.com/oklog/ulid/v2"
	"github.com/rs/zerolog/log"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"golang.org/x/crypto/bcrypt"
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
				if accountID, ok := authenticateWebDAV(c.Request, files); ok {
					c.Set(handler.WebDAVAccountIDKey, accountID)
				}
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

func isWebDAVRequest(r *http.Request) bool {
	return r != nil && r.URL != nil && strings.HasPrefix(r.URL.Path, "/webdav/")
}

// authenticateWebDAV handles WebDAV authentication independently from the global
// dyauth middleware. It checks Basic Auth credentials against the web_dav_tokens
// table first. If that fails and the password looks like a JWT/API key, it falls
// back to dyauth. Returns the account ID if authenticated.
func authenticateWebDAV(r *http.Request, files *service.FileService) (string, bool) {
	if r == nil {
		return "", false
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		return "", false
	}

	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if password == "" {
		return "", false
	}

	if isLikelyWebDAVTokenID(username) {
		if accountID, ok := authenticateWebDAVTokenCredentials(files, username, password); ok {
			return accountID, true
		}
	}

	if tokenID, secret, ok := parseEmbeddedWebDAVToken(password); ok {
		if accountID, ok := authenticateWebDAVTokenCredentials(files, tokenID, secret); ok {
			return accountID, true
		}
	}

	if accountID, ok := authenticateWebDAVTokenSecret(files, password); ok {
		return accountID, true
	}

	log.Warn().Str("username", username).Msg("webdav: token authentication failed")
	return "", false
}

func isLikelyWebDAVTokenID(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	_, err := ulid.ParseStrict(value)
	return err == nil
}

func parseEmbeddedWebDAVToken(password string) (string, string, bool) {
	parts := strings.SplitN(password, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	tokenID := strings.TrimSpace(parts[0])
	secret := strings.TrimSpace(parts[1])
	if tokenID == "" || secret == "" {
		return "", "", false
	}
	return tokenID, secret, true
}

func authenticateWebDAVTokenCredentials(files *service.FileService, tokenID, secret string) (string, bool) {
	var token database.WebDAVToken
	if err := files.DB().Where("id = ?", tokenID).First(&token).Error; err != nil {
		return "", false
	}

	if err := bcrypt.CompareHashAndPassword([]byte(token.TokenHash), []byte(secret)); err != nil {
		return "", false
	}

	markWebDAVTokenUsed(files, &token)
	return token.AccountID.String(), true
}

func authenticateWebDAVTokenSecret(files *service.FileService, secret string) (string, bool) {
	var tokens []database.WebDAVToken
	if err := files.DB().Find(&tokens).Error; err != nil {
		return "", false
	}

	for i := range tokens {
		token := &tokens[i]
		if err := bcrypt.CompareHashAndPassword([]byte(token.TokenHash), []byte(secret)); err != nil {
			continue
		}

		markWebDAVTokenUsed(files, token)
		return token.AccountID.String(), true
	}
	return "", false
}

func markWebDAVTokenUsed(files *service.FileService, token *database.WebDAVToken) {
	if token == nil {
		return
	}

	now := time.Now()
	_ = files.DB().Model(token).Update("last_used_at", &now)
	log.Info().
		Str("accountId", token.AccountID.String()).
		Str("tokenId", token.ID).
		Msg("webdav: authenticated via token")
}
