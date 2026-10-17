package httpapi

import (
	"errors"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func registerMemberProxyPlayback(admin *gin.RouterGroup, s *service.MemberPlaybackService, fail func(*gin.Context, error)) {
	routes := admin.Group("/:id/proxy-playback", PortalAdminOnly())
	routes.GET("", func(c *gin.Context) {
		state, err := s.ReadMemberProxyPlayback(c.Request.Context(), c.Param("id"))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"playback": state})
	})
	routes.POST("", func(c *gin.Context) {
		var in struct {
			Enabled          *bool   `json:"enabled"`
			ExpectedRevision *uint64 `json:"expectedRevision"`
		}
		if c.ShouldBindJSON(&in) != nil || in.Enabled == nil || in.ExpectedRevision == nil {
			c.JSON(400, gin.H{"error": "请提供反代播放状态与当前设置版本"})
			return
		}
		state, err := s.SetMemberProxyPlayback(c.Request.Context(), c.Param("id"), *in.Enabled, *in.ExpectedRevision)
		if errors.Is(err, service.ErrProxyPlaybackConflict) {
			c.JSON(409, gin.H{"error": "反代播放设置已变化，请核实后重试", "playback": state})
			return
		}
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"playback": state})
	})
}
