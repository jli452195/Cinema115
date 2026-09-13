package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
)

type ReshareJobReader interface {
	ListReshareJobs(context.Context, string, int, int) ([]model.ReshareJob, error)
	GetReshareJob(context.Context, string) (model.ReshareJob, error)
}

// RegisterReshareJobs 仅挂载到管理员路由，任务详情可能包含分享提取码。
func RegisterReshareJobs(admin *gin.RouterGroup, jobs ReshareJobReader) {
	admin.GET("/reshare/jobs", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "20"))
		offset, offsetErr := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if limitErr != nil || offsetErr != nil || limit < 1 || limit > 100 || offset < 0 {
			c.JSON(400, gin.H{"error": "limit 应为 1～100，offset 应为非负整数"})
			return
		}
		entries, err := jobs.ListReshareJobs(c.Request.Context(), c.Query("status"), limit, offset)
		if err != nil {
			c.JSON(500, gin.H{"error": "读取转存任务失败"})
			return
		}
		c.JSON(200, gin.H{"entries": entries, "limit": limit, "offset": offset})
	})
	admin.GET("/reshare/jobs/:id", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		job, err := jobs.GetReshareJob(c.Request.Context(), c.Param("id"))
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(404, gin.H{"error": "任务不存在"})
			return
		}
		if err != nil {
			c.JSON(500, gin.H{"error": "读取转存任务失败"})
			return
		}
		c.JSON(200, job)
	})
}
