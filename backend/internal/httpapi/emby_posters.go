package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/imagecache"
	"strm115/backend/internal/service"
)

func registerEmbyPosters(group *gin.RouterGroup, s *service.EmbyService) {
	handler := func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		c.Header("Vary", "Authorization")
		asset, err := s.OpenPoster(ctx, c.Param("scope"), c.Param("id"))
		if err != nil {
			switch {
			case errors.Is(err, imagecache.ErrInput):
				c.Status(http.StatusBadRequest)
			case errors.Is(err, imagecache.ErrNotFound):
				c.Header("Cache-Control", "private, max-age=3600")
				c.Status(http.StatusNotFound)
			case errors.Is(err, context.DeadlineExceeded):
				c.Status(http.StatusGatewayTimeout)
			default:
				c.Status(http.StatusBadGateway)
			}
			return
		}
		defer asset.File.Close()
		c.Header("Cache-Control", "private, max-age=86400")
		c.Header("Cross-Origin-Resource-Policy", "same-origin")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Type", asset.ContentType)
		c.Header("ETag", asset.ETag)
		http.ServeContent(c.Writer, c.Request, "poster.webp", asset.ModTime, asset.File)
	}
	group.GET("/posters/:scope/:id", handler)
	group.HEAD("/posters/:scope/:id", handler)
}
