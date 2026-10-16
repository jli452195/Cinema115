package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

func RegisterEmby(r *gin.Engine, admin *gin.RouterGroup, s *service.EmbyService) {
	group := admin.Group("/emby")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	group.GET("/config", func(c *gin.Context) { c.JSON(200, s.Config()) })
	registerEmbyPosters(group, s)
	registerEmbyMissingExclusions(group, s)
	group.POST("/webhook-config", func(c *gin.Context) {
		var input struct {
			BaseURL string `json:"baseUrl"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "地址格式无效"})
			return
		}
		if err := s.SaveWebhookBaseURL(c.Request.Context(), input.BaseURL); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, s.Config())
	})
	group.POST("/test", func(c *gin.Context) {
		var input service.EmbyConfig
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "配置格式无效"})
			return
		}
		server, libs, err := s.Test(c.Request.Context(), input)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"server": server, "libraries": libs})
	})
	group.POST("/config", func(c *gin.Context) {
		var input service.EmbyConfig
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "配置格式无效"})
			return
		}
		if err := s.Save(c.Request.Context(), input); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, s.Config())
	})
	group.GET("/status", func(c *gin.Context) {
		status, err := s.Status(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, status)
	})
	group.GET("/missing", func(c *gin.Context) {
		offset := 0
		if raw, exists := c.GetQuery("offset"); exists {
			var err error
			offset, err = strconv.Atoi(raw)
			if err != nil || offset < 0 || offset > 1_000_000 {
				c.JSON(400, gin.H{"error": "分页位置无效"})
				return
			}
		}
		page, err := s.MissingEpisodes(c.Request.Context(), service.EmbyMissingQuery{Search: c.Query("q"), Offset: offset})
		if err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, service.ErrEmbyMissingInput) {
				code = http.StatusBadRequest
			}
			c.JSON(code, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, page)
	})
	group.POST("/sync", func(c *gin.Context) {
		if err := s.StartSync(c.Request.Context()); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(202, gin.H{"message": "全量同步已加入后台队列"})
	})
	group.POST("/library-check", func(c *gin.Context) {
		var input struct {
			Items []model.EmbyMatch `json:"items"`
		}
		if c.ShouldBindJSON(&input) != nil || len(input.Items) > 100 {
			c.JSON(400, gin.H{"error": "每次最多查询 100 个 TMDB ID"})
			return
		}
		for _, v := range input.Items {
			if !validEmbyMedia(v.Kind, v.TMDB) {
				c.JSON(400, gin.H{"error": "媒体类型或 TMDB ID 无效"})
				return
			}
		}
		results, err := s.Matches(c.Request.Context(), input.Items, false)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"items": results})
	})
	group.GET("/media/:kind/:id", func(c *gin.Context) {
		kind, id := c.Param("kind"), c.Param("id")
		if !validEmbyMedia(kind, id) {
			c.JSON(400, gin.H{"error": "媒体类型或 TMDB ID 无效"})
			return
		}
		results, err := s.Matches(c.Request.Context(), []model.EmbyMatch{{Kind: kind, TMDB: id}}, true)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"media": results[0], "baseUrl": s.Config().URL, "serverId": s.Config().ServerID})
	})
	group.GET("/versions/:kind/:id", func(c *gin.Context) {
		kind, id := c.Param("kind"), c.Param("id")
		if !validEmbyMedia(kind, id) {
			c.JSON(400, gin.H{"error": "媒体类型或 TMDB ID 无效"})
			return
		}
		var query service.EmbyVersionQuery
		if value, exists := c.GetQuery("offset"); exists {
			offset, err := strconv.Atoi(value)
			if err != nil || offset < 0 {
				c.JSON(400, gin.H{"error": "分页位置无效"})
				return
			}
			query.Offset = offset
		}
		if kind == "tv" {
			season, err1 := strconv.Atoi(c.Query("season"))
			episode, err2 := strconv.Atoi(c.Query("episode"))
			if err1 != nil || err2 != nil || season < 0 || episode < 0 {
				c.JSON(400, gin.H{"error": "请选择需要查看的季和集"})
				return
			}
			query.Season = &season
			query.Episode = &episode
		}
		versions, err := s.Versions(c.Request.Context(), kind, id, query)
		if err != nil {
			c.JSON(502, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, versions)
	})
	r.POST("/api/webhook/emby", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
		data, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(413, gin.H{"error": "Webhook 请求体过大或读取失败"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		status, err := s.Webhook(ctx, c.Query("token"), data)
		if err != nil {
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(status, gin.H{"accepted": true})
	})
}
func validEmbyMedia(kind, id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return (kind == "movie" || kind == "tv") && err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}
