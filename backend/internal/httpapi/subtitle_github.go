package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/subshare"
	"strm115/backend/internal/service"
)

func RegisterSubtitleGitHubSettings(admin *gin.RouterGroup, settings *service.SubtitleGitHubSettings) {
	r := admin.Group("/subtitles/github/config")
	r.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		status, message := http.StatusInternalServerError, "GitHub 字幕库配置保存失败，请稍后重试"
		switch {
		case errors.Is(err, subshare.ErrInput):
			status, message = http.StatusBadRequest, subshare.ErrInput.Error()
		case errors.Is(err, subshare.ErrToken), errors.Is(err, subshare.ErrForbidden):
			status, message = http.StatusConflict, err.Error()
		case errors.Is(err, subshare.ErrRateLimited):
			status, message = http.StatusTooManyRequests, subshare.ErrRateLimited.Error()
		case errors.Is(err, subshare.ErrUnavailable):
			status, message = http.StatusBadGateway, subshare.ErrUnavailable.Error()
		case errors.Is(err, context.DeadlineExceeded):
			status, message = http.StatusGatewayTimeout, "GitHub 请求超时，请稍后重试"
		}
		c.JSON(status, gin.H{"error": message})
	}
	view := func(c *gin.Context) { c.JSON(http.StatusOK, settings.View()) }
	r.GET("", view)
	r.POST("", func(c *gin.Context) {
		var input struct {
			Token string `json:"token"`
		}
		if err := decodeStrictJSON(c.Request.Body, &input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": subshare.ErrInput.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		defer cancel()
		if err := settings.Configure(ctx, input.Token); err != nil {
			fail(c, err)
			return
		}
		view(c)
	})
	r.DELETE("", func(c *gin.Context) {
		if err := settings.Clear(c.Request.Context()); err != nil {
			fail(c, err)
			return
		}
		view(c)
	})
}
