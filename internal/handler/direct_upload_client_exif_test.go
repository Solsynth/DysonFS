package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
)

// clientExifAnalysis is the `client_analysis` the Flutter uploader sends for an
// image: dimensions, blurhash, and the v2 flat EXIF map its overlay reads.
const clientExifAnalysis = `{"width":64,"height":64,"blurhash":"LKO2?U%2Tw=w]~RBVZRi};RPxuwH",` +
	`"exif_version":2,"exif":{"DateTime":"2026:10:05 12:34:56","Model":"Pixel 8",` +
	`"ISOSpeedRatings":"125","FNumber":"28/10","ExposureTime":"1/60","FocalLength":"42/10"}}`

// startClientExifUpload prepares an image upload that advertises client
// analysis and a client compression derivative, PUTs both objects, and
// completes the upload. It returns the completed file id and the decoded
// complete response.
func startClientExifUpload(t *testing.T) (*service.FileService, *database.CloudFile, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	endpoint := startNoAuthMockS3(t, "testbucket")
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.PersistentTask{})
	stor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, stor)
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})
	accountID, poolID := uuid.New(), database.NewID()
	pool := database.FilePool{
		ID: poolID, Name: "test-s3", AccountID: accountID,
		StorageConfig: datatypes.JSON([]byte(fmt.Sprintf(
			`{"enable_signed":true,"enable_ssl":false,"endpoint":%q,"bucket":"testbucket","secret_id":"ak","secret_key":"sk"}`,
			endpoint))),
		BillingConfig: datatypes.JSON([]byte(`{}`)),
		PolicyConfig:  datatypes.JSON([]byte(`{}`)),
	}
	if err := db.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}
	source, compression := generateTestJPEG(t), generateTestJPEG(t)
	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, tasks, quota, nil, nil)

	body := fmt.Sprintf(`{"file_name":"photo.jpg","file_size":%d,"content_type":"image/jpeg","pool_id":%q,"client_analysis":%s,"want_compression":true,"compression_mime_type":"image/jpeg"}`,
		len(source), poolID, clientExifAnalysis)
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/prepare", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var prepared struct {
		TaskID         string `json:"task_id"`
		UploadURL      string `json:"upload_url"`
		CompressionURL string `json:"compression_upload_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	if prepared.TaskID == "" || prepared.UploadURL == "" || prepared.CompressionURL == "" {
		t.Fatalf("prepare response = %s, want task, source and compression URLs", rec.Body.String())
	}

	putURL(t, prepared.UploadURL, source)
	putURL(t, prepared.CompressionURL, compression)

	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/complete", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var completed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &completed); err != nil {
		t.Fatalf("decode complete response: %v", err)
	}
	fileID, _ := completed["id"].(string)
	if fileID == "" {
		t.Fatalf("complete response missing file id: %s", rec.Body.String())
	}
	var file database.CloudFile
	if err := db.Preload("Object").First(&file, "id = ?", fileID).Error; err != nil {
		t.Fatalf("load completed file: %v", err)
	}
	return files, &file, completed
}

// The client extracts EXIF before uploading and ships it in `client_analysis`.
// The complete response must already carry it, otherwise the file the app
// displays right after an upload shows no EXIF at all.
func TestDirectUploadPersistsClientExif(t *testing.T) {
	_, file, completed := startClientExifUpload(t)

	exif := assertClientExifStored(t, file)

	responseMeta, ok := completed["file_meta"].(map[string]any)
	if !ok {
		t.Fatalf("complete response file_meta = %#v, want object", completed["file_meta"])
	}
	responseExif, ok := responseMeta["exif"].(map[string]any)
	if !ok {
		t.Fatalf("complete response file_meta = %#v, want client exif", responseMeta)
	}
	if responseExif["Model"] != exif["Model"] {
		t.Fatalf("complete response exif = %#v, want %#v", responseExif, exif)
	}
	if version, _ := responseMeta["exif_version"].(float64); version != 2 {
		t.Fatalf("complete response exif_version = %#v, want 2", responseMeta["exif_version"])
	}
}

// Server reanalysis is authoritative but cannot read every source, so a client
// EXIF map must survive a reanalysis that extracts nothing.
func TestDirectUploadClientExifSurvivesServerReanalysis(t *testing.T) {
	files, file, _ := startClientExifUpload(t)
	objectID := ""
	if file.ObjectID != nil {
		objectID = *file.ObjectID
	}
	if objectID == "" {
		t.Fatalf("completed file has no object id: %#v", file)
	}

	if _, err := files.ReanalyzeStoredObject(context.Background(), objectID); err != nil {
		t.Fatalf("ReanalyzeStoredObject() error = %v", err)
	}

	reloaded := loadFileWithObject(t, files, file.ID)
	assertClientExifStored(t, reloaded)

	var meta map[string]any
	if err := json.Unmarshal(reloaded.Object.Meta, &meta); err != nil {
		t.Fatalf("decode reanalyzed object meta: %v", err)
	}
	if meta["analysis_source"] != "server" {
		t.Fatalf("reanalyzed analysis_source = %#v, want server", meta["analysis_source"])
	}
}

func loadFileWithObject(t *testing.T, files *service.FileService, fileID string) *database.CloudFile {
	t.Helper()
	file, err := files.GetFile(fileID)
	if err != nil {
		t.Fatalf("GetFile(%q) error = %v", fileID, err)
	}
	if file.Object == nil {
		t.Fatalf("file %q has no object", fileID)
	}
	return file
}

// assertClientExifStored requires the file's object metadata to carry the v2
// EXIF the client uploaded, and returns the stored exif map.
func assertClientExifStored(t *testing.T, file *database.CloudFile) map[string]any {
	t.Helper()
	if file.Object == nil {
		t.Fatalf("file %q has no object", file.ID)
	}
	if file.Object.MimeType != "image/jpeg" {
		t.Fatalf("object mime = %q, want image/jpeg", file.Object.MimeType)
	}
	var meta map[string]any
	if err := json.Unmarshal(file.Object.Meta, &meta); err != nil {
		t.Fatalf("decode object meta: %v", err)
	}
	if version, _ := meta["exif_version"].(float64); version != 2 {
		t.Fatalf("object meta exif_version = %#v, want 2", meta["exif_version"])
	}
	exif, ok := meta["exif"].(map[string]any)
	if !ok {
		t.Fatalf("object meta has no client exif: %#v", meta)
	}
	for key, want := range map[string]string{
		"Model":           "Pixel 8",
		"DateTime":        "2026:10:05 12:34:56",
		"ISOSpeedRatings": "125",
		"FNumber":         "28/10",
		"ExposureTime":    "1/60",
		"FocalLength":     "42/10",
	} {
		if got, _ := exif[key].(string); got != want {
			t.Fatalf("exif[%q] = %#v, want %q (meta = %#v)", key, exif[key], want, meta)
		}
	}
	return exif
}
