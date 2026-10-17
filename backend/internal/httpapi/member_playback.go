package httpapi

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
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
		if errors.Is(err, service.ErrAccountChanged) {
			message, code = err.Error(), 409
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
		if c.GetString("portal_role") != "user" {
			c.AbortWithStatusJSON(403, gin.H{"error": "此入口供普通网站用户开通个人播放；管理员请使用个人播放管理"})
			return
		}
		c.Next()
	})
	userID := func(c *gin.Context) string { value, _ := c.Get("portal_user"); return value.(model.PortalUser).ID }
	group.GET("/children", func(c *gin.Context) {
		v, err := s.Children(c.Request.Context(), userID(c))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, v)
	})
	group.POST("/children", func(c *gin.Context) {
		var input service.ChildCreateInput
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "子账号参数无效"})
			return
		}
		if err := s.CreateChild(c.Request.Context(), userID(c), input); err != nil {
			fail(c, err)
			return
		}
		v, err := s.Children(c.Request.Context(), userID(c))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, v)
	})
	group.POST("/children/:childID", func(c *gin.Context) {
		var input service.ChildActionInput
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "子账号操作无效"})
			return
		}
		if err := s.ChildAction(c.Request.Context(), userID(c), c.Param("childID"), input); err != nil {
			fail(c, err)
			return
		}
		v, err := s.Children(c.Request.Context(), userID(c))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, v)
	})
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
		var input struct {
			Replace    bool   `json:"replace"`
			AccountID  string `json:"accountId"`
			Revision   uint64 `json:"revision"`
			ClientType string `json:"clientType"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "扫码参数无效"})
			return
		}
		qr, err := s.CreateQR(c.Request.Context(), userID(c), service.MemberQRInput{Replace: input.Replace, AccountID: input.AccountID, Revision: input.Revision, ClientType: input.ClientType})
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"qrId": qr.ID, "qrcode": qr.ImageURL, "expiresAt": qr.ExpiresAt})
	})
	group.GET("/qrcode/status", func(c *gin.Context) {
		accountID, present := c.GetQuery("accountId")
		// Empty is the explicit initial-login account reference.
		if !present {
			_, present = c.Request.URL.Query()["accountId"]
		}
		if !present || accountID != s.View(userID(c)).AccountID {
			fail(c, service.ErrAccountChanged)
			return
		}
		status, err := s.PollQR(c.Request.Context(), userID(c), c.Query("qrId"))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"status": status, "playback": s.View(userID(c))})
	})
	group.POST("/drive-change", func(c *gin.Context) {
		var input struct {
			Cancel bool `json:"cancel"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "参数无效"})
			return
		}
		if err := s.FinishDriveChange(c.Request.Context(), userID(c), input.Cancel); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, s.View(userID(c)))
	})
	group.POST("/disconnect", func(c *gin.Context) {
		if err := s.Suspend(c.Request.Context(), userID(c)); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, s.View(userID(c)))
	})
	group.POST("/directory", func(c *gin.Context) {
		var input service.MemberDirectoryInput
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "目录参数无效"})
			return
		}
		if err := s.SetPersonalDirectory(c.Request.Context(), userID(c), input); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, s.View(userID(c)))
	})
	group.GET("/directories", func(c *gin.Context) {
		offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
		revision, revisionErr := strconv.ParseUint(c.Query("revision"), 10, 64)
		if err != nil || revisionErr != nil {
			c.JSON(400, gin.H{"error": "目录参数无效"})
			return
		}
		page, err := s.PersonalDirectories(c.Request.Context(), userID(c), service.MemberDirectoryInput{AccountID: c.Query("accountId"), Revision: revision, DirectoryID: c.DefaultQuery("cid", "0")}, offset)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, page)
	})
	group.POST("/use-personal", func(c *gin.Context) {
		if err := s.UsePersonalDrive(c.Request.Context(), userID(c)); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, s.View(userID(c)))
	})
	manage := admin.Group("/member-playback", guard)
	registerMemberDeletion(manage, s, fail)
	registerMemberProxyPlayback(manage, s, fail)
	manage.GET("/device-policy", func(c *gin.Context) {
		c.JSON(200, s.UserPlayback.DevicePolicy())
	})
	manage.POST("/device-policy", func(c *gin.Context) {
		var input model.PlaybackDevicePolicy
		if c.ShouldBindJSON(&input) != nil || input.Mode == "" || input.SilenceSeconds == 0 {
			c.JSON(400, gin.H{"error": "播放设备策略参数无效"})
			return
		}
		if err := s.UserPlayback.ConfigureDevicePolicy(c.Request.Context(), input); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, s.UserPlayback.DevicePolicy())
	})
	manage.POST("/refresh", func(c *gin.Context) {
		s.RequestDriveRefresh()
		c.JSON(202, gin.H{"members": s.DriveStatuses()})
	})
	manage.GET("/drive-status", func(c *gin.Context) {
		c.JSON(200, gin.H{"members": s.DriveStatuses()})
	})
	manage.GET("/settings", func(c *gin.Context) {
		var proxy service.EmbyProxyStatus
		if s.ProxyStatus != nil {
			proxy = s.ProxyStatus()
		}
		c.JSON(200, gin.H{"settings": s.Settings(), "proxy": proxy})
	})
	manage.POST("/:id/settings", func(c *gin.Context) {
		var in service.MemberEditInput
		if c.ShouldBindJSON(&in) != nil {
			c.JSON(400, gin.H{"error": "用户设置无效"})
			return
		}
		if err := s.EditMember(c.Request.Context(), c.Param("id"), in); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"settings": s.Settings(), "members": s.AdminMembers()})
	})
	manage.POST("/:id/child-limit", func(c *gin.Context) {
		var input struct {
			Limit *int `json:"limit"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Limit == nil {
			c.JSON(400, gin.H{"error": "请填写子账号配额"})
			return
		}
		if err := s.SetChildLimit(c.Request.Context(), c.Param("id"), *input.Limit); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"settings": s.Settings(), "members": s.AdminMembers()})
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
	manage.GET("/playback-sessions", func(c *gin.Context) {
		c.JSON(200, gin.H{"sessions": s.UserPlayback.List(), "devicePolicy": s.UserPlayback.DevicePolicy(), "serverTime": time.Now().UnixMilli()})
	})
	manage.POST("/playback-sessions/:key/release", func(c *gin.Context) {
		var input struct {
			Generation uint64 `json:"generation"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Generation == 0 {
			c.JSON(400, gin.H{"error": "播放会话版本无效"})
			return
		}
		if err := s.ReleaseUserPlayback(c.Request.Context(), c.Param("key"), input.Generation); err != nil {
			if errors.Is(err, service.ErrAccountChanged) {
				c.JSON(409, gin.H{"error": "播放会话已变化，请刷新后重试"})
			} else {
				c.JSON(500, gin.H{"error": "播放占用释放未确认，请刷新检查后重试"})
			}
			return
		}
		c.JSON(200, gin.H{"sessions": s.UserPlayback.List(), "serverTime": time.Now().UnixMilli()})
	})
	manage.GET("/pool", func(c *gin.Context) {
		c.JSON(200, gin.H{"settings": s.Pool.Settings(), "accounts": s.AccountOptions(), "metrics": s.Pool.Metrics(), "api": s.PoolRequests.Stats(), "events": s.Pool.Events()})
	})
	manage.POST("/pool", func(c *gin.Context) {
		var input model.PlaybackPoolSettings
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "池设置无效"})
			return
		}
		if err := s.ConfigurePool(c.Request.Context(), input); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"settings": s.Pool.Settings()})
	})
	manage.POST("/pool/:accountID", func(c *gin.Context) {
		var input struct {
			Mode string `json:"mode"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "池状态无效"})
			return
		}
		found := false
		for _, a := range s.AccountOptions() {
			if a.ID == c.Param("accountID") {
				found = true
				break
			}
		}
		if !found {
			c.JSON(404, gin.H{"error": "池账号不存在"})
			return
		}
		if err := s.Pool.SetMode(c.Request.Context(), c.Param("accountID"), input.Mode); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"accounts": s.AccountOptions()})
	})
	manage.POST("/pool/:accountID/check", func(c *gin.Context) {
		if err := s.CheckPoolAccount(c.Request.Context(), c.Param("accountID")); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"accounts": s.AccountOptions()})
	})
	manage.POST("/:id/sync", func(c *gin.Context) {
		if err := s.CheckUserPlayback(c.Request.Context(), c.Param("id")); err != nil {
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
	manage.POST("/registration-settings", func(c *gin.Context) {
		var input struct {
			LibraryIDs []string `json:"libraryIds"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "注册媒体库设置无效"})
			return
		}
		if err := s.SetRegistrationLibraries(c.Request.Context(), input.LibraryIDs); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"settings": s.Settings()})
	})
	manage.POST("", func(c *gin.Context) {
		var settings model.MemberPlaybackSettings
		if c.ShouldBindJSON(&settings) != nil {
			c.JSON(400, gin.H{"error": "配置格式无效"})
			return
		}
		if err := s.SetPlaybackSettings(c.Request.Context(), settings.AllowDownload, settings.PublicURL); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"settings": s.Settings(), "members": s.AdminMembers()})
	})

}
