package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

func TestRecordServerFailuresCountsUploadFailuresAndCapturesDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	files := service.NewFileService(&database.DB{}, storage.NewLocalBackend(t.TempDir()))
	r := gin.New()
	r.Use(recordServerFailures(files))
	r.GET("/api/files/upload/direct", func(c *gin.Context) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage unavailable"})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/files/upload/direct?token=do-not-log", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	snapshot := files.FailureLog().Snapshot(1)
	if snapshot.ServerFailureCount != 1 || snapshot.UploadFailureCount != 1 {
		t.Fatalf("failure counts = %+v, want one of each", snapshot)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Path != "/api/files/upload/direct" || snapshot.Events[0].Detail != `{"error":"storage unavailable"}` {
		t.Fatalf("event = %+v, want upload path and response detail", snapshot.Events)
	}
}

func TestAuthenticateWebDAVAcceptsTokenIDAndSecret(t *testing.T) {
	files := newWebDAVAuthTestService(t)
	accountID := uuid.New()
	tokenID := database.NewID()
	secret := database.NewID()
	createWebDAVAuthTestToken(t, files, tokenID, accountID, secret)

	req := httptest.NewRequest("PROPFIND", "/webdav/", nil)
	req.SetBasicAuth(tokenID, secret)

	gotAccountID, ok := authenticateWebDAV(req, files)
	if !ok {
		t.Fatal("authenticateWebDAV() ok = false, want true")
	}
	if gotAccountID != accountID.String() {
		t.Fatalf("authenticateWebDAV() accountID = %q, want %q", gotAccountID, accountID.String())
	}
}

func TestAuthenticateWebDAVAcceptsEmbeddedPasswordToken(t *testing.T) {
	files := newWebDAVAuthTestService(t)
	accountID := uuid.New()
	tokenID := database.NewID()
	secret := database.NewID()
	createWebDAVAuthTestToken(t, files, tokenID, accountID, secret)

	req := httptest.NewRequest("PROPFIND", "/webdav/", nil)
	req.SetBasicAuth("anything", tokenID+":"+secret)

	gotAccountID, ok := authenticateWebDAV(req, files)
	if !ok {
		t.Fatal("authenticateWebDAV() ok = false, want true")
	}
	if gotAccountID != accountID.String() {
		t.Fatalf("authenticateWebDAV() accountID = %q, want %q", gotAccountID, accountID.String())
	}
}

func TestAuthenticateWebDAVAcceptsSecretOnlyPassword(t *testing.T) {
	files := newWebDAVAuthTestService(t)
	accountID := uuid.New()
	tokenID := database.NewID()
	secret := database.NewID()
	createWebDAVAuthTestToken(t, files, tokenID, accountID, secret)

	req := httptest.NewRequest("PROPFIND", "/webdav/", nil)
	req.SetBasicAuth("anything", secret)

	gotAccountID, ok := authenticateWebDAV(req, files)
	if !ok {
		t.Fatal("authenticateWebDAV() ok = false, want true")
	}
	if gotAccountID != accountID.String() {
		t.Fatalf("authenticateWebDAV() accountID = %q, want %q", gotAccountID, accountID.String())
	}
}

func newWebDAVAuthTestService(t *testing.T) *service.FileService {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+database.NewID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := db.AutoMigrate(&database.WebDAVToken{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	return service.NewFileService(&database.DB{DB: db}, nil)
}

func createWebDAVAuthTestToken(t *testing.T, files *service.FileService, tokenID string, accountID uuid.UUID, secret string) {
	t.Helper()

	hashBytes, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	token := database.WebDAVToken{
		ID:        tokenID,
		AccountID: accountID,
		TokenHash: string(hashBytes),
		Label:     "test",
	}
	if err := files.DB().Create(&token).Error; err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}
