package httpapi

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"time"
)

func RegisterWebsiteNotifications(group *gin.RouterGroup, store *storage.Store) {
	g := group.Group("/notifications")
	g.Use(func(c *gin.Context) {
		value, _ := c.Get("portal_user")
		user, ok := value.(model.PortalUser)
		if !ok || user.ID == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "请先登录"})
			return
		}
		c.Set("notification_user_id", user.ID)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	g.GET("/unread", func(c *gin.Context) {
		count, err := store.WebsiteUnread(c.Request.Context(), c.GetString("notification_user_id"))
		if err != nil {
			followError(c, err)
			return
		}
		c.JSON(200, gin.H{"unread": count})
	})
	g.GET("", func(c *gin.Context) {
		before, err := strconv.ParseInt(c.DefaultQuery("before", "0"), 10, 64)
		if err != nil || before < 0 {
			c.JSON(400, gin.H{"error": "通知分页参数无效"})
			return
		}
		items, err := store.WebsiteNotifications(c.Request.Context(), c.GetString("notification_user_id"), before)
		if err != nil {
			followError(c, err)
			return
		}
		more := len(items) > 20
		if more {
			items = items[:20]
		}
		count, err := store.WebsiteUnread(c.Request.Context(), c.GetString("notification_user_id"))
		if err != nil {
			followError(c, err)
			return
		}
		c.JSON(200, gin.H{"items": items, "hasMore": more, "unread": count})
	})
	g.POST("/read", func(c *gin.Context) {
		var body struct {
			ID  int64 `json:"id"`
			All bool  `json:"all"`
		}
		if c.ShouldBindJSON(&body) != nil || body.ID <= 0 {
			c.JSON(400, gin.H{"error": "通知编号无效"})
			return
		}
		if err := store.ReadWebsiteNotifications(c.Request.Context(), c.GetString("notification_user_id"), body.ID, body.All); err != nil {
			followError(c, err)
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})
}
