package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func RegisterMediaNaming(admin *gin.RouterGroup, s service.MediaNaming) {
	admin.POST("/naming/media-preview", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var input service.MediaNamingInput
		if !bindNamingJSON(c, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		out, err := s.Preview(ctx, input)
		if err != nil {
			organizeError(c, err)
			return
		}
		c.JSON(200, out)
	})
}
