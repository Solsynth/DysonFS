package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"src.solsynth.dev/sosys/go/pkg/handoff"
)

// RegisterHandoffRoutes mounts the picker hand-off surface. The frame posts
// the code to this drive's own origin, so the exchange is same-origin for the
// caller and the client secret stays here.
func RegisterHandoffRoutes(r *gin.Engine, d Deps) {
	cfg := d.Config.Handoff
	client := &handoff.TokenClient{
		BaseURL:      strings.TrimSpace(cfg.Issuer),
		ClientID:     strings.TrimSpace(cfg.ClientID),
		ClientSecret: cfg.ClientSecret,
	}
	h := r.Group("/api/picker/handoff")
	{
		h.POST("/redeem", func(c *gin.Context) { redeemPickerHandoff(c, client) })
	}
}

// redeemPickerHandoff trades a single-use hand-off code for a token pair.
//
// This route is deliberately unauthenticated. The code is the credential: it
// is single-use, short-lived, and bound to this drive's registered client, so
// only this deployment can redeem it. A session cookie cannot gate it either
// — a cross-site frame never receives the drive's SameSite=Lax cookie, which
// is the whole reason the hand-off exists. The response carries the token in
// the body and sets no cookie, for the same reason.
//
// The client secret is only ever sent server-to-server to the issuer; it must
// never appear in a response body, an error, or a log line.
func redeemPickerHandoff(c *gin.Context, client *handoff.TokenClient) {
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Code) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code is required"})
		return
	}

	res, err := client.Redeem(c.Request.Context(), req.Code)
	if err != nil {
		// A refused redemption means the code was expired, consumed, or
		// issued to another client. The issuer's raw error text describes
		// its internals, so the anonymous caller gets one generic line
		// instead; the details stay in the server log, which is written
		// from the error code alone and never carries the secret.
		log.Warn().Str("code", handoffErrorCode(err)).Msg("picker hand-off redemption refused")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "the hand-off code was refused"})
		return
	}

	// No cookie is set: the frame is cross-site by construction and cannot
	// receive one, so the token pair goes back in the body for the frame to
	// hold in memory.
	c.JSON(http.StatusOK, gin.H{
		"token":              res.Token,
		"refresh_token":      res.RefreshToken,
		"expires_in":         res.ExpiresIn,
		"refresh_expires_in": res.RefreshExpiresIn,
	})
}

// handoffErrorCode extracts the protocol error code for logging. It returns
// only the OAuth-style code (e.g. "invalid_grant"), never the description,
// so issuer text and the client secret stay out of the logs.
func handoffErrorCode(err error) string {
	var hErr *handoff.Error
	if errors.As(err, &hErr) {
		return hErr.Code
	}
	return "unreachable"
}
