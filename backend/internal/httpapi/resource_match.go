package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

// RegisterResourceMatch 暴露一个批量入口；调用方不需要分别读取缺集后再自行判断标题。
func RegisterResourceMatch(group *gin.RouterGroup, matcher service.ResourceMatcher) {
	group.POST("/library/:kind/:id/resource-match", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
		kind := c.Param("kind")
		if kind != "movie" && kind != "tv" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "影视类型无效"})
			return
		}
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "TMDB ID 无效"})
			return
		}
		var input struct {
			Resources []service.ResourceCandidate `json:"resources"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
			return
		}
		if len(input.Resources) == 0 || len(input.Resources) > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "每次需要判断 1 到 100 个资源"})
			return
		}
		for _, resource := range input.Resources {
			if len(resource.ID) > 128 || strings.TrimSpace(resource.Title) == "" || len([]rune(resource.Title)) > 1000 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "资源编号或标题格式无效"})
				return
			}
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
		defer cancel()
		result, matchErr := matcher.Match(ctx, kind, id, input.Resources)
		if matchErr != nil && result.Status != "unavailable" {
			c.JSON(http.StatusBadGateway, gin.H{"error": "资源匹配暂时不可用"})
			return
		}
		c.JSON(http.StatusOK, result)
	})
}
