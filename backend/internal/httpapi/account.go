package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type CookieChecker interface {
	CheckCookie(context.Context, string) (bool, error)
}

// RegisterAccount 将敏感 Cookie 操作限制在已有 JWT 鉴权的管理员路由组。
func RegisterAccount(admin *gin.RouterGroup, value func() string, save func(string) error, checker CookieChecker) {
	admin.GET("/p115/status", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		cookie := value()
		if cookie == "" {
			c.JSON(200, gin.H{"configured": false, "connected": false})
			return
		}
		valid, err := checker.CheckCookie(c.Request.Context(), cookie)
		if err != nil {
			c.JSON(502, gin.H{"configured": true, "error": "暂时无法验证已保存的 Cookie，请重试"})
			return
		}
		c.JSON(200, gin.H{"configured": true, "connected": valid})
	})
	admin.POST("/p115/login", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var req struct {
			Cookie string `json:"cookie"`
		}
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(400, gin.H{"error": "Cookie 请求格式不正确"})
			return
		}
		cookie := strings.TrimSpace(req.Cookie)
		if cookie == "" {
			c.JSON(400, gin.H{"error": "请填写新的 Cookie，空值不会覆盖现有配置"})
			return
		}
		valid, err := checker.CheckCookie(c.Request.Context(), cookie)
		if err != nil {
			c.JSON(502, gin.H{"error": "暂时无法验证 Cookie，原配置未修改"})
			return
		}
		if !valid {
			c.JSON(422, gin.H{"error": "Cookie 无效或已过期，原配置未修改"})
			return
		}
		if err = save(cookie); err != nil {
			c.JSON(500, gin.H{"error": "Cookie 有效，但保存失败，请重试"})
			return
		}
		c.JSON(200, gin.H{"configured": true, "connected": true, "message": "Cookie 验证通过并已保存"})
	})
	admin.DELETE("/p115/login", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if err := save(""); err != nil {
			c.JSON(500, gin.H{"error": "清除登录会话失败"})
			return
		}
		c.Status(http.StatusNoContent)
	})
	admin.GET("/p115/cookie", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"cookie": value()})
	})
	admin.POST("/p115/cookie/test", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var req struct {
			Cookie string `json:"cookie"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Cookie 请求格式不正确"})
			return
		}
		cookie := strings.TrimSpace(req.Cookie)
		if cookie == "" {
			cookie = value()
		}
		if cookie == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请先扫码登录或填写 Cookie"})
			return
		}
		valid, err := checker.CheckCookie(c.Request.Context(), cookie)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "暂时无法验证 Cookie，请稍后重试"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"valid": valid})
	})
}
