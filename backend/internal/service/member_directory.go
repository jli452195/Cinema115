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
		return r, memberError("请先选择秒传保存目录")
	}
	if err := s.drive.ValidateCacheParent(ctx, r.DirectoryID, a.Cookie); err != nil {
		return r, memberError("播放目录不可用，请联系管理员检查")
	}
	if a.PlaybackParentCID != r.DirectoryID {
		return r, ErrAccountChanged
	}
	return r, nil
}
