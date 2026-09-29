package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"sync"
	"time"
)

var ErrMemberPlayback = errors.New("个人播放操作未完成")

type memberQRClient interface {
	CreateQRCode(context.Context, string) (*p115.QRCode, error)
	CheckQRCode(context.Context, string) (*p115.QRCodeStatus, error)
	CompleteQRCodeLogin(context.Context, string) (string, error)
}
type memberDriveClient interface {
	CreateDirectory(context.Context, string, string, string) (string, error)
	ValidateCacheParent(context.Context, string, string) error
}
type memberCookieChecker interface {
	CheckCookie(context.Context, string) (bool, error)
}
type memberEmbyClient interface {
	Libraries(context.Context) ([]emby.Library, error)
	Users(context.Context) ([]emby.User, error)
	ManagedUser(context.Context, string) (emby.ManagedUser, error)
	CreateManagedUser(context.Context, string) (emby.ManagedUser, error)
	SetManagedPolicy(context.Context, string, map[string]any) error
	SetManagedPassword(context.Context, string, string) error
	RenameManagedUser(context.Context, string, string) error
}
type memberQRSession struct {
	source   string
	qr       *p115.QRCode
	revision uint64
	lastPoll time.Time
}
type MemberPlaybackService struct {
	Cache       *PlaybackCache // wired before serving
	db          *storage.Store
	accounts    *P115Accounts
	emby        *EmbyService
	drive       memberDriveClient
	checker     memberCookieChecker
	qr          memberQRClient
	gate        chan struct{}
	wake        chan struct{}
	mu          sync.RWMutex
	records     map[string]model.MemberPlayback
	settings    model.MemberPlaybackSettings
	sessions    map[string]memberQRSession // guarded by gate, bounded and expires
	NewEmby     func(string, string) memberEmbyClient
	ProxyStatus func() EmbyProxyStatus // wired once before HTTP starts
}

func NewMemberPlayback(ctx context.Context, db *storage.Store, accounts *P115Accounts, server *EmbyService, drive memberDriveClient, checker memberCookieChecker, qr memberQRClient) (*MemberPlaybackService, error) {
	records, err := db.MemberPlaybackRecords(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := db.MemberPlaybackSettings(ctx)
	if err != nil {
		return nil, err
	}
	s := &MemberPlaybackService{db: db, accounts: accounts, emby: server, drive: drive, checker: checker, qr: qr, gate: make(chan struct{}, 1), records: make(map[string]model.MemberPlayback, len(records)), settings: settings, sessions: make(map[string]memberQRSession), NewEmby: func(base, key string) memberEmbyClient { return emby.New(base, key) }}
	for _, r := range records {
		if r.AccountID != "" {
			used, err := db.MemberAccountReferenced(ctx, r.UserID, r.AccountID)
			if err != nil {
				return nil, err
			}
			r, err = accounts.RepairMember(ctx, r, !used)
			if err != nil {
				return nil, fmt.Errorf("整理个人网盘归属: %w", err)
			}
		}
		s.records[r.UserID] = r
	}
	s.wake = make(chan struct{}, 1)
	return s, nil
}
func memberError(message string) error { return fmt.Errorf("%w：%s", ErrMemberPlayback, message) }
func (s *MemberPlaybackService) lock(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.gate <- struct{}{}:
		return nil
	default:
		return memberError("操作正在进行，请稍后重试")
	}
}
func (s *MemberPlaybackService) unlock() { <-s.gate }
func (s *MemberPlaybackService) record(id string) model.MemberPlayback {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.records[id]
}
func (s *MemberPlaybackService) save(ctx context.Context, r model.MemberPlayback) error {
	if err := s.db.SaveMemberPlayback(ctx, r); err != nil {
		return fmt.Errorf("保存个人播放进度: %w", err)
	}
	s.mu.Lock()
	s.records[r.UserID] = r
	s.mu.Unlock()
	return nil
}
func (s *MemberPlaybackService) Settings() model.MemberPlaybackSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	settings := s.settings
	settings.LibraryIDs = append([]string{}, settings.LibraryIDs...)
	return settings
}
func (s *MemberPlaybackService) Records() []model.MemberPlayback {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.MemberPlayback, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	return out
}
func (s *MemberPlaybackService) config() (EmbyConfig, memberEmbyClient, error) {
	c := s.emby.snapshot()
	settings := s.Settings()
	if c.APIKey == "" || c.URL != settings.EmbyURL || c.ServerID == "" || c.ServerID != settings.ServerID {
		return c, nil, memberError("Emby 服务配置已变化，请管理员重新检查")
	}
	return c, s.NewEmby(c.URL, c.APIKey), nil
}
func (s *MemberPlaybackService) SetSettings(ctx context.Context, settings model.MemberPlaybackSettings) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	c := s.emby.snapshot()
	if settings.Enabled {
		u, err := url.Parse(settings.PublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return memberError("请填写有效的 Emby 对外反代地址")
		}
		if c.APIKey == "" || c.ServerID == "" {
			return memberError("请先配置并验证 Emby 服务")
		}
		if s.ProxyStatus == nil {
			return memberError("播放反代未启动")
		}
		proxy := s.ProxyStatus()
		if !proxy.Listening || proxy.Config.URL != c.URL {
			return memberError("请先启用同一 Emby 服务的播放反代")
		}
		if len(settings.LibraryIDs) == 0 {
			return memberError("请至少选择一个允许访问的媒体库")
		}
		settings.PublicURL = strings.TrimRight(settings.PublicURL, "/")
	}
	if len(settings.LibraryIDs) > 100 {
		return memberError("媒体库数量超过限制")
	}
	ids := make([]string, 0, len(settings.LibraryIDs))
	if settings.Enabled && len(settings.LibraryIDs) > 0 {
		libraries, err := s.Libraries(ctx)
		if err != nil {
			return err
		}
		available := make(map[string]bool, len(libraries))
		for _, library := range libraries {
			available[library.ID] = library.ID != "" && library.GUID != ""
		}
		seen := make(map[string]bool)
		for _, id := range settings.LibraryIDs {
			if !available[id] {
				return memberError("所选媒体库已不存在，请刷新媒体库后重新选择")
			}
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	} else {
		ids = append(ids, settings.LibraryIDs...)
	}
	for _, r := range s.Records() {
		if settings.Enabled && (r.EmbyURL != c.URL || r.ServerID != c.ServerID) {
			return memberError("已有个人账号属于另一 Emby 服务，不能直接更换服务")
		}
	}
	settings.LibraryIDs = ids
	settings.Revision = s.Settings().Revision + 1
	settings.EmbyURL, settings.ServerID = c.URL, c.ServerID
	if !settings.Enabled && s.Settings().ServerID != "" {
		previous := s.Settings()
		settings.EmbyURL, settings.ServerID = previous.EmbyURL, previous.ServerID
	}
	if err := s.db.SaveMemberPlaybackSettings(ctx, settings); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = settings
	s.mu.Unlock()
	s.RequestSync()
	return nil
}

// Libraries reads the configured server, not a client-supplied URL or token.
func (s *MemberPlaybackService) Libraries(ctx context.Context) ([]emby.Library, error) {
	c := s.emby.snapshot()
	if c.URL == "" || c.APIKey == "" || c.ServerID == "" {
		return nil, memberError("请先配置并验证 Emby 服务")
	}
	libraries, err := s.NewEmby(c.URL, c.APIKey).Libraries(ctx)
	if err != nil {
		return nil, memberError("读取 Emby 媒体库失败，请检查服务连接")
	}
	return libraries, nil
}

// Library browsing uses ItemId, but Emby user policies require Guid. Keep the
// selected ItemIds in settings and resolve them against this server on each sync.
func memberPolicySettings(ctx context.Context, client memberEmbyClient, settings model.MemberPlaybackSettings) (model.MemberPlaybackSettings, error) {
	libraries, err := client.Libraries(ctx)
	if err != nil {
		return settings, memberError("无法读取媒体库权限编号，请检查 Emby 连接后重试")
	}
	byID := make(map[string]string, len(libraries))
	for _, library := range libraries {
		byID[library.ID] = library.GUID
	}
	guids := make([]string, 0, len(settings.LibraryIDs))
	for _, id := range settings.LibraryIDs {
		guid := byID[id]
		if guid == "" {
			return settings, memberError("所选媒体库不存在或缺少 Guid，请刷新媒体库并重新保存")
		}
		guids = append(guids, guid)
	}
	settings.LibraryIDs = guids
	return settings, nil
}

// RequestSync coalesces work into the application-owned worker.
func (s *MemberPlaybackService) RequestSync() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *MemberPlaybackService) websiteUser(ctx context.Context, id string) (model.WebsiteUser, error) {
	u, err := s.db.WebsiteUser(ctx, id)
	if err != nil || u.Disabled || u.Role != "user" {
		return u, memberError("网站账号不可用")
	}
	return u, nil
}
func (s *MemberPlaybackService) ensure(ctx context.Context, id string) (model.MemberPlayback, error) {
	_, err := s.websiteUser(ctx, id)
	if err != nil {
		return model.MemberPlayback{}, err
	}
	r := s.record(id)
	if r.UserID != "" {
		return r, nil
	}
	if !s.Settings().Enabled {
		return r, memberError("管理员尚未开放自助播放")
	}
	source, err := s.db.WebsitePlaybackSource(ctx, id)
	if err != nil {
		return r, err
	}
	c := s.emby.snapshot()
	var reservation [16]byte
	if _, err := rand.Read(reservation[:]); err != nil {
		return r, fmt.Errorf("生成 Emby 注册预约: %w", err)
	}
	r = model.MemberPlayback{UserID: id, Source: source, EmbyURL: c.URL, ServerID: c.ServerID, EmbyName: "c115_" + hex.EncodeToString(reservation[:]), Stage: "unbound"}
	return r, s.save(ctx, r)
}

type MemberPlaybackView struct {
	Adopted            bool   `json:"adopted"`
	CanPreparePersonal bool   `json:"canPreparePersonal"`
	Enabled            bool   `json:"enabled"`
	Stage              string `json:"stage"`
	Message            string `json:"message"`
	UID                string `json:"uid"`
	Connected          bool   `json:"connected"`
	DirectoryID        string `json:"directoryId"`
	EmbyName           string `json:"embyName"`
	EmbyURL            string `json:"embyUrl"`
	Suspended          bool   `json:"suspended"`
	Blocked            bool   `json:"blocked"`
	AllowDownload      bool   `json:"allowDownload"`
	SyncPending        bool   `json:"syncPending"`
	Source             string `json:"source"`
	Provisioned        bool   `json:"provisioned"`
	AccountRevision    uint64 `json:"accountRevision"`
	AccountMessage     string `json:"accountMessage"`
	SuggestedName      string `json:"suggestedName"`
	PendingName        string `json:"pendingName"`
}

func (s *MemberPlaybackService) View(id string) MemberPlaybackView {
	r := s.record(id)
	settings := s.Settings()
	drive := s.personalDrive(r)
	v := MemberPlaybackView{Enabled: settings.Enabled, Stage: r.Stage, Message: r.Message, UID: drive.UID, Connected: drive.Connected, DirectoryID: drive.DirectoryID, EmbyName: r.EmbyName, EmbyURL: settings.PublicURL, Suspended: r.Suspended, Blocked: r.AdminBlocked}
	v.AllowDownload = settings.AllowDownload
	v.Adopted = r.Adopted
	v.CanPreparePersonal = r.Source == "personal" || r.AllowPersonalSwitch
	v.Source = r.Source
	v.Provisioned = r.Provisioned
	v.AccountRevision, v.AccountMessage, v.PendingName = r.AccountRevision, r.AccountMessage, r.PendingName
	v.SuggestedName = r.RegistrationName
	if v.Source == "" {
		v.Source = "personal"
	}
	v.SyncPending = r.EmbyID != "" && (r.AppliedRevision != settings.Revision || r.SyncError != "")
	if v.SyncPending {
		v.Message = "权限正在同步，完成后自动恢复；失败时请联系管理员"
	}
	if r.UserID == "" {
		v.Stage = "unbound"
	}
	if r.EmbyID == "" {
		v.EmbyName = ""
	}
	return v
}
func (s *MemberPlaybackService) CreateQR(ctx context.Context, id string) (*p115.QRCode, error) {
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.unlock()
	if !s.Settings().Enabled {
		return nil, memberError("自助播放暂未开放")
	}
	r, err := s.ensure(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.Source != "personal" && !r.AllowPersonalSwitch {
		return nil, memberError("当前由管理员提供播放账号，无需扫码")
	}
	var revision uint64
	if r.AccountID != "" {
		a, err := s.accounts.Get(r.AccountID)
		if err != nil || a.OwnerUserID != id || a.Ownership != "member" {
			return nil, ErrAccountMissing
		}
		revision = a.Revision
	}
	for key, session := range s.sessions {
		if time.Now().After(session.qr.ExpiresAt) {
			delete(s.sessions, key)
		}
	}
	if old, ok := s.sessions[id]; ok && time.Until(old.qr.ExpiresAt) > 150*time.Second {
		return nil, memberError("请稍后再刷新二维码")
	}
	if len(s.sessions) >= 100 {
		return nil, memberError("扫码人数过多，请稍后重试")
	}
	qr, err := s.qr.CreateQRCode(ctx, "alipaymini")
	if err != nil {
		return nil, memberError("获取二维码失败")
	}
	s.sessions[id] = memberQRSession{qr: qr, revision: revision, source: r.Source}
	return qr, nil
}
func (s *MemberPlaybackService) PollQR(ctx context.Context, id, qrID string) (string, error) {
	if err := s.lock(ctx); err != nil {
		return "", err
	}
	defer s.unlock()
	if !s.Settings().Enabled {
		return "", memberError("自助播放已关闭")
	}
	if _, err := s.websiteUser(ctx, id); err != nil {
		return "", err
	}
	p, ok := s.sessions[id]
	if !ok || p.qr.ID != qrID || time.Now().After(p.qr.ExpiresAt) {
		return "", memberError("二维码已失效，请重新获取")
	}
	if time.Since(p.lastPoll) < time.Second {
		return "waiting", nil
	}
	p.lastPoll = time.Now()
	s.sessions[id] = p
	r := s.record(id)
	var account P115Account
	if r.AccountID != "" {
		account, _ = s.accounts.Get(r.AccountID)
	}
	if r.Source != p.source || (r.Source != "personal" && !r.AllowPersonalSwitch) || account.Revision != p.revision || r.AccountID != "" && (account.OwnerUserID != id || account.Ownership != "member") {
		return "", ErrAccountChanged
	}
	state, err := s.qr.CheckQRCode(ctx, p.qr.ID)
	if err != nil {
		return "", memberError("查询扫码状态失败")
	}
	if state.Status != "success" {
		return state.Status, nil
	}
	cookie := state.Cookie
	if cookie == "" {
		cookie, err = s.qr.CompleteQRCodeLogin(ctx, p.qr.ID)
		if err != nil {
			return "", memberError("扫码已确认，但登录资料获取失败，请重新扫码")
		}
	}
	valid, err := s.checker.CheckCookie(ctx, cookie)
	if err != nil || !valid || accountUID(cookie) == "" {
		return "", memberError("无法确认 115 登录状态，请重新扫码")
	}
	if _, err := s.websiteUser(ctx, id); err != nil {
		return "", err
	}
	u, err := s.websiteUser(ctx, id)
	if err != nil {
		return "", err
	}
	r, err = s.accounts.BindMember(ctx, r, u.Username, cookie, p.revision)
	if err != nil {
		return "", memberError("网盘 UID 冲突或配置已变化，请使用原账号重新扫码")
	}
	s.mu.Lock()
	s.records[id] = r
	s.mu.Unlock()
	delete(s.sessions, id)
	if r.Source != "personal" {
		_, err := s.prepareDirectory(ctx, r)
		if err != nil {
			return "", err
		}
		return "success", nil
	}
	if r.DirectoryID != "" {
		updated, err := s.accounts.Get(r.AccountID)
		if err != nil {
			return "", err
		}
		if s.drive.ValidateCacheParent(ctx, r.DirectoryID, updated.Cookie) == nil && updated.PlaybackParentCID != r.DirectoryID {
			if err := s.accounts.UpdatePlaybackDirectory(updated.ID, updated.Revision, r.DirectoryID); err != nil {
				return "", err
			}
		}
	}
	r.Suspended = false
	if r.EmbyID == "" {
		r.Stage = "bound"
	}
	r.Message = "115 已绑定，请设置 Emby 密码完成开通"
	if err := s.save(ctx, r); err != nil {
		return "", err
	}
	if r.EmbyID != "" && r.Stage != "creating" && r.Stage != "provisioning" {
		_ = s.reconcile(ctx, r)
	}
	return "success", nil
}

// Managed users are always represented, even with an unavailable account.
// Generic admin binding edits cannot remove this mandatory target.
func (s *MemberPlaybackService) Bindings(c PlaybackCacheConfig) PlaybackCacheConfig {
	records := s.Records()
	owned := make(map[string]bool, len(records))
	for _, r := range records {
		if r.EmbyID != "" {
			owned[r.EmbyID] = true
		}
	}
	bindings := make([]PlaybackBinding, 0, len(c.Bindings)+len(records))
	for _, b := range c.Bindings {
		if !owned[b.UserID] {
			bindings = append(bindings, b)
		}
	}
	for _, r := range records {
		if r.EmbyID != "" && r.Source != "primary" {
			target := r.AccountID
			if r.Source == "assigned" {
				target = r.AssignedAccountID
			}
			bindings = append(bindings, PlaybackBinding{UserID: r.EmbyID, UserName: r.EmbyName, AccountID: target})
		}
	}
	if c.EmbyURL == "" {
		c.EmbyURL = s.Settings().EmbyURL
	}
	c.Bindings = bindings
	return c
}
func (s *MemberPlaybackService) Authorize(ctx context.Context, server, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	for _, r := range s.Records() {
		if r.EmbyID != id {
			continue
		}
		c := s.emby.snapshot()
		settings := s.Settings()
		if r.AppliedRevision != settings.Revision || r.SyncError != "" || len(settings.LibraryIDs) == 0 {
			return true, memberError("个人播放权限尚未同步，请稍后重试")
		}
		if r.EmbyURL != server || c.URL != r.EmbyURL || c.ServerID != r.ServerID || r.Stage != "ready" || !r.Provisioned || !r.PolicyEnabled || r.Suspended || r.AdminBlocked || !s.Settings().Enabled {
			return true, memberError("个人播放尚未开通或已暂停，请到个人中心检查绑定")
		}
		if _, err := s.websiteUser(ctx, r.UserID); err != nil {
			return true, err
		}
		if _, err := s.playbackAccount(r); err != nil {
			return true, memberError("个人网盘不可用，已暂停播放")
		}
		return true, nil
	}
	return false, nil
}

// New users can briefly exist upstream before their deny policy is applied.
// Do not expose a session for a reserved name until provisioning is complete.
func (s *MemberPlaybackService) AuthorizeLogin(ctx context.Context, server, id, name string) error {
	for _, r := range s.Records() {
		if r.EmbyID != id || id == "" {
			continue
		}
		c := s.emby.snapshot()
		settings := s.Settings()
		if r.EmbyURL != server || c.URL != server || c.ServerID != r.ServerID || !settings.Enabled || !r.Provisioned || !r.LoginEnabled || r.AdminBlocked || r.SyncError != "" || r.AppliedRevision != settings.Revision {
			return memberError("Emby 账号尚未就绪或已被停用")
		}
		_, err := s.websiteUser(ctx, r.UserID)
		return err
	}
	if strings.HasPrefix(strings.ToLower(name), "c115_") {
		return memberError("个人 Emby 账号尚未完成开通")
	}
	return nil
}

func memberPolicy(source map[string]any, enabled bool, settings model.MemberPlaybackSettings, login ...bool) map[string]any {
	p := make(map[string]any, len(source)+12)
	for k, v := range source {
		p[k] = v
	}
	p["IsAdministrator"] = false
	p["IsDisabled"] = !enabled
	if len(login) > 0 {
		p["IsDisabled"] = !login[0]
	}
	p["EnableMediaPlayback"] = enabled
	p["IsHidden"] = true
	for _, key := range []string{"EnableContentDeletion", "EnableContentDownloading", "EnableSubtitleDownloading", "EnableSubtitleManagement", "EnableLiveTvAccess", "EnableLiveTvManagement", "EnableVideoPlaybackTranscoding", "EnableAudioPlaybackTranscoding", "EnablePlaybackRemuxing", "EnableSyncTranscoding", "EnableMediaConversion", "EnableRemoteControlOfOtherUsers", "EnableSharedDeviceControl", "EnableAllChannels", "EnableAllDevices"} {
		p[key] = false
	}
	p["EnableAllDevices"] = true
	p["EnableAllFolders"] = false
	p["EnabledFolders"] = append([]string{}, settings.LibraryIDs...)
	p["EnableContentDownloading"] = enabled && settings.AllowDownload
	p["EnableRemoteAccess"] = true
	p["EnableContentDeletionFromFolders"] = []string{}
	p["RemoteClientBitrateLimit"] = 0
	return p
}

func memberPolicyMatches(policy map[string]any, enabled bool, settings model.MemberPlaybackSettings, login ...bool) bool {
	expected := memberPolicy(nil, enabled, settings, login...)
	for key, value := range expected {
		if flag, ok := value.(bool); ok && policy[key] != flag {
			return false
		}
	}
	// Emby decodes arrays as []any; test implementations may use []string.
	stringsOf := func(value any) ([]string, bool) {
		switch list := value.(type) {
		case []string:
			return list, true
		case []any:
			out := make([]string, 0, len(list))
			for _, v := range list {
				id, ok := v.(string)
				if !ok {
					return nil, false
				}
				out = append(out, id)
			}
			return out, true
		default:
			return nil, false
		}
	}
	for _, key := range []string{"EnabledFolders", "EnableContentDeletionFromFolders"} {
		actual, ok := stringsOf(policy[key])
		if !ok {
			return false
		}
		want := expected[key].([]string)
		if len(actual) != len(want) {
			return false
		}
		set := make(map[string]bool, len(want))
		for _, id := range want {
			set[id] = true
		}
		for _, id := range actual {
			if !set[id] {
				return false
			}
			delete(set, id)
		}
	}
	return true
}
func (s *MemberPlaybackService) provision(ctx context.Context, id, password string) error {
	if s.record(id).Adopted {
		return memberError("已有 Emby 账号仅开放网盘管理，不能重置账号")
	}
	if len(password) < 8 || len(password) > 128 {
		return memberError("Emby 密码需要 8–128 字节")
	}
	if !s.Settings().Enabled {
		return memberError("自助播放未开放")
	}
	r, err := s.ensure(ctx, id)
	if err != nil {
		return err
	}
	if r.AdminBlocked {
		return memberError("播放已被管理员停用")
	}
	c, client, err := s.config()
	if err != nil {
		return err
	}
	if r.EmbyURL != c.URL || r.ServerID != c.ServerID {
		return memberError("原 Emby 服务已变化，请联系管理员")
	}
	if r.EmbyID != "" && r.LoginEnabled {
		u, err := client.ManagedUser(ctx, r.EmbyID)
		if err != nil {
			return memberError("无法检查现有 Emby 权限")
		}
		if u.Policy["IsDisabled"] == true || (r.PolicyEnabled && u.Policy["EnableMediaPlayback"] == false) {
			r.AdminBlocked = true
			r.Stage = "paused"
			if err := s.save(ctx, r); err != nil {
				return err
			}
			return memberError("Emby 账号已被停用，请联系管理员")
		}
	}
	if r.EmbyID == "" {
		users, err := client.Users(ctx)
		if err != nil {
			return memberError("无法检查 Emby 用户")
		}
		for _, u := range users {
			if strings.EqualFold(u.Name, r.EmbyName) {
				if r.Stage != "creating" {
					return memberError("Emby 用户名冲突，请联系管理员")
				}
				r.EmbyID = u.ID
			}
		}
		if r.EmbyID == "" {
			if r.Stage == "creating" {
				return memberError("Emby 创建结果待确认，请管理员核查后重试")
			}
			r.Stage = "creating"
			if err := s.save(ctx, r); err != nil {
				return err
			}
			// Emby's /Users/New contract creates a user by name. Keep it disabled,
			// verify the deny policy, and only then set the user's chosen password.
			u, err := client.CreateManagedUser(ctx, r.EmbyName)
			if err != nil {
				return memberError("Emby 创建结果未确认，可稍后继续开通；不要手动创建同名用户")
			}
			r.EmbyID = u.ID
		}
		r.Stage = "provisioning"
		if err := s.save(ctx, r); err != nil {
			return err
		}
	}
	policySettings, err := memberPolicySettings(ctx, client, s.Settings())
	if err != nil {
		return err
	}
	if err := client.SetManagedPolicy(ctx, r.EmbyID, memberPolicy(nil, false, policySettings)); err != nil {
		return memberError("Emby 禁播策略保存失败，请稍后继续开通")
	}
	denied, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || !memberPolicyMatches(denied.Policy, false, policySettings) {
		return memberError("Emby 禁播策略尚未确认，暂不设置登录密码")
	}
	r.PolicyEnabled = false
	r.LoginEnabled = false
	r.Stage = "provisioning"
	if err := s.save(ctx, r); err != nil {
		return err
	}
	if err := client.SetManagedPassword(ctx, r.EmbyID, password); err != nil {
		return memberError("Emby 密码设置结果未确认，请重新提交")
	}
	if r.RegistrationName != "" && r.EmbyName != r.RegistrationName {
		if err := s.renameAccount(ctx, client, &r, r.RegistrationName); err != nil {
			return err
		}
	}
	r.Provisioned = true
	if _, err := s.websiteUser(ctx, id); err != nil {
		return err
	}
	r.Suspended = false
	r.Stage = "paused"
	r.Message = "正在检查播放条件"
	if err := s.save(ctx, r); err != nil {
		return err
	}
	return s.reconcile(ctx, r)
}
func (s *MemberPlaybackService) Provision(ctx context.Context, id, password string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	return s.provision(ctx, id, password)
}

func (s *MemberPlaybackService) reconcile(ctx context.Context, r model.MemberPlayback) (result error) {
	if r.EmbyID == "" {
		return nil
	}
	defer func() {
		if result == nil {
			return
		}
		failed := s.record(r.UserID)
		failed.PolicyEnabled = false
		failed.LoginEnabled = false
		failed.SyncError = "Emby 权限同步失败，已暂停播放；请检查服务后重试"
		if errors.Is(result, ErrMemberPlayback) {
			failed.SyncError = result.Error()
		}
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := s.save(persist, failed); err != nil {
			result = errors.Join(result, err)
		}
	}()
	settings := s.Settings()
	c, client, err := s.config()
	if err != nil {
		return err
	}
	if c.URL != r.EmbyURL || c.ServerID != r.ServerID {
		return memberError("Emby 服务不一致")
	}
	u, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil {
		return memberError("Emby 用户状态不可用")
	}
	if r.EmbyName != u.Name || r.PendingName != "" {
		r.EmbyName = u.Name
		r.PendingName = ""
		r.AccountRevision++
		r.AccountMessage = "已按原 Emby ID 确认当前用户名"
	}
	if r.LoginEnabled && (u.Policy["IsDisabled"] == true || (r.PolicyEnabled && u.Policy["EnableMediaPlayback"] == false)) {
		r.AdminBlocked = true
	}
	login := settings.Enabled && len(settings.LibraryIDs) > 0 && r.Provisioned && !r.AdminBlocked && r.Stage != "creating" && r.Stage != "provisioning"
	if _, err := s.websiteUser(ctx, r.UserID); err != nil {
		login = false
	}
	enabled := login && !r.Suspended
	if enabled && r.Source == "personal" && r.AccountID != "" {
		a, e := s.accounts.Get(r.AccountID)
		if e == nil && a.Cookie != "" && r.DirectoryID == "" && !r.DirectoryStarted {
			r, e = s.prepareDirectory(ctx, r)
			if e != nil {
				enabled = false
			}
		}
	}
	a, err := s.playbackAccount(r)
	if err != nil {
		enabled = false
	}
	if enabled {
		valid, err := s.checker.CheckCookie(ctx, a.Cookie)
		if err != nil || !valid {
			enabled = false
		}
		if enabled && r.Source != "primary" && s.drive.ValidateCacheParent(ctx, a.PlaybackParentCID, a.Cookie) != nil {
			enabled = false
		}
	}
	// Persist the deny state before network I/O. An uncertain enabling write never
	// makes the proxy ready; the next reconciliation re-applies policy.
	if !enabled && r.Stage != "provisioning" && r.Stage != "creating" {
		r.Stage = "paused"
		r.Message = "播放已暂停，请检查网盘登录、目录或联系管理员"
	}
	r.PolicyEnabled = false
	r.LoginEnabled = false
	if err := s.save(ctx, r); err != nil {
		return err
	}
	policySettings, err := memberPolicySettings(ctx, client, settings)
	if err != nil {
		return err
	}
	if err := client.SetManagedPolicy(ctx, r.EmbyID, memberPolicy(u.Policy, enabled, policySettings, login)); err != nil {
		return memberError("Emby 权限同步失败，稍后自动重试")
	}
	verified, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || !memberPolicyMatches(verified.Policy, enabled, policySettings, login) {
		_ = client.SetManagedPolicy(ctx, r.EmbyID, memberPolicy(u.Policy, false, policySettings))
		return memberError("Emby 未确认预期权限，个人播放保持锁定")
	}
	if enabled {
		r.Stage = "ready"
		r.Message = "已开通，请通过下方反代地址登录 Emby"
		r.PolicyEnabled = true
	}
	r.LoginEnabled = login
	if login && !enabled {
		r.Stage = "pending"
		r.Message = "Emby 已注册，可登录浏览；播放待授权或绑定自己的 115"
		if r.Source != "personal" {
			r.Message = "Emby 可登录浏览；管理员提供的播放账号暂不可用，请联系管理员"
		}
		if r.Source == "personal" && r.DirectoryStarted && r.DirectoryID == "" {
			r.Message = "Emby 可登录浏览；网盘目录创建结果待核查，请管理员确认目录编号"
		}
		if r.Suspended {
			r.Stage = "paused"
			r.Message = "Emby 可登录浏览，播放已暂停；重新扫码原 115 账号可恢复"
		}
	}
	r.AppliedRevision = settings.Revision
	r.SyncError = ""
	return s.save(ctx, r)
}
func (s *MemberPlaybackService) Suspend(ctx context.Context, id string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	r := s.record(id)
	if r.UserID == "" {
		return nil
	}
	a, err := s.accounts.Get(r.AccountID)
	if err != nil || a.OwnerUserID != id || a.Ownership != "member" {
		return memberError("没有可退出的个人网盘")
	}
	if r.Source == "personal" {
		r.Suspended = true
		r.Stage = "paused"
		r.Message = "已暂停个人播放，重新扫码可恢复"
	}
	delete(s.sessions, id)
	if err := s.save(ctx, r); err != nil {
		return err
	}
	if err := s.accounts.ClearMemberCookie(id, a.Revision); err != nil {
		return err
	}
	if r.Source != "personal" {
		return nil
	}
	return s.reconcile(ctx, r)
}
func (s *MemberPlaybackService) AdminUpdate(ctx context.Context, id string, blocked *bool, directory string, retry bool) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	r := s.record(id)
	if r.UserID == "" {
		return memberError("个人播放配置不存在")
	}
	if blocked != nil {
		r.AdminBlocked = *blocked
		r.PolicyEnabled = false
		r.LoginEnabled = false
	}
	if directory != "" {
		a, err := s.accounts.Get(r.AccountID)
		if err != nil {
			return err
		}
		if (r.Source != "personal" && !r.AllowPersonalSwitch) || r.DirectoryID != "" || a.OwnerUserID != r.UserID || a.Ownership != "member" {
			return memberError("只能确认尚未完成开通的目录")
		}
		if directory == "0" || s.drive.ValidateCacheParent(ctx, directory, a.Cookie) != nil {
			return memberError("目录校验失败")
		}
		if err := s.accounts.UpdatePlaybackDirectory(a.ID, a.Revision, directory); err != nil {
			return err
		}
		r.DirectoryID = directory
	}
	if retry && r.Stage == "creating" && r.EmbyID == "" {
		_, client, err := s.config()
		if err != nil {
			return err
		}
		users, err := client.Users(ctx)
		if err != nil {
			return err
		}
		for _, u := range users {
			if strings.EqualFold(u.Name, r.EmbyName) {
				return memberError("Emby 已存在该用户，请让用户继续开通以恢复关联")
			}
		}
		r.Stage = "bound"
	}
	if r.AdminBlocked {
		r.Stage = "paused"
	}
	if err := s.save(ctx, r); err != nil {
		return err
	}
	return s.reconcile(ctx, r)
}
func (s *MemberPlaybackService) SyncUser(ctx context.Context, id string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	return s.reconcile(ctx, s.record(id))
}
func (s *MemberPlaybackService) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		for _, r := range s.Records() {
			if ctx.Err() != nil {
				return
			}
			op, cancel := context.WithTimeout(ctx, 40*time.Second)
			_ = s.SyncUser(op, r.UserID)
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}
