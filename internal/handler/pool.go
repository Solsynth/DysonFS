package handler

import (
	"net/http"
	"strings"

	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/go/pkg/auth"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type createPoolRequest struct {
	Name          string                    `json:"name" binding:"required"`
	Description   string                    `json:"description"`
	StorageConfig service.PoolStorageConfig `json:"storage_config" binding:"required"`
	BillingConfig service.PoolBillingConfig `json:"billing_config"`
	PolicyConfig  service.PoolConfig        `json:"policy_config"`
	IsHidden      bool                      `json:"is_hidden"`
}

type updatePoolRequest struct {
	Name          *string                    `json:"name"`
	Description   *string                    `json:"description"`
	StorageConfig *service.PoolStorageConfig `json:"storage_config"`
	BillingConfig *service.PoolBillingConfig `json:"billing_config"`
	PolicyConfig  *service.PoolConfig        `json:"policy_config"`
	IsHidden      *bool                      `json:"is_hidden"`
}

func createPool(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req createPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := files.ValidateStorageConfig(req.StorageConfig); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid storage config: " + err.Error()})
		return
	}

	pool, err := files.CreatePool(
		uuid.MustParse(result.Account.GetId()),
		strings.TrimSpace(req.Name),
		req.Description,
		req.StorageConfig,
		req.BillingConfig,
		req.PolicyConfig,
		req.IsHidden,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, pool)
}

func getPool(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	ctx := service.AccessContext{}
	if ok {
		ctx.Account = result.Account
		ctx.Session = result.Session
	}

	pool, err := files.GetPool(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if !files.CanUsePool(ctx, pool, "read") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	c.JSON(http.StatusOK, pool)
}

func updatePool(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	poolID := c.Param("id")
	pool, err := files.GetPool(poolID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if !result.Account.GetIsSuperuser() && pool.AccountID.String() != result.Account.GetId() {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	var req updatePoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.StorageConfig != nil {
		if err := files.ValidateStorageConfig(*req.StorageConfig); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid storage config: " + err.Error()})
			return
		}
	}

	updated, err := files.UpdatePool(poolID, req.Name, req.Description, req.StorageConfig, req.BillingConfig, req.PolicyConfig, req.IsHidden)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, updated)
}

func deletePool(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	poolID := c.Param("id")
	pool, err := files.GetPool(poolID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if !result.Account.GetIsSuperuser() && pool.AccountID.String() != result.Account.GetId() {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	if err := files.DeletePool(poolID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}
