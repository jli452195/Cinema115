package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func RegisterInstantUploads(admin *gin.RouterGroup, s *service.InstantUploads, share *ShareAPI, validateUser func(context.Context, string) error) {
	group := admin.Group("/emby/instant-upload")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 20<<10)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		status := 500
		if errors.Is(err, service.ErrInstantConfig) {
			status = 400
		}
		if errors.Is(err, service.ErrInstantBusy) {
			status = 409
		}
		c.JSON(status, gin.H{"error": err.Error()})
	}
	checkUser := func(c *gin.Context, id string) bool {
		if id == "" || validateUser == nil {
			c.JSON(400, gin.H{"error": "请选择已绑定小号的目标用户"})
			return false
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		if err := validateUser(ctx, id); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return false
		}
		return true
	}
	group.GET("", func(c *gin.Context) { c.JSON(200, s.Status()) })
	group.POST("", func(c *gin.Context) {
		var config service.InstantUploadConfig
		if c.ShouldBindJSON(&config) != nil {
			c.JSON(400, gin.H{"error": "秒传配置格式无效"})
			return
		}
		if !checkUser(c, config.UserID) {
			return
		}
		if err := s.Apply(config); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, s.Status())
	})
	group.POST("/run", func(c *gin.Context) {
		var input struct {
			ID   string `json:"id"`
			STRM string `json:"strm"`
		}
		if c.ShouldBindJSON(&input) != nil || len(input.STRM) > 16384 {
			c.JSON(400, gin.H{"error": "请粘贴单个 STRM 文件中的播放地址"})
			return
		}
		config := s.Status().Config
		if !checkUser(c, config.UserID) {
			return
		}
		// Same URL checks as playback; this endpoint never reads local paths.
		claim, err := share.parseSTRMURL(strings.TrimSpace(strings.TrimPrefix(input.STRM, "\ufeff")))
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		result, err := s.Run(c.Request.Context(), config, input.ID, &p115.ShareInfo{Code: claim.Code, Password: claim.Password}, claim.FileID)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, result)
	})
}

func InstantPlaybackUserValidator(emby *service.EmbyService, proxy *service.EmbyProxy, cache *service.PlaybackCache) func(context.Context, string) error {
	return func(ctx context.Context, id string) error {
		url := proxy.Status().Config.URL
		if cache.Status().Config.EmbyURL != url {
			return fmt.Errorf("Emby 服务已变化，请重新配置用户绑定")
		}
		users, err := emby.PlaybackUsers(ctx, url)
		if err != nil {
			return err
		}
		for _, user := range users {
			if user.ID == id && !user.Policy.IsDisabled {
				return nil
			}
		}
		return fmt.Errorf("目标用户不存在或已停用")
	}
}
