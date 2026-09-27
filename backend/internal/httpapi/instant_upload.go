package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func RegisterInstantUploads(admin *gin.RouterGroup, s *service.InstantUploads, share *ShareAPI) {
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
	group.GET("", func(c *gin.Context) { c.JSON(200, s.Status()) })
	group.POST("", func(c *gin.Context) {
		var config service.InstantUploadConfig
		if c.ShouldBindJSON(&config) != nil {
			c.JSON(400, gin.H{"error": "秒传配置格式无效"})
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
