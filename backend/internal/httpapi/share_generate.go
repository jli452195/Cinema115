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
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func (a *ShareAPI) generationConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"baseUrl": a.configuredBaseURL(), "videoExtensions": service.DefaultVideoExtensions, "metadataExtensions": service.DefaultMetadataExtensions, "outputDir": "strm", "outputRoot": "/strm", "directories": []string{"strm"}})
}

type outputDirectoryEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	DisplayPath string `json:"displayPath"`
}

func virtualOutputPath(relative string) string {
	if relative == "" {
		return "/strm"
	}
	return "/strm/" + relative
}

// outputDirectories 只浏览媒体库挂载根目录内的真实文件夹。页面使用统一的 /strm 虚拟路径，
// 不暴露 Windows 开发路径或 NAS 宿主机路径。
func (a *ShareAPI) outputDirectories(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	raw := strings.TrimSpace(c.Query("path"))
	if len(raw) > 1024 || strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, `\:`) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "媒体库目录路径无效"})
		return
	}
	root, err := filepath.Abs(a.OutputRoot)
	if err != nil || strings.TrimSpace(a.OutputRoot) == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法解析媒体库根目录"})
		return
	}
	relative := ""
	directory := root
	if raw != "" {
		directory, err = service.ResolveOutputDirectory(root, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "媒体库目录路径无效"})
			return
		}
		relative, err = filepath.Rel(root, directory)
		if err != nil || !filepath.IsLocal(relative) || relative == "." {
			c.JSON(http.StatusBadRequest, gin.H{"error": "媒体库目录路径无效"})
			return
		}
		relative = filepath.ToSlash(relative)
	}

	// 每一级都使用 Lstat，目录内的符号链接不会成为越出媒体库的浏览入口。
	current := root
	segments := []string{}
	if relative != "" {
		segments = strings.Split(relative, "/")
	}
	for _, segment := range append([]string{""}, segments...) {
		if segment != "" {
			current = filepath.Join(current, segment)
		}
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				c.JSON(http.StatusNotFound, gin.H{"error": "所选媒体库目录不存在"})
			} else if errors.Is(statErr, os.ErrPermission) {
				c.JSON(http.StatusForbidden, gin.H{"error": "没有权限读取所选媒体库目录"})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "读取媒体库目录失败"})
			}
			return
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "不能浏览符号链接或非目录路径"})
			return
		}
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			c.JSON(http.StatusForbidden, gin.H{"error": "没有权限读取所选媒体库目录"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取媒体库目录失败"})
		}
		return
	}
	directories := make([]outputDirectoryEntry, 0)
	truncated := false
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		child := entry.Name()
		if relative != "" {
			child = relative + "/" + child
		}
		// 只展示生成器能够安全接收的目录名称。
		if _, resolveErr := service.ResolveOutputDirectory(root, child); resolveErr != nil {
			continue
		}
		if len(directories) >= 500 {
			truncated = true
			break
		}
		directories = append(directories, outputDirectoryEntry{Name: entry.Name(), Path: child, DisplayPath: virtualOutputPath(child)})
	}
	c.JSON(http.StatusOK, gin.H{"path": relative, "displayPath": virtualOutputPath(relative), "directories": directories, "truncated": truncated})
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
		if errors.Is(err, service.ErrNoMedia) || errors.Is(err, p115.ErrShareUnderReview) {
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
	respond(http.StatusOK, gin.H{"directory": result.Directory, "count": result.Written, "metadataCount": result.MetadataWritten, "metadataFailed": result.MetadataFailed, "conflictRenamed": result.ConflictRenamed, "skipped": result.Skipped, "total": len(result.Files), "reshareUrl": result.ReshareURL, "tempDirectoryID": result.TempDirectoryID, "cleaned": result.Cleaned})
}
