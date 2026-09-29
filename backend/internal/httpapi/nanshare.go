package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/nanshare"
	"strm115/backend/internal/service"
)

type NanShareConfigurer interface {
	View() service.NanShareView
	Configure(context.Context, service.NanShareConfigInput) (service.NanShareView, error)
	Verify(context.Context) (service.NanShareView, error)
	SearchTest(context.Context, string, int64) (nanshare.SearchResult, error)
	Search(context.Context, string, int64, bool) (service.NanShareSearchView, error)
	ShareOptions(context.Context, string) (nanshare.ShareOptions, error)
	TransferOptions(context.Context, string) (nanshare.TransferOptions, error)
	Submit(context.Context, service.NanShareSubmitInput) (service.NanShareSubmission, error)
	Transfer(context.Context, service.NanShareTransferInput) (service.NanShareSubmission, error)
	Clear() error
}

func RegisterNanShare(admin *gin.RouterGroup, configurer NanShareConfigurer) {
	group := admin.Group("/nanshare")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		status := http.StatusBadGateway
		if errors.Is(err, nanshare.ErrInput) {
			status = http.StatusBadRequest
		}
		if errors.Is(err, nanshare.ErrAuth) {
			status = http.StatusConflict
		}
		if errors.Is(err, nanshare.ErrChanged) {
			status = http.StatusConflict
		}
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		message := err.Error()
		if status == http.StatusGatewayTimeout {
			message = "NanShare 请求超时，请稍后重试"
		}
		c.JSON(status, gin.H{"error": message})
	}
	group.GET("/config", func(c *gin.Context) { c.JSON(http.StatusOK, configurer.View()) })
	group.POST("/search", func(c *gin.Context) {
		var input struct {
			Kind    string `json:"kind"`
			TMDBID  int64  `json:"tmdbId"`
			Refresh bool   `json:"refresh"`
		}
		if c.ShouldBindJSON(&input) != nil {
			fail(c, nanshare.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 65*time.Second)
		defer cancel()
		view, err := configurer.Search(ctx, input.Kind, input.TMDBID, input.Refresh)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.GET("/share-options/:searchId", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 65*time.Second)
		defer cancel()
		view, err := configurer.ShareOptions(ctx, c.Param("searchId"))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.GET("/transfer-options/:searchId", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		view, err := configurer.TransferOptions(ctx, c.Param("searchId"))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.POST("/submit", func(c *gin.Context) {
		var input service.NanShareSubmitInput
		if c.ShouldBindJSON(&input) != nil {
			fail(c, nanshare.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Second)
		defer cancel()
		view, err := configurer.Submit(ctx, input)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.POST("/transfer", func(c *gin.Context) {
		var input service.NanShareTransferInput
		if c.ShouldBindJSON(&input) != nil {
			fail(c, nanshare.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 65*time.Second)
		defer cancel()
		view, err := configurer.Transfer(ctx, input)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.POST("/config", func(c *gin.Context) {
		var input service.NanShareConfigInput
		if c.ShouldBindJSON(&input) != nil {
			fail(c, nanshare.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
		defer cancel()
		view, err := configurer.Configure(ctx, input)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.DELETE("/config", func(c *gin.Context) {
		if err := configurer.Clear(); err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, configurer.View())
	})
	group.POST("/verify", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
		defer cancel()
		view, err := configurer.Verify(ctx)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	group.POST("/search-test", func(c *gin.Context) {
		var input struct {
			Kind   string `json:"kind"`
			TMDBID int64  `json:"tmdbId"`
		}
		if c.ShouldBindJSON(&input) != nil || (input.Kind != "movie" && input.Kind != "tv") || input.TMDBID < 1 || input.TMDBID > 9007199254740991 {
			fail(c, nanshare.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()
		result, err := configurer.SearchTest(ctx, input.Kind, input.TMDBID)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
}
