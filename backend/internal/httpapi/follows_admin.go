package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

func RegisterFollowAdmin(admin *gin.RouterGroup, s *service.FollowService) {
	group := admin.Group("/follows")
	group.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	group.GET("", func(c *gin.Context) {
		kind, status, query := c.Query("kind"), c.Query("status"), strings.TrimSpace(c.Query("q"))
		offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if err != nil || offset < 0 || offset > 1000000 || (kind != "" && kind != "movie" && kind != "tv") || (status != "" && status != "pending" && status != "processing" && status != "closed" && status != "completed") || !utf8.ValidString(query) || utf8.RuneCountInString(query) > 100 {
			c.JSON(400, gin.H{"error": "筛选条件无效"})
			return
		}
		items, more, err := s.AdminList(c.Request.Context(), kind, status, query, offset)
		if err != nil {
			followError(c, err)
			return
		}
		counts, err := s.Store.FollowCounts(c.Request.Context(), "", kind, query)
		if err != nil {
			followError(c, err)
			return
		}
		c.JSON(200, gin.H{"items": items, "hasMore": more, "counts": counts})
	})
	group.GET("/settings", func(c *gin.Context) {
		settings, err := s.Store.FollowSettings(c.Request.Context())
		if err != nil {
			followError(c, err)
			return
		}
		configured := s.Notifications != nil && s.Notifications.FollowNotificationsConfigured()
		c.JSON(200, gin.H{"baseUrl": settings.BaseURL, "notificationsConfigured": configured, "autoEnabled": settings.AutoEnabled})
	})
	group.POST("/settings", func(c *gin.Context) {
		var input model.FollowSettings
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "追更设置格式无效"})
			return
		}
		if err := s.SaveSettings(c.Request.Context(), input); err != nil {
			followError(c, err)
			return
		}
		c.JSON(200, gin.H{"message": "追更设置已保存"})
	})
	group.POST("/:kind/:id", func(c *gin.Context) {
		kind := c.Param("kind")
		id, err := strconv.Atoi(c.Param("id"))
		if !validEmbyMedia(kind, c.Param("id")) || err != nil || id > 2147483647 || strconv.Itoa(id) != c.Param("id") {
			c.JSON(400, gin.H{"error": "影视编号无效"})
			return
		}
		var input struct {
			Status   string `json:"status"`
			Note     string `json:"note"`
			Revision int64  `json:"revision"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "处理状态格式无效"})
			return
		}
		if err := s.Update(c.Request.Context(), kind, id, input.Status, input.Note, input.Revision); err != nil {
			followError(c, err)
			return
		}
		c.JSON(200, gin.H{"message": "处理状态已保存，订阅用户可以查看"})
	})
}
