package service

import (
	"context"
	"time"
)

type shareAuthorizationKey struct{}

// ShareResolutionContext binds deferred checks to this request's fixed target.
// It is never stored in the direct-link cache or shared with another request.
func (a *MediaAuthorization) ShareResolutionContext(ctx context.Context) context.Context {
	if a == nil || !a.deferredShare {
		return ctx
	}
	return context.WithValue(ctx, shareAuthorizationKey{}, a)
}

// ValidateCachedShare retains the fresh credential/directory check when no
// request to 115 was made by this request (including a coalesced cache flight).
func (a *MediaAuthorization) ValidateCachedShare(ctx context.Context) error {
	if a == nil || !a.deferredShare {
		return nil
	}
	if err := a.CheckCurrent(ctx); err != nil {
		return err
	}
	probeCtx := ctx
	if a.poolLease != nil {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
	}
	if err := a.service.checkMediaDrive(probeCtx, a.account); err != nil {
		return &playbackTargetError{stage: "目标账号登录与目录核验失败", err: err}
	}
	if err := a.CheckCurrent(ctx); err != nil {
		return err
	}
	a.deferredShare = false
	if a.poolLease != nil {
		a.poolLease.skipHealthRefresh = false
	}
	return nil
}

func shareResolutionAuthorization(ctx context.Context, selected *PlaybackCacheSelection) (*MediaAuthorization, error) {
	a, _ := ctx.Value(shareAuthorizationKey{}).(*MediaAuthorization)
	if a == nil {
		return nil, nil
	}
	if selected == nil || selected.binding.AccountID != a.account.ID || selected.accountRevision != a.account.Revision || selected.cookie != a.account.Cookie || selected.uid != a.account.UID || selected.binding.ParentCID != a.account.PlaybackParentCID {
		return nil, ErrAccountChanged
	}
	return a, a.CheckCurrent(ctx)
}
