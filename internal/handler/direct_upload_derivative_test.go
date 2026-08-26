package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/dispatch"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	"src.solsynth.dev/sosys/filesystem/internal/worker"
)

// TestDirectUploadImageGeneratesCompressionDerivative drives the full new
// direct-upload flow (prepare -> presigned PUT -> complete) with a real
// bundled worker attached, and verifies the worker still produces the
// system.compression.low derivative for image uploads.
func TestDirectUploadImageGeneratesCompressionDerivative(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := startNoAuthMockS3(t, "testbucket")
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.QuotaRecord{}, &database.PersistentTask{})
	defaultStor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, defaultStor)
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})
	accountID := uuid.New()

	poolID := database.NewID()
	pool := database.FilePool{
		ID:        poolID,
		Name:      "test-s3",
		AccountID: accountID,
		StorageConfig: datatypes.JSON([]byte(fmt.Sprintf(
			`{"enable_signed":true,"enable_ssl":false,"endpoint":%q,"bucket":"testbucket","secret_id":"ak","secret_key":"sk"}`,
			endpoint))),
		BillingConfig: datatypes.JSON([]byte(`{}`)),
		PolicyConfig:  datatypes.JSON([]byte(`{}`)),
	}
	if err := db.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}

	// Generate a small but real JPEG with libvips.
	jpeg := generateTestJPEG(t)

	w := worker.New(nil, files, defaultStor, &database.DB{DB: db}, t.TempDir())
	dispatcher := dispatch.NewBundled([]*worker.Worker{w})

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, tasks, quota, nil, dispatcher)

	prepareBody := fmt.Sprintf(`{"file_name":"photo.jpg","file_size":%d,"content_type":"image/jpeg","pool_id":%q}`, len(jpeg), poolID)
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/prepare", strings.NewReader(prepareBody))
	req.Header.Set("Content-Type", "application/json")
	wRec := httptest.NewRecorder()
	r.ServeHTTP(wRec, req)
	if wRec.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, want 200, body = %s", wRec.Code, wRec.Body.String())
	}
	var prepared struct {
		TaskID    string `json:"task_id"`
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(wRec.Body.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	if prepared.TaskID == "" || prepared.UploadURL == "" {
		t.Fatalf("prepare response = %s, want task_id and upload_url", wRec.Body.String())
	}
	putURL(t, prepared.UploadURL, jpeg)

	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/complete", nil)
	wRec = httptest.NewRecorder()
	r.ServeHTTP(wRec, req)
	if wRec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, want 200, body = %s", wRec.Code, wRec.Body.String())
	}
	var completed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(wRec.Body.Bytes(), &completed); err != nil {
		t.Fatalf("decode complete response: %v", err)
	}
	if completed.ID == "" {
		t.Fatalf("complete response missing file id: %s", wRec.Body.String())
	}

	// The worker processes asynchronously; wait for the derivative to land.
	deadline := time.Now().Add(15 * time.Second)
	var derived *database.CloudFile
	var parent database.CloudFile
	for time.Now().Before(deadline) {
		var children []database.CloudFile
		if err := db.Preload("Object").Where("parent_id = ? AND application_type = ?", completed.ID, "system.compression.low").Find(&children).Error; err != nil {
			t.Fatalf("query derived children: %v", err)
		}
		if len(children) > 0 {
			derived = &children[0]
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if derived == nil {
		var task database.PersistentTask
		_ = db.First(&task, "task_id = ?", prepared.TaskID).Error
		var children []database.CloudFile
		_ = db.Where("parent_id = ?", completed.ID).Find(&children).Error
		var file database.CloudFile
		_ = db.Preload("Object").First(&file, "id = ?", completed.ID).Error
		mime := ""
		status := int64(-1)
		if file.Object != nil {
			mime = file.Object.MimeType
		}
		_ = db.Model(&database.CloudFile{}).Select("upload_status").First(&file, "id = ?", completed.ID).Scan(&status)
		t.Fatalf("no system.compression.low derivative was created for a direct-uploaded image; task status=%v err=%q children=%d object.mime=%q file.upload_status=%v", task.UploadStatus, processingError(task), len(children), mime, status)
	}
	if err := db.Preload("Object").First(&parent, "id = ?", completed.ID).Error; err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if parent.Object == nil || !parent.Object.HasCompression {
		t.Fatalf("parent object has_compression = %+v, want true after derivative creation", parent.Object)
	}

	// The compressed bytes must be retrievable from the storage the worker
	// writes derivatives to.
	rc, info, err := defaultStor.Get(context.Background(), *derived.Object.StorageKey)
	if err != nil {
		t.Fatalf("read derived object %q: %v", *derived.Object.StorageKey, err)
	}
	rc.Close()
	if info.Size <= 0 {
		t.Fatalf("derived object %q has empty content", *derived.Object.StorageKey)
	}

	var task database.PersistentTask
	if err := db.First(&task, "task_id = ?", prepared.TaskID).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	if task.UploadStatus != database.UploadStatusCompleted {
		t.Fatalf("task status = %v (error %q), want completed", task.UploadStatus, processingError(task))
	}
}

// TestDirectUploadVideoGeneratesThumbnailDerivative is the video counterpart:
// a direct-uploaded mp4 must gain a system.thumbnail child.
func TestDirectUploadVideoGeneratesThumbnailDerivative(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg binary not available")
	}
	gin.SetMode(gin.TestMode)
	endpoint := startNoAuthMockS3(t, "testbucket")
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.QuotaRecord{}, &database.PersistentTask{})
	defaultStor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, defaultStor)
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})
	accountID := uuid.New()

	poolID := database.NewID()
	pool := database.FilePool{
		ID:        poolID,
		Name:      "test-s3",
		AccountID: accountID,
		StorageConfig: datatypes.JSON([]byte(fmt.Sprintf(
			`{"enable_signed":true,"enable_ssl":false,"endpoint":%q,"bucket":"testbucket","secret_id":"ak","secret_key":"sk"}`,
			endpoint))),
		BillingConfig: datatypes.JSON([]byte(`{}`)),
		PolicyConfig:  datatypes.JSON([]byte(`{}`)),
	}
	if err := db.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}

	// Generate a 1-frame mp4 with the local ffmpeg binary.
	mp4Path := t.TempDir() + "/sample.mp4"
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=duration=1:size=64x64:rate=1", "-pix_fmt", "yuv420p", mp4Path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate sample: %v\n%s", err, out)
	}
	mp4Bytes, err := os.ReadFile(mp4Path)
	if err != nil {
		t.Fatalf("read sample mp4: %v", err)
	}

	w := worker.New(nil, files, defaultStor, &database.DB{DB: db}, t.TempDir())
	dispatcher := dispatch.NewBundled([]*worker.Worker{w})

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, tasks, quota, nil, dispatcher)

	prepareBody := fmt.Sprintf(`{"file_name":"movie.mp4","file_size":%d,"content_type":"video/mp4","pool_id":%q}`, len(mp4Bytes), poolID)
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/prepare", strings.NewReader(prepareBody))
	req.Header.Set("Content-Type", "application/json")
	wRec := httptest.NewRecorder()
	r.ServeHTTP(wRec, req)
	if wRec.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, want 200, body = %s", wRec.Code, wRec.Body.String())
	}
	var prepared struct {
		TaskID    string `json:"task_id"`
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(wRec.Body.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	putURL(t, prepared.UploadURL, mp4Bytes)

	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/complete", nil)
	wRec = httptest.NewRecorder()
	r.ServeHTTP(wRec, req)
	if wRec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, want 200, body = %s", wRec.Code, wRec.Body.String())
	}
	var completed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(wRec.Body.Bytes(), &completed); err != nil {
		t.Fatalf("decode complete response: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	var derived *database.CloudFile
	for time.Now().Before(deadline) {
		var children []database.CloudFile
		if err := db.Preload("Object").Where("parent_id = ? AND application_type = ?", completed.ID, "system.thumbnail").Find(&children).Error; err != nil {
			t.Fatalf("query thumbnail children: %v", err)
		}
		if len(children) > 0 {
			derived = &children[0]
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if derived == nil {
		t.Fatal("no system.thumbnail derivative was created for a direct-uploaded video")
	}
	var parent database.CloudFile
	if err := db.Preload("Object").First(&parent, "id = ?", completed.ID).Error; err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if parent.Object == nil || !parent.Object.HasThumbnail {
		t.Fatalf("parent object has_thumbnail = %+v, want true after thumbnail creation", parent.Object)
	}
}

// TestDirectUploadMultipartImageGeneratesCompressionDerivative verifies the
// multipart direct path too: part PUTs carry no Content-Type, so the completed
// object stats back as application/octet-stream; the server must still resolve
// the real media type from the bytes and generate the compression derivative.
func TestDirectUploadMultipartImageGeneratesCompressionDerivative(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := startNoAuthMockS3(t, "testbucket")
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.QuotaRecord{}, &database.PersistentTask{})
	defaultStor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, defaultStor)
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})
	accountID := uuid.New()

	poolID := database.NewID()
	pool := database.FilePool{
		ID:        poolID,
		Name:      "test-s3",
		AccountID: accountID,
		StorageConfig: datatypes.JSON([]byte(fmt.Sprintf(
			`{"enable_signed":true,"enable_ssl":false,"endpoint":%q,"bucket":"testbucket","secret_id":"ak","secret_key":"sk"}`,
			endpoint))),
		BillingConfig: datatypes.JSON([]byte(`{}`)),
		PolicyConfig:  datatypes.JSON([]byte(`{}`)),
	}
	if err := db.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}

	jpeg := generateTestJPEG(t)

	w := worker.New(nil, files, defaultStor, &database.DB{DB: db}, t.TempDir())
	dispatcher := dispatch.NewBundled([]*worker.Worker{w})

	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, tasks, quota, nil, dispatcher)

	prepareBody := fmt.Sprintf(`{"file_name":"photo.jpg","file_size":%d,"content_type":"image/jpeg","multipart":true,"pool_id":%q}`, len(jpeg), poolID)
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/prepare", strings.NewReader(prepareBody))
	req.Header.Set("Content-Type", "application/json")
	wRec := httptest.NewRecorder()
	r.ServeHTTP(wRec, req)
	if wRec.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, want 200, body = %s", wRec.Code, wRec.Body.String())
	}
	var prepared struct {
		TaskID    string `json:"task_id"`
		UploadID  string `json:"upload_id"`
		PartCount int    `json:"part_count"`
	}
	if err := json.Unmarshal(wRec.Body.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	if prepared.TaskID == "" || prepared.UploadID == "" || prepared.PartCount != 1 {
		t.Fatalf("prepare response = %s, want 1-part multipart plan", wRec.Body.String())
	}
	partReq := httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/part", strings.NewReader(`{"part_number":1}`))
	partReq.Header.Set("Content-Type", "application/json")
	partRec := httptest.NewRecorder()
	r.ServeHTTP(partRec, partReq)
	if partRec.Code != http.StatusOK {
		t.Fatalf("presign part status = %d, body = %s", partRec.Code, partRec.Body.String())
	}
	var part struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(partRec.Body.Bytes(), &part); err != nil {
		t.Fatalf("decode presign part response: %v", err)
	}
	putURL(t, part.UploadURL, jpeg)

	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/complete", nil)
	wRec = httptest.NewRecorder()
	r.ServeHTTP(wRec, req)
	if wRec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, want 200, body = %s", wRec.Code, wRec.Body.String())
	}
	var completed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(wRec.Body.Bytes(), &completed); err != nil {
		t.Fatalf("decode complete response: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	var derived *database.CloudFile
	for time.Now().Before(deadline) {
		var children []database.CloudFile
		if err := db.Preload("Object").Where("parent_id = ? AND application_type = ?", completed.ID, "system.compression.low").Find(&children).Error; err != nil {
			t.Fatalf("query derived children: %v", err)
		}
		if len(children) > 0 {
			derived = &children[0]
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if derived == nil {
		var task database.PersistentTask
		_ = db.First(&task, "task_id = ?", prepared.TaskID).Error
		t.Fatalf("no system.compression.low derivative for multipart direct image; task status=%v err=%q", task.UploadStatus, processingError(task))
	}
}

// TestDirectUploadClientMediaSkipsSourceAnalysis verifies that client metadata
// and a client-produced thumbnail complete the upload without dispatching the
// source object through the worker.
func TestDirectUploadClientMediaSkipsSourceAnalysis(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := startNoAuthMockS3(t, "testbucket")
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.QuotaRecord{}, &database.PersistentTask{})
	defaultStor := storage.NewLocalBackend(t.TempDir())
	files := service.NewFileService(&database.DB{DB: db}, defaultStor)
	tasks := service.NewTaskService(&database.DB{DB: db})
	quota := service.NewQuotaService(&database.DB{DB: db})
	accountID := uuid.New()
	poolID := database.NewID()
	pool := database.FilePool{
		ID:        poolID,
		Name:      "test-s3",
		AccountID: accountID,
		StorageConfig: datatypes.JSON([]byte(fmt.Sprintf(
			`{"enable_signed":true,"enable_ssl":false,"endpoint":%q,"bucket":"testbucket","secret_id":"ak","secret_key":"sk"}`,
			endpoint))),
		BillingConfig: datatypes.JSON([]byte(`{}`)),
		PolicyConfig:  datatypes.JSON([]byte(`{}`)),
	}
	if err := db.Create(&pool).Error; err != nil {
		t.Fatalf("create pool: %v", err)
	}

	source := generateTestJPEG(t)
	thumbnail := generateTestJPEG(t)
	w := worker.New(nil, files, defaultStor, &database.DB{DB: db}, t.TempDir())
	dispatcher := dispatch.NewBundled([]*worker.Worker{w})
	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, tasks, quota, nil, dispatcher)

	prepareBody := fmt.Sprintf(`{"file_name":"movie.mp4","file_size":%d,"content_type":"video/mp4","pool_id":%q,"multipart":true,"client_analysis":{"width":1920,"height":1080,"duration_ms":83420,"aspect_ratio":"16:9"},"want_thumbnail":true,"want_compression":true}`, len(source), poolID)
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/prepare", strings.NewReader(prepareBody))
	req.Header.Set("Content-Type", "application/json")
	wRec := httptest.NewRecorder()
	r.ServeHTTP(wRec, req)
	if wRec.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, body = %s", wRec.Code, wRec.Body.String())
	}
	var prepared struct {
		TaskID       string `json:"task_id"`
		UploadID     string `json:"upload_id"`
		PartCount    int    `json:"part_count"`
		ThumbnailURL string `json:"thumbnail_upload_url"`
		ThumbnailKey string `json:"thumbnail_key"`
	}
	if err := json.Unmarshal(wRec.Body.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	if prepared.TaskID == "" || prepared.UploadID == "" || prepared.PartCount != 1 || prepared.ThumbnailURL == "" || prepared.ThumbnailKey == "" {
		t.Fatalf("prepare response = %s, want multipart source and thumbnail URLs", wRec.Body.String())
	}
	partReq := httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/part", strings.NewReader(`{"part_number":1}`))
	partReq.Header.Set("Content-Type", "application/json")
	partRec := httptest.NewRecorder()
	r.ServeHTTP(partRec, partReq)
	if partRec.Code != http.StatusOK {
		t.Fatalf("presign part status = %d, body = %s", partRec.Code, partRec.Body.String())
	}
	var part struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(partRec.Body.Bytes(), &part); err != nil {
		t.Fatalf("decode part response: %v", err)
	}
	putURL(t, part.UploadURL, source)
	putURL(t, prepared.ThumbnailURL, thumbnail)

	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/complete", nil)
	wRec = httptest.NewRecorder()
	r.ServeHTTP(wRec, req)
	if wRec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", wRec.Code, wRec.Body.String())
	}
	var completed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(wRec.Body.Bytes(), &completed); err != nil {
		t.Fatalf("decode complete response: %v", err)
	}

	var parent database.CloudFile
	if err := db.Preload("Object").First(&parent, "id = ?", completed.ID).Error; err != nil {
		t.Fatalf("load parent: %v", err)
	}
	if parent.UploadStatus != database.UploadStatusCompleted {
		t.Fatalf("parent status = %v, want completed", parent.UploadStatus)
	}
	if parent.Object == nil || parent.Object.Meta == nil {
		t.Fatal("client metadata was not persisted")
	}
	var meta map[string]any
	if err := json.Unmarshal(parent.Object.Meta, &meta); err != nil {
		t.Fatalf("decode object metadata: %v", err)
	}
	if meta["analysis_source"] != "client" || meta["width"] != float64(1920) || meta["duration_ms"] != float64(83420) {
		t.Fatalf("object metadata = %#v, want client analysis", meta)
	}
	var children []database.CloudFile
	if err := db.Preload("Object").Where("parent_id = ? AND application_type = ?", completed.ID, "system.thumbnail").Find(&children).Error; err != nil {
		t.Fatalf("query thumbnail children: %v", err)
	}
	if len(children) != 1 || children[0].Object == nil {
		t.Fatalf("thumbnail children = %#v, want one stored child", children)
	}
	if !parent.Object.HasThumbnail {
		t.Fatal("parent has_thumbnail = false, want true")
	}
	var task database.PersistentTask
	if err := db.First(&task, "task_id = ?", prepared.TaskID).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	if task.UploadStatus != database.UploadStatusCompleted {
		t.Fatalf("task status = %v, want completed", task.UploadStatus)
	}
	var compressions []database.CloudFile
	if err := db.Where("parent_id = ? AND application_type = ?", completed.ID, "system.compression.low").Find(&compressions).Error; err != nil {
		t.Fatalf("query unexpected compression children: %v", err)
	}
	if len(compressions) != 0 {
		t.Fatalf("client-assisted upload dispatched source processing: %#v", compressions)
	}
}

func TestDirectUploadClientImageCompressionSkipsSourceAnalysis(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := startNoAuthMockS3(t, "testbucket")
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.QuotaRecord{}, &database.PersistentTask{})
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
	source, thumbnail, compression := generateTestJPEG(t), generateTestJPEG(t), generateTestWebP(t)
	r := gin.New()
	r.Use(testAuthMiddleware(accountID))
	RegisterRoutes(r, &config.Config{}, files, nil, tasks, quota, nil, nil)

	body := fmt.Sprintf(`{"file_name":"photo.jpg","file_size":%d,"content_type":"image/jpeg","pool_id":%q,"multipart":true,"client_analysis":{"width":64,"height":64},"want_thumbnail":true,"want_compression":true}`, len(source), poolID)
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/prepare", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var prepared struct {
		TaskID         string `json:"task_id"`
		ThumbnailURL   string `json:"thumbnail_upload_url"`
		CompressionURL string `json:"compression_upload_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	if prepared.TaskID == "" || prepared.ThumbnailURL == "" || prepared.CompressionURL == "" {
		t.Fatalf("prepare response = %s, want thumbnail and compression URLs", rec.Body.String())
	}

	partReq := httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/part", strings.NewReader(`{"part_number":1}`))
	partReq.Header.Set("Content-Type", "application/json")
	partRec := httptest.NewRecorder()
	r.ServeHTTP(partRec, partReq)
	if partRec.Code != http.StatusOK {
		t.Fatalf("presign part status = %d, body = %s", partRec.Code, partRec.Body.String())
	}
	var part struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(partRec.Body.Bytes(), &part); err != nil {
		t.Fatalf("decode part response: %v", err)
	}
	putURL(t, part.UploadURL, source)
	putURL(t, prepared.ThumbnailURL, thumbnail)
	putURL(t, prepared.CompressionURL, compression)

	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/complete", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var completed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &completed); err != nil {
		t.Fatalf("decode complete response: %v", err)
	}
	var parent database.CloudFile
	if err := db.Preload("Object").First(&parent, "id = ?", completed.ID).Error; err != nil {
		t.Fatalf("load parent: %v", err)
	}
	if parent.UploadStatus != database.UploadStatusCompleted || parent.Object == nil || !parent.Object.HasCompression || !parent.Object.HasThumbnail {
		t.Fatalf("parent status/object = %v/%#v, want completed with compression and thumbnail", parent.UploadStatus, parent.Object)
	}
	var children []database.CloudFile
	if err := db.Where("parent_id = ? AND application_type = ?", completed.ID, "system.compression.low").Find(&children).Error; err != nil {
		t.Fatalf("query compression child: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("compression children = %d, want 1", len(children))
	}
	var thumbnails []database.CloudFile
	if err := db.Where("parent_id = ? AND application_type = ?", completed.ID, "system.thumbnail").Find(&thumbnails).Error; err != nil {
		t.Fatalf("query thumbnail child: %v", err)
	}
	if len(thumbnails) != 1 {
		t.Fatalf("thumbnail children = %d, want 1", len(thumbnails))
	}
}

// TestDirectUploadJpegCompressionDerivative verifies a desktop client can
// upload a JPEG compression derivative: prepare declares
// compression_mime_type "image/jpeg", the server presigns a JPEG-typed URL
// and complete validates the uploaded bytes against the declared MIME (the
// legacy default stays image/webp).
func TestDirectUploadJpegCompressionDerivative(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := startNoAuthMockS3(t, "testbucket")
	db := openHandlerTestDB(t, &database.CloudFile{}, &database.FileObject{}, &database.FilePool{}, &database.FilePermission{}, &database.QuotaRecord{}, &database.PersistentTask{})
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

	body := fmt.Sprintf(`{"file_name":"photo.jpg","file_size":%d,"content_type":"image/jpeg","pool_id":%q,"multipart":true,"client_analysis":{"width":64,"height":64},"want_compression":true,"compression_mime_type":"image/jpeg"}`, len(source), poolID)
	req := httptest.NewRequest(http.MethodPost, "/api/files/upload/prepare", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var prepared struct {
		TaskID         string `json:"task_id"`
		CompressionURL string `json:"compression_upload_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare response: %v", err)
	}
	if prepared.TaskID == "" || prepared.CompressionURL == "" {
		t.Fatalf("prepare response = %s, want compression URL", rec.Body.String())
	}

	partReq := httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/part", strings.NewReader(`{"part_number":1}`))
	partReq.Header.Set("Content-Type", "application/json")
	partRec := httptest.NewRecorder()
	r.ServeHTTP(partRec, partReq)
	if partRec.Code != http.StatusOK {
		t.Fatalf("presign part status = %d, body = %s", partRec.Code, partRec.Body.String())
	}
	var part struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(partRec.Body.Bytes(), &part); err != nil {
		t.Fatalf("decode part response: %v", err)
	}
	putURL(t, part.UploadURL, source)
	putURL(t, prepared.CompressionURL, compression)

	req = httptest.NewRequest(http.MethodPost, "/api/files/upload/"+prepared.TaskID+"/complete", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var completed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &completed); err != nil {
		t.Fatalf("decode complete response: %v", err)
	}
	var parent database.CloudFile
	if err := db.Preload("Object").First(&parent, "id = ?", completed.ID).Error; err != nil {
		t.Fatalf("load parent: %v", err)
	}
	if !parent.Object.HasCompression {
		t.Fatalf("parent object = %#v, want HasCompression", parent.Object)
	}
	var children []database.CloudFile
	if err := db.Preload("Object").Where("parent_id = ? AND application_type = ?", completed.ID, "system.compression.low").Find(&children).Error; err != nil {
		t.Fatalf("query compression child: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("compression children = %d, want 1", len(children))
	}
	if children[0].Object == nil || children[0].Object.MimeType != "image/jpeg" {
		t.Fatalf("compression child object = %#v, want image/jpeg MIME", children[0].Object)
	}
}

// generateTestJPEG renders a small but real JPEG with libvips, usable as
func generateTestJPEG(t *testing.T) []byte {
	t.Helper()
	img, err := vips.Black(64, 64)
	if err != nil {
		t.Fatalf("vips.Black: %v", err)
	}
	jpeg, _, err := img.ExportJpeg(&vips.JpegExportParams{Quality: 80})
	img.Close()
	if err != nil {
		t.Fatalf("export jpeg: %v", err)
	}
	return jpeg
}
func generateTestWebP(t *testing.T) []byte {
	t.Helper()
	img, err := vips.Black(64, 64)
	if err != nil {
		t.Fatalf("vips.Black: %v", err)
	}
	webp, _, err := img.ExportWebp(&vips.WebpExportParams{Quality: 80})
	img.Close()
	if err != nil {
		t.Fatalf("export webp: %v", err)
	}
	return webp
}

func processingError(task database.PersistentTask) string {
	if task.ProcessingError == nil {
		return ""
	}
	return *task.ProcessingError
}
