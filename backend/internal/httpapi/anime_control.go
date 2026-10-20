package httpapi

import (
	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func registerAnimeControl(r *gin.RouterGroup, s *service.AnimeAdminService) {
	// Local lifecycle changes never delete drive files.
	for _, action := range []string{"pause", "resume", "archive", "remove", "unarchive"} {
		r.POST("/subscriptions/:id/"+action, func(c *gin.Context) {
			id, ok := animeID(c)
			if !ok {
				return
			}
			var in service.AnimeControlInput
			if !animeBody(c, &in, false) {
				return
			}
			if action == "resume" && !s.DownloadAvailable() {
				animeError(c, storage.ErrAnimeCapability)
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
		v, err := s.JobPageView(c.Request.Context(), id, page, size)
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
		v, err := s.JobView(c.Request.Context(), id)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
	for _, action := range []string{"reconcile", "retry"} {
		r.POST("/jobs/:id/"+action, func(c *gin.Context) {
			id, ok := animeID(c)
			if !ok {
				return
			}
			var in service.AnimeControlInput
			if !animeBody(c, &in, false) {
				return
			}
			v, err := s.JobAction(c.Request.Context(), animeActor(c), c.GetHeader("Idempotency-Key"), id, action, in)
			if err != nil {
				animeError(c, err)
				return
			}
			c.JSON(200, v)
		})
	}
	for _, path := range []string{"/subscriptions/:id/enable-preview", "/subscriptions/:id/enable"} {
		r.POST(path, func(c *gin.Context) { animeError(c, storage.ErrAnimeCapability) })
	}
}
