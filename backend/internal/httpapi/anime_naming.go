package httpapi

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func registerAnimeNaming(r *gin.RouterGroup, s *service.AnimeAdminService) {
	r.POST("/naming/preview", func(c *gin.Context) {
		var in service.AnimeNamingPreviewInput
		if !animeBody(c, &in, true) {
			return
		}
		v, err := service.PreviewAnimeNaming(c.Request.Context(), s.Store, in)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
	r.POST("/naming/search", func(c *gin.Context) {
		var in struct {
			Keyword   string `json:"keyword"`
			BangumiID string `json:"bangumiId"`
		}
		if !animeBody(c, &in, false) {
			return
		}
		in.Keyword = strings.TrimSpace(in.Keyword)
		if in.Keyword != "" && !validAnimeSearch(in.Keyword, 128) || in.BangumiID != "" && !model.ValidAnimeExternalID(in.BangumiID) || in.Keyword == "" && in.BangumiID == "" {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		if s.Naming == nil || s.Naming.Metadata == nil {
			animeError(c, storage.ErrAnimeCapability)
			return
		}
		names := []string{in.Keyword}
		if in.Keyword == "" {
			v, err := s.Discovery.Subject(c.Request.Context(), in.BangumiID)
			if err != nil {
				animeError(c, err)
				return
			}
			names = []string{v.Subject.Title, v.Subject.OriginalTitle}
		}
		v, err := s.Naming.Search(c.Request.Context(), names...)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
	r.POST("/naming/target", func(c *gin.Context) {
		var in service.AnimeNamingTargetInput
		if !animeBody(c, &in, false) {
			return
		}
		if s.Naming == nil {
			animeError(c, storage.ErrAnimeCapability)
			return
		}
		date := ""
		var names []string
		if in.BangumiID != "" {
			if !model.ValidAnimeExternalID(in.BangumiID) {
				animeError(c, storage.ErrAnimeInvalid)
				return
			}
			v, err := s.Discovery.Subject(c.Request.Context(), in.BangumiID)
			if err != nil {
				animeError(c, err)
				return
			}
			date = v.Subject.Date
			names = []string{v.Subject.Title, v.Subject.OriginalTitle}
		}
		v, err := s.Naming.Target(c.Request.Context(), in, date, names...)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
	r.GET("/naming/groups/:id", func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id < 1 || !model.ValidAnimeExternalID(c.Param("id")) {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		if s.Naming == nil || s.Naming.Metadata == nil {
			animeError(c, storage.ErrAnimeCapability)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		v, err := s.Naming.Metadata.AnimeGroups(ctx, id)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, gin.H{"items": v})
	})
}
