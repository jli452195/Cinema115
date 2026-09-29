package httpapi

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"time"
)

func RegisterMemberPlayback(member, admin *gin.RouterGroup, s *service.MemberPlaybackService) {
	fail := func(c *gin.Context, err error) {
		message := "个人播放操作失败，请稍后重试"
		code := 500
		if errors.Is(err, service.ErrMemberPlayback) {
			message = err.Error()
			code = 400
		}
		c.JSON(code, gin.H{"error": message})
	}
	guard := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
	group := member.Group("/playback", guard)
	group.Use(func(c *gin.Context) {
		if c.GetString("website_role") != "user" {
			c.AbortWithStatusJSON(403, gin.H{"error": "此入口供普通网站用户开通个人播放；管理员请使用个人播放管理"})
			return
		}
		c.Next()
	})
	userID := func(c *gin.Context) string { value, _ := c.Get("website_user"); return value.(model.WebsiteUser).ID }
	group.GET("", func(c *gin.Context) {
		view, err := s.CurrentView(c.Request.Context(), userID(c))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, view)
	})
	group.POST("/refresh", func(c *gin.Context) {
		if err := s.SyncUser(c.Request.Context(), userID(c)); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, s.View(userID(c)))
	})
	group.POST("/qrcode", func(c *gin.Context) {
		qr, err := s.CreateQR(c.Request.Context(), userID(c))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"qrId": qr.ID, "qrcode": qr.ImageURL, "expiresAt": qr.ExpiresAt})
	})
	group.GET("/qrcode/status", func(c *gin.Context) {
		status, err := s.PollQR(c.Request.Context(), userID(c), c.Query("qrId"))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"status": status, "playback": s.View(userID(c))})
	})
	group.POST("/activate", func(c *gin.Context) {
		var input struct {
			Password string  `json:"password"`
			Username string  `json:"username"`
			Revision *uint64 `json:"revision"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Revision == nil {
			c.JSON(400, gin.H{"error": "请输入 Emby 密码"})
			return
		}
		if err := s.RegisterAccount(c.Request.Context(), userID(c), input.Username, input.Password, *input.Revision); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, s.View(userID(c)))
	})
	for _, operation := range []string{"username", "password"} {
		group.POST("/"+operation, func(c *gin.Context) {
			var input struct {
				Username string  `json:"username"`
				Password string  `json:"password"`
				Revision *uint64 `json:"revision"`
			}
			if c.ShouldBindJSON(&input) != nil || input.Revision == nil {
				c.JSON(400, gin.H{"error": "请输入修改内容并刷新账号版本"})
				return
			}
			var err error
			if operation == "username" {
				err = s.RenameAccount(c.Request.Context(), userID(c), input.Username, *input.Revision)
			} else {
				err = s.ChangePassword(c.Request.Context(), userID(c), input.Password, *input.Revision)
			}
			if err != nil {
				fail(c, err)
				return
			}
			c.JSON(200, s.View(userID(c)))
		})
	}
	group.POST("/disconnect", func(c *gin.Context) {
		if err := s.Suspend(c.Request.Context(), userID(c)); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, s.View(userID(c)))
	})
	group.POST("/use-personal", func(c *gin.Context) {
		if err := s.UsePersonalDrive(c.Request.Context(), userID(c)); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, s.View(userID(c)))
	})
	manage := admin.Group("/member-playback", guard)
	manage.GET("/adoption/:embyID", func(c *gin.Context) {
		preview, err := s.AdoptionPreview(c.Request.Context(), c.Param("embyID"))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, preview)
	})
	manage.POST("/adoption/:embyID", func(c *gin.Context) {
		var input struct {
			Fingerprint   string `json:"fingerprint"`
			WebsiteUserID string `json:"websiteUserId"`
			Invite        bool   `json:"invite"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Fingerprint == "" {
			c.JSON(400, gin.H{"error": "请先检查并确认关联预览"})
			return
		}
		if input.Invite {
			invite, err := s.CreateAdoptionInvite(c.Request.Context(), c.Param("embyID"), input.Fingerprint, c.GetString("admin_username"))
			if err != nil {
				fail(c, err)
				return
			}
			c.JSON(201, gin.H{"invite": invite})
			return
		}
		if err := s.LinkExistingUser(c.Request.Context(), c.Param("embyID"), input.WebsiteUserID, input.Fingerprint); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"linked": true})
	})
	manage.GET("/users", func(c *gin.Context) {
		members, err := s.AdminMemberDirectory(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"settings": s.Settings(), "members": members})
	})
	manage.GET("/accounts", func(c *gin.Context) { c.JSON(200, gin.H{"accounts": s.AccountOptions()}) })
	manage.POST("/:id/sync", func(c *gin.Context) {
		if err := s.SyncUser(c.Request.Context(), c.Param("id")); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"settings": s.Settings(), "members": s.AdminMembers()})
	})
	manage.POST("/:id/source", func(c *gin.Context) {
		var input struct {
			Source    string `json:"source"`
			AccountID string `json:"accountId"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "播放方式格式无效"})
			return
		}
		if err := s.SetSource(c.Request.Context(), c.Param("id"), input.Source, input.AccountID); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"settings": s.Settings(), "members": s.AdminMembers()})
	})
	manage.GET("", func(c *gin.Context) { c.JSON(200, gin.H{"settings": s.Settings(), "members": s.AdminMembers()}) })
	manage.GET("/libraries", func(c *gin.Context) {
		libraries, err := s.Libraries(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"libraries": libraries})
	})
	manage.POST("/sync", func(c *gin.Context) {
		s.RequestSync()
		c.JSON(202, gin.H{"settings": s.Settings(), "members": s.AdminMembers()})
	})
	manage.POST("", func(c *gin.Context) {
		var settings model.MemberPlaybackSettings
		if c.ShouldBindJSON(&settings) != nil {
			c.JSON(400, gin.H{"error": "配置格式无效"})
			return
		}
		if err := s.SetSettings(c.Request.Context(), settings); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"settings": s.Settings(), "members": s.AdminMembers()})
	})
	manage.POST("/:id", func(c *gin.Context) {
		var input struct {
			Blocked     *bool  `json:"blocked"`
			DirectoryID string `json:"directoryId"`
			Retry       bool   `json:"retry"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "操作格式无效"})
			return
		}
		if err := s.AdminUpdate(c.Request.Context(), c.Param("id"), input.Blocked, input.DirectoryID, input.Retry); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"settings": s.Settings(), "members": s.AdminMembers()})
	})
}
