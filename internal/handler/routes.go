package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"src.solsynth.dev/sosys/filesystem/internal/storage"
	"src.solsynth.dev/sosys/go/pkg/auth"
	eb "src.solsynth.dev/sosys/go/pkg/eventbus"
	gen "src.solsynth.dev/sosys/go/proto"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/datatypes"
	"gorm.io/gorm"
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
		f.GET("/meta", func(c *gin.Context) { listFilesMetadata(c, files) })
		f.GET("/:id", func(c *gin.Context) { openFile(c, cfg, files) })
		f.GET("/:id/info", func(c *gin.Context) { fileInfo(c, files) })
		f.GET("/:id/breadcrumb", func(c *gin.Context) { fileBreadcrumb(c, files) })
		f.GET("/:id/open", func(c *gin.Context) { openFile(c, cfg, files) })
		f.GET("/:id/references", func(c *gin.Context) { c.JSON(http.StatusOK, []any{}) })
		f.POST("/:id/edit", func(c *gin.Context) { createEditSession(c, wopi, files) })
		f.GET("/root/children", func(c *gin.Context) { listRootIndexed(c, files, quota) })
		f.GET("/:id/children", func(c *gin.Context) { listChildren(c, files, quota) })
		f.POST("/folders", func(c *gin.Context) { createFolder(c, files, quota) })
		f.GET("/me", func(c *gin.Context) { listRootOwned(c, files, quota) })
		f.GET("/unindexed", func(c *gin.Context) { listUnindexed(c, files, quota) })
		f.PATCH("/:id", func(c *gin.Context) { patchFile(c, files) })
		f.PUT("/:id/sensitive", func(c *gin.Context) { setSensitiveMarks(c, files) })
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
		u.POST("/prepare", func(c *gin.Context) { prepareDirectUpload(c, files, tasks, quota) })
		u.POST("/:taskId/part", func(c *gin.Context) { presignUploadPart(c, files, tasks) })
		u.POST("/:taskId/complete", func(c *gin.Context) { completeDirectUpload(c, files, tasks, bus, dispatcher) })
		u.POST("/create", func(c *gin.Context) { createUploadTask(c, cfg, files, tasks, quota) })
		u.POST("/direct", func(c *gin.Context) { directUpload(c, cfg, files, tasks, quota, bus, dispatcher) })
		u.POST("/chunk/:taskId/:idx", func(c *gin.Context) { uploadChunk(c, cfg, files, tasks) })
		u.POST("/complete/:taskId", func(c *gin.Context) { completeUpload(c, cfg, files, tasks, quota, bus, dispatcher) })
		u.GET("/tasks", func(c *gin.Context) { listUploadTasks(c, tasks) })
		u.GET("/progress/:taskId", func(c *gin.Context) { uploadProgress(c, tasks) })
		u.GET("/status/:taskId", func(c *gin.Context) { uploadStatus(c, tasks) })
		u.GET("/resume/:taskId", func(c *gin.Context) { uploadResume(c, tasks) })
		u.DELETE("/:taskId", func(c *gin.Context) { cancelUpload(c, files, tasks) })
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
		b.GET("workspaces/:workspaceId/quota", func(c *gin.Context) { getWorkspaceQuota(c, quota) })
	}
	// Golden-points quota purchase routes; absent when the [wallet] target is
	// unset (404), matching the WebDAV/WOPI convention.
	if cfg.Wallet.Target != "" {
		b.GET("quota/purchase", func(c *gin.Context) { getQuotaPurchaseInfo(c, quota) })
		b.POST("quota/purchase", func(c *gin.Context) { createQuotaPurchase(c, quota) })
	}

	if cfg.WebDAV.Enabled {
		prefix := cfg.WebDAV.Prefix
		if prefix == "" {
			prefix = "/webdav"
		}
		srvWebDAV := func(c *gin.Context) {
			handleWebDAV(c, files, quota, bus, dispatcher, prefix)
		}
		r.Any(prefix+"/*path", srvWebDAV)
		// Gin's cleanPath strips trailing slashes, so /webdav/ routes to /webdav.
		// Rewrite the URL so the webdav handler always sees the trailing slash.
		r.Any(prefix, func(c *gin.Context) {
			c.Request.URL.Path = prefix + "/"
			handleWebDAV(c, files, quota, bus, dispatcher, prefix)
		})
		for _, method := range []string{"PROPFIND", "PROPPATCH", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK"} {
			r.Handle(method, prefix+"/*path", srvWebDAV)
			r.Handle(method, prefix, func(c *gin.Context) {
				c.Request.URL.Path = prefix + "/"
				handleWebDAV(c, files, quota, bus, dispatcher, prefix)
			})
		}

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

	sn := r.Group("/api/nodes")
	{
		sn.POST("", func(c *gin.Context) { createNode(c, files) })
		sn.GET("", func(c *gin.Context) { listNodes(c, files) })
		sn.GET("/:id", func(c *gin.Context) { getNode(c, files) })
		sn.PATCH("/:id", func(c *gin.Context) { updateNode(c, files) })
		sn.DELETE("/:id", func(c *gin.Context) { deleteNode(c, files) })
	}

	adminStorage := r.Group("/api/admin/storage")
	{
		adminStorage.GET("/config", func(c *gin.Context) { getAdminStorageConfig(c, files) })
		adminStorage.PATCH("/config/:poolId", func(c *gin.Context) { updateAdminStorageConfig(c, files) })
		adminStorage.GET("/status", func(c *gin.Context) { getAdminStorageStatus(c, files) })
		adminStorage.GET("/health", func(c *gin.Context) { getAdminStorageHealth(c, files) })
		adminStorage.GET("/stats", func(c *gin.Context) { getAdminStorageStats(c, files) })
		adminStorage.GET("/failures", func(c *gin.Context) { getAdminStorageFailures(c, files) })
		adminStorage.POST("/pool-migrations", func(c *gin.Context) { createPoolMigration(c, files, tasks) })
		adminStorage.GET("/pool-migrations/:taskId", func(c *gin.Context) { getPoolMigration(c, files, tasks) })
	}

	adminUploads := r.Group("/api/admin/uploads")
	{
		adminUploads.POST("/gc", func(c *gin.Context) { triggerUploadTaskGC(c, files) })
	}

	adminReanalysis := r.Group("/api/admin/reanalysis")
	{
		adminReanalysis.GET("/candidates", func(c *gin.Context) { listReanalysisCandidates(c, files) })
		adminReanalysis.POST("/run", func(c *gin.Context) { runReanalysis(c, files) })
		adminReanalysis.POST("/files", func(c *gin.Context) { reanalyzeFiles(c, files) })
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
	file, err := getRequestedFile(c, files, c.Param("id"))
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

// @Summary List file metadata
// @Tags files
// @Produce json
// @Param ids query []string true "File IDs; may be repeated or comma-separated"
// @Success 200 {array} database.CloudFile
// @Router /api/files/meta [get]
func listFilesMetadata(c *gin.Context, files *service.FileService) {
	ids := queryFileIDs(c, "ids")
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids is required"})
		return
	}

	// Metadata lookups are ID-based and intentionally span personal and
	// workspace namespaces. Listing endpoints remain workspace-scoped.
	items, err := files.GetFiles(ids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	result, _, authenticated := auth.GetAuth(c)
	itemsByID := make(map[string]database.CloudFile, len(items))
	for _, item := range items {
		if authenticated {
			if !files.CanAccessFile(result.Account, result.Session, &item, "read") {
				continue
			}
		} else if !files.CanAccessFile(nil, nil, &item, "read") {
			continue
		}
		itemsByID[item.ID] = item
	}

	ordered := make([]database.CloudFile, 0, len(ids))
	for _, id := range ids {
		if item, ok := itemsByID[id]; ok {
			ordered = append(ordered, item)
		}
	}
	c.Header("X-Total", strconv.Itoa(len(ordered)))
	c.JSON(http.StatusOK, ordered)
}

func queryFileIDs(c *gin.Context, key string) []string {
	ids := make([]string, 0)
	for _, value := range c.QueryArray(key) {
		for _, id := range strings.Split(value, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

type breadcrumbItem struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	ParentID *string `json:"parent_id"`
	IsFolder bool    `json:"is_folder"`
}

func fileBreadcrumb(c *gin.Context, files *service.FileService) {
	file, err := getRequestedFile(c, files, c.Param("id"))
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
	file, err := getRequestedFile(c, files, c.Param("id"))
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
	file, err := getRequestedFile(c, files, c.Param("id"))
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
			if parent, err := getRequestedFile(c, files, *file.ParentID); err == nil {
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
			if kind == "system.compression.low" {
				logging.Log.Info().Str("fileId", file.ID).Str("storageKey", deref(legacy.StorageKey)).Msg("compression_pending_fallback")
			}
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

func listChildren(c *gin.Context, files *service.FileService, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	workspaceID, err := selectedWorkspaceID(c, quota, result.Account.GetId())
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	parent, err := files.GetFileInWorkspace(c.Param("id"), workspaceID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if workspaceID == nil && !files.CanAccessFile(result.Account, result.Session, parent, "read") {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	filters := parseListQuery(c, 0, 50)
	items, err := files.GetChildrenInWorkspace(c.Param("id"), workspaceID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	items = filterAndSortFiles(items, filters)
	total := len(items)
	items = paginateFiles(items, filters.Offset, filters.Take)
	filtered := make([]database.CloudFile, 0, len(items))
	for _, item := range items {
		if workspaceID != nil || files.CanAccessFile(result.Account, result.Session, &item, "read") {
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

// @Summary Get quota purchase terms
// @Tags billing
// @Produce json
// @Success 200 {object} service.QuotaPurchaseInfo
// @Router /api/billing/quota/purchase [get]
func getQuotaPurchaseInfo(c *gin.Context, quota *service.QuotaService) {
	_, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	c.JSON(http.StatusOK, quota.PurchaseInfo())
}

// @Summary Create quota purchase order
// @Description Create a Wallet order for quantityGB GB of extra quota at the configured per-GB price; the user pays it via the Wallet API, and the granted quota lands in quota records once the payment event arrives.
// @Tags billing
// @Accept json
// @Produce json
// @Param body body object true "purchase request" SchemaExample({"quantity_gb":10})
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]any
// @Failure 401 {object} map[string]any
// @Failure 502 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /api/billing/quota/purchase [post]
func createQuotaPurchase(c *gin.Context, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req struct {
		QuantityGB int64 `json:"quantity_gb"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.QuantityGB <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "quantity_gb is required"})
		return
	}
	order, err := quota.CreatePurchaseOrder(c.Request.Context(), result.Account.GetId(), req.QuantityGB)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPurchaseNotConfigured):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "quota purchase is not configured"})
		case errors.Is(err, service.ErrPurchaseQuantityTooLow):
			c.JSON(http.StatusBadRequest, gin.H{"error": "quantity_gb is below the minimum"})
		case errors.Is(err, service.ErrPurchaseQuantityTooHigh):
			c.JSON(http.StatusBadRequest, gin.H{"error": "quantity_gb would exceed the maximum extra quota"})
		default:
			logging.Log.Error().Err(err).Msg("create quota purchase order failed")
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create order"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"order_id":    order.GetId(),
		"amount":      order.GetAmount(),
		"currency":    order.GetCurrency().GetValue(),
		"quantity_gb": req.QuantityGB,
		"quota_mb":    req.QuantityGB * 1024,
	})
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

// @Summary Get workspace storage quota
// @Tags billing
// @Produce json
// @Param workspaceId path string true "Workspace ID"
// @Success 200 {object} service.WorkspaceUsageSummary
// @Router /api/billing/workspaces/{workspaceId}/quota [get]
func getWorkspaceQuota(c *gin.Context, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	summary, err := quota.GetWorkspaceUsage(c.Request.Context(), c.Param("workspaceId"), result.Account.GetId())
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
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
	file, err := getRequestedFile(c, files, c.Param("id"))
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
	file, err := getRequestedFile(c, files, c.Param("id"))
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

func listRootIndexed(c *gin.Context, files *service.FileService, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	filters := parseListQuery(c, 0, 50)
	workspaceID, err := selectedWorkspaceID(c, quota, result.Account.GetId())
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	var items []database.CloudFile
	var total int64
	if workspaceID == nil {
		items, total, err = files.ListRootPage(uuid.MustParse(result.Account.GetId()), fileListOptions(filters))
	} else {
		items, total, err = files.ListWorkspaceRootPage(*workspaceID, fileListOptions(filters))
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Total", strconv.FormatInt(total, 10))
	c.JSON(http.StatusOK, items)
}

func listRootOwned(c *gin.Context, files *service.FileService, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	filters := parseListQuery(c, 0, 20)
	workspaceID, err := selectedWorkspaceID(c, quota, result.Account.GetId())
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	var items []database.CloudFile
	var total int64
	if workspaceID == nil {
		items, total, err = files.ListOwnedPage(uuid.MustParse(result.Account.GetId()), fileListOptions(filters))
	} else {
		items, total, err = files.ListWorkspaceOwnedPage(*workspaceID, fileListOptions(filters))
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Total", strconv.FormatInt(total, 10))
	c.JSON(http.StatusOK, items)
}

func listUnindexed(c *gin.Context, files *service.FileService, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	pool := strings.TrimSpace(c.Query("pool"))
	recycled := strings.EqualFold(c.Query("recycled"), "true") || c.Query("recycled") == "1"
	filters := parseListQuery(c, 0, 20)
	if pool != "" && filters.PoolID == "" {
		filters.PoolID = pool
	}
	if filters.Recycled == nil {
		filters.Recycled = &recycled
	}
	workspaceID, err := selectedWorkspaceID(c, quota, result.Account.GetId())
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	var items []database.CloudFile
	var total int64
	if workspaceID == nil {
		items, total, err = files.ListUnindexedPage(uuid.MustParse(result.Account.GetId()), fileListOptions(filters))
	} else {
		items, total, err = files.ListWorkspaceUnindexedPage(*workspaceID, fileListOptions(filters))
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Total", strconv.FormatInt(total, 10))
	c.JSON(http.StatusOK, items)
}

// selectedWorkspaceID keeps workspace data opt-in: no query parameter means
// the caller receives only personal files. When set, membership is verified
// before any workspace file query is issued.
func selectedWorkspaceID(c *gin.Context, quota *service.QuotaService, accountID string) (*string, error) {
	workspaceID := optionalStringPtr(c.Query("workspace_id"))
	if workspaceID == nil {
		return nil, nil
	}
	if _, err := quota.GetWorkspaceUsage(c.Request.Context(), *workspaceID, accountID); err != nil {
		return nil, err
	}
	return workspaceID, nil
}

// getRequestedFile is intentionally an ID-based lookup. Direct file access
// and metadata can bypass namespace selection; only browse/list endpoints are
// constrained by workspace_id.
func getRequestedFile(c *gin.Context, files *service.FileService, fileID string) (*database.CloudFile, error) {
	return files.GetFile(fileID)
}

func fileListOptions(filters fileListFilters) service.FileListOptions {
	return service.FileListOptions{
		Offset: filters.Offset, Take: filters.Take, Query: filters.Query, Name: filters.Name, Extension: filters.Extension,
		Order: filters.Order, OrderDesc: filters.OrderDesc, Usage: filters.Usage, ApplicationType: filters.ApplicationType,
		ContentType: filters.ContentType, PoolID: filters.PoolID, ParentID: filters.ParentID, Indexed: filters.Indexed,
		Recycled: filters.Recycled, IsFolder: filters.IsFolder, HasThumbnail: filters.HasThumbnail,
		HasCompression: filters.HasCompression, MinSize: filters.MinSize, MaxSize: filters.MaxSize,
		CreatedAfter: filters.CreatedAfter, CreatedBefore: filters.CreatedBefore, UpdatedAfter: filters.UpdatedAfter, UpdatedBefore: filters.UpdatedBefore,
	}
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

func createFolder(c *gin.Context, files *service.FileService, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req struct {
		Name        string  `json:"name"`
		ParentID    *string `json:"parent_id"`
		WorkspaceID *string `json:"workspace_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	workspaceID := optionalStringPtr(deref(req.WorkspaceID))
	if workspaceID != nil {
		if err := quota.CheckWorkspaceUploadQuota(c.Request.Context(), *workspaceID, result.Account.GetId(), 0); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	folder, err := files.CreateWorkspaceFolder(uuid.MustParse(result.Account.GetId()), workspaceID, req.Name, req.ParentID)
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

	file, err := getRequestedFile(c, files, c.Param("id"))
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
	file, err = getRequestedFile(c, files, file.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	file.Name = name
	file.PermissionStatus.Writable = true
	c.JSON(http.StatusOK, file)
}

func setSensitiveMarks(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	file, err := getRequestedFile(c, files, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !result.Account.GetIsSuperuser() && file.AccountID.String() != result.Account.GetId() {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	var req struct {
		Marks []int `json:"marks"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	marksJSON, err := json.Marshal(req.Marks)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid marks payload"})
		return
	}

	if err := files.SetSensitiveMarks(file.ID, datatypes.JSON(marksJSON)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func patchFileContent(c *gin.Context, files *service.FileService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	fileID := c.Param("id")
	file, err := getRequestedFile(c, files, fileID)
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
	file, err := getRequestedFile(c, files, c.Param("id"))
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
	file, err := getRequestedFile(c, files, c.Param("id"))
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
	file, err := getRequestedFile(c, files, c.Param("id"))
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
	// When a selection includes both a folder and its children, purging the
	// parent already removes the children. Only purge top-level selected IDs
	// so we don't 500 on record-not-found for already-cascaded deletes.
	roots, err := filterTopLevelBatchFiles(files, batchFiles)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	count := int64(0)
	for _, file := range roots {
		if err := files.PurgeFile(file.ID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// Already removed as a descendant of an earlier purge.
				continue
			}
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

// filterTopLevelBatchFiles drops selected IDs that are descendants of other
// selected IDs, so nested multi-select can purge each tree once.
func filterTopLevelBatchFiles(files *service.FileService, batchFiles []database.CloudFile) ([]database.CloudFile, error) {
	if len(batchFiles) <= 1 {
		return batchFiles, nil
	}
	selected := make(map[string]database.CloudFile, len(batchFiles))
	ids := make([]string, 0, len(batchFiles))
	for _, file := range batchFiles {
		selected[file.ID] = file
		ids = append(ids, file.ID)
	}
	descendantOfSelected := make(map[string]struct{})
	for _, id := range ids {
		descendants, err := files.LoadDescendantIDsIncludingDeleted([]string{id})
		if err != nil {
			return nil, err
		}
		for _, descendantID := range descendants {
			if descendantID == id {
				continue
			}
			if _, ok := selected[descendantID]; ok {
				descendantOfSelected[descendantID] = struct{}{}
			}
		}
	}
	roots := make([]database.CloudFile, 0, len(batchFiles))
	for _, file := range batchFiles {
		if _, nested := descendantOfSelected[file.ID]; nested {
			continue
		}
		roots = append(roots, file)
	}
	return roots, nil
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
	startedAt := time.Now()
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !requireUploadPermission(c, files, result) {
		return
	}
	var req struct {
		Hash            *string `json:"hash"`
		FileName        string  `json:"file_name"`
		Description     *string `json:"description"`
		Index           bool    `json:"index"`
		FileSize        int64   `json:"file_size"`
		PoolID          *string `json:"pool_id"`
		WorkspaceID     *string `json:"workspace_id"`
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
	parseDuration := time.Since(startedAt)
	if req.FileSize <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_size must be greater than zero"})
		return
	}
	if req.OverwriteID != nil {
		target, err := files.GetFileInWorkspace(strings.TrimSpace(*req.OverwriteID), optionalStringPtr(deref(req.WorkspaceID)))
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
		if req.WorkspaceID != nil && target.WorkspaceID != nil && strings.TrimSpace(*req.WorkspaceID) != strings.TrimSpace(*target.WorkspaceID) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_id must match the overwritten file"})
			return
		}
		req.WorkspaceID = target.WorkspaceID
	}
	if strings.TrimSpace(req.FileName) == "" {
		req.FileName = service.DefaultUploadFileName(req.ContentType)
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
	quotaStart := time.Now()
	logQuotaCheck(result.Account, req.FileSize, poolMultiplier, "create-upload", false, nil)
	var quotaErr error
	if workspaceID := optionalStringPtr(deref(req.WorkspaceID)); workspaceID != nil {
		quotaErr = quota.CheckWorkspaceUploadQuota(c.Request.Context(), *workspaceID, result.Account.GetId(), req.FileSize)
		req.WorkspaceID = workspaceID
	} else {
		quotaErr = quota.CheckUploadQuota(result.Account, req.FileSize, poolMultiplier)
	}
	if quotaErr != nil {
		logQuotaCheck(result.Account, req.FileSize, poolMultiplier, "create-upload", true, quotaErr)
		status := http.StatusBadRequest
		if errors.Is(quotaErr, service.ErrQuotaExceeded) {
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"error": quotaErr.Error()})
		return
	}
	if err := files.ValidatePoolUsage(ctx, req.PoolID, req.FileSize, req.ContentType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	quotaDuration := time.Since(quotaStart)
	chunks := int((req.FileSize + req.ChunkSize - 1) / req.ChunkSize)
	dbStart := time.Now()
	payload := &database.PersistentTask{Description: req.Description, Hash: req.Hash, ExpiredAt: expiredAt, Usage: req.Usage, ParentID: req.ParentID, OverwriteID: req.OverwriteID, FastMode: req.FastMode, ApplicationType: req.ApplicationType, Indexed: req.Index, WorkspaceID: req.WorkspaceID}
	task, err := tasks.CreateUploadTask(uuid.MustParse(result.Account.GetId()), name, payload, req.FileSize, resolvedPoolID, name, req.ContentType, req.ChunkSize, chunks)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	dbDuration := time.Since(dbStart)
	_ = cfg
	logging.Log.Info().
		Str("accountId", result.Account.GetId()).
		Str("name", name).
		Int64("fileSize", req.FileSize).
		Int64("chunkSize", req.ChunkSize).
		Int("chunks", chunks).
		Str("contentType", req.ContentType).
		Dur("parseDuration", parseDuration).
		Dur("quotaDuration", quotaDuration).
		Dur("dbDuration", dbDuration).
		Dur("totalDuration", time.Since(startedAt)).
		Msg("upload task created")
	c.JSON(http.StatusOK, gin.H{"task_id": task.TaskID, "chunk_size": task.ChunkSize, "chunks_count": task.ChunksCount})
}

func requireUploadPermission(c *gin.Context, files *service.FileService, result *auth.AuthResult) bool {
	if result == nil || result.Account == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return false
	}
	if result.Account.GetIsSuperuser() {
		return true
	}
	if err := files.RequireAccountPermission(c.Request.Context(), result.Account.GetId(), service.PermissionFilesUpload); err != nil {
		if errors.Is(err, service.ErrPermissionDenied) {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return false
	}
	return true
}

type metadataEventDispatcher interface {
	PublishFileMetadataUpdated(context.Context, eventbus.FileMetadataUpdatedEvent) error
}

// defaultMultipartPartSize is the server-chosen part size for multipart direct
// uploads. It mirrors the proxied chunk size so both flows use comparable
// request granularity.
const defaultMultipartPartSize int64 = 5 * 1024 * 1024

// multipartPlan derives the part size and part count for a direct upload of
// fileSize bytes.
func multipartPlan(fileSize int64) (partSize, partCount int64) {
	partSize = defaultMultipartPartSize
	if fileSize <= 0 {
		return partSize, 0
	}
	return partSize, (fileSize + partSize - 1) / partSize
}

const maxClientUploadAnalysisBytes = 16 * 1024

type clientUploadParameters struct {
	ClientAnalysis map[string]any `json:"client_analysis,omitempty"`
	Thumbnail      bool           `json:"thumbnail,omitempty"`
	Compression    bool           `json:"compression,omitempty"`
}

func encodeClientUploadParameters(analysis map[string]any, thumbnail, compression bool) (datatypes.JSON, error) {
	if len(analysis) == 0 && !thumbnail && !compression {
		return datatypes.JSON([]byte(`{}`)), nil
	}
	params := clientUploadParameters{ClientAnalysis: analysis, Thumbnail: thumbnail, Compression: compression}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxClientUploadAnalysisBytes {
		return nil, fmt.Errorf("client upload analysis is too large")
	}
	return datatypes.JSON(raw), nil
}

func decodeClientUploadParameters(raw datatypes.JSON) clientUploadParameters {
	var params clientUploadParameters
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &params)
	}
	return params
}

func clientThumbnailKey(sourceKey string) string {
	return strings.TrimSpace(sourceKey) + ".thumbnail"
}

func clientCompressionKey(sourceKey string) string {
	return strings.TrimSpace(sourceKey) + ".compressed"
}

func presignClientDerivative(ctx context.Context, direct storage.DirectUploadBackend, key, contentType string, enabled bool) (string, error) {
	if !enabled {
		return "", nil
	}
	return direct.PresignedPutURL(ctx, key, 15*time.Minute, contentType)
}

func addClientDerivativeURLs(ctx context.Context, direct storage.DirectUploadBackend, sourceKey string, params clientUploadParameters, response gin.H) error {
	thumbnailURL, err := presignClientDerivative(ctx, direct, clientThumbnailKey(sourceKey), "image/jpeg", params.Thumbnail)
	if err != nil {
		return err
	}
	if thumbnailURL != "" {
		response["thumbnail_upload_url"] = thumbnailURL
		response["thumbnail_key"] = clientThumbnailKey(sourceKey)
	}
	compressionURL, err := presignClientDerivative(ctx, direct, clientCompressionKey(sourceKey), "image/webp", params.Compression)
	if err != nil {
		return err
	}
	if compressionURL != "" {
		response["compression_upload_url"] = compressionURL
		response["compression_key"] = clientCompressionKey(sourceKey)
	}
	return nil
}

func prepareDirectUpload(c *gin.Context, files *service.FileService, tasks *service.TaskService, quota *service.QuotaService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !requireUploadPermission(c, files, result) {
		return
	}
	var req struct {
		Hash            *string        `json:"hash"`
		FileName        string         `json:"file_name"`
		Description     *string        `json:"description"`
		Index           bool           `json:"index"`
		FileSize        int64          `json:"file_size"`
		PoolID          *string        `json:"pool_id"`
		WorkspaceID     *string        `json:"workspace_id"`
		ExpiredAt       *string        `json:"expired_at"`
		ParentID        *string        `json:"parent_id"`
		OverwriteID     *string        `json:"overwrite_id"`
		Usage           *string        `json:"usage"`
		ApplicationType *string        `json:"application_type"`
		ContentType     string         `json:"content_type"`
		Multipart       bool           `json:"multipart"`
		ClientAnalysis  map[string]any `json:"client_analysis"`
		WantThumbnail   bool           `json:"want_thumbnail"`
		WantCompression bool           `json:"want_compression"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.FileName) == "" {
		req.FileName = service.DefaultUploadFileName(req.ContentType)
	}
	if strings.TrimSpace(req.ContentType) == "" {
		req.ContentType = "application/octet-stream"
	}
	// Video processing produces a thumbnail only. Never create or accept a
	// client-side compression derivative for video uploads.
	if strings.HasPrefix(strings.ToLower(req.ContentType), "video/") {
		req.WantCompression = false
	}
	clientParamsJSON, err := encodeClientUploadParameters(req.ClientAnalysis, req.WantThumbnail, req.WantCompression)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	expiredAt, err := parseRFC3339Ptr(req.ExpiredAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.OverwriteID != nil {
		target, targetErr := files.GetFileInWorkspace(strings.TrimSpace(*req.OverwriteID), req.WorkspaceID)
		if targetErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": targetErr.Error()})
			return
		}
		if target.IsFolder || (!result.Account.GetIsSuperuser() && target.AccountID.String() != result.Account.GetId()) {
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid overwrite target"})
			return
		}
		req.FileName, req.Description, req.ParentID = target.Name, target.Description, target.ParentID
		req.Usage, req.ApplicationType, req.Index, expiredAt = target.Usage, target.ApplicationType, target.Indexed, target.ExpiredAt
		req.WorkspaceID = target.WorkspaceID
	}
	if req.WorkspaceID != nil {
		if err := quota.CheckWorkspaceUploadQuota(c.Request.Context(), *req.WorkspaceID, result.Account.GetId(), req.FileSize); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	} else if err := quota.CheckUploadQuota(result.Account, req.FileSize, 1.0); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	if err := files.ValidatePoolUsage(service.AccessContext{Account: result.Account, Session: result.Session}, req.PoolID, req.FileSize, req.ContentType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	direct, err := files.DirectUploadBackend(req.PoolID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "use_proxied_upload": true})
		return
	}
	multipartBackend, multipartSupported := direct.(storage.MultipartDirectUploadBackend)
	if req.Multipart && !multipartSupported {
		c.JSON(http.StatusBadRequest, gin.H{"error": "storage pool does not support multipart direct uploads; use proxied upload", "use_proxied_upload": true})
		return
	}
	poolID := files.ResolvedPoolID(req.PoolID)

	// Resume: a prepare for the same file (hash), size, and destination that
	// already has an in-progress direct upload continues it instead of
	// starting over. The same task is returned, so both attempts share its
	// task-derived object key: already-uploaded multipart parts are skipped
	// and a repeated single-PUT overwrites in place. Keys are derived from
	// the task id (unique per upload), never from the hash, so an in-flight
	// upload can never collide with a completed file's object.
	resumeHash := ""
	if req.Hash != nil {
		resumeHash = strings.TrimSpace(*req.Hash)
	}
	if resumeHash != "" {
		if existing, resumeErr := tasks.FindResumableUploadTask(uuid.MustParse(result.Account.GetId()), poolID, resumeHash, req.FileSize, req.ParentID, req.WorkspaceID, req.OverwriteID); resumeErr == nil && existing != nil {
			// Keep the session alive so the hourly expiry sweep does not
			// collect a task the client is actively continuing.
			_ = tasks.DB().Model(&database.PersistentTask{}).Where("task_id = ?", existing.TaskID).Updates(map[string]any{"updated_at": time.Now(), "last_activity": time.Now()}).Error
			existingParams := decodeClientUploadParameters(existing.Parameters)
			if len(req.ClientAnalysis) > 0 {
				existingParams.ClientAnalysis = req.ClientAnalysis
			}
			existingParams.Thumbnail = existingParams.Thumbnail || req.WantThumbnail
			existingParams.Compression = existingParams.Compression || req.WantCompression
			if updatedParams, paramsErr := encodeClientUploadParameters(existingParams.ClientAnalysis, existingParams.Thumbnail, existingParams.Compression); paramsErr == nil {
				_ = tasks.DB().Model(&database.PersistentTask{}).Where("task_id = ?", existing.TaskID).Update("parameters", updatedParams).Error
				existing.Parameters = updatedParams
			}
			resumeKey := "uploads/" + existing.TaskID + "/source"
			if existing.SourceKey != nil && strings.TrimSpace(*existing.SourceKey) != "" {
				resumeKey = strings.TrimSpace(*existing.SourceKey)
			}
			if req.Multipart && existing.UploadID != nil && strings.TrimSpace(*existing.UploadID) != "" {
				uploaded := []int{}
				if parts, listErr := multipartBackend.ListParts(c.Request.Context(), resumeKey, *existing.UploadID); listErr == nil {
					for _, part := range parts {
						uploaded = append(uploaded, part.PartNumber)
					}
				}
				partSize, partCount := multipartPlan(req.FileSize)
				response := gin.H{
					"task_id": existing.TaskID, "status": database.UploadStatusUploading, "resumed": true,
					"object_key": resumeKey, "upload_id": *existing.UploadID,
					"part_size": partSize, "part_count": partCount,
					"uploaded_parts": uploaded,
					"expires_in":     900, "content_type": req.ContentType,
				}
				if err := addClientDerivativeURLs(c.Request.Context(), direct, resumeKey, existingParams, response); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
				c.JSON(http.StatusOK, response)
				return
			}
			url, urlErr := direct.PresignedPutURL(c.Request.Context(), resumeKey, 15*time.Minute, req.ContentType)
			if urlErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": urlErr.Error()})
				return
			}
			response := gin.H{"task_id": existing.TaskID, "status": database.UploadStatusUploading, "resumed": true, "object_key": resumeKey, "upload_url": url, "expires_in": 900, "content_type": req.ContentType}
			if err := addClientDerivativeURLs(c.Request.Context(), direct, resumeKey, existingParams, response); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, response)
			return
		}
	}
	task, err := tasks.CreateUploadTask(uuid.MustParse(result.Account.GetId()), req.FileName, &database.PersistentTask{
		Description: req.Description, Hash: req.Hash, ExpiredAt: expiredAt, Usage: req.Usage,
		ParentID: req.ParentID, OverwriteID: req.OverwriteID, ApplicationType: req.ApplicationType,
		Indexed: req.Index, WorkspaceID: req.WorkspaceID, Parameters: clientParamsJSON,
	}, req.FileSize, poolID, req.FileName, req.ContentType, 0, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	sourceKey := "uploads/" + task.TaskID + "/source"
	if err := tasks.DB().Model(&database.PersistentTask{}).Where("task_id = ?", task.TaskID).Updates(map[string]any{"source_key": sourceKey}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if req.Multipart {
		uploadID, mpErr := multipartBackend.CreateMultipartUpload(c.Request.Context(), sourceKey, req.ContentType)
		if mpErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": mpErr.Error()})
			return
		}
		if mpErr := tasks.DB().Model(&database.PersistentTask{}).Where("task_id = ?", task.TaskID).Update("upload_id", uploadID).Error; mpErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": mpErr.Error()})
			return
		}
		partSize, partCount := multipartPlan(req.FileSize)
		response := gin.H{
			"task_id": task.TaskID, "status": database.UploadStatusUploading,
			"object_key": sourceKey, "upload_id": uploadID,
			"part_size": partSize, "part_count": partCount,
			"uploaded_parts": []int{},
			"expires_in":     900, "content_type": req.ContentType,
		}
		if err := addClientDerivativeURLs(c.Request.Context(), direct, sourceKey, decodeClientUploadParameters(clientParamsJSON), response); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, response)
		return
	}
	url, err := direct.PresignedPutURL(c.Request.Context(), sourceKey, 15*time.Minute, req.ContentType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	response := gin.H{"task_id": task.TaskID, "status": database.UploadStatusUploading, "object_key": sourceKey, "upload_url": url, "expires_in": 900, "content_type": req.ContentType}
	if err := addClientDerivativeURLs(c.Request.Context(), direct, sourceKey, decodeClientUploadParameters(clientParamsJSON), response); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, response)
}

// presignUploadPart issues a presigned PUT URL for a single part of a
// multipart direct upload. Issuing one URL per request (instead of returning
// every part URL in prepare) keeps responses small and lets a client resume an
// interrupted upload by re-requesting the parts it still needs.
func presignUploadPart(c *gin.Context, files *service.FileService, tasks *service.TaskService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !requireUploadPermission(c, files, result) {
		return
	}
	var req struct {
		PartNumber int `json:"part_number"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	task, err := tasks.GetUploadTask(c.Param("taskId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if task.AccountID.String() != result.Account.GetId() {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if task.UploadStatus != database.UploadStatusUploading || task.UploadID == nil || strings.TrimSpace(*task.UploadID) == "" || task.SourceKey == nil || strings.TrimSpace(*task.SourceKey) == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "upload is not awaiting part upload", "status": task.UploadStatus})
		return
	}
	fileSize := int64(0)
	if task.FileSize != nil {
		fileSize = *task.FileSize
	}
	_, partCount := multipartPlan(fileSize)
	if req.PartNumber <= 0 || int64(req.PartNumber) > partCount {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("part_number out of range: expected 1..%d", partCount)})
		return
	}
	direct, err := files.DirectUploadBackend(task.PoolID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "use_proxied_upload": true})
		return
	}
	multipartBackend, ok := direct.(storage.MultipartDirectUploadBackend)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "storage pool does not support multipart direct uploads; use proxied upload", "use_proxied_upload": true})
		return
	}
	url, err := multipartBackend.PresignPartUpload(c.Request.Context(), *task.SourceKey, *task.UploadID, req.PartNumber, 15*time.Minute)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Refresh task activity so the hourly expiry sweep never expires a
	// multipart session that is still uploading parts.
	_ = tasks.DB().Model(&database.PersistentTask{}).Where("task_id = ?", task.TaskID).Updates(map[string]any{"updated_at": time.Now(), "last_activity": time.Now()}).Error
	c.JSON(http.StatusOK, gin.H{"part_number": req.PartNumber, "upload_url": url, "expires_in": 900, "content_type": stringValue(task.ContentType)})
}

func completeDirectUpload(c *gin.Context, files *service.FileService, tasks *service.TaskService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !requireUploadPermission(c, files, result) {
		return
	}
	task, err := tasks.GetUploadTask(c.Param("taskId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if task.AccountID.String() != result.Account.GetId() {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if task.UploadStatus == database.UploadStatusProcessing || task.UploadStatus == database.UploadStatusCompleted {
		if task.CreatedFileID != nil {
			file, fileErr := files.GetFileInWorkspace(*task.CreatedFileID, task.WorkspaceID)
			if fileErr == nil {
				c.JSON(http.StatusOK, file)
				return
			}
		}
	}
	clientParams := decodeClientUploadParameters(task.Parameters)
	clientProcessed := len(clientParams.ClientAnalysis) > 0 || clientParams.Thumbnail || clientParams.Compression
	if task.UploadStatus != database.UploadStatusUploading || task.SourceKey == nil || strings.TrimSpace(*task.SourceKey) == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "upload is not awaiting completion", "status": task.UploadStatus})
		return
	}
	backend, err := files.BackendForPoolID(task.PoolID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "use_proxied_upload": true})
		return
	}
	if task.UploadID != nil && strings.TrimSpace(*task.UploadID) != "" {
		multipartBackend, ok := backend.(storage.MultipartDirectUploadBackend)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "storage pool does not support multipart direct uploads; use proxied upload", "use_proxied_upload": true})
			return
		}
		parts, listErr := multipartBackend.ListParts(c.Request.Context(), *task.SourceKey, *task.UploadID)
		if listErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uploaded parts are unavailable"})
			return
		}
		fileSize := int64(0)
		if task.FileSize != nil {
			fileSize = *task.FileSize
		}
		_, partCount := multipartPlan(fileSize)
		if int64(len(parts)) != partCount {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("uploaded parts are incomplete: got %d, want %d", len(parts), partCount)})
			return
		}
		var totalSize int64
		for idx, part := range parts {
			if part.PartNumber != idx+1 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "uploaded parts are out of order or missing"})
				return
			}
			totalSize += part.Size
		}
		if totalSize != fileSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uploaded parts size does not match file_size"})
			return
		}
		if err := multipartBackend.CompleteMultipartUpload(c.Request.Context(), *task.SourceKey, *task.UploadID, parts); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	info, err := backend.Stat(c.Request.Context(), *task.SourceKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "uploaded object is unavailable"})
		return
	}
	if task.FileSize == nil || info.Size != *task.FileSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "uploaded object size does not match file_size"})
		return
	}
	if err := tasks.MarkProcessing(task.TaskID, *task.SourceKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	contentType := stringValue(task.ContentType)
	// S3 reports application/octet-stream when the presigned PUT carried no
	// Content-Type, so only a real (non-generic) stored type may override the
	// type the client declared in prepare. The authoritative type is resolved
	// from the actual bytes after the post-commit download below.
	if m := strings.TrimSpace(info.MimeType); m != "" && !strings.EqualFold(m, "application/octet-stream") {
		contentType = m
	}
	if strings.HasPrefix(contentType, "image/") && (len(clientParams.ClientAnalysis) == 0 || !clientParams.Compression) {
		clientProcessed = false
	}
	if strings.HasPrefix(contentType, "video/") && (len(clientParams.ClientAnalysis) == 0 || !clientParams.Thumbnail) {
		clientProcessed = false
	}
	object, err := files.CreateStoredObject(*task.SourceKey, &service.StagedFileInfo{Size: info.Size, ContentType: contentType})
	if err != nil {
		_ = tasks.MarkFailed(task.TaskID, err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var file *database.CloudFile
	if task.OverwriteID != nil && strings.TrimSpace(*task.OverwriteID) != "" {
		file, err = files.OverwriteFile(*task.OverwriteID, object.ID, object.StorageKey)
	} else {
		file, err = files.CreatePendingUploadedFile(task.AccountID, task.WorkspaceID, stringValue(task.FileName), task.Description, task.Hash, task.ExpiredAt, task.Usage, task.ParentID, object.ID, task.PoolID, task.ApplicationType, object.StorageKey, task.Indexed)
	}
	if err != nil {
		_ = tasks.MarkFailed(task.TaskID, err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	file.Object = object
	if err := tasks.DB().Model(&database.PersistentTask{}).Where("task_id = ?", task.TaskID).Update("created_file_id", file.ID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if clientProcessed {
		if len(clientParams.ClientAnalysis) > 0 {
			refreshed, metadataErr := files.StoreClientSourceMetadata(file.ID, clientParams.ClientAnalysis)
			if metadataErr != nil {
				_ = tasks.MarkFailed(task.TaskID, metadataErr.Error())
				c.JSON(http.StatusBadRequest, gin.H{"error": metadataErr.Error()})
				return
			}
			file = refreshed
		}
		if clientParams.Thumbnail {
			thumbKey := clientThumbnailKey(*task.SourceKey)
			if _, thumbnailErr := files.CreateStoredDerivedFile(c.Request.Context(), backend, file.ID, thumbKey, "system.thumbnail", "image/jpeg"); thumbnailErr != nil {
				_ = tasks.MarkFailed(task.TaskID, thumbnailErr.Error())
				c.JSON(http.StatusBadRequest, gin.H{"error": thumbnailErr.Error()})
				return
			}
		}
		if clientParams.Compression {
			compressionKey := clientCompressionKey(*task.SourceKey)
			if _, compressionErr := files.CreateStoredDerivedFile(c.Request.Context(), backend, file.ID, compressionKey, "system.compression.low", "image/webp"); compressionErr != nil {
				_ = tasks.MarkFailed(task.TaskID, compressionErr.Error())
				c.JSON(http.StatusBadRequest, gin.H{"error": compressionErr.Error()})
				return
			}
		}
		if clientParams.Thumbnail || clientParams.Compression {
			if derivativeErr := files.TouchCompatibilityFlags(file.ID); derivativeErr != nil {
				_ = tasks.MarkFailed(task.TaskID, derivativeErr.Error())
				c.JSON(http.StatusInternalServerError, gin.H{"error": derivativeErr.Error()})
				return
			}
		}
		if err := tasks.MarkCompleted(task.TaskID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := tasks.DB().Model(&database.CloudFile{}).Where("id = ?", file.ID).Update("upload_status", database.UploadStatusCompleted).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		file, err = files.GetFileInWorkspace(file.ID, task.WorkspaceID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := publishFileMetadataUpdated(c.Request.Context(), bus, dispatcher, file, task.TaskID); err != nil {
			logging.Log.Error().Err(err).Str("fileId", file.ID).Msg("failed to publish client upload state")
		}
		c.JSON(http.StatusOK, file)
		return
	}
	// Legacy direct uploads still use server-side analysis when the client did
	// not provide metadata or derivatives.
	analysis, resolvedMime, analysisErr := files.RefreshStoredObjectAnalysis(c.Request.Context(), backend, object.ID, *task.SourceKey, contentType)
	if resolvedMime != "" {
		contentType = resolvedMime
	}
	if analysisErr != nil {
		logging.Log.Warn().Err(analysisErr).Str("fileId", file.ID).Str("storageKey", *task.SourceKey).Msg("failed to analyze direct-uploaded object")
	} else {
		refreshed, storeErr := files.StoreSourceAnalysis(file.ID, analysis)
		if storeErr != nil {
			logging.Log.Warn().Err(storeErr).Str("fileId", file.ID).Msg("failed to persist direct-uploaded object metadata")
		} else {
			file = refreshed
		}
	}
	if err := publishFileUploaded(c.Request.Context(), bus, dispatcher, eventbus.FileUploadedEvent{
		FileID: file.ID, TaskID: task.TaskID, ContentType: contentType, StorageKey: *object.StorageKey,
	}); err != nil {
		_ = tasks.MarkFailed(task.TaskID, err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := publishFileMetadataUpdated(c.Request.Context(), bus, dispatcher, file, task.TaskID); err != nil {
		logging.Log.Error().Err(err).Str("fileId", file.ID).Msg("failed to publish direct upload state")
	}
	c.JSON(http.StatusOK, file)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func publishFileMetadataUpdated(ctx context.Context, bus *eventbus.Bus, dispatcher dispatch.Dispatcher, file *database.CloudFile, taskID string) error {
	if file == nil {
		return fmt.Errorf("file is required")
	}
	snapshot := eventbus.FileMetadataSnapshot{ID: file.ID, Name: file.Name, Status: int(file.UploadStatus), UpdatedAt: file.UpdatedAt, Usage: file.Usage, ApplicationType: file.ApplicationType}
	if file.Object != nil {
		snapshot.MimeType, snapshot.Size, snapshot.HasCompression, snapshot.HasThumbnail = file.Object.MimeType, file.Object.Size, file.Object.HasCompression, file.Object.HasThumbnail
		snapshot.Hash = file.Object.Hash
	}
	evt := eventbus.FileMetadataUpdatedEvent{Event: eb.Event{EventID: database.NewID(), Timestamp: time.Now().UTC(), EventType: "filesystem.file.updated.v1", StreamName: "filesystem_events"}, FileID: file.ID, TaskID: taskID, AccountID: file.AccountID.String(), Status: int(file.UploadStatus), File: snapshot}
	if dispatcher != nil {
		if d, ok := dispatcher.(metadataEventDispatcher); ok {
			return d.PublishFileMetadataUpdated(ctx, evt)
		}
	}
	if bus != nil {
		return bus.PublishFileMetadataUpdated(ctx, evt)
	}
	return fmt.Errorf("no metadata event sink configured")
}

// @Summary Direct upload
// @Tags uploads
// @Produce json
// @Success 200 {object} database.CloudFile
// @Router /api/files/upload/direct [post]
func directUpload(c *gin.Context, cfg *config.Config, files *service.FileService, tasks *service.TaskService, quota *service.QuotaService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
	startedAt := time.Now()
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !requireUploadPermission(c, files, result) {
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	if strings.TrimSpace(fileHeader.Filename) == "" {
		fileHeader.Filename = service.DefaultUploadFileName(fileHeader.Header.Get("Content-Type"))
	}
	description := optionalStringPtr(c.PostForm("description"))
	hash := optionalStringPtr(c.PostForm("hash"))
	parentID := optionalStringPtr(c.PostForm("parent_id"))
	workspaceID := optionalStringPtr(c.PostForm("workspace_id"))
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
		overwriteTarget, err = files.GetFileInWorkspace(strings.TrimSpace(*overwriteID), workspaceID)
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
		if workspaceID != nil && overwriteTarget.WorkspaceID != nil && *workspaceID != *overwriteTarget.WorkspaceID {
			c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_id must match the overwritten file"})
			return
		}
		workspaceID = overwriteTarget.WorkspaceID
	}

	logQuotaCheck(result.Account, fileHeader.Size, 1.0, "direct-upload", false, nil)
	var quotaErr error
	if workspaceID != nil {
		quotaErr = quota.CheckWorkspaceUploadQuota(c.Request.Context(), *workspaceID, result.Account.GetId(), fileHeader.Size)
	} else {
		quotaErr = quota.CheckUploadQuota(result.Account, fileHeader.Size, 1.0)
	}
	if quotaErr != nil {
		logQuotaCheck(result.Account, fileHeader.Size, 1.0, "direct-upload", true, quotaErr)
		status := http.StatusBadRequest
		if errors.Is(quotaErr, service.ErrQuotaExceeded) {
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"error": quotaErr.Error()})
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
	hasher := sha256.New()
	written, err := io.Copy(out, io.TeeReader(reader, hasher))
	if err != nil {
		_ = out.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = out.Close()
	stagedInfo := service.NewStagedFileInfo(tempPath, fileHeader.Header.Get("Content-Type"), written, hex.EncodeToString(hasher.Sum(nil)))
	stagedDuration := time.Since(startedAt)
	logging.Log.Info().
		Str("accountId", result.Account.GetId()).
		Str("tempPath", tempPath).
		Dur("stageDuration", stagedDuration).
		Msg("direct upload staged to disk")
	cleanupTempPath := true
	defer func() {
		if cleanupTempPath {
			_ = os.Remove(tempPath)
		}
	}()
	var createdFile *database.CloudFile
	var object *database.FileObject
	analysisStartedAt := time.Now()
	var analysis *service.SourceAnalysis
	var analysisErr error
	if overwriteTarget != nil && fastMode {
		analysis, analysisErr = files.AnalyzeSourceFile(c.Request.Context(), tempPath, fileHeader.Header.Get("Content-Type"))
		if updated, applied, err := files.FastOverwriteFile(overwriteTarget.ID, tempPath, analysis); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		} else if applied {
			createdFile = updated
			object = createdFile.Object
		}
	}
	if createdFile == nil {
		uploadStartedAt := time.Now()
		object, analysis, analysisErr, err = storeUploadedSource(c.Request.Context(), files, tempPath, stagedInfo)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		uploadDuration := time.Since(uploadStartedAt)
		analysisDuration := time.Since(analysisStartedAt)
		storageKey := &object.ID
		if overwriteTarget != nil {
			createdFile, err = files.OverwriteFile(overwriteTarget.ID, object.ID, storageKey)
		} else {
			createdFile, err = files.CreateWorkspaceUploadedFile(uuid.MustParse(result.Account.GetId()), workspaceID, fileHeader.Filename, description, hash, expiredAt, usage, parentID, object.ID, nil, appType, storageKey, indexed)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if analysisErr != nil {
			logging.Log.Warn().Err(analysisErr).Str("fileId", createdFile.ID).Msg("failed to analyze source file")
		}
		logging.Log.Info().
			Str("accountId", result.Account.GetId()).
			Str("fileId", createdFile.ID).
			Str("objectId", object.ID).
			Str("contentType", object.MimeType).
			Int64("size", object.Size).
			Dur("analysisDuration", analysisDuration).
			Dur("uploadDuration", uploadDuration).
			Dur("totalDuration", time.Since(startedAt)).
			Msg("direct upload persisted")
	} else if object == nil {
		object = createdFile.Object
	}
	analysisDuration := time.Since(analysisStartedAt)
	if createdFile == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "overwrite failed"})
		return
	}
	if createdFile.Object == nil {
		createdFile, err = files.GetFileInWorkspace(createdFile.ID, workspaceID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	logging.Log.Info().
		Str("accountId", result.Account.GetId()).
		Str("fileId", createdFile.ID).
		Str("objectId", deref(createdFile.ObjectID)).
		Dur("analysisDuration", analysisDuration).
		Dur("totalDuration", time.Since(startedAt)).
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
	eventStart := time.Now()
	if err := publishFileUploaded(c.Request.Context(), bus, dispatcher, eventbus.FileUploadedEvent{FileID: createdFile.ID, ContentType: contentType, StorageKey: storageKey, ProcessingFilePath: tempPath, IsTempFile: true}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	eventDuration := time.Since(eventStart)
	cleanupTempPath = false
	logging.Log.Info().
		Str("fileId", createdFile.ID).
		Dur("eventDuration", eventDuration).
		Dur("totalDuration", time.Since(startedAt)).
		Msg("direct upload complete")
	c.JSON(http.StatusOK, createdFile)
}

func uploadChunk(c *gin.Context, cfg *config.Config, files *service.FileService, tasks *service.TaskService) {
	startedAt := time.Now()
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !requireUploadPermission(c, files, result) {
		return
	}
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
	task, err := tasks.GetUploadTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if task.AccountID != uuid.MustParse(result.Account.GetId()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if idx >= task.ChunksCount || task.FileSize == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "chunk index out of range"})
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
	copyStart := time.Now()
	if _, err := service.WriteUploadChunk(cfg.Storage.TempDir, taskID, idx, task.ChunkSize, *task.FileSize, reader); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	copyDuration := time.Since(copyStart)
	dbStart := time.Now()
	if err := tasks.UpdateUploadedChunk(taskID, idx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	dbDuration := time.Since(dbStart)
	logging.Log.Debug().
		Str("taskId", taskID).
		Int("chunkIndex", idx).
		Int64("chunkSize", fileHeader.Size).
		Dur("copyDuration", copyDuration).
		Dur("dbDuration", dbDuration).
		Dur("totalDuration", time.Since(startedAt)).
		Msg("upload chunk staged")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// @Summary Complete upload
// @Tags uploads
// @Produce json
// @Param taskId path string true "Task ID"
// @Success 200 {object} database.CloudFile
// @Router /api/files/upload/complete/{taskId} [post]
func completeUpload(c *gin.Context, cfg *config.Config, files *service.FileService, tasks *service.TaskService, quota *service.QuotaService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
	startedAt := time.Now()
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !requireUploadPermission(c, files, result) {
		return
	}
	taskID := c.Param("taskId")
	task, err := tasks.GetUploadTaskWithChunks(taskID)
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
	lookupDuration := time.Since(startedAt)
	mergedPath := filepath.Join(cfg.Storage.TempDir, taskID+".upload")
	cleanupMergedPath := true
	defer func() {
		if cleanupMergedPath {
			_ = os.Remove(mergedPath)
		}
	}()
	if task.FileSize == nil || task.ChunksUploaded != task.ChunksCount {
		c.JSON(http.StatusBadRequest, gin.H{"error": "upload is incomplete"})
		return
	}
	if task.WorkspaceID != nil {
		if err := quota.CheckWorkspaceUploadQuota(c.Request.Context(), *task.WorkspaceID, result.Account.GetId(), *task.FileSize); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, service.ErrQuotaExceeded) {
				status = http.StatusForbidden
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
	}
	mergeDuration := time.Duration(0)
	stat, err := os.Stat(mergedPath)
	if err != nil {
		legacyChunkDir := filepath.Join(cfg.Storage.TempDir, taskID)
		if !os.IsNotExist(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "upload source is unavailable"})
			return
		}
		mergeStart := time.Now()
		if err := files.MergeChunks(taskID, legacyChunkDir, mergedPath, task.ChunksCount, nil); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "upload source is unavailable"})
			return
		}
		mergeDuration = time.Since(mergeStart)
		logging.Log.Info().Str("taskId", taskID).Dur("legacyMergeDuration", mergeDuration).Msg("legacy upload chunks merged")
		defer os.RemoveAll(legacyChunkDir)
		stat, err = os.Stat(mergedPath)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "upload source is unavailable"})
			return
		}
	}
	if stat.Size() != *task.FileSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "upload size is incomplete"})
		return
	}
	logging.Log.Info().
		Str("taskId", taskID).
		Int("chunks", task.ChunksCount).
		Str("stagedPath", mergedPath).
		Dur("lookupDuration", lookupDuration).
		Dur("mergeDuration", mergeDuration).
		Msg("upload chunks merged")
	ctx := service.AccessContext{Account: result.Account, Session: result.Session}
	_ = ctx
	var created *database.CloudFile
	var object *database.FileObject
	analysisStartedAt := time.Now()
	var analysis *service.SourceAnalysis
	var analysisErr error
	if task.FastMode && task.OverwriteID != nil && strings.TrimSpace(*task.OverwriteID) != "" {
		analysis, analysisErr = files.AnalyzeSourceFile(c.Request.Context(), mergedPath, "")
		if updated, applied, err := files.FastOverwriteFile(strings.TrimSpace(*task.OverwriteID), mergedPath, analysis); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		} else if applied {
			created = updated
			object = created.Object
		}
	}
	if created == nil {
		uploadStartedAt := time.Now()
		object, analysis, analysisErr, err = storeUploadedSource(c.Request.Context(), files, mergedPath, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		uploadDuration := time.Since(uploadStartedAt)
		analysisDuration := time.Since(analysisStartedAt)
		storageKey := &object.ID
		if task.OverwriteID != nil && strings.TrimSpace(*task.OverwriteID) != "" {
			created, err = files.OverwriteFile(strings.TrimSpace(*task.OverwriteID), object.ID, storageKey)
		} else {
			created, err = files.CreateWorkspaceUploadedFile(task.AccountID, task.WorkspaceID, deref(task.FileName), task.Description, task.Hash, task.ExpiredAt, task.Usage, task.ParentID, object.ID, task.PoolID, task.ApplicationType, storageKey, task.Indexed)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if analysisErr != nil {
			logging.Log.Warn().Err(analysisErr).Str("fileId", created.ID).Msg("failed to analyze source file")
		}
		logging.Log.Info().
			Str("taskId", taskID).
			Str("fileId", created.ID).
			Str("objectId", object.ID).
			Str("contentType", object.MimeType).
			Int64("size", object.Size).
			Dur("analysisDuration", analysisDuration).
			Dur("uploadDuration", uploadDuration).
			Dur("totalDuration", time.Since(startedAt)).
			Msg("chunked upload persisted")
	} else if object == nil {
		object = created.Object
	}
	analysisDuration := time.Since(analysisStartedAt)
	if created == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "overwrite failed"})
		return
	}
	if created.Object == nil {
		created, err = files.GetFileInWorkspace(created.ID, task.WorkspaceID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	logging.Log.Info().
		Str("taskId", taskID).
		Str("fileId", created.ID).
		Str("objectId", deref(created.ObjectID)).
		Dur("analysisDuration", analysisDuration).
		Dur("totalDuration", time.Since(startedAt)).
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
	eventStart := time.Now()
	if err := publishFileUploaded(c.Request.Context(), bus, dispatcher, eventbus.FileUploadedEvent{FileID: created.ID, TaskID: task.TaskID, ContentType: contentType, StorageKey: storageKey, ProcessingFilePath: mergedPath, IsTempFile: true}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	eventDuration := time.Since(eventStart)
	cleanupMergedPath = false
	_ = tasks.MarkCompleted(task.TaskID)
	logging.Log.Info().
		Str("taskId", taskID).
		Str("fileId", created.ID).
		Dur("eventDuration", eventDuration).
		Dur("totalDuration", time.Since(startedAt)).
		Msg("chunked upload complete")
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
		"id":         token.ID,
		"label":      token.Label,
		"secret":     rawToken,
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

type sourceAnalysisResult struct {
	analysis *service.SourceAnalysis
	err      error
}

type sourceStorageResult struct {
	storageKey string
	err        error
}

// storeUploadedSource keeps source analysis synchronous for the response, but
// runs it beside the independent storage transfer. The object row is created
// only after both complete so its first persisted metadata is already final.
func storeUploadedSource(ctx context.Context, files *service.FileService, path string, info *service.StagedFileInfo) (*database.FileObject, *service.SourceAnalysis, error, error) {
	if info == nil {
		var err error
		info, err = service.InspectStagedFile(path, "")
		if err != nil {
			return nil, nil, nil, err
		}
	}

	analysisCh := make(chan sourceAnalysisResult, 1)
	storageCh := make(chan sourceStorageResult, 1)
	go func() {
		analysis, err := files.AnalyzeSourceFile(ctx, path, info.ContentType)
		analysisCh <- sourceAnalysisResult{analysis: analysis, err: err}
	}()
	go func() {
		storageKey, err := files.UploadStagedFile(ctx, path, info)
		storageCh <- sourceStorageResult{storageKey: storageKey, err: err}
	}()

	analysisResult := <-analysisCh
	storageResult := <-storageCh
	if storageResult.err != nil {
		return nil, analysisResult.analysis, analysisResult.err, storageResult.err
	}
	object, err := files.CreateUploadedObject(storageResult.storageKey, info, analysisResult.analysis)
	if err != nil {
		_ = files.Storage().Delete(context.Background(), storageResult.storageKey)
		return nil, analysisResult.analysis, analysisResult.err, err
	}
	return object, analysisResult.analysis, analysisResult.err, nil
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
	task, err := tasks.GetUploadTaskWithChunks(c.Param("taskId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": task.TaskID, "progress": task.Progress})
}

func uploadStatus(c *gin.Context, tasks *service.TaskService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	task, err := tasks.GetUploadTask(c.Param("taskId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if task.AccountID.String() != result.Account.GetId() {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": task.TaskID, "status": task.UploadStatus, "progress": task.Progress, "file_id": task.CreatedFileID, "error": task.ProcessingError})
}

func uploadResume(c *gin.Context, tasks *service.TaskService) {
	task, err := tasks.GetUploadTask(c.Param("taskId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, task)
}

func cancelUpload(c *gin.Context, files *service.FileService, tasks *service.TaskService) {
	taskID := c.Param("taskId")
	task, err := tasks.GetUploadTask(taskID)
	if err == nil && task != nil && task.SourceKey != nil {
		if backend, backendErr := files.BackendForPoolID(task.PoolID); backendErr == nil {
			if task.UploadID != nil && strings.TrimSpace(*task.UploadID) != "" {
				if multipartBackend, ok := backend.(storage.MultipartDirectUploadBackend); ok {
					_ = multipartBackend.AbortMultipartUpload(c.Request.Context(), *task.SourceKey, *task.UploadID)
				}
			}
			_ = backend.Delete(c.Request.Context(), *task.SourceKey)
			_ = backend.Delete(c.Request.Context(), *task.SourceKey+".thumbnail")
		}
	}
	if err := tasks.FailTask(taskID, "cancelled"); err != nil {
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
