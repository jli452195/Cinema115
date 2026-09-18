package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/gying"
	"strm115/backend/internal/service"
)

type GyingSearcher interface {
	View() service.GyingView
	Configure(context.Context, service.GyingConfigInput) (service.GyingView, error)
	UpdateResourceOrder([]string) (service.GyingView, error)
	Clear() error
	Search(context.Context, service.GyingSearchInput) (service.GyingSearchResult, error)
}

func RegisterGying(admin *gin.RouterGroup, searcher GyingSearcher, tmdb TMDBReader) {
	group := admin.Group("/gying")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, gying.ErrInput):
			status = http.StatusBadRequest
		case errors.Is(err, gying.ErrNotConfigured), errors.Is(err, gying.ErrLogin), errors.Is(err, gying.ErrForbidden):
			status = http.StatusConflict
		case errors.Is(err, context.DeadlineExceeded):
			status = http.StatusGatewayTimeout
		}
		c.JSON(status, gin.H{"error": err.Error()})
	}
	group.GET("/config", func(c *gin.Context) { c.JSON(http.StatusOK, searcher.View()) })
	group.POST("/config", func(c *gin.Context) {
		var input service.GyingConfigInput
		if c.ShouldBindJSON(&input) != nil {
			fail(c, gying.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
		defer cancel()
		view, err := searcher.Configure(ctx, input)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.DELETE("/config", func(c *gin.Context) {
		if err := searcher.Clear(); err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, searcher.View())
	})
	group.PUT("/resource-order", func(c *gin.Context) {
		var input struct {
			Types []string `json:"types"`
		}
		if c.ShouldBindJSON(&input) != nil {
			fail(c, gying.ErrInput)
			return
		}
		view, err := searcher.UpdateResourceOrder(input.Types)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.POST("/search", func(c *gin.Context) {
		var input struct {
			Kind    string `json:"kind"`
			TMDBID  string `json:"tmdbId"`
			Refresh bool   `json:"refresh"`
		}
		if c.ShouldBindJSON(&input) != nil || (input.Kind != "movie" && input.Kind != "tv") {
			fail(c, gying.ErrInput)
			return
		}
		id, err := strconv.Atoi(input.TMDBID)
		if err != nil || id < 1 {
			fail(c, gying.ErrInput)
			return
		}
		metadataCtx, metadataCancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		detail, err := tmdb.Details(metadataCtx, input.Kind, id)
		metadataCancel()
		if err != nil {
			tmdbError(c, err)
			return
		}
		searchCtx, searchCancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
		defer searchCancel()
		result, err := searcher.Search(searchCtx, service.GyingSearchInput{
			CacheKey:      input.Kind + ":" + strconv.Itoa(id),
			OriginalTitle: detail.OriginalTitle,
			IMDBID:        detail.IMDBID,
			Refresh:       input.Refresh,
		})
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
}
