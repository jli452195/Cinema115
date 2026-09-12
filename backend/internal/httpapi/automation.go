package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

// 自动化配置与 Cookie 一样只允许已登录管理员访问。
func RegisterAutomation(admin *gin.RouterGroup, store *service.AutomationStore, botConfigured func() bool) {
	admin.GET("/p115/share/automation-config", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(200, gin.H{"settings": store.Value(), "botConfigured": botConfigured()})
	})
	admin.POST("/p115/share/automation-config", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var settings service.AutomationSettings
		if c.ShouldBindJSON(&settings) != nil {
			c.JSON(400, gin.H{"error": "自动化配置格式错误"})
			return
		}
		if err := store.Save(settings); err != nil {
			if errors.Is(err, service.ErrGenerationInput) {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
			c.JSON(500, gin.H{"error": "保存自动化配置失败"})
			return
		}
		c.JSON(200, gin.H{"settings": store.Value(), "botConfigured": botConfigured()})
	})
}
