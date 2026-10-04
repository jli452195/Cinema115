package httpapi

import (
	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func registerMemberDeletion(admin *gin.RouterGroup, s *service.MemberPlaybackService, fail func(*gin.Context, error)) {
	// Explicit guard also protects this destructive route if mounted separately.
	routes := admin.Group("/deletions", PortalAdminOnly())
	routes.GET("", func(c *gin.Context) {
		pending, jobs, err := s.DeletionDirectory(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"pending": pending, "jobs": jobs})
	})
	routes.POST("/preview", func(c *gin.Context) {
		var in struct {
			Key string `json:"key"`
		}
		if c.ShouldBindJSON(&in) != nil || in.Key == "" {
			c.JSON(400, gin.H{"error": "请选择删除目标"})
			return
		}
		preview, err := s.PreviewMemberDeletion(c.Request.Context(), in.Key)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, preview)
	})
	routes.POST("", func(c *gin.Context) {
		var in service.MemberDeleteInput
		if c.ShouldBindJSON(&in) != nil || in.Key == "" || in.Fingerprint == "" || in.ConfirmName == "" {
			c.JSON(400, gin.H{"error": "请先预览并输入确认名称"})
			return
		}
		job, err := s.DeleteMember(c.Request.Context(), in, c.GetString("admin_username"))
		if err != nil {
			fail(c, err)
			return
		}
		status := 202
		if job.Stage == "completed" {
			status = 200
		}
		c.JSON(status, gin.H{"job": job})
	})
}
