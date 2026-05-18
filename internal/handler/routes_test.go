package handler

import (
	"testing"

	"github.com/gin-gonic/gin"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/eventbus"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

func TestRegisterRoutesNoPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	files := service.NewFileService(&database.DB{}, nil)
	tasks := service.NewTaskService(&database.DB{})
	quota := service.NewQuotaService(&database.DB{})

	defer func() {
		if recover() != nil {
			t.Fatal("RegisterRoutes() panicked")
		}
	}()

	RegisterRoutes(r, &config.Config{}, files, tasks, quota, (*eventbus.Bus)(nil), nil)
}
