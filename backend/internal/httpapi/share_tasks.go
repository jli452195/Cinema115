package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
)

type ShareTaskHistory interface {
	ListShareTasks(context.Context, string, string, string, int, int) ([]model.ShareTask, error)
	ClearShareTasks(context.Context, []string) (int64, error)
}

func RegisterShareTasks(admin *gin.RouterGroup, store ShareTaskHistory) {
	admin.GET("/p115/share/tasks", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		limit, e1 := strconv.Atoi(c.DefaultQuery("limit", "50"))
		offset, e2 := strconv.Atoi(c.DefaultQuery("offset", "0"))
		status, mode := c.Query("status"), c.Query("mode")
		validStatus := map[string]bool{"": true, "running": true, "waiting_review": true, "success": true, "failed": true, "review": true, "interrupted": true, "cancelled": true}
		if e1 != nil || e2 != nil || limit < 1 || limit > 100 || offset < 0 || !validStatus[status] || (mode != "" && mode != "direct" && mode != "reshare") || len(c.Query("search")) > 1000 {
			c.JSON(400, gin.H{"error": "日志筛选参数无效"})
			return
		}
		entries, err := store.ListShareTasks(c.Request.Context(), status, mode, c.Query("search"), limit, offset)
		if err != nil {
			c.JSON(500, gin.H{"error": "读取分享日志失败"})
			return
		}
		c.JSON(200, gin.H{"entries": entries})
	})
	admin.POST("/p115/share/tasks/clear", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var request struct {
			IDs []string `json:"ids"`
		}
		if c.ShouldBindJSON(&request) != nil || len(request.IDs) > 100 {
			c.JSON(400, gin.H{"error": "清理请求格式错误"})
			return
		}
		count, err := store.ClearShareTasks(c.Request.Context(), request.IDs)
		if err != nil {
			c.JSON(500, gin.H{"error": "清理分享日志失败"})
			return
		}
		c.JSON(200, gin.H{"deleted": count, "message": "已清理结束的记录，执行中和等待审核重试的任务继续保留"})
	})
}
