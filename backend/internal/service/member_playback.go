package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"sync"
	"time"
)

var ErrMemberPlayback = errors.New("个人播放操作未完成")

// A failed upstream identity lookup is not proof that a user or binding is invalid.
var ErrPlaybackIdentityUnavailable = errors.New("Emby 播放身份查询暂不可用")

type memberQRClient interface {
	CreateQRCode(context.Context, string) (*p115.QRCode, error)
	CheckQRCode(context.Context, string) (*p115.QRCodeStatus, error)
	CompleteQRCodeLogin(context.Context, string) (string, error)
}
type memberDriveClient interface {
	CreateDirectory(context.Context, string, string, string) (string, error)
	ValidateCacheParent(context.Context, string, string) error
	DriveFiles(context.Context, string, int, string) (*p115.SharePage, error)
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
	app       string
	replace   bool
	accountID string
	source    string
	qr        *p115.QRCode
	revision  uint64
	lastPoll  time.Time
}
type MemberPlaybackService struct {
	Pool                 *DynamicPool
	UserPlayback         *UserPlayback
	PoolNotifier         PoolNotifier // wired before workers start
	ValidatePoolNotifier func(string) error
	PoolRequests         p115.RequestBudget
	Cache                *PlaybackCache // wired before serving
	db                   *storage.Store
	accounts             *P115Accounts
	emby                 *EmbyService
	drive                memberDriveClient
	checker              memberCookieChecker
	qr                   memberQRClient
	gate                 chan struct{}
	wake                 chan struct{}
	healthWake           chan struct{}
	mu                   sync.RWMutex
	records              map[string]model.MemberPlayback
	settings             model.MemberPlaybackSettings
	sessions             map[string]memberQRSession // guarded by gate, bounded and expires
	NewEmby              func(string, string) memberEmbyClient
	ProxyStatus          func() EmbyProxyStatus // wired once before HTTP starts
	ClearDirectLinks     func()                 // wired once before HTTP starts
	driveHealthMu        sync.Mutex
	driveHealth          map[string]driveHealthEntry
	driveHealthGate      chan struct{}
	driveHealthEpoch     uint64
	driveProbeMu         sync.Mutex
	driveProbes          map[[32]byte]*mediaDriveProbe
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
	pool, err := NewDynamicPool(ctx, db)
	if err != nil {
		return nil, err
	}
	s := &MemberPlaybackService{db: db, accounts: accounts, emby: server, drive: drive, checker: checker, qr: qr, gate: make(chan struct{}, 1), records: make(map[string]model.MemberPlayback, len(records)), settings: settings, sessions: make(map[string]memberQRSession), NewEmby: func(base, key string) memberEmbyClient { return emby.New(base, key) }}
	s.Pool = pool
	s.UserPlayback, err = NewUserPlayback(ctx, db)
	if err != nil {
		return nil, err
	}
	s.configurePoolRequests()
	for _, r := range records {
		if _, err := db.PortalUser(ctx, r.UserID); err != nil {
			return nil, fmt.Errorf("个人中心数据结构已重建，请停服并使用 dbtool -action reset-portal -output 备份路径 重置测试用户: %w", err)
		}
		s.records[r.UserID] = r
	}
	s.wake = make(chan struct{}, 1)
	s.healthWake = make(chan struct{}, 1)
	s.driveHealth = make(map[string]driveHealthEntry)
	s.driveHealthGate = make(chan struct{}, 1)
	if accounts != nil {
		accounts.mu.Lock()
		accounts.playbackWake = s.healthWake
		accounts.mu.Unlock()
	}
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
func (s *MemberPlaybackService) unlock() {
	<-s.gate
	// Account transactions may notify before the member binding is published.
	// Wake again after the complete operation so new personal bindings are seen.
	s.requestHealthCheck()
}
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
	settings := s.settings
	settings.LibraryIDs = append([]string{}, settings.LibraryIDs...)
	s.mu.RUnlock()
	// Read-only runtime projection: the proxy is the only playback switch.
	settings.Enabled = false
	if s.ProxyStatus != nil {
		proxy := s.ProxyStatus()
		settings.Enabled = proxy.Config.Enabled && proxy.Listening
	}
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
	return s.setSettings(ctx, settings)
}

// Separate forms update only their owned fields under the same lock.
func (s *MemberPlaybackService) SetRegistrationLibraries(ctx context.Context, ids []string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	settings := s.Settings()
	settings.LibraryIDs = ids
	return s.setSettings(ctx, settings)
}

func (s *MemberPlaybackService) SetPlaybackSettings(ctx context.Context, download bool, publicURL string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	settings := s.Settings()
	settings.AllowDownload, settings.PublicURL = download, publicURL
	return s.setSettings(ctx, settings)
}

func (s *MemberPlaybackService) setSettings(ctx context.Context, settings model.MemberPlaybackSettings) error {
	c := s.emby.snapshot()
	previous := s.Settings()
	settings.Enabled = false // runtime projection only; never accept an independent playback toggle
	settings.PublicURL = strings.TrimSpace(settings.PublicURL)
	if settings.PublicURL != "" {
		u, err := url.Parse(settings.PublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return memberError("请填写有效的 Emby 连接地址，或留空")
		}
		if c.APIKey == "" || c.ServerID == "" {
			return memberError("请先配置并验证 Emby 服务")
		}
		settings.PublicURL = strings.TrimRight(settings.PublicURL, "/")
	}
	if len(settings.LibraryIDs) > 100 {
		return memberError("媒体库数量超过限制")
	}
	ids := make([]string, 0, len(settings.LibraryIDs))
	if len(settings.LibraryIDs) > 0 && (!slices.Equal(settings.LibraryIDs, previous.LibraryIDs) || previous.ServerID != c.ServerID || previous.EmbyURL != c.URL) {
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
		if r.EmbyURL != c.URL || r.ServerID != c.ServerID {
			return memberError("已有个人账号属于另一 Emby 服务，不能直接更换服务")
		}
	}
	settings.LibraryIDs = ids
	slices.Sort(settings.LibraryIDs)
	settings.LibraryIDs = slices.Compact(settings.LibraryIDs)
	policyChanged := settings.AllowDownload != previous.AllowDownload || previous.EmbyURL != c.URL || previous.ServerID != c.ServerID
	settings.EmbyURL, settings.ServerID = c.URL, c.ServerID
	if err := s.db.SaveMemberPlaybackSettings(ctx, settings); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = settings
	s.mu.Unlock()
	if policyChanged {
		s.RequestSync()
	}
	return nil
}

// ApplyProxy serializes the takeover boundary with all policy writes. On activation,
// stale permissions are invalidated before opening the listener; failed starts stay denied.
func (s *MemberPlaybackService) ApplyProxy(ctx context.Context, proxy *EmbyProxy, input EmbyProxyConfig) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	config, err := normalizeEmbyProxy(input)
	if err != nil {
		return err
	}
	previous := proxy.Status()
	if config.Enabled {
		c, _, err := s.config()
		if err != nil {
			return err
		}
		settings := s.Settings()
		if c.URL != config.URL || settings.EmbyURL != c.URL || settings.ServerID != c.ServerID {
			return memberError("请先保存同一 Emby 服务的媒体访问设置")
		}
		if !previous.Config.Enabled || !previous.Listening {
			settings.Enabled = false
			if err := s.db.SaveMemberPlaybackSettings(ctx, settings); err != nil {
				return err
			}
			s.mu.Lock()
			s.settings = settings
			s.mu.Unlock()
		}
	}
	if err := proxy.Apply(ctx, config); err != nil {
		return err
	}
	if config.Enabled {
		s.RequestSync()
	}
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
// selected ItemIds in settings and resolve them when creating a new account.
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
	s.requestHealthCheck()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *MemberPlaybackService) portalUser(ctx context.Context, id string) (model.PortalUser, error) {
	if s.record(id).PlaybackOnly {
		return model.PortalUser{}, ErrLogin
	}
	u, err := s.db.PortalUser(ctx, id)
	if err != nil {
		return u, ErrLogin
	}
	c := s.emby.snapshot()
	if u.ServerID != c.ServerID || c.APIKey == "" {
		return u, ErrLogin
	}
	remote, err := s.NewEmby(c.URL, c.APIKey).ManagedUser(ctx, u.EmbyID)
	if err != nil || remote.Policy["IsDisabled"] == true || remote.Policy["IsAdministrator"] == true {
		return u, ErrLogin
	}
	u.Username = remote.Name
	return u, nil
}
func (s *MemberPlaybackService) ensure(ctx context.Context, id string) (model.MemberPlayback, error) {
	r := s.record(id)
	if _, err := s.portalUser(ctx, id); err != nil {
		return r, err
	}
	if r.UserID == "" {
		return r, memberError("播放尚未授权，请联系管理员")
	}
	return r, nil
}

type MemberPlaybackView struct {
	AccountID                  string                  `json:"accountId"`
	Revision                   uint64                  `json:"revision"`
	DirectoryAccount           *MemberDirectoryAccount `json:"directoryAccount,omitempty"`
	NeedsDirectoryConfirmation bool                    `json:"needsDirectoryConfirmation"`
	CanPreparePersonal         bool                    `json:"canPreparePersonal"`
	Enabled                    bool                    `json:"enabled"`
	Stage                      string                  `json:"stage"`
	Message                    string                  `json:"message"`
	UID                        string                  `json:"uid"`
	Connected                  bool                    `json:"connected"`
	DirectoryID                string                  `json:"directoryId"`
	EmbyName                   string                  `json:"embyName"`
	EmbyURL                    string                  `json:"embyUrl"`
	Suspended                  bool                    `json:"suspended"`
	AllowDownload              bool                    `json:"allowDownload"`
	Source                     string                  `json:"source"`
	Provisioned                bool                    `json:"provisioned"`
	PendingDrive               bool                    `json:"pendingDrive"`
}

func (s *MemberPlaybackService) View(id string) MemberPlaybackView {
	r := s.record(id)
	settings := s.Settings()
	drive := s.personalDrive(r)
	v := MemberPlaybackView{Enabled: settings.Enabled, Stage: r.Stage, Message: r.Message, UID: drive.UID, Connected: drive.Connected, DirectoryID: drive.DirectoryID, EmbyName: r.EmbyName, EmbyURL: settings.PublicURL, Suspended: r.Suspended}
	v.AllowDownload = settings.AllowDownload
	v.CanPreparePersonal = r.Source == "personal" || r.AllowPersonalSwitch
	v.Source = r.Source
	v.Provisioned = r.Provisioned
	v.PendingDrive = r.PendingAccountID != ""
	if a, err := s.accounts.Get(r.AccountID); err == nil && a.Ownership == "member" && a.OwnerUserID == id {
		v.AccountID, v.Revision = a.ID, a.Revision
	}
	v.DirectoryAccount = s.directoryAccountView(r)
	v.NeedsDirectoryConfirmation = r.PendingAccountID != "" && r.PendingDirectoryStarted || r.DirectoryStarted && r.DirectoryID == ""

	if r.CheckError != "" {
		v.Message = r.CheckError
	}
	if r.UserID == "" {
		v.Stage = "unbound"
	}
	if r.EmbyID == "" {
		v.EmbyName = ""
	}
	return v
}

type MemberQRInput struct {
	AccountID  string `json:"accountId"`
	Revision   uint64 `json:"revision"`
	ClientType string `json:"clientType"`
	Replace    bool   `json:"replace"`
}

func (s *MemberPlaybackService) CreateQR(ctx context.Context, id string, input MemberQRInput) (*p115.QRCode, error) {
	app, ok := p115.LoginApp(input.ClientType)
	if !ok {
		return nil, memberError("登录客户端无效")
	}
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.unlock()
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
	if input.AccountID != r.AccountID || input.Revision != revision {
		return nil, ErrAccountChanged
	}
	if r.PendingAccountID != "" {
		return nil, memberError("请先完成或取消待绑定网盘")
	}
	for key, session := range s.sessions {
		if time.Now().After(session.qr.ExpiresAt) {
			delete(s.sessions, key)
		}
	}
	if old, ok := s.sessions[id]; ok && old.app == app && time.Until(old.qr.ExpiresAt) > 150*time.Second {
		return nil, memberError("请稍后再刷新二维码")
	}
	if _, replacing := s.sessions[id]; !replacing && len(s.sessions) >= 100 {
		return nil, memberError("扫码人数过多，请稍后重试")
	}
	qr, err := s.qr.CreateQRCode(ctx, app)
	if err != nil {
		return nil, memberError("获取二维码失败")
	}
	s.sessions[id] = memberQRSession{app: app, qr: qr, revision: revision, source: r.Source, accountID: r.AccountID, replace: input.Replace}
	return qr, nil
}

func (s *MemberPlaybackService) PollQR(ctx context.Context, id, qrID string) (string, error) {
	if err := s.lock(ctx); err != nil {
		return "", err
	}
	defer s.unlock()
	if _, err := s.portalUser(ctx, id); err != nil {
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
	if r.AccountID != p.accountID || r.Source != p.source || (r.Source != "personal" && !r.AllowPersonalSwitch) || account.Revision != p.revision || r.AccountID != "" && (account.OwnerUserID != id || account.Ownership != "member") {
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
	if _, err := s.portalUser(ctx, id); err != nil {
		return "", err
	}
	u, err := s.portalUser(ctx, id)
	if err != nil {
		return "", err
	}
	r, err = s.accounts.BindMember(ctx, r, u.Username, cookie, p.revision, p.replace)
	if err != nil {
		if errors.Is(err, ErrMemberPlayback) {
			return "", err
		}
		if errors.Is(err, ErrAccountChanged) {
			return "", memberError("配置已变化，请刷新二维码")
		}
		return "", fmt.Errorf("保存个人网盘: %w", err)
	}
	s.mu.Lock()
	s.records[id] = r
	s.mu.Unlock()
	delete(s.sessions, id)
	// A new drive remains pending until its owner explicitly chooses a directory.
	if r.Source == "personal" {
		if err := s.reconcile(ctx, r); err != nil {
			return "", err
		}
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
		if r.EmbyID != "" && r.Source != "" && r.Source != "primary" {
			target := r.AccountID
			if r.PlaybackOnly {
				target = ""
				if a, err := s.playbackAccount(r); err == nil {
					target = a.ID
				}
			}
			if r.Source == "assigned" {
				target = "" // Pool users must never fall through to primary in static snapshots.
			}
			bindings = append(bindings, PlaybackBinding{UserID: r.EmbyID, UserName: r.EmbyName, AccountID: target, Source: r.Source})
		}
	}
	if c.EmbyURL == "" {
		c.EmbyURL = s.Settings().EmbyURL
	}
	c.Bindings = bindings
	return c
}
func (s *MemberPlaybackService) Authorize(ctx context.Context, server, id string) (bool, error) {
	return s.AuthorizeMedia(ctx, server, id, false)
}

// AuthorizeMedia shares identity, source and library checks, with independent action gates.
func (s *MemberPlaybackService) AuthorizeMedia(ctx context.Context, server, id string, download bool) (bool, error) {
	ctx = WithPoolRequest(ctx, PoolRequest{Session: DirectLinkKey("authorization-check", id), Download: download})
	a, err := s.BeginMediaAuthorization(ctx, server, id, download, nil)
	if a != nil {
		if closeErr := a.Close(); err == nil {
			err = closeErr
		}
	}
	return true, err
}

// New users can briefly exist upstream before their deny policy is applied.
// Do not expose a session for a reserved name until provisioning is complete.
func (s *MemberPlaybackService) AuthorizeLogin(ctx context.Context, server, id, name string) error {
	if strings.HasPrefix(strings.ToLower(name), "c115_") {
		return memberError("Emby 账号尚未完成注册")
	}
	for _, r := range s.Records() {
		if r.EmbyID != id || id == "" {
			continue
		}
		c := s.emby.snapshot()
		if r.EmbyURL != server || c.URL != server || c.ServerID != r.ServerID || !r.Provisioned {
			return memberError("Emby 身份尚未就绪")
		}
		if r.PlaybackOnly && (r.ChildDisabled || r.ChildRevoked) {
			return memberError("播放子账号已停用")
		}
		remote, err := s.playbackUser(ctx, r)
		if err != nil {
			return err
		}
		_, err = s.ObserveUser(ctx, c, remote)
		return err
	}
	c := s.emby.snapshot()
	if c.URL != server || c.ServerID == "" || c.APIKey == "" || id == "" {
		return ErrLogin
	}
	remote, err := s.NewEmby(c.URL, c.APIKey).ManagedUser(ctx, id)
	if err != nil || remote.ID != id || remote.Policy["IsDisabled"] == true {
		return ErrLogin
	}
	if _, err := s.ObserveUser(ctx, c, remote); err != nil {
		return err
	}
	s.RequestSync()
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
	// Emby grants the initial ability; the proxy independently enforces the download switch.
	p["EnableContentDownloading"] = true
	p["EnableRemoteAccess"] = true
	p["EnableContentDeletionFromFolders"] = []string{}
	p["RemoteClientBitrateLimit"] = 0
	return p
}

func memberPolicyMatches(policy map[string]any, enabled bool, settings model.MemberPlaybackSettings, login ...bool) bool {
	return memberPolicyMatchesExpected(policy, memberPolicy(nil, enabled, settings, login...))
}

func memberPolicyMatchesExpected(policy, expected map[string]any) bool {
	for key, value := range expected {
		if flag, ok := value.(bool); ok && policy[key] != flag {
			return false
		}
	}
	// Emby decodes arrays as []any; test implementations may use []string.
	stringsOf := func(value any) ([]string, bool) {
		switch list := value.(type) {
		case nil:
			return nil, true
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
		value, exists := expected[key]
		if !exists {
			continue
		}
		actual, ok := stringsOf(policy[key])
		if !ok {
			return false
		}
		want, ok := stringsOf(value)
		if !ok {
			return false
		}
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

// Check results describe drive health only and never grant playback permission.
func (s *MemberPlaybackService) checkDrive(ctx context.Context, r model.MemberPlayback) error {
	if r.Source == "assigned" && !r.Suspended {
		return nil
	}
	if r.Suspended {
		return memberError("个人网盘已解绑，播放暂停")
	}
	if r.PlaybackOnly {
		if err := s.authorizeChildOwner(ctx, r); err != nil {
			return err
		}
	}
	a, err := s.playbackAccount(r)
	if err != nil {
		return err
	}
	valid, err := s.checker.CheckCookie(ctx, a.Cookie)
	if err != nil {
		return memberError("暂时无法检查网盘登录，请稍后重试")
	}
	if !valid {
		return memberError("网盘登录已失效，请重新登录")
	}
	if r.Source != "primary" {
		if err := s.drive.ValidateCacheParent(ctx, a.PlaybackParentCID, a.Cookie); err != nil {
			return memberError("网盘目录不可用，请检查目录及账号")
		}
	}
	return nil
}

func (s *MemberPlaybackService) recordDriveCheck(ctx context.Context, r model.MemberPlayback) error {
	err := s.checkDrive(ctx, r)
	r.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	r.CheckError = ""
	r.Stage, r.Message = "ready", "网盘最近检查通过"
	if r.Source == "assigned" {
		r.Message = "共享池自动调度，播放时实时检查"
	}
	if err != nil {
		r.Stage, r.Message = "pending", "网盘检查失败，请稍后重试"
		if errors.Is(err, ErrMemberPlayback) {
			r.Message = err.Error()
		}
		r.CheckError = r.Message
	}
	return s.save(ctx, r)
}

// Explicit checks enforce only original-file restrictions and child login lifecycle.
// They never derive an Emby playback switch from drive health.
func (s *MemberPlaybackService) reconcile(ctx context.Context, r model.MemberPlayback) error {
	if r.EmbyID == "" {
		return nil
	}
	if !s.Settings().Enabled && !(r.PlaybackOnly && (r.ChildDisabled || r.ChildRevoked || r.ChildManagedDisabled)) {
		return nil
	}
	c, client, err := s.config()
	if err != nil {
		return err
	}
	if c.URL != r.EmbyURL || c.ServerID != r.ServerID {
		return memberError("Emby 服务不一致")
	}
	u, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || u.ID != r.EmbyID {
		return memberError("无法读取 Emby 用户状态，请检查连接及用户是否存在")
	}
	r.Administrator = u.Policy["IsAdministrator"] == true
	if r.PlaybackOnly && r.Administrator {
		return memberError("子账号已变为管理员，停止托管")
	}
	if r.Administrator {
		r.AllowPersonalSwitch = false
	}
	r.EmbyName = u.Name
	policy := playbackRestrictions(u.Policy)
	if r.PlaybackOnly {
		if r.ChildDisabled || r.ChildRevoked || !r.Provisioned {
			policy["IsDisabled"] = true
			// Record our disable before the network write, allowing safe owner recovery.
			r.ChildManagedDisabled = true
			if err := s.save(ctx, r); err != nil {
				return err
			}
		} else if r.ChildManagedDisabled {
			policy["IsDisabled"] = false
		}
	}
	if !memberPolicyMatchesExpected(u.Policy, policy) {
		if err := client.SetManagedPolicy(ctx, r.EmbyID, policy); err != nil {
			return memberError("Emby 播放限制应用结果未确认，请刷新后检查")
		}
	}
	verified, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || verified.ID != r.EmbyID || !memberPolicyMatchesExpected(verified.Policy, policy) {
		return memberError("Emby 未确认播放限制，请刷新后检查")
	}
	if r.PlaybackOnly && !r.ChildDisabled && !r.ChildRevoked && r.Provisioned {
		r.ChildManagedDisabled = false
	}
	s.requestHealthCheck()
	return s.recordDriveCheck(ctx, r)
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
	if _, err := s.portalUser(ctx, id); err != nil {
		return err
	}
	r, err := s.accounts.UnbindMember(ctx, r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.records[id] = r
	s.mu.Unlock()
	delete(s.sessions, id)
	if s.ClearDirectLinks != nil {
		s.ClearDirectLinks()
	}
	if r.Source != "personal" {
		return nil
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

func (s *MemberPlaybackService) CheckUserPlayback(ctx context.Context, id string) error {
	r := s.record(id)
	if r.UserID == "" {
		return memberError("用户不存在，请刷新")
	}
	a, err := s.healthAccount(r)
	if err != nil {
		return nil // The response carries the current binding/lifecycle condition.
	}
	return s.refreshDriveHealth(ctx, a, true)
}
func (s *MemberPlaybackService) Run(ctx context.Context) {
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { defer workers.Done(); s.runDriveHealth(ctx) }()
	defer workers.Wait()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		for _, r := range s.Records() {
			if ctx.Err() != nil {
				return
			}
			op, cancel := context.WithTimeout(ctx, 40*time.Second)
			if r.PlaybackOnly && (r.ChildDisabled || r.ChildRevoked || r.ChildManagedDisabled) {
				_ = s.SyncUser(op, r.UserID)
			}
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

// Playback identity validation permits Emby administrators without granting them
// access to ordinary-user personal-drive endpoints or changing their admin role.
func (s *MemberPlaybackService) playbackUser(ctx context.Context, r model.MemberPlayback) (emby.ManagedUser, error) {
	c := s.emby.snapshot()
	if r.UserID == "" || r.ServerID != c.ServerID || r.EmbyURL != c.URL || c.APIKey == "" {
		return emby.ManagedUser{}, ErrLogin
	}
	u, err := s.NewEmby(c.URL, c.APIKey).ManagedUser(ctx, r.EmbyID)
	if ctx.Err() != nil {
		return u, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return u, err
		}
		return u, ErrPlaybackIdentityUnavailable
	}
	if u.ID != r.EmbyID || u.Policy["IsDisabled"] == true {
		return u, ErrLogin
	}
	if r.PlaybackOnly && (u.Policy["IsAdministrator"] == true || r.ChildDisabled || r.ChildRevoked) {
		return u, ErrLogin
	}
	return u, nil
}

// Emby owns playback and all ordinary permissions. Only original-file
// restrictions are continuously enforced by Cinema115.
func playbackRestrictions(source map[string]any) map[string]any {
	p := make(map[string]any, len(source)+8)
	for k, v := range source {
		p[k] = v
	}
	for _, key := range []string{"EnableVideoPlaybackTranscoding", "EnableAudioPlaybackTranscoding", "EnablePlaybackRemuxing", "EnableSyncTranscoding", "EnableMediaConversion"} {
		p[key] = false
	}
	return p
}
