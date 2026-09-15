package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdbimage"
)

type TMDBImageStore interface {
	Open(context.Context, string, string) (*tmdbimage.Asset, error)
}

func RegisterTMDBImages(r gin.IRoutes, images TMDBImageStore) {
	// img 标签不携带 Bearer 令牌；公开路由只能访问缓存层校验后的固定 TMDB 图片尺寸和文件名。
	handler := func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		asset, err := images.Open(ctx, c.Param("size"), c.Param("name"))
		if err != nil {
			switch {
			case errors.Is(err, tmdbimage.ErrInput):
				c.Status(http.StatusBadRequest)
			case errors.Is(err, tmdbimage.ErrNotFound):
				c.Status(http.StatusNotFound)
			case errors.Is(err, context.DeadlineExceeded):
				c.Status(http.StatusGatewayTimeout)
			default:
				c.Status(http.StatusBadGateway)
			}
			return
		}
		defer asset.File.Close()
		c.Header("Cache-Control", "public, max-age="+strconv.FormatInt(int64(tmdbimage.BrowserMaxAge()/time.Second), 10)+", stale-while-revalidate=604800")
		c.Header("Cross-Origin-Resource-Policy", "same-origin")
		c.Header("X-Content-Type-Options", "nosniff")
		if asset.ContentType != "" {
			c.Header("Content-Type", asset.ContentType)
		}
		if asset.ETag != "" {
			c.Header("ETag", asset.ETag)
			for _, candidate := range strings.Split(c.GetHeader("If-None-Match"), ",") {
				if strings.TrimSpace(candidate) == asset.ETag {
					c.Status(http.StatusNotModified)
					return
				}
			}
		}
		http.ServeContent(c.Writer, c.Request, c.Param("name"), asset.ModTime, asset.File)
	}
	r.GET("/api/tmdb/image/:size/:name", handler)
	r.HEAD("/api/tmdb/image/:size/:name", handler)
}
