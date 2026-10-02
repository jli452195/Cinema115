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

// This is an explicit Emby edit, never a persisted desired state or retry job.
func (s *MemberPlaybackService) SetEmbyPlayback(ctx context.Context, id string, allowed, expected bool) (MemberPlaybackAdminView, error) {
	if err := s.lock(ctx); err != nil {
		return MemberPlaybackAdminView{}, err
	}
	defer s.unlock()
	r := s.record(id)
	c := s.emby.snapshot()
	if c.ServerID == "" || c.APIKey == "" {
		return MemberPlaybackAdminView{}, memberError("请先配置并验证 Emby 服务")
	}
	client := s.NewEmby(c.URL, c.APIKey)
	if r.UserID == "" || r.ServerID != c.ServerID || r.EmbyURL != c.URL || !r.Provisioned {
		return MemberPlaybackAdminView{}, memberError("用户身份未就绪，请刷新")
	}
	u, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || u.ID != r.EmbyID {
		return MemberPlaybackAdminView{}, memberError("无法读取 Emby 权限，请刷新后重试")
	}
	current, known := u.Policy["EnableMediaPlayback"].(bool)
	if !known || current != expected {
		return MemberPlaybackAdminView{}, memberError("Emby 播放权限已变化或无法确认，请刷新后重试")
	}
	if allowed && (u.Policy["IsDisabled"] == true || (r.PlaybackOnly && (r.ChildRevoked || r.ChildDisabled || r.ChildPaused || u.Policy["IsAdministrator"] == true))) {
		return MemberPlaybackAdminView{}, memberError("账号已停用、共享已暂停或撤销，不能开启播放")
	}
	policy := playbackRestrictions(u.Policy)
	policy["EnableMediaPlayback"] = allowed
	if s.ClearDirectLinks != nil {
		s.ClearDirectLinks()
	}
	if err := client.SetManagedPolicy(ctx, r.EmbyID, policy); err != nil {
		return MemberPlaybackAdminView{}, memberError("Emby 修改结果未确认，请刷新查看；不会自动重试")
	}
	verified, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || verified.ID != r.EmbyID || !memberPolicyMatchesExpected(verified.Policy, policy) {
		return MemberPlaybackAdminView{}, memberError("Emby 修改结果未确认，请刷新查看；不会自动重试")
	}
	return s.memberPermissionView(r, verified), nil
}

func (s *MemberPlaybackService) MemberPermissions(ctx context.Context, id string) (MemberPlaybackAdminView, error) {
	r := s.record(id)
	c := s.emby.snapshot()
	if c.ServerID == "" || c.APIKey == "" {
		return MemberPlaybackAdminView{}, memberError("请先配置并验证 Emby 服务")
	}
	client := s.NewEmby(c.URL, c.APIKey)
	if r.UserID == "" || r.ServerID != c.ServerID || r.EmbyURL != c.URL {
		return MemberPlaybackAdminView{}, memberError("用户不存在或服务已变化")
	}
	u, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || u.ID != r.EmbyID {
		return MemberPlaybackAdminView{}, memberError("无法读取 Emby 用户权限")
	}
	return s.memberPermissionView(r, u), nil
}

func (s *MemberPlaybackService) memberPermissionView(r model.MemberPlayback, u emby.ManagedUser) MemberPlaybackAdminView {
	r.Administrator = u.Policy["IsAdministrator"] == true
	r.EmbyName = u.Name
	v := MemberPlaybackAdminView{DriveStatus: s.DriveHealth(r), MemberPlayback: adminPlaybackRecord(r), Username: u.Name, Disabled: u.Policy["IsDisabled"] == true, PersonalDrive: PersonalDriveView{Connected: s.personalDrive(r).Connected}}
	if allowed, ok := u.Policy["EnableMediaPlayback"].(bool); ok {
		v.PlaybackAllowed = &allowed
	}
	if allowed, ok := u.Policy["EnableContentDownloading"].(bool); ok {
		v.DownloadAllowed = &allowed
	}
	if r.PlaybackOnly {
		v.OwnerName = s.record(r.OwnerUserID).EmbyName
	}
	return v
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
