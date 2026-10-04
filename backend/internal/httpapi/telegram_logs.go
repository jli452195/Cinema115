package httpapi

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

// 只记录预定义操作和结果摘要，不读取请求体、上游原文或带凭据的 URL。
func telegramOperationLogs(logs *service.GenerationLogs, source string, actions map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != "POST" {
			c.Next()
			return
		}
		label, ok := actions[c.FullPath()]
		if !ok {
			c.Next()
			return
		}
		start := time.Now()
		logs.Append("Telegram", "", source, "DEBUG", label+"：开始")
		c.Next()
		code := c.Writer.Status()
		level, message := "INFO", label+"：完成"
		if code >= 400 {
			level, message = "WARNING", fmt.Sprintf("%s：未完成（HTTP %d），请查看页面错误提示", label, code)
			if code >= 500 {
				level = "ERROR"
			}
		} else if failed, _ := c.Get("telegramLogFailed"); failed == true {
			level, message = "ERROR", label+"：未完成，请查看页面错误提示"
			if summary, ok := c.Get("telegramLogSummary"); ok {
				message = label + "：" + fmt.Sprint(summary)
			}
		} else if summary, ok := c.Get("telegramLogSummary"); ok {
			message = label + "：" + fmt.Sprint(summary)
			if cached, _ := c.Get("telegramLogCached"); cached == true {
				level = "DEBUG"
			}
		}
		logs.Append("Telegram", "", source, level, fmt.Sprintf("%s，耗时 %.2f 秒", message, time.Since(start).Seconds()))
	}
}
