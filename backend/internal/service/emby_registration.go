package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
	"time"
)

var memberUsername = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}_.-]{2,31}$`)

func (s *MemberPlaybackService) ObserveUser(ctx context.Context, c EmbyConfig, u emby.ManagedUser) (model.PortalUser, error) {
	if err := s.lock(ctx); err != nil {
		return model.PortalUser{}, err
	}
	defer s.unlock()
	profile, err := s.observeUser(ctx, c, u)
	if err != nil {
		return profile, err
	}
	s.RequestSync()
	return profile, nil
}

func (s *MemberPlaybackService) observeUser(ctx context.Context, c EmbyConfig, remote emby.ManagedUser) (model.PortalUser, error) {
	if s.deletionBlocked(embyProfileID(c.ServerID, remote.ID)) {
		return model.PortalUser{}, ErrLogin
	}
	u := model.PortalUser{ID: embyProfileID(c.ServerID, remote.ID), ServerID: c.ServerID, EmbyID: remote.ID, Username: remote.Name, Role: "user", TokenVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := s.db.SaveEmbyProfile(ctx, u); err != nil {
		return u, err
	}
	r := s.record(u.ID)
	changed := r.UserID == "" || r.Source == "" || r.Administrator != (remote.Policy["IsAdministrator"] == true) || r.EmbyName != remote.Name
	if r.UserID == "" {
		r = model.MemberPlayback{UserID: u.ID, ServerID: c.ServerID, EmbyID: u.EmbyID, EmbyName: u.Username, EmbyURL: c.URL, Provisioned: true, Stage: "pending"}
	}
	if r.Source == "" {
		if r.PlaybackOnly {
			r.Source = "personal_shared"
		} else {
			r.Source = "primary"
			r.AllowPersonalSwitch = true
		}
	}
	if r.PlaybackOnly {
		r.AllowPersonalSwitch = false
	}
	r.Administrator = remote.Policy["IsAdministrator"] == true
	r.EmbyName = remote.Name
	if r.Administrator {
		r.AllowPersonalSwitch = false
	}
	if changed {
		if err := s.save(ctx, r); err != nil {
			return u, err
		}
	}

	return s.db.PortalUser(ctx, u.ID)
}

func (s *MemberPlaybackService) RegisterInvited(ctx context.Context, hash, name, password string) (model.PortalUser, error) {
	var empty model.PortalUser
	if !memberUsername.MatchString(name) || strings.HasPrefix(strings.ToLower(name), "c115_") || len(password) < 8 || len(password) > 128 {
		return empty, memberError("用户名需为 3–32 位字母、汉字、数字、点、下划线或短横线，密码需为 8–128 字节；c115_ 为保留前缀")
	}
	if err := s.lockCreation(ctx); err != nil {
		return empty, err
	}
	defer s.unlock()
	c := s.emby.snapshot()
	if c.URL == "" || c.APIKey == "" || c.ServerID == "" {
		return empty, memberError("请先配置并验证 Emby 服务")
	}
	current := s.Settings()
	if current.ServerID != "" && (current.ServerID != c.ServerID || current.EmbyURL != c.URL) {
		return empty, memberError("Emby 服务配置已变化，请管理员重新检查")
	}
	client := s.NewEmby(c.URL, c.APIKey)
	r, err := s.db.Registration(ctx, hash)
	if err != nil {
		return empty, err
	}
	if r.ServerID != "" && (r.ServerID != c.ServerID || r.EmbyURL != c.URL || r.Name != name) {
		return empty, memberError("此邀请已有注册进度，请使用原用户名和原 Emby 服务继续")
	}
	settings := current
	if r.ActivationPolicy == nil {
		if len(current.LibraryIDs) == 0 {
			return empty, memberError("尚未配置新注册用户的默认媒体库，请联系管理员")
		}
		settings, err = memberPolicySettings(ctx, client, current)
		if err != nil {
			return empty, err
		}
	}
	users, err := client.Users(ctx)
	if err != nil {
		return empty, memberError("无法检查 Emby 用户名")
	}
	for _, u := range users {
		if strings.EqualFold(u.Name, name) && u.ID != r.EmbyID {
			return empty, memberError("Emby 用户名已存在，请直接登录或使用其他名称")
		}
	}
	if r.ServerID == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return empty, err
		}
		r.ServerID, r.EmbyURL, r.Name, r.ReservedName = c.ServerID, c.URL, name, "c115_"+hex.EncodeToString(random[:])
		if err := s.db.SaveRegistration(ctx, hash, r); err != nil {
			return empty, err
		}
	}
	if r.EmbyID == "" {
		if !r.Started {
			r.Started = true
			if err := s.db.SaveRegistration(ctx, hash, r); err != nil {
				return empty, err
			}
			u, createErr := client.CreateManagedUser(ctx, r.ReservedName)
			if createErr == nil {
				r.EmbyID = u.ID
			}
		}
		if r.EmbyID == "" {
			r.EmbyID = confirmReservedUser(ctx, client, r.ReservedName)
		}
		if r.EmbyID == "" {
			return empty, memberError("Emby 创建结果待确认，请使用原邀请重试；不会重复创建，持续未确认请联系管理员")
		}

		if err := s.db.SaveRegistration(ctx, hash, r); err != nil {
			return empty, err
		}
	}
	remote, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || remote.ID != r.EmbyID || remote.Policy["IsAdministrator"] == true || (remote.Name != r.ReservedName && remote.Name != name) {
		return empty, memberError("注册中的 Emby 身份已变化，请联系管理员")
	}
	if r.ActivationPolicy != nil && remote.Name != name {
		return empty, memberError("注册中的用户名已变化，请联系管理员")
	}
	u := model.PortalUser{ID: embyProfileID(c.ServerID, r.EmbyID), ServerID: c.ServerID, EmbyID: r.EmbyID, Username: name, Role: "user", TokenVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	m := s.record(u.ID)
	if m.UserID != "" && (m.PlaybackOnly || !m.CreatedByCinema || m.Source != r.Source || m.Provisioned) {
		return empty, memberError("注册中的本地身份已变化，请联系管理员")
	}
	if m.UserID == "" {
		m = model.MemberPlayback{UserID: u.ID, ServerID: c.ServerID, EmbyURL: c.URL, EmbyID: r.EmbyID, EmbyName: name, Source: r.Source, CreatedByCinema: true, AllowPersonalSwitch: true, Stage: "provisioning", Message: "注册未完成，请使用原邀请和用户名继续"}
	}
	if err := s.db.PrepareRegistration(ctx, hash, u, m); err != nil {
		return empty, err
	}
	s.publishChild(m)
	if r.ActivationPolicy == nil {
		if !saveProvisioningPolicy(ctx, client, r.EmbyID, memberPolicy(nil, false, settings)) {
			return empty, memberError("Emby 禁播策略尚未确认，请使用原邀请重试")
		}
		if err := client.SetManagedPassword(ctx, r.EmbyID, password); err != nil {
			return empty, memberError("密码设置结果未确认，请使用相同邀请和密码重试")
		}
		if remote.Name != name && !renameProvisionedUser(ctx, client, r.EmbyID, name) {
			return empty, memberError("用户名保存结果未确认，请使用相同邀请重试")
		}
		r.ActivationPolicy = memberPolicy(nil, true, settings, true)
		if err := s.db.SaveRegistration(ctx, hash, r); err != nil {
			return empty, err
		}
	}
	if !saveProvisioningPolicy(ctx, client, r.EmbyID, r.ActivationPolicy) {
		return empty, memberError("账号已创建，登录权限尚未确认，请使用相同邀请和用户名继续；不会重复创建")
	}
	m.Provisioned = true
	m.Stage, m.Message = "pending", "Emby 注册完成，网盘待检查"
	if err := s.db.CompleteRegistration(ctx, hash, u, m); err != nil {
		disableUncommittedUser(ctx, client, r.EmbyID, r.ActivationPolicy)
		return empty, err
	}
	s.publishChild(m)
	// Drive health is independent of account creation and cannot turn a completed
	// registration into an error with an already consumed invitation.
	s.requestHealthCheck()
	return u, nil
}
