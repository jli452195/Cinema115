package httpapi

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"strm115/backend/internal/bot"
	"time"
)

func RegisterTelegram(admin *gin.RouterGroup, manager *bot.Manager) {
	admin.GET("/telegram/config", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.JSON(200, manager.View()) })
	handle := func(test bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
			var input bot.BotConfig
			if c.ShouldBindJSON(&input) != nil {
				c.JSON(400, gin.H{"error": "Bot 配置格式错误"})
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
			defer cancel()
			var err error
			if test {
				var identity bot.Identity
				identity, err = manager.Test(ctx, input.Token)
				if err == nil {
					c.JSON(200, identity)
					return
				}
			} else {
				err = manager.Save(ctx, input)
				if err == nil {
					c.JSON(200, manager.View())
					return
				}
			}
			code := http.StatusBadGateway
			if errors.Is(err, bot.ErrConfig) {
				code = http.StatusBadRequest
			}
			if ctx.Err() != nil {
				c.JSON(504, gin.H{"error": "连接 Telegram 超时，请检查后端网络后重试"})
				return
			}
			c.JSON(code, gin.H{"error": err.Error()})
		}
	}
	admin.POST("/telegram/test", handle(true))
	admin.POST("/telegram/config", handle(false))
}
