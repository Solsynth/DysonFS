package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/go/pkg/auth"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type registerStorageNodeRequest struct {
	Name     string `json:"name" binding:"required"`
	MachineID string `json:"machine_id" binding:"required"`
	Endpoint string `json:"endpoint" binding:"required"`
	AuthToken string `json:"auth_token" binding:"required"`
	PoolID   *string `json:"pool_id"`
}

type updateStorageNodeRequest struct {
	Name      *string `json:"name"`
	Endpoint  *string `json:"endpoint"`
	AuthToken *string `json:"auth_token"`
	Status    *string `json:"status"`
	PoolID    *string `json:"pool_id"`
}

func registerStorageNode(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req registerStorageNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now()
	node := database.StorageNode{
		Name:       strings.TrimSpace(req.Name),
		MachineID:  strings.TrimSpace(req.MachineID),
		Endpoint:   strings.TrimSpace(req.Endpoint),
		AuthToken:  strings.TrimSpace(req.AuthToken),
		Status:     "online",
		LastSeenAt: &now,
		PoolID:     req.PoolID,
		AccountID:  uuid.MustParse(result.Account.GetId()),
	}

	if err := files.DB().Create(&node).Error; err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "storage node with this machine_id may already exist: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, node)
}

func listStorageNodes(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var nodes []database.StorageNode
	query := files.DB().Where("account_id = ?", result.Account.GetId())
	if err := query.Order("created_at desc").Find(&nodes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("X-Total", strconv.Itoa(len(nodes)))
	c.JSON(http.StatusOK, nodes)
}

func getStorageNode(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var node database.StorageNode
	if err := files.DB().Where("id = ? AND account_id = ?", c.Param("id"), result.Account.GetId()).First(&node).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "storage node not found"})
		return
	}

	c.JSON(http.StatusOK, node)
}

func updateStorageNode(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	nodeID := c.Param("id")
	var existing database.StorageNode
	if err := files.DB().Where("id = ? AND account_id = ?", nodeID, result.Account.GetId()).First(&existing).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "storage node not found"})
		return
	}

	var req updateStorageNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Endpoint != nil {
		updates["endpoint"] = strings.TrimSpace(*req.Endpoint)
	}
	if req.AuthToken != nil {
		updates["auth_token"] = strings.TrimSpace(*req.AuthToken)
	}
	if req.Status != nil {
		updates["status"] = strings.TrimSpace(*req.Status)
	}
	if req.PoolID != nil {
		updates["pool_id"] = req.PoolID
	}

	if len(updates) > 0 {
		if err := files.DB().Model(&database.StorageNode{}).Where("id = ?", nodeID).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	if err := files.DB().Where("id = ?", nodeID).First(&existing).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, existing)
}

func deleteStorageNode(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	nodeID := c.Param("id")
	var node database.StorageNode
	if err := files.DB().Where("id = ? AND account_id = ?", nodeID, result.Account.GetId()).First(&node).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "storage node not found"})
		return
	}

	if err := files.DB().Delete(&node).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func storageNodeHeartbeat(c *gin.Context, files *service.FileService) {
	machineID := c.Param("machineId")
	now := time.Now()
	result := files.DB().Model(&database.StorageNode{}).Where("machine_id = ?", machineID).Updates(map[string]any{
		"last_seen_at": now,
		"status":       "online",
	})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "storage node not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "last_seen_at": now})
}
