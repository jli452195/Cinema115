package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

// MediaAuthorization belongs to one HTTP request. Never share it across requests.
type MediaAuthorization struct {
	releaseMember  func()
	retiredDevices int
	userLease      *UserPlaybackLease
	poolLease      *PoolLease
	service        *MemberPlaybackService
	record         model.MemberPlayback
	account        P115Account
	download       bool
	checkedAt      time.Time
	deferredRead   bool
	targetRead     bool
}

type playbackDirectShareKey struct{}
type playbackFileReuseKey struct{}

// WithPlaybackFileReuse allows a verified target-file read to prove current
// credentials. Cache hits and writes must still validate the target directory.
// This marker is set by the authenticated playback ingress, never client input.
func WithPlaybackFileReuse(ctx context.Context) context.Context {
	return context.WithValue(ctx, playbackFileReuseKey{}, true)
}

func deferredPlaybackRead(ctx context.Context) bool {
	req, _ := ctx.Value(poolRequestKey{}).(PoolRequest)
	file, _ := ctx.Value(playbackFileReuseKey{}).(bool)
	return (playbackDirectShare(ctx) || file) && !req.Download && !req.Task
}

// WithPlaybackDirectShare marks a share STRM request whose target account will
// first try the read-only share endpoint. The endpoint itself validates the
// cookie; directory validation remains mandatory if the request falls back to
// instant upload.
func WithPlaybackDirectShare(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, playbackDirectShareKey{}, enabled)
}

func playbackDirectShare(ctx context.Context) bool {
	v, _ := ctx.Value(playbackDirectShareKey{}).(bool)
	return v
}

func (s *MemberPlaybackService) playbackRecord(serverID, embyID string) model.MemberPlayback {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if r, ok := s.records[embyProfileID(serverID, embyID)]; ok {
		return r
	}
	// Records created by internal callers can use an explicit profile key.
	for _, r := range s.records {
		if r.ServerID == serverID && r.EmbyID == embyID {
			return r
		}
	}
	return model.MemberPlayback{}
}

// verified must come from an authenticated Emby response for this exact user,
// never from client request fields. Nil asks the service to fetch it itself.
func (s *MemberPlaybackService) BeginMediaAuthorization(ctx context.Context, server, id string, download bool, verified *emby.ManagedUser) (*MediaAuthorization, error) {
	return s.beginMediaAuthorization(ctx, server, id, download, verified, true)
}

// Negotiation validates identity and Emby permissions without reserving pool
// capacity or probing 115. The original-file redirect performs target checks.
func (s *MemberPlaybackService) AuthorizeNegotiation(ctx context.Context, server, id string) error {
	_, err := s.beginMediaAuthorization(ctx, server, id, false, nil, false)
	return err
}

func (s *MemberPlaybackService) beginMediaAuthorization(ctx context.Context, server, id string, download bool, verified *emby.ManagedUser, selectAccount bool) (*MediaAuthorization, error) {
	release, err := s.useMember(embyProfileID(s.emby.snapshot().ServerID, id))
	if err != nil {
		return nil, err
	}
	grant, err := s.beginMediaAuthorizationTracked(ctx, server, id, download, verified, selectAccount)
	if err != nil || grant == nil {
		release()
	} else {
		grant.releaseMember = release
	}
	return grant, err
}

func (s *MemberPlaybackService) beginMediaAuthorizationTracked(ctx context.Context, server, id string, download bool, verified *emby.ManagedUser, selectAccount bool) (*MediaAuthorization, error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c := s.emby.snapshot()
	if id == "" || c.URL != server || c.ServerID == "" || c.APIKey == "" {
		return nil, ErrLogin
	}
	r := s.playbackRecord(c.ServerID, id)
	if r.UserID != "" {
		if err := s.checkMediaLocal(r, server, download); err != nil {
			return nil, err
		}
	}
	var remote emby.ManagedUser
	if verified != nil {
		remote = *verified
	} else if r.UserID != "" {
		var err error
		remote, err = s.playbackUser(ctx, r)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		remote, err = s.NewEmby(c.URL, c.APIKey).ManagedUser(ctx, id)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, ErrPlaybackIdentityUnavailable
		}
	}
	if remote.ID != id || remote.Policy["IsDisabled"] == true || strings.HasPrefix(strings.ToLower(remote.Name), "c115_") {
		return nil, ErrLogin
	}
	if r.UserID == "" {
		profile, err := s.ObserveUser(ctx, c, remote)
		if err != nil {
			return nil, err
		}
		r = s.record(profile.ID)
	}
	if err := s.checkMediaLocal(r, server, download); err != nil {
		return nil, err
	}
	if r.PlaybackOnly && remote.Policy["IsAdministrator"] == true {
		return nil, ErrLogin
	}
	if err := mediaPermission(remote, download); err != nil {
		return nil, err
	}
	if err := s.mediaCheckTiming(ctx, "Emby 原文件限制检查", func(ctx context.Context) error {
		return s.ensureOriginalPlayback(ctx, r, remote)
	}); err != nil {
		return nil, err
	}
	if r.PlaybackOnly {
		if err := s.authorizeChildOwner(ctx, r); err != nil {
			return nil, err
		}
	}
	var a P115Account
	if !selectAccount {
		return nil, nil
	}
	var userLease *UserPlaybackLease
	if holder, _ := ctx.Value(userPlaybackRequestKey{}).(*UserPlaybackRequest); holder != nil && !download {
		req, _ := ctx.Value(poolRequestKey{}).(PoolRequest)
		if !req.Task {
			if s.UserPlayback == nil {
				return nil, ErrUserPlaybackBusy
			}
			if holder.lease == nil {
				var err error
				holder.lease, err = s.UserPlayback.Acquire(ctx, r, req)
				if err != nil {
					return nil, err
				}
				if s.Cache != nil && s.Cache.logs != nil {
					s.Cache.logs.Add(ctx, "DEBUG", "设备播放会话："+holder.lease.handoffReason)
				}
			}
			userLease = holder.lease
			if userLease.Record.Key != userPlaybackKey(r, req) || !userLease.Current() {
				return nil, ErrUserPlaybackStopped
			}
		}
	}
	var lease *PoolLease
	var err error
	if r.Source == "assigned" {
		lease, err = s.poolAuthorization(ctx, r)
		if err == nil {
			a = lease.Account
		}
	} else {
		a, err = s.playbackAccount(r)
		if err == nil && !(deferredPlaybackRead(ctx) && !download && !a.Primary) {
			err = s.checkMediaDrive(ctx, a)
		}
	}
	if err != nil {
		return nil, err
	}
	grant := &MediaAuthorization{service: s, record: r, account: a, download: download, checkedAt: started, poolLease: lease, userLease: userLease}
	grant.deferredRead = deferredPlaybackRead(ctx) && !download && !a.Primary && (lease == nil || lease.skipHealthRefresh)
	if err := grant.CheckCurrent(ctx); err != nil {
		return nil, errors.Join(err, grant.Close())
	}
	return grant, nil
}

type mediaDriveProbe struct {
	done chan struct{}
	err  error
}

// Only overlapping probes share work. Completed results are immediately removed;
// there is no TTL authorization cache and no detached goroutine.
func (s *MemberPlaybackService) checkMediaDrive(ctx context.Context, a P115Account) error {
	key := driveHealthFingerprint(a)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.driveProbeMu.Lock()
		if probe := s.driveProbes[key]; probe != nil {
			s.driveProbeMu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-probe.done:
			}
			// The leading request may have been canceled independently of this one.
			if errors.Is(probe.err, context.Canceled) {
				continue
			}
			return probe.err
		}
		if s.driveProbes == nil {
			s.driveProbes = make(map[[32]byte]*mediaDriveProbe)
		}
		probe := &mediaDriveProbe{done: make(chan struct{})}
		s.driveProbes[key] = probe
		s.driveProbeMu.Unlock()
		probe.err = s.probeMediaDrive(ctx, a)
		s.driveProbeMu.Lock()
		delete(s.driveProbes, key)
		close(probe.done)
		s.driveProbeMu.Unlock()
		return probe.err
	}
}

func (s *MemberPlaybackService) probeMediaDrive(ctx context.Context, a P115Account) error {
	if !a.Primary {
		if drive, ok := s.drive.(interface {
			ValidateMediaPlaybackDirectory(context.Context, string, string, string) error
		}); ok {
			err := s.mediaCheckTiming(ctx, "115 登录与目录合并核验", func(ctx context.Context) error {
				return drive.ValidateMediaPlaybackDirectory(ctx, a.ID, a.PlaybackParentCID, a.Cookie)
			})
			if ctx.Err() != nil {
				return errors.Join(ctx.Err(), err)
			}
			if err != nil {
				return fmt.Errorf("网盘登录或目录不可用: %w", err)
			}
			return nil
		}
	}
	return s.mediaCheckTiming(ctx, "115 登录及目录检查", func(ctx context.Context) error {
		return s.probeMediaDriveSeparate(ctx, a)
	})
}

func (s *MemberPlaybackService) probeMediaDriveSeparate(ctx context.Context, a P115Account) error {
	valid, err := s.checker.CheckCookie(ctx, a.Cookie)
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), err)
	}
	if err != nil {
		return fmt.Errorf("暂时无法检查网盘登录: %w", err)
	}
	if !valid {
		return fmt.Errorf("%w: %w", ErrMemberPlayback, p115.ErrShareLoginRequired)
	}
	if !a.Primary {
		err := s.drive.ValidateCacheParent(ctx, a.PlaybackParentCID, a.Cookie)
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), err)
		}
		if err != nil {
			return fmt.Errorf("网盘目录不可用: %w", err)
		}
	}
	return nil
}

func (s *MemberPlaybackService) mediaCheckTiming(ctx context.Context, stage string, check func(context.Context) error) error {
	if !ProxyDebugEnabled(ctx) || s.Cache == nil {
		return check(ctx)
	}
	started := time.Now()
	var total p115.RequestTiming
	readCtx := p115.WithRequestTiming(ctx, func(v p115.RequestTiming) {
		total.Requests += v.Requests
		total.Retries += v.Retries
		total.Wait += v.Wait
		total.HTTP += v.HTTP
	})
	err := check(readCtx)
	message := fmt.Sprintf("媒体授权计时：%s=%d ms｜成功=%t", stage, time.Since(started).Milliseconds(), err == nil)
	if total.Requests > 0 || total.Wait > 0 {
		message += fmt.Sprintf("｜115 请求=%d｜重试=%d｜排队及限速=%d ms｜HTTP合计=%d ms", total.Requests, total.Retries, total.Wait.Milliseconds(), total.HTTP.Milliseconds())
	}
	s.Cache.logs.Add(ctx, "INFO", message)
	return err
}

func mediaPermission(u emby.ManagedUser, download bool) error {
	if (!download && u.Policy["EnableMediaPlayback"] != true) || (download && u.Policy["EnableContentDownloading"] != true) {
		return memberError("Emby 未允许此用户播放或下载，请在 Emby 中检查用户权限")
	}
	return nil
}

func (s *MemberPlaybackService) checkMediaLocal(r model.MemberPlayback, server string, download bool) error {
	if s.deletionBlocked(r.UserID) {
		return memberError("用户正在删除或已删除")
	}
	c, settings := s.emby.snapshot(), s.Settings()
	if !settings.Enabled {
		return memberError("302 反代未运行，媒体访问未接管")
	}
	if download && !settings.AllowDownload {
		return memberError("原文件下载已关闭，请联系管理员")
	}
	if !download && r.ProxyPlaybackDisabled {
		return memberError("此用户的 Cinema115 反代播放已暂停，请联系管理员")
	}
	if r.UserID == "" || r.EmbyURL != server || c.URL != server || c.ServerID != r.ServerID || !r.Provisioned || r.Suspended {
		return memberError("媒体访问尚未开通或已暂停，请检查账号与网盘绑定")
	}
	if r.PlaybackOnly && (r.ChildRevoked || r.ChildDisabled || r.ChildPaused || r.Administrator) {
		return memberError("播放子账号已暂停或撤销")
	}
	return nil
}

// Cheap late check: a source change or revocation cannot reuse an earlier grant.
func (a *MediaAuthorization) CheckCurrent(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.userLease != nil && !a.userLease.Current() {
		return ErrUserPlaybackStopped
	}
	s := a.service
	r := s.record(a.record.UserID)
	if err := s.checkMediaLocal(r, a.record.EmbyURL, a.download); err != nil {
		return err
	}
	if r.EmbyID != a.record.EmbyID || r.Source != a.record.Source || r.PlaybackOnly != a.record.PlaybackOnly || r.OwnerUserID != a.record.OwnerUserID {
		return ErrAccountChanged
	}
	if !a.download && r.ProxyPlaybackRevision != a.record.ProxyPlaybackRevision {
		return ErrAccountChanged // A pause/resume cycle cannot revive an old grant.
	}
	var current P115Account
	var err error
	if a.poolLease != nil {
		if !a.poolLease.Current() {
			return ErrAccountChanged
		}
		current, err = s.accounts.Get(a.account.ID)
		if err == nil && !eligiblePoolAccount(current) {
			return ErrAccountChanged
		}
	} else {
		current, err = s.playbackAccount(r)
	}
	if err != nil {
		return err
	}
	if current.ID != a.account.ID || current.Revision != a.account.Revision || driveHealthFingerprint(current) != driveHealthFingerprint(a.account) {
		return ErrAccountChanged
	}
	return nil
}

func (a *MediaAuthorization) MatchesSelection(primary P115Account, selected *PlaybackCacheSelection) bool {
	if a.record.Source == "primary" {
		return selected == nil && primary.ID == a.account.ID && primary.Revision == a.account.Revision
	}
	return selected != nil && selected.binding.AccountID == a.account.ID && selected.accountRevision == a.account.Revision && selected.cookie == a.account.Cookie && selected.binding.ParentCID == a.account.PlaybackParentCID
}

// Slow file preparation must re-read Emby before issuing a new CDN URL.
func (a *MediaAuthorization) Revalidate(ctx context.Context) error {
	if err := a.CheckCurrent(ctx); err != nil {
		return err
	}
	if time.Since(a.checkedAt) >= 5*time.Second {
		u, err := a.service.playbackUser(ctx, a.service.record(a.record.UserID))
		if err != nil {
			return err
		}
		if err := mediaPermission(u, a.download); err != nil {
			return err
		}
		if err := a.service.ensureOriginalPlayback(ctx, a.record, u); err != nil {
			return err
		}
		if a.record.PlaybackOnly {
			if err := a.service.authorizeChildOwner(ctx, a.record); err != nil {
				return err
			}
		}
	}
	return a.CheckCurrent(ctx)
}
