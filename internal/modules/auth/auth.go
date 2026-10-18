// Package auth owns request authentication. It has two modes:
//
//   - stargate: tokens are validated against the Stargate gRPC auth service and
//     the resolved account/session are attached to the request.
//   - synthesized: no auth provider is compiled in, every request belongs to
//     one fixed account and every permission check is skipped.
//
// Both modes install a gin middleware on the host engine, and both expose the
// same context contract so handlers never branch on the mode.
package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	dyauth "src.solsynth.dev/sosys/go/pkg/auth"
	gen "src.solsynth.dev/sosys/go/proto"

	"src.solsynth.dev/sosys/filesystem/internal/core"
)

// Middleware authenticates every request.
//
// With a Stargate authenticator it behaves like the historical fail-open
// middleware: a token is resolved when present, and a request without a valid
// token continues unauthenticated so public file reads still work. Without an
// authenticator it attaches the synthesized identity to every request.
type Middleware struct {
	authenticator dyauth.TokenAuthenticator
	synthesized   *core.SynthesizedIdentity
}

// NewStargate builds the Stargate-backed middleware.
func NewStargate(authenticator dyauth.TokenAuthenticator) *Middleware {
	return &Middleware{authenticator: authenticator}
}

// NewSynthesized builds the auth-less middleware.
func NewSynthesized(identity *core.SynthesizedIdentity) *Middleware {
	return &Middleware{synthesized: identity}
}

// Synthesized reports whether requests resolve to a fixed account.
func (m *Middleware) Synthesized() bool { return m != nil && m.synthesized != nil }

// Handler returns the gin middleware.
func (m *Middleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if m == nil || m.synthesized != nil {
			attachSynthesized(c, m.identity())
			c.Next()
			return
		}

		if isPublicAsset(c.Request) {
			c.Next()
			return
		}
		if isWOPICallback(c.Request) {
			c.Next()
			return
		}

		tokenInfo, ok := dyauth.ExtractToken(c.Request)
		if !ok {
			c.Next()
			return
		}
		result, err := dyauth.AuthenticateRequest(c.Request.Context(), m.authenticator, c.Request)
		if err != nil {
			// Fail open: the handler decides whether the route needs an
			// account. Public file reads keep working with a stale token.
			c.Next()
			return
		}
		dyauth.WithAuth(c, result, tokenInfo)
		c.Next()
	}
}

func (m *Middleware) identity() *core.SynthesizedIdentity {
	if m == nil || m.synthesized == nil {
		return &core.SynthesizedIdentity{Superuser: true}
	}
	return m.synthesized
}

// attachSynthesized stores an AuthResult for the fixed account so every
// handler's auth.GetAuth call succeeds.
func attachSynthesized(c *gin.Context, identity *core.SynthesizedIdentity) {
	accountID := strings.TrimSpace(identity.ID)
	if accountID == "" {
		accountID = uuid.Nil.String()
	}
	sessionID := strings.TrimSpace(identity.SessionID)
	if sessionID == "" {
		sessionID = uuid.Nil.String()
	}
	now := timestamppb.Now()
	result := &dyauth.AuthResult{
		Account: &gen.DyAccount{
			Id:          accountID,
			Name:        identity.Name,
			Nick:        identity.Nick,
			IsSuperuser: identity.Superuser,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Session: &gen.DyAuthSession{
			Id:            sessionID,
			AccountId:     accountID,
			LastGrantedAt: now,
		},
	}
	dyauth.WithAuth(c, result, dyauth.TokenInfo{Type: dyauth.TokenTypeAuthKey})
}

// isPublicAsset reports whether a request may skip authentication entirely: the
// built console and its assets.
func isPublicAsset(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return !IsAPIPath(r.URL.Path)
}

// IsAPIPath reports whether the path belongs to an API surface.
func IsAPIPath(upath string) bool {
	upath = strings.TrimSuffix(upath, "/")
	for _, prefix := range []string{"/api", "/wopi", "/webdav", "/_dfs", "/swagger"} {
		if upath == prefix || strings.HasPrefix(upath, prefix+"/") {
			return true
		}
	}
	return false
}

// isWOPICallback reports whether the request carries a WOPI access token, which
// the WOPI handler validates itself.
func isWOPICallback(r *http.Request) bool {
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

// cloneProto is a helper kept for callers that need an independent copy of an
// authenticated account (for example to enrich it without mutating the shared
// result).
func cloneProto[T proto.Message](value T) T {
	if any(value) == nil {
		return value
	}
	return proto.Clone(value).(T)
}
