package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/anime/notification"
	"strm115/backend/internal/bot"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func registerAnimeTelegramNotifications(group *gin.RouterGroup, hub *bot.Hub) {
	fail := func(c *gin.Context, err error) {
		status, message := http.StatusInternalServerError, "动漫通知配置操作失败，请稍后重试"
		switch {
		case errors.Is(err, notification.ErrTemplate), errors.Is(err, notification.ErrImage):
			status, message = http.StatusBadRequest, err.Error()
		case errors.Is(err, storage.ErrAnimeInvalid), errors.Is(err, bot.ErrConfig):
			status, message = http.StatusBadRequest, "通知配置格式或取值不正确"
		case errors.Is(err, storage.ErrAnimeConflict):
			status, message = http.StatusConflict, "通知配置已变化，请保留输入并重新读取"
		case errors.Is(err, bot.ErrBotMissing):
			status, message = http.StatusBadRequest, "所选 Bot 已删除，请重新选择"
		case errors.Is(err, bot.ErrAnimeNotificationsUnavailable):
			status, message = http.StatusServiceUnavailable, err.Error()
		}
		c.JSON(status, gin.H{"error": message})
	}
	view := func(c *gin.Context, settings model.AnimeNotificationSettings) {
		sample := notification.Sample("organized")
		html, err := notification.Render(settings.Template, notification.Values(sample))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"settings": settings, "defaultTemplate": notification.DefaultTemplate,
			"variables": notification.Variables, "preview": gin.H{"html": html, "photoUrl": notification.PhotoURL(settings.ImageMode, settings.ImageURL, sample.CoverURL)},
			"maxTemplateLength": notification.MaxTemplateLength,
		})
	}
	group.GET("/anime-notifications", func(c *gin.Context) {
		settings, err := hub.AnimeNotificationSettings(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		view(c, settings)
	})
	group.PUT("/anime-notifications", func(c *gin.Context) {
		var input model.AnimeNotificationSettings
		if c.ShouldBindJSON(&input) != nil {
			fail(c, storage.ErrAnimeInvalid)
			return
		}
		settings, err := hub.SaveAnimeNotificationSettings(c.Request.Context(), input)
		if err != nil {
			fail(c, err)
			return
		}
		if hub.Logs != nil {
			hub.Logs.Append("Telegram", "", "动漫通知", "INFO", "动漫订阅通知模板与推送设置已保存")
		}
		view(c, settings)
	})
	group.POST("/anime-notifications/preview", func(c *gin.Context) {
		var input struct {
			Template  string `json:"template"`
			Event     string `json:"event"`
			ImageMode string `json:"imageMode"`
			ImageURL  string `json:"imageUrl"`
		}
		if c.ShouldBindJSON(&input) != nil || !notification.ValidEvent(input.Event) {
			fail(c, storage.ErrAnimeInvalid)
			return
		}
		mode, imageURL, err := notification.NormalizeImage(input.ImageMode, input.ImageURL)
		if err != nil {
			fail(c, err)
			return
		}
		sample := notification.Sample(input.Event)
		html, err := notification.Render(input.Template, notification.Values(sample))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"html": html, "photoUrl": notification.PhotoURL(mode, imageURL, sample.CoverURL)})
	})
}
