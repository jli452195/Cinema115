package httpapi

import (
	"fmt"
	"io"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func readSubtitleUpload(c *gin.Context) (map[string]string, map[string][]byte, bool) {
	// 流式读取 multipart 并限制内存，浏览器提供的路径不会用于磁盘写入。
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
		if isFile {
			if _, exists := files[key]; exists {
				c.JSON(400, gin.H{"error": "字幕条目重复"})
				return nil, nil, false
			}
			if len(files) >= service.MaxSubtitleImportFiles {
				c.JSON(400, gin.H{"error": fmt.Sprintf("字幕不能超过 %d 个", service.MaxSubtitleImportFiles)})
				return nil, nil, false
			}
		}
		limit := int64(1024)
		if isFile {
			limit = p115.MaxSubtitleBytes
		}
		data, readErr := io.ReadAll(io.LimitReader(part, limit+1))
		_ = part.Close()
		if readErr != nil {
			c.JSON(400, gin.H{"error": "字幕请求内容不完整"})
			return nil, nil, false
		}
		if int64(len(data)) > limit {
			message := "字幕确认参数过长"
			if isFile {
				message = "单个字幕不能超过 16 MiB"
			}
			c.JSON(400, gin.H{"error": message})
			return nil, nil, false
		}
		if isFile {
			if len(data) == 0 {
				c.JSON(400, gin.H{"error": "字幕不能为空文件"})
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
