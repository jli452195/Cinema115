package httpapi

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"strm115/backend/internal/service"
	"sync"
	"time"
)

func registerWebsiteRegistration(r *gin.Engine, auth *service.WebsiteAuth) {
	var mu sync.Mutex
	var window time.Time
	count := 0
	guard := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		mu.Lock()
		if time.Since(window) >= time.Minute {
			window, count = time.Now(), 0
		}
		allowed := count < 30
		if allowed {
			count++
		}
		mu.Unlock()
		if !allowed {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(429, gin.H{"error": "注册尝试过于频繁，请稍后重试"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
	r.POST("/api/auth/invite/check", guard, func(c *gin.Context) {
		var input struct {
			Code string `json:"code"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "请输入邀请码"})
			return
		}
		if err := auth.CheckInvite(c.Request.Context(), input.Code); err != nil {
			websiteAuthError(c, err)
			return
		}
		c.JSON(200, gin.H{"valid": true})
	})
	r.POST("/api/auth/register", guard, func(c *gin.Context) {
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "注册信息格式无效"})
			return
		}
		u, err := auth.Register(c.Request.Context(), input.Username, input.Password, input.Code)
		if err != nil {
			websiteAuthError(c, err)
			return
		}
		c.JSON(201, gin.H{"user": u})
	})
}

func registerWebsiteInvites(admin *gin.RouterGroup, auth *service.WebsiteAuth) {
	group := admin.Group("/invites")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		c.Next()
	})
	group.GET("", func(c *gin.Context) {
		offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if err != nil || offset < 0 || offset > 1000000 {
			c.JSON(400, gin.H{"error": "分页参数无效"})
			return
		}
		invites, err := auth.DB.WebsiteInvites(c.Request.Context(), offset)
		if err != nil {
			websiteAuthError(c, err)
			return
		}
		hasMore := len(invites) > 50
		if hasMore {
			invites = invites[:50]
		}
		c.JSON(200, gin.H{"invites": invites, "hasMore": hasMore})
	})
	group.POST("", func(c *gin.Context) {
		input := struct {
			Count int    `json:"count"`
			Days  int    `json:"days"`
			Note  string `json:"note"`
		}{Count: 1, Days: 7}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "邀请配置格式无效"})
			return
		}
		invites, err := auth.GenerateInvites(c.Request.Context(), c.GetString("admin_username"), input.Note, input.Count, input.Days)
		if err != nil {
			websiteAuthError(c, err)
			return
		}
		c.JSON(201, gin.H{"invites": invites})
	})
	group.POST("/:id/revoke", func(c *gin.Context) {
		if err := auth.DB.RevokeWebsiteInvite(c.Request.Context(), c.Param("id")); err != nil {
			websiteAuthError(c, err)
			return
		}
		c.JSON(200, gin.H{"message": "邀请已撤销"})
	})
}
