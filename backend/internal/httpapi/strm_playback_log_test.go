package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func TestSTRMPlaybackLogsSeparateRoutineRangesFromFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		status  int
		failure string
		level   string
	}{
		{"successful file", http.StatusOK, "", "DEBUG"},
		{"successful range", http.StatusPartialContent, "", "DEBUG"},
		{"not modified", http.StatusNotModified, "", "DEBUG"},
		{"file missing", http.StatusNotFound, "115 文件不存在", "WARNING"},
		{"range rejected", http.StatusRequestedRangeNotSatisfiable, "", "WARNING"},
		{"upstream unavailable", http.StatusServiceUnavailable, "主账号不可用", "ERROR"},
		{"stream failed after headers", http.StatusPartialContent, "读取 115 原文件失败", "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/api/drive115/file", nil)
			c.Request.Header.Set("Range", "bytes=0-1023")
			c.Request.Header.Set("User-Agent", "libmpv")
			c.Status(tc.status)
			logs := &service.GenerationLogs{}
			entry := strmPlaybackLog{started: time.Now(), phase: "原文件传输", failure: tc.failure}
			entry.write(c, logs, "目录播放")
			entry.write(c, logs, "目录播放")
			entries := logs.Snapshot("ALL", "STRM")
			if len(entries) != 1 || entries[0].Level != tc.level || !strings.Contains(entries[0].Message, "阶段=原文件传输") || !strings.Contains(entries[0].Message, "bytes=0-1023") {
				t.Fatalf("incorrect severity, diagnostics, or duplicate log: %+v", entries)
			}
			if tc.failure != "" && !strings.Contains(entries[0].Message, tc.failure) {
				t.Fatal("failure detail was discarded")
			}
		})
	}
}

func TestSTRMPlaybackLogsKeepOneLateStreamFailure(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/share115/file", nil)
	c.Status(http.StatusPartialContent)
	logs := &service.GenerationLogs{}
	entry := strmPlaybackLog{started: time.Now(), phase: "原文件传输"}
	entry.write(c, logs, "分享播放")
	entry.failure = "读取 115 原文件失败"
	entry.write(c, logs, "分享播放")
	entry.write(c, logs, "分享播放")
	entries := logs.Snapshot("ALL", "STRM")
	if len(entries) != 2 || entries[0].Level != "ERROR" || entries[1].Level != "DEBUG" {
		t.Fatalf("late stream failure was lost or duplicated: %+v", entries)
	}
}
