package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/client/p115"
)

type PlaybackBinding struct {
	UserID    string `json:"userId"`
	UserName  string `json:"userName"`
	AccountID string `json:"accountId"`
	ParentCID string `json:"-"` // Request snapshot only; configured on the selected 115 account.
}
type PlaybackCacheConfig struct {
	EmbyURL        string            `json:"embyUrl"`
	CleanupEnabled bool              `json:"cleanupEnabled"`
	RetentionHours int               `json:"retentionHours"`
	Bindings       []PlaybackBinding `json:"bindings"`
}
type PlaybackCacheEntry struct {
	MaterialID      string                 `json:"materialId,omitempty"`
	SharedDirectory bool                   `json:"sharedDirectory,omitempty"`
	Key             string                 `json:"key"`
	AccountID       string                 `json:"accountId"`
	UID             string                 `json:"uid"`
	ParentCID       string                 `json:"parentCid"`
	DirectoryID     string                 `json:"directoryId"`
	DirectoryName   string                 `json:"directoryName"`
	File            p115.DrivePlaybackFile `json:"file"`
	Status          string                 `json:"status"`
	LastUsed        time.Time              `json:"lastUsed"`
	CreatedAt       time.Time              `json:"createdAt"`
	Message         string                 `json:"message"`
	RetryNotBefore  time.Time              `json:"retryNotBefore,omitempty"`
	SourceKey       string                 `json:"sourceKey,omitempty"` // Hash only; no STRM, Cookie or URL.
	TargetScope     string                 `json:"targetScope,omitempty"`
}
type PlaybackCacheStatus struct {
	Materials   []PlaybackMaterial       `json:"materials"`
	Directories []PlaybackCacheDirectory `json:"directories,omitempty"`
	Config      PlaybackCacheConfig      `json:"config"`
	Entries     []PlaybackCacheEntry     `json:"entries"`
}
type playbackCacheClient interface {
	ValidateCacheParent(context.Context, string, string) error
	DownloadInfoWithUA(context.Context, *p115.ShareInfo, string, string, string) (*p115.DownloadInfo, error)
	DownloadInstantInfoWithUA(context.Context, *p115.ShareInfo, string, string, string) (*p115.DownloadInfo, error)
	PickCodeInstantInfo(context.Context, string, string) (*p115.DownloadInfo, error)
	DriveDownloadURLWithUA(context.Context, string, string, string, string) (string, error)
}
type PlaybackCache struct {
	foregroundWaiting atomic.Int64
	sourceMappings    map[string]string // bounded, memory-only; guarded by gate
	mu                sync.Mutex
	gate              chan struct{}
	store             accountSecretStore
	accounts          *P115Accounts
	client            playbackCacheClient
	logs              *EmbyProxyLogs
	doc               PlaybackCacheStatus
}

func NewPlaybackCache(ctx context.Context, store *SessionStore, accounts *P115Accounts, client playbackCacheClient, logs *EmbyProxyLogs) (*PlaybackCache, error) {
	s := &PlaybackCache{store: store, accounts: accounts, client: client, logs: logs, gate: make(chan struct{}, 1), doc: PlaybackCacheStatus{Config: PlaybackCacheConfig{RetentionHours: 72, Bindings: []PlaybackBinding{}}, Entries: []PlaybackCacheEntry{}}}
	if raw := store.Value(); raw != "" {
		if json.Unmarshal([]byte(raw), &s.doc) != nil || len(s.doc.Entries) > 2000 || len(s.doc.Directories) > 1000 || len(s.doc.Materials) > maxPlaybackMaterials {
			return nil, fmt.Errorf("播放缓存配置或记录损坏")
		}
		if err := validatePlaybackMaterials(s.doc.Materials); err != nil {
			return nil, err
		}
	}
	changed := s.doc.Config.CleanupEnabled
	s.doc.Config.CleanupEnabled = false
	for i := range s.doc.Directories {
		if s.doc.Directories[i].Status == "creating" {
			s.doc.Directories[i].Status = "uncertain"
			changed = true
		}
	}
	for i := range s.doc.Entries {
		e := &s.doc.Entries[i]
		if e.Status == "creating" || e.Status == "uploading" || e.Status == "deleting" {
			e.Status = "uncertain"
			e.Message = "服务中断；下次播放先按 SHA1 查找，冷却结束且未命中时允许重新秒传"
			changed = true
		}
	}
	if changed {
		if err := s.saveLocked(s.doc); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *PlaybackCache) Close() {}
func (s *PlaybackCache) Status() PlaybackCacheStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	doc.Config.Bindings = append([]PlaybackBinding{}, doc.Config.Bindings...)
	doc.Entries = append([]PlaybackCacheEntry{}, doc.Entries...)
	doc.Directories = append([]PlaybackCacheDirectory{}, doc.Directories...)
	doc.Materials = clonePlaybackMaterials(doc.Materials)
	return doc
}
func (s *PlaybackCache) saveLocked(doc PlaybackCacheStatus) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("编码播放缓存记录: %w", err)
	}
	if s.store.Save(string(raw)) != nil {
		return fmt.Errorf("播放缓存记录保存失败，已停止云端操作")
	}
	s.doc = doc
	return nil
}
func (s *PlaybackCache) Apply(c PlaybackCacheConfig) error {
	return s.applyConfig(c, false)
}

// Only replace binding fields; obsolete cleanup fields remain compatibility data.
func (s *PlaybackCache) ApplyBindings(embyURL string, bindings []PlaybackBinding) error {
	return s.applyConfig(PlaybackCacheConfig{EmbyURL: embyURL, Bindings: bindings, RetentionHours: 72}, true)
}

func (s *PlaybackCache) ApplyCleanup(enabled bool, hours int) error {
	if hours < 24 || hours > 720 {
		return fmt.Errorf("缓存保留时间须为 24～720 小时")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	if enabled {
		return fmt.Errorf("网盘文件自动清理已停用，请在 115 中手动删除")
	}
	doc.Config.CleanupEnabled = false
	doc.Config.RetentionHours = hours
	return s.saveLocked(doc)
}

var ErrPlaybackBindingChanged = errors.New("该用户绑定已变化，请刷新后重试")

// Update only this user's binding under lock; other cards remain intact.
func (s *PlaybackCache) UpdateBinding(embyURL string, binding PlaybackBinding, expectedAccountID string, remove bool) error {
	normalized, err := emby.NormalizeURL(embyURL)
	if err != nil {
		return err
	}
	if !instantRunID.MatchString(binding.UserID) || len([]rune(binding.UserName)) > 100 || strings.ContainsAny(binding.UserName, "\r\n\x00") {
		return fmt.Errorf("Emby 用户信息无效")
	}
	if !remove {
		if err := s.accounts.ValidateSecondary(binding.AccountID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	if len(doc.Config.Bindings) > 0 && doc.Config.EmbyURL != normalized {
		return fmt.Errorf("Emby 服务已变化，请先移除原服务的用户绑定")
	}
	index, current := -1, ""
	for i, b := range doc.Config.Bindings {
		if b.UserID == binding.UserID {
			index, current = i, b.AccountID
			break
		}
	}
	if current != expectedAccountID {
		return ErrPlaybackBindingChanged
	}
	doc.Config.Bindings = append([]PlaybackBinding{}, doc.Config.Bindings...)
	if remove {
		if index >= 0 {
			doc.Config.Bindings = append(doc.Config.Bindings[:index], doc.Config.Bindings[index+1:]...)
		}
	} else if index >= 0 {
		doc.Config.Bindings[index] = binding
	} else {
		if len(doc.Config.Bindings) >= 100 {
			return fmt.Errorf("最多配置 100 个 Emby 用户绑定")
		}
		doc.Config.Bindings = append(doc.Config.Bindings, binding)
	}
	doc.Config.EmbyURL = normalized
	return s.saveLocked(doc)
}

func (s *PlaybackCache) applyConfig(c PlaybackCacheConfig, bindingsOnly bool) error {
	if c.RetentionHours < 24 || c.RetentionHours > 720 {
		return fmt.Errorf("缓存保留时间须为 24～720 小时")
	}
	normalized, err := emby.NormalizeURL(c.EmbyURL)
	if err != nil {
		return err
	}
	c.EmbyURL = normalized
	if len(c.Bindings) > 100 {
		return fmt.Errorf("最多配置 100 个 Emby 用户绑定")
	}
	seen := make(map[string]bool, len(c.Bindings))
	for _, b := range c.Bindings {
		if !instantRunID.MatchString(b.UserID) || seen[b.UserID] || len([]rune(b.UserName)) > 100 || strings.ContainsAny(b.UserName, "\r\n\x00") {
			return fmt.Errorf("Emby 用户 ID 无效或重复")
		}
		seen[b.UserID] = true
		if err := s.accounts.ValidateSecondary(b.AccountID); err != nil {
			return err
		}
	}
	c.Bindings = append([]PlaybackBinding{}, c.Bindings...)
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	if bindingsOnly {
		c.CleanupEnabled = doc.Config.CleanupEnabled
		c.RetentionHours = doc.Config.RetentionHours
	}
	c.CleanupEnabled = false
	doc.Config = c
	return s.saveLocked(doc)
}

type PlaybackCacheSelection struct {
	donors          []P115Account // Request snapshot only; never persisted or returned by Status.
	binding         PlaybackBinding
	accountName     string
	accountRevision uint64
	cookie, uid     string
	err             error
}

// Selection is captured with proxy settings at request start, before network I/O.
type PlaybackCacheSnapshot struct {
	embyURL    string
	selections map[string]PlaybackCacheSelection
}

func (s *PlaybackCache) Snapshot() PlaybackCacheSnapshot {
	c := s.Status().Config
	// Freeze all eligible source credentials before the request starts network I/O.
	s.accounts.mu.RLock()
	donors := make([]P115Account, 0, len(s.accounts.value.Accounts))
	for _, a := range s.accounts.value.Accounts {
		if !a.Primary && a.Cookie != "" && p115.CookieUserID(a.Cookie) == a.UID {
			donors = append(donors, a)
		}
	}
	s.accounts.mu.RUnlock()
	snap := PlaybackCacheSnapshot{embyURL: c.EmbyURL, selections: make(map[string]PlaybackCacheSelection, len(c.Bindings))}
	for _, b := range c.Bindings {
		a, err := s.accounts.Get(b.AccountID)
		b.ParentCID = a.PlaybackParentCID
		if err == nil && (a.Cookie == "" || p115.CookieUserID(a.Cookie) == "") {
			err = fmt.Errorf("绑定的朋友账号未登录")
		}
		snap.selections[b.UserID] = PlaybackCacheSelection{donors: donors, binding: b, accountName: a.Name, accountRevision: a.Revision, cookie: a.Cookie, uid: p115.CookieUserID(a.Cookie), err: err}
	}
	return snap
}
func (snap PlaybackCacheSnapshot) Select(embyURL, userID string) (*PlaybackCacheSelection, error) {
	if len(snap.selections) == 0 {
		return nil, nil // Unbound users use the primary account captured by the caller.
	}
	if snap.embyURL != embyURL {
		return nil, fmt.Errorf("绑定的 Emby 服务已变化，请重新保存用户绑定")
	}
	selected, ok := snap.selections[userID]
	if !ok {
		return nil, nil
	}
	if selected.err != nil || selected.cookie == "" {
		return nil, fmt.Errorf("绑定账号不可用，播放已暂停且保留绑定；请恢复账号或由管理员更换绑定，不会自动切回主账号")
	}
	return &selected, nil
}

type playbackPrewarmKey struct{}

func PlaybackPrewarmContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, playbackPrewarmKey{}, true)
}

func (s *PlaybackCache) enter(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if warm, _ := ctx.Value(playbackPrewarmKey{}).(bool); warm {
		if s.foregroundWaiting.Load() > 0 {
			return fmt.Errorf("正式播放正在等待，跳过本次预热")
		}
		select {
		case s.gate <- struct{}{}:
			return nil
		default:
			return fmt.Errorf("播放缓存正在使用，跳过本次预热")
		}
	}
	s.foregroundWaiting.Add(1)
	defer s.foregroundWaiting.Add(-1)
	select {
	case s.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *PlaybackCache) put(e PlaybackCacheEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	doc.Entries = append([]PlaybackCacheEntry{}, doc.Entries...)
	for i := range doc.Entries {
		if doc.Entries[i].Key == e.Key {
			doc.Entries[i] = e
			return s.saveLocked(doc)
		}
	}
	if len(doc.Entries) >= 2000 {
		return fmt.Errorf("文件记录达到上限，请在缓存管理移除不再需要的本地记录后重试")
	}
	doc.Entries = append(doc.Entries, e)
	return s.saveLocked(doc)
}
func cacheMatches(f p115.DrivePlaybackFile, info *p115.DownloadInfo) bool {
	return info != nil && len(info.SHA1) == 40 && strings.EqualFold(f.SHA1, info.SHA1)
}

// PlaybackResolution contains display metadata only; it never contains credentials or URLs.
type PlaybackResolution struct {
	Mode         string
	AccountLabel string
	UsedSource   bool
	FileCacheKey string
	FileCacheID  string
}

func (s *PlaybackCacheSelection) DirectLinkScope() string {
	if s == nil {
		return "unbound"
	}
	return DirectLinkKey(s.binding.UserID, s.binding.AccountID, s.binding.ParentCID, s.cookie, strconv.FormatUint(s.accountRevision, 10))
}

func (s *PlaybackCacheSelection) UsesSourceAccount(accountID string) bool {
	return s != nil && s.binding.AccountID == accountID
}

// Pickcodes are owned by the configured source. Never probe the friend's drive
// or a share endpoint using the source pickcode.
func (s *PlaybackCache) ResolvePickCodeWithReport(ctx context.Context, selection *PlaybackCacheSelection, pickCode, sourceCookie, ua string, sourceAccountIDs ...string) (string, PlaybackResolution, error) {
	var report PlaybackResolution
	if selection == nil || selection.cookie == "" || selection.err != nil {
		return "", report, fmt.Errorf("未选择有效的朋友账号")
	}
	report.AccountLabel = PlaybackAccountLabel(selection.accountName, selection.uid, selection.binding.AccountID)
	s.logs.Add(ctx, "INFO", "pickcode 属于配置的源账号，直接进入朋友账号秒传缓存流程；目标账号="+report.AccountLabel)
	var sourceID string
	if len(sourceAccountIDs) > 0 {
		sourceID = sourceAccountIDs[0]
	}
	ctx = context.WithValue(ctx, playbackSourceKey{}, DirectLinkKey(sourceID, sourceCookie, pickCode, selection.DirectLinkScope()))
	origin := DirectLinkKey("pickcode-material", sourceID, pickCode)
	if sourceID == "" {
		origin = DirectLinkKey("pickcode-material", sourceCookie, pickCode)
	}
	ctx = context.WithValue(ctx, playbackMaterialOrigin{}, origin)
	link, err := s.resolveInstant(ctx, selection, sourceCookie, ua, &report, func() (*p115.DownloadInfo, error) {
		if lazy, ok := s.client.(interface {
			PlaybackPickCodeSource(context.Context, string, string, string) (*p115.DownloadInfo, error)
		}); ok {
			return lazy.PlaybackPickCodeSource(ctx, sourceID, pickCode, sourceCookie)
		}
		return s.client.PickCodeInstantInfo(ctx, pickCode, sourceCookie)
	})
	if err != nil {
		return "", PlaybackResolution{}, err
	}
	return link, report, nil
}

// A cached target link must not outlive a removed/uncertain file-cache record.
// Refresh last use on link reuse, serialized with local record mutations.
func (s *PlaybackCache) TouchDirectLink(ctx context.Context, key, fileID string) error {
	if key == "" {
		return nil
	}
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	for _, e := range s.Status().Entries {
		if e.Key == key && e.Status == "ready" && e.File.ID == fileID {
			e.LastUsed = time.Now().UTC()
			return s.put(e)
		}
	}
	return fmt.Errorf("秒传缓存记录已变化，需重新解析直链")
}

func (s *PlaybackCache) Resolve(ctx context.Context, selection *PlaybackCacheSelection, share *p115.ShareInfo, fileID, sourceCookie, ua string) (string, error) {
	link, _, err := s.ResolveWithReport(ctx, selection, share, fileID, sourceCookie, ua)
	return link, err
}

func (s *PlaybackCache) ResolveWithReport(ctx context.Context, selection *PlaybackCacheSelection, share *p115.ShareInfo, fileID, sourceCookie, ua string) (string, PlaybackResolution, error) {
	var report PlaybackResolution
	link, err := s.resolve(ctx, selection, share, fileID, sourceCookie, ua, &report)
	if err != nil {
		return "", PlaybackResolution{}, err
	}
	return link, report, nil
}

func (s *PlaybackCache) resolve(ctx context.Context, selection *PlaybackCacheSelection, share *p115.ShareInfo, fileID, sourceCookie, ua string, report *PlaybackResolution) (string, error) {
	if selection == nil || selection.cookie == "" || selection.err != nil {
		return "", fmt.Errorf("未选择有效的朋友账号")
	}
	report.AccountLabel = PlaybackAccountLabel(selection.accountName, selection.uid, selection.binding.AccountID)
	s.logs.Add(ctx, "INFO", "优先使用朋友账号读取分享直链，账号="+report.AccountLabel)
	direct, err := s.client.DownloadInfoWithUA(ctx, share, fileID, selection.cookie, ua)
	if err == nil {
		if direct == nil || !p115.AllowedDirectPlaybackURL(direct.URL) {
			return "", fmt.Errorf("朋友分享直链无效或要求额外凭据，无法直接 302 播放")
		}
		s.logs.Add(ctx, "INFO", "朋友账号读取分享成功，直接播放；无需秒传或创建缓存")
		report.Mode = "朋友分享直取"
		return direct.URL, nil
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("朋友分享取链失败：%s", p115.SharePlaybackFailureReason(ctx.Err()))
	}
	if !errors.Is(err, p115.ErrShareNotReadable) {
		return "", fmt.Errorf("朋友分享取链失败：%s；未尝试源账号秒传", p115.SharePlaybackFailureReason(err))
	}
	s.logs.Add(ctx, "INFO", "朋友账号无法读取此分享文件，尝试配置的源账号辅助秒传（不返回源账号直链）")
	if share != nil {
		ctx = context.WithValue(ctx, playbackMaterialOrigin{}, DirectLinkKey("share-material", share.Code, share.Password, fileID))
	}
	return s.resolveInstant(ctx, selection, sourceCookie, ua, report, func() (*p115.DownloadInfo, error) {
		return s.client.DownloadInstantInfoWithUA(ctx, share, fileID, sourceCookie, p115.PlaybackUserAgent)
	})
}

func (s *PlaybackCache) resolveInstant(ctx context.Context, selection *PlaybackCacheSelection, sourceCookie, ua string, report *PlaybackResolution, loadSource func() (*p115.DownloadInfo, error)) (string, error) {
	fast, ok := s.client.(fastPlaybackClient)
	if !ok {
		return "", fmt.Errorf("播放客户端不支持 SHA1 秒传流程")
	}
	return s.resolveFastInstant(ctx, fast, selection, sourceCookie, ua, report, loadSource)
}

// Kept for callers of the retired API. Cloud files are only deleted by the user.
func (s *PlaybackCache) Cleanup(ctx context.Context) error { return ctx.Err() }
func (s *PlaybackCache) ForgetLocked(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	doc.Entries = make([]PlaybackCacheEntry, 0, len(s.doc.Entries))
	for _, e := range s.doc.Entries {
		if e.Key != key {
			doc.Entries = append(doc.Entries, e)
		}
	}
	return s.saveLocked(doc)
}

// Forget drops only the local entry after administrator review; it never deletes cloud files.
func (s *PlaybackCache) Forget(ctx context.Context, key string) error {
	if len(key) != 64 {
		return fmt.Errorf("缓存编号无效")
	}
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	return s.ForgetLocked(key)
}
