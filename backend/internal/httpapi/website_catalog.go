package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

// 普通用户只读取入库状态及集号，不暴露 Emby 地址、媒体路径和服务器条目编号。
func catalogMatches(matches []model.EmbyMatch) []model.EmbyMatch {
	for i := range matches {
		for j, item := range matches[i].Items {
			matches[i].Items[j] = model.EmbyItem{ID: strconv.Itoa(j), Type: item.Type, Season: item.Season, Episode: item.Episode, EndEpisode: item.EndEpisode}
		}
	}
	return matches
}
func RegisterWebsiteCatalog(group *gin.RouterGroup, emby *service.EmbyService) {
	group = group.Group("/emby")
	group.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	group.POST("/library-check", func(c *gin.Context) {
		var input struct {
			Items []model.EmbyMatch `json:"items"`
		}
		if c.ShouldBindJSON(&input) != nil || len(input.Items) > 100 {
			c.JSON(400, gin.H{"error": "每次最多查询 100 个影视编号"})
			return
		}
		for _, item := range input.Items {
			if !validEmbyMedia(item.Kind, item.TMDB) {
				c.JSON(400, gin.H{"error": "影视编号无效"})
				return
			}
		}
		matches, err := emby.Matches(c.Request.Context(), input.Items, false)
		if err != nil {
			c.JSON(500, gin.H{"error": "入库状态暂时不可用"})
			return
		}
		c.JSON(200, gin.H{"items": catalogMatches(matches)})
	})
	group.GET("/media/:kind/:id", func(c *gin.Context) {
		kind, id := c.Param("kind"), c.Param("id")
		if !validEmbyMedia(kind, id) {
			c.JSON(400, gin.H{"error": "影视编号无效"})
			return
		}
		matches, err := emby.Matches(c.Request.Context(), []model.EmbyMatch{{Kind: kind, TMDB: id}}, true)
		if err != nil {
			c.JSON(500, gin.H{"error": "入库状态暂时不可用"})
			return
		}
		c.JSON(200, gin.H{"media": catalogMatches(matches)[0], "baseUrl": "", "serverId": ""})
	})
}
