package httpapi

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/subshare"
)

func RegisterSubtitleLibrary(admin *gin.RouterGroup, library *subshare.Client) {
	r := admin.Group("/subtitles")
	r.GET("/search", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		queries := c.QueryArray("q")
		if len(queries) == 0 {
			c.JSON(400, gin.H{"error": "请输入搜索词"})
			return
		}
		out, err := library.Search(ctx, queries)
		if err != nil {
			c.JSON(502, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"versions": out})
	})
	r.GET("/versions/:id/files", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		out, err := library.Files(ctx, c.Param("id"))
		if err != nil {
			c.JSON(502, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"files": out})
	})
}
