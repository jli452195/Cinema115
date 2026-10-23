package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func RegisterGardenOrganize(admin *gin.RouterGroup, s *service.GardenOrganize) {
	r := admin.Group("/p115/garden-organize")
	r.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10)
		c.Next()
	})
	r.POST("/files", func(c *gin.Context) {
		var in service.GardenResourceInput
		if c.ShouldBindJSON(&in) != nil {
			c.JSON(400, gin.H{"error": "资源参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()
		out, err := s.Garden.Files(ctx, in)
		if err != nil {
			organizeError(c, err)
			return
		}
		c.JSON(200, out)
	})
	r.POST("/preview", func(c *gin.Context) {
		var in service.GardenOrganizeInput
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
}
