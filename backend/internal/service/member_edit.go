package service

import (
	"context"
	"strm115/backend/internal/model"
)

type MemberEditValues struct {
	Source     string `json:"source"`
	AccountID  string `json:"accountId"`
	ChildLimit int    `json:"childLimit"`
}
type MemberEditInput struct {
	MemberEditValues
	Expected MemberEditValues `json:"expected"`
}

func memberEditValues(r model.MemberPlayback) MemberEditValues {
	return MemberEditValues{Source: r.Source, ChildLimit: r.ChildLimit}
}

// Save the complete form once; Emby synchronization is explicitly asynchronous.
func (s *MemberPlaybackService) EditMember(ctx context.Context, id string, in MemberEditInput) error {
	if in.AccountID != "" || in.Expected.AccountID != "" {
		return memberError("共享池自动调度，不接受指定小号")
	}
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
		if admin || in.Source != r.Source || in.AccountID != "" || in.ChildLimit != r.ChildLimit {
			return memberError("子账号不能修改播放来源和配额")
		}
	} else {
		if in.Source != "primary" && in.Source != "assigned" && in.Source != "personal" {
			return memberError("播放来源无效")
		}
		if admin && (in.Source == "personal" || in.ChildLimit != r.ChildLimit) {
			return memberError("管理员不支持个人网盘及子账号配额")
		}
	}
	sourceChanged := in.Source != r.Source
	if sourceChanged {
		if u.Policy["IsDisabled"] == true {
			return memberError("Emby 用户已停用，不能修改播放来源")
		}
		r.Source = in.Source
		r.AllowPersonalSwitch = !admin
		r.Suspended = false
	}
	policyChanged := sourceChanged
	r.ChildLimit = in.ChildLimit
	if sourceChanged {
		r.CheckedAt = ""
		r.CheckError = ""
		r.Stage = "pending"
		r.Message = "网盘尚未检查"
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
