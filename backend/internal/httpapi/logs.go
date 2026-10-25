package httpapi

import (
	"strings"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

// RegisterLogs 必须挂载到管理员鉴权路由，日志不提供公开读取入口。
func RegisterLogs(admin *gin.RouterGroup, logs *service.GenerationLogs) {
	admin.GET("/logs", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		level := strings.ToUpper(c.DefaultQuery("level", "ALL"))
		switch level {
		case "ALL", "INFO", "DEBUG", "ERROR", "WARNING":
		default:
			c.JSON(400, gin.H{"error": "不支持的日志等级"})
			return
		}
		module := c.DefaultQuery("module", "STRM")
		if module != "STRM" && module != "Telegram" && module != "订阅" && module != "搜索" && module != "Emby" && module != "Emby 302" && module != "ALL" {
			c.JSON(400, gin.H{"error": "不支持的日志模块"})
			return
		}
		c.JSON(200, gin.H{"entries": logs.Snapshot(level, module), "capacity": service.GenerationLogCapacity})
	})
}
