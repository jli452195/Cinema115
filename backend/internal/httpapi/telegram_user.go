package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tguser"
	"strm115/backend/internal/service"
)

func RegisterTelegramUser(admin *gin.RouterGroup, account *tguser.Client, tmdb TMDBReader, sinks ...*service.GenerationLogs) {
	group := admin.Group("/telegram-user")
	if len(sinks) > 0 {
		actions := make(map[string]string)
		for path, label := range map[string]string{"config": "保存用户与资源 Bot 配置", "check": "检查用户账号连接", "send-code": "请求登录验证码", "sign-in": "验证用户登录", "disconnect": "退出用户账号"} {
			actions[group.BasePath()+"/"+path] = label
		}
		group.Use(operationLogs(sinks[0], "Telegram", "用户账号", actions))
		group.Use(operationLogs(sinks[0], "搜索", "Bot", map[string]string{
			group.BasePath() + "/search":        "资源搜索",
			group.BasePath() + "/search-stream": "流式资源搜索",
			group.BasePath() + "/select":        "获取资源或翻页",
		}))
		group.Use(operationLogs(sinks[0], "Telegram", "频道配置", map[string]string{
			group.BasePath() + "/channels": "保存搜索频道",
		}))
	}
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	})
	fail := func(c *gin.Context, err error) {
		status := 502
		if errors.Is(err, tguser.ErrChannelSelection) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "channel_selection_expired"})
			return
		}
		var detail *tguser.OperationError
		if errors.As(err, &detail) {
			if detail.RetryAt != nil {
				c.Header("Retry-After", strconv.Itoa(max(1, int(time.Until(*detail.RetryAt).Seconds())+1)))
			}
			c.JSON(http.StatusConflict, detail)
			return
		}
		if errors.Is(err, tguser.ErrBusy) {
			status = 409
		}
		if errors.Is(err, tguser.ErrInput) {
			status = 400
		}
		if errors.Is(err, tguser.ErrLogin) || errors.Is(err, tguser.ErrConfig) || errors.Is(err, tguser.ErrBotConfig) {
			status = 409
		}
		c.JSON(status, gin.H{"error": err.Error()})
	}
	group.GET("/config", func(c *gin.Context) { c.JSON(200, account.View()) })
	group.GET("/channels", func(c *gin.Context) { c.JSON(200, account.ChannelConfig()) })
	group.POST("/channels/list", func(c *gin.Context) {
		var input struct {
			Cursor string `json:"cursor"`
		}
		if c.ShouldBindJSON(&input) != nil || len(input.Cursor) > 64 {
			fail(c, tguser.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
		defer cancel()
		result, err := account.ListChannels(ctx, input.Cursor)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, result)
	})
	group.POST("/channels", func(c *gin.Context) {
		var input struct {
			SelectionToken string   `json:"selectionToken"`
			ChannelIDs     []string `json:"channelIds"`
		}
		if c.ShouldBindJSON(&input) != nil || len(input.SelectionToken) > 64 {
			fail(c, tguser.ErrInput)
			return
		}
		if err := account.SaveChannels(input.SelectionToken, input.ChannelIDs); err != nil {
			fail(c, err)
			return
		}
		c.Set("operationLogSummary", fmt.Sprintf("已保存 %d 个频道", len(input.ChannelIDs)))
		c.JSON(200, account.ChannelConfig())
	})
	group.POST("/config", func(c *gin.Context) {
		var input tguser.Config
		if c.ShouldBindJSON(&input) != nil {
			fail(c, tguser.ErrInput)
			return
		}
		if err := account.Save(input); err != nil {
			fail(c, err)
			return
		}
		c.JSON(200, account.View())
	})
	for _, path := range []string{"check", "send-code", "sign-in", "disconnect"} {
		group.POST("/"+path, func(c *gin.Context) {
			var input struct {
				Phone    string `json:"phone"`
				Code     string `json:"code"`
				Password string `json:"password"`
			}
			if c.ShouldBindJSON(&input) != nil {
				fail(c, tguser.ErrInput)
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
			defer cancel()
			var err error
			switch path {
			case "check":
				err = account.Check(ctx)
			case "send-code":
				err = account.SendCode(ctx, input.Phone)
			case "sign-in":
				err = account.SignIn(ctx, input.Code, input.Password)
			case "disconnect":
				err = account.Disconnect(ctx)
			}
			if err != nil {
				fail(c, err)
				return
			}
			view := account.View()
			if path == "sign-in" && view.Stage == "password" {
				c.Set("operationLogSummary", "等待二步验证密码")
			}
			if path == "check" && !view.Authorized {
				c.Set("operationLogSummary", "当前账号未登录")
			}
			c.JSON(200, account.View())
		})
	}
	type searchInput struct {
		Kind    string `json:"kind"`
		TMDBID  string `json:"tmdbId"`
		Refresh bool   `json:"refresh"`
	}
	loadSearchTitle := func(c *gin.Context, input searchInput) (string, bool) {
		if input.Kind != "movie" && input.Kind != "tv" {
			fail(c, tguser.ErrInput)
			return "", false
		}
		id, err := strconv.Atoi(input.TMDBID)
		if err != nil || id < 1 {
			fail(c, tguser.ErrInput)
			return "", false
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		detail, err := tmdb.Details(ctx, input.Kind, id)
		if err != nil {
			tmdbError(c, err)
			return "", false
		}
		return detail.Title, true
	}
	setSearchSummary := func(c *gin.Context, input searchInput, result tguser.SearchResult) {
		summary := fmt.Sprintf("%s/%s，资源 %d 项、链接 %d 条", input.Kind, input.TMDBID, len(result.Items), len(result.Links))
		if result.Empty {
			summary = fmt.Sprintf("%s/%s，未找到资源", input.Kind, input.TMDBID)
		}
		if result.Cached {
			summary += "，复用缓存，未发送 Bot 查询"
			c.Set("operationLogCached", true)
		}
		c.Set("operationLogSummary", summary)
	}
	group.POST("/search", func(c *gin.Context) {
		var input searchInput
		if c.ShouldBindJSON(&input) != nil || (input.Kind != "movie" && input.Kind != "tv") {
			fail(c, tguser.ErrInput)
			return
		}
		// 只发送服务端 TMDB 条目的中文标题，前端不能传任意 Bot 命令。
		title, ok := loadSearchTitle(c, input)
		if !ok {
			return
		}
		searchCtx, stop := context.WithTimeout(c.Request.Context(), 110*time.Second)
		defer stop()
		result, err := account.Search(searchCtx, title, input.Refresh)
		if err != nil {
			fail(c, err)
			return
		}
		setSearchSummary(c, input, result)
		c.JSON(200, result)
	})
	group.POST("/search-stream", func(c *gin.Context) {
		var input searchInput
		if c.ShouldBindJSON(&input) != nil {
			fail(c, tguser.ErrInput)
			return
		}
		title, ok := loadSearchTitle(c, input)
		if !ok {
			return
		}
		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "当前服务不支持流式响应"})
			return
		}
		c.Header("Content-Type", "text/event-stream; charset=utf-8")
		c.Header("Cache-Control", "no-cache, no-store")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
		flusher.Flush()
		searchCtx, stop := context.WithTimeout(c.Request.Context(), 110*time.Second)
		defer stop()
		push := func(result tguser.SearchResult, complete bool) {
			c.SSEvent("result", gin.H{"complete": complete, "result": result})
			flusher.Flush()
		}
		result, err := account.SearchWithProgress(searchCtx, title, input.Refresh, func(update tguser.SearchResult) {
			push(update, false)
		}, func(progress tguser.SearchProgress) {
			c.SSEvent("status", progress)
			flusher.Flush()
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				c.Set("operationLogSummary", "页面停止等待；已开始的 Bot 查询继续限时收尾，未开始的查询退出排队")
				return
			}
			c.Set("operationLogFailed", true)
			if result.Partial {
				push(result, false)
			}
			var detail *tguser.OperationError
			if errors.As(err, &detail) {
				c.Set("operationLogSummary", "未完成（"+detail.Code+"）："+detail.Message)
				c.SSEvent("error", detail)
			} else {
				c.SSEvent("error", gin.H{"error": err.Error()})
			}
			flusher.Flush()
			return
		}
		setSearchSummary(c, input, result)
		push(result, true)
	})
	group.POST("/select", func(c *gin.Context) {
		var input struct {
			ID string `json:"id"`
		}
		if c.ShouldBindJSON(&input) != nil || len(input.ID) != 32 {
			fail(c, tguser.ErrInput)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 110*time.Second)
		defer cancel()
		result, err := account.Select(ctx, input.ID)
		if err != nil {
			fail(c, err)
			return
		}
		c.Set("operationLogSummary", fmt.Sprintf("返回资源 %d 项、链接 %d 条", len(result.Items), len(result.Links)))
		if result.Cached {
			c.Set("operationLogSummary", "复用资源缓存，未发送 Bot 查询")
			c.Set("operationLogCached", true)
		}
		c.JSON(200, result)
	})
}
