package httpapi

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/service"
)

func registerAnimeGarden(r *gin.RouterGroup, s *service.AnimeAdminService) {
	r = r.Group("")
	r.Use(operationLogs(s.Logs, "搜索", "Garden", map[string]string{r.BasePath() + "/resources/garden/search": "资源搜索"}))
	ready := func(c *gin.Context) bool {
		if s.Bindings == nil || s.Bindings.Garden == nil {
			c.JSON(503, gin.H{"code": "PROVIDER_NOT_READY", "error": "Anime Garden 来源暂不可用"})
			return false
		}
		return true
	}
	r.POST("/resources/garden/search", func(c *gin.Context) {
		var in service.GardenSearchInput
		if !animeBody(c, &in, false) || !ready(c) {
			return
		}
		out, err := s.Bindings.Garden.SearchResources(c.Request.Context(), in)
		if err != nil {
			animeError(c, err)
			return
		}
		c.Set("operationLogSummary", fmt.Sprintf("第 %d 页，找到 %d 条资源", out.Page, len(out.Items)))
		if out.Cached {
			c.Set("operationLogSummary", fmt.Sprintf("命中缓存，第 %d 页，返回 %d 条资源", out.Page, len(out.Items)))
			c.Set("operationLogCached", true)
		}
		c.JSON(200, out)
	})
	r.POST("/sources/garden/search", func(c *gin.Context) {
		var in struct {
			Keyword   string  `json:"keyword"`
			BangumiID *string `json:"bangumiId"`
		}
		if !animeBody(c, &in, false) {
			return
		}
		in.Keyword = strings.TrimSpace(in.Keyword)
		if !garden.ValidQuery(in.Keyword) || in.BangumiID != nil && !animefeed.ValidItem(*in.BangumiID) {
			animeError(c, animefeed.ErrInput)
			return
		}
		if !ready(c) {
			return
		}
		out, err := s.Bindings.Garden.Catalog(c.Request.Context(), "", in.Keyword, false)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, gin.H{"items": out.Items, "truncated": out.Truncated, "fetchedAt": out.FetchedAt, "expiresAt": out.ExpiresAt})
	})
	r.GET("/sources/garden/:id", func(c *gin.Context) {
		id := c.Param("id")
		if !animefeed.ValidItem(id) {
			animeError(c, animefeed.ErrInput)
			return
		}
		if !ready(c) {
			return
		}
		budget, err := service.NewAnimeSourceBudget(nil)
		if err != nil {
			animeError(c, err)
			return
		}
		out, err := s.Bindings.Garden.ReadAnimeSourceEntry(c.Request.Context(), budget, id, false)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, gin.H{"entry": out.Entry, "observedAt": out.ObservedAt.UnixMilli(), "expiresAt": out.ExpiresAt.UnixMilli(), "cached": out.Cached})
	})
}
