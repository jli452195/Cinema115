package httpapi

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"time"
)

func RegisterPlaybackCache(admin *gin.RouterGroup, s *service.PlaybackCache, proxy *service.EmbyProxy, emby *service.EmbyService) {
	group := admin.Group("/emby/playback-cache")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		c.Next()
	})
	group.GET("", func(c *gin.Context) { c.JSON(200, s.Status()) })
	group.POST("/forget-material", func(c *gin.Context) {
		var input struct {
			ID string `json:"id"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "秒传资料参数无效"})
			return
		}
		if err := s.ForgetMaterial(c.Request.Context(), input.ID); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, s.Status())
	})
	group.POST("/forget-directory", func(c *gin.Context) {
		var input struct {
			Key string `json:"key"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "目录记录参数无效"})
			return
		}
		if err := s.ForgetDirectory(c.Request.Context(), input.Key); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, s.Status())
	})
	group.GET("/users/:userID/avatar", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		data, kind, err := emby.PlaybackUserImage(ctx, proxy.Status().Config.URL, c.Param("userID"))
		if err != nil {
			c.Status(404)
			return
		}
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(200, kind, data)
	})
	group.GET("/users", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		defer cancel()
		proxyURL := proxy.Status().Config.URL
		users, err := emby.PlaybackUsers(ctx, proxyURL)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		members := make([]model.MemberPlayback, 0)
		if s.Members != nil {
			for _, member := range s.Members.Records() {
				if member.EmbyURL == proxyURL {
					members = append(members, member)
				}
			}
		}
		c.JSON(200, gin.H{"users": users, "embyUrl": proxyURL, "members": members})
	})
	// Retired endpoints cannot re-enable cloud deletion, even for older clients.
	group.POST("/policy", func(c *gin.Context) {
		c.JSON(http.StatusGone, gin.H{"error": "网盘文件自动清理已停用，请在 115 中手动删除"})
	})
	group.POST("/cleanup", func(c *gin.Context) {
		c.JSON(http.StatusGone, gin.H{"error": "网盘文件清理已停用，请在 115 中手动删除"})
	})
	group.POST("/forget", func(c *gin.Context) {
		var in struct {
			Key string `json:"key"`
		}
		if c.ShouldBindJSON(&in) != nil {
			c.JSON(400, gin.H{"error": "缓存编号无效"})
			return
		}
		if err := s.Forget(c.Request.Context(), in.Key); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, s.Status())
	})
}
