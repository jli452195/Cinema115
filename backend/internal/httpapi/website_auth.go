package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func websiteAuthError(c *gin.Context, err error) {
	status, message := 500, "账号操作失败，请稍后重试"
	switch {
	case errors.Is(err, service.ErrLogin):
		status, message = 401, service.ErrLogin.Error()
	case errors.Is(err, service.ErrAuthBusy):
		status, message = 429, err.Error()
	case errors.Is(err, service.ErrUserInput):
		status, message = 400, err.Error()
	case errors.Is(err, service.ErrMemberPlayback):
		status, message = 400, err.Error()
	case errors.Is(err, storage.ErrWebsiteUserExists):
		status, message = 409, err.Error()
	case errors.Is(err, storage.ErrWebsiteInviteInvalid):
		status, message = 400, err.Error()
	case errors.Is(err, storage.ErrWebsiteUserNotFound):
		status, message = 404, err.Error()
	}
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}
func WebsiteAuthenticated(auth *service.WebsiteAuth) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(401, gin.H{"error": "请先登录"})
			return
		}
		u, err := auth.Authenticate(c.Request.Context(), strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			websiteAuthError(c, err)
			return
		}
		c.Set("website_user", u)
		c.Set("website_role", u.Role)
		if u.Role == "admin" {
			c.Set("admin_username", u.Username)
		}
		c.Next()
	}
}
func WebsiteAdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("website_role") != "admin" {
			c.AbortWithStatusJSON(403, gin.H{"error": "仅管理员可以使用此功能"})
			return
		}
		c.Next()
	}
}
func RegisterWebsiteAuth(r *gin.Engine, auth *service.WebsiteAuth) {
	registerWebsiteRegistration(r, auth)
	// 单实例固定窗口限制匿名登录次数，不按任意用户名创建无限增长的记录。
	var mu sync.Mutex
	var window time.Time
	count := 0
	r.POST("/api/auth/login", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		mu.Lock()
		if time.Since(window) >= time.Minute {
			window = time.Now()
			count = 0
		}
		allowed := count < 30
		if allowed {
			count++
		}
		mu.Unlock()
		if !allowed {
			c.Header("Retry-After", "60")
			c.JSON(429, gin.H{"error": "登录尝试过于频繁，请稍后重试"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "请输入账号和密码"})
			return
		}
		token, u, err := auth.Login(c.Request.Context(), input.Username, input.Password)
		if err != nil {
			websiteAuthError(c, err)
			return
		}
		c.JSON(200, gin.H{"token": token, "expiresIn": 86400, "user": u})
	})
	r.GET("/api/auth/me", WebsiteAuthenticated(auth), func(c *gin.Context) { u, _ := c.Get("website_user"); c.JSON(200, gin.H{"user": u}) })
}
func RegisterWebsiteUsers(admin *gin.RouterGroup, auth *service.WebsiteAuth) {
	registerWebsiteInvites(admin, auth)
	group := admin.Group("/users")
	registerWebsiteCleanup(group, auth)
	group.Use(func(c *gin.Context) { c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096); c.Next() })
	group.GET("", func(c *gin.Context) {
		offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if err != nil || offset < 0 || offset > 1000000 {
			c.JSON(400, gin.H{"error": "分页参数无效"})
			return
		}
		users, err := auth.DB.WebsiteUsers(c.Request.Context(), offset)
		if err != nil {
			websiteAuthError(c, err)
			return
		}
		hasMore := len(users) > 50
		if hasMore {
			users = users[:50]
		}
		c.JSON(200, gin.H{"users": users, "hasMore": hasMore})
	})
	group.POST("", func(c *gin.Context) {
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "用户配置格式无效"})
			return
		}
		u, err := auth.Create(c.Request.Context(), input.Username, input.Password)
		if err != nil {
			websiteAuthError(c, err)
			return
		}
		c.JSON(201, gin.H{"user": u})
	})
	group.POST("/:id", func(c *gin.Context) {
		var input struct {
			Password string `json:"password"`
			Disabled *bool  `json:"disabled"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "用户配置格式无效"})
			return
		}
		if err := auth.Update(c.Request.Context(), c.Param("id"), input.Password, input.Disabled); err != nil {
			websiteAuthError(c, err)
			return
		}
		c.JSON(200, gin.H{"message": "用户已更新，原登录凭证已失效"})
	})
}
