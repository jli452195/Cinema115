package httpapi

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

// Record once when media headers arrive, without waiting for the entire movie.
// A later stream failure adds an error; normal stops and seeks add no duplicate.
type strmPlaybackLog struct {
	started        time.Time
	phase, failure string
	cacheHit       *bool
	recorded       bool
}

func (entry *strmPlaybackLog) write(c *gin.Context, logs *service.GenerationLogs, source string) {
	if logs == nil || entry.recorded && entry.failure == "" {
		return
	}
	level := "INFO"
	if c.Writer.Status() >= 400 || entry.failure != "" {
		level = "ERROR"
	}
	details := ""
	if entry.cacheHit != nil {
		details += fmt.Sprintf("，缓存命中=%t", *entry.cacheHit)
	}
	if ua := c.Request.UserAgent(); ua != "" {
		details += "，UA=" + service.PlaybackLogName(ua)
	}
	if value := c.GetHeader("Range"); value != "" {
		details += "，Range=" + service.PlaybackLogName(value)
	}
	if entry.failure != "" {
		details += "，原因=" + entry.failure
	}
	logs.Append("STRM", "", source, level, fmt.Sprintf("STRM 播放：%s HTTP %d，阶段=%s，耗时=%d ms%s", c.Request.Method, c.Writer.Status(), entry.phase, time.Since(entry.started).Milliseconds(), details))
	entry.recorded = true
}
