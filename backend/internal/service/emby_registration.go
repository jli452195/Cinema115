package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"sort"
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
	return s.observeUser(ctx, c, u)
}

func (s *MemberPlaybackService) observeUser(ctx context.Context, c EmbyConfig, remote emby.ManagedUser) (model.PortalUser, error) {
	u := model.PortalUser{ID: embyProfileID(c.ServerID, remote.ID), ServerID: c.ServerID, EmbyID: remote.ID, Username: remote.Name, Role: "user", TokenVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := s.db.SaveEmbyProfile(ctx, u); err != nil {
		return u, err
	}
	r := s.record(u.ID)
	changed := r.UserID == "" || r.Source == "" || r.Administrator != (remote.Policy["IsAdministrator"] == true) || r.EmbyName != remote.Name
	if r.UserID == "" {
		r = model.MemberPlayback{UserID: u.ID, ServerID: c.ServerID, EmbyID: u.EmbyID, EmbyName: u.Username, EmbyURL: c.URL, Provisioned: true, LoginEnabled: true, Stage: "pending"}
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
		r.SkipLibrarySync = true
		r.AllowPersonalSwitch = false
	}
	if changed {
		r.PolicyEnabled = false
		r.AppliedRevision = 0
		if err := s.save(ctx, r); err != nil {
			return u, err
		}
		s.RequestSync()
	}

	return s.db.PortalUser(ctx, u.ID)
}

func (s *MemberPlaybackService) RegisterInvited(ctx context.Context, hash, name, password string) (model.PortalUser, error) {
	var empty model.PortalUser
	if !memberUsername.MatchString(name) || strings.HasPrefix(strings.ToLower(name), "c115_") || len(password) < 8 || len(password) > 128 {
		return empty, memberError("用户名需为 3–32 位字母、汉字、数字、点、下划线或短横线，密码需为 8–128 字节；c115_ 为保留前缀")
	}
	if err := s.lock(ctx); err != nil {
		return empty, err
	}
	defer s.unlock()
	if !s.Settings().Enabled {
		return empty, memberError("邀请注册暂未开放")
	}
	c, client, err := s.config()
	if err != nil {
		return empty, err
	}
	r, err := s.db.Registration(ctx, hash)
	if err != nil {
		return empty, err
	}
	if r.ServerID != "" && (r.ServerID != c.ServerID || r.EmbyURL != c.URL || r.Name != name) {
		return empty, memberError("此邀请已有注册进度，请使用原用户名和原 Emby 服务继续")
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
		if r.Started {
			for _, u := range users {
				if u.Name == r.ReservedName {
					r.EmbyID = u.ID
				}
			}
			if r.EmbyID == "" {
				return empty, memberError("Emby 创建结果待确认，请稍后重试或联系管理员；不会重复创建")
			}
		} else {
			r.Started = true
			if err := s.db.SaveRegistration(ctx, hash, r); err != nil {
				return empty, err
			}
			u, err := client.CreateManagedUser(ctx, r.ReservedName)
			if err != nil {
				return empty, memberError("Emby 创建结果未确认，请使用相同邀请和用户名重试")
			}
			r.EmbyID = u.ID
		}
		if err := s.db.SaveRegistration(ctx, hash, r); err != nil {
			return empty, err
		}
	}
	remote, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || remote.Policy["IsAdministrator"] == true || (remote.Name != r.ReservedName && remote.Name != name) {
		return empty, memberError("注册中的 Emby 身份已变化，请联系管理员")
	}
	settings, err := memberPolicySettings(ctx, client, s.Settings())
	if err != nil {
		return empty, err
	}
	if err := client.SetManagedPolicy(ctx, r.EmbyID, memberPolicy(nil, false, settings)); err != nil {
		return empty, memberError("Emby 禁播策略保存失败，请重试")
	}
	denied, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || !memberPolicyMatches(denied.Policy, false, settings) {
		return empty, memberError("Emby 禁播策略尚未确认")
	}
	if err := client.SetManagedPassword(ctx, r.EmbyID, password); err != nil {
		return empty, memberError("密码设置结果未确认，请使用相同邀请重试")
	}
	if remote.Name != name {
		if err := client.RenameManagedUser(ctx, r.EmbyID, name); err != nil {
			return empty, memberError("用户名保存结果未确认，请使用相同邀请重试")
		}
	}
	u := model.PortalUser{ID: embyProfileID(c.ServerID, r.EmbyID), ServerID: c.ServerID, EmbyID: r.EmbyID, Username: name, Role: "user", TokenVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	m := model.MemberPlayback{UserID: u.ID, ServerID: c.ServerID, EmbyURL: c.URL, EmbyID: r.EmbyID, EmbyName: name, Source: r.Source, AllowPersonalSwitch: true, Provisioned: true, Stage: "provisioning", Message: "Emby 已注册，播放待开通"}
	if err := s.db.CompleteRegistration(ctx, hash, u, m); err != nil {
		return empty, err
	}
	s.mu.Lock()
	s.records[u.ID] = m
	s.mu.Unlock()
	// A committed identity can always retry policy synchronization from the
	// administrator view. Never consume an invitation twice after a lost reply.
	if err := client.SetManagedPolicy(ctx, m.EmbyID, memberPolicy(nil, false, settings, true)); err != nil {
		return u, memberError("账号已创建，登录权限待管理员同步，请勿重复注册")
	}
	if m.Source == "assigned" {
		if err := s.assignPool(ctx, &m); err != nil {
			return u, err
		}
	}
	if err := s.reconcile(ctx, m); err != nil {
		return u, memberError("账号已创建，权限待管理员同步，请勿重复注册")
	}
	return u, nil
}

// Assign once, sharing the least-used available administrator account. Existing
// assignments are never rotated or replaced merely because credentials fail.
func (s *MemberPlaybackService) assignPool(ctx context.Context, r *model.MemberPlayback) error {
	if r.Source != "assigned" || r.AssignedAccountID != "" {
		return nil
	}
	if err := s.resolvePool(ctx, r); err != nil {
		return err
	}
	return s.save(ctx, *r)
}

// Resolve without publishing so a combined settings edit can commit once.
func (s *MemberPlaybackService) resolvePool(ctx context.Context, r *model.MemberPlayback) error {
	used := make(map[string]int)
	for _, other := range s.Records() {
		if other.Source == "assigned" {
			used[other.AssignedAccountID]++
		}
	}
	options := s.AccountOptions()
	sort.Slice(options, func(i, j int) bool {
		if used[options[i].ID] == used[options[j].ID] {
			return options[i].ID < options[j].ID
		}
		return used[options[i].ID] < used[options[j].ID]
	})
	for _, a := range options {
		if !a.Configured || a.PlaybackParentCID == "" {
			continue
		}
		account, err := s.accounts.Get(a.ID)
		if err != nil {
			continue
		}
		valid, err := s.checker.CheckCookie(ctx, account.Cookie)
		if err != nil || !valid {
			continue
		}
		if s.drive.ValidateCacheParent(ctx, account.PlaybackParentCID, account.Cookie) != nil {
			continue
		}
		r.AssignedAccountID = a.ID
		return nil
	}
	r.Source = "primary"
	r.AssignedAccountID = ""
	return nil
}
