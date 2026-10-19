package httpapi

import (
	"strings"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/anibt"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/service"
)

func registerAnimeAnibt(r *gin.RouterGroup, s *service.AnimeAdminService) {
	ready := func(c *gin.Context) bool {
		if s.Bindings == nil || s.Bindings.Anibt == nil {
			c.JSON(503, gin.H{"code": "PROVIDER_NOT_READY", "error": "AniBT 来源暂不可用"})
			return false
		}
		return true
	}
	r.POST("/sources/anibt/search", func(c *gin.Context) {
		var in struct {
			Keyword   string  `json:"keyword"`
			BangumiID *string `json:"bangumiId"`
		}
		if !animeBody(c, &in, false) {
			return
		}
		in.Keyword = strings.TrimSpace(in.Keyword)
		if !anibt.ValidQuery(in.Keyword) || in.BangumiID != nil && !animefeed.ValidItem(*in.BangumiID) {
			animeError(c, animefeed.ErrInput)
			return
		}
		if !ready(c) {
			return
		}
		out, err := s.Bindings.Anibt.Catalog(c.Request.Context(), "", in.Keyword, false)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, gin.H{"items": out.Items, "truncated": out.Truncated, "fetchedAt": out.FetchedAt, "expiresAt": out.ExpiresAt})
	})
	r.GET("/sources/anibt/:id", func(c *gin.Context) {
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
		out, err := s.Bindings.Anibt.ReadAnimeSourceEntry(c.Request.Context(), budget, id, false)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, gin.H{"entry": out.Entry, "observedAt": out.ObservedAt.UnixMilli(), "expiresAt": out.ExpiresAt.UnixMilli(), "cached": out.Cached})
	})
}
