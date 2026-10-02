package service

import (
	"context"
	"slices"
	"strm115/backend/internal/model"
)

type MemberDirectoryAccount struct {
	AccountID         string `json:"accountId"`
	Revision          uint64 `json:"revision"`
	UID               string `json:"uid"`
	DirectoryID       string `json:"directoryId"`
	CreationUncertain bool   `json:"creationUncertain"`
}

type MemberDirectoryInput struct {
	AccountID   string `json:"accountId"`
	Revision    uint64 `json:"revision"`
	DirectoryID string `json:"directoryId"`
	Create      bool   `json:"create"`
}

type MemberDirectory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type MemberDirectoryPage struct {
	Directories []MemberDirectory `json:"directories"`
	NextOffset  int               `json:"nextOffset"`
	HasMore     bool              `json:"hasMore"`
}

func memberDirectoryID(id string, root bool) bool {
	if id == "0" {
		return root
	}
	if len(id) == 0 || len(id) > 32 || id[0] == '0' {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func personalDirectoryTarget(r model.MemberPlayback) string {
	if r.PendingAccountID != "" {
		return r.PendingAccountID
	}
	return r.AccountID
}

func (s *MemberPlaybackService) directoryAccountView(r model.MemberPlayback) *MemberDirectoryAccount {
	a, err := s.accounts.Get(personalDirectoryTarget(r))
	if err != nil || a.Primary || a.OwnerUserID != r.UserID || a.Ownership != "member" || a.Cookie == "" {
		return nil
	}
	return &MemberDirectoryAccount{AccountID: a.ID, Revision: a.Revision, UID: a.UID, DirectoryID: a.PlaybackParentCID, CreationUncertain: slices.Contains(r.UncertainDirectories, a.ID) || r.PendingAccountID == a.ID && r.PendingDirectoryStarted}
}

// Caller holds the member gate. All remote requests use this credential snapshot.
func (s *MemberPlaybackService) directoryTarget(ctx context.Context, id string, input MemberDirectoryInput) (model.MemberPlayback, P115Account, error) {
	r := s.record(id)
	if _, err := s.portalUser(ctx, id); err != nil {
		return r, P115Account{}, err
	}
	if r.Source != "personal" && !r.AllowPersonalSwitch {
		return r, P115Account{}, memberError("当前不能管理个人网盘目录")
	}
	if input.AccountID == "" || input.AccountID != personalDirectoryTarget(r) {
		return r, P115Account{}, ErrAccountChanged
	}
	a, err := s.accounts.Get(input.AccountID)
	if err != nil || a.Primary || a.Ownership != "member" || a.OwnerUserID != id || a.Revision != input.Revision {
		return r, P115Account{}, ErrAccountChanged
	}
	if a.Cookie == "" || a.UID == "" || accountUID(a.Cookie) != a.UID {
		return r, P115Account{}, memberError("个人网盘登录已失效，请重新扫码")
	}
	return r, a, nil
}

func (s *MemberPlaybackService) PersonalDirectories(ctx context.Context, id string, input MemberDirectoryInput, offset int) (*MemberDirectoryPage, error) {
	if !memberDirectoryID(input.DirectoryID, true) || offset < 0 || offset > 20000 {
		return nil, memberError("目录或分页参数无效")
	}
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.unlock()
	_, a, err := s.directoryTarget(ctx, id, input)
	if err != nil {
		return nil, err
	}
	page, err := s.drive.DriveFiles(ctx, input.DirectoryID, offset, a.Cookie)
	if err != nil {
		return nil, memberError("读取个人网盘目录失败，请检查登录状态后重试")
	}
	result := &MemberDirectoryPage{Directories: make([]MemberDirectory, 0, len(page.Files)), NextOffset: offset + len(page.Files), HasMore: offset+len(page.Files) < page.Count}
	for _, f := range page.Files {
		if f.Directory {
			result.Directories = append(result.Directories, MemberDirectory{ID: f.ID, Name: f.Name})
		}
	}
	return result, nil
}

func (s *MemberPlaybackService) SetPersonalDirectory(ctx context.Context, id string, input MemberDirectoryInput) error {
	if !memberDirectoryID(input.DirectoryID, input.Create) {
		return memberError("请选择一个文件夹作为秒传保存目录")
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	r, a, err := s.directoryTarget(ctx, id, input)
	if err != nil {
		return err
	}
	valid, err := s.checker.CheckCookie(ctx, a.Cookie)
	if err != nil || !valid {
		return memberError("个人网盘登录已失效，请重新扫码")
	}
	if err := s.drive.ValidateCacheParent(ctx, input.DirectoryID, a.Cookie); err != nil {
		return memberError("目录校验失败，请重新选择")
	}
	cid := input.DirectoryID
	if input.Create {
		if slices.Contains(r.UncertainDirectories, a.ID) || r.PendingAccountID == a.ID && r.PendingDirectoryStarted {
			return memberError("上次创建结果未确认，请浏览网盘并选择已创建的文件夹")
		}
		// Persist intent before writing upstream; an uncertain result must not be retried.
		r.UncertainDirectories = append(append([]string{}, r.UncertainDirectories...), a.ID)
		if r.PendingAccountID == a.ID {
			r.PendingDirectoryStarted = true
		}
		if err := s.save(ctx, r); err != nil {
			return err
		}
		cid, err = s.drive.CreateDirectory(ctx, input.DirectoryID, "Cinema115", a.Cookie)
		if err != nil {
			return memberError("文件夹创建结果未确认，请浏览网盘并选择已创建的文件夹；原绑定保留")
		}
		if !memberDirectoryID(cid, false) || s.drive.ValidateCacheParent(ctx, cid, a.Cookie) != nil {
			return memberError("新目录校验未完成，请浏览网盘核查；原绑定保留")
		}
	}
	r, err = s.accounts.SetMemberDirectory(ctx, r, a.ID, a.Revision, cid)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.records[id] = r
	s.mu.Unlock()
	delete(s.sessions, id)
	if s.ClearDirectLinks != nil {
		s.ClearDirectLinks()
	}
	if r.Source == "personal" {
		return s.reconcile(ctx, r)
	}
	return nil
}
