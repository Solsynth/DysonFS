package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/go/pkg/auth"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const storageNodeHealthyWithin = 2 * time.Minute

type adminStorageConfigResponse struct {
	ID                  string                    `json:"id"`
	Name                string                    `json:"name"`
	Description         string                    `json:"description"`
	StorageConfig       service.PoolStorageConfig `json:"storage_config"`
	SecretIDConfigured  bool                      `json:"secret_id_configured"`
	SecretKeyConfigured bool                      `json:"secret_key_configured"`
	BillingConfig       service.PoolBillingConfig `json:"billing_config"`
	PolicyConfig        service.PoolConfig        `json:"policy_config"`
	IsHidden            bool                      `json:"is_hidden"`
}

type adminStorageNodeStatus struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	MachineID  string     `json:"machine_id"`
	Endpoint   string     `json:"endpoint"`
	PoolID     *string    `json:"pool_id"`
	Status     string     `json:"status"`
	Healthy    bool       `json:"healthy"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

type adminStoragePoolStats struct {
	PoolID    string `json:"pool_id"`
	FileCount int64  `json:"file_count"`
	UsedBytes int64  `json:"used_bytes"`
}

type poolMigrationRequest struct {
	SourcePoolID string   `json:"source_pool_id" binding:"required"`
	TargetPoolID string   `json:"target_pool_id" binding:"required"`
	FileIDs      []string `json:"file_ids"`
}

func requireStorageAdminPermission(c *gin.Context, files *service.FileService) bool {
	result, _, ok := auth.GetAuth(c)
	if !ok || result == nil || result.Account == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return false
	}
	if result.Account.GetIsSuperuser() {
		return true
	}
	if err := files.RequireAccountPermission(c.Request.Context(), result.Account.GetId(), service.PermissionFilesManage); err != nil {
		if errors.Is(err, service.ErrPermissionDenied) {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return false
	}
	return true
}

func createPoolMigration(c *gin.Context, files *service.FileService, tasks *service.TaskService) {
	if !requireStorageAdminPermission(c, files) {
		return
	}
	if tasks == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "task service unavailable"})
		return
	}
	var req poolMigrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.SourcePoolID == req.TargetPoolID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source and target pools must be different"})
		return
	}
	if _, err := files.GetPool(req.SourcePoolID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source pool not found"})
		return
	}
	if _, err := files.GetPool(req.TargetPoolID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "target pool not found"})
		return
	}
	result, _, _ := auth.GetAuth(c)
	task, err := tasks.CreatePoolMigrationTask(uuid.MustParse(result.Account.GetId()), req.SourcePoolID, req.TargetPoolID, req.FileIDs)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, task)
}

func getPoolMigration(c *gin.Context, files *service.FileService, tasks *service.TaskService) {
	if !requireStorageAdminPermission(c, files) {
		return
	}
	if tasks == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "task service unavailable"})
		return
	}
	task, err := tasks.GetTask(c.Param("taskId"))
	if err != nil || task.Type != service.PoolMigrationTaskType {
		c.JSON(http.StatusNotFound, gin.H{"error": "pool migration task not found"})
		return
	}
	c.JSON(http.StatusOK, task)
}

func getAdminStorageConfig(c *gin.Context, files *service.FileService) {
	if !requireStorageAdminPermission(c, files) {
		return
	}
	pools, err := files.ListAllPools()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	configs := make([]adminStorageConfigResponse, 0, len(pools))
	for _, pool := range pools {
		configs = append(configs, redactAdminStorageConfig(&pool))
	}
	c.JSON(http.StatusOK, configs)
}

func updateAdminStorageConfig(c *gin.Context, files *service.FileService) {
	if !requireStorageAdminPermission(c, files) {
		return
	}
	var req struct {
		StorageConfig service.PoolStorageConfig `json:"storage_config" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	current, err := files.GetPool(c.Param("poolId"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "storage pool not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.StorageConfig.SecretId) == "" {
		req.StorageConfig.SecretId = current.StorageConfig.SecretId
	}
	if strings.TrimSpace(req.StorageConfig.SecretKey) == "" {
		req.StorageConfig.SecretKey = current.StorageConfig.SecretKey
	}
	if err := files.ValidateStorageConfig(req.StorageConfig); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid storage config: " + err.Error()})
		return
	}
	pool, err := files.UpdatePool(c.Param("poolId"), nil, nil, &req.StorageConfig, nil, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, redactAdminStorageConfig(pool))
}

func getAdminStorageStatus(c *gin.Context, files *service.FileService) {
	if !requireStorageAdminPermission(c, files) {
		return
	}
	var nodes []database.StorageNode
	if err := files.DB().Order("created_at desc").Find(&nodes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	now := time.Now()
	items := make([]adminStorageNodeStatus, 0, len(nodes))
	for _, node := range nodes {
		items = append(items, storageNodeStatus(node, now))
	}
	c.JSON(http.StatusOK, gin.H{"checked_at": now, "nodes": items})
}

func getAdminStorageHealth(c *gin.Context, files *service.FileService) {
	if !requireStorageAdminPermission(c, files) {
		return
	}
	var nodes []database.StorageNode
	if err := files.DB().Find(&nodes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	now := time.Now()
	healthy := 0
	for _, node := range nodes {
		if storageNodeStatus(node, now).Healthy {
			healthy++
		}
	}
	status := "healthy"
	if len(nodes) == 0 || healthy == 0 {
		status = "unhealthy"
	} else if healthy < len(nodes) {
		status = "degraded"
	}
	c.JSON(http.StatusOK, gin.H{
		"status":        status,
		"checked_at":    now,
		"total_nodes":   len(nodes),
		"healthy_nodes": healthy,
	})
}

func getAdminStorageStats(c *gin.Context, files *service.FileService) {
	if !requireStorageAdminPermission(c, files) {
		return
	}
	type poolStatsRow struct {
		PoolID    *string
		FileCount int64
		UsedBytes int64
	}
	var rows []poolStatsRow
	if err := files.DB().Table("cloud_files").
		Select("cloud_files.pool_id AS pool_id, COUNT(cloud_files.id) AS file_count, COALESCE(SUM(file_objects.size), 0) AS used_bytes").
		Joins("LEFT JOIN file_objects ON file_objects.id = cloud_files.object_id AND file_objects.deleted_at IS NULL").
		Where("cloud_files.deleted_at IS NULL").
		Group("cloud_files.pool_id").
		Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := make([]adminStoragePoolStats, 0, len(rows))
	for _, row := range rows {
		poolID := "default"
		if row.PoolID != nil && strings.TrimSpace(*row.PoolID) != "" {
			poolID = *row.PoolID
		}
		items = append(items, adminStoragePoolStats{PoolID: poolID, FileCount: row.FileCount, UsedBytes: row.UsedBytes})
	}
	c.JSON(http.StatusOK, gin.H{"calculated_at": time.Now(), "pools": items})
}

func getAdminStorageFailures(c *gin.Context, files *service.FileService) {
	if !requireStorageAdminPermission(c, files) {
		return
	}
	limit := 100
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > service.MaxServerFailureEvents {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 100"})
			return
		}
		limit = parsed
	}
	c.JSON(http.StatusOK, files.FailureLog().Snapshot(limit))
}

func redactAdminStorageConfig(pool *service.Pool) adminStorageConfigResponse {
	storageConfig := pool.StorageConfig
	secretIDConfigured := strings.TrimSpace(storageConfig.SecretId) != ""
	secretKeyConfigured := strings.TrimSpace(storageConfig.SecretKey) != ""
	storageConfig.SecretId = ""
	storageConfig.SecretKey = ""
	return adminStorageConfigResponse{
		ID:                  pool.ID,
		Name:                pool.Name,
		Description:         pool.Description,
		StorageConfig:       storageConfig,
		SecretIDConfigured:  secretIDConfigured,
		SecretKeyConfigured: secretKeyConfigured,
		BillingConfig:       pool.BillingConfig,
		PolicyConfig:        pool.PolicyConfig,
		IsHidden:            pool.IsHidden,
	}
}

func storageNodeStatus(node database.StorageNode, now time.Time) adminStorageNodeStatus {
	healthy := node.Status == "online" && node.LastSeenAt != nil && now.Sub(*node.LastSeenAt) <= storageNodeHealthyWithin
	return adminStorageNodeStatus{
		ID: node.ID, Name: node.Name, MachineID: node.MachineID, Endpoint: node.Endpoint,
		PoolID: node.PoolID, Status: node.Status, Healthy: healthy, LastSeenAt: node.LastSeenAt,
	}
}
