package httpapi

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/storage"
)

type TMDBReader interface {
	SearchPeople(context.Context, string, int) (*tmdb.PersonPage, error)
	PersonDetails(context.Context, int) (*tmdb.PersonDetail, error)
	CollectionDetails(context.Context, int) (*tmdb.CollectionDetail, error)
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
	if errors.Is(err, storage.ErrNumberingConflict) {
		status = 409
	}
	if errors.Is(err, context.DeadlineExceeded) {
		c.JSON(504, gin.H{"error": "TMDB 请求超时，请稍后重试"})
		return
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

// RegisterTMDB 挂载到调用方提供的鉴权分组，浏览器不会接触 API 密钥。
func RegisterTMDB(admin *gin.RouterGroup, client TMDBReader) {
	admin = admin.Group("")
	// 手动刷新与后台重新验证走同一路由，只跳过 TMDB 缓存，不暴露密钥。
	admin.Use(func(c *gin.Context) {
		if c.Query("refresh") == "1" && c.GetString("portal_role") != "user" {
			c.Request = c.Request.WithContext(tmdb.WithFresh(c.Request.Context()))
		}
		if resolver, ok := client.(interface {
			PinNumbering(context.Context, int) (context.Context, error)
			NumberingRevision(context.Context, int) (int64, error)
		}); ok && (c.Param("kind") == "tv" || strings.Contains(c.FullPath(), "/tmdb/tv/")) {
			id, err := strconv.Atoi(c.Param("id"))
			if err == nil && id > 0 {
				ctx, err := resolver.PinNumbering(c.Request.Context(), id)
				if err != nil {
					tmdbError(c, err)
					c.Abort()
					return
				}
				if expected := c.Query("numberingRevision"); expected != "" {
					revision, parseErr := strconv.ParseInt(expected, 10, 64)
					actual, readErr := resolver.NumberingRevision(ctx, id)
					if parseErr != nil || revision < 0 {
						tmdbError(c, tmdb.ErrInput)
						c.Abort()
						return
					}
					if readErr != nil {
						tmdbError(c, readErr)
						c.Abort()
						return
					}
					if revision != actual {
						tmdbError(c, storage.ErrNumberingConflict)
						c.Abort()
						return
					}
				}
				c.Request = c.Request.WithContext(ctx)
			}
		}
		c.Next()
	})
	if schedules, ok := client.(interface {
		Schedule(context.Context, int, int) (*tmdb.Schedule, error)
		Schedules(context.Context, int) ([]tmdb.Schedule, error)
	}); ok {
		admin.GET("/tmdb/tv/:id/schedules", func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			id, err := strconv.Atoi(c.Param("id"))
			if err != nil || id < 1 {
				tmdbError(c, tmdb.ErrInput)
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
			defer cancel()
			data, err := schedules.Schedules(ctx, id)
			if err != nil {
				tmdbError(c, err)
				return
			}
			c.JSON(200, gin.H{"seasons": data})
		})
		admin.GET("/tmdb/tv/:id/schedule/:number", func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			id, e1 := strconv.Atoi(c.Param("id"))
			number, e2 := strconv.Atoi(c.Param("number"))
			if e1 != nil || e2 != nil || id < 1 || number < 0 || number > 10000 {
				tmdbError(c, tmdb.ErrInput)
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
			defer cancel()
			data, err := schedules.Schedule(ctx, id, number)
			if err != nil {
				tmdbError(c, err)
				return
			}
			c.JSON(200, data)
		})
	}
	admin.GET("/tmdb/people", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
		if err != nil {
			tmdbError(c, tmdb.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 18*time.Second)
		defer cancel()
		result, err := client.SearchPeople(ctx, c.Query("query"), page)
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, result)
	})
	admin.GET("/tmdb/person/:id", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id < 1 {
			tmdbError(c, tmdb.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 18*time.Second)
		defer cancel()
		result, err := client.PersonDetails(ctx, id)
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, result)
	})
	admin.GET("/tmdb/collection/:id", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id < 1 {
			tmdbError(c, tmdb.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 18*time.Second)
		defer cancel()
		result, err := client.CollectionDetails(ctx, id)
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, result)
	})
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
		result, err := client.Browse(ctx, tmdb.Options{Kind: c.DefaultQuery("kind", "tv"), Category: c.Query("category"), Genre: c.Query("genre"), Region: c.Query("region"), Year: c.Query("year"), Sort: c.Query("sort"), Query: c.Query("query"), Page: page})
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, result)
	})
}
