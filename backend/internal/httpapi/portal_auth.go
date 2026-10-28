package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func portalAuthError(c *gin.Context, err error) {
	status, message := 500, "账号操作失败，请稍后重试"
	switch {
	case errors.Is(err, service.ErrLogin):
		status, message = 401, service.ErrLogin.Error()
	case errors.Is(err, service.ErrAuthBusy):
		status, message = 429, err.Error()
	case errors.Is(err, service.ErrAuthUnavailable):
		status, message = 503, service.ErrAuthUnavailable.Error()
	case errors.Is(err, service.ErrUserInput):
		status, message = 400, err.Error()
	case errors.Is(err, service.ErrMemberPlayback):
		status, message = 400, err.Error()
	case errors.Is(err, storage.ErrEmbyUserExists):
		status, message = 409, err.Error()
	case errors.Is(err, storage.ErrPortalInviteInvalid):
		status, message = 400, err.Error()
	case errors.Is(err, storage.ErrEmbyUserNotFound):
		status, message = 404, err.Error()
	}
	c.AbortWithStatusJSON(status, portalAuthPayload(c, status, message))
}

// Keep existing auth behavior; anime clients additionally get stable codes.
func portalAuthPayload(c *gin.Context, status int, message string) gin.H {
	out := gin.H{"error": message}
	if strings.HasPrefix(c.Request.URL.Path, "/api/admin/anime/") {
		switch status {
		case 401:
			out["code"] = "UNAUTHENTICATED"
		case 403:
			out["code"] = "ADMIN_REQUIRED"
		default:
			out["code"] = "AUTH_UNAVAILABLE"
		}
		out["details"] = gin.H{}
	}
	return out
}
func PortalAuthenticated(auth *service.PortalAuth) gin.HandlerFunc {
	return portalAuthenticated(auth, false)
}
func portalAuthenticated(auth *service.PortalAuth, renew bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(401, portalAuthPayload(c, 401, "请先登录"))
			return
		}
		value := strings.TrimPrefix(header, "Bearer ")
		var u model.PortalUser
		var renewed string
		var err error
		if renew {
			u, renewed, err = auth.Session(c.Request.Context(), value)
		} else {
			u, err = auth.Authenticate(c.Request.Context(), value)
		}
		if err != nil {
			portalAuthError(c, err)
			return
		}
		c.Set("portal_user", u)
		c.Set("portal_role", u.Role)
		if renewed != "" {
			c.Set("portal_token", renewed)
		}
		if u.Role == "admin" {
			c.Set("admin_username", u.Username)
		}
		c.Next()
	}
}
func PortalAdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("portal_role") != "admin" {
			c.AbortWithStatusJSON(403, portalAuthPayload(c, 403, "仅管理员可以使用此功能"))
			return
		}
		c.Next()
	}
}
func RegisterPortalAuth(r *gin.Engine, auth *service.PortalAuth) {
	registerEmbyRegistration(r, auth)
	// 单实例固定窗口限制匿名登录次数，不按任意用户名创建无限增长的记录。
	var mu sync.Mutex
	var window time.Time
	count := 0
	login := func(c *gin.Context) {
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
		login := auth.Login
		if c.FullPath() == "/api/auth/admin/login" {
			login = auth.LoginAdmin
		}
		token, u, err := login(c.Request.Context(), input.Username, input.Password)
		if err != nil {
			portalAuthError(c, err)
			return
		}
		c.JSON(200, gin.H{"token": token, "expiresIn": int(service.PortalSessionLifetime.Seconds()), "user": u})
	}
	r.POST("/api/auth/login", login)
	r.POST("/api/auth/admin/login", login)
	r.GET("/api/auth/me", portalAuthenticated(auth, true), func(c *gin.Context) {
		u, _ := c.Get("portal_user")
		out := gin.H{"user": u}
		if token := c.GetString("portal_token"); token != "" {
			out["token"] = token
		}
		c.JSON(200, out)
	})
}
func RegisterPortalInvites(admin *gin.RouterGroup, auth *service.PortalAuth) {
	registerPortalInvites(admin, auth)
}
