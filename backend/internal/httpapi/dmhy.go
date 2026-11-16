package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/dmhy"
	"strm115/backend/internal/service"
)

func RegisterDmhy(admin *gin.RouterGroup, s *service.DmhyService) {
	r := admin.Group("")
	var logs *service.GenerationLogs
	if s != nil {
		logs = s.Logs
	}
	r.Use(operationLogs(logs, "搜索", "动漫花园", map[string]string{
		r.BasePath() + "/dmhy/search": "种子搜索",
		r.BasePath() + "/dmhy/magnet": "读取原帖磁力",
	}))
	r.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if s == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "动漫花园暂不可用"})
			return
		}
		c.Next()
	})
	r.POST("/dmhy/search", func(c *gin.Context) {
		var input service.DmhySearchInput
		if !decodeDmhyInput(c, &input) {
			return
		}
		result, err := s.Search(c.Request.Context(), input)
		if err != nil {
			dmhyRequestError(c, err)
			return
		}
		c.Set("operationLogSummary", fmt.Sprintf("第 %d 页，返回 %d 条资源", result.Page, len(result.Items)))
		if result.Cached {
			c.Set("operationLogCached", true)
			c.Set("operationLogSummary", fmt.Sprintf("命中缓存，第 %d 页，返回 %d 条资源", result.Page, len(result.Items)))
		}
		c.JSON(http.StatusOK, result)
	})
	r.POST("/dmhy/magnet", func(c *gin.Context) {
		var input struct {
			ID string `json:"id"`
		}
		if !decodeDmhyInput(c, &input) {
			return
		}
		result, err := s.ResolveMagnet(c.Request.Context(), input.ID)
		if err != nil {
			dmhyRequestError(c, err)
			return
		}
		c.Set("operationLogSummary", "已读取原帖磁力")
		if result.Cached {
			c.Set("operationLogCached", true)
			c.Set("operationLogSummary", "命中缓存，已读取原帖磁力")
		}
		c.JSON(http.StatusOK, result)
	})
}

func decodeDmhyInput(c *gin.Context, input any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(input) != nil || decoder.Decode(new(any)) != io.EOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": "动漫花园请求参数无效"})
		return false
	}
	return true
}

func dmhyRequestError(c *gin.Context, err error) {
	status, message := http.StatusBadGateway, "动漫花园请求失败，请稍后重试"
	switch {
	case errors.Is(err, dmhy.ErrInput):
		status, message = http.StatusBadRequest, "动漫花园请求参数无效"
	case errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusGatewayTimeout, "动漫花园请求超时，请稍后重试"
	case errors.Is(err, context.Canceled):
		status, message = http.StatusRequestTimeout, "动漫花园请求已取消"
	case errors.Is(err, dmhy.ErrBlocked):
		message = dmhy.ErrBlocked.Error()
	case errors.Is(err, dmhy.ErrParse):
		message = dmhy.ErrParse.Error()
	case errors.Is(err, dmhy.ErrNetwork):
		message = dmhy.ErrNetwork.Error()
	}
	var upstream *animefeed.HTTPError
	if errors.As(err, &upstream) {
		if upstream.Status == http.StatusTooManyRequests || upstream.Status == http.StatusServiceUnavailable && !upstream.RetryAt.IsZero() {
			status, message = http.StatusTooManyRequests, "动漫花园请求过于频繁，请稍后重试"
			seconds := int64(time.Until(upstream.RetryAt).Seconds()) + 1
			if seconds < 1 {
				seconds = 60
			}
			c.Header("Retry-After", strconv.FormatInt(seconds, 10))
		}
	}
	c.JSON(status, gin.H{"error": message})
}
