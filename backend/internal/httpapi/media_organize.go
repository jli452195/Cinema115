package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func RegisterMediaOrganize(admin *gin.RouterGroup, s *service.MediaOrganizer) {
	g := admin.Group("/media-organize")
	g.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrMediaOrganizeBusy) {
			status = http.StatusConflict
		}
		if errors.Is(err, service.ErrClassificationUnavailable) {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{"error": err.Error()})
	}
	g.GET("", func(c *gin.Context) {
		c.JSON(200, s.State())
	})
	g.GET("/directories", func(c *gin.Context) {
		out, err := s.LocalDirectories(c.Request.Context(), c.Query("path"))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/history", func(c *gin.Context) {
		page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
		if err != nil || page < 1 {
			fail(c, service.ErrMediaOrganizeInput)
			return
		}
		out, err := s.History(c.Request.Context(), c.Query("q"), c.DefaultQuery("status", "all"), page)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, out)
	})
	g.DELETE("/history", func(c *gin.Context) {
		var in service.MediaOrganizeDelete
		if !bindNamingJSON(c, &in) {
			return
		}
		count, err := s.DeleteHistory(c.Request.Context(), in)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"deleted": count})
	})
	g.POST("/rules", func(c *gin.Context) {
		type ruleFields service.MediaOrganizeRule
		var in struct {
			ruleFields
			AttachmentCleanupEnabled *bool `json:"attachmentCleanupEnabled"`
			AttachmentGraceSeconds   *int  `json:"attachmentGraceSeconds"`
		}
		if !bindNamingJSON(c, &in) {
			return
		}
		rule := service.MediaOrganizeRule(in.ruleFields)
		rule.AttachmentCleanupEnabled = true
		if in.AttachmentCleanupEnabled != nil {
			rule.AttachmentCleanupEnabled = *in.AttachmentCleanupEnabled
		}
		rule.AttachmentGraceSeconds = 10
		if in.AttachmentGraceSeconds != nil {
			rule.AttachmentGraceSeconds = *in.AttachmentGraceSeconds
		}
		out, err := s.SaveRuleContext(c.Request.Context(), rule)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, out)
	})
	g.PATCH("/rules/:id/schedule", func(c *gin.Context) {
		var in struct {
			RealtimeEnabled *bool `json:"realtimeEnabled"`
			ScheduleEnabled *bool `json:"scheduleEnabled"`
			IntervalSeconds *int  `json:"intervalSeconds"`
		}
		if !bindNamingJSON(c, &in) {
			return
		}
		if in.RealtimeEnabled == nil || in.ScheduleEnabled == nil || in.IntervalSeconds == nil {
			fail(c, service.ErrMediaOrganizeInput)
			return
		}
		out, err := s.UpdateSchedule(c.Request.Context(), c.Param("id"), service.MediaOrganizeSchedule{
			RealtimeEnabled: *in.RealtimeEnabled,
			ScheduleEnabled: *in.ScheduleEnabled,
			IntervalSeconds: *in.IntervalSeconds,
		})
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.DELETE("/rules/:id", func(c *gin.Context) {
		if err := s.DeleteRule(c.Param("id")); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})
	g.POST("/rules/:id/run", func(c *gin.Context) {
		out, err := s.Start(c.Param("id"))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(202, gin.H{"id": out.ID, "status": out.Status})
	})
	g.POST("/rules/:id/stop", func(c *gin.Context) {
		rule, err := s.Stop(c.Request.Context(), c.Param("id"))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"rule": rule})
	})
	g.GET("/runs/:id/task", func(c *gin.Context) {
		out, err := s.Task(c.Param("id"), c.Query("fileId"))
		if err != nil {
			if errors.Is(err, service.ErrMediaOrganizeTaskNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "整理任务记录已移除"})
				return
			}
			fail(c, err)
			return
		}
		c.JSON(200, mediaOrganizeTaskOutput(out))
	})
	g.POST("/runs/:id/retry", func(c *gin.Context) {
		var in service.MediaOrganizeCorrection
		if !bindNamingJSON(c, &in) {
			return
		}
		out, err := s.Retry(c.Param("id"), in)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(202, mediaOrganizeTaskOutput(out))
	})
}
