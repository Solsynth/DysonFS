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

func RegisterRoutes(r *gin.Engine, cfg *config.Config, files *service.FileService, tasks *service.TaskService, quota *service.QuotaService, bus *eventbus.Bus) {
	if bus != nil {
		r.Use(func(c *gin.Context) {
			c.Set("bus", bus)
			c.Next()
		})
	}
	f := r.Group("/api/files")
	{
		f.GET("/:id/info", func(c *gin.Context) { fileInfo(c, files) })
		f.GET("/:id/open", func(c *gin.Context) { openFile(c, cfg, files) })
		f.GET("/:id/references", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"items": []any{}}) })
		f.GET("/:id/e2ee", func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"code": "file.e2ee_not_found"}) })
		f.GET("/root/children", func(c *gin.Context) { listRoot(c, files) })
		f.GET("/children/:id", func(c *gin.Context) { listChildren(c, files) })
		f.POST("/folders", func(c *gin.Context) { createFolder(c, files) })
		f.GET("/me", func(c *gin.Context) { listRoot(c, files) })
		f.POST("/batches/delete", func(c *gin.Context) { batchRecycleFiles(c, files, bus) })
		f.DELETE("/:id", func(c *gin.Context) { deleteFile(c, files, bus) })
		f.DELETE("/me/recycle", func(c *gin.Context) { purgeMyRecycleBin(c, files, bus) })
		f.DELETE("/recycle", func(c *gin.Context) { purgeMyRecycleBin(c, files, bus) })
		f.POST("/:id/recycle", func(c *gin.Context) { recycleFile(c, files, bus) })
		f.POST("/:id/restore", func(c *gin.Context) { restoreFile(c, files, bus) })
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
		b.GET("quota", func(c *gin.Context) { getQuota(c, quota) })
		b.GET("quota/records", func(c *gin.Context) { listQuotaRecords(c, quota) })
		b.GET("usage", func(c *gin.Context) { getUsage(c, quota) })
		b.GET("usage/:poolId", func(c *gin.Context) { getPoolUsage(c, quota) })
	}

	r.NoRoute(func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"error": "not found"}) })
}

// @Summary Get file info
// @Tags files
// @Produce json
// @Param id path string true "File ID"
// @Success 200 {object} database.CloudFile
// @Failure 404 {object} map[string]any
// @Router /api/files/{id}/info [get]
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

// @Summary Open file
// @Tags files
// @Produce json
// @Param id path string true "File ID"
// @Param download query bool false "Download"
// @Success 307
// @Failure 404 {object} map[string]any
// @Router /api/files/{id}/open [get]
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
	parent, err := files.GetFile(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	result, _, ok := auth.GetAuth(c)
	if !ok && !files.CanAccessFile(nil, nil, parent, "read") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	items, err := files.GetChildren(c.Param("id"))
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

// @Summary Get quota summary
// @Tags billing
// @Produce json
// @Success 200 {object} service.QuotaSummary
// @Router /api/billing/quota [get]
func getQuota(c *gin.Context, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	summary, err := quota.GetSummary(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, summary)
}

// @Summary List quota records
// @Tags billing
// @Produce json
// @Success 200 {array} database.QuotaRecord
// @Router /api/billing/quota/records [get]
func listQuotaRecords(c *gin.Context, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	records, err := quota.ListRecords(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Total", strconv.Itoa(len(records)))
	c.JSON(http.StatusOK, records)
}

// @Summary Get quota usage
// @Tags billing
// @Produce json
// @Success 200 {object} service.QuotaSummary
// @Router /api/billing/usage [get]
func getUsage(c *gin.Context, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	summary, err := quota.GetUsage(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, summary)
}

// @Summary Get pool usage
// @Tags billing
// @Produce json
// @Param poolId path string true "Pool ID"
// @Success 200 {object} map[string]any
// @Router /api/billing/usage/{poolId} [get]
func getPoolUsage(c *gin.Context, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	usage, err := quota.GetPoolUsage(uuid.MustParse(result.Account.GetId()), c.Param("poolId"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, usage)
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

func deleteFile(c *gin.Context, files *service.FileService, bus *eventbus.Bus) {
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
	if bus != nil {
		_ = bus.PublishFileAction(c.Request.Context(), eventbus.FileActionEvent{Action: "delete", FileID: file.ID, AccountID: result.Account.GetId(), Name: file.Name})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func recycleFile(c *gin.Context, files *service.FileService, bus *eventbus.Bus) {
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
	if file.AccountID.String() != result.Account.GetId() {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if err := files.RecycleFile(file.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if bus != nil {
		_ = bus.PublishFileAction(c.Request.Context(), eventbus.FileActionEvent{Action: "recycle", FileID: file.ID, AccountID: result.Account.GetId(), Name: file.Name})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func restoreFile(c *gin.Context, files *service.FileService, bus *eventbus.Bus) {
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
	if file.AccountID.String() != result.Account.GetId() {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if err := files.RestoreFile(file.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if bus != nil {
		_ = bus.PublishFileAction(c.Request.Context(), eventbus.FileActionEvent{Action: "restore", FileID: file.ID, AccountID: result.Account.GetId(), Name: file.Name})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func batchRecycleFiles(c *gin.Context, files *service.FileService, bus *eventbus.Bus) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req struct { IDs []string `json:"ids"` }
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	count, err := files.RecycleBatch(req.IDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if bus != nil {
		for _, id := range req.IDs {
			_ = bus.PublishFileAction(c.Request.Context(), eventbus.FileActionEvent{Action: "recycle", FileID: id, AccountID: result.Account.GetId()})
		}
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

func purgeMyRecycleBin(c *gin.Context, files *service.FileService, bus *eventbus.Bus) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	count, err := files.PurgeRecycleBin(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if bus != nil {
		_ = bus.PublishFileAction(c.Request.Context(), eventbus.FileActionEvent{Action: "purge", AccountID: result.Account.GetId()})
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

// @Summary Create upload task
// @Tags uploads
// @Accept json
// @Produce json
// @Success 200 {object} map[string]any
// @Router /api/files/upload/create [post]
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

// @Summary Direct upload
// @Tags uploads
// @Produce json
// @Success 200 {object} database.CloudFile
// @Router /api/files/upload/direct [post]
func directUpload(c *gin.Context, cfg *config.Config, files *service.FileService, tasks *service.TaskService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
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

// @Summary Complete upload
// @Tags uploads
// @Produce json
// @Param taskId path string true "Task ID"
// @Success 200 {object} database.CloudFile
// @Router /api/files/upload/complete/{taskId} [post]
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
