package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/woniu"
	"strm115/backend/internal/service"
)

type WoniuSearcher interface {
	View() service.WoniuView
	Configure(context.Context, service.WoniuConfigInput) (service.WoniuView, error)
	Clear(context.Context) error
	Search(context.Context, service.WoniuSearchInput) (service.WoniuSearchResult, error)
}

func RegisterWoniu(admin *gin.RouterGroup, searcher WoniuSearcher, tmdb TMDBReader) {
	group := admin.Group("/woniu")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		code, message := woniu.PublicError(err)
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, woniu.ErrInput):
			status = http.StatusBadRequest
		case errors.Is(err, woniu.ErrRateLimited):
			status = http.StatusTooManyRequests
			c.Header("Retry-After", "4")
		case errors.Is(err, woniu.ErrNotConfigured), errors.Is(err, woniu.ErrSession), errors.Is(err, woniu.ErrLogin), errors.Is(err, woniu.ErrChallenge):
			status = http.StatusConflict
		case errors.Is(err, context.DeadlineExceeded):
			status = http.StatusGatewayTimeout
		}
		c.JSON(status, gin.H{"error": message, "code": code})
	}
	group.GET("/config", func(c *gin.Context) { c.JSON(http.StatusOK, searcher.View()) })
	group.POST("/config", func(c *gin.Context) {
		var input service.WoniuConfigInput
		if c.ShouldBindJSON(&input) != nil {
			fail(c, woniu.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 70*time.Second)
		defer cancel()
		view, err := searcher.Configure(ctx, input)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.DELETE("/config", func(c *gin.Context) {
		if err := searcher.Clear(c.Request.Context()); err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, searcher.View())
	})
	group.POST("/search", func(c *gin.Context) {
		var input struct {
			Kind    string `json:"kind"`
			TMDBID  string `json:"tmdbId"`
			Page    int    `json:"page"`
			Method  string `json:"method"`
			Refresh bool   `json:"refresh"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Kind != "movie" && input.Kind != "tv" {
			fail(c, woniu.ErrInput)
			return
		}
		id, err := strconv.Atoi(input.TMDBID)
		if err != nil || id < 1 || input.Page < 0 || input.Page > woniu.MaxPages || input.Method != "" && input.Method != "title" && input.Method != "original" || input.Page > 1 && input.Method == "" {
			fail(c, woniu.ErrInput)
			return
		}
		view := searcher.View()
		if !view.Configured {
			fail(c, woniu.ErrNotConfigured)
			return
		}
		metadataCtx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		detail, err := tmdb.Details(metadataCtx, input.Kind, id)
		cancel()
		if err != nil {
			tmdbError(c, err)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
		defer cancel()
		result, err := searcher.Search(ctx, service.WoniuSearchInput{CacheKey: input.Kind + ":" + strconv.Itoa(id), Title: detail.Title, OriginalTitle: detail.OriginalTitle, Page: input.Page, Method: input.Method, Refresh: input.Refresh})
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
}
