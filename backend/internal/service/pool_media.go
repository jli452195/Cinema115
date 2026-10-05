package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strm115/backend/internal/client/p115"
	"time"

	"strm115/backend/internal/model"
)

// Only errors explicitly tagged as a target read failure can select a new
// account. Source failures and uncertain writes never reach this branch.
func (a *MediaAuthorization) PoolTargetFailed(ctx context.Context, err error) (bool, error) {
	if a == nil || a.poolLease == nil || ctx.Err() != nil {
		return false, nil
	}
	var target *playbackTargetError
	if !errors.As(err, &target) || errors.Is(err, p115.ErrInstantUncertain) {
		return false, nil
	}
	decision := ClassifyPoolFault(PoolFaultTarget, err)
	recordPoolCandidate(ctx, a.account.UID, "target", decision.Code, a.poolLease.Record.Generation)
	if !decision.SwitchUID {
		return false, nil
	}
	if err := a.poolLease.pool.report(ctx, a.account, err); err != nil {
		return false, err
	}
	return true, a.Close()
}

type poolRequestKey struct{}
type poolAttemptsKey struct{}

// Request-local, shared by sequential probe and target-read attempts.
// It must never be shared between concurrent requests.
type PoolAttempts struct {
	remaining int
	excluded  map[string]bool
}

func WithPoolAttempts(ctx context.Context, limit int) context.Context {
	return context.WithValue(ctx, poolAttemptsKey{}, &PoolAttempts{remaining: limit, excluded: make(map[string]bool)})
}
func PoolCanRetry(ctx context.Context) bool {
	a, _ := ctx.Value(poolAttemptsKey{}).(*PoolAttempts)
	return ctx.Err() == nil && a != nil && a.remaining > 0
}

func PoolAttemptSwitches(ctx context.Context) int {
	a, _ := ctx.Value(poolAttemptsKey{}).(*PoolAttempts)
	if a == nil {
		return 0
	}
	return max(0, len(a.excluded)-1)
}

func (s *MemberPlaybackService) PersistPoolCooling() error {
	if s.Pool == nil {
		return nil
	}
	until := s.PoolRequests.Stats().CoolingUntil
	if until == 0 {
		return nil
	}
	p := s.Pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if until <= p.doc.GlobalUntil {
		return nil
	}
	d := p.clone()
	d.GlobalUntil = until
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return p.save(ctx, d)
}

type PoolRequest struct {
	transferCapacity bool // Set only after authenticated user-device admission.
	Device           string
	handoff          *model.UserPlaybackLease
	Client           string
	eventFallback    string
	userGeneration   uint64
	PlaySession      string
	observationKey   string
	Session          string
	Item             string
	Source           string
	Download         bool
	Task             bool
	slotKey          string
	lifecycleKey     string
}

// Session must be scoped by an authenticated token identity by the HTTP caller.
// Client-provided user IDs are never used here.
func WithPoolRequest(ctx context.Context, r PoolRequest) context.Context {
	return context.WithValue(ctx, poolRequestKey{}, r)
}

func (s *MemberPlaybackService) poolAccounts() []P115Account {
	views := s.accounts.ManagedList()
	out := make([]P115Account, 0, len(views))
	for _, v := range views {
		a, err := s.accounts.Get(v.ID)
		if err == nil && eligiblePoolAccount(a) {
			out = append(out, a)
		}
	}
	return out
}

func (s *MemberPlaybackService) poolAuthorization(ctx context.Context, r model.MemberPlayback) (*PoolLease, error) {
	if s.Pool == nil {
		return nil, ErrPoolUnavailable
	}
	req, _ := ctx.Value(poolRequestKey{}).(PoolRequest)
	if req.Session == "" {
		return nil, fmt.Errorf("%w：缺少已认证的播放会话", ErrPoolUnavailable)
	}
	key := DirectLinkKey(r.ServerID, r.UserID, req.Session, req.Item, req.Source, strconv.FormatBool(req.Download))
	if !req.Download {
		if req.Device != "" && !req.Task {
			req.slotKey = DirectLinkKey("pool-slot", r.ServerID, r.UserID, req.Device)
		}
		req.lifecycleKey = DirectLinkKey(r.ServerID, r.UserID, req.Session, req.Item)
		if req.Client != "" {
			req.eventFallback = DirectLinkKey("playback-event", r.ServerID, r.UserID, req.Client, req.Item)
		}
		if holder, _ := ctx.Value(userPlaybackRequestKey{}).(*UserPlaybackRequest); holder != nil && holder.lease != nil {
			req.userGeneration = holder.lease.Record.Generation
			req.handoff = holder.lease.handoff
			req.transferCapacity = holder.lease.policyMode == DevicePolicyHeartbeat
		}
		if req.PlaySession != "" && len(req.PlaySession) <= 256 {
			req.observationKey = DirectLinkKey("pool-observation", r.ServerID, r.EmbyID, req.PlaySession, req.Item)
		}
	}
	ctx = WithPoolRequest(ctx, req)
	started := time.Now()
	probe := s.checkMediaDrive
	if playbackDirectShare(ctx) && !req.Download && !req.Task {
		// A direct share read is itself the credential check. Do not spend a
		// second 115 request on the configured write directory unless the share
		// read fails and the instant-upload fallback is actually entered.
		probe = nil
		if s.Cache != nil && s.Cache.logs != nil {
			s.Cache.logs.Add(ctx, "DEBUG", "分享直取候选跳过前置目录核验；仅在需要秒传时核验目标目录")
		}
	}
	lease, err := s.Pool.Acquire(ctx, key, r.UserID, s.poolAccounts(), probe)
	if s.Cache != nil && s.Cache.logs != nil {
		identity := DirectLinkKey("pool-log", r.ServerID, r.UserID)[:12]
		if err == nil {
			s.Cache.logs.Add(ctx, "DEBUG", fmt.Sprintf("池调度完成：用户摘要=%s，代次=%d，准备耗时=%d ms，已换 UID 次数=%d", identity, lease.Record.Generation, time.Since(started).Milliseconds(), PoolAttemptSwitches(ctx)))
		} else {
			reason := "核验或存储失败"
			var admission *PoolAdmissionError
			switch {
			case errors.As(err, &admission):
				reason = admission.Error() + "；" + admission.Diagnostic
			case errors.Is(err, ErrPoolBusy):
				reason = "共享池请求繁忙"
			case errors.Is(err, ErrPoolCooling):
				reason = "限流或故障冷却"
			case errors.Is(err, ErrPoolInvalid):
				reason = "凭据或目录失效"
			case errors.Is(err, ErrPoolEmpty):
				reason = "无已配置候选"
			case ctx.Err() != nil:
				reason = "取消或超时"
			}
			s.Cache.logs.Add(ctx, "DEBUG", fmt.Sprintf("池调度结束：用户摘要=%s，原因=%s，耗时=%d ms", identity, reason, time.Since(started).Milliseconds()))
		}
	}
	return lease, err
}

func (a *MediaAuthorization) PoolSelection(snap PlaybackCacheSnapshot) *PlaybackCacheSelection {
	if a == nil || a.poolLease == nil {
		return nil
	}
	t := a.account
	return &PlaybackCacheSelection{binding: PlaybackBinding{UserID: a.record.EmbyID, AccountID: t.ID, ParentCID: t.PlaybackParentCID}, donors: snap.donors, accountName: t.Name, accountRevision: t.Revision, cookie: t.Cookie, uid: t.UID, poolGeneration: a.poolLease.Record.Generation}
}
func (a *MediaAuthorization) Close() error {
	if a != nil && a.releaseMember != nil {
		defer a.releaseMember()
		a.releaseMember = nil
	}
	if a == nil || a.poolLease == nil {
		return nil
	}
	return a.poolLease.Close()
}
func (a *MediaAuthorization) Commit(ctx context.Context) error {
	if a.deferredShare && !a.shareRead {
		return ErrAccountChanged
	}
	defer func() {
		if a.retiredDevices > 0 && a.service != nil && a.service.Cache != nil && a.service.Cache.logs != nil {
			a.service.Cache.logs.Add(ctx, "INFO", fmt.Sprintf("播放设备接替已提交：退休设备=%d；旧会话后续取链已拦截，已签发 CDN 直链不可撤回", a.retiredDevices))
		}
	}()
	if err := a.Revalidate(ctx); err != nil {
		return err
	}
	if a.userLease != nil && a.poolLease != nil && a.poolLease.Record.SlotKey != "" {
		return a.commitDevicePlayback(ctx)
	}
	if a.userLease != nil && a.userLease.policyMode == DevicePolicyHeartbeat {
		if a.poolLease != nil {
			return ErrPlaybackDeviceIdentity
		}
		return a.commitNonPoolDevicePolicy(ctx)
	}
	if a.userLease != nil && a.userLease.handoff != nil {
		if err := a.commitHandoff(ctx); err != nil {
			return err
		}
		if a.service.Cache != nil && a.service.Cache.logs != nil {
			if a.userLease.Record.HandoffAt != 0 {
				a.service.Cache.logs.Add(ctx, "INFO", "新会话播放证据已确认，同设备静默旧会话接替完成")
			} else {
				a.service.Cache.logs.Add(ctx, "INFO", "新取链尚未确认播放，保留旧会话；本次按独立名额提交")
			}
		}
		return nil
	}
	if a.poolLease != nil {
		if err := a.poolLease.Commit(ctx); err != nil {
			return err
		}
	}
	if a.userLease != nil {
		return a.userLease.Commit(ctx)
	}
	return nil
}

func (s *MemberPlaybackService) IsPoolUser(server, id string) bool {
	c := s.emby.snapshot()
	if s.deletionBlocked(embyProfileID(c.ServerID, id)) {
		return false
	}
	return c.URL == server && s.playbackRecord(c.ServerID, id).Source == "assigned"
}
