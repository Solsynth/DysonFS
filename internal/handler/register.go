package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts every HTTP surface. It is the compatibility entry point
// for tests and single-binary deployments; a module system mounts the
// individual Register* functions instead, so a disabled module registers
// nothing.
func RegisterRoutes(r *gin.Engine, d Deps) {
	RegisterFileRoutes(r, d)
	RegisterContentEditRoutes(r, d)
	RegisterUploadRoutes(r, d)
	RegisterPoolRoutes(r, d)
	RegisterBillingRoutes(r, d)
	RegisterAccountRoutes(r, d)
	RegisterS3TokenRoutes(r, d)
	RegisterNodeRoutes(r, d)
	RegisterStorageAdminRoutes(r, d)
	RegisterReanalysisRoutes(r, d)
	RegisterStorageNodeRoutes(r, d)
	// The picker hand-off redeem route mounts only when an issuer is
	// configured; an unconfigured deployment keeps the picker on the
	// cookie-backed session exactly as before.
	if strings.TrimSpace(d.Config.Handoff.Issuer) != "" {
		RegisterHandoffRoutes(r, d)
	}
	if d.Config.WebDAV.Enabled {
		RegisterWebDAVRoutes(r, d)
	}
	// WOPI mounts when a service is present; each handler answers 404 on its
	// own when wopi.enabled is false, which keeps the old single-binary
	// behaviour and its tests intact.
	if d.Wopi != nil {
		RegisterWOPIRoutes(r, d)
	}
	r.NoRoute(func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"error": "not found"}) })
}

// RegisterFileRoutes mounts the file read/write surface.
func RegisterFileRoutes(r *gin.Engine, d Deps) {
	registerBusStash(r, d.Bus)
	files, quota := d.Files, d.Quota
	f := r.Group("/api/files")
	{
		f.GET("/meta", func(c *gin.Context) { listFilesMetadata(c, files) })
		f.GET("/:id", func(c *gin.Context) { openFile(c, d.Config, files) })
		f.GET("/:id/info", func(c *gin.Context) { fileInfo(c, files) })
		f.GET("/:id/breadcrumb", func(c *gin.Context) { fileBreadcrumb(c, files) })
		f.GET("/:id/open", func(c *gin.Context) { openFile(c, d.Config, files) })
		f.GET("/:id/references", func(c *gin.Context) { c.JSON(http.StatusOK, []any{}) })
		f.GET("/root/children", func(c *gin.Context) { listRootIndexed(c, files, quota) })
		f.GET("/:id/children", func(c *gin.Context) { listChildren(c, files, quota) })
		f.POST("/folders", func(c *gin.Context) { createFolder(c, files, quota) })
		f.GET("/me", func(c *gin.Context) { listRootOwned(c, files, quota) })
		f.GET("/unindexed", func(c *gin.Context) { listUnindexed(c, files, quota) })
		f.PATCH("/:id", func(c *gin.Context) { patchFile(c, files) })
		f.PUT("/:id/sensitive", func(c *gin.Context) { setSensitiveMarks(c, files) })
		f.POST("/recycle/batch", func(c *gin.Context) { batchRecycleFiles(c, files, d.Bus, d.Dispatcher) })
		f.POST("/restore/batch", func(c *gin.Context) { batchRestoreFiles(c, files, d.Bus, d.Dispatcher) })
		f.POST("/delete/batch", func(c *gin.Context) { batchDeleteFiles(c, files, d.Bus, d.Dispatcher) })
		f.POST("/move/batch", func(c *gin.Context) { batchMoveFiles(c, files, d.Bus, d.Dispatcher) })
		f.DELETE("/:id", func(c *gin.Context) { deleteFile(c, files, d.Bus, d.Dispatcher) })
		f.DELETE("/me/recycle", func(c *gin.Context) { purgeMyRecycleBin(c, files, d.Bus, d.Dispatcher) })
		f.DELETE("/recycle", func(c *gin.Context) { purgeMyRecycleBin(c, files, d.Bus, d.Dispatcher) })
		f.POST("/:id/recycle", func(c *gin.Context) { recycleFile(c, files, d.Bus, d.Dispatcher) })
		f.POST("/:id/restore", func(c *gin.Context) { restoreFile(c, files, d.Bus, d.Dispatcher) })
		f.GET("/:id/permissions", func(c *gin.Context) { getFilePermissions(c, files) })
		f.PUT("/:id/permissions", func(c *gin.Context) { updateFilePermissions(c, files) })
	}
}

// RegisterContentEditRoutes mounts binary patch and content replacement.
func RegisterContentEditRoutes(r *gin.Engine, d Deps) {
	f := r.Group("/api/files")
	{
		f.PATCH("/:id/content", func(c *gin.Context) { patchFileContent(c, d.Files, d.Bus, d.Dispatcher) })
		f.PUT("/:id/content", func(c *gin.Context) { putFileContent(c, d.Files, d.Bus, d.Dispatcher) })
	}
}

// RegisterUploadRoutes mounts every upload entry point.
func RegisterUploadRoutes(r *gin.Engine, d Deps) {
	files, tasks := d.Files, d.Tasks
	u := r.Group("/api/files/upload")
	{
		u.POST("/prepare", func(c *gin.Context) { prepareDirectUpload(c, files, tasks, d.Quota) })
		u.POST("/:taskId/part", func(c *gin.Context) { presignUploadPart(c, files, tasks) })
		u.POST("/:taskId/complete", func(c *gin.Context) { completeDirectUpload(c, files, tasks, d.Bus, d.Dispatcher, d.Analysis) })
		u.POST("/create", func(c *gin.Context) { createUploadTask(c, d.Config, files, tasks, d.Quota) })
		u.POST("/direct", func(c *gin.Context) { directUpload(c, d.Config, files, tasks, d.Quota, d.Bus, d.Dispatcher, d.Analysis) })
		u.POST("/chunk/:taskId/:idx", func(c *gin.Context) { uploadChunk(c, d.Config, files, tasks) })
		u.POST("/complete/:taskId", func(c *gin.Context) { completeUpload(c, d.Config, files, tasks, d.Quota, d.Bus, d.Dispatcher, d.Analysis) })
		u.GET("/tasks", func(c *gin.Context) { listUploadTasks(c, tasks) })
		u.GET("/progress/:taskId", func(c *gin.Context) { uploadProgress(c, tasks) })
		u.GET("/status/:taskId", func(c *gin.Context) { uploadStatus(c, tasks) })
		u.GET("/resume/:taskId", func(c *gin.Context) { uploadResume(c, tasks) })
		u.DELETE("/:taskId", func(c *gin.Context) { cancelUpload(c, files, tasks) })
		u.GET("/stats", func(c *gin.Context) { uploadStats(c, tasks) })
		u.DELETE("/tasks/cleanup", func(c *gin.Context) { cleanupTasks(c, tasks) })
		u.GET("/tasks/recent", func(c *gin.Context) { recentTasks(c, tasks) })
		u.GET("/tasks/:taskId/details", func(c *gin.Context) { taskDetails(c, d.Config, tasks) })
	}
}

// RegisterPoolRoutes mounts pool CRUD and pool permissions.
func RegisterPoolRoutes(r *gin.Engine, d Deps) {
	files := d.Files
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
}

// RegisterBillingRoutes mounts quota and usage reporting.
func RegisterBillingRoutes(r *gin.Engine, d Deps) {
	quota := d.Quota
	b := r.Group("/api/billing")
	{
		b.GET("quota", func(c *gin.Context) { getQuota(c, quota) })
		b.GET("usage", func(c *gin.Context) { getUsage(c, quota) })
		b.GET("usage/:poolId", func(c *gin.Context) { getPoolUsage(c, quota) })
		b.GET("workspaces/:workspaceId/quota", func(c *gin.Context) { getWorkspaceQuota(c, quota) })
	}
}

// RegisterAccountRoutes mounts the identity endpoint.
func RegisterAccountRoutes(r *gin.Engine, d Deps) {
	a := r.Group("/api/account")
	{
		a.GET("/me", getAccountIdentity)
	}
}

// RegisterWOPIRoutes mounts the WOPI callback surface. WOPI authenticates with
// its own signed token, never the account middleware.
func RegisterWOPIRoutes(r *gin.Engine, d Deps) {
	wopi := d.Wopi
	w := r.Group("/wopi")
	{
		w.GET("/files/:id", func(c *gin.Context) { wopiCheckFileInfo(c, wopi) })
		w.GET("/files/:id/contents", func(c *gin.Context) { wopiGetFile(c, wopi) })
		w.POST("/files/:id/contents", func(c *gin.Context) { wopiPutFile(c, wopi) })
		w.POST("/files/:id", func(c *gin.Context) { wopiLock(c, wopi) })
	}
	f := r.Group("/api/files")
	{
		f.POST("/:id/edit", func(c *gin.Context) { createEditSession(c, wopi, d.Files) })
	}
}

// RegisterS3TokenRoutes mounts S3 access token management.
func RegisterS3TokenRoutes(r *gin.Engine, d Deps) {
	files := d.Files
	s3t := r.Group("/api/s3/tokens")
	{
		s3t.POST("", func(c *gin.Context) { createS3Token(c, files) })
		s3t.GET("", func(c *gin.Context) { listS3Tokens(c, files) })
		s3t.DELETE("/:id", func(c *gin.Context) { deleteS3Token(c, files) })
	}
}

// RegisterNodeRoutes mounts user-owned storage node management.
func RegisterNodeRoutes(r *gin.Engine, d Deps) {
	files := d.Files
	sn := r.Group("/api/nodes")
	{
		sn.POST("", func(c *gin.Context) { createNode(c, files) })
		sn.GET("", func(c *gin.Context) { listNodes(c, files) })
		sn.GET("/:id", func(c *gin.Context) { getNode(c, files) })
		sn.PATCH("/:id", func(c *gin.Context) { updateNode(c, files) })
		sn.DELETE("/:id", func(c *gin.Context) { deleteNode(c, files) })
	}
}

// RegisterStorageAdminRoutes mounts the administrative storage surface.
func RegisterStorageAdminRoutes(r *gin.Engine, d Deps) {
	files, tasks := d.Files, d.Tasks
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
}

// RegisterReanalysisRoutes mounts the admin metadata repair surface.
func RegisterReanalysisRoutes(r *gin.Engine, d Deps) {
	files := d.Files
	adminReanalysis := r.Group("/api/admin/reanalysis")
	{
		adminReanalysis.GET("/candidates", func(c *gin.Context) { listReanalysisCandidates(c, files) })
		adminReanalysis.POST("/run", func(c *gin.Context) { runReanalysis(c, files) })
		adminReanalysis.POST("/files", func(c *gin.Context) { reanalyzeFiles(c, files) })
	}
}

// RegisterWebDAVRoutes mounts the WebDAV data plane and its token management.
func RegisterWebDAVRoutes(r *gin.Engine, d Deps) {
	files, quota := d.Files, d.Quota
	prefix := d.Config.WebDAV.Prefix
	if prefix == "" {
		prefix = "/webdav"
	}
	srvWebDAV := func(c *gin.Context) {
		handleWebDAV(c, files, quota, d.Bus, d.Dispatcher, prefix)
	}
	r.Any(prefix+"/*path", srvWebDAV)
	// Gin's cleanPath strips trailing slashes, so /webdav/ routes to /webdav.
	// Rewrite the URL so the webdav handler always sees the trailing slash.
	r.Any(prefix, func(c *gin.Context) {
		c.Request.URL.Path = prefix + "/"
		handleWebDAV(c, files, quota, d.Bus, d.Dispatcher, prefix)
	})
	for _, method := range []string{"PROPFIND", "PROPPATCH", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK"} {
		r.Handle(method, prefix+"/*path", srvWebDAV)
		r.Handle(method, prefix, func(c *gin.Context) {
			c.Request.URL.Path = prefix + "/"
			handleWebDAV(c, files, quota, d.Bus, d.Dispatcher, prefix)
		})
	}

	t := r.Group("/api/webdav/tokens")
	{
		t.POST("", func(c *gin.Context) { createWebDAVToken(c, files) })
		t.GET("", func(c *gin.Context) { listWebDAVTokens(c, files) })
		t.DELETE("/:id", func(c *gin.Context) { deleteWebDAVToken(c, files) })
	}
}

// RegisterStorageNodeRoutes mounts the storage-node control surface (/_dfs).
func RegisterStorageNodeRoutes(r *gin.Engine, d Deps) {
	dfs := r.Group("/_dfs")
	{
		dfs.GET("/version", func(c *gin.Context) { StorageNodeVersion(c, d.Config.StorageNode) })
		dfs.GET("/identity", func(c *gin.Context) { StorageNodeIdentity(c, d.Config.StorageNode) })
		dfs.POST("/auth/validate", func(c *gin.Context) { StorageNodeAuthValidate(c, d.Config.StorageNode) })
	}
}
