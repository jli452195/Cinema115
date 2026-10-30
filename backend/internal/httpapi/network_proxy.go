package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/netproxy"
	"strm115/backend/internal/service"
)

func RegisterNetworkProxy(admin *gin.RouterGroup, s *service.NetworkProxy) {
	admin.GET("/network-proxy", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, s.View())
	})
	admin.POST("/network-proxy", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var input service.NetworkProxyInput
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "网络代理配置格式无效"})
			return
		}
		view, err := s.Save(c.Request.Context(), input)
		if err != nil {
			networkProxyError(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	})
	admin.POST("/network-proxy/test", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var input struct {
			service.NetworkProxyInput
			Service string `json:"service"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "网络代理测试参数无效"})
			return
		}
		result, err := s.Test(c.Request.Context(), input.NetworkProxyInput, input.Service)
		if err != nil {
			networkProxyError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	})
}

func networkProxyError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, netproxy.ErrConfig) {
		status = http.StatusBadRequest
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusRequestTimeout
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
