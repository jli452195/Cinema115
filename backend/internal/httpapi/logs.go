package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
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
		after, incremental := c.GetQuery("after")
		var cursor uint64
		if incremental {
			var err error
			cursor, err = strconv.ParseUint(after, 10, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "日志游标无效"})
				return
			}
		}
		c.JSON(http.StatusOK, logs.Read(level, module, cursor, incremental, c.Query("instanceID")))
	})
	admin.GET("/logs/files", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		files, err := logs.LogFiles()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取历史日志列表失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"files": files, "maxSizeMB": service.GenerationLogFileMaxSizeMB, "maxBackups": service.GenerationLogFileMaxBackups, "maxAgeDays": service.GenerationLogFileMaxAgeDays})
	})
	admin.GET("/logs/files/:name", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		file, size, err := logs.OpenLogFile(c.Param("name"))
		if err != nil {
			status, message := http.StatusInternalServerError, "读取历史日志失败"
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrInvalid) {
				status, message = http.StatusNotFound, "日志文件不存在或已轮转清理，请刷新列表"
			}
			c.JSON(status, gin.H{"error": message})
			return
		}
		defer file.Close()
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": c.Param("name")}))
		c.DataFromReader(http.StatusOK, size, "application/x-ndjson; charset=utf-8", io.NewSectionReader(file, 0, size), nil)
	})
}
