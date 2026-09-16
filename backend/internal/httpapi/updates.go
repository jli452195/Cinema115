package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

type RecentUpdatesReader interface {
	List(context.Context, service.RecentUpdatesQuery) (service.RecentUpdatesPage, error)
}

func RegisterRecentUpdates(group *gin.RouterGroup, updates RecentUpdatesReader) {
	group.GET("/updates", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		defer cancel()
		page, err := updates.List(ctx, service.RecentUpdatesQuery{
			Range: c.Query("range"), Date: c.Query("date"), Cursor: c.Query("cursor"),
		})
		if errors.Is(err, service.ErrRecentUpdatesInput) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "最近更新暂时不可用"})
			return
		}
		c.JSON(http.StatusOK, page)
	})
}
