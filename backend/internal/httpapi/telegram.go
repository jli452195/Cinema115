package httpapi

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"strm115/backend/internal/bot"
	"strm115/backend/internal/service"
	"time"
)

func RegisterTelegram(admin *gin.RouterGroup, manager *bot.Manager, sinks ...*service.GenerationLogs) {
	admin = admin.Group("")
	if len(sinks) > 0 {
		admin.Use(telegramOperationLogs(sinks[0], "Bot", map[string]string{admin.BasePath() + "/telegram/config": "保存 Bot 配置", admin.BasePath() + "/telegram/test": "测试 Bot 连接"}))
	}
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

func RegisterTelegramHub(admin *gin.RouterGroup, hub *bot.Hub, sinks ...*service.GenerationLogs) {
	group := admin.Group("/telegram")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, bot.ErrBotMissing):
			code = http.StatusNotFound
		case errors.Is(err, bot.ErrBotChanged):
			code = http.StatusConflict
		case errors.Is(err, bot.ErrConfig):
			code = http.StatusBadRequest
		}
		if c.Request.Context().Err() != nil {
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "连接 Telegram 超时，请检查后端网络后重试"})
			return
		}
		c.JSON(code, gin.H{"error": err.Error()})
	}
	group.GET("/bots", func(c *gin.Context) { c.JSON(200, gin.H{"bots": hub.List()}) })
	group.POST("/bots", func(c *gin.Context) {
		var input struct {
			Name string `json:"name"`
		}
		if c.ShouldBindJSON(&input) != nil {
			fail(c, bot.ErrConfig)
			return
		}
		id, err := hub.Add(input.Name)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"id": id, "bots": hub.List()})
	})
	group.PUT("/bots/:botID", func(c *gin.Context) {
		var input bot.BotProfile
		if c.ShouldBindJSON(&input) != nil || input.Revision == 0 {
			fail(c, bot.ErrConfig)
			return
		}
		input.ID = c.Param("botID")
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		if err := hub.Update(ctx, input, input.Revision); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"bots": hub.List()})
	})
	group.DELETE("/bots/:botID", func(c *gin.Context) {
		var input struct {
			Revision uint64 `json:"revision"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Revision == 0 {
			fail(c, bot.ErrConfig)
			return
		}
		if err := hub.Delete(c.Param("botID"), input.Revision); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"bots": hub.List()})
	})
	group.DELETE("/bots/:botID/token", func(c *gin.Context) {
		var input struct {
			Revision uint64 `json:"revision"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Revision == 0 {
			fail(c, bot.ErrConfig)
			return
		}
		if err := hub.ClearToken(c.Param("botID"), input.Revision); err != nil {
			fail(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.POST("/bots/:botID/test", func(c *gin.Context) {
		profile, err := hub.Profiles().Get(c.Param("botID"))
		if err != nil {
			fail(c, err)
			return
		}
		var input struct {
			Token string `json:"token"`
		}
		if c.ShouldBindJSON(&input) != nil {
			fail(c, bot.ErrConfig)
			return
		}
		if input.Token == "" {
			input.Token = profile.Token
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		identity, err := hub.Test(ctx, input.Token)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, identity)
	})
}
