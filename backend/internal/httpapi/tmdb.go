package httpapi

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdb"
)

type TMDBReader interface {
	Genres(context.Context, string) ([]tmdb.Genre, error)
	Browse(context.Context, tmdb.Options) (*tmdb.Page, error)
	Details(context.Context, string, int) (*tmdb.Detail, error)
	Season(context.Context, int, int) (*tmdb.SeasonDetail, error)
	Recommendations(context.Context, string, int) (*tmdb.Page, error)
}

func tmdbError(c *gin.Context, err error) {
	status := 502
	if errors.Is(err, tmdb.ErrInput) {
		status = 400
	}
	if errors.Is(err, tmdb.ErrNotConfigured) {
		status = 503
	}
	if errors.Is(err, tmdb.ErrNotFound) {
		status = 404
	}
	if errors.Is(err, context.DeadlineExceeded) {
		c.JSON(504, gin.H{"error": "TMDB 请求超时，请稍后重试"})
		return
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

// RegisterTMDB 只开放管理员查询接口，浏览器不会接触 API 密钥。
func RegisterTMDB(admin *gin.RouterGroup, client TMDBReader) {
	admin.GET("/tmdb/recommendations/:kind/:id", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		id, err := strconv.Atoi(c.Param("id"))
		kind := c.Param("kind")
		if err != nil || id < 1 || (kind != "tv" && kind != "movie") {
			tmdbError(c, tmdb.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 18*time.Second)
		defer cancel()
		result, err := client.Recommendations(ctx, kind, id)
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, result)
	})
	admin.GET("/tmdb/detail/:kind/:id", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		id, err := strconv.Atoi(c.Param("id"))
		kind := c.Param("kind")
		if err != nil || id < 1 || (kind != "tv" && kind != "movie") {
			tmdbError(c, tmdb.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 18*time.Second)
		defer cancel()
		result, err := client.Details(ctx, kind, id)
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, result)
	})
	admin.GET("/tmdb/tv/:id/season/:number", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		id, idErr := strconv.Atoi(c.Param("id"))
		number, numberErr := strconv.Atoi(c.Param("number"))
		if idErr != nil || numberErr != nil || id < 1 || number < 0 || number > 10000 {
			tmdbError(c, tmdb.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 18*time.Second)
		defer cancel()
		result, err := client.Season(ctx, id, number)
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, result)
	})
	admin.GET("/tmdb/genres", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(c.Request.Context(), 18*time.Second)
		defer cancel()
		genres, err := client.Genres(ctx, c.DefaultQuery("kind", "tv"))
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, gin.H{"genres": genres})
	})
	admin.GET("/tmdb/browse", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
		if err != nil {
			tmdbError(c, tmdb.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 18*time.Second)
		defer cancel()
		result, err := client.Browse(ctx, tmdb.Options{Kind: c.DefaultQuery("kind", "tv"), Genre: c.Query("genre"), Region: c.Query("region"), Year: c.Query("year"), Sort: c.Query("sort"), Query: c.Query("query"), Page: page})
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, result)
	})
}
