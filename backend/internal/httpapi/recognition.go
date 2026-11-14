package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/recognition"
	"strm115/backend/internal/service"
)

func RegisterRecognition(admin *gin.RouterGroup, settings *service.RecognitionSettings) {
	g := admin.Group("/recognition")
	g.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	g.GET("", func(c *gin.Context) {
		record, err := settings.Read(c.Request.Context())
		if err != nil {
			recognitionError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"record": record})
	})
	g.PUT("", func(c *gin.Context) {
		var in struct {
			Groups    *string `json:"groups"`
			Platforms *string `json:"platforms"`
		}
		if !bindRecognition(c, &in) {
			return
		}
		if in.Groups == nil || in.Platforms == nil {
			recognitionError(c, recognition.ErrConfig)
			return
		}
		record, err := settings.Save(c.Request.Context(), *in.Groups, *in.Platforms)
		if err != nil {
			recognitionError(c, err)
			return
		}
		c.JSON(http.StatusOK, record)
	})
	g.POST("/reload", func(c *gin.Context) {
		record, err := settings.Reload(c.Request.Context())
		if err != nil {
			recognitionError(c, err)
			return
		}
		c.JSON(http.StatusOK, record)
	})
	g.POST("/preview", func(c *gin.Context) {
		var in struct {
			Groups    *string  `json:"groups"`
			Platforms *string  `json:"platforms"`
			Items     []string `json:"items"`
		}
		if !bindRecognition(c, &in) {
			return
		}
		if in.Groups == nil || in.Platforms == nil || len(in.Items) == 0 || len(in.Items) > 100 {
			recognitionError(c, recognition.ErrInput)
			return
		}
		snapshot, err := recognition.Compile(*in.Groups, *in.Platforms)
		if err != nil {
			recognitionError(c, err)
			return
		}
		type item struct {
			Name string `json:"name"`
			recognition.Result
		}
		out := make([]item, 0, len(in.Items))
		for _, name := range in.Items {
			if err := c.Request.Context().Err(); err != nil {
				recognitionError(c, err)
				return
			}
			result, err := snapshot.Recognize(name)
			if err != nil {
				recognitionError(c, err)
				return
			}
			out = append(out, item{Name: name, Result: result})
		}
		c.JSON(http.StatusOK, gin.H{"items": out, "revision": snapshot.Revision})
	})
}

func bindRecognition(c *gin.Context, in any) bool {
	if err := decodeStrictJSON(c.Request.Body, in); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "识别词表请求过大", "code": "REQUEST_TOO_LARGE"})
		} else {
			recognitionError(c, recognition.ErrInput)
		}
		return false
	}
	return true
}

func recognitionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, recognition.ErrConfig), errors.Is(err, recognition.ErrInput):
		var diagnostic *recognition.Diagnostic
		errors.As(err, &diagnostic)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID_RECOGNITION", "diagnostic": diagnostic})
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		c.JSON(http.StatusRequestTimeout, gin.H{"error": "词表操作已取消或超时，请重新读取确认结果", "code": "REQUEST_TIMEOUT"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法读取或保存识别词表，请检查数据目录后重试", "code": "RECOGNITION_UNAVAILABLE"})
	}
}
