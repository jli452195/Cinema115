package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/anibt"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func animeError(c *gin.Context, err error) {
	status, code, message := 500, "INTERNAL_ERROR", "动漫配置操作失败，请稍后重试"
	details := gin.H{}
	var config *service.AnimeConfigError
	var exists *storage.AnimeExistsError
	var upstream *bangumi.UpstreamError
	var sourceHTTP *mikan.ResponseError
	var rule *model.AnimeRuleError
	switch {
	case errors.As(err, &rule):
		status, code, message = 400, "SOURCE_RULE_INVALID", rule.Message
		details["sourceRef"], details["field"], details["line"] = rule.SourceRef, rule.Field, rule.Line
	case errors.Is(err, animefeed.ErrInput):
		status, code, message = 400, "INVALID_INPUT", "站点、条目或字幕组参数不正确"
	case errors.Is(err, garden.ErrInvalid):
		status, code, message = 502, "UPSTREAM_INVALID", "Anime Garden \u8fd4\u56de\u6570\u636e\u65e0\u6cd5\u6838\u9a8c"
	case errors.Is(err, garden.ErrUnavailable):
		status, code, message = 502, "UPSTREAM_UNAVAILABLE", "Anime Garden \u6682\u65f6\u65e0\u6cd5\u8bfb\u53d6"
	case errors.Is(err, anibt.ErrInvalid):
		status, code, message = 502, "UPSTREAM_INVALID", "AniBT 返回的数据无法核验，原配置保留"
	case errors.Is(err, anibt.ErrMoved):
		status, code, message = 409, "SOURCE_MOVED", "AniBT 条目已合并，请返回该站点重新选择，原配置保留"
	case errors.Is(err, anibt.ErrUnavailable):
		status, code, message = 502, "UPSTREAM_UNAVAILABLE", "AniBT 暂时无法读取，已有配置保留"
	case errors.Is(err, service.ErrAnimeNamingChanged):
		status, code, message = 409, "NAMING_METADATA_CHANGED", "TMDB 资料已变化，请重新读取命名预览"
	case errors.Is(err, tmdb.ErrInput):
		status, code, message = 400, "INVALID_INPUT", "TMDB 目标或季集参数不正确"
	case errors.Is(err, tmdb.ErrNotConfigured):
		status, code, message = 503, "TMDB_NOT_CONFIGURED", "请先配置 TMDB 服务，再选择整理命名目标"
	case errors.Is(err, tmdb.ErrNotFound):
		status, code, message = 404, "NAMING_TARGET_NOT_FOUND", "目标作品或季集不存在，请更换目标"
	case errors.Is(err, storage.ErrAnimeIdempotency):
		status, code, message = 409, "IDEMPOTENCY_KEY_REUSED", "该请求标识已用于其他内容"
	case errors.Is(err, storage.ErrAnimeTransition):
		status, code, message = 409, "STATE_CONFLICT", "当前订阅状态不支持此操作"
	case errors.Is(err, storage.ErrAnimeDeleteBusy):
		status, code, message = 409, "TASK_BUSY", "任务正在处理，请稍后重试删除；115 文件将保留"
	case errors.Is(err, storage.ErrAnimeConfigurationRequired):
		status, code, message = 409, "CONFIGURATION_REQUIRED", "请重新读取并保存订阅配置"
	case errors.Is(err, storage.ErrAnimeDirectoryRequired):
		status, code, message = 409, "DIRECTORY_REQUIRED", "请为当前主账号重新选择目录"
	case errors.Is(err, storage.ErrAnimeCapability):
		status, code, message = 409, "CAPABILITY_UNAVAILABLE", "当前操作暂不可用"
	case errors.Is(err, storage.ErrAnimeAccountChanged):
		status, code, message = 409, "ACCOUNT_SNAPSHOT_CHANGED", "主账号已变化，请重新核对目录"
	case errors.Is(err, storage.ErrAnimeSettingsChanged):
		status, code, message = 409, "DIRECTORY_BINDING_CHANGED", "功能目录配置已变化，请重新读取配置"
	case errors.Is(err, storage.ErrAnimeConflict), errors.Is(err, animepreview.ErrConflict):
		status, code, message = 409, "REVISION_CONFLICT", "配置版本已变化，请保留输入并读取新版本"
	case errors.Is(err, animepreview.ErrExpired):
		status, code, message = 409, "PREVIEW_EXPIRED", "预览已过期，请重新生成，已选输入保留"
	case errors.Is(err, animepreview.ErrReview):
		status, code, message = 409, "PREVIEW_CHANGED", "预览选择需要重新核对"
	case errors.As(err, &exists):
		status, code, message = 409, "SUBSCRIPTION_EXISTS", "该篇章已有订阅配置"
		if exists.Kind == "source" {
			code, message = "SOURCE_EXISTS", "该资源来源已添加"
		}
		if exists.ID != "" {
			details["existingId"] = exists.ID
		}
	case errors.Is(err, storage.ErrAnimeExists):
		status, code, message = 409, "SOURCE_EXISTS", "配置存在重复来源"
	case errors.Is(err, storage.ErrAnimeIdentity), errors.Is(err, storage.ErrAnimeInvalid), errors.Is(err, animepreview.ErrInput), errors.Is(err, service.ErrAnimeBindingInput), errors.Is(err, bangumi.ErrInput), errors.Is(err, mikan.ErrInput):
		status, code, message = 400, "INVALID_INPUT", "参数格式或取值不正确"
	case errors.Is(err, sql.ErrNoRows):
		status, code, message = 404, "NOT_FOUND", "本地配置或预览不存在"
	case errors.Is(err, bangumi.ErrNotFound):
		status, code, message = 404, "UPSTREAM_NOT_FOUND", "外部资料不存在或已删除"
	case errors.Is(err, mikan.ErrSourceBinding):
		status, code, message = 422, "SOURCE_SCOPE_UNCONFIRMED", "所选字幕组不属于当前资源条目"
	case errors.Is(err, bangumi.ErrCooldown), errors.Is(err, bangumi.ErrBusy):
		status, code, message = 429, "SOURCE_COOLDOWN", "来源正在冷却，请稍后再试"
	case errors.Is(err, bangumi.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		status, code, message = 504, "UPSTREAM_TIMEOUT", "读取来源超时，已有配置保留"
	case errors.Is(err, bangumi.ErrInvalid), errors.Is(err, mikan.ErrParse):
		status, code, message = 502, "UPSTREAM_INVALID", "来源返回的数据无法核验"
	case errors.Is(err, service.ErrAnimeDirectoryRead), errors.Is(err, bangumi.ErrUnavailable), errors.Is(err, bangumi.ErrNetwork), errors.Is(err, bangumi.ErrAccess), errors.Is(err, mikan.ErrNetwork), errors.Is(err, mikan.ErrBlocked), errors.Is(err, mikan.ErrLimit):
		status, code, message = 502, "UPSTREAM_UNAVAILABLE", "来源暂时无法读取，已有配置保留"
	case errors.Is(err, context.Canceled):
		status, code, message = 408, "REQUEST_CANCELED", "请求已取消"
	}
	if errors.As(err, &config) {
		code = config.Code
		switch code {
		case "CONFIG_INCOMPLETE":
			status, message = 409, "请补齐订阅来源、命名与存储目录"
		case "UPSTREAM_INVALID":
			status, message = 502, "目录身份无法核验"
		default:
			status, code, message = 500, "INTERNAL_ERROR", "动漫配置操作失败"
		}
		details["issues"] = config.Issues
	}
	var retry time.Time
	var anibtHTTP *animefeed.HTTPError
	if errors.As(err, &anibtHTTP) {
		status, code, message = 502, "UPSTREAM_UNAVAILABLE", "AniBT 暂时无法读取，已有配置保留"
		if anibtHTTP.Status == 404 {
			status, code, message = 404, "UPSTREAM_NOT_FOUND", "AniBT 条目不存在或已隐藏"
		}
		if anibtHTTP.Status == 429 || anibtHTTP.Status == 503 && !anibtHTTP.RetryAt.IsZero() {
			status, code, message = 429, "SOURCE_COOLDOWN", "AniBT 正在冷却，请稍后重试"
		}
		if anibtHTTP.Site == "garden" {
			message = strings.ReplaceAll(message, "AniBT", "Anime Garden")
		}
		retry = anibtHTTP.RetryAt
	}
	var mikanCooldown *service.MikanCooldownError
	if errors.As(err, &mikanCooldown) {
		status, code, message, retry = 429, "SOURCE_COOLDOWN", "蜜柑正在冷却，请稍后再试", mikanCooldown.Until
	}
	if errors.As(err, &upstream) {
		retry = upstream.RetryAfterAt
	}
	if errors.As(err, &sourceHTTP) {
		if sourceHTTP.StatusCode == 404 {
			status, code, message = 404, "UPSTREAM_NOT_FOUND", "资源来源不存在或已删除"
		}
		if sourceHTTP.StatusCode == 429 || sourceHTTP.StatusCode == 503 && !sourceHTTP.RetryAt.IsZero() || sourceHTTP.StatusCode == 403 && !sourceHTTP.RetryAt.IsZero() {
			status, code, message = 429, "SOURCE_COOLDOWN", "来源正在冷却，请稍后再试"
		}
		retry = sourceHTTP.RetryAt
	}
	if status == 429 && !retry.IsZero() {
		details["retryAfterAt"] = retry.UTC().UnixMilli()
		c.Header("Retry-After", strconv.Itoa(max(1, int(time.Until(retry).Seconds())+1)))
	}
	c.AbortWithStatusJSON(status, gin.H{"error": message, "code": code, "details": details})
}

func animeBody(c *gin.Context, dst any, large bool) bool {
	limit := int64(64 << 10)
	if large {
		limit = 1 << 20
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	raw, err := io.ReadAll(c.Request.Body)
	if err == nil {
		err = model.DecodeAnimeJSON(bytes.NewReader(raw), dst)
	}
	if err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			c.AbortWithStatusJSON(413, gin.H{"error": "请求内容超过限制", "code": "BODY_TOO_LARGE", "details": gin.H{}})
		} else {
			animeError(c, storage.ErrAnimeInvalid)
		}
		return false
	}
	return true
}
func animeActor(c *gin.Context) string {
	u, ok := c.Get("portal_user")
	if !ok {
		return ""
	}
	v, ok := u.(model.PortalUser)
	if !ok || v.Role != "admin" {
		return ""
	}
	return v.ID
}
func animeID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !model.ValidAnimeID(id) {
		animeError(c, storage.ErrAnimeInvalid)
		return "", false
	}
	return id, true
}
func animePage(c *gin.Context) (int, int, bool) {
	read := func(key string, def, maxValue int) (int, error) {
		values, ok := c.Request.URL.Query()[key]
		if !ok {
			return def, nil
		}
		if len(values) != 1 || values[0] == "" {
			return 0, storage.ErrAnimeInvalid
		}
		v, err := strconv.Atoi(values[0])
		if err != nil || v < 1 || v > maxValue || strconv.Itoa(v) != values[0] {
			return 0, storage.ErrAnimeInvalid
		}
		return v, nil
	}
	p, e := read("page", 1, 1000000)
	if e != nil {
		animeError(c, e)
		return 0, 0, false
	}
	n, e := read("pageSize", 50, 100)
	if e != nil {
		animeError(c, e)
		return 0, 0, false
	}
	return p, n, true
}

// RegisterAnime must receive the existing authenticated administrator group.
// A second local guard prevents accidental registration to an unprotected one.
func RegisterAnime(admin *gin.RouterGroup, s *service.AnimeAdminService) {
	r := admin.Group("/anime", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if animeActor(c) == "" || c.GetString("portal_role") != "admin" {
			c.AbortWithStatusJSON(403, gin.H{"error": "仅管理员可以使用此功能", "code": "ADMIN_REQUIRED"})
			return
		}
		c.Next()
	})
	r.GET("/capabilities", func(c *gin.Context) {
		v, e := s.Settings(c.Request.Context())
		if e != nil {
			animeError(c, e)
			return
		}
		c.JSON(200, gin.H{"capabilities": gin.H{"configure": true, "download": s.DownloadAvailable(), "organize": s.OrganizationAvailable()}, "readiness": v.Readiness, "settingsRevision": v.Settings.Revision})
	})
	r.GET("/settings", func(c *gin.Context) {
		v, e := s.Settings(c.Request.Context())
		if e != nil {
			animeError(c, e)
			return
		}
		c.JSON(200, v)
	})
	r.GET("/rss-settings", func(c *gin.Context) {
		v, err := s.Store.GetAnimeRSSSettings(c.Request.Context())
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
	r.PATCH("/rss-settings", func(c *gin.Context) {
		var in model.AnimeRSSSettings
		if !animeBody(c, &in, false) {
			return
		}
		v, err := s.SaveRSSSettings(c.Request.Context(), animeActor(c), c.GetHeader("Idempotency-Key"), in)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
	r.PATCH("/settings", func(c *gin.Context) {
		var in service.AnimeSettingsInput
		if !animeBody(c, &in, false) {
			return
		}
		v, e := s.SaveSettings(c.Request.Context(), animeActor(c), c.GetHeader("Idempotency-Key"), in)
		if e != nil {
			animeError(c, e)
			return
		}
		c.JSON(200, v)
	})
	registerAnimeCatalog(r, s)
	registerAnimeAnibt(r, s)
	registerAnimeGarden(r, s)
	r.GET("/discovery/calendar", func(c *gin.Context) {
		day := 0
		if raw, ok := c.GetQuery("weekday"); ok {
			v, e := strconv.Atoi(raw)
			if e != nil || v < 1 || v > 7 || strconv.Itoa(v) != raw {
				animeError(c, storage.ErrAnimeInvalid)
				return
			}
			day = v
		}
		v, e := s.Discovery.Calendar(c.Request.Context(), day)
		if e != nil {
			animeError(c, e)
			return
		}
		c.JSON(200, v)
	})
	r.POST("/discovery/search", func(c *gin.Context) {
		var in bangumi.SearchOptions
		if !animeBody(c, &in, false) {
			return
		}
		in.Keyword = strings.TrimSpace(in.Keyword)
		if !validAnimeSearch(in.Keyword, 128) || in.Sort != "" && in.Sort != "match" || in.Limit < 0 || in.Limit > 50 || in.Offset < 0 || in.Offset > 1000 {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		if in.Limit == 0 {
			in.Limit = 20
		}
		if in.Sort == "" {
			in.Sort = "match"
		}
		v, e := s.Discovery.Search(c.Request.Context(), in)
		if e != nil {
			animeError(c, e)
			return
		}
		c.JSON(200, v)
	})
	r.GET("/discovery/subjects/:id/episodes", func(c *gin.Context) {
		id := c.Param("id")
		offset := 0
		if raw, ok := c.GetQuery("offset"); ok {
			n, err := strconv.Atoi(raw)
			if err != nil || strconv.Itoa(n) != raw || n < 0 || n >= bangumi.MaxEpisodeItems || n%bangumi.EpisodePageSize != 0 {
				animeError(c, storage.ErrAnimeInvalid)
				return
			}
			offset = n
		}
		if !model.ValidAnimeExternalID(id) {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		reader, ok := s.Discovery.(service.AnimeEpisodeDiscovery)
		if !ok {
			animeError(c, storage.ErrAnimeCapability)
			return
		}
		v, err := reader.Episodes(c.Request.Context(), id, offset)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, v)
	})
	r.GET("/discovery/subjects/:id", func(c *gin.Context) {
		id := c.Param("id")
		if !model.ValidAnimeExternalID(id) {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		relations := c.Query("includeRelations")
		if relations != "" && relations != "true" && relations != "false" {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		v, e := s.Discovery.Subject(c.Request.Context(), id)
		if e != nil {
			animeError(c, e)
			return
		}
		out := gin.H{"subject": v.Subject, "metadata": v.CacheMeta}
		if relations == "true" {
			rel, e := s.Discovery.Relations(c.Request.Context(), id)
			if e != nil {
				animeError(c, e)
				return
			}
			out["relations"] = rel
		}
		c.JSON(200, out)
	})
	r.POST("/sources/mikan/search", func(c *gin.Context) {
		var in struct {
			Keyword   string  `json:"keyword"`
			BangumiID *string `json:"bangumiId"`
			GroupID   string  `json:"groupId"`
		}
		if !animeBody(c, &in, false) {
			return
		}
		in.Keyword = strings.TrimSpace(in.Keyword)
		if !validAnimeSearch(in.Keyword, 128) || in.BangumiID != nil && !model.ValidAnimeExternalID(*in.BangumiID) || in.GroupID != "" && !mikan.ValidID(in.GroupID) {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		query := service.AnimeMikanCandidatesInput{Keyword: in.Keyword, GroupID: in.GroupID}
		if in.BangumiID != nil {
			query.Entry = &service.AnimeEntryRef{BangumiID: in.BangumiID, EditionKey: "original"}
		}
		v, e := s.Bindings.SearchMikanCandidates(c.Request.Context(), query)
		if e != nil {
			animeError(c, e)
			return
		}
		c.JSON(200, v)
	})
	r.GET("/sources/mikan/:id", func(c *gin.Context) {
		id := c.Param("id")
		if !mikan.ValidID(id) {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		b, e := service.NewAnimeSourceBudget(nil)
		if e != nil {
			animeError(c, e)
			return
		}
		v, e := s.Bindings.Mikan.ReadAnimeSourceEntry(c.Request.Context(), b, id, false)
		if e != nil {
			animeError(c, e)
			return
		}
		c.JSON(200, gin.H{"entry": v.Entry, "fetchedAt": v.ObservedAt.UnixMilli(), "expiresAt": v.ExpiresAt.UnixMilli(), "cached": v.Cached})
	})
	r.POST("/source-bindings/preview", func(c *gin.Context) {
		var in service.AnimeBindingInput
		if !animeBody(c, &in, false) {
			return
		}
		b, e := service.NewAnimeSourceBudget(nil)
		if e != nil {
			animeError(c, e)
			return
		}
		v, e := s.Bindings.PreviewBinding(c.Request.Context(), b, in)
		if e != nil {
			animeError(c, e)
			return
		}
		c.JSON(200, v)
	})
	registerAnimePreviews(r, s)
	registerAnimeNaming(r, s)
	registerAnimeOrganization(r, s)
	// Old preview-based activation cannot grant work through the public API.
	r.POST("/subscriptions", func(c *gin.Context) { animeSaveDefinition(c, s, "") })
	r.PATCH("/subscriptions/:id", func(c *gin.Context) {
		if id, ok := animeID(c); ok {
			animeSaveDefinition(c, s, id)
		}
	})
	r.GET("/subscriptions", func(c *gin.Context) {
		p, n, ok := animePage(c)
		if !ok {
			return
		}
		v, e := s.Subscriptions(c.Request.Context(), storage.AnimeSubscriptionFilter{Status: c.Query("status"), Keyword: strings.TrimSpace(c.Query("keyword")), BangumiID: c.Query("bangumiId"), Page: p, PageSize: n})
		if e != nil {
			animeError(c, e)
			return
		}
		c.JSON(200, v)
	})
	r.GET("/subscriptions/:id", func(c *gin.Context) {
		id, ok := animeID(c)
		if !ok {
			return
		}
		v, e := s.Subscription(c.Request.Context(), id)
		if e != nil {
			animeError(c, e)
			return
		}
		c.JSON(200, v)
	})
	r.GET("/subscriptions/:id/releases", func(c *gin.Context) {
		id, ok := animeID(c)
		if !ok {
			return
		}
		p, n, ok := animePage(c)
		if !ok {
			return
		}
		v, e := s.Releases(c.Request.Context(), id, storage.AnimeReleaseFilter{SourceID: c.Query("sourceId"), Disposition: c.Query("decision"), Page: p, PageSize: n})
		if e != nil {
			animeError(c, e)
			return
		}
		c.JSON(200, v)
	})
	registerAnimeControl(r, s)
	// Explicit execution endpoints remain closed until their phase is accepted.
	for _, path := range []string{"/subscriptions/:id/check", "/subscriptions/:id/history/apply", "/subscriptions/:id/organize/apply", "/subscriptions/:id/organize/preview", "/subscriptions/:id/naming-mapping/preview", "/subscriptions/:id/naming-metadata/check", "/subscriptions/:id/naming-metadata/apply", "/subscriptions/:id/sources/:sourceId/enable", "/subscriptions/:id/sources/:sourceId/pause", "/subscriptions/:id/sources/:sourceId/recover"} {
		r.POST(path, func(c *gin.Context) { animeError(c, storage.ErrAnimeCapability) })
	}
	r.PUT("/subscriptions/:id/naming-mapping", func(c *gin.Context) { animeError(c, storage.ErrAnimeCapability) })
}

func animeSaveDefinition(c *gin.Context, s *service.AnimeAdminService, id string) {
	var in service.AnimeSaveInput
	if !animeBody(c, &in, true) {
		return
	}
	v, err := s.SaveDefinition(c.Request.Context(), animeActor(c), c.GetHeader("Idempotency-Key"), id, in)
	if err != nil {
		animeError(c, err)
		return
	}
	status := 200
	if id == "" {
		status = 201
	}
	c.JSON(status, v)
}

func validAnimeSearch(v string, n int) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) >= 1 && utf8.RuneCountInString(v) <= n && !strings.ContainsFunc(v, unicode.IsControl)
}
