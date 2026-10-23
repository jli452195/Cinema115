package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func RegisterManualOrganize(admin *gin.RouterGroup, organizer *service.ManualOrganize) {
	group := admin.Group("/p115/manual-organize")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		limit := int64(512 << 10)
		if c.Request.Method == http.MethodPost && c.FullPath() == "/api/admin/p115/manual-organize/subtitles/import" {
			limit = service.MaxSubtitleImportBytes + (1 << 20)
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	})
	fail := organizeError
	registerOrganizeSubtitles(group, organizer, fail)
	group.GET("/config", func(c *gin.Context) { c.JSON(200, organizer.Config()) })
	group.PUT("/config", func(c *gin.Context) {
		var input service.OrganizeConfig
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "整理配置无效"})
			return
		}
		if err := organizer.SaveConfig(input); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, organizer.Config())
	})
	group.GET("/files", func(c *gin.Context) {
		offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if err != nil {
			fail(c, service.ErrOrganizeInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
		defer cancel()
		page, err := organizer.Browse(ctx, c.Query("accountId"), c.Query("cid"), offset)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, page)
	})
	group.POST("/scan", func(c *gin.Context) {
		var input service.OrganizeScanInput
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "扫描参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
		defer cancel()
		result, err := organizer.Scan(ctx, input)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, result)
	})
	group.POST("/preview", func(c *gin.Context) {
		var input service.OrganizePreviewInput
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "预览参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
		defer cancel()
		result, err := organizer.Preview(ctx, c.Query("accountId"), input)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, result)
	})
	group.POST("/jobs", func(c *gin.Context) {
		var input struct {
			AccountID string `json:"accountId"`
			PreviewID string `json:"previewId"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "整理任务参数无效"})
			return
		}
		result, err := organizer.Start(input.AccountID, input.PreviewID)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusAccepted, result)
	})
	group.GET("/jobs", func(c *gin.Context) { c.JSON(200, gin.H{"jobs": organizer.History()}) })
	group.GET("/cleanup", func(c *gin.Context) { c.JSON(200, gin.H{"jobs": organizer.CleanupHistory()}) })
	group.POST("/cleanup/preview", func(c *gin.Context) {
		var input service.OrganizeCleanupInput
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "清理参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
		defer cancel()
		result, err := organizer.PreviewCleanup(ctx, input)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, result)
	})
	group.POST("/cleanup", func(c *gin.Context) {
		var input struct {
			AccountID string `json:"accountId"`
			PreviewID string `json:"previewId"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "清理确认参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
		defer cancel()
		result, err := organizer.ExecuteCleanup(ctx, input.AccountID, input.PreviewID)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, result)
	})
}

func organizeError(c *gin.Context, err error) {
	status := http.StatusBadGateway
	if errors.Is(err, service.ErrOrganizeInput) || errors.Is(err, service.ErrGenerationInput) {
		status = http.StatusBadRequest
	}
	if errors.Is(err, service.ErrOrganizeConflict) || errors.Is(err, service.ErrAccountMissing) {
		status = http.StatusConflict
	}
	if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
