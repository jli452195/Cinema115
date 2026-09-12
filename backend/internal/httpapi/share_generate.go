package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func (a *ShareAPI) generationConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	root, err := filepath.Abs(a.OutputRoot)
	if err != nil {
		c.JSON(500, gin.H{"error": "无法解析媒体库根目录"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"baseUrl": a.configuredBaseURL(), "videoExtensions": service.DefaultVideoExtensions, "outputDir": "strm", "outputRoot": filepath.ToSlash(root), "directories": []string{"strm"}})
}

// 网页保存的地址实时供网页、单文件生成和机器人使用，无需重启。
func (a *ShareAPI) configuredBaseURL() string {
	if a.BaseURL == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(a.BaseURL()), "/")
}

func (a *ShareAPI) generationBaseURL(c *gin.Context) string {
	if base := a.configuredBaseURL(); base != "" {
		return base
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}

func (a *ShareAPI) generate(c *gin.Context) {
	a.runGenerate(c, false)
}

func (a *ShareAPI) generateStream(c *gin.Context) {
	a.runGenerate(c, true)
}

func (a *ShareAPI) runGenerate(c *gin.Context, streaming bool) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var input service.GenerateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "生成请求格式不正确"})
		return
	}
	if input.BaseURL == "" {
		input.BaseURL = a.generationBaseURL(c)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	respond := func(code int, data gin.H) { c.JSON(code, data) }
	if streaming {
		c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
		c.Header("X-Accel-Buffering", "no")
		encoder := json.NewEncoder(c.Writer)
		emit := func(event string, data any) {
			if err := encoder.Encode(gin.H{"event": event, "data": data}); err != nil {
				cancel()
				return
			}
			c.Writer.Flush()
		}
		emit("progress", service.GenerationProgress{Stage: "starting", Message: "任务已接收，正在准备；单次处理最多 5 分钟"})
		ctx = service.WithGenerationProgress(ctx, func(progress service.GenerationProgress) { emit("progress", progress) })
		respond = func(code int, data gin.H) {
			if code >= 400 {
				data["status"] = code
				emit("error", data)
				return
			}
			emit("result", data)
		}
	}
	var generator service.MediaGenerator = &service.STRMGenerator{Client: a.Client, Cookie: a.Cookie, Root: a.OutputRoot}
	if a.Workflow != nil {
		generator = a.Workflow
	}
	result, err := generator.Generate(ctx, input)
	if err != nil {
		code := http.StatusBadGateway
		var fileErr *os.PathError
		if errors.As(err, &fileErr) {
			code = http.StatusInternalServerError
		}
		if errors.Is(err, service.ErrGenerationInput) {
			code = http.StatusBadRequest
		}
		if errors.Is(err, service.ErrNoMedia) {
			code = http.StatusUnprocessableEntity
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			response := gin.H{"error": "处理超过 5 分钟：" + err.Error()}
			if result != nil {
				response["reshareUrl"] = result.ReshareURL
				response["tempDirectoryID"] = result.TempDirectoryID
				response["cleaned"] = result.Cleaned
			}
			respond(http.StatusGatewayTimeout, response)
			return
		}
		if c.Request.Context().Err() != nil {
			return
		}
		message := err.Error()
		if result != nil {
			message = fmt.Sprintf("%s；已写入 %d 个文件", message, result.Written)
		}
		response := gin.H{"error": message}
		if result != nil {
			response["reshareUrl"] = result.ReshareURL
			response["tempDirectoryID"] = result.TempDirectoryID
			response["cleaned"] = result.Cleaned
		}
		respond(code, response)
		return
	}
	respond(http.StatusOK, gin.H{"directory": result.Directory, "count": result.Written, "skipped": result.Skipped, "total": len(result.Files), "reshareUrl": result.ReshareURL, "tempDirectoryID": result.TempDirectoryID, "cleaned": result.Cleaned})
}
