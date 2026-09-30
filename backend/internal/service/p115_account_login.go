package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrAccountInvalidCookie = errors.New("Cookie 无效或已过期，原配置未修改")
var ErrAccountVerification = errors.New("暂时无法验证 Cookie，原配置未修改")

type AccountLoginChecker interface {
	CheckCookie(context.Context, string) (bool, error)
}

type AccountDirectoryChecker interface {
	ValidateCacheParent(context.Context, string, string) error
}

// LoginManaged validates a credential snapshot before committing. Shared users
// continue referencing the same slot, while UID scopes isolate their file copies.
func (s *P115Accounts) LoginManaged(ctx context.Context, id string, revision uint64, name, cookie, directory string, checker AccountLoginChecker, drive AccountDirectoryChecker) error {
	a, err := s.Get(id)
	if err != nil {
		return err
	}
	if a.Revision != revision {
		return ErrAccountChanged
	}
	if s.IsPersonal(id) {
		return fmt.Errorf("%w：个人网盘只能由本人管理", ErrAccountInput)
	}
	uid := accountUID(cookie)
	if uid == "" {
		return ErrAccountInvalidCookie
	}
	if checker == nil {
		return fmt.Errorf("%w：账号验证服务不可用", ErrAccountInput)
	}
	valid, err := checker.CheckCookie(ctx, cookie)
	if err != nil {
		return ErrAccountVerification
	}
	if !valid {
		return ErrAccountInvalidCookie
	}
	prepared := ""
	if !a.Primary && a.UID != "" && a.UID != uid {
		directory = strings.TrimSpace(directory)
		if directory == "" || strings.Trim(directory, "0123456789") != "" {
			return fmt.Errorf("%w：换号需要填写新网盘的播放目录编号", ErrAccountInput)
		}
		if drive == nil {
			return fmt.Errorf("%w：目录验证服务不可用", ErrAccountInput)
		}
		if err := drive.ValidateCacheParent(ctx, directory, cookie); err != nil {
			return fmt.Errorf("%w：新网盘目录校验失败，原配置未修改", ErrAccountInput)
		}
		prepared = directory
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.update(id, revision, name, &cookie, prepared)
}
