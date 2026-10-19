package httpapi

import (
	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func registerAnimeControl(r *gin.RouterGroup, s *service.AnimeAdminService) {
	// These actions only change local state. Public enable/resume remain gated
	// until the download UI and phase validation are accepted.
	for _, action := range []string{"pause", "archive", "remove", "unarchive"} {
		r.POST("/subscriptions/:id/"+action, func(c *gin.Context) {
			id, ok := animeID(c)
			if !ok {
				return
			}
			var in service.AnimeControlInput
			if !animeBody(c, &in, false) {
				return
			}
			v, err := s.ControlSubscription(c.Request.Context(), animeActor(c), c.GetHeader("Idempotency-Key"), id, action, in)
			if err != nil {
				animeError(c, err)
				return
			}
			c.JSON(200, v)
		})
	}
	r.GET("/subscriptions/:id/jobs", func(c *gin.Context) {
		id, ok := animeID(c)
		if !ok {
			return
		}
		page, size, ok := animePage(c)
		if !ok {
			return
		}
		v, err := s.SubscriptionJobs(c.Request.Context(), id, page, size)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
	r.GET("/jobs/:id", func(c *gin.Context) {
		id, ok := animeID(c)
		if !ok {
			return
		}
		v, err := s.DownloadJob(c.Request.Context(), id)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
}
