package httpapi

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/assrt"
	"strm115/backend/internal/service"
)

func RegisterAssrt(admin *gin.RouterGroup, s *service.AssrtService) {
	r := admin.Group("/subtitles")
	r.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		status, message := http.StatusBadGateway, "射手网暂时无法访问，请稍后重试"
		switch {
		case errors.Is(err, assrt.ErrInput):
			status, message = 400, err.Error()
		case errors.Is(err, assrt.ErrToken), errors.Is(err, assrt.ErrNotConfigured):
			status, message = 409, err.Error()
		case errors.Is(err, assrt.ErrFile):
			status, message = 422, err.Error()
		case errors.Is(err, assrt.ErrRateLimited):
			status, message = 429, err.Error()
			c.Header("Retry-After", "60")
		case errors.Is(err, context.DeadlineExceeded):
			status, message = 504, "射手网请求超时，请稍后重试"
		}
		c.JSON(status, gin.H{"error": message})
	}
	view := func(c *gin.Context) { c.JSON(200, gin.H{"assrt": s.View()}) }
	r.GET("/config", view)
	r.POST("/assrt/config", func(c *gin.Context) {
		var in struct {
			Token string `json:"token"`
		}
		if c.ShouldBindJSON(&in) != nil {
			c.JSON(400, gin.H{"error": "请输入射手网 API Token"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		if err := s.Configure(ctx, in.Token); err != nil {
			fail(c, err)
			return
		}
		view(c)
	})
	r.DELETE("/assrt/config", func(c *gin.Context) {
		if err := s.Clear(c.Request.Context()); err != nil {
			fail(c, err)
			return
		}
		view(c)
	})
	r.GET("/assrt/search", func(c *gin.Context) {
		position := 0
		if raw := c.Query("pos"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 || n > 1500 {
				fail(c, assrt.ErrInput)
				return
			}
			position = n
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		result, err := s.Search(ctx, c.Query("q"), position)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, result)
	})
	r.GET("/assrt/:id/download", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			fail(c, assrt.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 65*time.Second)
		defer cancel()
		file, err := s.Download(ctx, id)
		if err != nil {
			fail(c, err)
			return
		}
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Subtitle-Filename", url.PathEscape(file.Name))
		c.Header("Content-Length", strconv.Itoa(len(file.Data)))
		c.Data(200, "application/octet-stream", file.Data)
	})
}
