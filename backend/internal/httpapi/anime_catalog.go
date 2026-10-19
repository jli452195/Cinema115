package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func registerAnimeCatalog(r *gin.RouterGroup, s *service.AnimeAdminService) {
	r.GET("/providers", func(c *gin.Context) { c.JSON(200, gin.H{"items": service.AnimeProviders()}) })
	r.POST("/catalog/:provider", func(c *gin.Context) {
		provider := c.Param("provider")
		if provider != "mikan" {
			status, code := http.StatusNotFound, "PROVIDER_NOT_FOUND"
			if provider == "anibt" || provider == "garden" {
				status, code = http.StatusServiceUnavailable, "PROVIDER_NOT_READY"
			}
			c.JSON(status, gin.H{"code": code, "error": "该站点暂未接入，请明确选择可用站点"})
			return
		}
		var in struct {
			Mode    string `json:"mode"`
			Season  string `json:"season"`
			Keyword string `json:"keyword"`
			Refresh bool   `json:"refresh"`
		}
		if !animeBody(c, &in, false) {
			return
		}
		in.Keyword = strings.TrimSpace(in.Keyword)
		if in.Mode != "calendar" && in.Mode != "search" || in.Mode == "calendar" && (!mikan.ValidCatalogSeason(in.Season) || in.Keyword != "") || in.Mode == "search" && (!validAnimeSearch(in.Keyword, 128) || in.Season != "") {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		if s.Bindings == nil || s.Bindings.Mikan == nil {
			c.JSON(503, gin.H{"code": "PROVIDER_NOT_READY", "error": "蜜柑来源暂不可用"})
			return
		}
		out, err := s.Bindings.Mikan.AnimeCatalog(c.Request.Context(), in.Season, in.Keyword, in.Refresh)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, out)
	})
}
