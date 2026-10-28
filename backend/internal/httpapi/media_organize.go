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
		c.JSON(status, gin.H{"error": err.Error()})
	}
	g.GET("", func(c *gin.Context) {
		page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
		if err != nil || page < 1 {
			fail(c, service.ErrMediaOrganizeInput)
			return
		}
		c.JSON(200, s.State(page))
	})
	g.POST("/rules", func(c *gin.Context) {
		var in service.MediaOrganizeRule
		if !bindNamingJSON(c, &in) {
			return
		}
		out, err := s.SaveRule(in)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, out)
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
		c.JSON(202, out)
	})
	g.GET("/runs/:id/task", func(c *gin.Context) {
		out, err := s.Task(c.Param("id"), c.Query("fileId"))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, out)
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
		c.JSON(202, out)
	})
}
