package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/dispatch"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/logging"
	"src.solsynth.dev/sosys/filesystem/internal/service"
	"src.solsynth.dev/sosys/go/pkg/auth"
	gen "src.solsynth.dev/sosys/go/proto"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"
)

func RegisterRoutes(r *gin.Engine, cfg *config.Config, files *service.FileService, wopi *service.WOPIService, tasks *service.TaskService, quota *service.QuotaService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
	if bus != nil {
		r.Use(func(c *gin.Context) {
			c.Set("bus", bus)
			c.Next()
		})
	}
	f := r.Group("/api/files")
	{
		f.GET("/:id", func(c *gin.Context) { openFile(c, cfg, files) })
		f.GET("/:id/info", func(c *gin.Context) { fileInfo(c, files) })
		f.GET("/:id/breadcrumb", func(c *gin.Context) { fileBreadcrumb(c, files) })
		f.GET("/:id/open", func(c *gin.Context) { openFile(c, cfg, files) })
		f.GET("/:id/references", func(c *gin.Context) { c.JSON(http.StatusOK, []any{}) })
		f.POST("/:id/edit", func(c *gin.Context) { createEditSession(c, wopi, files) })
		f.GET("/root/children", func(c *gin.Context) { listRootIndexed(c, files) })
		f.GET("/:id/children", func(c *gin.Context) { listChildren(c, files) })
		f.POST("/folders", func(c *gin.Context) { createFolder(c, files) })
		f.GET("/me", func(c *gin.Context) { listRootOwned(c, files) })
		f.GET("/unindexed", func(c *gin.Context) { listUnindexed(c, files) })
		f.PATCH("/:id", func(c *gin.Context) { patchFile(c, files) })
		f.PATCH("/:id/content", func(c *gin.Context) { patchFileContent(c, files, bus, dispatcher) })
		f.POST("/recycle/batch", func(c *gin.Context) { batchRecycleFiles(c, files, bus, dispatcher) })
		f.POST("/restore/batch", func(c *gin.Context) { batchRestoreFiles(c, files, bus, dispatcher) })
		f.POST("/delete/batch", func(c *gin.Context) { batchDeleteFiles(c, files, bus, dispatcher) })
		f.POST("/move/batch", func(c *gin.Context) { batchMoveFiles(c, files, bus, dispatcher) })
		f.DELETE("/:id", func(c *gin.Context) { deleteFile(c, files, bus, dispatcher) })
		f.DELETE("/me/recycle", func(c *gin.Context) { purgeMyRecycleBin(c, files, bus, dispatcher) })
		f.DELETE("/recycle", func(c *gin.Context) { purgeMyRecycleBin(c, files, bus, dispatcher) })
		f.POST("/:id/recycle", func(c *gin.Context) { recycleFile(c, files, bus, dispatcher) })
		f.POST("/:id/restore", func(c *gin.Context) { restoreFile(c, files, bus, dispatcher) })
		f.GET("/:id/permissions", func(c *gin.Context) { getFilePermissions(c, files) })
		f.PUT("/:id/permissions", func(c *gin.Context) { updateFilePermissions(c, files) })
	}

	w := r.Group("/wopi")
	{
		w.GET("/files/:id", func(c *gin.Context) { wopiCheckFileInfo(c, wopi) })
		w.GET("/files/:id/contents", func(c *gin.Context) { wopiGetFile(c, wopi) })
		w.POST("/files/:id/contents", func(c *gin.Context) { wopiPutFile(c, wopi) })
		w.POST("/files/:id", func(c *gin.Context) { wopiLock(c, wopi) })
	}

	u := r.Group("/api/files/upload")
	{
		u.POST("/create", func(c *gin.Context) { createUploadTask(c, cfg, files, tasks, quota) })
		u.POST("/direct", func(c *gin.Context) { directUpload(c, cfg, files, tasks, quota, bus, dispatcher) })
		u.POST("/chunk/:taskId/:idx", func(c *gin.Context) { uploadChunk(c, cfg, tasks) })
		u.POST("/complete/:taskId", func(c *gin.Context) { completeUpload(c, cfg, files, tasks, bus, dispatcher) })
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
		p.POST("", func(c *gin.Context) { createPool(c, files) })
		p.GET("/me", func(c *gin.Context) { listOwnedPools(c, files) })
		p.GET("/:id", func(c *gin.Context) { getPool(c, files) })
		p.PATCH("/:id", func(c *gin.Context) { updatePool(c, files) })
		p.DELETE("/:id", func(c *gin.Context) { deletePool(c, files) })
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

	if cfg.WebDAV.Enabled {
		prefix := cfg.WebDAV.Prefix
		if prefix == "" {
			prefix = "/webdav"
		}
		r.Any(prefix+"/*path", func(c *gin.Context) {
			handleWebDAV(c, files, bus, dispatcher, prefix)
		})
		r.Any(prefix, func(c *gin.Context) {
			c.Redirect(http.StatusMovedPermanently, prefix+"/")
		})

		t := r.Group("/api/webdav/tokens")
		{
			t.POST("", func(c *gin.Context) { createWebDAVToken(c, files) })
			t.GET("", func(c *gin.Context) { listWebDAVTokens(c, files) })
			t.DELETE("/:id", func(c *gin.Context) { deleteWebDAVToken(c, files) })
		}
	}

	s3t := r.Group("/api/s3/tokens")
	{
		s3t.POST("", func(c *gin.Context) { createS3Token(c, files) })
		s3t.GET("", func(c *gin.Context) { listS3Tokens(c, files) })
		s3t.DELETE("/:id", func(c *gin.Context) { deleteS3Token(c, files) })
	}

	sn := r.Group("/api/storage-nodes")
	{
		sn.POST("/register", func(c *gin.Context) { registerStorageNode(c, files) })
		sn.GET("", func(c *gin.Context) { listStorageNodes(c, files) })
		sn.GET("/:id", func(c *gin.Context) { getStorageNode(c, files) })
		sn.PATCH("/:id", func(c *gin.Context) { updateStorageNode(c, files) })
		sn.DELETE("/:id", func(c *gin.Context) { deleteStorageNode(c, files) })
		sn.POST("/heartbeat/:machineId", func(c *gin.Context) { storageNodeHeartbeat(c, files) })
	}

	dfs := r.Group("/_dfs")
	{
		dfs.GET("/version", func(c *gin.Context) { StorageNodeVersion(c, cfg.StorageNode) })
		dfs.GET("/identity", func(c *gin.Context) { StorageNodeIdentity(c, cfg.StorageNode) })
		dfs.POST("/auth/validate", func(c *gin.Context) { StorageNodeAuthValidate(c, cfg.StorageNode) })
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

type breadcrumbItem struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	ParentID *string `json:"parent_id"`
	IsFolder bool    `json:"is_folder"`
}

func fileBreadcrumb(c *gin.Context, files *service.FileService) {
	file, err := files.GetFile(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	result, _, ok := auth.GetAuth(c)
	if !ok && !files.CanAccessFile(nil, nil, file, "read") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if ok && !files.CanAccessFile(result.Account, result.Session, file, "read") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	items, err := files.GetBreadcrumb(file.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	resp := make([]breadcrumbItem, 0, len(items))
	for _, item := range items {
		resp = append(resp, breadcrumbItem{
			ID:       item.ID,
			Name:     item.Name,
			ParentID: item.ParentID,
			IsFolder: item.IsFolder,
		})
	}
	c.JSON(http.StatusOK, resp)
}

func createEditSession(c *gin.Context, wopi *service.WOPIService, files *service.FileService) {
	if wopi == nil || !wopi.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "wopi is disabled"})
		return
	}
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
	if !files.CanAccessFile(result.Account, result.Session, file, "read") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	session, err := wopi.CreateSession(c.Request.Context(), file.ID, result.Account, result.Session)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, service.ErrWOPIUnsupportedFile):
			status = http.StatusBadRequest
		case errors.Is(err, service.ErrWOPIUnauthorized):
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, session)
}

func wopiCheckFileInfo(c *gin.Context, wopi *service.WOPIService) {
	claims, ok := authenticateWOPIRequest(c, wopi)
	if !ok {
		return
	}
	info, err := wopi.CheckFileInfo(c.Request.Context(), c.Param("id"), claims)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, info)
}

func wopiGetFile(c *gin.Context, wopi *service.WOPIService) {
	_, ok := authenticateWOPIRequest(c, wopi)
	if !ok {
		return
	}
	reader, contentType, err := wopi.OpenContents(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer reader.Close()
	c.Header("Content-Type", contentType)
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, reader)
}

func wopiPutFile(c *gin.Context, wopi *service.WOPIService) {
	claims, ok := authenticateWOPIRequest(c, wopi)
	if !ok {
		return
	}
	updated, err := wopi.SaveContents(c.Request.Context(), c.Param("id"), claims, c.GetHeader("X-WOPI-Lock"), c.Request.Body, c.GetHeader("Content-Type"))
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, service.ErrWOPIUnauthorized):
			status = http.StatusForbidden
		case errors.Is(err, service.ErrWOPIConflict):
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-WOPI-ItemVersion", fileVersionHeader(updated))
	size := int64(0)
	if updated.Object != nil {
		size = updated.Object.Size
	}
	c.JSON(http.StatusOK, gin.H{"Name": updated.Name, "Size": size})
}

func wopiLock(c *gin.Context, wopi *service.WOPIService) {
	claims, ok := authenticateWOPIRequest(c, wopi)
	if !ok {
		return
	}
	result, err := wopi.HandleLock(
		c.Request.Context(),
		c.Param("id"),
		claims,
		c.GetHeader("X-WOPI-Override"),
		c.GetHeader("X-WOPI-Lock"),
		firstNonEmpty(c.GetHeader("X-WOPI-OldLock"), c.GetHeader("X-WOPI-Oldlock")),
	)
	if result != nil && strings.TrimSpace(result.CurrentLock) != "" {
		c.Header("X-WOPI-Lock", result.CurrentLock)
	}
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, service.ErrWOPIUnauthorized):
			status = http.StatusForbidden
		case errors.Is(err, service.ErrWOPIConflict):
			status = http.StatusConflict
		case errors.Is(err, service.ErrWOPIInvalidLockOperation):
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusOK)
}

func authenticateWOPIRequest(c *gin.Context, wopi *service.WOPIService) (*service.WOPITokenClaims, bool) {
	if wopi == nil || !wopi.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "wopi is disabled"})
		return nil, false
	}
	rawToken := strings.TrimSpace(c.Query("access_token"))
	if rawToken == "" {
		rawToken = strings.TrimSpace(c.PostForm("access_token"))
	}
	if rawToken == "" {
		rawToken = bearerToken(c.GetHeader("Authorization"))
	}
	if rawToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing access token"})
		return nil, false
	}
	claims, err := wopi.AuthenticateToken(rawToken, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid access token"})
		return nil, false
	}
	return claims, true
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" || len(header) < 7 || !strings.EqualFold(header[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

// @Summary Open file
// @Tags files
// @Produce json
// @Param id path string true "File ID"
// @Param download query bool false "Download"
// @Param original query bool false "Prefer original source object"
// @Param thumbnail query bool false "Prefer thumbnail variant"
// @Success 307
// @Failure 404 {object} map[string]any
// @Router /api/files/{id} [get]
// @Router /api/files/{id}/open [get]
func openFile(c *gin.Context, cfg *config.Config, files *service.FileService) {
	file, err := files.GetFile(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if variant := c.Query("thumbnail"); strings.EqualFold(variant, "1") || strings.EqualFold(variant, "true") {
		if thumb, err := resolveOpenVariant(c.Request.Context(), files, file, "system.thumbnail"); err == nil {
			file = thumb
		} else {
			c.JSON(http.StatusNotFound, gin.H{"error": "thumbnail not available"})
			return
		}
	} else if variant := c.Query("original"); strings.EqualFold(variant, "1") || strings.EqualFold(variant, "true") {
		if isDerivedVariant(file) && file.ParentID != nil {
			if parent, err := files.GetFile(*file.ParentID); err == nil {
				file = parent
			}
		}
	} else if file.Object != nil && strings.HasPrefix(file.Object.MimeType, "image/") {
		if compressed, err := resolveOpenVariant(c.Request.Context(), files, file, "system.compression.low"); err == nil {
			file = compressed
		}
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

func isDerivedVariant(file *database.CloudFile) bool {
	if file == nil || file.ApplicationType == nil {
		return false
	}
	switch *file.ApplicationType {
	case "system.thumbnail", "system.compression.low":
		return true
	default:
		return false
	}
}

func resolveDerivedFile(files *service.FileService, parentID, kind string) (*database.CloudFile, error) {
	children, err := files.GetChildren(parentID)
	if err != nil {
		return nil, err
	}
	for i := range children {
		child := &children[i]
		if child.ApplicationType != nil && *child.ApplicationType == kind {
			return child, nil
		}
	}
	return nil, fmt.Errorf("derived file %s not found", kind)
}

func resolveOpenVariant(ctx context.Context, files *service.FileService, file *database.CloudFile, kind string) (*database.CloudFile, error) {
	if derived, err := resolveDerivedFile(files, file.ID, kind); err == nil {
		normalizeDerivedStorageKey(file.ID, derived, kind)
		ok, err := derivedVariantAvailable(ctx, files, derived)
		if err != nil {
			return nil, err
		}
		if ok {
			return derived, nil
		}
	}
	if legacy := legacyDerivedFile(file, kind); legacy != nil {
		ok, err := derivedVariantAvailable(ctx, files, legacy)
		if err != nil {
			return nil, err
		}
		if ok {
			return legacy, nil
		}
	}
	return nil, fmt.Errorf("derived file %s not found", kind)
}

func derivedVariantAvailable(ctx context.Context, files *service.FileService, file *database.CloudFile) (bool, error) {
	if files == nil || file == nil || file.StorageKey == nil || strings.TrimSpace(*file.StorageKey) == "" {
		return false, nil
	}
	backend, err := files.BackendForFile(file)
	if err != nil {
		return false, err
	}
	if _, err := backend.Stat(ctx, *file.StorageKey); err != nil {
		if isMissingStorageError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func isMissingStorageError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "not found") || strings.Contains(text, "no such file") || strings.Contains(text, "no such key") || strings.Contains(text, "object does not exist") {
		return true
	}
	return errors.Is(err, os.ErrNotExist)
}

func normalizeDerivedStorageKey(parentID string, file *database.CloudFile, kind string) {
	if file == nil {
		return
	}

	var suffix string
	switch kind {
	case "system.thumbnail":
		suffix = ".thumbnail"
	case "system.compression.low":
		suffix = ".compressed"
	default:
		return
	}

	legacyKey := parentID + suffix
	if file.ObjectID != nil {
		wrongKey := *file.ObjectID + suffix
		if file.StorageKey != nil && *file.StorageKey == wrongKey {
			file.StorageKey = &legacyKey
		}
		if file.Object != nil && file.Object.StorageKey != nil && *file.Object.StorageKey == wrongKey {
			file.Object.StorageKey = &legacyKey
		}
	}
	if file.StorageKey == nil && file.Object != nil && file.Object.StorageKey != nil {
		file.StorageKey = file.Object.StorageKey
	}
}

func legacyDerivedFile(file *database.CloudFile, kind string) *database.CloudFile {
	if file == nil || file.Object == nil {
		return nil
	}

	var suffix string
	switch kind {
	case "system.thumbnail":
		if !file.Object.HasThumbnail {
			return nil
		}
		suffix = ".thumbnail"
	case "system.compression.low":
		if !file.Object.HasCompression {
			return nil
		}
		suffix = ".compressed"
	default:
		return nil
	}

	legacy := *file
	storageKey := file.ID + suffix
	legacy.StorageKey = &storageKey
	return &legacy
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
	filters := parseListQuery(c, 0, 50)
	items, err := files.GetChildren(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	items = filterAndSortFiles(items, filters)
	total := len(items)
	items = paginateFiles(items, filters.Offset, filters.Take)
	filtered := make([]database.CloudFile, 0, len(items))
	for _, item := range items {
		if !ok || files.CanAccessFile(result.Account, result.Session, &item, "read") {
			filtered = append(filtered, item)
		}
	}
	c.Header("X-Total", strconv.Itoa(total))
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
	c.JSON(http.StatusOK, items)
}

func listOwnedPools(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	items, err := files.ListOwnedPools(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Total", strconv.Itoa(len(items)))
	c.JSON(http.StatusOK, items)
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
	account, err := quota.EnrichedAccount(c.Request.Context(), result.Account)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	quotaLogEvent(logging.Log.Info(), account).
		Msg("quota endpoint accessed")
	summary, err := quota.GetSummary(account)
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
// @Success 200 {object} service.UsageSummary
// @Router /api/billing/usage [get]
func getUsage(c *gin.Context, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	summary, err := quota.GetUsage(result.Account)
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
	c.JSON(http.StatusOK, perms)
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
	c.JSON(http.StatusOK, perms)
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

func listRootIndexed(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	filters := parseListQuery(c, 0, 50)
	items, err := files.ListRoot(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items = filterAndSortFiles(items, filters)
	total := len(items)
	items = paginateFiles(items, filters.Offset, filters.Take)
	c.Header("X-Total", strconv.Itoa(total))
	c.JSON(http.StatusOK, items)
}

func listRootOwned(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	filters := parseListQuery(c, 0, 20)
	items, err := files.ListOwned(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items = filterAndSortFiles(items, filters)
	items = rootOnly(items)
	total := len(items)
	items = paginateFiles(items, filters.Offset, filters.Take)
	c.Header("X-Total", strconv.Itoa(total))
	c.JSON(http.StatusOK, items)
}

func listUnindexed(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	pool := strings.TrimSpace(c.Query("pool"))
	recycled := strings.EqualFold(c.Query("recycled"), "true") || c.Query("recycled") == "1"
	filters := parseListQuery(c, 0, 20)
	items, err := files.ListUnindexed(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items = filterAndSortUnindexed(items, pool, recycled, filters)
	total := len(items)
	items = paginateFiles(items, filters.Offset, filters.Take)
	c.Header("X-Total", strconv.Itoa(total))
	c.JSON(http.StatusOK, items)
}

type fileListFilters struct {
	Offset          int
	Take            int
	Query           string
	Name            string
	Extension       string
	Order           string
	OrderDesc       bool
	Usage           string
	ApplicationType string
	ContentType     string
	PoolID          string
	ParentID        string
	Indexed         *bool
	Recycled        *bool
	IsFolder        *bool
	HasThumbnail    *bool
	HasCompression  *bool
	MinSize         *int64
	MaxSize         *int64
	CreatedAfter    *time.Time
	CreatedBefore   *time.Time
	UpdatedAfter    *time.Time
	UpdatedBefore   *time.Time
}

func parseListQuery(c *gin.Context, defaultOffset, defaultTake int) fileListFilters {
	filters := fileListFilters{
		Offset:          defaultOffset,
		Take:            defaultTake,
		Name:            strings.TrimSpace(c.Query("name")),
		Extension:       strings.TrimPrefix(strings.ToLower(strings.TrimSpace(c.Query("extension"))), "."),
		Order:           strings.TrimSpace(c.Query("order")),
		OrderDesc:       true,
		Usage:           strings.TrimSpace(c.Query("usage")),
		ApplicationType: strings.TrimSpace(c.Query("application_type")),
		ContentType:     strings.TrimSpace(c.Query("content_type")),
		PoolID:          strings.TrimSpace(firstNonEmptyQuery(c, "pool_id", "pool")),
		ParentID:        strings.TrimSpace(c.Query("parent_id")),
	}
	if filters.ContentType == "" {
		filters.ContentType = strings.TrimSpace(c.Query("mime_type"))
	}
	if filters.Order == "" {
		filters.Order = "date"
	}
	if v := strings.TrimSpace(c.Query("offset")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			filters.Offset = n
		}
	}
	if v := strings.TrimSpace(c.Query("take")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filters.Take = n
		}
	}
	if v := strings.TrimSpace(c.Query("query")); v != "" {
		filters.Query = v
	}
	if v := strings.TrimSpace(c.Query("orderDesc")); v != "" {
		filters.OrderDesc = !(strings.EqualFold(v, "false") || v == "0")
	}
	filters.Indexed = parseOptionalBool(c, "indexed")
	filters.Recycled = parseOptionalBool(c, "recycled")
	filters.IsFolder = parseOptionalBool(c, "is_folder")
	filters.HasThumbnail = parseOptionalBool(c, "has_thumbnail")
	filters.HasCompression = parseOptionalBool(c, "has_compression")
	filters.MinSize = parseOptionalInt64(c, "min_size")
	filters.MaxSize = parseOptionalInt64(c, "max_size")
	filters.CreatedAfter = parseOptionalTime(c, "created_after")
	filters.CreatedBefore = parseOptionalTime(c, "created_before")
	filters.UpdatedAfter = parseOptionalTime(c, "updated_after")
	filters.UpdatedBefore = parseOptionalTime(c, "updated_before")
	return filters
}

func filterAndSortFiles(items []database.CloudFile, filters fileListFilters) []database.CloudFile {
	filtered := make([]database.CloudFile, 0, len(items))
	for _, item := range items {
		if !matchesFileFilters(item, filters) {
			continue
		}
		filtered = append(filtered, item)
	}
	sortFiles(filtered, filters.Order, filters.OrderDesc)
	return filtered
}

func rootOnly(items []database.CloudFile) []database.CloudFile {
	filtered := make([]database.CloudFile, 0, len(items))
	for _, item := range items {
		if item.ParentID == nil {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func filterAndSortUnindexed(items []database.CloudFile, pool string, recycled bool, filters fileListFilters) []database.CloudFile {
	filtered := make([]database.CloudFile, 0, len(items))
	for _, item := range items {
		if item.Indexed || item.IsFolder {
			continue
		}
		if pool != "" && filters.PoolID == "" {
			filters.PoolID = pool
		}
		if filters.Recycled == nil {
			filters.Recycled = &recycled
		}
		if !matchesFileFilters(item, filters) {
			continue
		}
		filtered = append(filtered, item)
	}
	sortFiles(filtered, filters.Order, filters.OrderDesc)
	return filtered
}

func matchesFileFilters(item database.CloudFile, filters fileListFilters) bool {
	if filters.Query != "" && !strings.Contains(strings.ToLower(item.Name), strings.ToLower(filters.Query)) {
		return false
	}
	if filters.Name != "" && !strings.EqualFold(strings.TrimSpace(item.Name), filters.Name) {
		return false
	}
	if filters.Extension != "" {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Name)), ".")
		if ext != filters.Extension {
			return false
		}
	}
	if filters.Usage != "" {
		if item.Usage == nil || !strings.EqualFold(strings.TrimSpace(*item.Usage), filters.Usage) {
			return false
		}
	}
	if filters.ApplicationType != "" {
		if item.ApplicationType == nil || !strings.EqualFold(strings.TrimSpace(*item.ApplicationType), filters.ApplicationType) {
			return false
		}
	}
	if filters.ContentType != "" && !strings.EqualFold(strings.TrimSpace(item.ResponseMimeType()), filters.ContentType) {
		return false
	}
	if filters.PoolID != "" {
		if item.PoolID == nil || !strings.EqualFold(strings.TrimSpace(*item.PoolID), filters.PoolID) {
			return false
		}
	}
	if filters.ParentID != "" {
		if item.ParentID == nil || !strings.EqualFold(strings.TrimSpace(*item.ParentID), filters.ParentID) {
			return false
		}
	}
	if filters.Indexed != nil && item.Indexed != *filters.Indexed {
		return false
	}
	if filters.Recycled != nil && item.IsMarkedRecycle != *filters.Recycled {
		return false
	}
	if filters.IsFolder != nil && item.IsFolder != *filters.IsFolder {
		return false
	}
	if filters.HasThumbnail != nil {
		hasThumbnail := item.Object != nil && item.Object.HasThumbnail
		if hasThumbnail != *filters.HasThumbnail {
			return false
		}
	}
	if filters.HasCompression != nil {
		hasCompression := item.Object != nil && item.Object.HasCompression
		if hasCompression != *filters.HasCompression {
			return false
		}
	}
	size := int64(0)
	if item.Object != nil {
		size = item.Object.Size
	}
	if filters.MinSize != nil && size < *filters.MinSize {
		return false
	}
	if filters.MaxSize != nil && size > *filters.MaxSize {
		return false
	}
	if filters.CreatedAfter != nil && item.CreatedAt.Before(*filters.CreatedAfter) {
		return false
	}
	if filters.CreatedBefore != nil && item.CreatedAt.After(*filters.CreatedBefore) {
		return false
	}
	if filters.UpdatedAfter != nil && item.UpdatedAt.Before(*filters.UpdatedAfter) {
		return false
	}
	if filters.UpdatedBefore != nil && item.UpdatedAt.After(*filters.UpdatedBefore) {
		return false
	}
	return true
}

func firstNonEmptyQuery(c *gin.Context, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(c.Query(key)); value != "" {
			return value
		}
	}
	return ""
}

func parseOptionalBool(c *gin.Context, key string) *bool {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return nil
	}
	parsed := strings.EqualFold(value, "true") || value == "1"
	return &parsed
}

func parseOptionalInt64(c *gin.Context, key string) *int64 {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func parseOptionalTime(c *gin.Context, key string) *time.Time {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return nil
	}
	layouts := []string{time.RFC3339, "2006-01-02"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return &parsed
		}
	}
	return nil
}

func sortFiles(items []database.CloudFile, order string, orderDesc bool) {
	sort.SliceStable(items, func(i, j int) bool {
		less := func() bool {
			switch strings.ToLower(order) {
			case "name":
				return items[i].Name < items[j].Name
			case "size":
				iSize := int64(0)
				jSize := int64(0)
				if items[i].Object != nil {
					iSize = items[i].Object.Size
				}
				if items[j].Object != nil {
					jSize = items[j].Object.Size
				}
				return iSize < jSize
			default:
				return items[i].CreatedAt.Before(items[j].CreatedAt)
			}
		}
		if orderDesc {
			return !less()
		}
		return less()
	})
}

func paginateFiles(items []database.CloudFile, offset, take int) []database.CloudFile {
	if offset < 0 {
		offset = 0
	}
	if take <= 0 || offset >= len(items) {
		if offset >= len(items) {
			return []database.CloudFile{}
		}
		return items
	}
	end := offset + take
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
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

func patchFile(c *gin.Context, files *service.FileService) {
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
	if !result.Account.GetIsSuperuser() && file.AccountID.String() != result.Account.GetId() {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	var req struct {
		Name *string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	name := strings.TrimSpace(*req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	if err := files.DB().Model(&database.CloudFile{}).Where("id = ?", file.ID).Update("name", name).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	file.Name = name
	file, err = files.GetFile(file.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	file.Name = name
	file.PermissionStatus.Writable = true
	c.JSON(http.StatusOK, file)
}

func patchFileContent(c *gin.Context, files *service.FileService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	fileID := c.Param("id")
	file, err := files.GetFile(fileID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !files.CanAccessFile(result.Account, result.Session, file, "write") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	lockToken := strings.TrimSpace(c.GetHeader("X-Lock-Token"))

	updated, err := files.ApplyPatch(c.Request.Context(), fileID, c.Request.Body, lockToken)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotLocked):
			c.JSON(http.StatusLocked, gin.H{"error": err.Error()})
		case errors.Is(err, service.ErrPatchFailed):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	publishFileUploaded(c.Request.Context(), bus, dispatcher, eventbus.FileUploadedEvent{
		FileID: updated.ID,
	})

	c.JSON(http.StatusOK, updated)
}

func deleteFile(c *gin.Context, files *service.FileService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
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
	if err := files.PurgeFile(file.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = bus
	_ = dispatcher
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func recycleFile(c *gin.Context, files *service.FileService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
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
	publishFileAction(c.Request.Context(), bus, dispatcher, eventbus.FileActionEvent{Action: "recycle", FileID: file.ID, AccountID: result.Account.GetId(), Name: file.Name})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func restoreFile(c *gin.Context, files *service.FileService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
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
	publishFileAction(c.Request.Context(), bus, dispatcher, eventbus.FileActionEvent{Action: "restore", FileID: file.ID, AccountID: result.Account.GetId(), Name: file.Name})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func batchRecycleFiles(c *gin.Context, files *service.FileService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req batchFileIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ids := req.normalizedIDs()
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_ids is required"})
		return
	}
	batchFiles, err := loadBatchFilesForAccount(files, result.Account.GetId(), ids, false)
	if err != nil {
		handleBatchFileLookupError(c, err)
		return
	}
	count, err := files.RecycleBatch(ids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, file := range batchFiles {
		publishFileAction(c.Request.Context(), bus, dispatcher, eventbus.FileActionEvent{Action: "recycle", FileID: file.ID, AccountID: result.Account.GetId(), Name: file.Name})
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

func batchRestoreFiles(c *gin.Context, files *service.FileService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req batchFileIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ids := req.normalizedIDs()
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_ids is required"})
		return
	}
	batchFiles, err := loadBatchFilesForAccount(files, result.Account.GetId(), ids, false)
	if err != nil {
		handleBatchFileLookupError(c, err)
		return
	}
	count, err := files.RestoreBatch(ids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, file := range batchFiles {
		publishFileAction(c.Request.Context(), bus, dispatcher, eventbus.FileActionEvent{Action: "restore", FileID: file.ID, AccountID: result.Account.GetId(), Name: file.Name})
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

func batchDeleteFiles(c *gin.Context, files *service.FileService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req batchFileIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ids := req.normalizedIDs()
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_ids is required"})
		return
	}
	batchFiles, err := loadBatchFilesForAccount(files, result.Account.GetId(), ids, true)
	if err != nil {
		handleBatchFileLookupError(c, err)
		return
	}
	count := int64(0)
	for _, file := range batchFiles {
		if err := files.PurgeFile(file.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		publishFileAction(c.Request.Context(), bus, dispatcher, eventbus.FileActionEvent{Action: "delete", FileID: file.ID, AccountID: result.Account.GetId(), Name: file.Name})
		count++
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

func batchMoveFiles(c *gin.Context, files *service.FileService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req batchMoveFilesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ids := req.normalizedIDs()
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_ids is required"})
		return
	}
	batchFiles, err := loadBatchFilesForAccount(files, result.Account.GetId(), ids, false)
	if err != nil {
		handleBatchFileLookupError(c, err)
		return
	}
	count, err := files.MoveBatch(ids, req.ParentID, req.Indexed)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, file := range batchFiles {
		publishFileAction(c.Request.Context(), bus, dispatcher, eventbus.FileActionEvent{Action: "move", FileID: file.ID, AccountID: result.Account.GetId(), Name: file.Name})
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

type batchFileIDsRequest struct {
	FileIDs []string `json:"file_ids"`
	IDs     []string `json:"ids"`
}

func (req batchFileIDsRequest) normalizedIDs() []string {
	seen := make(map[string]struct{}, len(req.FileIDs)+len(req.IDs))
	ids := make([]string, 0, len(req.FileIDs)+len(req.IDs))
	appendIDs := func(values []string) {
		for _, raw := range values {
			id := strings.TrimSpace(raw)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	appendIDs(req.FileIDs)
	appendIDs(req.IDs)
	return ids
}

type batchMoveFilesRequest struct {
	FileIDs  []string `json:"file_ids"`
	IDs      []string `json:"ids"`
	ParentID *string  `json:"parent_id"`
	Indexed  *bool    `json:"indexed"`
}

func (req batchMoveFilesRequest) normalizedIDs() []string {
	return batchFileIDsRequest{FileIDs: req.FileIDs, IDs: req.IDs}.normalizedIDs()
}

func loadBatchFilesForAccount(files *service.FileService, accountID string, ids []string, includeDeleted bool) ([]database.CloudFile, error) {
	query := files.DB().DB
	if includeDeleted {
		query = query.Unscoped()
	}
	var batchFiles []database.CloudFile
	if err := query.Where("id IN ?", ids).Find(&batchFiles).Error; err != nil {
		return nil, err
	}
	if len(batchFiles) != len(ids) {
		return nil, errBatchFileNotFound
	}
	for _, file := range batchFiles {
		if file.AccountID.String() != accountID {
			return nil, errBatchFileForbidden
		}
	}
	return batchFiles, nil
}

var (
	errBatchFileNotFound  = errors.New("file not found")
	errBatchFileForbidden = errors.New("forbidden")
)

func handleBatchFileLookupError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errBatchFileNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, errBatchFileForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func purgeMyRecycleBin(c *gin.Context, files *service.FileService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
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
	publishFileAction(c.Request.Context(), bus, dispatcher, eventbus.FileActionEvent{Action: "purge", AccountID: result.Account.GetId()})
	c.JSON(http.StatusOK, gin.H{"count": count})
}

// @Summary Create upload task
// @Tags uploads
// @Accept json
// @Produce json
// @Success 200 {object} map[string]any
// @Router /api/files/upload/create [post]
func createUploadTask(c *gin.Context, cfg *config.Config, files *service.FileService, tasks *service.TaskService, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req struct {
		Hash            *string `json:"hash"`
		FileName        string  `json:"file_name"`
		Description     *string `json:"description"`
		Index           bool    `json:"index"`
		FileSize        int64   `json:"file_size"`
		PoolID          *string `json:"pool_id"`
		ExpiredAt       *string `json:"expired_at"`
		ChunkSize       int64   `json:"chunk_size"`
		ParentID        *string `json:"parent_id"`
		OverwriteID     *string `json:"overwrite_id"`
		FastMode        bool    `json:"fast_mode"`
		Usage           *string `json:"usage"`
		ApplicationType *string `json:"application_type"`
		ContentType     string  `json:"content_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.FileSize <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_size must be greater than zero"})
		return
	}
	if req.OverwriteID != nil {
		target, err := files.GetFile(strings.TrimSpace(*req.OverwriteID))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if target.IsFolder {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cannot overwrite folder"})
			return
		}
		if !result.Account.GetIsSuperuser() && target.AccountID.String() != result.Account.GetId() {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		trimmedID := strings.TrimSpace(*req.OverwriteID)
		req.OverwriteID = &trimmedID
		req.FileName = target.Name
		req.ParentID = target.ParentID
		req.Description = target.Description
		if target.ExpiredAt != nil {
			expiredAtValue := target.ExpiredAt.Format(time.RFC3339)
			req.ExpiredAt = &expiredAtValue
		} else {
			req.ExpiredAt = nil
		}
		req.Usage = target.Usage
		req.ApplicationType = target.ApplicationType
		req.Index = target.Indexed
	}
	if strings.TrimSpace(req.FileName) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_name is required"})
		return
	}
	name := strings.TrimSpace(req.FileName)
	if req.ChunkSize <= 0 {
		req.ChunkSize = 5 * 1024 * 1024
	}
	if strings.TrimSpace(req.ContentType) == "" {
		req.ContentType = "application/octet-stream"
	}
	expiredAt, err := parseRFC3339Ptr(req.ExpiredAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := service.AccessContext{Account: result.Account, Session: result.Session}
	resolvedPoolID := files.ResolvedPoolID(req.PoolID)
	poolMultiplier := 1.0
	if resolvedPoolID != nil && strings.TrimSpace(*resolvedPoolID) != "" {
		if pool, err := files.GetPool(*resolvedPoolID); err == nil && pool.BillingConfig.CostMultiplier != nil && *pool.BillingConfig.CostMultiplier > 0 {
			poolMultiplier = *pool.BillingConfig.CostMultiplier
		}
	}
	account, err := quota.EnrichedAccount(c.Request.Context(), result.Account)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	logQuotaCheck(account, req.FileSize, poolMultiplier, "create-upload", false, nil)
	if err := quota.CheckUploadQuota(account, req.FileSize, poolMultiplier); err != nil {
		logQuotaCheck(account, req.FileSize, poolMultiplier, "create-upload", true, err)
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrQuotaExceeded) {
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if err := files.ValidatePoolUsage(ctx, req.PoolID, req.FileSize, req.ContentType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	chunks := int((req.FileSize + req.ChunkSize - 1) / req.ChunkSize)
	logging.Log.Info().
		Str("accountId", result.Account.GetId()).
		Str("name", name).
		Int64("fileSize", req.FileSize).
		Int64("chunkSize", req.ChunkSize).
		Int("chunks", chunks).
		Str("contentType", req.ContentType).
		Msg("creating upload task")
	payload := &database.PersistentTask{Description: req.Description, Hash: req.Hash, ExpiredAt: expiredAt, Usage: req.Usage, ParentID: req.ParentID, OverwriteID: req.OverwriteID, FastMode: req.FastMode, ApplicationType: req.ApplicationType, Indexed: req.Index}
	task, err := tasks.CreateUploadTask(uuid.MustParse(result.Account.GetId()), name, payload, req.FileSize, resolvedPoolID, name, req.ContentType, req.ChunkSize, chunks)
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
func directUpload(c *gin.Context, cfg *config.Config, files *service.FileService, tasks *service.TaskService, quota *service.QuotaService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
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
	description := optionalStringPtr(c.PostForm("description"))
	hash := optionalStringPtr(c.PostForm("hash"))
	parentID := optionalStringPtr(c.PostForm("parent_id"))
	overwriteID := optionalStringPtr(c.PostForm("overwrite_id"))
	fastMode := optionalBool(c.PostForm("fast_mode"))
	usage := optionalStringPtr(c.PostForm("usage"))
	appType := optionalStringPtr(c.PostForm("application_type"))
	indexed := optionalBool(c.PostForm("index"))
	expiredAt, err := parseRFC3339Ptr(optionalStringPtr(c.PostForm("expired_at")))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var overwriteTarget *database.CloudFile
	if overwriteID != nil {
		overwriteTarget, err = files.GetFile(strings.TrimSpace(*overwriteID))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if overwriteTarget.IsFolder {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cannot overwrite folder"})
			return
		}
		if !result.Account.GetIsSuperuser() && overwriteTarget.AccountID.String() != result.Account.GetId() {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		fileHeader.Filename = overwriteTarget.Name
		description = overwriteTarget.Description
		parentID = overwriteTarget.ParentID
		usage = overwriteTarget.Usage
		appType = overwriteTarget.ApplicationType
		indexed = overwriteTarget.Indexed
		expiredAt = overwriteTarget.ExpiredAt
	}

	account, err := quota.EnrichedAccount(c.Request.Context(), result.Account)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	logQuotaCheck(account, fileHeader.Size, 1.0, "direct-upload", false, nil)
	if err := quota.CheckUploadQuota(account, fileHeader.Size, 1.0); err != nil {
		logQuotaCheck(account, fileHeader.Size, 1.0, "direct-upload", true, err)
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrQuotaExceeded) {
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"error": err.Error()})
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
	logging.Log.Info().
		Str("accountId", result.Account.GetId()).
		Str("fileName", fileHeader.Filename).
		Int64("size", fileHeader.Size).
		Msg("starting direct upload")
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
	logging.Log.Info().
		Str("accountId", result.Account.GetId()).
		Str("tempPath", tempPath).
		Msg("direct upload staged to disk")
	defer os.Remove(tempPath)
	var createdFile *database.CloudFile
	var object *database.FileObject
	analysis, analysisErr := files.AnalyzeSourceFile(c.Request.Context(), tempPath, fileHeader.Header.Get("Content-Type"))
	if overwriteTarget != nil && fastMode {
		if updated, applied, err := files.FastOverwriteFile(overwriteTarget.ID, tempPath, analysis); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		} else if applied {
			createdFile = updated
			object = createdFile.Object
		}
	}
	if createdFile == nil {
		stage, err := os.Open(tempPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		object, err = files.StreamToStorage(c.Request.Context(), stage, fileHeader.Header.Get("Content-Type"))
		_ = stage.Close()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		storageKey := &object.ID
		if overwriteTarget != nil {
			createdFile, err = files.OverwriteFile(overwriteTarget.ID, object.ID, storageKey)
		} else {
			createdFile, err = files.CreateUploadedFile(uuid.MustParse(result.Account.GetId()), fileHeader.Filename, description, hash, expiredAt, usage, parentID, object.ID, nil, appType, storageKey, indexed)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if analysisErr == nil {
			if updated, err := files.StoreSourceAnalysis(createdFile.ID, analysis); err == nil {
				createdFile = updated
			} else {
				logging.Log.Warn().Err(err).Str("fileId", createdFile.ID).Msg("failed to persist source analysis")
			}
		} else {
			logging.Log.Warn().Err(analysisErr).Str("fileId", createdFile.ID).Msg("failed to analyze source file")
		}
	} else if object == nil {
		object = createdFile.Object
	}
	if createdFile == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "overwrite failed"})
		return
	}
	logging.Log.Info().
		Str("accountId", result.Account.GetId()).
		Str("fileId", createdFile.ID).
		Str("objectId", deref(createdFile.ObjectID)).
		Msg("direct upload stored")
	_ = tasks
	contentType := fileHeader.Header.Get("Content-Type")
	if object != nil && strings.TrimSpace(object.MimeType) != "" {
		contentType = object.MimeType
	}
	storageKey := deref(createdFile.ObjectID)
	if createdFile.Object != nil && createdFile.Object.StorageKey != nil && strings.TrimSpace(*createdFile.Object.StorageKey) != "" {
		storageKey = strings.TrimSpace(*createdFile.Object.StorageKey)
	} else if createdFile.StorageKey != nil && strings.TrimSpace(*createdFile.StorageKey) != "" {
		storageKey = strings.TrimSpace(*createdFile.StorageKey)
	}
	if err := publishFileUploaded(c.Request.Context(), bus, dispatcher, eventbus.FileUploadedEvent{FileID: createdFile.ID, ContentType: contentType, StorageKey: storageKey, ProcessingFilePath: tempPath, IsTempFile: true}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
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
	logging.Log.Debug().
		Str("taskId", taskID).
		Int("chunkIndex", idx).
		Int64("chunkSize", fileHeader.Size).
		Msg("upload chunk staged")
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
func completeUpload(c *gin.Context, cfg *config.Config, files *service.FileService, tasks *service.TaskService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
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
	logging.Log.Info().
		Str("taskId", taskID).
		Int("chunks", task.ChunksCount).
		Str("mergedPath", mergedPath).
		Msg("upload chunks merged")
	ctx := service.AccessContext{Account: result.Account, Session: result.Session}
	_ = ctx
	var created *database.CloudFile
	var object *database.FileObject
	analysis, analysisErr := files.AnalyzeSourceFile(c.Request.Context(), mergedPath, "")
	if task.FastMode && task.OverwriteID != nil && strings.TrimSpace(*task.OverwriteID) != "" {
		if updated, applied, err := files.FastOverwriteFile(strings.TrimSpace(*task.OverwriteID), mergedPath, analysis); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		} else if applied {
			created = updated
			object = created.Object
		}
	}
	if created == nil {
		stage, err := os.Open(mergedPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		object, err = files.StreamToStorage(c.Request.Context(), stage, "")
		_ = stage.Close()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		storageKey := &object.ID
		if task.OverwriteID != nil && strings.TrimSpace(*task.OverwriteID) != "" {
			created, err = files.OverwriteFile(strings.TrimSpace(*task.OverwriteID), object.ID, storageKey)
		} else {
			created, err = files.CreateUploadedFile(task.AccountID, deref(task.FileName), task.Description, task.Hash, task.ExpiredAt, task.Usage, task.ParentID, object.ID, task.PoolID, task.ApplicationType, storageKey, task.Indexed)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if analysisErr == nil {
			if updated, err := files.StoreSourceAnalysis(created.ID, analysis); err == nil {
				created = updated
			} else {
				logging.Log.Warn().Err(err).Str("fileId", created.ID).Msg("failed to persist source analysis")
			}
		} else {
			logging.Log.Warn().Err(analysisErr).Str("fileId", created.ID).Msg("failed to analyze source file")
		}
	} else if object == nil {
		object = created.Object
	}
	if created == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "overwrite failed"})
		return
	}
	logging.Log.Info().
		Str("taskId", taskID).
		Str("fileId", created.ID).
		Str("objectId", deref(created.ObjectID)).
		Msg("upload stored")
	storageKey := deref(created.ObjectID)
	if created.Object != nil && created.Object.StorageKey != nil && strings.TrimSpace(*created.Object.StorageKey) != "" {
		storageKey = strings.TrimSpace(*created.Object.StorageKey)
	} else if created.StorageKey != nil && strings.TrimSpace(*created.StorageKey) != "" {
		storageKey = strings.TrimSpace(*created.StorageKey)
	}
	contentType := "application/octet-stream"
	if object != nil && strings.TrimSpace(object.MimeType) != "" {
		contentType = object.MimeType
	}
	if err := publishFileUploaded(c.Request.Context(), bus, dispatcher, eventbus.FileUploadedEvent{FileID: created.ID, TaskID: task.TaskID, ContentType: contentType, StorageKey: storageKey, ProcessingFilePath: mergedPath, IsTempFile: true}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	logging.Log.Info().
		Str("taskId", taskID).
		Str("fileId", created.ID).
		Msg("upload event published")
	_ = tasks.MarkCompleted(task.TaskID)
	c.JSON(http.StatusOK, created)
}

func createWebDAVToken(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Label string `json:"label"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Label) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "label is required"})
		return
	}

	rawToken := database.NewID()

	hashBytes, err := bcrypt.GenerateFromPassword([]byte(rawToken), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash token"})
		return
	}

	accountUUID, err := uuid.Parse(result.Account.GetId())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account ID in session"})
		return
	}
	token := database.WebDAVToken{
		ID:        database.NewID(),
		AccountID: accountUUID,
		TokenHash: string(hashBytes),
		Label:     strings.TrimSpace(req.Label),
	}
	if err := files.DB().Create(&token).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":       token.ID,
		"label":    token.Label,
		"secret":   rawToken,
		"created_at": token.CreatedAt,
	})
}

func listWebDAVTokens(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var tokens []database.WebDAVToken
	if err := files.DB().Where("account_id = ?", result.Account.GetId()).Order("created_at desc").Find(&tokens).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, tokens)
}

func deleteWebDAVToken(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	tokenID := c.Param("id")
	var token database.WebDAVToken
	if err := files.DB().Where("id = ? AND account_id = ?", tokenID, result.Account.GetId()).First(&token).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "token not found"})
		return
	}

	if err := files.DB().Delete(&token).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func publishFileUploaded(ctx context.Context, bus *eventbus.Bus, dispatcher dispatch.Dispatcher, evt eventbus.FileUploadedEvent) error {
	if dispatcher != nil {
		return dispatcher.PublishFileUploaded(ctx, evt)
	}
	if bus != nil {
		return bus.PublishFileUploaded(ctx, evt)
	}
	return fmt.Errorf("no upload event sink configured")
}

func publishFileAction(ctx context.Context, bus *eventbus.Bus, dispatcher dispatch.Dispatcher, evt eventbus.FileActionEvent) {
	if dispatcher != nil {
		_ = dispatcher.PublishFileAction(ctx, evt)
		return
	}
	if bus != nil {
		_ = bus.PublishFileAction(ctx, evt)
	}
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
	c.JSON(http.StatusOK, []any{})
}

func taskDetails(c *gin.Context, cfg *config.Config, tasks *service.TaskService) {
	c.JSON(http.StatusOK, gin.H{"task": nil})
}

func logQuotaCheck(account *gen.DyAccount, fileSize int64, costMultiplier float64, source string, refused bool, err error) {
	if account == nil {
		return
	}
	entry := quotaLogEvent(logging.Log.Info(), account).
		Str("source", source).
		Int64("fileSize", fileSize).
		Float64("costMultiplier", costMultiplier)
	if refused && err != nil {
		entry = entry.Err(err)
	}
	if refused {
		quotaLogEvent(logging.Log.Warn(), account).
			Str("source", source).
			Int64("fileSize", fileSize).
			Float64("costMultiplier", costMultiplier).
			Err(err).
			Msg("upload quota check")
		return
	}
	entry.Msg("upload quota check")
}

func quotaLogEvent(event *zerolog.Event, account *gen.DyAccount) *zerolog.Event {
	if event == nil || account == nil {
		return event
	}
	perkLevel := account.GetPerkLevel()
	perkSubscriptionLevel := int32(0)
	hasPerkSubscription := false
	if sub := account.GetPerkSubscription(); sub != nil {
		hasPerkSubscription = true
		perkSubscriptionLevel = sub.GetPerkLevel()
	}
	level := int32(0)
	experience := int32(0)
	levelingProgress := 0.0
	if profile := account.GetProfile(); profile != nil {
		level = profile.GetLevel()
		experience = profile.GetExperience()
		levelingProgress = profile.GetLevelingProgress()
	}
	return event.
		Str("accountId", account.GetId()).
		Bool("isSuperuser", account.GetIsSuperuser()).
		Int32("level", level).
		Int32("experience", experience).
		Float64("levelingProgress", levelingProgress).
		Int32("perkLevel", perkLevel).
		Bool("hasPerkSubscription", hasPerkSubscription).
		Int32("perkSubscriptionLevel", perkSubscriptionLevel)
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func fileVersionHeader(file *database.CloudFile) string {
	if file == nil {
		return ""
	}
	if file.Object != nil && strings.TrimSpace(file.Object.Hash) != "" {
		return strings.TrimSpace(file.Object.Hash)
	}
	return strconv.FormatInt(file.UpdatedAt.UnixMilli(), 10)
}

func optionalStringPtr(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

func optionalBool(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	parsed, err := strconv.ParseBool(v)
	return err == nil && parsed
}

func parseRFC3339Ptr(v *string) (*time.Time, error) {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*v))
	if err != nil {
		return nil, fmt.Errorf("invalid expired_at: %w", err)
	}
	return &parsed, nil
}
