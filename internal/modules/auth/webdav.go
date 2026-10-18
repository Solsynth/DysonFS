package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

// WebDAVAccountIDKey is the gin context key carrying a WebDAV-token account.
const WebDAVAccountIDKey = "webdav_account_id"

// WebDAVHandler authenticates WebDAV requests. Basic-auth credentials are
// checked against the web_dav_tokens table; when no token matches the request
// continues unauthenticated so the caller can still see a synthesized identity.
//
// A deployment without the auth module never reaches this: WebDAV then relies
// on the synthesized account.
func WebDAVHandler(files *service.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if accountID, ok := AuthenticateWebDAV(c.Request, files); ok {
			c.Set(WebDAVAccountIDKey, accountID)
		}
		c.Next()
	}
}

// AuthenticateWebDAV resolves a WebDAV Basic-auth credential to an account ID.
func AuthenticateWebDAV(r *http.Request, files *service.FileService) (string, bool) {
	if r == nil || files == nil {
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
		if accountID, ok := authenticateTokenCredentials(files, username, password); ok {
			return accountID, true
		}
	}
	if tokenID, secret, ok := parseEmbeddedToken(password); ok {
		if accountID, ok := authenticateTokenCredentials(files, tokenID, secret); ok {
			return accountID, true
		}
	}
	if accountID, ok := authenticateTokenSecret(files, password); ok {
		return accountID, true
	}

	log.Warn().Str("username", username).Msg("webdav: token authentication failed")
	return "", false
}

func authenticateTokenCredentials(files *service.FileService, tokenID, secret string) (string, bool) {
	var token database.WebDAVToken
	if err := files.DB().Where("id = ?", tokenID).First(&token).Error; err != nil {
		return "", false
	}
	if err := bcrypt.CompareHashAndPassword([]byte(token.TokenHash), []byte(secret)); err != nil {
		return "", false
	}
	markTokenUsed(files, &token)
	return token.AccountID.String(), true
}

func authenticateTokenSecret(files *service.FileService, secret string) (string, bool) {
	var tokens []database.WebDAVToken
	if err := files.DB().Find(&tokens).Error; err != nil {
		return "", false
	}
	for i := range tokens {
		token := &tokens[i]
		if err := bcrypt.CompareHashAndPassword([]byte(token.TokenHash), []byte(secret)); err != nil {
			continue
		}
		markTokenUsed(files, token)
		return token.AccountID.String(), true
	}
	return "", false
}

func markTokenUsed(files *service.FileService, token *database.WebDAVToken) {
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

func isLikelyWebDAVTokenID(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	_, err := ulid.ParseStrict(value)
	return err == nil
}

func parseEmbeddedToken(password string) (string, string, bool) {
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

// ParseAccountID extracts the account ID from an auth result.
func ParseAccountID(result interface{ GetId() string }) string {
	if result == nil {
		return ""
	}
	return strings.TrimSpace(result.GetId())
}

// NilAccountID is the all-zero UUID. Handlers treat it as "no account".
func NilAccountID() string { return uuid.Nil.String() }
