package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/client/imagecache"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type EmbyConfig struct {
	URL            string   `json:"url"`
	APIKey         string   `json:"apiKey,omitempty"`
	LibraryIDs     []string `json:"libraryIds"`
	ServerID       string   `json:"serverId"`
	ServerName     string   `json:"serverName"`
	WebhookSecret  string   `json:"webhookSecret,omitempty"`
	WebhookBaseURL string   `json:"webhookBaseUrl"`
}
type EmbyConfigView struct {
	URL            string   `json:"url"`
	Configured     bool     `json:"configured"`
	LibraryIDs     []string `json:"libraryIds"`
	ServerID       string   `json:"serverId"`
	ServerName     string   `json:"serverName"`
	WebhookPath    string   `json:"webhookPath"`
	WebhookBaseURL string   `json:"webhookBaseUrl"`
}
type EmbyStatus struct {
	model.EmbyState
	Configured      bool   `json:"configured"`
	Pending         int    `json:"pending"`
	Failed          int    `json:"failed"`
	Connected       bool   `json:"connected"`
	ConnectionError string `json:"connectionError"`
	EventError      string `json:"eventError"`
	MetadataPending int    `json:"metadataPending"`
	MetadataFailed  int    `json:"metadataFailed"`
	UpdatesStatus   string `json:"updatesStatus"`
}

var errEmbyMetadataPending = errors.New("Emby 刮削信息不完整")
var errEmbyLibraryChanged = errors.New("Emby 媒体库在同步期间发生变化")

type EmbyService struct {
	// Logs is wired once before the background worker starts.
	Logs            *GenerationLogs
	Posters         *imagecache.Cache // Wired once before serving requests or starting workers.
	mu              sync.RWMutex
	config          EmbyConfig
	secrets         *SessionStore
	db              *storage.Store
	checkedAt       time.Time
	connectionError string
	syncLogScope    string
	syncLogTask     string
	webhookRejectAt time.Time
	// Service locks precede missingMu; cache construction shares the published-index read lock.
	missingMu sync.Mutex
	missing   embyMissingCache
}

func NewEmbyService(secrets *SessionStore, db *storage.Store) (*EmbyService, error) {
	s := &EmbyService{secrets: secrets, db: db, config: EmbyConfig{URL: "http://192.168.0.3:8096", LibraryIDs: []string{}}}
	if raw := secrets.Value(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &s.config); err != nil {
			return nil, fmt.Errorf("读取 Emby 加密配置: %w", err)
		}
	}
	return s, nil
}
func embyScope(c EmbyConfig) string {
	ids := append([]string(nil), c.LibraryIDs...)
	sort.Strings(ids)
	h := sha256.Sum256([]byte(c.URL + "\x00" + c.ServerID + "\x00" + strings.Join(ids, ",")))
	return hex.EncodeToString(h[:])
}
func (s *EmbyService) snapshot() EmbyConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.config
	c.LibraryIDs = append([]string{}, c.LibraryIDs...)
	return c
}

// User listing is administration only. Playback continues to authorize with the player's token.
func (s *EmbyService) PlaybackUsers(ctx context.Context, proxyURL string) ([]emby.User, error) {
	c := s.snapshot()
	if c.URL != proxyURL || c.APIKey == "" {
		return nil, fmt.Errorf("请先在 Emby 设置中配置与反代相同的服务器及 API Key")
	}
	return emby.New(c.URL, c.APIKey).Users(ctx)
}

func (s *EmbyService) PlaybackUserImage(ctx context.Context, proxyURL, userID string) ([]byte, string, error) {
	c := s.snapshot()
	if c.URL != proxyURL || c.APIKey == "" {
		return nil, "", fmt.Errorf("请先配置与反代相同的 Emby 服务和 API Key")
	}
	return emby.New(c.URL, c.APIKey).UserImage(ctx, userID)
}
func (s *EmbyService) CachedSeriesIDs(ctx context.Context) ([]int, error) {
	c := s.snapshot()
	if c.APIKey == "" || len(c.LibraryIDs) == 0 {
		return []int{}, nil
	}
	return s.db.EmbySeriesIDs(ctx, embyScope(c))
}
func (s *EmbyService) Config() EmbyConfigView {
	c := s.snapshot()
	path := ""
	if c.WebhookSecret != "" {
		path = "/api/webhook/emby?token=" + url.QueryEscape(c.WebhookSecret)
	}
	return EmbyConfigView{URL: c.URL, Configured: c.APIKey != "", LibraryIDs: c.LibraryIDs, ServerID: c.ServerID, ServerName: c.ServerName, WebhookPath: path, WebhookBaseURL: c.WebhookBaseURL}
}

func normalizeWebhookBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("Cinema115 地址须为 HTTP 或 HTTPS 地址，不要填写账号、查询参数或完整 Webhook 链接")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("请填写 Cinema115 的访问地址与端口，不要附加接口路径")
	}
	u.Path = ""
	return u.String(), nil
}

// 单独保存回调地址，无需连接 Emby，也不改变 API Key、媒体库或索引。
func (s *EmbyService) SaveWebhookBaseURL(ctx context.Context, raw string) (resultErr error) {
	ctx, finish := s.startOperation(ctx, "保存 Webhook 地址")
	defer func() { finish(resultErr) }()
	base, err := normalizeWebhookBaseURL(raw)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("保存 Webhook 地址取消: %w", err)
	}
	c := s.config
	if c.WebhookSecret == "" {
		return fmt.Errorf("请先保存 Emby 连接配置")
	}
	c.WebhookBaseURL = base
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("编码 Webhook 配置: %w", err)
	}
	if err := s.secrets.Save(string(data)); err != nil {
		return fmt.Errorf("保存 Webhook 地址: %w", err)
	}
	s.config = c
	return nil
}
func (s *EmbyService) candidate(input EmbyConfig) (EmbyConfig, error) {
	current := s.snapshot()
	base, err := emby.NormalizeURL(input.URL)
	if err != nil {
		return input, err
	}
	input.URL = base
	input.APIKey = strings.TrimSpace(input.APIKey)
	if input.APIKey == "" {
		if base != current.URL {
			return input, fmt.Errorf("更换 Emby 地址时请重新填写 API Key")
		}
		input.APIKey = current.APIKey
	}
	if input.APIKey == "" || len(input.APIKey) > 512 || strings.ContainsAny(input.APIKey, "\r\n") {
		return input, fmt.Errorf("请填写有效的 Emby API Key")
	}
	input.WebhookSecret = current.WebhookSecret
	return input, nil
}
func (s *EmbyService) Test(ctx context.Context, input EmbyConfig) (result emby.Server, libraries []emby.Library, resultErr error) {
	ctx, finish := s.startOperation(ctx, "测试 Emby 连接", input.APIKey)
	defer func() { finish(resultErr) }()
	c, err := s.candidate(input)
	if err != nil {
		return emby.Server{}, nil, err
	}
	client := emby.New(c.URL, c.APIKey)
	server, err := client.Info(ctx)
	if err != nil {
		return server, nil, err
	}
	libs, err := client.Libraries(ctx)
	if err == nil {
		logEmby(ctx, "INFO", fmt.Sprintf("连接成功，读取到 %d 个媒体库", len(libs)))
	}
	return server, libs, err
}
func (s *EmbyService) Save(ctx context.Context, input EmbyConfig) (resultErr error) {
	ctx, finish := s.startOperation(ctx, "保存 Emby 配置", input.APIKey)
	defer func() { finish(resultErr) }()
	c, err := s.candidate(input)
	if err != nil {
		return err
	}
	if input.WebhookBaseURL != "" {
		c.WebhookBaseURL, err = normalizeWebhookBaseURL(input.WebhookBaseURL)
		if err != nil {
			return err
		}
	}
	client := emby.New(c.URL, c.APIKey)
	server, err := client.Info(ctx)
	if err != nil {
		return err
	}
	libs, err := client.Libraries(ctx)
	if err != nil {
		return err
	}
	allowed := make(map[string]bool, len(libs))
	for _, lib := range libs {
		allowed[lib.ID] = true
	}
	ids := make([]string, 0, len(c.LibraryIDs))
	seen := make(map[string]bool, len(c.LibraryIDs))
	for _, id := range c.LibraryIDs {
		if !allowed[id] {
			return fmt.Errorf("所选媒体库已不存在，请重新测试连接")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("请至少选择一个媒体库")
	}
	sort.Strings(ids)
	c.LibraryIDs = ids
	c.ServerID = server.ID
	c.ServerName = server.Name
	if c.WebhookSecret == "" {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return fmt.Errorf("创建 Webhook 密钥: %w", err)
		}
		c.WebhookSecret = hex.EncodeToString(b[:])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 兼容旧页面；未提交地址时保留当前已保存的地址。
	if input.WebhookBaseURL == "" {
		c.WebhookBaseURL = s.config.WebhookBaseURL
	}
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("编码 Emby 配置: %w", err)
	}
	if err := s.db.EmbyEnsure(ctx, embyScope(c)); err != nil {
		return err
	}
	if embyScope(c) != embyScope(s.config) {
		state, err := s.db.EmbyState(ctx, embyScope(c))
		if err != nil {
			return err
		}
		if state.Active != "" {
			if err := s.db.EmbyProgress(ctx, embyScope(c), "failed", 0, "同步范围已切换，请重新执行全量同步"); err != nil {
				return err
			}
		}
	}
	if err := s.secrets.Save(string(data)); err != nil {
		return fmt.Errorf("保存 Emby 加密配置: %w", err)
	}
	changed := embyScope(c) != embyScope(s.config)
	s.logConnection(ctx, c, nil)
	s.config = c
	s.checkedAt = time.Now()
	s.connectionError = ""
	logEmby(ctx, "INFO", fmt.Sprintf("配置已保存，同步范围 %d 个媒体库", len(c.LibraryIDs)))
	if changed {
		logEmby(ctx, "WARNING", "服务器或同步范围已改变，请执行全量同步")
	}
	return s.db.RecheckFollows(ctx)
}
func (s *EmbyService) Status(ctx context.Context) (EmbyStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusLocked(ctx)
}

// 调用方持有读锁，使配置和快照切换与状态查询保持一致。
func (s *EmbyService) statusLocked(ctx context.Context) (EmbyStatus, error) {
	c := s.config
	state, err := s.db.EmbyState(ctx, embyScope(c))
	if err != nil {
		return EmbyStatus{}, err
	}
	pending, failed, _, err := s.db.EmbyEventStats(ctx, embyScope(c))
	if err != nil {
		return EmbyStatus{}, err
	}
	connected := !s.checkedAt.IsZero() && time.Since(s.checkedAt) < 10*time.Minute && s.connectionError == ""
	message := s.connectionError
	eventError, err := s.db.EmbyEventError(ctx, embyScope(c))
	if err != nil {
		return EmbyStatus{}, err
	}
	metadataPending, metadataFailed, err := s.db.EmbyMetadataStats(ctx, embyScope(c))
	if err != nil {
		return EmbyStatus{}, err
	}
	updatesStatus := "waiting"
	if state.Active != "" && state.UpdatesGeneration == state.Active {
		updatesStatus = "building"
		if state.UpdatesReady {
			updatesStatus = "ready"
		}
		if state.UpdatesError != "" {
			updatesStatus = "failed"
		}
	}
	return EmbyStatus{EmbyState: state, Configured: c.APIKey != "", Pending: pending, Failed: failed, Connected: connected, ConnectionError: message, EventError: eventError, MetadataPending: metadataPending, MetadataFailed: metadataFailed, UpdatesStatus: updatesStatus}, nil
}
func (s *EmbyService) StartSync(ctx context.Context) (resultErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.config
	ctx = s.logContext(ctx, c, s.newLogTask(), "全量同步")
	defer func() {
		if resultErr != nil {
			logEmby(ctx, "ERROR", "提交全量同步失败："+embyLogError(resultErr))
		}
	}()
	if c.APIKey == "" || len(c.LibraryIDs) == 0 {
		return fmt.Errorf("请先保存 Emby 配置并选择媒体库")
	}
	scope := embyScope(c)
	state, err := s.db.EmbyState(ctx, scope)
	if err != nil {
		return err
	}
	if state.Status == "queued" || state.Status == "running" {
		logEmby(ctx, "DEBUG", "全量同步已在排队或执行中，未重复提交")
		return nil
	}
	if err := s.db.EmbyQueueSync(ctx, scope); err != nil {
		return err
	}
	s.syncLogScope = scope
	s.syncLogTask = ctx.Value(embyLogKey{}).(embyOperationLog).task
	logEmby(ctx, "INFO", "全量同步已加入后台队列，离开页面不影响处理")
	return nil
}
func (s *EmbyService) Run(ctx context.Context) (resultErr error) {
	defer func() {
		if resultErr != nil && ctx.Err() == nil {
			logCtx := s.logContext(ctx, s.snapshot(), "EMBY-WORKER", "后台处理")
			var operationErr *embyOperationError
			if errors.As(resultErr, &operationErr) {
				logCtx = context.WithValue(ctx, embyLogKey{}, operationErr.logger)
			}
			logEmby(logCtx, "ERROR", "Emby 后台处理停止，本次操作未完成："+embyLogError(resultErr))
		}
	}()
	if err := s.db.EmbyRecover(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var updatesRetryAt time.Time
	var updatesRetryScope string
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		c := s.snapshot()
		if c.APIKey == "" || len(c.LibraryIDs) == 0 {
			continue
		}
		scope := embyScope(c)
		s.mu.RLock()
		check := time.Since(s.checkedAt) > 5*time.Minute
		s.mu.RUnlock()
		if check {
			server, err := emby.New(c.URL, c.APIKey).Info(ctx)
			if err == nil && server.ID != c.ServerID {
				err = fmt.Errorf("Emby 服务器 ID 已改变，请重新保存配置")
			}
			s.mu.Lock()
			if scope == embyScope(s.config) {
				if ctx.Err() == nil {
					s.logConnection(ctx, c, err)
				}
				s.checkedAt = time.Now()
				s.connectionError = ""
				if err != nil {
					s.connectionError = err.Error()
				}
			}
			s.mu.Unlock()
		}
		state, err := s.db.EmbyState(ctx, scope)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if state.Status == "queued" {
			if err := s.syncLibrary(ctx, c); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			continue
		}
		if state.Active == "" {
			continue
		}
		if state.UpdatesGeneration == state.Active && !state.UpdatesReady {
			if updatesRetryScope != scope {
				updatesRetryScope = scope
				updatesRetryAt = time.Time{}
			}
			if !time.Now().Before(updatesRetryAt) {
				ready, buildErr := s.db.EmbyBuildMediaUpdates(ctx, scope, state.Active)
				if ctx.Err() != nil {
					return nil
				}
				if buildErr != nil {
					if err := s.db.EmbyMediaUpdatesError(ctx, scope, state.Active, buildErr.Error()); err != nil {
						return errors.Join(buildErr, err)
					}
					updatesRetryAt = time.Now().Add(30 * time.Second)
					if state.UpdatesError == "" {
						logEmby(s.logContext(ctx, c, "EMBY-UPDATES", "最近更新"), "WARNING", "最近更新索引构建失败，30 秒后重试："+embyLogError(buildErr))
					}
				} else if ready {
					logEmby(s.logContext(ctx, c, "EMBY-UPDATES", "最近更新"), "INFO", "最近更新索引构建完成")
				}
			}
		}
		// 每轮最多处理十条，退出信号和配置变更能及时被观察到。
		for n := 0; n < 10; n++ {
			if ctx.Err() != nil {
				return nil
			}
			event, err := s.db.EmbyNextEvent(ctx, scope)
			if err != nil {
				return err
			}
			if event == nil {
				break
			}
			if err := s.applyEvent(ctx, c, state.Active, *event); err != nil {
				return err
			}
		}
	}
}
func (s *EmbyService) syncLibrary(ctx context.Context, c EmbyConfig) (resultErr error) {
	scope := embyScope(c)
	s.mu.Lock()
	if s.syncLogScope != scope || s.syncLogTask == "" {
		s.syncLogScope, s.syncLogTask = scope, s.newLogTask()
	}
	ctx = s.logContext(ctx, c, s.syncLogTask, "全量同步")
	s.mu.Unlock()
	logEmby(ctx, "INFO", fmt.Sprintf("开始全量同步，共 %d 个媒体库", len(c.LibraryIDs)))
	defer func() {
		resultErr = embyFailure(ctx, resultErr)
		if ctx.Err() != nil {
			logEmby(ctx, "WARNING", "全量同步已中断，未发布未完成的索引")
		}
	}()
	state, err := s.db.EmbyState(ctx, scope)
	if err != nil {
		return err
	}
	if state.StagingComplete && state.StagingGeneration != "" {
		logEmby(ctx, "INFO", "恢复发布已完成的媒体快照，无需重新扫描")
		return s.publishLibrary(ctx, c, state.StagingGeneration, state.StagingCutoff)
	}
	for attempt := 0; attempt < 2; attempt++ {
		generation := strconv.FormatInt(time.Now().UnixNano(), 10)
		_, _, cutoff, err := s.db.EmbyEventStats(ctx, scope)
		if err != nil {
			return err
		}
		if err := s.db.EmbyBeginSync(ctx, scope, generation, cutoff); err != nil {
			return err
		}
		workCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
		count, scanErr := s.scan(workCtx, c, generation)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if embyScope(s.snapshot()) != scope {
			scanErr = fmt.Errorf("媒体库配置已更改，请按新配置重新同步")
		}
		if scanErr != nil {
			if err := s.db.EmbyFailSync(ctx, scope, generation, scanErr.Error(), true); err != nil {
				return err
			}
			if !errors.Is(scanErr, errEmbyLibraryChanged) || attempt > 0 {
				level := "ERROR"
				if embyScope(s.snapshot()) != scope {
					level = "WARNING"
				}
				logEmby(ctx, level, fmt.Sprintf("全量同步未完成，未替换现有索引：%s，耗时 %.2f 秒", embyLogError(scanErr), embyElapsed(ctx)))
				return nil
			}
			logEmby(ctx, "WARNING", "同步期间媒体库发生变化，3 秒后重新扫描（最多重试一次）")
			timer := time.NewTimer(3 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			continue
		}
		if err := s.db.EmbyCompleteStage(ctx, scope, generation, count); err != nil {
			return err
		}
		logEmby(ctx, "INFO", fmt.Sprintf("扫描完成，暂存 %d 条索引，正在发布快照", count))
		return s.publishLibrary(ctx, c, generation, cutoff)
	}
	return nil
}

func (s *EmbyService) publishLibrary(ctx context.Context, c EmbyConfig, generation string, cutoff int64) error {
	scope := embyScope(c)
	s.mu.Lock()
	if embyScope(s.config) != scope {
		s.mu.Unlock()
		if err := s.db.EmbyFailSync(ctx, scope, generation, "媒体库配置已更改，请按新配置重新同步", true); err != nil {
			return err
		}
		logEmby(ctx, "WARNING", "媒体库配置已更改，未发布本次快照，请按新配置重新同步")
		return nil
	}
	err := s.db.EmbyPublish(ctx, scope, generation, cutoff)
	if err == nil {
		s.logConnection(ctx, c, nil)
		s.checkedAt = time.Now()
		s.connectionError = ""
	}
	s.mu.Unlock()
	if err == nil {
		logEmby(ctx, "INFO", fmt.Sprintf("全量同步完成，新索引已发布，耗时 %.2f 秒", embyElapsed(ctx)))
		return nil
	}
	if recordErr := s.db.EmbyFailSync(ctx, scope, generation, err.Error(), false); recordErr != nil {
		return errors.Join(err, recordErr)
	}
	logEmby(ctx, "ERROR", "快照发布失败，未替换现有索引；已保留扫描结果供重试："+embyLogError(err))
	return nil
}
func (s *EmbyService) scan(ctx context.Context, c EmbyConfig, generation string) (int, error) {
	client := emby.New(c.URL, c.APIKey)
	scope := embyScope(c)
	count := 0
	server, err := client.Info(ctx)
	if err != nil {
		return count, err
	}
	if server.ID != c.ServerID {
		return count, fmt.Errorf("Emby 服务器 ID 已改变，请重新保存配置")
	}
	libs, err := client.Libraries(ctx)
	if err != nil {
		return count, err
	}
	available := make(map[string]bool, len(libs))
	for _, lib := range libs {
		available[lib.ID] = true
	}
	for _, library := range c.LibraryIDs {
		libraryStarted := time.Now()
		libraryCount := count
		logEmby(ctx, "DEBUG", "开始读取媒体库 "+library)
		lastProgress := time.Now()
		if !available[library] {
			return count, fmt.Errorf("所选媒体库已不存在，请重新选择同步范围")
		}
		seen := make(map[string]bool)
		expected := -1
		for start := 0; ; {
			if embyScope(s.snapshot()) != scope {
				return count, fmt.Errorf("同步配置已更改")
			}
			page, err := client.Items(ctx, library, start)
			if err != nil {
				return count, err
			}
			if expected < 0 {
				expected = page.TotalRecordCount
			}
			if page.TotalRecordCount != expected {
				return count, fmt.Errorf("%w，请稍后重试全量同步", errEmbyLibraryChanged)
			}
			if expected < 0 || start+len(page.Items) > expected {
				return count, fmt.Errorf("Emby 分页总数无效，已保留旧索引")
			}
			if len(page.Items) == 0 && start < expected {
				return count, fmt.Errorf("Emby 分页提前结束，已保留旧索引")
			}
			records := make([]model.EmbyItem, 0, len(page.Items))
			for _, item := range page.Items {
				if item.ID == "" || seen[item.ID] {
					return count, fmt.Errorf("Emby 媒体库 %s 从第 %d 条分页时出现重复或空 ID %q，已保留旧索引", library, start, item.ID)
				}
				seen[item.ID] = true
				if record, ok := item.Record(library); ok {
					records = append(records, record)
				}
			}
			if err := s.db.EmbyStagePage(ctx, scope, generation, records); err != nil {
				return count, err
			}
			start += len(page.Items)
			count += len(records)
			if err := s.db.EmbyProgress(ctx, scope, "running", count, ""); err != nil {
				return count, err
			}
			if time.Since(lastProgress) >= 10*time.Second {
				logEmby(ctx, "DEBUG", fmt.Sprintf("媒体库 %s：已读取 %d/%d 条，累计暂存 %d 条索引", library, start, expected, count))
				lastProgress = time.Now()
			}
			if start >= expected {
				break
			}
		}
		logEmby(ctx, "INFO", fmt.Sprintf("媒体库 %s 读取完成，暂存 %d 条索引，耗时 %.2f 秒（尚未发布）", library, count-libraryCount, time.Since(libraryStarted).Seconds()))
	}
	return count, nil
}
func (s *EmbyService) applyEvent(ctx context.Context, c EmbyConfig, generation string, event model.EmbyEvent) (resultErr error) {
	ctx = s.logContext(ctx, c, fmt.Sprintf("EMBY-EVENT-%d", event.ID), "Webhook / 刮削补查")
	defer func() { resultErr = embyFailure(ctx, resultErr) }()
	logEmby(ctx, "DEBUG", fmt.Sprintf("开始处理条目 %s，尝试 %d/6", event.ItemID, event.Attempts+1))
	workCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	items, remove, fetchErr := s.eventItems(workCtx, c, event)
	if ctx.Err() != nil {
		logEmby(ctx, "WARNING", "通知处理已中断，保留队列供后续处理")
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if embyScope(s.config) != embyScope(c) {
		logEmby(ctx, "WARNING", "同步配置已改变，本次通知未应用到索引")
		return nil
	}
	current, err := s.db.EmbyEventForItem(ctx, embyScope(c), event.ItemID)
	if err != nil {
		return err
	}
	if current == nil || current.ID != event.ID {
		logEmby(ctx, "DEBUG", "通知已被合并或处理，本次结果未重复应用")
		return nil
	}
	if fetchErr != nil {
		if errors.Is(fetchErr, errEmbyMetadataPending) {
			event.Kind = "metadata"
			// Local detection needs Emby identity and episode numbers, not TMDB.
			// Commit the complete fetched records and the bounded metadata retry together.
			if err := s.db.EmbyApplyMetadataEvent(ctx, embyScope(c), generation, event, items, fetchErr.Error()); err != nil {
				return err
			}
		} else {
			if err := s.db.EmbyRetryEvent(ctx, event, fetchErr.Error()); err != nil {
				return err
			}
		}
		if event.Attempts+1 >= 6 {
			if errors.Is(fetchErr, errEmbyMetadataPending) {
				logEmby(ctx, "ERROR", fmt.Sprintf("条目 %s 资料补查已达 6 次尝试上限，可用本地索引已保存；补充资料后发送更新通知或全量同步：%s", event.ItemID, embyLogError(fetchErr)))
			} else {
				logEmby(ctx, "ERROR", fmt.Sprintf("条目 %s 处理失败，已达 6 次尝试上限，请修复后发送更新通知或全量同步：%s", event.ItemID, embyLogError(fetchErr)))
			}
		} else {
			level := "WARNING"
			state := "暂未更新"
			if errors.Is(fetchErr, errEmbyMetadataPending) {
				level = "DEBUG"
				state = "可用本地索引已保存，资料待补充"
			}
			logEmby(ctx, level, fmt.Sprintf("条目 %s %s，%d 秒后重试（%d/6）：%s", event.ItemID, state, 30*(1<<event.Attempts), event.Attempts+1, embyLogError(fetchErr)))
		}
		return nil
	}
	if err := s.db.EmbyApplyEvent(ctx, embyScope(c), generation, event, items, remove); err != nil {
		return err
	}
	if remove {
		logEmby(ctx, "INFO", fmt.Sprintf("条目 %s 本地索引清理完成（不存在、范围外或不符合索引条件），未删除 Emby 文件，耗时 %.2f 秒", event.ItemID, embyElapsed(ctx)))
	} else {
		logEmby(ctx, "INFO", fmt.Sprintf("条目 %s 索引更新完成，写入 %d 条，耗时 %.2f 秒", event.ItemID, len(items), embyElapsed(ctx)))
	}
	return nil
}
func (s *EmbyService) eventItems(ctx context.Context, c EmbyConfig, event model.EmbyEvent) ([]model.EmbyItem, bool, error) {
	client := emby.New(c.URL, c.APIKey)
	item, err := client.Item(ctx, event.ItemID, false)
	if err != nil {
		return nil, false, err
	}
	if item == nil {
		if event.Kind == "delete" {
			logEmby(ctx, "DEBUG", "Emby 条目已不存在，准备移除本地索引")
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("Emby 条目暂不可读，等待媒体扫描完成")
	}
	library := ""
	for _, id := range c.LibraryIDs {
		if item.ID == id {
			library = id
			break
		}
		belongs, err := client.InLibrary(ctx, id, item.ID)
		if err != nil {
			return nil, false, err
		}
		if belongs {
			library = id
			break
		}
	}
	if library == "" {
		logEmby(ctx, "DEBUG", "条目不在所选媒体库范围内，不纳入索引；如有旧索引则清理")
		return nil, true, nil
	}
	if item.IsVirtualItem || item.LocationType == "Virtual" {
		logEmby(ctx, "DEBUG", "条目为虚拟媒体，不纳入索引；如有旧索引则清理")
		return nil, true, nil
	}
	record, ok := item.Record(library)
	records := make([]model.EmbyItem, 0, 4)
	var metadataErr error
	if ok {
		if (item.Type == "Movie" || item.Type == "Series") && record.TMDB == "" {
			metadataErr = fmt.Errorf("%w：尚未填写 TMDB ID", errEmbyMetadataPending)
		}
		records = append(records, record)
	}
	if item.Type == "Episode" || item.Type == "Season" {
		if item.SeriesID == "" || (item.Type == "Episode" && (record.Season < 0 || record.Episode < 0)) {
			metadataErr = fmt.Errorf("%w：缺少剧集 ID、季号或集号", errEmbyMetadataPending)
		}
		if item.SeriesID == "" {
			return records, false, metadataErr
		}
		series, err := client.Item(ctx, item.SeriesID, false)
		if err != nil {
			return nil, false, err
		}
		if series == nil {
			return nil, false, fmt.Errorf("Emby 剧集尚未就绪")
		}
		r, valid := series.Record(library)
		if !valid {
			return nil, false, fmt.Errorf("Emby 剧集条目暂不可用于索引")
		}
		if r.TMDB == "" && metadataErr == nil {
			metadataErr = fmt.Errorf("%w：剧集 TMDB ID 尚未刮削", errEmbyMetadataPending)
		}
		records = append(records, r)
	}
	// 分组通知只给出剧集或季时，分页读取该目录，避免漏掉其中的分集。
	if item.Type == "Series" || item.Type == "Season" || item.Type == "Folder" || item.Type == "CollectionFolder" {
		for start := 0; ; {
			page, err := client.Items(ctx, item.ID, start)
			if err != nil {
				return nil, false, err
			}
			if len(page.Items) == 0 && start < page.TotalRecordCount {
				return nil, false, fmt.Errorf("Emby 子目录分页未完成")
			}
			for _, child := range page.Items {
				if r, valid := child.Record(library); valid {
					records = append(records, r)
				}
			}
			start += len(page.Items)
			if start >= page.TotalRecordCount {
				break
			}
			if start > 100000 {
				return nil, false, fmt.Errorf("通知目录过大，请手动全量同步")
			}
		}
	}
	if metadataErr != nil {
		return records, false, metadataErr
	}
	return records, !ok && len(records) == 0, nil
}
func (s *EmbyService) Matches(ctx context.Context, requests []model.EmbyMatch, detail bool) ([]model.EmbyMatch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.config
	state, err := s.db.EmbyState(ctx, embyScope(c))
	if err != nil {
		return nil, err
	}
	ready := state.Active != ""
	results := make([]model.EmbyMatch, 0, len(requests))
	for _, request := range requests {
		result := model.EmbyMatch{Kind: request.Kind, TMDB: request.TMDB, State: "unknown", Items: []model.EmbyItem{}}
		if ready {
			result.Items, err = s.db.EmbyMatch(ctx, embyScope(c), state.Active, request.Kind, request.TMDB, detail)
			if err != nil {
				return nil, err
			}
			// 入库结果以当前范围最近发布的完整快照为准，通知成功后增量更新。
			// 同步、连接及刮削异常由状态接口单独展示，不覆盖其他影视的匹配结果。
			result.State = "absent"
			if len(result.Items) > 0 {
				result.State = "present"
			}
		}
		if detail {
			seen := make(map[string]bool)
			for _, item := range result.Items {
				if item.Type != "Episode" || item.Season < 0 || item.Episode < 0 {
					continue
				}
				end := item.EndEpisode
				if end < item.Episode || end-item.Episode > 100 {
					end = item.Episode
				}
				for n := item.Episode; n <= end; n++ {
					seen[strconv.Itoa(item.Season)+":"+strconv.Itoa(n)] = true
				}
			}
			result.EpisodeCount = len(seen)
		}
		results = append(results, result)
	}
	return results, nil
}

type EmbyVersion struct {
	ItemID  string        `json:"itemId"`
	Name    string        `json:"name"`
	URL     string        `json:"url"`
	Sources []emby.Source `json:"sources"`
}

type EmbyVersionQuery struct {
	Season, Episode *int
	Offset          int
}
type EmbyVersionPage struct {
	Versions   []EmbyVersion `json:"versions"`
	Total      int           `json:"total"`
	NextOffset int           `json:"nextOffset"`
	HasMore    bool          `json:"hasMore"`
}

func (s *EmbyService) Versions(ctx context.Context, kind, tmdb string, query EmbyVersionQuery) (EmbyVersionPage, error) {
	page := EmbyVersionPage{Versions: make([]EmbyVersion, 0, 20)}
	if query.Offset < 0 || (kind == "tv" && (query.Season == nil || query.Episode == nil || *query.Season < 0 || *query.Episode < 0)) {
		return page, fmt.Errorf("剧集媒体版本需要有效的季号、集号及分页位置")
	}
	c := s.snapshot()
	results, err := s.Matches(ctx, []model.EmbyMatch{{Kind: kind, TMDB: tmdb}}, true)
	if err != nil {
		return page, err
	}
	if embyScope(s.snapshot()) != embyScope(c) {
		return page, fmt.Errorf("Emby 配置已更改，请重新读取媒体版本")
	}
	client := emby.New(c.URL, c.APIKey)
	selected := make([]model.EmbyItem, 0, len(results[0].Items))
	for _, record := range results[0].Items {
		if kind == "movie" && record.Type == "Movie" {
			selected = append(selected, record)
		}
		if kind == "tv" && record.Type == "Episode" && record.Season == *query.Season {
			end := record.EndEpisode
			if end < record.Episode || end-record.Episode > 100 {
				end = record.Episode
			}
			if *query.Episode >= record.Episode && *query.Episode <= end {
				selected = append(selected, record)
			}
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })
	page.Total = len(selected)
	start := min(query.Offset, len(selected))
	end := min(start+20, len(selected))
	page.NextOffset = end
	page.HasMore = end < len(selected)
	for _, record := range selected[start:end] {
		item, err := client.Item(ctx, record.ID, true)
		if err != nil {
			return page, err
		}
		if item == nil {
			continue
		}
		fresh, valid := item.Record(record.LibraryID)
		if !valid || fresh.Type != record.Type {
			continue
		}
		if kind == "movie" && fresh.TMDB != "" && fresh.TMDB != tmdb {
			continue
		}
		if kind == "tv" {
			last := fresh.EndEpisode
			if last < fresh.Episode || last-fresh.Episode > 100 {
				last = fresh.Episode
			}
			if fresh.SeriesID != record.SeriesID || fresh.Season != *query.Season || *query.Episode < fresh.Episode || *query.Episode > last {
				continue
			}
		}
		page.Versions = append(page.Versions, EmbyVersion{ItemID: record.ID, Name: item.Name, URL: c.URL + "/web/index.html#!/item?id=" + url.QueryEscape(item.ID) + "&serverId=" + url.QueryEscape(c.ServerID), Sources: item.MediaSources})
	}
	return page, nil
}

// 通知 ID 同时接受字符串和数字，避免大整数经浮点转换后失真。
type embyWebhookID string

func (id *embyWebhookID) UnmarshalJSON(data []byte) error {
	var value string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
	} else {
		value = string(data)
	}
	if value == "null" {
		value = ""
	}
	if len(value) > 128 {
		return fmt.Errorf("Emby 条目 ID 过长")
	}
	*id = embyWebhookID(value)
	return nil
}

type embyWebhook struct {
	Event  string
	Server struct {
		ID string `json:"Id"`
	}
	Item struct {
		ID embyWebhookID `json:"Id"`
	}
	ItemID embyWebhookID `json:"ItemId"`
}

func (s *EmbyService) Webhook(ctx context.Context, token string, data []byte) (status int, resultErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.config
	if c.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(token), []byte(c.WebhookSecret)) != 1 {
		if time.Since(s.webhookRejectAt) >= time.Minute {
			s.webhookRejectAt = time.Now()
			logEmby(s.logContext(ctx, c, "EMBY-WEBHOOK", "Webhook 接收"), "WARNING", "Webhook 尚未配置或密钥无效，通知未接收（同类提醒每分钟最多一次）")
		}
		return 401, fmt.Errorf("Webhook 密钥无效")
	}
	// Rate-limit unauthenticated traffic above; never record tokens or bodies.
	ctx = s.logContext(ctx, c, s.newLogTask(), "Webhook 接收")
	defer func() {
		if resultErr != nil {
			level := "WARNING"
			if status >= 500 {
				level = "ERROR"
			}
			logEmby(ctx, level, "Webhook 接收失败："+embyLogError(resultErr))
		}
	}()
	var payload embyWebhook
	if err := json.Unmarshal(data, &payload); err != nil {
		return 400, fmt.Errorf("Webhook JSON 格式无效")
	}
	if payload.Server.ID != c.ServerID {
		return 403, fmt.Errorf("Webhook 服务器 ID 不匹配")
	}
	event := strings.ToLower(strings.TrimSpace(payload.Event))
	if len(event) > 100 {
		return 400, fmt.Errorf("Webhook 事件名称过长")
	}
	scope := embyScope(c)
	// Emby 的不同通知入口使用不同测试事件名称，均只验证连接。
	if event == "system.webhooktest" || event == "system.notificationtest" {
		if err := s.db.EmbyWebhook(ctx, scope, event, ""); err != nil {
			return 503, err
		}
		logEmby(ctx, "INFO", "收到 Emby 测试通知（"+event+"），仅验证连通性，未修改索引")
		return 200, nil
	}
	kind := ""
	switch event {
	case "library.new", "library.itemadded", "item.added", "library.updated", "library.itemupdated", "item.updated":
		kind = "upsert"
	case "library.deleted", "library.itemremoved", "item.removed":
		kind = "delete"
	}
	id := string(payload.Item.ID)
	if id == "" {
		id = string(payload.ItemID)
	}
	if kind == "" || id == "" {
		message := "收到未支持的事件或缺少 Item.Id，请核对实际媒体通知格式"
		if err := s.db.EmbyWebhook(ctx, scope, event, message); err != nil {
			return 503, err
		}
		logEmby(ctx, "WARNING", message+"，未加入处理队列")
		return 202, nil
	}
	if err := s.db.EmbyEnqueue(ctx, scope, id, kind); err != nil {
		return 503, err
	}
	if err := s.db.EmbyWebhook(ctx, scope, event, ""); err != nil {
		return 503, err
	}
	queued, readErr := s.db.EmbyEventForItem(ctx, scope, id)
	if readErr == nil && queued != nil {
		ctx = s.logContext(ctx, c, fmt.Sprintf("EMBY-EVENT-%d", queued.ID), "Webhook 接收")
	}
	logEmby(ctx, "DEBUG", fmt.Sprintf("收到 %s，条目 %s 已入队（同条目待处理通知合并），约 10 秒后尝试处理；等待全量同步或刮削时会延后", event, id))
	return 202, nil
}
