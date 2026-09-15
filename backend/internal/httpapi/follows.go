package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func followError(c *gin.Context, err error) {
	code, message := 500, "追更操作失败，请稍后重试"
	switch {
	case errors.Is(err, service.ErrFollowInput):
		code, message = 400, err.Error()
	case errors.Is(err, storage.ErrFollowConflict):
		code, message = 409, err.Error()
	case errors.Is(err, service.ErrFollowAvailable):
		code, message = 409, err.Error()
	case errors.Is(err, storage.ErrFollowLimit):
		code, message = 409, err.Error()
	case errors.Is(err, tmdb.ErrNotFound):
		code, message = 404, "未找到对应影视"
	case errors.Is(err, service.ErrFollowMetadata), errors.Is(err, service.ErrFollowLibrary):
		code, message = 502, err.Error()
	}
	c.JSON(code, gin.H{"error": message})
}
func RegisterFollows(group *gin.RouterGroup, s *service.FollowService) {
	group = group.Group("/follows")
	group.Use(func(c *gin.Context) {
		value, ok := c.Get("website_user")
		user, valid := value.(model.WebsiteUser)
		if !ok || !valid || user.ID == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "请先登录"})
			return
		}
		// 用户编号只取已认证身份，忽略查询或请求体中的其他用户编号。
		c.Set("follow_user_id", user.ID)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	group.GET("", func(c *gin.Context) {
		kind := c.Query("kind")
		offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if (kind != "" && kind != "movie" && kind != "tv") || err != nil || offset < 0 || offset > 1000000 {
			c.JSON(400, gin.H{"error": "列表参数无效"})
			return
		}
		items, more, err := s.List(c.Request.Context(), c.GetString("follow_user_id"), kind, offset)
		if err != nil {
			followError(c, err)
			return
		}
		c.JSON(200, gin.H{"items": items, "hasMore": more})
	})
	media := group.Group("/:kind/:id")
	media.Use(func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if !validEmbyMedia(c.Param("kind"), c.Param("id")) || err != nil || id > 2147483647 || strconv.Itoa(id) != c.Param("id") {
			c.AbortWithStatusJSON(400, gin.H{"error": "影视编号无效"})
			return
		}
		c.Set("follow_tmdb_id", id)
		c.Next()
	})
	media.GET("", func(c *gin.Context) {
		state, err := s.State(c.Request.Context(), c.GetString("follow_user_id"), c.Param("kind"), c.GetInt("follow_tmdb_id"))
		if err != nil {
			followError(c, err)
			return
		}
		c.JSON(200, state)
	})
	media.POST("", func(c *gin.Context) {
		state, err := s.Subscribe(c.Request.Context(), c.GetString("follow_user_id"), c.Param("kind"), c.GetInt("follow_tmdb_id"))
		if err != nil {
			followError(c, err)
			return
		}
		c.JSON(200, state)
	})
	media.POST("/cancel", func(c *gin.Context) {
		err := s.Store.CancelFollow(c.Request.Context(), c.GetString("follow_user_id"), c.Param("kind"), c.GetInt("follow_tmdb_id"))
		if err != nil {
			followError(c, err)
			return
		}
		c.JSON(200, gin.H{"subscribed": false})
	})
}
