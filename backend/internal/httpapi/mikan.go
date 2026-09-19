package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/service"
)

func RegisterMikan(admin *gin.RouterGroup, s *service.MikanService) {
	admin.POST("/mikan/search", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var input service.MikanInput
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "蜜柑搜索参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 70*time.Second)
		defer cancel()
		result, err := s.Search(ctx, input)
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, mikan.ErrInput) {
				status = http.StatusBadRequest
			}
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	})
}
