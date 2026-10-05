package service

import (
	"context"
	"time"
)

type resolutionAuthorizationKey struct{}

// ResolutionContext binds deferred checks to this request's fixed target.
// It is never stored in the direct-link cache or shared with another request.
func (a *MediaAuthorization) ResolutionContext(ctx context.Context) context.Context {
	if a == nil || !a.deferredRead {
		return ctx
	}
	return context.WithValue(ctx, resolutionAuthorizationKey{}, a)
}

// ValidateTargetDirectory proves current credentials and directory identity
// before writing, or when reusing a URL without this request reading 115
// (including a coalesced cache flight).
func (a *MediaAuthorization) ValidateTargetDirectory(ctx context.Context) error {
	if a == nil || !a.deferredRead {
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
	a.deferredRead = false
	if a.poolLease != nil {
		a.poolLease.skipHealthRefresh = false
	}
	return nil
}

func resolutionAuthorization(ctx context.Context, selected *PlaybackCacheSelection) (*MediaAuthorization, error) {
	a, _ := ctx.Value(resolutionAuthorizationKey{}).(*MediaAuthorization)
	if a == nil {
		return nil, nil
	}
	if selected == nil || selected.binding.AccountID != a.account.ID || selected.accountRevision != a.account.Revision || selected.cookie != a.account.Cookie || selected.uid != a.account.UID || selected.binding.ParentCID != a.account.PlaybackParentCID {
		return nil, ErrAccountChanged
	}
	return a, a.CheckCurrent(ctx)
}
