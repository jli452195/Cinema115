package httpapi

import (
	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func registerAnimeControl(r *gin.RouterGroup, s *service.AnimeAdminService) {
	r.POST("/subscriptions/:id/redownload", func(c *gin.Context) {
		id, ok := animeID(c)
		if !ok {
			return
		}
		var in storage.AnimeRedownloadInput
		if !animeBody(c, &in, true) {
			return
		}
		v, err := s.RedownloadAnime(c.Request.Context(), animeActor(c), c.GetHeader("Idempotency-Key"), id, in)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
	// Refresh execution state without rereading the RSS or inspecting 115.
	r.POST("/subscriptions/:id/release-states", func(c *gin.Context) {
		id, ok := animeID(c)
		if !ok {
			return
		}
		var in struct {
			SourceID   string   `json:"sourceId"`
			ReleaseIDs []string `json:"releaseIds"`
		}
		if !animeBody(c, &in, true) {
			return
		}
		if !model.ValidAnimeID(in.SourceID) || len(in.ReleaseIDs) > 2000 {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		source, err := s.Store.GetAnimeSource(c.Request.Context(), in.SourceID)
		if err != nil {
			animeError(c, err)
			return
		}
		if source.SubscriptionID != id {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		states, jobs, err := s.Store.AnimePreviewStates(c.Request.Context(), in.SourceID, in.ReleaseIDs)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, gin.H{"localStates": states, "localJobs": jobs})
	})
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
}
