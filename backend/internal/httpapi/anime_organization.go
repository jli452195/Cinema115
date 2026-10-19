package httpapi

import (
	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func registerAnimeOrganization(r *gin.RouterGroup, s *service.AnimeAdminService) {
	r.PATCH("/settings/organize-directory", func(c *gin.Context) {
		var in service.AnimeOrganizeDirectoryInput
		if !animeBody(c, &in, false) {
			return
		}
		v, err := s.SaveOrganizeDirectory(c.Request.Context(), animeActor(c), c.GetHeader("Idempotency-Key"), in)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, gin.H{"id": "anime-settings", "revision": v.Revision, "settings": v})
	})
	r.GET("/subscriptions/:id/organization-preview", func(c *gin.Context) {
		id, ok := animeID(c)
		if !ok {
			return
		}
		v, err := s.OrganizationPreview(c.Request.Context(), id)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
	r.GET("/jobs/:id/organization-preview", func(c *gin.Context) {
		id, ok := animeID(c)
		if !ok {
			return
		}
		v, err := s.JobOrganizationPreview(c.Request.Context(), id)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
}
