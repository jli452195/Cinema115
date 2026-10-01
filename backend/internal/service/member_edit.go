package service

import (
	"context"
	"strm115/backend/internal/model"
)

type MemberEditValues struct {
	Source          string `json:"source"`
	AccountID       string `json:"accountId"`
	ChildLimit      int    `json:"childLimit"`
	SkipLibrarySync bool   `json:"skipLibrarySync"`
}
type MemberEditInput struct {
	MemberEditValues
	Expected MemberEditValues `json:"expected"`
}

func memberEditValues(r model.MemberPlayback) MemberEditValues {
	return MemberEditValues{Source: r.Source, AccountID: r.AssignedAccountID, ChildLimit: r.ChildLimit, SkipLibrarySync: r.SkipLibrarySync}
}

// Save the complete form once; Emby synchronization is explicitly asynchronous.
func (s *MemberPlaybackService) EditMember(ctx context.Context, id string, in MemberEditInput) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	r := s.record(id)
	if r.UserID == "" || memberEditValues(r) != in.Expected {
		return memberError("用户配置已变化，请关闭弹窗并刷新后重试")
	}
	if in.ChildLimit < 0 || in.ChildLimit > 20 {
		return memberError("子账号名额需为 0–20")
	}
	c, client, err := s.config()
	if err != nil {
		return err
	}
	if r.ServerID != c.ServerID || r.EmbyURL != c.URL {
		return memberError("Emby 服务已变化")
	}
	u, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || u.ID != r.EmbyID {
		return memberError("无法核验 Emby 用户")
	}
	admin := u.Policy["IsAdministrator"] == true
	if admin != r.Administrator {
		return memberError("用户身份已变化，请刷新")
	}
	if r.PlaybackOnly {
		if admin || in.Source != r.Source || in.AccountID != r.AssignedAccountID || in.ChildLimit != r.ChildLimit {
			return memberError("子账号不能修改播放来源和配额")
		}
	} else {
		if in.Source != "primary" && in.Source != "assigned" && in.Source != "personal" {
			return memberError("播放来源无效")
		}
		if admin && (in.Source == "personal" || !in.SkipLibrarySync || in.ChildLimit != r.ChildLimit) {
			return memberError("管理员固定属于不同步组，不支持个人网盘及子账号配额")
		}
	}
	sourceChanged := in.Source != r.Source || in.AccountID != r.AssignedAccountID
	if sourceChanged {
		if u.Policy["IsDisabled"] == true {
			return memberError("Emby 用户已停用，不能修改播放来源")
		}
		if in.Source != "assigned" && in.AccountID != "" {
			return memberError("播放来源参数无效")
		}
		if in.Source == "assigned" && in.AccountID != "" {
			a, err := s.accounts.Get(in.AccountID)
			if err != nil || a.Primary || s.accounts.IsPersonal(a.ID) || a.PlaybackParentCID == "" {
				return memberError("请选择已配置目录的共享小号")
			}
		}
		r.Source, r.AssignedAccountID = in.Source, in.AccountID
		r.AllowPersonalSwitch = !admin
		r.Suspended = false
		if r.Source == "assigned" && r.AssignedAccountID == "" {
			if err := s.resolvePool(ctx, &r); err != nil {
				return err
			}
		}
	}
	policyChanged := sourceChanged || r.SkipLibrarySync != in.SkipLibrarySync
	r.ChildLimit, r.SkipLibrarySync = in.ChildLimit, in.SkipLibrarySync
	if policyChanged {
		r.PolicyEnabled = false
		r.AppliedRevision = 0
	}
	if err := s.save(ctx, r); err != nil {
		return err
	}
	if policyChanged {
		delete(s.sessions, id)
		if s.ClearDirectLinks != nil {
			s.ClearDirectLinks()
		}
		s.RequestSync()
	}
	return nil
}
