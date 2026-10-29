package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/mediacategory"
	"strm115/backend/internal/service"
)

func RegisterMediaClassification(admin *gin.RouterGroup, settings *service.MediaClassification) {
	g := admin.Group("/classification")
	g.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	g.GET("", func(c *gin.Context) {
		document, err := settings.Read(c.Request.Context())
		if err != nil {
			classificationError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"record": document, "defaultYAML": mediacategory.DefaultYAML()})
	})
	g.PUT("", func(c *gin.Context) {
		var input struct {
			YAML     *string `json:"yaml"`
			Revision *string `json:"revision"`
		}
		if err := decodeStrictJSON(c.Request.Body, &input); err != nil {
			var sizeError *http.MaxBytesError
			if errors.As(err, &sizeError) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "分类配置请求过大", "code": "REQUEST_TOO_LARGE"})
			} else {
				c.JSON(http.StatusBadRequest, gin.H{"error": "分类配置请求格式无效", "code": "INVALID_CLASSIFICATION"})
			}
			return
		}
		if input.YAML == nil || input.Revision == nil {
			classificationError(c, mediacategory.ErrConfig)
			return
		}
		document, err := settings.Save(c.Request.Context(), *input.YAML, *input.Revision)
		if err != nil {
			classificationError(c, err)
			return
		}
		c.JSON(http.StatusOK, document)
	})
	g.POST("/reload", func(c *gin.Context) {
		document, err := settings.Reload(c.Request.Context())
		if err != nil {
			classificationError(c, err)
			return
		}
		c.JSON(http.StatusOK, document)
	})
}

func classificationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrClassificationConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "REVISION_CONFLICT"})
	case errors.Is(err, mediacategory.ErrConfig):
		var diagnostic *mediacategory.ConfigError
		errors.As(err, &diagnostic)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID_CLASSIFICATION", "diagnostic": diagnostic})
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		c.JSON(http.StatusRequestTimeout, gin.H{"error": "分类配置操作已取消或超时，请重新读取确认结果", "code": "REQUEST_TIMEOUT"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法读取或保存分类规则文件，请检查数据目录后重试", "code": "CLASSIFICATION_UNAVAILABLE"})
	}
}
