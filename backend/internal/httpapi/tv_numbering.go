package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/service"
)

type TVNumberingManager interface {
	NumberingChoices(context.Context, int) (*service.NumberingChoices, error)
	PreviewNumbering(context.Context, int, string) (*service.NumberingPreview, error)
	SaveNumbering(context.Context, int, service.SaveNumberingInput) (*tmdb.NumberingInfo, error)
}

// Only registered on the administrator router. Catalog users receive the effective
// ordering through existing read APIs; they cannot change or refresh a saved group.
func RegisterTVNumbering(admin *gin.RouterGroup, manager TVNumberingManager) {
	group := admin.Group("/tmdb/tv/:id")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id < 1 {
			tmdbError(c, tmdb.ErrInput)
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	group.GET("/episode-groups", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		value, err := manager.NumberingChoices(c.Request.Context(), id)
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, value)
	})
	group.GET("/episode-groups/:groupId", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		value, err := manager.PreviewNumbering(c.Request.Context(), id, c.Param("groupId"))
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, value)
	})
	group.PUT("/numbering", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		var input service.SaveNumberingInput
		if c.ShouldBindJSON(&input) != nil {
			tmdbError(c, tmdb.ErrInput)
			return
		}
		value, err := manager.SaveNumbering(c.Request.Context(), id, input)
		if err != nil {
			tmdbError(c, err)
			return
		}
		c.JSON(200, value)
	})
}
