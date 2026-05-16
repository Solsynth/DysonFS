package handler

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/go/pkg/auth"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func RegisterRoutes(r *gin.Engine, cfg *config.Config, files *service.FileService, tasks *service.TaskService, bus *eventbus.Bus) {
	_ = bus
	f := r.Group("/api/files")
	{
		f.GET("/:id/info", func(c *gin.Context) { fileInfo(c, files) })
		f.GET("/:id/open", func(c *gin.Context) { openFile(c, cfg, files) })
		f.GET("/:id/references", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"items": []any{}}) })
		f.GET("/:id/e2ee", func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"code": "file.e2ee_not_found"}) })
		f.GET("/root/children", func(c *gin.Context) { listRoot(c, files) })
		f.GET("/:parentId/children", func(c *gin.Context) { listChildren(c, files) })
		f.POST("/folders", func(c *gin.Context) { createFolder(c, files) })
		f.GET("/me", func(c *gin.Context) { listRoot(c, files) })
		f.POST("/batches/delete", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"count": 0}) })
		f.DELETE("/:id", func(c *gin.Context) { deleteFile(c, files) })
		f.DELETE("/me/recycle", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"count": 0}) })
		f.DELETE("/recycle", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"count": 0}) })
		f.GET("/:id/permissions", func(c *gin.Context) { getFilePermissions(c, files) })
		f.PUT("/:id/permissions", func(c *gin.Context) { updateFilePermissions(c, files) })
	}

	u := r.Group("/api/files/upload")
	{
		u.POST("/create", func(c *gin.Context) { createUploadTask(c, cfg, files, tasks) })
		u.POST("/direct", func(c *gin.Context) { directUpload(c, cfg, files, tasks) })
		u.POST("/chunk/:taskId/:idx", func(c *gin.Context) { uploadChunk(c, cfg, tasks) })
		u.POST("/complete/:taskId", func(c *gin.Context) { completeUpload(c, cfg, files, tasks, bus) })
		u.GET("/tasks", func(c *gin.Context) { listUploadTasks(c, tasks) })
		u.GET("/progress/:taskId", func(c *gin.Context) { uploadProgress(c, tasks) })
		u.GET("/resume/:taskId", func(c *gin.Context) { uploadResume(c, tasks) })
		u.DELETE("/task/:taskId", func(c *gin.Context) { cancelUpload(c, tasks) })
		u.GET("/stats", func(c *gin.Context) { uploadStats(c, tasks) })
		u.DELETE("/tasks/cleanup", func(c *gin.Context) { cleanupTasks(c, tasks) })
		u.GET("/tasks/recent", func(c *gin.Context) { recentTasks(c, tasks) })
		u.GET("/tasks/:taskId/details", func(c *gin.Context) { taskDetails(c, cfg, tasks) })
	}

	p := r.Group("/api/pools")
	{
		p.GET("", func(c *gin.Context) { listPools(c, files) })
		p.DELETE("/:id/recycle", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"count": 0}) })
		p.GET("/:id/permissions", func(c *gin.Context) { getPoolPermissions(c, files) })
		p.PUT("/:id/permissions", func(c *gin.Context) { updatePoolPermissions(c, files) })
	}

	b := r.Group("/api/billing")
	{
		b.GET("quota", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"based_quota": 0, "extra_quota": 0, "total_quota": 0})
		})
		b.GET("quota/records", func(c *gin.Context) { c.Header("X-Total", "0"); c.JSON(http.StatusOK, []any{}) })
		b.GET("usage", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"total_quota": 0}) })
		b.GET("usage/:poolId", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"pool_id": c.Param("poolId")}) })
	}

	r.NoRoute(func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"error": "not found"}) })
}

func fileInfo(c *gin.Context, files *service.FileService) {
	file, err := files.GetFile(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	result, _, ok := auth.GetAuth(c)
	if ok && !files.CanAccessFile(result.Account, result.Session, file, "read") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(http.StatusOK, file)
}

func openFile(c *gin.Context, cfg *config.Config, files *service.FileService) {
	file, err := files.GetFile(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	result, _, ok := auth.GetAuth(c)
	if ok && !files.CanAccessFile(result.Account, result.Session, file, "read") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if file.StorageKey == nil && file.Object != nil && file.Object.StorageKey != nil {
		file.StorageKey = file.Object.StorageKey
	}
	if file.StorageKey == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file storage key missing"})
		return
	}
	download := c.Query("download") == "1" || strings.EqualFold(c.Query("download"), "true")
	name := file.Name
	if file.Object != nil && file.Object.MimeType != "" {
		_ = file.Object.MimeType
	}
	url, err := files.Storage().SignedURL(c.Request.Context(), *file.StorageKey, 15*time.Minute, name, download)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = cfg
	c.Redirect(http.StatusTemporaryRedirect, url)
}

func listChildren(c *gin.Context, files *service.FileService) {
	parent, err := files.GetFile(c.Param("parentId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	result, _, ok := auth.GetAuth(c)
	if !ok && !files.CanAccessFile(nil, nil, parent, "read") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	items, err := files.GetChildren(c.Param("parentId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	filtered := make([]database.CloudFile, 0, len(items))
	for _, item := range items {
		if !ok || files.CanAccessFile(result.Account, result.Session, &item, "read") {
			filtered = append(filtered, item)
		}
	}
	c.Header("X-Total", strconv.Itoa(len(filtered)))
	c.JSON(http.StatusOK, filtered)
}

func listPools(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	ctx := service.AccessContext{}
	if ok {
		ctx.Account = result.Account
		ctx.Session = result.Session
	}
	items, err := files.ListPools(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Total", strconv.Itoa(len(items)))
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func getPoolPermissions(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	pool, err := files.GetPool(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !files.CanUsePool(service.AccessContext{Account: result.Account, Session: result.Session}, pool, "manage") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	perms, err := files.ListPoolPermissions(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": perms})
}

func updatePoolPermissions(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	pool, err := files.GetPool(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !files.CanUsePool(service.AccessContext{Account: result.Account, Session: result.Session}, pool, "manage") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	var req struct {
		Items []database.PoolPermission `json:"items"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := files.UpdatePoolPermissions(c.Param("id"), req.Items); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func getFilePermissions(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	file, err := files.GetFile(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !files.CanAccessFile(result.Account, result.Session, file, "manage") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	perms, err := files.ListFilePermissions(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": perms})
}

func updateFilePermissions(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	file, err := files.GetFile(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !files.CanAccessFile(result.Account, result.Session, file, "manage") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	var req struct {
		Items []database.FilePermission `json:"items"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := files.UpdateFilePermissions(c.Param("id"), req.Items); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func listRoot(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	items, err := files.ListRoot(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Total", strconv.Itoa(len(items)))
	c.JSON(http.StatusOK, items)
}

func createFolder(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req struct {
		Name     string  `json:"name"`
		ParentID *string `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	folder, err := files.CreateFolder(uuid.MustParse(result.Account.GetId()), req.Name, req.ParentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, folder)
}

func deleteFile(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	file, err := files.GetFile(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !files.CanAccessFile(result.Account, result.Session, file, "delete") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if err := files.DeleteFile(file.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func createUploadTask(c *gin.Context, cfg *config.Config, files *service.FileService, tasks *service.TaskService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req struct {
		Name        string  `json:"name"`
		FileName    string  `json:"file_name"`
		FileSize    int64   `json:"file_size"`
		PoolID      *string `json:"pool_id"`
		ChunkSize   int64   `json:"chunk_size"`
		ContentType string  `json:"content_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.FileSize <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_size must be greater than zero"})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if req.ChunkSize <= 0 {
		req.ChunkSize = 5 * 1024 * 1024
	}
	if strings.TrimSpace(req.ContentType) == "" {
		req.ContentType = "application/octet-stream"
	}
	ctx := service.AccessContext{Account: result.Account, Session: result.Session}
	if err := files.ValidatePoolUsage(ctx, req.PoolID, req.FileSize, req.ContentType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	chunks := int((req.FileSize + req.ChunkSize - 1) / req.ChunkSize)
	task, err := tasks.CreateUploadTask(uuid.MustParse(result.Account.GetId()), req.Name, req.FileSize, req.PoolID, req.FileName, req.ContentType, req.ChunkSize, chunks)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = cfg
	c.JSON(http.StatusOK, gin.H{"task_id": task.TaskID, "chunk_size": task.ChunkSize, "chunks_count": task.ChunksCount})
}

func directUpload(c *gin.Context, cfg *config.Config, files *service.FileService, tasks *service.TaskService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if cfg.Files.PreferredStorage == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "storage backend is not configured"})
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	reader, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	defer reader.Close()
	tempDir := cfg.Storage.TempDir
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tempPath := filepath.Join(tempDir, database.NewID()+".upload")
	out, err := os.Create(tempPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if _, err := io.Copy(out, reader); err != nil {
		_ = out.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = out.Close()
	defer os.Remove(tempPath)
	object, err := files.DetectAndCreateObject(tempPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	storageKey := &object.ID
	createdFile, err := files.CreateUploadedFile(uuid.MustParse(result.Account.GetId()), fileHeader.Filename, object.ID, nil, nil, storageKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	stage, err := os.Open(tempPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := files.Storage().Put(c.Request.Context(), object.ID, stage, fileHeader.Header.Get("Content-Type")); err != nil {
		_ = stage.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = stage.Close()
	_ = tasks
	c.JSON(http.StatusOK, createdFile)
}

func uploadChunk(c *gin.Context, cfg *config.Config, tasks *service.TaskService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	_ = result
	taskID := c.Param("taskId")
	idx, err := strconv.Atoi(c.Param("idx"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chunk index"})
		return
	}
	if idx < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chunk index"})
		return
	}
	fileHeader, err := c.FormFile("chunk")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "chunk is required"})
		return
	}
	reader, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	defer reader.Close()
	if _, err := service.CopyStreamToChunk(cfg.Storage.TempDir, taskID, idx, reader); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := tasks.UpdateUploadedChunk(taskID, idx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func completeUpload(c *gin.Context, cfg *config.Config, files *service.FileService, tasks *service.TaskService, bus *eventbus.Bus) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	taskID := c.Param("taskId")
	task, err := tasks.GetUploadTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if task.AccountID != uuid.MustParse(result.Account.GetId()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if task.ChunksCount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task has no chunks"})
		return
	}
	chunkDir := filepath.Join(cfg.Storage.TempDir, taskID)
	mergedPath := filepath.Join(cfg.Storage.TempDir, taskID+".merged")
	if err := files.MergeChunks(taskID, chunkDir, mergedPath, task.ChunksCount, nil); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	object, err := files.DetectAndCreateObject(mergedPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx := service.AccessContext{Account: result.Account, Session: result.Session}
	_ = ctx
	storageKey := &object.ID
	created, err := files.CreateUploadedFile(task.AccountID, deref(task.FileName), object.ID, task.PoolID, task.ApplicationType, storageKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	stage, err := os.Open(mergedPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := files.Storage().Put(c.Request.Context(), object.ID, stage, object.MimeType); err != nil {
		_ = stage.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = stage.Close()
	if err := bus.PublishFileUploaded(c.Request.Context(), eventbus.FileUploadedEvent{FileID: created.ID, TaskID: task.TaskID, ContentType: object.MimeType, ProcessingFilePath: mergedPath, IsTempFile: true}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = tasks.MarkCompleted(task.TaskID)
	c.JSON(http.StatusOK, created)
}

func listUploadTasks(c *gin.Context, tasks *service.TaskService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	items, err := tasks.ListTasks(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Total", strconv.Itoa(len(items)))
	c.JSON(http.StatusOK, items)
}

func uploadProgress(c *gin.Context, tasks *service.TaskService) {
	task, err := tasks.GetUploadTask(c.Param("taskId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": task.TaskID, "progress": task.Progress})
}

func uploadResume(c *gin.Context, tasks *service.TaskService) {
	task, err := tasks.GetUploadTask(c.Param("taskId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, task)
}

func cancelUpload(c *gin.Context, tasks *service.TaskService) {
	if err := tasks.FailTask(c.Param("taskId"), "cancelled"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func uploadStats(c *gin.Context, tasks *service.TaskService) {
	c.JSON(http.StatusOK, gin.H{"total_tasks": 0, "in_progress_tasks": 0})
}
func cleanupTasks(c *gin.Context, tasks *service.TaskService) {
	c.JSON(http.StatusOK, gin.H{"count": 0})
}
func recentTasks(c *gin.Context, tasks *service.TaskService) {
	c.JSON(http.StatusOK, gin.H{"items": []any{}})
}
func taskDetails(c *gin.Context, cfg *config.Config, tasks *service.TaskService) {
	c.JSON(http.StatusOK, gin.H{"task": nil})
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
