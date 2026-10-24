package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func RegisterDownloadOrganize(admin *gin.RouterGroup, s *service.DownloadOrganize) {
	r := admin.Group("/p115/download-organize")
	r.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10)
		c.Next()
	})
	r.GET("/config", func(c *gin.Context) { c.JSON(200, s.Target()) })
	r.GET("/jobs", func(c *gin.Context) { c.JSON(200, gin.H{"jobs": s.History()}) })
	r.POST("/files", func(c *gin.Context) {
		var in service.DownloadResourceInput
		if c.ShouldBindJSON(&in) != nil {
			c.JSON(400, gin.H{"error": "资源参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()
		out, err := s.Resources.Files(ctx, in)
		if err != nil {
			organizeError(c, err)
			return
		}
		c.JSON(200, out)
	})
	r.POST("/preview", func(c *gin.Context) {
		var in service.DownloadOrganizeInput
		if c.ShouldBindJSON(&in) != nil {
			c.JSON(400, gin.H{"error": "整理映射无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
		defer cancel()
		out, err := s.Preview(ctx, in)
		if err != nil {
			organizeError(c, err)
			return
		}
		c.JSON(200, out)
	})
	r.POST("/jobs", func(c *gin.Context) {
		var in struct {
			PreviewID string `json:"previewId"`
		}
		if c.ShouldBindJSON(&in) != nil {
			c.JSON(400, gin.H{"error": "任务参数无效"})
			return
		}
		out, err := s.Start(in.PreviewID)
		if err != nil {
			organizeError(c, err)
			return
		}
		c.JSON(202, out)
	})
	r.POST("/resume", func(c *gin.Context) {
		var in struct {
			JobID string `json:"jobId"`
		}
		if c.ShouldBindJSON(&in) != nil || in.JobID == "" {
			c.JSON(400, gin.H{"error": "任务参数无效"})
			return
		}
		out, err := s.Resume(in.JobID)
		if err != nil {
			organizeError(c, err)
			return
		}
		c.JSON(202, out)
	})
}
