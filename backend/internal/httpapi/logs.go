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
		if module := c.DefaultQuery("module", "STRM"); module != "STRM" {
			c.JSON(400, gin.H{"error": "暂仅支持 STRM 日志"})
			return
		}
		c.JSON(200, gin.H{"entries": logs.Snapshot(level), "capacity": service.GenerationLogCapacity})
	})
}
