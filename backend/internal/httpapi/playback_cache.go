package httpapi

import (
	"context"
	"errors"
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
		c.JSON(200, gin.H{"users": users, "embyUrl": proxyURL})
	})
	group.POST("/bindings/:userID", func(c *gin.Context) {
		var in struct {
			EmbyURL           string  `json:"embyUrl"`
			Mode              string  `json:"mode"`
			AccountID         string  `json:"accountId"`
			ExpectedAccountID *string `json:"expectedAccountId"`
		}
		if c.ShouldBindJSON(&in) != nil || in.ExpectedAccountID == nil || (in.Mode != "unbound" && in.Mode != "personal") || (in.Mode == "unbound" && in.AccountID != "") {
			c.JSON(400, gin.H{"error": "用户播放配置无效"})
			return
		}
		binding := service.PlaybackBinding{UserID: c.Param("userID"), AccountID: in.AccountID}
		if in.Mode == "personal" {
			proxyURL := proxy.Status().Config.URL
			if in.EmbyURL != proxyURL {
				c.JSON(400, gin.H{"error": "Emby 服务已变化，请刷新用户列表"})
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
			defer cancel()
			users, err := emby.PlaybackUsers(ctx, proxyURL)
			if err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
			found := false
			for _, user := range users {
				if user.ID == binding.UserID && !user.Policy.IsDisabled {
					binding.UserName, found = user.Name, true
					break
				}
			}
			if !found {
				c.JSON(400, gin.H{"error": "绑定的 Emby 用户不存在或已停用"})
				return
			}
		}
		if err := s.UpdateBinding(in.EmbyURL, binding, *in.ExpectedAccountID, in.Mode == "unbound"); err != nil {
			code := 400
			if errors.Is(err, service.ErrPlaybackBindingChanged) {
				code = 409
			}
			c.JSON(code, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, s.Status())
	})
	group.POST("/bindings", func(c *gin.Context) {
		var config service.PlaybackCacheConfig
		if c.ShouldBindJSON(&config) != nil {
			c.JSON(400, gin.H{"error": "播放缓存配置无效"})
			return
		}
		if len(config.Bindings) > 0 {
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
		if err := s.ApplyBindings(config.EmbyURL, config.Bindings); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, s.Status())
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
