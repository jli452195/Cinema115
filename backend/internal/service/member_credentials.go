package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"strm115/backend/internal/model"
	"time"
)

var memberUsername = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}_.-]{2,31}$`)

func (s *MemberPlaybackService) checkName(ctx context.Context, client memberEmbyClient, r model.MemberPlayback, name string) error {
	if !memberUsername.MatchString(name) || strings.HasPrefix(strings.ToLower(name), "c115_") {
		return memberError("用户名需为 3–32 位字母、汉字、数字、点、下划线或短横线，首位为字母或数字；c115_ 为系统保留前缀")
	}
	for _, other := range s.Records() {
		if other.UserID != r.UserID && (strings.EqualFold(other.RegistrationName, name) && !other.Provisioned || strings.EqualFold(other.PendingName, name)) {
			return memberError("该用户名正在注册或修改，请选择其他名称")
		}
	}
	users, err := client.Users(ctx)
	if err != nil {
		return memberError("无法检查 Emby 用户名，请稍后重试")
	}
	for _, user := range users {
		if user.ID != r.EmbyID && strings.EqualFold(user.Name, name) {
			return memberError("Emby 用户名已被占用，请选择其他名称")
		}
	}
	return nil
}

// Only the persisted ID identifies an existing account. Names are never used
// to recover a deleted user's identity or select a password mutation target.
func (s *MemberPlaybackService) credentialTarget(ctx context.Context, id string, revision uint64) (model.MemberPlayback, memberEmbyClient, error) {
	r := s.record(id)
	if r.Adopted {
		return r, nil, memberError("管理员仅开放网盘管理，已有 Emby 账号名称和密码由管理员维护")
	}
	if _, err := s.websiteUser(ctx, id); err != nil {
		return r, nil, err
	}
	if !r.Provisioned || r.EmbyID == "" || r.AdminBlocked {
		return r, nil, memberError("账号尚未完成注册或已被管理员停用")
	}
	if revision != r.AccountRevision {
		return r, nil, memberError("账号信息已变化，请刷新后重新提交")
	}
	c, client, err := s.config()
	if err != nil {
		return r, nil, err
	}
	if c.URL != r.EmbyURL || c.ServerID != r.ServerID {
		return r, nil, memberError("Emby 服务已变化，不能修改原账号")
	}
	u, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || u.ID != r.EmbyID {
		return r, nil, memberError("原 Emby 账号不存在或暂不可用，不会关联同名账号")
	}
	if u.Policy["IsDisabled"] == true {
		return r, nil, memberError("Emby 账号已禁用，请联系管理员")
	}
	if u.Name != r.EmbyName || r.PendingName != "" {
		r.EmbyName = u.Name
		r.PendingName = ""
		r.AccountRevision++
		r.AccountMessage = "已按原 Emby ID 核实当前用户名，请重新提交修改"
		if err := s.save(ctx, r); err != nil {
			return r, nil, err
		}
		return r, nil, memberError(r.AccountMessage)
	}
	return r, client, nil
}

func (s *MemberPlaybackService) renameAccount(ctx context.Context, client memberEmbyClient, r *model.MemberPlayback, name string) error {
	if err := s.checkName(ctx, client, *r, name); err != nil {
		return err
	}
	r.PendingName = name
	r.AccountRevision++
	r.AccountMessage = "用户名修改结果待确认"
	if err := s.save(ctx, *r); err != nil {
		return err
	}
	writeErr := client.RenameManagedUser(ctx, r.EmbyID, name)
	// Even when the response was lost, resolve the outcome only by the fixed ID.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	u, readErr := client.ManagedUser(readCtx, r.EmbyID)
	if readErr == nil && u.ID == r.EmbyID {
		r.EmbyName = u.Name
		r.PendingName = ""
		r.AccountRevision++
		if u.Name == name {
			r.AccountMessage = "用户名已更新，请使用新名称登录 Emby"
		} else {
			r.AccountMessage = "用户名未变更，请检查重名后重试"
		}
		if err := s.save(readCtx, *r); err != nil {
			return err
		}
		if u.Name == name {
			return nil
		}
	}
	_ = writeErr // Remote errors may contain implementation details; expose no secrets.
	return memberError("用户名修改结果尚未确认，请刷新状态；不会重新绑定其他账号")
}

func (s *MemberPlaybackService) RegisterAccount(ctx context.Context, id, name, password string, revision uint64) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	r, err := s.ensure(ctx, id)
	if err != nil {
		return err
	}
	if r.Provisioned {
		return memberError("Emby 已注册，请使用修改用户名或修改密码入口")
	}
	if revision != r.AccountRevision {
		return memberError("注册信息已变化，请刷新后重试")
	}
	if len(password) < 8 || len(password) > 128 {
		return memberError("Emby 密码需要 8–128 字节")
	}
	_, client, err := s.config()
	if err != nil {
		return err
	}
	if err := s.checkName(ctx, client, r, name); err != nil {
		return err
	}
	r.RegistrationName = name
	r.AccountRevision++
	if err := s.save(ctx, r); err != nil {
		return err
	}
	return s.provision(ctx, id, password)
}

func (s *MemberPlaybackService) RenameAccount(ctx context.Context, id, name string, revision uint64) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	r, client, err := s.credentialTarget(ctx, id, revision)
	if err != nil {
		return err
	}
	return s.renameAccount(ctx, client, &r, name)
}

func (s *MemberPlaybackService) ChangePassword(ctx context.Context, id, password string, revision uint64) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if len(password) < 8 || len(password) > 128 {
		return memberError("Emby 密码需要 8–128 字节")
	}
	r, client, err := s.credentialTarget(ctx, id, revision)
	if err != nil {
		return err
	}
	r.AccountRevision++
	r.AccountMessage = "密码修改结果待确认；如未确认成功，请重新提交新密码"
	if err := s.save(ctx, r); err != nil {
		return err
	}
	if err := client.SetManagedPassword(ctx, r.EmbyID, password); err != nil {
		return memberError(r.AccountMessage)
	}
	r.AccountMessage = "密码已更新，请在客户端重新登录；已有会话不保证立即退出"
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.save(persist, r); err != nil {
		return errors.Join(memberError("密码已提交，但本地结果保存失败，请刷新"), err)
	}
	return nil
}
