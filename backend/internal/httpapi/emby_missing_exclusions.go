package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func missingOperationError(c *gin.Context, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, service.ErrEmbyMissingInput) {
		code = http.StatusBadRequest
	} else if errors.Is(err, service.ErrEmbyMissingChanged) {
		code = http.StatusConflict
	}
	c.JSON(code, gin.H{"error": err.Error()})
}

func registerEmbyMissingExclusions(group *gin.RouterGroup, s *service.EmbyService) {
	group.GET("/missing/preview", func(c *gin.Context) {
		preview, err := s.MissingExclusionPreview(c.Request.Context(), c.Query("key"))
		if err != nil {
			missingOperationError(c, err)
			return
		}
		c.JSON(http.StatusOK, preview)
	})
	group.GET("/missing/exclusions", func(c *gin.Context) {
		offset := 0
		if raw, exists := c.GetQuery("offset"); exists {
			var err error
			offset, err = strconv.Atoi(raw)
			if err != nil {
				missingOperationError(c, service.ErrEmbyMissingInput)
				return
			}
		}
		page, err := s.MissingExclusions(c.Request.Context(), service.EmbyMissingQuery{Search: c.Query("q"), Offset: offset})
		if err != nil {
			missingOperationError(c, err)
			return
		}
		c.JSON(http.StatusOK, page)
	})
	group.POST("/missing/exclusions", func(c *gin.Context) {
		var input service.EmbyMissingExcludeInput
		if c.ShouldBindJSON(&input) != nil {
			missingOperationError(c, service.ErrEmbyMissingInput)
			return
		}
		count, err := s.ExcludeMissing(c.Request.Context(), input)
		if err != nil {
			missingOperationError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"excluded": count})
	})
	group.POST("/missing/restore", func(c *gin.Context) {
		var input service.EmbyMissingRestoreInput
		if c.ShouldBindJSON(&input) != nil {
			missingOperationError(c, service.ErrEmbyMissingInput)
			return
		}
		if err := s.RestoreMissing(c.Request.Context(), input); err != nil {
			missingOperationError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"restored": true})
	})
}
