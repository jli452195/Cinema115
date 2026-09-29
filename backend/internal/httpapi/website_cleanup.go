package httpapi

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"strm115/backend/internal/service"
	"time"
)

func registerWebsiteCleanup(group *gin.RouterGroup, auth *service.WebsiteAuth) {
	available := func(c *gin.Context) bool {
		c.Header("Cache-Control", "no-store")
		if auth.Members == nil {
			c.JSON(503, gin.H{"error": "用户清理服务暂不可用"})
			return false
		}
		return true
	}
	group.GET("/cleanup-records", func(c *gin.Context) {
		if !available(c) {
			return
		}
		items, err := auth.Members.CleanupHistory(c.Request.Context())
		if err != nil {
			websiteAuthError(c, err)
			return
		}
		c.JSON(200, gin.H{"records": items})
	})
	group.GET("/:id/cleanup", func(c *gin.Context) {
		if !available(c) {
			return
		}
		p, err := auth.Members.CleanupPreview(c.Request.Context(), c.Param("id"))
		if err != nil {
			websiteAuthError(c, err)
			return
		}
		c.JSON(200, p)
	})
	group.POST("/:id/cleanup", func(c *gin.Context) {
		if !available(c) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var input struct {
			Fingerprint string `json:"fingerprint"`
			Username    string `json:"username"`
			Mode        string `json:"mode"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "删除确认格式无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
		defer cancel()
		if err := auth.Members.CleanupWebsiteUser(ctx, c.Param("id"), input.Fingerprint, input.Username, input.Mode); err != nil {
			websiteAuthError(c, err)
			return
		}
		c.JSON(200, gin.H{"message": "网站用户已删除，网盘文件未删除"})
	})
}
