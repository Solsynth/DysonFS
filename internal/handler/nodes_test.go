package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

func TestCreateNodeCreatesOwnedPoolAfterValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const machineID = "node-test-1"
	const authToken = "node-auth-token"

	nodeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_dfs/identity":
			if r.Method != http.MethodGet {
				t.Errorf("identity method = %s, want GET", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"machine_id": machineID, "node_type": "storage"})
		case "/_dfs/auth/validate":
			var body struct {
				Token string `json:"token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode auth request: %v", err)
			}
			if body.Token != authToken {
				t.Errorf("auth token = %q, want %q", body.Token, authToken)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"valid": body.Token == authToken, "machine_id": machineID})
		default:
			http.NotFound(w, r)
		}
	}))
	defer nodeServer.Close()

	db := openHandlerTestDB(t, &database.FilePool{}, &database.StorageNode{})
	files := service.NewFileService(&database.DB{DB: db}, storage.NewLocalBackend(t.TempDir()))
	accountID := uuid.New()
	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, service.NewTaskService(&database.DB{DB: db}), service.NewQuotaService(&database.DB{DB: db}), nil, nil)

	request := map[string]any{
		"name":       "Test node",
		"machine_id": machineID,
		"endpoint":   nodeServer.URL,
		"auth_token": authToken,
		"pool": map[string]any{
			"name":          "Test node pool",
			"description":   "Owned test pool",
			"bucket":        "default",
			"access_key":    "access-key",
			"secret_key":    "secret-key",
			"enable_signed": true,
			"policy_config": map[string]any{
				"public_usable": false,
			},
		},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/nodes", bytes.NewReader(payload)))
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}

	var created struct {
		Node struct {
			ID        string  `json:"id"`
			AccountID string  `json:"account_id"`
			PoolID    *string `json:"pool_id"`
		} `json:"node"`
		PoolID string `json:"pool_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if created.Node.ID == "" || created.Node.AccountID != accountID.String() || created.Node.PoolID == nil || *created.Node.PoolID != created.PoolID {
		t.Fatalf("created response = %+v, want owned node linked to pool", created)
	}

	var pool database.FilePool
	if err := db.Where("id = ?", created.PoolID).First(&pool).Error; err != nil {
		t.Fatalf("load pool: %v", err)
	}
	if pool.AccountID != accountID {
		t.Fatalf("pool account = %s, want %s", pool.AccountID, accountID)
	}
	var storageConfig service.PoolStorageConfig
	if err := json.Unmarshal(pool.StorageConfig, &storageConfig); err != nil {
		t.Fatalf("decode pool storage config: %v", err)
	}
	if storageConfig.Endpoint != nodeServer.Listener.Addr().String() || storageConfig.Bucket != "default" || storageConfig.SecretId != "access-key" || storageConfig.SecretKey != "secret-key" {
		t.Fatalf("storage config = %+v, want node S3 config", storageConfig)
	}

	var node database.StorageNode
	if err := db.Where("id = ?", created.Node.ID).First(&node).Error; err != nil {
		t.Fatalf("load node: %v", err)
	}
	if node.Endpoint != nodeServer.URL || node.Status != "online" || node.PoolID == nil || *node.PoolID != pool.ID {
		t.Fatalf("node = %+v, want validated online node linked to pool", node)
	}

	patchBody := bytes.NewBufferString(`{"name":"Renamed node","pool_name":"Renamed pool"}`)
	patchResponse := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/nodes/"+created.Node.ID, patchBody)
	r.ServeHTTP(patchResponse, patchRequest)
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want %d; body = %s", patchResponse.Code, http.StatusOK, patchResponse.Body.String())
	}
	var renamedNode database.StorageNode
	if err := db.Where("id = ?", created.Node.ID).First(&renamedNode).Error; err != nil {
		t.Fatalf("reload renamed node: %v", err)
	}
	if renamedNode.Name != "Renamed node" {
		t.Fatalf("node name = %q, want Renamed node", renamedNode.Name)
	}
	if err := db.Where("id = ?", created.PoolID).First(&pool).Error; err != nil {
		t.Fatalf("reload renamed pool: %v", err)
	}
	if pool.Name != "Renamed pool" {
		t.Fatalf("pool name = %q, want Renamed pool", pool.Name)
	}
}
