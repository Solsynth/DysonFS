package handler

import (
	"net/http"
	"time"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func RegisterRoutes(r *gin.Engine, cfg *config.Config, files *service.FileService, tasks *service.TaskService, bus *eventbus.Bus) {
	_ = bus
	f := r.Group("/api/files")
	{
		f.GET(":id/info", func(c *gin.Context) { fileInfo(c, files) })
		f.GET(":id/references", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"items": []any{}}) })
		f.GET(":id/e2ee", func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"code": "file.e2ee_not_found"}) })
		f.GET("root/children", func(c *gin.Context) { listRoot(c, files) })
		f.GET(":parentId/children", func(c *gin.Context) { listChildren(c, files) })
		f.POST("folders", func(c *gin.Context) { createFolder(c, files) })
		f.GET("me", func(c *gin.Context) { listRoot(c, files) })
	}

	u := r.Group("/api/files/upload")
	{
		u.POST("create", func(c *gin.Context) { c.JSON(http.StatusNotImplemented, gin.H{"error": "upload create not yet wired"}) })
		u.POST("direct", func(c *gin.Context) { c.JSON(http.StatusNotImplemented, gin.H{"error": "direct upload not yet wired"}) })
		u.POST("chunk/:taskId/:idx", func(c *gin.Context) { c.JSON(http.StatusNotImplemented, gin.H{"error": "chunk upload not yet wired"}) })
		u.POST("complete/:taskId", func(c *gin.Context) {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "complete upload not yet wired"})
		})
		u.GET("tasks", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"items": []any{}}) })
		u.GET("progress/:taskId", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"progress": 0}) })
		u.GET("resume/:taskId", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"resume": true}) })
		u.DELETE("task/:taskId", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		u.GET("stats", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"total_tasks": 0}) })
		u.DELETE("tasks/cleanup", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"count": 0}) })
		u.GET("tasks/recent", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"items": []any{}}) })
		u.GET("tasks/:taskId/details", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"task": nil}) })
	}

	p := r.Group("/api/pools")
	{
		p.GET("", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"items": []any{}}) })
		p.DELETE(":id/recycle", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"count": 0}) })
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
	c.JSON(http.StatusOK, file)
}

func listChildren(c *gin.Context, files *service.FileService) {
	items, err := files.GetChildren(c.Param("parentId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Total", "0")
	c.JSON(http.StatusOK, items)
}

func listRoot(c *gin.Context, files *service.FileService) {
	_ = files
	if _, err := uuid.Parse(c.GetHeader("X-Account-ID")); err != nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	c.JSON(http.StatusOK, []any{})
}

func createFolder(c *gin.Context, files *service.FileService) {
	_ = time.Second
	_ = files
	c.JSON(http.StatusNotImplemented, gin.H{"error": "create folder not yet wired"})
}
