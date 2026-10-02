package service

import (
	"context"
	"strm115/backend/internal/model"
)

func (s *MemberPlaybackService) preparePendingDrive(ctx context.Context, r model.MemberPlayback) (model.MemberPlayback, error) {
	a, err := s.accounts.Get(r.PendingAccountID)
	if err != nil || a.OwnerUserID != r.UserID || a.Ownership != "member" || a.Cookie == "" || accountUID(a.Cookie) != a.UID {
		return r, memberError("待绑定网盘不可用，请重新扫码")
	}
	valid, err := s.checker.CheckCookie(ctx, a.Cookie)
	if err != nil || !valid {
		return r, memberError("待绑定网盘登录已失效")
	}
	if a.PlaybackParentCID == "" {
		return r, memberError("请先选择秒传保存目录，原绑定保留")
	}
	if err := s.drive.ValidateCacheParent(ctx, a.PlaybackParentCID, a.Cookie); err != nil {
		return r, memberError("新网盘目录校验失败，原绑定保留")
	}
	r, err = s.accounts.ActivateMember(ctx, r)
	if err != nil {
		return r, err
	}
	s.mu.Lock()
	s.records[r.UserID] = r
	s.mu.Unlock()
	if s.ClearDirectLinks != nil {
		s.ClearDirectLinks()
	}
	return r, nil
}

func (s *MemberPlaybackService) FinishDriveChange(ctx context.Context, id string, cancel bool) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	r, err := s.ensure(ctx, id)
	if err != nil {
		return err
	}
	if r.PendingAccountID == "" {
		return nil
	}
	if cancel {
		r, err = s.accounts.CancelPendingMember(ctx, r)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.records[id] = r
		s.mu.Unlock()
		delete(s.sessions, id)
		return nil
	}
	if r.Source != "personal" && !r.AllowPersonalSwitch {
		return memberError("尚未授权个人网盘")
	}
	r, err = s.preparePendingDrive(ctx, r)
	if err != nil {
		return err
	}
	return s.reconcile(ctx, r)
}
