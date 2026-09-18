package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

// RegisterCloudDownload exposes the administrator-only cloud-download card.
// Every request carries accountId; there is deliberately no fallback account.
func RegisterCloudDownload(admin *gin.RouterGroup, accounts *service.P115Accounts, settings *service.CloudDownloadSettings, downloads *service.CloudDownloadService, cloud *p115.CloudClient, drive *p115.ShareClient) {
	group := admin.Group("/p115/cloud-download")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	})
	account := func(c *gin.Context) (service.P115Account, bool) {
		id := strings.TrimSpace(c.Query("accountId"))
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请先选择云下载使用的 115 账号配置"})
			return service.P115Account{}, false
		}
		value, err := accounts.Get(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "所选 115 账号配置不存在，请重新选择"})
			return service.P115Account{}, false
		}
		if value.Cookie == "" {
			c.JSON(http.StatusConflict, gin.H{"error": "所选 115 账号尚未登录，请先在 115 助手中配置凭据"})
			return service.P115Account{}, false
		}
		return value, true
	}
	fail := func(c *gin.Context, err error) {
		status := http.StatusBadGateway
		if errors.Is(err, p115.ErrCloudInput) {
			status = http.StatusBadRequest
		} else if errors.Is(err, service.ErrGenerationInput) {
			status = http.StatusBadRequest
		} else {
			var upstream *p115.HTTPError
			if errors.As(err, &upstream) {
				status = http.StatusBadGateway
				if upstream.Status == http.StatusUnauthorized || upstream.Status == http.StatusForbidden {
					status = http.StatusConflict
				}
			}
		}
		c.JSON(status, gin.H{"error": err.Error()})
	}
	group.GET("/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, settings.View())
	})
	group.PUT("/config", func(c *gin.Context) {
		var input service.CloudDownloadConfig
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "云下载配置参数无效"})
			return
		}
		value, err := settings.Save(input)
		if err != nil {
			if errors.Is(err, service.ErrAccountInput) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			if errors.Is(err, service.ErrAccountMissing) {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, value)
	})

	group.GET("/paths", func(c *gin.Context) {
		value, ok := account(c)
		if !ok {
			return
		}
		cid := strings.TrimSpace(c.DefaultQuery("cid", "0"))
		offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if err != nil || offset < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "目录分页参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		defer cancel()
		page, err := drive.DriveFiles(ctx, cid, offset, value.Cookie)
		if err != nil {
			fail(c, err)
			return
		}
		directories := make([]gin.H, 0, len(page.Files))
		for _, file := range page.Files {
			if file.Directory {
				directories = append(directories, gin.H{"id": file.ID, "name": file.Name})
			}
		}
		c.JSON(http.StatusOK, gin.H{"cid": cid, "directories": directories, "count": page.Count, "offset": page.Offset, "hasMore": offset+len(page.Files) < page.Count})
	})

	group.POST("/tasks", func(c *gin.Context) {
		value, ok := account(c)
		if !ok {
			return
		}
		var input struct {
			URL       string `json:"url"`
			TargetCID string `json:"targetCID"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "云下载链接参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		targetCID := strings.TrimSpace(input.TargetCID)
		targetPath := "/"
		if saved := settings.View(); targetCID == "" && saved.AccountID == value.ID {
			targetCID, targetPath = saved.TargetCID, saved.TargetPath
		}
		if targetCID == "" {
			targetCID = "0"
		}
		receipt, err := downloads.Submit(ctx, service.CloudDownloadConfig{AccountID: value.ID, TargetCID: targetCID, TargetPath: targetPath}, input.URL)
		if err != nil {
			fail(c, err)
			return
		}
		// Do not return the upstream payload wholesale: it can contain fields
		// that are not part of our stable API. The task list is the source of
		// truth for subsequent progress polling.
		c.JSON(http.StatusAccepted, gin.H{"accepted": true, "task": taskView(receipt.Task)})
	})

	group.GET("/tasks", func(c *gin.Context) {
		value, ok := account(c)
		if !ok {
			return
		}
		page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
		if err != nil {
			page = 1
		}
		pageSize, err := strconv.Atoi(c.DefaultQuery("pageSize", "30"))
		if err != nil {
			pageSize = 30
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		defer cancel()
		result, err := cloud.ListTasks(ctx, value.Cookie, page, pageSize, c.Query("stat"))
		if err != nil {
			fail(c, err)
			return
		}
		tasks := make([]gin.H, 0, len(result.Tasks))
		for _, task := range result.Tasks {
			tasks = append(tasks, taskView(task))
		}
		c.JSON(http.StatusOK, gin.H{"tasks": tasks, "total": result.Total, "page": page})
	})
}

func taskView(task p115.CloudTask) gin.H {
	return gin.H{"infoHash": task.InfoHash, "name": task.Name, "status": task.Status, "progress": task.Progress, "doneBytes": task.DoneBytes, "totalBytes": task.TotalBytes, "error": task.Error}
}
