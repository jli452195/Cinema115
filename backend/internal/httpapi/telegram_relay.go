package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tguser"
	"strm115/backend/internal/service"
)

func RegisterTelegramRelay(admin *gin.RouterGroup, relay *service.TelegramRelay) {
	group := admin.Group("/telegram-relay")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		status, message := http.StatusInternalServerError, "Telegram转发操作失败，请稍后重试"
		var detail *tguser.OperationError
		switch {
		case errors.Is(err, sql.ErrNoRows):
			status, message = http.StatusNotFound, "转发规则不存在"
		case errors.Is(err, service.ErrTelegramRelayInput), errors.Is(err, tguser.ErrInput):
			status, message = http.StatusBadRequest, err.Error()
		case errors.Is(err, tguser.ErrLogin), errors.Is(err, tguser.ErrConfig), errors.Is(err, tguser.ErrBusy), errors.Is(err, tguser.ErrChannelSelection):
			status, message = http.StatusConflict, err.Error()
		case errors.As(err, &detail):
			status, message = http.StatusConflict, detail.Message
		case errors.Is(err, context.DeadlineExceeded):
			status, message = http.StatusGatewayTimeout, "Telegram请求超时，请稍后重试"
		}
		c.JSON(status, gin.H{"error": message})
	}
	group.GET("", func(c *gin.Context) {
		result, err := relay.List(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.POST("/peers", func(c *gin.Context) {
		var input struct {
			Cursor string `json:"cursor"`
		}
		if c.ShouldBindJSON(&input) != nil || len(input.Cursor) > 64 {
			fail(c, tguser.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
		defer cancel()
		result, err := relay.Peers(ctx, input.Cursor)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/avatars/:peerId", func(c *gin.Context) {
		peerID, avatarID := c.Param("peerId"), c.Query("photo")
		if len(peerID) > 80 || avatarID == "" || len(avatarID) > 32 {
			fail(c, tguser.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		data, contentType, err := relay.Avatar(ctx, peerID, avatarID)
		if err != nil {
			if errors.Is(err, tguser.ErrAvatarUnavailable) {
				c.JSON(http.StatusNotFound, gin.H{"error": "头像不可用"})
				return
			}
			fail(c, err)
			return
		}
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cross-Origin-Resource-Policy", "same-origin")
		c.Data(http.StatusOK, contentType, data)
	})
	group.POST("/rules", func(c *gin.Context) {
		var input service.TelegramRelayRuleInput
		if c.ShouldBindJSON(&input) != nil {
			fail(c, tguser.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
		defer cancel()
		result, err := relay.Save(ctx, input)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"rule": result})
	})
	group.DELETE("/rules/:id", func(c *gin.Context) {
		if err := relay.Delete(c.Request.Context(), c.Param("id")); err != nil {
			fail(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}
