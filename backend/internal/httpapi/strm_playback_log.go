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
	started         time.Time
	phase, failure  string
	cacheHit        *bool
	recorded        bool
	recordedFailure string
}

func (entry *strmPlaybackLog) write(c *gin.Context, logs *service.GenerationLogs, source string) {
	if logs == nil || entry.recorded && (entry.failure == "" || entry.failure == entry.recordedFailure) {
		return
	}
	// Each seek creates another successful Range request. Keep that traffic in
	// recent diagnostics instead of filling the persisted operational history.
	level := "DEBUG"
	if c.Writer.Status() >= 500 || entry.failure != "" && c.Writer.Status() < 400 {
		level = "ERROR"
	} else if c.Writer.Status() >= 400 {
		level = "WARNING"
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
	entry.recordedFailure = entry.failure
}
