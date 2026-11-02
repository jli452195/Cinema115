package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/clouddrive2"
	"strm115/backend/internal/service"
)

func RegisterCD2(admin *gin.RouterGroup, s *service.CD2Connections) {
	admin.GET("/cd2", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, s.View())
	})
	for _, test := range []bool{false, true} {
		test := test
		endpoint := "/cd2"
		if test {
			endpoint += "/test"
		}
		admin.POST(endpoint, func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
			var input service.CD2Input
			if c.ShouldBindJSON(&input) != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "CloudDrive2 配置格式无效"})
				return
			}
			if test {
				result, err := s.Test(c.Request.Context(), input)
				if err != nil {
					cd2Error(c, err)
					return
				}
				c.JSON(http.StatusOK, result)
				return
			}
			view, err := s.Save(c.Request.Context(), input)
			if err != nil {
				cd2Error(c, err)
				return
			}
			c.JSON(http.StatusOK, view)
		})
	}
	admin.GET("/cd2/directories", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		revision, err := strconv.ParseUint(c.DefaultQuery("revision", "0"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "CloudDrive2 连接版本无效"})
			return
		}
		page, err := s.Directories(c.Request.Context(), c.DefaultQuery("path", "/"), revision)
		if err != nil {
			cd2Error(c, err)
			return
		}
		c.JSON(http.StatusOK, page)
	})
}

func cd2Error(c *gin.Context, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, clouddrive2.ErrInput):
		status = http.StatusBadRequest
	case errors.Is(err, clouddrive2.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, service.ErrCD2Changed), errors.Is(err, service.ErrCD2Unavailable):
		status = http.StatusConflict
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusRequestTimeout
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
