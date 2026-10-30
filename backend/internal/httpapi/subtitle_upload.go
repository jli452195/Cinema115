package httpapi

import (
	"io"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

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
