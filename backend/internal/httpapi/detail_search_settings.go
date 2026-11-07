package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
)

type detailSearchSettingsStore interface {
	DetailSearchSettings(context.Context) (model.DetailSearchSettings, error)
	SaveDetailSearchSettings(context.Context, model.DetailSearchSettings) (model.DetailSearchSettings, error)
}

func RegisterDetailSearchSettings(admin *gin.RouterGroup, store detailSearchSettingsStore) {
	group := admin.Group("/resources/search-settings")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	group.GET("", func(c *gin.Context) {
		settings, err := store.DetailSearchSettings(c.Request.Context())
		if err != nil {
			detailSearchSettingsError(c, err)
			return
		}
		c.JSON(http.StatusOK, settings)
	})
	group.PUT("", func(c *gin.Context) {
		var input model.DetailSearchSettings
		if err := decodeStrictJSON(c.Request.Body, &input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "搜索设置格式无效"})
			return
		}
		if err := input.Validate(); err != nil {
			detailSearchSettingsError(c, err)
			return
		}
		settings, err := store.SaveDetailSearchSettings(c.Request.Context(), input)
		if err != nil {
			detailSearchSettingsError(c, err)
			return
		}
		c.JSON(http.StatusOK, settings)
	})
}

func detailSearchSettingsError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, model.ErrDetailSearchSettings) {
		status = http.StatusBadRequest
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusRequestTimeout
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
