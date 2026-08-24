package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/go/pkg/auth"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type createNodePoolRequest struct {
	Name          string                    `json:"name" binding:"required"`
	Description   string                    `json:"description"`
	Bucket        string                    `json:"bucket" binding:"required"`
	AccessKey     string                    `json:"access_key" binding:"required"`
	SecretKey     string                    `json:"secret_key" binding:"required"`
	EnableSigned  bool                      `json:"enable_signed"`
	BillingConfig service.PoolBillingConfig `json:"billing_config"`
	PolicyConfig  service.PoolConfig        `json:"policy_config"`
	IsHidden      bool                      `json:"is_hidden"`
}

type createNodeRequest struct {
	Name      string                `json:"name" binding:"required"`
	MachineID string                `json:"machine_id" binding:"required"`
	Endpoint  string                `json:"endpoint" binding:"required"`
	AuthToken string                `json:"auth_token" binding:"required"`
	Pool      createNodePoolRequest `json:"pool" binding:"required"`
}

type nodeCreateResponse struct {
	Node   database.StorageNode `json:"node"`
	PoolID string               `json:"pool_id"`
}

type nodeIdentityResponse struct {
	MachineID string `json:"machine_id"`
	NodeType  string `json:"node_type"`
}

type nodeAuthValidationResponse struct {
	Valid     bool   `json:"valid"`
	MachineID string `json:"machine_id"`
}

func createNode(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok || result == nil || result.Account == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req createNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	accountID, err := uuid.Parse(result.Account.GetId())
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid account identity"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.MachineID = strings.TrimSpace(req.MachineID)
	req.Endpoint = strings.TrimSpace(req.Endpoint)
	req.AuthToken = strings.TrimSpace(req.AuthToken)
	req.Pool.Name = strings.TrimSpace(req.Pool.Name)
	req.Pool.Bucket = strings.TrimSpace(req.Pool.Bucket)
	req.Pool.AccessKey = strings.TrimSpace(req.Pool.AccessKey)
	req.Pool.SecretKey = strings.TrimSpace(req.Pool.SecretKey)
	if req.Name == "" || req.MachineID == "" || req.Endpoint == "" || req.AuthToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name, machine_id, endpoint, and auth_token are required"})
		return
	}
	if req.Pool.Name == "" || req.Pool.Bucket == "" || req.Pool.AccessKey == "" || req.Pool.SecretKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pool name, bucket, access_key, and secret_key are required"})
		return
	}

	nodeURL, err := validateNodeEndpoint(c.Request.Context(), req.Endpoint, req.MachineID, req.AuthToken)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "storage node validation failed: " + err.Error()})
		return
	}

	storageConfig := service.PoolStorageConfig{
		Endpoint:     nodeURL.Host,
		Bucket:       req.Pool.Bucket,
		EnableSsl:    nodeURL.Scheme == "https",
		EnableSigned: req.Pool.EnableSigned,
		SecretId:     req.Pool.AccessKey,
		SecretKey:    req.Pool.SecretKey,
	}
	if err := files.ValidateStorageConfig(storageConfig); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid node storage config: " + err.Error()})
		return
	}

	storageJSON, err := json.Marshal(storageConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encode node storage config: " + err.Error()})
		return
	}
	billingJSON, err := json.Marshal(req.Pool.BillingConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encode pool billing config: " + err.Error()})
		return
	}
	policyJSON, err := json.Marshal(req.Pool.PolicyConfig)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encode pool policy config: " + err.Error()})
		return
	}
	now := time.Now()
	poolID := database.NewID()
	node := database.StorageNode{
		Name:       req.Name,
		MachineID:  req.MachineID,
		Endpoint:   strings.TrimRight(req.Endpoint, "/"),
		AuthToken:  req.AuthToken,
		Status:     "online",
		LastSeenAt: &now,
		AccountID:  accountID,
		PoolID:     &poolID,
	}
	pool := database.FilePool{
		ID:            poolID,
		Name:          req.Pool.Name,
		Description:   req.Pool.Description,
		AccountID:     accountID,
		StorageConfig: datatypes.JSON(storageJSON),
		BillingConfig: datatypes.JSON(billingJSON),
		PolicyConfig:  datatypes.JSON(policyJSON),
		IsHidden:      req.Pool.IsHidden,
	}

	if err := files.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&pool).Error; err != nil {
			return err
		}
		return tx.Create(&node).Error
	}); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "could not create node and pool: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, nodeCreateResponse{Node: node, PoolID: pool.ID})
}

func listNodes(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok || result == nil || result.Account == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var nodes []database.StorageNode
	if err := files.DB().Where("account_id = ?", result.Account.GetId()).Order("created_at desc").Find(&nodes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Total", fmt.Sprintf("%d", len(nodes)))
	c.JSON(http.StatusOK, nodes)
}

func getNode(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok || result == nil || result.Account == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var node database.StorageNode
	if err := files.DB().Where("id = ? AND account_id = ?", c.Param("id"), result.Account.GetId()).First(&node).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	c.JSON(http.StatusOK, node)
}

type updateNodeRequest struct {
	Name     *string `json:"name"`
	PoolName *string `json:"pool_name"`
}

func updateNode(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok || result == nil || result.Account == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var node database.StorageNode
	if err := files.DB().Where("id = ? AND account_id = ?", c.Param("id"), result.Account.GetId()).First(&node).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}

	var req updateNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]any{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name cannot be empty"})
			return
		}
		updates["name"] = name
	}
	if req.PoolName != nil {
		poolName := strings.TrimSpace(*req.PoolName)
		if poolName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "pool_name cannot be empty"})
			return
		}
		if node.PoolID == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "node has no linked pool"})
			return
		}
		if err := files.DB().Model(&database.FilePool{}).Where("id = ? AND account_id = ?", *node.PoolID, result.Account.GetId()).Update("name", poolName).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if len(updates) > 0 {
		if err := files.DB().Model(&database.StorageNode{}).Where("id = ? AND account_id = ?", node.ID, result.Account.GetId()).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := files.DB().Where("id = ? AND account_id = ?", node.ID, result.Account.GetId()).First(&node).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, node)
}

func deleteNode(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok || result == nil || result.Account == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var node database.StorageNode
	if err := files.DB().Where("id = ? AND account_id = ?", c.Param("id"), result.Account.GetId()).First(&node).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	if err := files.DB().Delete(&node).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "pool_id": node.PoolID})
}

func validateNodeEndpoint(ctx context.Context, rawEndpoint, machineID, authToken string) (*url.URL, error) {
	nodeURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(rawEndpoint), "/"))
	if err != nil || nodeURL.Scheme == "" || nodeURL.Host == "" {
		return nil, fmt.Errorf("endpoint must be an absolute http or https URL")
	}
	if nodeURL.Scheme != "http" && nodeURL.Scheme != "https" {
		return nil, fmt.Errorf("endpoint scheme must be http or https")
	}
	if nodeURL.Path != "" || nodeURL.RawQuery != "" || nodeURL.Fragment != "" {
		return nil, fmt.Errorf("endpoint must not contain a path, query, or fragment")
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	identityRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, nodeURL.String()+"/_dfs/identity", nil)
	if err != nil {
		return nil, err
	}
	identityResponse, err := client.Do(identityRequest)
	if err != nil {
		return nil, err
	}
	defer identityResponse.Body.Close()
	if identityResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("identity endpoint returned HTTP %d", identityResponse.StatusCode)
	}
	var identity nodeIdentityResponse
	if err := decodeJSONResponse(identityResponse, &identity); err != nil {
		return nil, fmt.Errorf("decode identity response: %w", err)
	}
	if identity.NodeType != "storage" || identity.MachineID != machineID {
		return nil, fmt.Errorf("identity mismatch")
	}

	authPayload, err := json.Marshal(map[string]string{"token": authToken})
	if err != nil {
		return nil, err
	}
	authRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, nodeURL.String()+"/_dfs/auth/validate", strings.NewReader(string(authPayload)))
	if err != nil {
		return nil, err
	}
	authRequest.Header.Set("Content-Type", "application/json")
	authResponse, err := client.Do(authRequest)
	if err != nil {
		return nil, err
	}
	defer authResponse.Body.Close()
	if authResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth validation returned HTTP %d", authResponse.StatusCode)
	}
	var validation nodeAuthValidationResponse
	if err := decodeJSONResponse(authResponse, &validation); err != nil {
		return nil, fmt.Errorf("decode auth validation response: %w", err)
	}
	if !validation.Valid || validation.MachineID != machineID {
		return nil, fmt.Errorf("auth token rejected")
	}

	return nodeURL, nil
}

func decodeJSONResponse(response *http.Response, target any) error {
	body := io.LimitReader(response.Body, 1<<20)
	return json.NewDecoder(body).Decode(target)
}
