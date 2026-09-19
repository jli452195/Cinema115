package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/imagecache"
)

type TMDBImageStore interface {
	Open(context.Context, string, string) (*imagecache.Asset, error)
}

func RegisterTMDBImages(r gin.IRoutes, images TMDBImageStore) {
	registerImages(r, "/api/tmdb/image/:size/:name", images)
}

func RegisterMikanImages(r gin.IRoutes, images TMDBImageStore) {
	// size 在蜜柑路由中表示年月目录，由缓存层校验；不接收任意图片 URL。
	registerImages(r, "/api/mikan/image/:size/:name", images)
}

func RegisterGyingImages(r gin.IRoutes, images TMDBImageStore) {
	// size 表示观影类型，name 表示条目编号及扩展名；只允许固定图片源。
	registerImages(r, "/api/gying/image/:size/:name", images)
}

func registerImages(r gin.IRoutes, route string, images TMDBImageStore) {
	// img 标签不携带 Bearer 令牌；仅公开固定来源的图片，不开放搜索或资源接口。
	handler := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		asset, err := images.Open(ctx, c.Param("size"), c.Param("name"))
		if err != nil {
			switch {
			case errors.Is(err, imagecache.ErrInput):
				c.Status(http.StatusBadRequest)
			case errors.Is(err, imagecache.ErrNotFound):
				c.Status(http.StatusNotFound)
			case errors.Is(err, context.DeadlineExceeded):
				c.Status(http.StatusGatewayTimeout)
			default:
				c.Status(http.StatusBadGateway)
			}
			return
		}
		defer asset.File.Close()
		c.Header("Cache-Control", "public, max-age="+strconv.FormatInt(int64(imagecache.BrowserMaxAge()/time.Second), 10)+", stale-while-revalidate=604800")
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
	r.GET(route, handler)
	r.HEAD(route, handler)
}
