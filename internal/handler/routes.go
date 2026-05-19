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
)

func RegisterRoutes(r *gin.Engine, cfg *config.Config, files *service.FileService, tasks *service.TaskService, quota *service.QuotaService, bus *eventbus.Bus, dispatcher dispatch.Dispatcher) {
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
		f.GET("/:id/open", func(c *gin.Context) { openFile(c, cfg, files) })
		f.GET("/:id/references", func(c *gin.Context) { c.JSON(http.StatusOK, []any{}) })
		f.GET("/root/children", func(c *gin.Context) { listRootIndexed(c, files) })
		f.GET("/:id/children", func(c *gin.Context) { listChildren(c, files) })
		f.POST("/folders", func(c *gin.Context) { createFolder(c, files) })
		f.GET("/me", func(c *gin.Context) { listRootOwned(c, files) })
		f.GET("/unindexed", func(c *gin.Context) { listUnindexed(c, files) })
		f.POST("/batches/delete", func(c *gin.Context) { batchRecycleFiles(c, files, bus, dispatcher) })
		f.DELETE("/:id", func(c *gin.Context) { deleteFile(c, files, bus, dispatcher) })
		f.DELETE("/me/recycle", func(c *gin.Context) { purgeMyRecycleBin(c, files, bus, dispatcher) })
		f.DELETE("/recycle", func(c *gin.Context) { purgeMyRecycleBin(c, files, bus, dispatcher) })
		f.POST("/:id/recycle", func(c *gin.Context) { recycleFile(c, files, bus, dispatcher) })
		f.POST("/:id/restore", func(c *gin.Context) { restoreFile(c, files, bus, dispatcher) })
		f.GET("/:id/permissions", func(c *gin.Context) { getFilePermissions(c, files) })
		f.PUT("/:id/permissions", func(c *gin.Context) { updateFilePermissions(c, files) })
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
// @Param original query bool false "Prefer original variant"
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
		if thumb, err := resolveDerivedFile(files, file.ID, "system.thumbnail"); err == nil {
			file = thumb
		} else {
			c.JSON(http.StatusNotFound, gin.H{"error": "thumbnail not available"})
			return
		}
	} else if variant := c.Query("original"); strings.EqualFold(variant, "1") || strings.EqualFold(variant, "true") {
		if original, err := resolveDerivedFile(files, file.ID, "system.original"); err == nil {
			file = original
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
	offset, take, query, order, orderDesc := parseListQuery(c, 0, 50)
	items, err := files.GetChildren(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	items = filterAndSortFiles(items, query, order, orderDesc)
	total := len(items)
	items = paginateFiles(items, offset, take)
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
	perkLevel := result.Account.GetPerkLevel()
	perkSubscriptionLevel := int32(0)
	hasPerkSubscription := false
	if sub := result.Account.GetPerkSubscription(); sub != nil {
		hasPerkSubscription = true
		perkSubscriptionLevel = sub.GetPerkLevel()
	}
	logging.Log.Info().
		Str("accountId", result.Account.GetId()).
		Bool("isSuperuser", result.Account.GetIsSuperuser()).
		Int32("perkLevel", perkLevel).
		Bool("hasPerkSubscription", hasPerkSubscription).
		Int32("perkSubscriptionLevel", perkSubscriptionLevel).
		Msg("quota endpoint accessed")
	summary, err := quota.GetSummary(result.Account)
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
	offset, take, query, order, orderDesc := parseListQuery(c, 0, 50)
	items, err := files.ListRoot(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items = filterAndSortFiles(items, query, order, orderDesc)
	total := len(items)
	items = paginateFiles(items, offset, take)
	c.Header("X-Total", strconv.Itoa(total))
	c.JSON(http.StatusOK, items)
}

func listRootOwned(c *gin.Context, files *service.FileService) {
	result, _, ok := auth.GetAuth(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	offset, take, query, order, orderDesc := parseListQuery(c, 0, 20)
	items, err := files.ListOwned(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items = filterAndSortFiles(items, query, order, orderDesc)
	items = rootOnly(items)
	total := len(items)
	items = paginateFiles(items, offset, take)
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
	offset, take, query, order, orderDesc := parseListQuery(c, 0, 20)
	items, err := files.ListUnindexed(uuid.MustParse(result.Account.GetId()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items = filterAndSortUnindexed(items, pool, recycled, query, order, orderDesc)
	total := len(items)
	items = paginateFiles(items, offset, take)
	c.Header("X-Total", strconv.Itoa(total))
	c.JSON(http.StatusOK, items)
}

func parseListQuery(c *gin.Context, defaultOffset, defaultTake int) (offset, take int, query, order string, orderDesc bool) {
	offset = defaultOffset
	take = defaultTake
	order = strings.TrimSpace(c.Query("order"))
	if order == "" {
		order = "date"
	}
	orderDesc = true
	if v := strings.TrimSpace(c.Query("offset")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if v := strings.TrimSpace(c.Query("take")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			take = n
		}
	}
	if v := strings.TrimSpace(c.Query("query")); v != "" {
		query = v
	}
	if v := strings.TrimSpace(c.Query("orderDesc")); v != "" {
		orderDesc = !(strings.EqualFold(v, "false") || v == "0")
	}
	return offset, take, query, order, orderDesc
}

func filterAndSortFiles(items []database.CloudFile, query, order string, orderDesc bool) []database.CloudFile {
	filtered := make([]database.CloudFile, 0, len(items))
	for _, item := range items {
		if query != "" && !strings.Contains(strings.ToLower(item.Name), strings.ToLower(query)) {
			continue
		}
		filtered = append(filtered, item)
	}
	sortFiles(filtered, order, orderDesc)
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

func filterAndSortUnindexed(items []database.CloudFile, pool string, recycled bool, query, order string, orderDesc bool) []database.CloudFile {
	filtered := make([]database.CloudFile, 0, len(items))
	for _, item := range items {
		if item.IsMarkedRecycle != recycled {
			continue
		}
		if item.Indexed || item.IsFolder {
			continue
		}
		if pool != "" {
			if item.PoolID == nil || *item.PoolID != pool {
				continue
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Name), strings.ToLower(query)) {
			continue
		}
		filtered = append(filtered, item)
	}
	sortFiles(filtered, order, orderDesc)
	return filtered
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
	if err := files.DeleteFile(file.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	publishFileAction(c.Request.Context(), bus, dispatcher, eventbus.FileActionEvent{Action: "delete", FileID: file.ID, AccountID: result.Account.GetId(), Name: file.Name})
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
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	count, err := files.RecycleBatch(req.IDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, id := range req.IDs {
		publishFileAction(c.Request.Context(), bus, dispatcher, eventbus.FileActionEvent{Action: "recycle", FileID: id, AccountID: result.Account.GetId()})
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
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
	poolMultiplier := 1.0
	if req.PoolID != nil && strings.TrimSpace(*req.PoolID) != "" {
		if pool, err := files.GetPool(*req.PoolID); err == nil && pool.BillingConfig.CostMultiplier != nil && *pool.BillingConfig.CostMultiplier > 0 {
			poolMultiplier = *pool.BillingConfig.CostMultiplier
		}
	}
	logQuotaCheck(result.Account, req.FileSize, poolMultiplier, "create-upload", false, nil)
	if err := quota.CheckUploadQuota(result.Account, req.FileSize, poolMultiplier); err != nil {
		logQuotaCheck(result.Account, req.FileSize, poolMultiplier, "create-upload", true, err)
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
	payload := &database.PersistentTask{Description: req.Description, Hash: req.Hash, ExpiredAt: expiredAt, Usage: req.Usage, ParentID: req.ParentID, ApplicationType: req.ApplicationType, Indexed: req.Index}
	task, err := tasks.CreateUploadTask(uuid.MustParse(result.Account.GetId()), name, payload, req.FileSize, req.PoolID, name, req.ContentType, req.ChunkSize, chunks)
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
	usage := optionalStringPtr(c.PostForm("usage"))
	appType := optionalStringPtr(c.PostForm("application_type"))
	indexed := optionalBool(c.PostForm("index"))
	expiredAt, err := parseRFC3339Ptr(optionalStringPtr(c.PostForm("expired_at")))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	logQuotaCheck(result.Account, fileHeader.Size, 1.0, "direct-upload", false, nil)
	if err := quota.CheckUploadQuota(result.Account, fileHeader.Size, 1.0); err != nil {
		logQuotaCheck(result.Account, fileHeader.Size, 1.0, "direct-upload", true, err)
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
	object, err := files.DetectAndCreateObject(tempPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	storageKey := &object.ID
	createdFile, err := files.CreateUploadedFile(uuid.MustParse(result.Account.GetId()), fileHeader.Filename, description, hash, expiredAt, usage, parentID, object.ID, nil, appType, storageKey, indexed)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if analysis, err := files.AnalyzeSourceFile(c.Request.Context(), tempPath, object.MimeType); err == nil {
		if updated, err := files.StoreSourceAnalysis(createdFile.ID, analysis); err == nil {
			createdFile = updated
		} else {
			logging.Log.Warn().Err(err).Str("fileId", createdFile.ID).Msg("failed to persist source analysis")
		}
	} else {
		logging.Log.Warn().Err(err).Str("fileId", createdFile.ID).Msg("failed to analyze source file")
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
	logging.Log.Info().
		Str("accountId", result.Account.GetId()).
		Str("fileId", createdFile.ID).
		Str("objectId", object.ID).
		Msg("direct upload stored")
	_ = tasks
	_ = publishFileUploaded(c.Request.Context(), bus, dispatcher, eventbus.FileUploadedEvent{FileID: createdFile.ID, ContentType: object.MimeType, StorageKey: object.ID, ProcessingFilePath: tempPath, IsTempFile: true})
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
	object, err := files.DetectAndCreateObject(mergedPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx := service.AccessContext{Account: result.Account, Session: result.Session}
	_ = ctx
	storageKey := &object.ID
	created, err := files.CreateUploadedFile(task.AccountID, deref(task.FileName), task.Description, task.Hash, task.ExpiredAt, task.Usage, task.ParentID, object.ID, task.PoolID, task.ApplicationType, storageKey, task.Indexed)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if analysis, err := files.AnalyzeSourceFile(c.Request.Context(), mergedPath, object.MimeType); err == nil {
		if updated, err := files.StoreSourceAnalysis(created.ID, analysis); err == nil {
			created = updated
		} else {
			logging.Log.Warn().Err(err).Str("fileId", created.ID).Msg("failed to persist source analysis")
		}
	} else {
		logging.Log.Warn().Err(err).Str("fileId", created.ID).Msg("failed to analyze source file")
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
	logging.Log.Info().
		Str("taskId", taskID).
		Str("fileId", created.ID).
		Str("objectId", object.ID).
		Msg("upload stored")
	if err := publishFileUploaded(c.Request.Context(), bus, dispatcher, eventbus.FileUploadedEvent{FileID: created.ID, TaskID: task.TaskID, ContentType: object.MimeType, StorageKey: object.ID, ProcessingFilePath: mergedPath, IsTempFile: true}); err != nil {
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

func publishFileUploaded(ctx context.Context, bus *eventbus.Bus, dispatcher dispatch.Dispatcher, evt eventbus.FileUploadedEvent) error {
	if dispatcher != nil {
		return dispatcher.PublishFileUploaded(ctx, evt)
	}
	if bus != nil {
		return bus.PublishFileUploaded(ctx, evt)
	}
	return nil
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
	perkLevel := account.GetPerkLevel()
	perkSubscriptionLevel := int32(0)
	hasPerkSubscription := false
	if sub := account.GetPerkSubscription(); sub != nil {
		hasPerkSubscription = true
		perkSubscriptionLevel = sub.GetPerkLevel()
	}
	entry := logging.Log.Info().
		Str("source", source).
		Str("accountId", account.GetId()).
		Bool("isSuperuser", account.GetIsSuperuser()).
		Int32("perkLevel", perkLevel).
		Bool("hasPerkSubscription", hasPerkSubscription).
		Int32("perkSubscriptionLevel", perkSubscriptionLevel).
		Int64("fileSize", fileSize).
		Float64("costMultiplier", costMultiplier)
	if refused && err != nil {
		entry = entry.Err(err)
	}
	if refused {
		logging.Log.Warn().
			Str("source", source).
			Str("accountId", account.GetId()).
			Bool("isSuperuser", account.GetIsSuperuser()).
			Int32("perkLevel", perkLevel).
			Bool("hasPerkSubscription", hasPerkSubscription).
			Int32("perkSubscriptionLevel", perkSubscriptionLevel).
			Int64("fileSize", fileSize).
			Float64("costMultiplier", costMultiplier).
			Err(err).
			Msg("upload quota check")
		return
	}
	entry.Msg("upload quota check")
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
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
