package service

import (
	"context"
	"strm115/backend/internal/model"
)

func (s *MemberPlaybackService) playbackAccount(r model.MemberPlayback) (P115Account, error) {
	var a P115Account
	var err error
	switch r.Source {
	case "assigned":
		a, err = s.accounts.Get(r.AssignedAccountID)
		if a.Primary || s.accounts.IsPersonal(a.ID) {
			return a, memberError("分配的小号不可用")
		}
	case "personal":
		a, err = s.accounts.Get(r.AccountID)
		if a.Primary || a.OwnerUserID != r.UserID || a.Ownership != "member" || r.DirectoryID == "" || a.PlaybackParentCID != r.DirectoryID {
			return a, memberError("个人网盘不可用")
		}
	default:
		return a, memberError("播放尚未授权")
	}
	if err != nil || a.Cookie == "" || a.UID == "" || accountUID(a.Cookie) != a.UID || a.PlaybackParentCID == "" {
		return a, memberError("播放账号不可用，保留绑定且不回退主账号")
	}
	return a, nil
}

func (s *MemberPlaybackService) SelectionMatches(embyID string, selected *PlaybackCacheSelection) bool {
	for _, r := range s.Records() {
		if r.EmbyID != embyID || embyID == "" {
			continue
		}
		target := r.AccountID
		if r.Source == "assigned" {
			target = r.AssignedAccountID
		}
		return selected != nil && target != "" && selected.binding.AccountID == target
	}
	return true
}

func (s *MemberPlaybackService) CurrentView(ctx context.Context, id string) (MemberPlaybackView, error) {
	if _, err := s.ensure(ctx, id); err != nil {
		return MemberPlaybackView{}, err
	}
	return s.View(id), nil
}

func (s *MemberPlaybackService) AccountOptions() []P115AccountView {
	accounts := s.accounts.List()
	out := make([]P115AccountView, 0, len(accounts))
	for _, a := range accounts {
		if !a.Primary && a.OwnerUserID == "" && a.Ownership != "member" && !IsMemberAccount(a.ID) {
			out = append(out, a)
		}
	}
	return out
}

func (s *MemberPlaybackService) SetSource(ctx context.Context, id, source, accountID string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if source != "personal" && source != "assigned" && source != "" {
		return memberError("播放方式无效")
	}
	r, err := s.ensure(ctx, id)
	if err != nil {
		return err
	}
	if source == "assigned" && accountID != "" {
		for _, other := range s.Records() {
			if other.UserID != id && other.AssignedAccountID == accountID {
				return memberError("此小号已分配给其他用户")
			}
		}
		a, err := s.accounts.Get(accountID)
		if err != nil || a.Primary || s.accounts.IsPersonal(a.ID) || a.PlaybackParentCID == "" {
			return memberError("请选择已配置播放目录的管理员小号")
		}
	} else {
		accountID = ""
	}
	r.Source, r.AssignedAccountID = source, accountID
	r.AllowPersonalSwitch = source != ""
	r.PolicyEnabled = false
	r.AppliedRevision = 0
	r.Suspended = false
	delete(s.sessions, id)
	if s.ClearDirectLinks != nil {
		s.ClearDirectLinks()
	}
	if err := s.save(ctx, r); err != nil {
		return err
	}
	if err := s.assignPool(ctx, &r); err != nil {
		return err
	}
	return s.reconcile(ctx, r)
}

func (s *MemberPlaybackService) UsePersonalDrive(ctx context.Context, id string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if _, err := s.portalUser(ctx, id); err != nil {
		return err
	}
	r := s.record(id)
	if !r.AllowPersonalSwitch || r.AdminBlocked || !s.Settings().Enabled {
		return memberError("尚未授权切换到个人网盘")
	}
	prepared, err := s.prepareDirectory(ctx, r)
	if err != nil {
		return err
	}
	prepared.Source = "personal"
	prepared.AssignedAccountID = ""
	prepared.Suspended = false
	if _, err := s.playbackAccount(prepared); err != nil {
		return err
	}
	prepared.PolicyEnabled = false
	prepared.AppliedRevision = 0
	if err := s.save(ctx, prepared); err != nil {
		return err
	}
	delete(s.sessions, id)
	if s.ClearDirectLinks != nil {
		s.ClearDirectLinks()
	}
	return s.reconcile(ctx, prepared)
}
