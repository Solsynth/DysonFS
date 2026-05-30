package handler

import (
	"net/http"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/version"

	"github.com/gin-gonic/gin"
)

func StorageNodeVersion(c *gin.Context, cfg config.StorageNodeConfig) {
	c.JSON(http.StatusOK, gin.H{
		"version":     version.Version,
		"git_commit":  version.GitCommit,
		"api_version": version.APIVersion,
		"machine_id":  cfg.MachineID,
	})
}

func StorageNodeIdentity(c *gin.Context, cfg config.StorageNodeConfig) {
	c.JSON(http.StatusOK, gin.H{
		"machine_id": cfg.MachineID,
		"node_type":  "storage",
		"version":    version.Version,
	})
}

func StorageNodeAuthValidate(c *gin.Context, cfg config.StorageNodeConfig) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if cfg.AuthToken == "" || req.Token != cfg.AuthToken {
		c.JSON(http.StatusUnauthorized, gin.H{
			"valid":      false,
			"machine_id": cfg.MachineID,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"valid":      true,
		"machine_id": cfg.MachineID,
	})
}

func StorageNodeAuthMiddleware(authToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if authToken == "" {
			c.Next()
			return
		}
		token := c.GetHeader("Authorization")
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}
		if token == "" {
			token = c.Query("token")
		}
		if token != authToken {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid auth token"})
			return
		}
		c.Next()
	}
}
