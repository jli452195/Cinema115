package httpapi

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
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
	group.GET("/users", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		defer cancel()
		users, err := emby.PlaybackUsers(ctx, proxy.Status().Config.URL)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"users": users, "embyUrl": proxy.Status().Config.URL})
	})
	group.POST("", func(c *gin.Context) {
		var config service.PlaybackCacheConfig
		if c.ShouldBindJSON(&config) != nil {
			c.JSON(400, gin.H{"error": "播放缓存配置无效"})
			return
		}
		if config.Enabled {
			proxyURL := proxy.Status().Config.URL
			if config.EmbyURL != proxyURL {
				c.JSON(400, gin.H{"error": "Emby 服务已变化，请刷新用户列表"})
				return
			}
			users, err := emby.PlaybackUsers(c.Request.Context(), proxyURL)
			if err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
			for i, b := range config.Bindings {
				found := false
				for _, u := range users {
					if u.ID == b.UserID && !u.Policy.IsDisabled {
						found = true
						config.Bindings[i].UserName = u.Name
						break
					}
				}
				if !found {
					c.JSON(400, gin.H{"error": "绑定的 Emby 用户不存在或已停用"})
					return
				}
			}
		}
		if err := s.Apply(config); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, s.Status())
	})
	group.POST("/cleanup", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 80*time.Second)
		defer cancel()
		if err := s.Cleanup(ctx); err != nil {
			c.JSON(400, gin.H{"error": "清理未完成，请查看 Emby 302 日志"})
			return
		}
		c.JSON(200, s.Status())
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
