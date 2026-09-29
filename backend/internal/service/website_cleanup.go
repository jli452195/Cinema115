package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
)

type WebsiteCleanupPreview struct {
	UserID        string            `json:"userId"`
	Username      string            `json:"username"`
	EmbyID        string            `json:"embyId"`
	EmbyName      string            `json:"embyName"`
	Adopted       bool              `json:"adopted"`
	Source        string            `json:"source"`
	Drives        []P115AccountView `json:"drives"`
	State         string            `json:"state"`
	Mode          string            `json:"mode"`
	BlockedReason string            `json:"blockedReason"`
	Fingerprint   string            `json:"fingerprint"`
}

func (s *MemberPlaybackService) cleanupPreview(ctx context.Context, id string) (WebsiteCleanupPreview, error) {
	r := s.record(id)
	p := WebsiteCleanupPreview{UserID: id, EmbyID: r.EmbyID, EmbyName: r.EmbyName, Adopted: r.Adopted, Source: r.Source, State: r.CleanupState, Mode: r.CleanupMode, Drives: []P115AccountView{}}
	var version int64
	if r.CleanupState == "deleted" {
		p.Username = r.CleanupName
	} else {
		u, err := s.db.WebsiteUser(ctx, id)
		if err != nil {
			return p, err
		}
		if u.Role != "user" {
			return p, memberError("不能删除管理员")
		}
		p.Username, version = u.Username, u.TokenVersion
	}
	for _, view := range s.accounts.List() {
		a, err := s.accounts.Get(view.ID)
		if err != nil {
			return p, err
		}
		if a.OwnerUserID == id && a.Ownership == "member" {
			p.Drives = append(p.Drives, view)
		}
	}
	sort.Slice(p.Drives, func(i, j int) bool { return p.Drives[i].ID < p.Drives[j].ID })
	if r.EmbyID == "" && (r.Stage == "creating" || r.Stage == "provisioning") {
		p.BlockedReason = "Emby 注册结果尚未确认，请先在播放管理中核查原注册，避免遗留远端账号"
	}
	if p.Mode == "" {
		p.Mode = "none"
		if r.EmbyID != "" {
			p.Mode = "delete"
			if r.Adopted {
				p.Mode = "keep"
			}
		}
	}
	raw, err := json.Marshal(struct {
		Preview WebsiteCleanupPreview
		Record  model.MemberPlayback
		Version int64
	}{p, r, version})
	if err != nil {
		return p, err
	}
	sum := sha256.Sum256(raw)
	p.Fingerprint = hex.EncodeToString(sum[:])
	return p, nil
}

func (s *MemberPlaybackService) CleanupPreview(ctx context.Context, id string) (WebsiteCleanupPreview, error) {
	if err := s.lock(ctx); err != nil {
		return WebsiteCleanupPreview{}, err
	}
	defer s.unlock()
	return s.cleanupPreview(ctx, id)
}

func (s *MemberPlaybackService) CleanupHistory(ctx context.Context) ([]WebsiteCleanupPreview, error) {
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.unlock()
	out := make([]WebsiteCleanupPreview, 0)
	for _, r := range s.Records() {
		if r.CleanupState != "deleted" {
			continue
		}
		p, err := s.cleanupPreview(ctx, r.UserID)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

func (s *MemberPlaybackService) CleanupWebsiteUser(ctx context.Context, id, fingerprint, name, mode string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	p, err := s.cleanupPreview(ctx, id)
	if err != nil {
		return err
	}
	if p.State == "deleted" {
		return nil
	}
	if fingerprint == "" || fingerprint != p.Fingerprint || name != p.Username {
		return memberError("用户关联已变化，请重新预览并确认用户名")
	}
	if p.BlockedReason != "" {
		return memberError(p.BlockedReason)
	}
	if p.EmbyID == "" && mode != "none" || p.EmbyID != "" && mode != "keep" && mode != "delete" {
		return memberError("请选择有效的 Emby 清理方式")
	}
	r := s.record(id)
	if r.CleanupState != "" && r.CleanupMode != mode {
		return memberError("删除已开始，不能更改原清理方式")
	}
	if r.CleanupState == "" {
		r.UserID, r.CleanupName, r.CleanupState, r.CleanupMode = id, p.Username, "deleting", mode
		r.AdminBlocked, r.Suspended, r.PolicyEnabled, r.LoginEnabled = true, true, false, false
		r.AllowPersonalSwitch = false
		r.Stage, r.Message = "paused", "用户删除待完成，请在网站用户管理重试"
		if err := s.db.BeginWebsiteCleanup(ctx, r); err != nil {
			return err
		}
		s.mu.Lock()
		s.records[id] = r
		s.mu.Unlock()
	}
	delete(s.sessions, id)
	if err := s.accounts.clearMemberCredentials(id); err != nil {
		return memberError("个人网盘凭据清理未完成，请重试")
	}
	if s.ClearDirectLinks != nil {
		s.ClearDirectLinks()
	}
	if r.EmbyID != "" {
		if err := s.cleanupEmby(ctx, r); err != nil {
			return err
		}
		if err := s.removeCleanupBinding(r); err != nil {
			return err
		}
	}
	r.CleanupState = "deleted"
	r.Message = "网站用户已删除，原身份保持禁用；网盘文件未删除"
	if err := s.db.FinishWebsiteCleanup(ctx, r); err != nil {
		return err
	}
	s.mu.Lock()
	s.records[id] = r
	s.mu.Unlock()
	return nil
}

func (s *MemberPlaybackService) removeCleanupBinding(r model.MemberPlayback) error {
	if s.Cache == nil {
		return nil
	}
	s.Cache.mu.Lock()
	defer s.Cache.mu.Unlock()
	doc := s.Cache.doc
	if doc.Config.EmbyURL != r.EmbyURL {
		return nil
	}
	bindings := make([]PlaybackBinding, 0, len(doc.Config.Bindings))
	for _, b := range doc.Config.Bindings {
		if b.UserID != r.EmbyID {
			bindings = append(bindings, b)
		}
	}
	if len(bindings) == len(doc.Config.Bindings) {
		return nil
	}
	doc.Config.Bindings = bindings
	return s.Cache.saveLocked(doc)
}

type cleanupEmbyClient interface {
	Info(context.Context) (emby.Server, error)
	DeleteManagedUser(context.Context, string) error
}

func (s *MemberPlaybackService) cleanupEmby(ctx context.Context, r model.MemberPlayback) error {
	c, client, err := s.config()
	if err != nil {
		return memberError("Emby 暂不可用，删除待完成，请重试")
	}
	if c.URL != r.EmbyURL || c.ServerID != r.ServerID {
		return memberError("Emby 服务已变化，保留删除记录；请恢复原服务后重试")
	}
	remote, ok := client.(cleanupEmbyClient)
	if !ok {
		return memberError("Emby 服务不支持用户清理")
	}
	info, err := remote.Info(ctx)
	if err != nil || info.ID == "" || info.ID != r.ServerID {
		return memberError("无法核实原 Emby 服务身份，删除待完成")
	}
	users, err := client.Users(ctx)
	if err != nil {
		return memberError("Emby 用户列表读取失败，删除待完成")
	}
	exists := false
	for _, u := range users {
		if u.ID == r.EmbyID {
			exists = true
			break
		}
	}
	if !exists {
		return nil
	}
	u, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || u.ID != r.EmbyID {
		return memberError("原 Emby 用户核验失败，删除待完成")
	}
	if u.Policy["IsAdministrator"] != false {
		return memberError("原 Emby 用户是管理员或权限不明确，拒绝清理")
	}
	// Disable first, also for DELETE: failed/ambiguous responses remain locked.
	policy := memberPolicy(u.Policy, false, model.MemberPlaybackSettings{}, false)
	if err := client.SetManagedPolicy(ctx, r.EmbyID, policy); err != nil {
		return memberError("Emby 停用未确认，删除待完成")
	}
	verified, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || !memberPolicyMatches(verified.Policy, false, model.MemberPlaybackSettings{}, false) {
		return memberError("Emby 停用核验失败，删除待完成")
	}
	if r.CleanupMode == "keep" {
		return nil
	}
	// A lost response is resolved by reading the original ID, never by name.
	deleteErr := remote.DeleteManagedUser(ctx, r.EmbyID)
	users, err = client.Users(ctx)
	if err != nil {
		return memberError("Emby 删除结果未确认，请重试")
	}
	for _, user := range users {
		if user.ID == r.EmbyID {
			if deleteErr != nil {
				return memberError("Emby 删除请求失败，原用户仍存在，请重试")
			}
			return memberError("Emby 用户仍存在，删除待完成，请重试")
		}
	}
	// Absence in a successful authenticated full list confirms deletion even
	// when the preceding DELETE response was lost.
	return nil
}
