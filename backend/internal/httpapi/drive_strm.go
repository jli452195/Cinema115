package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

// Accept configuration only; runtime state, statistics and pending work are
// owned by the service and cannot be replaced by a browser's stale snapshot.
type driveSTRMInput struct {
	Name               string `json:"name"`
	AccountID          string `json:"accountId"`
	SourceCID          string `json:"sourceCID"`
	OutputDir          string `json:"outputDir"`
	BaseURL            string `json:"baseUrl"`
	Enabled            bool   `json:"enabled"`
	VideoExtensions    string `json:"videoExtensions"`
	MetadataExtensions string `json:"metadataExtensions"`
	MinSizeMB          int64  `json:"minSizeMB"`
}

func RegisterDriveSTRM(admin *gin.RouterGroup, scanner *service.DriveSTRMService, events *service.DriveSTRMEvents, defaultBaseURL func() string) {
	group := admin.Group("/p115/drive-strm")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
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
		values, err := scanner.List(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		primary, primaryErr := scanner.Accounts.Primary("")
		if values == nil {
			values = make([]model.DriveSTRM, 0)
		}
		for i := range values {
			values[i].PendingDirectories = nil
			values[i].FullScan = false
		}
		c.JSON(http.StatusOK, gin.H{"mappings": values, "primaryAccountId": scanner.Accounts.PrimaryID(), "accountReady": primaryErr == nil && primary.Cookie != "", "events": events.State(), "defaults": gin.H{"baseUrl": defaultBaseURL(), "videoExtensions": service.DefaultVideoExtensions}})
	})
	save := func(c *gin.Context) {
		var input driveSTRMInput
		if c.ShouldBindJSON(&input) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "目录 STRM 参数无效"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		value, err := scanner.Save(ctx, model.DriveSTRM{ID: c.Param("id"), Name: input.Name, AccountID: input.AccountID, SourceCID: input.SourceCID, OutputDir: input.OutputDir, BaseURL: input.BaseURL, Enabled: input.Enabled, VideoExtensions: input.VideoExtensions, MetadataExtensions: input.MetadataExtensions, MinSizeMB: input.MinSizeMB})
		if err != nil {
			fail(c, err)
			return
		}
		status := http.StatusOK
		if c.Param("id") == "" {
			status = http.StatusCreated
		}
		c.JSON(status, value)
	}
	group.POST("", save)
	group.PUT("/:id", save)
	group.DELETE("/:id", func(c *gin.Context) {
		if err := scanner.Delete(c.Request.Context(), c.Param("id")); err != nil {
			fail(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.PUT("/:id/monitor", func(c *gin.Context) {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Enabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请指定自动监控开关"})
			return
		}
		value, err := scanner.SetMonitoring(c.Request.Context(), c.Param("id"), *input.Enabled)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, value)
	})
	group.POST("/:id/scan", func(c *gin.Context) {
		if err := scanner.Queue(c.Request.Context(), c.Param("id"), true); err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"accepted": true})
	})
	group.POST("/:id/stop", func(c *gin.Context) {
		if err := scanner.Stop(c.Request.Context(), c.Param("id")); err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"stopped": true})
	})
	group.POST("/:id/check", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()
		value, err := scanner.Store.GetDriveSTRM(ctx, c.Param("id"))
		if err != nil {
			fail(c, err)
			return
		}
		if _, err := scanner.Accounts.Primary(value.AccountID); err != nil {
			fail(c, err)
			return
		}
		if !value.Enabled {
			c.JSON(http.StatusConflict, gin.H{"error": "请先开启自动监控，或使用完整扫描补齐"})
			return
		}
		if err := events.Check(ctx); err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"checked": true})
	})
}
