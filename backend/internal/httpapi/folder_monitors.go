package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type folderMonitorInput struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	AccountID          string `json:"accountId"`
	SourceCID          string `json:"sourceCID"`
	SourcePath         string `json:"sourcePath"`
	OutputDir          string `json:"outputDir"`
	BaseURL            string `json:"baseUrl"`
	VideoExtensions    string `json:"videoExtensions"`
	MetadataExtensions string `json:"metadataExtensions"`
	MinSizeMB          int64  `json:"minSizeMB"`
	Recursive          bool   `json:"recursive"`
	Enabled            bool   `json:"enabled"`
	IntervalSeconds    int    `json:"intervalSeconds"`
}

func (in folderMonitorInput) model() model.FolderMonitor {
	return model.FolderMonitor{
		ID: in.ID, Name: in.Name, AccountID: in.AccountID, SourceCID: in.SourceCID, SourcePath: in.SourcePath,
		OutputDir: in.OutputDir, BaseURL: in.BaseURL, VideoExtensions: in.VideoExtensions,
		MetadataExtensions: in.MetadataExtensions, MinSizeMB: in.MinSizeMB, Recursive: in.Recursive,
		Enabled: in.Enabled, IntervalSeconds: in.IntervalSeconds,
	}
}

func folderMonitorView(value model.FolderMonitor) gin.H {
	batchCount := len(value.BatchIDs)
	if batchCount == 0 && value.BatchDirectoryID != "" {
		batchCount = value.BatchMoveIndex
	}
	return gin.H{
		"id": value.ID, "name": value.Name, "accountId": value.AccountID,
		"sourceCID": value.SourceCID, "sourcePath": value.SourcePath,
		"outputDir": value.OutputDir, "baseUrl": value.BaseURL,
		"videoExtensions": value.VideoExtensions, "metadataExtensions": value.MetadataExtensions,
		"minSizeMB": value.MinSizeMB, "recursive": value.Recursive,
		"enabled": value.Enabled, "intervalSeconds": value.IntervalSeconds,
		"running": value.Running, "queued": value.TaskPending && !value.Running, "trigger": value.TaskTrigger,
		"logTaskID": value.LogTaskID, "logAttempt": value.LogAttempt, "generationTaskId": value.GenerationTaskID,
		"hasBatch": value.BatchDirectoryID != "",
		"stage":    value.Stage, "shareUrl": value.ShareURL, "shareReviewing": value.ShareReviewing, "stableScans": value.StableScans,
		"batchCount": batchCount, "batchMoved": value.BatchMoveIndex, "cleanupIndex": value.CleanupIndex,
		"batchDirectoryName": value.BatchDirectoryName, "batchMoveIndex": value.BatchMoveIndex,
		"lastScanAt": value.LastScanAt, "nextScanAt": value.NextScanAt,
		"lastError": value.LastError, "updatedAt": value.UpdatedAt,
	}
}

func RegisterFolderMonitors(admin *gin.RouterGroup, monitors *service.FolderMonitorService) {
	group := admin.Group("/p115/folder-monitors")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		status := http.StatusConflict
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, service.ErrGenerationInput) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
	}
	group.GET("", func(c *gin.Context) {
		values, err := monitors.List(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		views := make([]gin.H, 0, len(values))
		for _, value := range values {
			views = append(views, folderMonitorView(value))
		}
		c.JSON(http.StatusOK, gin.H{"monitors": views})
	})
	group.POST("", func(c *gin.Context) {
		var input folderMonitorInput
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.ID) != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "目录监控参数无效"})
			return
		}
		value, err := monitors.SaveUsingSharedSettings(c.Request.Context(), input.model())
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusCreated, folderMonitorView(value))
	})
	group.PUT("/:id", func(c *gin.Context) {
		var input folderMonitorInput
		if c.ShouldBindJSON(&input) != nil || input.ID != "" && input.ID != c.Param("id") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "目录监控参数无效"})
			return
		}
		input.ID = c.Param("id")
		value, err := monitors.SaveUsingSharedSettings(c.Request.Context(), input.model())
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, folderMonitorView(value))
	})
	group.DELETE("/:id", func(c *gin.Context) {
		if err := monitors.Delete(c.Request.Context(), c.Param("id")); err != nil {
			fail(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.PUT("/:id/schedule", func(c *gin.Context) {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Enabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请指定定时检查开关状态"})
			return
		}
		value, err := monitors.SetSchedule(c.Request.Context(), c.Param("id"), *input.Enabled)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, folderMonitorView(value))
	})
	trigger := func(c *gin.Context) {
		value, accepted, err := monitors.Trigger(c.Request.Context(), c.Param("id"))
		if err != nil {
			fail(c, err)
			return
		}
		status := http.StatusAccepted
		if !accepted {
			status = http.StatusOK
		}
		c.JSON(status, gin.H{"accepted": accepted, "taskId": value.LogTaskID, "monitorId": value.ID, "generationTaskId": value.GenerationTaskID})
	}
	group.POST("/:id/share", trigger)
	// Preserve the old manual trigger for bookmarks/older clients.
	group.POST("/:id/scan", trigger)
	group.GET("/defaults", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"videoExtensions": service.DefaultVideoExtensions, "metadataExtensions": service.DefaultMetadataExtensions})
	})
}
