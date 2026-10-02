package service

import (
	"context"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
)

func (s *MemberPlaybackService) authorizePersonalDonor(ctx context.Context, a P115Account) error {
	r := s.record(a.OwnerUserID)
	if r.UserID == "" || r.Suspended || r.AccountID != a.ID || r.DirectoryID != a.PlaybackParentCID || !r.Provisioned {
		return memberError("个人来源已暂停或解绑")
	}
	current, err := s.accounts.Get(a.ID)
	if err != nil || current.Cookie == "" || current.UID != a.UID || current.Revision != a.Revision {
		return memberError("个人来源凭据已变化")
	}
	u, err := s.playbackUser(ctx, r)
	if err != nil {
		return err
	}
	if u.Policy["IsAdministrator"] == true || u.Policy["EnableMediaPlayback"] != true {
		return memberError("个人来源已停用")
	}
	return nil
}

// Apply mandatory restrictions on first use, without requiring prior enrollment.
func (s *MemberPlaybackService) ensureOriginalPlayback(ctx context.Context, r model.MemberPlayback, u emby.ManagedUser) error {
	if memberPolicyMatchesExpected(u.Policy, playbackRestrictions(u.Policy)) {
		return nil
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	_, client, err := s.config()
	if err != nil {
		return err
	}
	latest, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || latest.ID != r.EmbyID {
		return memberError("无法核验 Emby 原文件播放限制")
	}
	if latest.Policy["IsDisabled"] == true || latest.Policy["EnableMediaPlayback"] != u.Policy["EnableMediaPlayback"] || latest.Policy["EnableContentDownloading"] != u.Policy["EnableContentDownloading"] {
		return memberError("Emby 权限已变化，请重新发起请求")
	}
	policy := playbackRestrictions(latest.Policy)
	if err := client.SetManagedPolicy(ctx, r.EmbyID, policy); err != nil {
		return memberError("无法应用 Emby 原文件播放限制，请稍后重试")
	}
	verified, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || verified.ID != r.EmbyID || !memberPolicyMatchesExpected(verified.Policy, policy) {
		return memberError("Emby 尚未确认原文件播放限制")
	}
	return nil
}
