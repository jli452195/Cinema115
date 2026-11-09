package httpapi

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func mediaOrganizeHistoryEvents(c *gin.Context, s *service.MediaOrganizer) {
	changes, unsubscribe := s.SubscribeHistoryChanges()
	defer unsubscribe()
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("X-Accel-Buffering", "no")
	controller := http.NewResponseController(c.Writer)
	write := func(frame string) bool {
		// 每次写入单独限时，空闲连接仍可等待心跳，慢连接不会无限占用请求。
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return false
		}
		defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
		if _, err := io.WriteString(c.Writer, frame); err != nil {
			return false
		}
		return controller.Flush() == nil
	}
	if !write("event: ready\ndata: {}\n\n") {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var pending <-chan time.Time
	notifications := changes
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-notifications:
			// 使用固定窗口而非不断延后，连续检查点最多每半秒通知一次。
			timer.Reset(500 * time.Millisecond)
			pending, notifications = timer.C, nil
		case <-pending:
			pending = nil
			select {
			case <-changes:
			default:
			}
			notifications = changes
			if !write("event: changed\ndata: {}\n\n") {
				return
			}
		case <-heartbeat.C:
			if !write(": heartbeat\n\n") {
				return
			}
		}
	}
}
