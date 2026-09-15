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
}

var errEmbyMetadataPending = errors.New("Emby 刮削信息不完整")

type EmbyService struct {
	mu              sync.RWMutex
	config          EmbyConfig
	secrets         *SessionStore
	db              *storage.Store
	checkedAt       time.Time
	connectionError string
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
func (s *EmbyService) SaveWebhookBaseURL(ctx context.Context, raw string) error {
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
func (s *EmbyService) Test(ctx context.Context, input EmbyConfig) (emby.Server, []emby.Library, error) {
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
	return server, libs, err
}
func (s *EmbyService) Save(ctx context.Context, input EmbyConfig) error {
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
	s.config = c
	s.checkedAt = time.Now()
	s.connectionError = ""
	return nil
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
	return EmbyStatus{EmbyState: state, Configured: c.APIKey != "", Pending: pending, Failed: failed, Connected: connected, ConnectionError: message, EventError: eventError, MetadataPending: metadataPending, MetadataFailed: metadataFailed}, nil
}
func (s *EmbyService) StartSync(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.config
	if c.APIKey == "" || len(c.LibraryIDs) == 0 {
		return fmt.Errorf("请先保存 Emby 配置并选择媒体库")
	}
	return s.db.EmbyQueueSync(ctx, embyScope(c))
}
func (s *EmbyService) Run(ctx context.Context) error {
	if err := s.db.EmbyRecover(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
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
func (s *EmbyService) syncLibrary(ctx context.Context, c EmbyConfig) error {
	scope := embyScope(c)
	generation := strconv.FormatInt(time.Now().UnixNano(), 10)
	_, _, cutoff, err := s.db.EmbyEventStats(ctx, scope)
	if err != nil {
		return err
	}
	if err := s.db.EmbyProgress(ctx, scope, "running", 0, ""); err != nil {
		return err
	}
	workCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	count, scanErr := s.scan(workCtx, c, generation)
	if ctx.Err() != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if embyScope(s.config) != scope {
		scanErr = fmt.Errorf("媒体库配置已更改，请按新配置重新同步")
	}
	if scanErr != nil {
		if err := s.db.EmbyDiscard(ctx, scope, generation); err != nil {
			return err
		}
		return s.db.EmbyProgress(ctx, scope, "failed", count, scanErr.Error())
	}
	if err := s.db.EmbyPublish(ctx, scope, generation, cutoff); err != nil {
		return err
	}
	s.checkedAt = time.Now()
	s.connectionError = ""
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
				return count, fmt.Errorf("同步期间媒体库数量变化，请稍后重试全量同步")
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
			if err := s.db.EmbyStage(ctx, scope, generation, records); err != nil {
				return count, err
			}
			if err := s.db.EmbyQueueMetadata(ctx, scope, records); err != nil {
				return count, err
			}
			start += len(page.Items)
			count += len(records)
			if err := s.db.EmbyProgress(ctx, scope, "running", count, ""); err != nil {
				return count, err
			}
			if start >= expected {
				break
			}
		}
	}
	return count, nil
}
func (s *EmbyService) applyEvent(ctx context.Context, c EmbyConfig, generation string, event model.EmbyEvent) error {
	workCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	items, remove, fetchErr := s.eventItems(workCtx, c, event)
	if ctx.Err() != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if embyScope(s.config) != embyScope(c) {
		return nil
	}
	if fetchErr != nil {
		if errors.Is(fetchErr, errEmbyMetadataPending) {
			event.Kind = "metadata"
		}
		return s.db.EmbyRetryEvent(ctx, event, fetchErr.Error())
	}
	return s.db.EmbyApplyEvent(ctx, embyScope(c), generation, event, items, remove)
}
func (s *EmbyService) eventItems(ctx context.Context, c EmbyConfig, event model.EmbyEvent) ([]model.EmbyItem, bool, error) {
	client := emby.New(c.URL, c.APIKey)
	item, err := client.Item(ctx, event.ItemID, false)
	if err != nil {
		return nil, false, err
	}
	if item == nil {
		if event.Kind == "delete" {
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
		return nil, true, nil
	}
	if item.IsVirtualItem || item.LocationType == "Virtual" {
		return nil, true, nil
	}
	record, ok := item.Record(library)
	records := make([]model.EmbyItem, 0, 4)
	if ok {
		if (item.Type == "Movie" || item.Type == "Series") && record.TMDB == "" {
			return nil, false, fmt.Errorf("%w：尚未填写 TMDB ID", errEmbyMetadataPending)
		}
		records = append(records, record)
	}
	if item.Type == "Episode" || item.Type == "Season" {
		if item.SeriesID == "" || (item.Type == "Episode" && (record.Season < 0 || record.Episode < 0)) {
			return nil, false, fmt.Errorf("%w：缺少剧集 ID、季号或集号", errEmbyMetadataPending)
		}
		series, err := client.Item(ctx, item.SeriesID, false)
		if err != nil {
			return nil, false, err
		}
		if series == nil {
			return nil, false, fmt.Errorf("Emby 剧集尚未就绪")
		}
		r, valid := series.Record(library)
		if !valid || r.TMDB == "" {
			return nil, false, fmt.Errorf("%w：剧集 TMDB ID 尚未刮削", errEmbyMetadataPending)
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

func (s *EmbyService) Webhook(ctx context.Context, token string, data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.config
	if c.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(token), []byte(c.WebhookSecret)) != 1 {
		return 401, fmt.Errorf("Webhook 密钥无效")
	}
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
		return 202, nil
	}
	if err := s.db.EmbyEnqueue(ctx, scope, id, kind); err != nil {
		return 503, err
	}
	if err := s.db.EmbyWebhook(ctx, scope, event, ""); err != nil {
		return 503, err
	}
	return 202, nil
}
