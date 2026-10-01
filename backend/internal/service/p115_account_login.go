package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

var ErrAccountInvalidCookie = errors.New("Cookie 无效或已过期，原配置未修改")
var ErrAccountVerification = errors.New("暂时无法验证 Cookie，原配置未修改")

type AccountLoginChecker interface {
	CheckCookie(context.Context, string) (bool, error)
}

type AccountDirectoryChecker interface {
	ValidateCacheParent(context.Context, string, string) error
}

// PreparedManagedLogin pins verified credentials and directory ownership until commit.
type PreparedManagedLogin struct {
	summary                     model.P115Summary
	id, name, cookie, directory string
	revision                    uint64
	client                      []string
}

func (s *P115Accounts) VerifyManagedCredential(ctx context.Context, id string, revision uint64, cookie string, checker AccountLoginChecker) (P115Account, error) {
	a, err := s.Get(id)
	if err != nil {
		return a, err
	}
	if a.Revision != revision {
		return a, ErrAccountChanged
	}
	if a.Ownership == "member" || a.OwnerUserID != "" {
		return a, ErrAccountInput
	}
	if accountUID(cookie) == "" {
		return a, ErrAccountInvalidCookie
	}
	if checker == nil {
		return a, ErrAccountVerification
	}
	var valid bool
	a.Summary = model.P115Summary{}
	if reader, ok := checker.(interface {
		QuerySummary(context.Context, string, string) (model.P115Summary, bool, error)
	}); ok {
		a.Summary, valid, err = reader.QuerySummary(ctx, id, cookie)
	} else {
		valid, err = checker.CheckCookie(ctx, cookie)
	}
	if err != nil {
		return a, ErrAccountVerification
	}
	if !valid {
		return a, ErrAccountInvalidCookie
	}
	if err := ctx.Err(); err != nil {
		return a, err
	}
	return a, nil
}

func (s *P115Accounts) PrepareManagedLogin(ctx context.Context, id string, revision uint64, name, cookie, directory string, checker AccountLoginChecker, drive AccountDirectoryChecker, loginClient ...string) (*PreparedManagedLogin, error) {
	if len(loginClient) > 0 && !p115.KnownLoginApp(loginClient[0]) {
		return nil, ErrAccountInput
	}
	a, err := s.VerifyManagedCredential(ctx, id, revision, cookie, checker)
	if err != nil {
		return nil, err
	}
	prepared := ""
	if !a.Primary && a.UID != "" && a.UID != accountUID(cookie) {
		directory = strings.TrimSpace(directory)
		if directory == "" || strings.Trim(directory, "0123456789") != "" {
			return nil, fmt.Errorf("%w：请选择新网盘的秒传目录", ErrAccountInput)
		}
		if drive == nil {
			return nil, fmt.Errorf("%w：目录验证服务不可用", ErrAccountInput)
		}
		if err := drive.ValidateCacheParent(ctx, directory, cookie); err != nil {
			return nil, fmt.Errorf("%w：新网盘目录校验失败，原配置未修改", ErrAccountInput)
		}
		prepared = directory
	}
	return &PreparedManagedLogin{summary: a.Summary, id: id, revision: revision, name: name, cookie: cookie, directory: prepared, client: append([]string(nil), loginClient...)}, nil
}

func (s *P115Accounts) CommitManagedLogin(ctx context.Context, prepared *PreparedManagedLogin) error {
	if prepared == nil {
		return ErrAccountInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.updateWithSummary(prepared.id, prepared.revision, prepared.name, &prepared.cookie, prepared.directory, &prepared.summary, prepared.client...)
}

func (s *P115Accounts) LoginManaged(ctx context.Context, id string, revision uint64, name, cookie, directory string, checker AccountLoginChecker, drive AccountDirectoryChecker, loginClient ...string) error {
	prepared, err := s.PrepareManagedLogin(ctx, id, revision, name, cookie, directory, checker, drive, loginClient...)
	if err != nil {
		return err
	}
	return s.CommitManagedLogin(ctx, prepared)
}

func (s *P115Accounts) SaveManagedDirectory(ctx context.Context, id string, revision uint64, cid string, drive AccountDirectoryChecker) error {
	a, err := s.Get(id)
	if err != nil {
		return err
	}
	if a.Revision != revision {
		return ErrAccountChanged
	}
	if a.Primary || a.Ownership == "member" || a.OwnerUserID != "" {
		return ErrAccountInput
	}
	cid = strings.TrimSpace(cid)
	if cid != "" {
		if strings.Trim(cid, "0123456789") != "" || a.Cookie == "" || drive == nil {
			return ErrAccountInput
		}
		if err := drive.ValidateCacheParent(ctx, cid, a.Cookie); err != nil {
			return fmt.Errorf("%w：目录不可访问，原目录未修改", ErrAccountInput)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.UpdatePlaybackDirectory(id, revision, cid)
}

// SupplementLoginClient updates display metadata only. The credential revision
// remains unchanged so playback snapshots and QR sessions remain valid.
func (s *P115Accounts) SupplementLoginClient(snapshot P115Account, client string) error {
	if !p115.KnownLoginApp(client) || snapshot.Cookie == "" {
		return ErrAccountInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.copy()
	for i, a := range value.Accounts {
		if a.ID != snapshot.ID {
			continue
		}
		if a.Cookie != snapshot.Cookie || a.Revision != snapshot.Revision {
			return ErrAccountChanged
		}
		if a.Ownership == "member" || a.OwnerUserID != "" {
			return ErrAccountInput
		}
		if a.LoginClient != "" {
			return nil
		}
		value.Accounts[i].LoginClient = client
		return s.commit(value)
	}
	return ErrAccountMissing
}
