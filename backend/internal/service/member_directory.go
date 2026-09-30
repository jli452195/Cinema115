package service

import (
	"context"
	"strm115/backend/internal/model"
)

func (s *MemberPlaybackService) prepareDirectory(ctx context.Context, r model.MemberPlayback) (model.MemberPlayback, error) {
	id := r.UserID
	a, err := s.accounts.Get(r.AccountID)
	if err != nil || a.Cookie == "" || a.UID == "" {
		return r, memberError("请先扫码绑定个人 115")
	}
	if a.Primary || a.Ownership != "member" || a.OwnerUserID != id || accountUID(a.Cookie) != a.UID {
		return r, memberError("个人网盘归属或身份不一致，请联系管理员检查")
	}
	valid, err := s.checker.CheckCookie(ctx, a.Cookie)
	if err != nil || !valid {
		return r, memberError("115 登录已失效，请重新扫码")
	}
	if r.DirectoryID == "" {
		if r.DirectoryStarted {
			return r, memberError("目录创建结果待核查，请在个人中心确认目录编号")
		}
		r.DirectoryStarted = true
		if err := s.save(ctx, r); err != nil {
			return r, err
		}
		cid, err := s.drive.CreateDirectory(ctx, "0", "Cinema115_"+id, a.Cookie)
		if err != nil {
			return r, memberError("播放目录创建结果未确认，请在个人网盘核查并确认目录")
		}
		r.DirectoryID = cid
		if err := s.save(ctx, r); err != nil {
			return r, err
		}
	}
	if err := s.drive.ValidateCacheParent(ctx, r.DirectoryID, a.Cookie); err != nil {
		return r, memberError("播放目录不可用，请联系管理员检查")
	}
	if a.PlaybackParentCID != r.DirectoryID {
		if err := s.accounts.UpdatePlaybackDirectory(a.ID, a.Revision, r.DirectoryID); err != nil {
			return r, err
		}
	}

	return r, nil
}
