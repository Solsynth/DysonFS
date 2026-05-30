package handler

import (
	"net/http"
	"strconv"
	"strings"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/go/pkg/auth"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type createS3TokenRequest struct {
	Label  string  `json:"label" binding:"required"`
	PoolID *string `json:"pool_id"`
}

func createS3Token(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req createS3TokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Label) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "label is required"})
		return
	}

	if req.PoolID != nil && strings.TrimSpace(*req.PoolID) != "" {
		if _, err := files.GetPool(strings.TrimSpace(*req.PoolID)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "pool not found: " + err.Error()})
			return
		}
	}

	accessKeyRaw := database.NewID() + database.NewID()
	secretKeyRaw := database.NewID() + database.NewID()

	accountUUID := uuid.MustParse(result.Account.GetId())
	token := database.S3Token{
		AccountID: accountUUID,
		AccessKey: accessKeyRaw,
		SecretKey: secretKeyRaw,
		Label:     strings.TrimSpace(req.Label),
		PoolID:    req.PoolID,
	}
	if err := files.DB().Create(&token).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":         token.ID,
		"label":      token.Label,
		"pool_id":    token.PoolID,
		"access_key": accessKeyRaw,
		"secret_key": secretKeyRaw,
		"created_at": token.CreatedAt,
	})
}

func listS3Tokens(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var tokens []database.S3Token
	if err := files.DB().Where("account_id = ?", result.Account.GetId()).Order("created_at desc").Find(&tokens).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("X-Total", strconv.Itoa(len(tokens)))
	c.JSON(http.StatusOK, tokens)
}

func deleteS3Token(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	tokenID := c.Param("id")
	var token database.S3Token
	if err := files.DB().Where("id = ? AND account_id = ?", tokenID, result.Account.GetId()).First(&token).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "token not found"})
		return
	}

	if err := files.DB().Delete(&token).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}
