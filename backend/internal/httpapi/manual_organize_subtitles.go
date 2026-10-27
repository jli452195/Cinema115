package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func registerOrganizeSubtitles(group *gin.RouterGroup, organizer *service.ManualOrganize, fail func(*gin.Context, error)) {
	group.POST("/subtitles/sources", func(c *gin.Context) {
		var input service.SubtitleSourcesInput
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "字幕来源参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
		defer cancel()
		result, err := organizer.PrepareSubtitleSources(ctx, input)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, result)
	})
	group.GET("/subtitles/sources/:sourceID/files/:fileID", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
		defer cancel()
		content, err := organizer.ReadSubtitleSource(ctx, c.Query("accountId"), c.Param("sourceID"), c.Param("fileID"))
		if err != nil {
			fail(c, err)
			return
		}
		defer content.Close()
		controller := http.NewResponseController(c.Writer)
		deadline, _ := ctx.Deadline()
		if err := controller.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
			fail(c, fmt.Errorf("设置字幕下载响应超时: %w", err))
			return
		}
		defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(200, "application/octet-stream", content.Data)
	})
	group.DELETE("/subtitles/sources/:sourceID", func(c *gin.Context) {
		organizer.ReleaseSubtitleSources(c.Query("accountId"), c.Param("sourceID"))
		c.Status(204)
	})
	group.GET("/subtitles", func(c *gin.Context) { c.JSON(200, gin.H{"jobs": organizer.SubtitleHistory()}) })
	group.GET("/subtitles/videos", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
		defer cancel()
		videos, err := organizer.SubtitleVideos(ctx, c.Query("accountId"), c.Query("jobId"))
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, gin.H{"videos": videos})
	})
	group.POST("/subtitles/preview", func(c *gin.Context) {
		var input service.SubtitlePreviewInput
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(400, gin.H{"error": "字幕预览参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
		defer cancel()
		plan, err := organizer.PreviewSubtitles(ctx, input)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, plan)
	})
	group.POST("/subtitles/import", func(c *gin.Context) {
		values, files, ok := readSubtitleUpload(c)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
		defer cancel()
		result, err := organizer.ImportSubtitles(ctx, values["accountId"], values["previewId"], files)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, result)
	})
}

func readSubtitleUpload(c *gin.Context) (map[string]string, map[string][]byte, bool) {
	// Stream multipart parsing into bounded memory; no browser paths reach disk.
	reader, err := c.Request.MultipartReader()
	if err != nil {
		c.JSON(400, gin.H{"error": "请选择字幕文件后重新预览"})
		return nil, nil, false
	}
	values := make(map[string]string, 2)
	files := make(map[string][]byte)
	total := 0
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			c.JSON(400, gin.H{"error": "字幕请求过大或内容不完整"})
			return nil, nil, false
		}
		key := part.FormName()
		isFile := part.FileName() != ""
		limit := int64(1024)
		if isFile {
			limit = p115.MaxSubtitleBytes
		}
		data, readErr := io.ReadAll(io.LimitReader(part, limit+1))
		_ = part.Close()
		if readErr != nil || int64(len(data)) > limit {
			c.JSON(400, gin.H{"error": "单个字幕不能超过 16 MiB，或请求内容不完整"})
			return nil, nil, false
		}
		if isFile {
			if _, exists := files[key]; exists || len(files) >= 100 {
				c.JSON(400, gin.H{"error": "字幕条目重复或超过 100 个"})
				return nil, nil, false
			}
			total += len(data)
			if total > service.MaxSubtitleImportBytes {
				c.JSON(400, gin.H{"error": "字幕总大小不能超过 64 MiB"})
				return nil, nil, false
			}
			files[key] = data
		} else {
			if _, exists := values[key]; exists || (key != "accountId" && key != "previewId") {
				c.JSON(400, gin.H{"error": "字幕确认参数无效"})
				return nil, nil, false
			}
			values[key] = string(data)
		}
	}
	return values, files, true
}
