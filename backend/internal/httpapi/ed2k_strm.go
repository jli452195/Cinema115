package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
	"strm115/backend/internal/strm"
)

func RegisterED2KSTRM(admin *gin.RouterGroup, s *service.ED2KSTRMService, defaultBaseURL func() string, refreshBots func()) {
	g := admin.Group("/p115/ed2k-strm")
	g.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		code := http.StatusConflict
		if errors.Is(err, storage.ErrNotFound) {
			code = http.StatusNotFound
		} else if errors.Is(err, service.ErrGenerationInput) {
			code = http.StatusBadRequest
		}
		message := "ED2K STRM 操作未完成，请稍后重试"
		if errors.Is(err, service.ErrGenerationInput) || errors.Is(err, storage.ErrNotFound) {
			message = err.Error()
		}
		c.JSON(code, gin.H{"error": message})
	}
	g.GET("", func(c *gin.Context) {
		settings, err := s.Store.GetED2KSTRMSettings(c.Request.Context())
		saved := err == nil
		if errors.Is(err, storage.ErrNotFound) {
			settings = model.ED2KSTRMSettings{BaseURL: defaultBaseURL()}
		} else if err != nil {
			fail(c, err)
			return
		}
		a, accountErr := s.Accounts.Primary("")
		cacheErr := s.CheckCacheReady(settings)
		cacheHint := ""
		if cacheErr != nil {
			cacheHint = cacheErr.Error()
		}
		c.JSON(http.StatusOK, gin.H{"settings": settings, "settingsSaved": saved, "primaryAccountId": s.Accounts.PrimaryID(), "accountReady": accountErr == nil && a.Cookie != "", "cacheReady": cacheErr == nil, "cacheSetupHint": cacheHint, "resources": s.List(), "targets": s.Targets(), "tasks": s.Tasks()})
	})
	g.PUT("/settings", func(c *gin.Context) {
		var in model.ED2KSTRMSettings
		if c.ShouldBindJSON(&in) != nil {
			c.JSON(400, gin.H{"error": "ED2K STRM 设置格式无效"})
			return
		}
		v, err := s.SaveSettings(c.Request.Context(), in)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, v)
	})
	g.POST("/imports", func(c *gin.Context) {
		var in struct {
			Links     []string `json:"links"`
			TargetID  string   `json:"targetId"`
			RequestID string   `json:"requestId"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			var oversized *http.MaxBytesError
			if errors.As(err, &oversized) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "ED2K 链接内容超过 512 KiB，本次未接收，请减少链接后重试"})
				return
			}
			c.JSON(400, gin.H{"error": "请提供 ED2K 链接列表"})
			return
		}
		result, err := s.ImportTo(c.Request.Context(), in.TargetID, in.Links, in.RequestID)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusAccepted, result)
	})
	g.GET("/targets", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"targets": s.Targets()}) })
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		endpoint := "/targets"
		if method == http.MethodPut {
			endpoint += "/:id"
		}
		g.Handle(method, endpoint, func(c *gin.Context) {
			var in struct {
				Name      string `json:"name"`
				OutputDir string `json:"outputDir"`
			}
			if c.ShouldBindJSON(&in) != nil {
				c.JSON(400, gin.H{"error": "输出目录格式无效"})
				return
			}
			v, err := s.SaveTarget(c.Request.Context(), model.ED2KSTRMTarget{ID: c.Param("id"), Name: in.Name, OutputDir: in.OutputDir})
			if err != nil {
				fail(c, err)
				return
			}
			if refreshBots != nil {
				refreshBots()
			}
			c.JSON(http.StatusOK, v)
		})
	}
	g.DELETE("/targets/:id", func(c *gin.Context) {
		if err := s.DeleteTarget(c.Request.Context(), c.Param("id")); err != nil {
			fail(c, err)
			return
		}
		if refreshBots != nil {
			refreshBots()
		}
		c.Status(http.StatusNoContent)
	})
	g.POST("/tasks/:id/retry", func(c *gin.Context) {
		if err := s.RetryTask(c.Request.Context(), c.Param("id")); err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"accepted": true})
	})
	g.DELETE("/tasks/:id", func(c *gin.Context) {
		if err := s.DeleteTask(c.Request.Context(), c.Param("id")); err != nil {
			fail(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	for _, action := range []string{"check", "restore"} {
		g.POST("/resources/:hash/"+action, func(c *gin.Context) {
			var in struct {
				Size int64 `json:"size"`
			}
			if c.ShouldBindJSON(&in) != nil || in.Size <= 0 {
				c.JSON(400, gin.H{"error": "请提供有效的字节大小"})
				return
			}
			v, err := s.Request(c.Request.Context(), c.Param("hash"), in.Size, action == "check")
			if err != nil {
				fail(c, err)
				return
			}
			c.JSON(http.StatusAccepted, v)
		})
	}
}

// 独立播放器入口仅传输已有原文件；离线恢复由导入、管理操作或已授权播放准备安排。
func RegisterED2KPlayback(r *gin.Engine, s *service.ED2KSTRMService, mediaHTTP *http.Client, logs *service.GenerationLogs) {
	play := func(c *gin.Context) {
		entry := strmPlaybackLog{started: time.Now(), phase: "ED2K 地址解析"}
		defer entry.write(c, logs, "ED2K播放")
		c.Header("Cache-Control", "no-store")
		fail := func(status int, message string) {
			entry.failure = message
			if c.Request.Method == http.MethodHead {
				c.Status(status)
				return
			}
			c.JSON(status, gin.H{"error": message})
		}
		if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
			fail(400, "ED2K STRM 地址不接受查询参数")
			return
		}
		claim, err := strm.ParseED2KPath(c.Request.URL.EscapedPath())
		if err != nil {
			fail(400, "ED2K STRM 地址格式无效")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		link, err := s.ResolveURL(ctx, claim, p115.PlaybackUserAgent, false)
		cancel()
		if err != nil {
			var pending *service.PlaybackPendingError
			if errors.As(err, &pending) {
				c.Header("Retry-After", "5")
				fail(503, "ED2K 缓存未就绪，请在管理页面恢复后重试")
			} else {
				fail(502, "ED2K 原文件暂不可读取，请检查缓存与账号状态")
			}
			return
		}
		entry.phase = "原文件传输"
		if err := streamMedia(c, mediaHTTP, link, claim.Name, func() { entry.write(c, logs, "ED2K播放") }); err != nil && c.Request.Context().Err() == nil && !c.Writer.Written() {
			fail(502, "ED2K 原文件读取失败，请重试")
		}
	}
	r.GET(strm.ED2KPrefix+"*path", play)
	r.HEAD(strm.ED2KPrefix+"*path", play)
}
