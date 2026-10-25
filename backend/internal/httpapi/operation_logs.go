package httpapi

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

// 只记录预定义操作和结果摘要，不读取请求体、上游原文或带凭据的 URL。
func operationLogs(logs *service.GenerationLogs, module, source string, actions map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if logs == nil || c.Request.Method != "POST" {
			c.Next()
			return
		}
		label, ok := actions[c.FullPath()]
		if !ok {
			c.Next()
			return
		}
		start := time.Now()
		task := logs.NewTaskID("OP")
		logs.Append(module, task, source, "DEBUG", label+"：开始")
		c.Next()
		code := c.Writer.Status()
		level, message := "INFO", label+"：完成"
		if code >= 400 {
			level, message = "WARNING", fmt.Sprintf("%s：未完成（HTTP %d），请查看页面错误提示", label, code)
			if code >= 500 {
				level = "ERROR"
			}
		} else {
			if failed, _ := c.Get("operationLogFailed"); failed == true {
				level, message = "ERROR", label+"：未完成，请查看页面错误提示"
			} else if c.Request.Context().Err() != nil {
				level, message = "WARNING", label+"：请求已取消或超时"
			} else if cached, _ := c.Get("operationLogCached"); cached == true {
				level = "DEBUG"
			}
			if summary, ok := c.Get("operationLogSummary"); ok {
				message = label + "：" + fmt.Sprint(summary)
			}
		}
		logs.Append(module, task, source, level, fmt.Sprintf("%s，耗时 %.2f 秒", message, time.Since(start).Seconds()))
	}
}
