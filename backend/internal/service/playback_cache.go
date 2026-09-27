package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/client/p115"
)

type PlaybackBinding struct {
	UserID    string `json:"userId"`
	UserName  string `json:"userName"`
	AccountID string `json:"accountId"`
	ParentCID string `json:"parentCid"`
}
type PlaybackCacheConfig struct {
	Enabled        bool              `json:"enabled"`
	EmbyURL        string            `json:"embyUrl"`
	CleanupEnabled bool              `json:"cleanupEnabled"`
	RetentionHours int               `json:"retentionHours"`
	Bindings       []PlaybackBinding `json:"bindings"`
}
type PlaybackCacheEntry struct {
	Key           string                 `json:"key"`
	AccountID     string                 `json:"accountId"`
	UID           string                 `json:"uid"`
	ParentCID     string                 `json:"parentCid"`
	DirectoryID   string                 `json:"directoryId"`
	DirectoryName string                 `json:"directoryName"`
	File          p115.DrivePlaybackFile `json:"file"`
	Status        string                 `json:"status"`
	LastUsed      time.Time              `json:"lastUsed"`
	CreatedAt     time.Time              `json:"createdAt"`
	Message       string                 `json:"message"`
}
type PlaybackCacheStatus struct {
	Config  PlaybackCacheConfig  `json:"config"`
	Entries []PlaybackCacheEntry `json:"entries"`
}
type playbackCacheClient interface {
	ValidateCacheParent(context.Context, string, string) error
	DownloadInfoWithUA(context.Context, *p115.ShareInfo, string, string, string) (*p115.DownloadInfo, error)
	DownloadInstantInfoWithUA(context.Context, *p115.ShareInfo, string, string, string) (*p115.DownloadInfo, error)
	InstantUpload(context.Context, *p115.DownloadInfo, string, string, func(p115.InstantProgress)) (*p115.InstantUploadResult, error)
	CreateDirectory(context.Context, string, string, string) (string, error)
	CacheDirectoryFiles(context.Context, string, string) ([]p115.DrivePlaybackFile, error)
	DriveDownloadURLWithUA(context.Context, string, string, string, string) (string, error)
	DeleteEntries(context.Context, []string, string) error
}
type PlaybackCache struct {
	mu       sync.Mutex
	gate     chan struct{}
	store    accountSecretStore
	accounts *P115Accounts
	client   playbackCacheClient
	logs     *EmbyProxyLogs
	doc      PlaybackCacheStatus
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewPlaybackCache(ctx context.Context, store *SessionStore, accounts *P115Accounts, client playbackCacheClient, logs *EmbyProxyLogs) (*PlaybackCache, error) {
	s := &PlaybackCache{store: store, accounts: accounts, client: client, logs: logs, gate: make(chan struct{}, 1), doc: PlaybackCacheStatus{Config: PlaybackCacheConfig{RetentionHours: 72, Bindings: []PlaybackBinding{}}, Entries: []PlaybackCacheEntry{}}}
	if raw := store.Value(); raw != "" {
		if json.Unmarshal([]byte(raw), &s.doc) != nil || len(s.doc.Entries) > 2000 {
			return nil, fmt.Errorf("播放缓存配置或记录损坏")
		}
	}
	changed := false
	for i := range s.doc.Entries {
		e := &s.doc.Entries[i]
		if e.Status == "creating" || e.Status == "uploading" || e.Status == "deleting" {
			e.Status = "uncertain"
			e.Message = "服务中断，需核查专用目录；不会自动重放写入"
			changed = true
		}
	}
	if changed {
		if err := s.saveLocked(s.doc); err != nil {
			return nil, err
		}
	}
	workerCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				cleanCtx, stop := context.WithTimeout(workerCtx, 10*time.Minute)
				err := s.Cleanup(cleanCtx)
				stop()
				if err != nil && workerCtx.Err() == nil {
					s.logs.Add(workerCtx, "WARNING", "播放缓存定时清理未完成，请检查缓存记录")
				}
			}
		}
	}()
	return s, nil
}
func (s *PlaybackCache) Close() { s.cancel(); s.wg.Wait() }
func (s *PlaybackCache) Status() PlaybackCacheStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	doc.Config.Bindings = append([]PlaybackBinding{}, doc.Config.Bindings...)
	doc.Entries = append([]PlaybackCacheEntry{}, doc.Entries...)
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
		if _, err := strconv.ParseUint(b.ParentCID, 10, 64); err != nil || b.ParentCID == "" {
			return fmt.Errorf("请明确填写缓存父目录，根目录为 0")
		}
		if c.Enabled {
			if err := s.accounts.ValidateSelection(b.AccountID); err != nil {
				return err
			}
		}
	}
	if c.Enabled && len(c.Bindings) == 0 {
		return fmt.Errorf("启用前请添加用户绑定")
	}
	c.Bindings = append([]PlaybackBinding{}, c.Bindings...)
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	doc.Config = c
	return s.saveLocked(doc)
}

type PlaybackCacheSelection struct {
	binding         PlaybackBinding
	accountName     string
	accountRevision uint64
	cookie, uid     string
	err             error
}

// Selection is captured with proxy settings at request start, before network I/O.
type PlaybackCacheSnapshot struct {
	enabled    bool
	embyURL    string
	selections map[string]PlaybackCacheSelection
}

func (s *PlaybackCache) Snapshot() PlaybackCacheSnapshot {
	c := s.Status().Config
	snap := PlaybackCacheSnapshot{enabled: c.Enabled, embyURL: c.EmbyURL, selections: make(map[string]PlaybackCacheSelection, len(c.Bindings))}
	for _, b := range c.Bindings {
		a, err := s.accounts.Get(b.AccountID)
		if err == nil && (a.Cookie == "" || p115.CookieUserID(a.Cookie) == "") {
			err = fmt.Errorf("绑定的朋友账号未登录")
		}
		snap.selections[b.UserID] = PlaybackCacheSelection{binding: b, accountName: a.Name, accountRevision: a.Revision, cookie: a.Cookie, uid: p115.CookieUserID(a.Cookie), err: err}
	}
	return snap
}
func (snap PlaybackCacheSnapshot) Select(embyURL, userID string) (*PlaybackCacheSelection, error) {
	if !snap.enabled {
		return nil, nil
	}
	if snap.embyURL != embyURL {
		return nil, fmt.Errorf("绑定的 Emby 服务已变化，请重新保存用户绑定")
	}
	selected, ok := snap.selections[userID]
	if !ok {
		return nil, nil
	}
	if selected.err != nil || selected.cookie == "" {
		return nil, fmt.Errorf("朋友账号已删除或登录失效，请检查绑定")
	}
	return &selected, nil
}
func (s *PlaybackCache) enter(ctx context.Context) error {
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
		return fmt.Errorf("缓存记录达到上限，请清理后重试")
	}
	doc.Entries = append(doc.Entries, e)
	return s.saveLocked(doc)
}
func cacheMatches(f p115.DrivePlaybackFile, info *p115.DownloadInfo) bool {
	return f.Size == info.Size && strings.EqualFold(f.SHA1, info.SHA1) && f.Name == info.Name
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

// A cached target link must not outlive a removed/uncertain file-cache record.
// Refresh retention on link reuse, serialized with cleanup and file mutations.
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
	if sourceCookie == "" {
		return "", fmt.Errorf("秒传使用的源账号未选择、已删除或未登录，请检查 302 账号配置")
	}
	report.UsedSource = true
	// One gate serializes mutation, reuse and cleanup. Waiters recheck the committed record.
	if err := s.enter(ctx); err != nil {
		return "", err
	}
	defer func() { <-s.gate }()
	s.logs.Add(ctx, "INFO", "朋友账号播放解析，目标账号="+selection.binding.AccountID)
	info, err := s.client.DownloadInstantInfoWithUA(ctx, share, fileID, sourceCookie, p115.PlaybackUserAgent)
	if err != nil {
		return "", fmt.Errorf("源账号读取秒传文件信息失败：%s", p115.SharePlaybackFailureReason(err))
	}
	if err := p115.ValidateInstantSource(info); err != nil {
		return "", err
	}
	s.logs.Add(ctx, "INFO", "源账号读取成功，开始检查朋友账号缓存；未命中时执行秒传")
	sum := sha256.Sum256([]byte(selection.binding.AccountID + "\x00" + selection.uid + "\x00" + selection.binding.ParentCID + "\x00" + strings.ToUpper(info.SHA1) + "\x00" + strconv.FormatInt(info.Size, 10)))
	key := hex.EncodeToString(sum[:])
	report.FileCacheKey = key
	var entry PlaybackCacheEntry
	for _, e := range s.Status().Entries {
		if e.Key == key {
			entry = e
			break
		}
	}
	if entry.Key != "" && entry.Status != "ready" {
		return "", fmt.Errorf("缓存结果待核查，请在缓存页面检查；不会重复秒传")
	}
	if entry.Key == "" {
		report.Mode = "新建秒传缓存"
		if err := s.client.ValidateCacheParent(ctx, selection.binding.ParentCID, selection.cookie); err != nil {
			return "", err
		}
		var nonce [12]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", fmt.Errorf("生成缓存目录编号失败")
		}
		entry = PlaybackCacheEntry{Key: key, AccountID: selection.binding.AccountID, UID: selection.uid, ParentCID: selection.binding.ParentCID, DirectoryName: "Cinema115-cache-" + hex.EncodeToString(nonce[:]), Status: "creating", CreatedAt: time.Now().UTC(), LastUsed: time.Now().UTC(), File: p115.DrivePlaybackFile{Name: info.Name, Size: info.Size, SHA1: info.SHA1}}
		if err := s.put(entry); err != nil {
			return "", err
		}
		cid, err := s.client.CreateDirectory(ctx, entry.ParentCID, entry.DirectoryName, selection.cookie)
		if err != nil {
			entry.Status = "uncertain"
			entry.Message = "缓存目录创建结果未确认"
			_ = s.put(entry)
			return "", fmt.Errorf("缓存目录创建结果未确认，请检查目标目录")
		}
		entry.DirectoryID = cid
		entry.Status = "uploading"
		if err := s.put(entry); err != nil {
			return "", err
		}
		// A new dedicated directory must be empty before the only supported write.
		files, err := s.client.CacheDirectoryFiles(ctx, cid, selection.cookie)
		if err != nil || len(files) != 0 {
			entry.Status = "uncertain"
			entry.Message = "新缓存目录未能确认为空"
			_ = s.put(entry)
			return "", fmt.Errorf("新缓存目录未能确认为空，已停止秒传")
		}
		s.logs.Add(ctx, "INFO", "缓存未命中，开始向朋友账号秒传")
		result, err := s.client.InstantUpload(ctx, info, cid, selection.cookie, func(p p115.InstantProgress) {
			if p.Stage == "range" {
				s.logs.Add(ctx, "INFO", "秒传二次校验，读取片段字节数="+strconv.FormatInt(p.Bytes, 10))
			}
		})
		if err != nil || result == nil {
			entry.Status = "uncertain"
			entry.Message = "秒传未确认成功，请核查专用目录"
			if err != nil {
				entry.Message = err.Error()
				s.logs.Add(ctx, "ERROR", "朋友账号秒传失败："+err.Error())
			}
			_ = s.put(entry)
			return "", fmt.Errorf("朋友账号秒传未成功，请检查缓存记录与账号权限")
		}
		files, err = s.client.CacheDirectoryFiles(ctx, cid, selection.cookie)
		if err != nil || len(files) != 1 || !cacheMatches(files[0], info) || (result.PickCode != "" && files[0].PickCode != result.PickCode) || (result.FileID != "" && files[0].ID != result.FileID) {
			entry.Status = "uncertain"
			entry.Message = "115 返回成功，目标文件校验尚未通过"
			_ = s.put(entry)
			return "", fmt.Errorf("秒传目标文件校验失败，已停止取链")
		}
		entry.File = files[0]
		entry.Status = "ready"
		entry.Message = "目标文件校验通过"
		s.logs.Add(ctx, "INFO", "朋友账号秒传成功，目标文件 SHA1、大小和标识校验通过")
	} else {
		report.Mode = "复用秒传缓存"
		files, err := s.client.CacheDirectoryFiles(ctx, entry.DirectoryID, selection.cookie)
		if err != nil || len(files) != 1 || files[0].ID != entry.File.ID || files[0].SHA1 != entry.File.SHA1 || files[0].Size != entry.File.Size || files[0].PickCode != entry.File.PickCode {
			return "", fmt.Errorf("缓存文件已删除、移动或改变，请检查缓存记录")
		}
		s.logs.Add(ctx, "INFO", "朋友账号缓存命中，复用已秒传文件")
	}
	entry.LastUsed = time.Now().UTC()
	report.FileCacheID = entry.File.ID
	if err := s.put(entry); err != nil {
		return "", err
	}
	s.logs.Add(ctx, "INFO", "使用朋友账号及当前播放器 UA 获取目标文件直链")
	link, err := s.client.DriveDownloadURLWithUA(ctx, entry.File.ID, entry.File.PickCode, selection.cookie, ua)
	if err != nil || !p115.AllowedMediaURL(link) {
		return "", fmt.Errorf("朋友账号直链读取失败，请检查账号和客户端")
	}
	return link, nil
}

// Cleanup recycles only exact recorded file IDs. The dedicated empty folders remain,
// avoiding recursive deletion of files added by the account owner during a race.
func (s *PlaybackCache) Cleanup(ctx context.Context) error {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	snapshot := s.Status()
	if !snapshot.Config.CleanupEnabled {
		return nil
	}
	cutoff := time.Now().Add(-time.Duration(snapshot.Config.RetentionHours) * time.Hour)
	// Pin every referenced account once for this cleanup task, including missing accounts.
	credentials := make(map[string]P115Account, len(snapshot.Entries))
	for _, e := range snapshot.Entries {
		if _, seen := credentials[e.AccountID]; !seen {
			a, _ := s.accounts.Get(e.AccountID)
			credentials[e.AccountID] = a
		}
	}
	for _, e := range snapshot.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Status != "ready" || !e.LastUsed.Before(cutoff) {
			continue
		}
		account := credentials[e.AccountID]
		if account.Cookie == "" || p115.CookieUserID(account.Cookie) != e.UID {
			s.logs.Add(ctx, "WARNING", "缓存清理跳过：原账号缺失、未登录或用户身份改变")
			continue
		}
		files, err := s.client.CacheDirectoryFiles(ctx, e.DirectoryID, account.Cookie)
		if err != nil || len(files) != 1 || files[0] != e.File {
			s.logs.Add(ctx, "WARNING", "缓存清理跳过：目录或文件信息已改变")
			continue
		}
		e.Status = "deleting"
		if err := s.put(e); err != nil {
			return err
		}
		if err := s.client.DeleteEntries(ctx, []string{e.File.ID}, account.Cookie); err != nil {
			e.Status = "uncertain"
			e.Message = "清理结果未确认，需检查专用目录"
			_ = s.put(e)
			s.logs.Add(ctx, "WARNING", "播放缓存清理结果未确认，停止重试")
			continue
		}
		remaining, err := s.client.CacheDirectoryFiles(ctx, e.DirectoryID, account.Cookie)
		if err != nil {
			e.Status = "uncertain"
			e.Message = "删除后目录核查失败"
			_ = s.put(e)
			continue
		}
		present := false
		for _, f := range remaining {
			if f.ID == e.File.ID {
				present = true
			}
		}
		if present {
			e.Status = "uncertain"
			e.Message = "删除后目标文件仍存在"
			_ = s.put(e)
			continue
		}
		if err := s.ForgetLocked(e.Key); err != nil {
			return err
		}
		s.logs.Add(ctx, "INFO", "过期播放缓存已移入回收站；账号="+e.AccountID+"；文件="+e.File.ID)
	}
	return nil
}
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
