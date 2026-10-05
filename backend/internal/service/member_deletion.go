package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
	"sync"
	"time"
)

type MemberDeleteInput struct {
	Key             string `json:"key"`
	Fingerprint     string `json:"fingerprint"`
	ConfirmName     string `json:"confirmName"`
	IncludeChildren bool   `json:"includeChildren"`
}
type MemberDeletePreview struct {
	EmbyStates     map[string]MemberEmbyState   `json:"embyStates"`
	Key            string                       `json:"key"`
	Name           string                       `json:"name"`
	Fingerprint    string                       `json:"fingerprint"`
	Targets        []model.MemberDeletionTarget `json:"targets"`
	Children       int                          `json:"children"`
	PersonalDrives int                          `json:"personalDrives"`
	Sessions       int                          `json:"sessions"`
}

func (s *MemberPlaybackService) deletionBlocked(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deleting[id]
}

// Admission and deletion fencing share one mutex. Existing work drains before
// credentials or capacity are removed; no new work starts after the fence.
func (s *MemberPlaybackService) useMember(id string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleting[id] {
		return nil, memberError("用户正在删除或已删除")
	}
	if s.memberUses == nil {
		s.memberUses = make(map[string]int)
	}
	s.memberUses[id]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.memberUses[id]--
			if s.memberUses[id] == 0 {
				delete(s.memberUses, id)
			}
		})
	}, nil
}

func (s *MemberPlaybackService) installDeletion(job model.MemberDeletion) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleting == nil {
		s.deleting = make(map[string]bool)
	}
	for _, t := range job.Targets {
		s.deleting[t.Key] = true
		if t.UserID != "" {
			s.deleting[t.UserID] = true
		}
		if t.ChildID != "" {
			s.deleting["child:"+t.ChildID] = true
		}
		if t.InviteID != "" {
			s.deleting["invite:"+t.InviteID] = true
		}
	}
}

func (s *MemberPlaybackService) DeletionDirectory(ctx context.Context) ([]model.MemberDeletionTarget, []model.MemberDeletion, error) {
	pending, err := s.db.IncompleteMembers(ctx)
	if err != nil {
		return nil, nil, err
	}
	jobs, err := s.db.MemberDeletions(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]model.MemberDeletionTarget, 0, len(pending))
	for _, t := range pending {
		if !s.deletionBlocked(t.Key) {
			out = append(out, t)
		}
	}
	return out, jobs, nil
}

func (s *MemberPlaybackService) deletionTargets(ctx context.Context) (map[string]model.MemberDeletionTarget, error) {
	out := make(map[string]model.MemberDeletionTarget)
	for _, r := range s.Records() {
		out[r.UserID] = model.MemberDeletionTarget{Key: r.UserID, UserID: r.UserID, ServerID: r.ServerID, EmbyURL: r.EmbyURL, EmbyID: r.EmbyID, Name: r.EmbyName, OwnerID: r.OwnerUserID, Started: true}
	}
	pending, err := s.db.IncompleteMembers(ctx, true)
	if err != nil {
		return nil, err
	}
	for _, t := range pending {
		if t.EmbyID != "" {
			t.UserID = embyProfileID(t.ServerID, t.EmbyID)
			if old, ok := out[t.UserID]; ok {
				t.UserID = old.UserID
				t.Key = old.Key
				if s.record(old.UserID).Provisioned {
					t.Name = old.Name
					t.ReservedName = ""
				}
			}
		}
		out[t.Key] = t
	}
	return out, nil
}

func (s *MemberPlaybackService) previewDeletion(ctx context.Context, key string) (MemberDeletePreview, error) {
	var out MemberDeletePreview
	targets, err := s.deletionTargets(ctx)
	if err != nil {
		return out, err
	}
	t, ok := targets[key]
	if !ok {
		for _, v := range targets {
			if "invite:"+v.InviteID == key || "child:"+v.ChildID == key {
				t, ok = v, true
				break
			}
		}
	}
	if !ok || s.deletionBlocked(t.Key) {
		return out, memberError("用户不存在或已有删除任务，请刷新任务列表")
	}
	c := s.emby.snapshot()
	if c.URL == "" || c.ServerID == "" || c.APIKey == "" {
		return out, memberError("请先配置并验证 Emby")
	}
	client := s.NewEmby(c.URL, c.APIKey)
	users, err := client.Users(ctx)
	if err != nil {
		return out, memberError("无法核验 Emby 用户列表，未开始删除")
	}
	selected := []model.MemberDeletionTarget{t}
	for _, v := range targets {
		if t.UserID != "" && v.OwnerID == t.UserID {
			selected = append(selected, v)
		}
	}
	for i := range selected {
		v := &selected[i]
		if v.ServerID != c.ServerID || v.EmbyURL != c.URL {
			return out, memberError("用户属于其他 Emby 服务，不能在当前服务删除")
		}
		if v.EmbyID == "" && v.Started {
			for _, u := range users {
				if u.Name == v.ReservedName {
					if v.EmbyID != "" {
						return out, memberError("创建身份有歧义，请管理员核查")
					}
					v.EmbyID = u.ID
				}
			}
			if v.EmbyID == "" {
				return out, memberError("外部创建结果仍未确认，保留任务和名额；请确认 Emby 中的保留账号后重新核查")
			}
			v.UserID = embyProfileID(v.ServerID, v.EmbyID)
		}
		for _, u := range users {
			if u.ID == v.EmbyID {
				remote, err := client.ManagedUser(ctx, u.ID)
				if err != nil || remote.ID != v.EmbyID {
					return out, memberError("无法核验删除目标身份")
				}
				if remote.Policy["IsAdministrator"] != false || s.record(v.UserID).Administrator {
					return out, memberError("禁止删除 Emby 管理员；请先在 Emby 中处理管理员身份")
				}
				if v.ReservedName != "" && remote.Name != v.Name && remote.Name != v.ReservedName {
					return out, memberError("创建中的用户身份已变化，停止删除")
				}
				if v.ReservedName == "" {
					v.Name = remote.Name
				}
			}
		}
		if v.UserID != "" && s.deletionBlocked(v.UserID) {
			return out, memberError("目标或所属子账号已有删除任务，请先完成原任务")
		}
		if s.record(v.UserID).Administrator {
			return out, memberError("禁止删除管理员身份")
		}
	}
	// Children first, owner last. The frozen set cannot grow during deletion.
	if len(selected) > 1 {
		sort.Slice(selected[1:], func(i, j int) bool { return selected[i+1].Key < selected[j+1].Key })
		selected = append(selected[1:], selected[0])
	}
	out = MemberDeletePreview{Key: key, Name: selected[len(selected)-1].Name, Targets: selected, Children: len(selected) - 1}
	verifyCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	byID := make(map[string]emby.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	out.EmbyStates = make(map[string]MemberEmbyState, len(selected))
	for _, v := range selected {
		out.EmbyStates[v.Key] = missingMemberEmbyState(verifyCtx, client, v.EmbyID, byID)
		for _, a := range s.accounts.List() {
			if a.Ownership == "member" && a.OwnerUserID == v.UserID {
				out.PersonalDrives++
			}
		}
		for _, l := range s.UserPlayback.List() {
			if l.UserID == v.UserID {
				out.Sessions++
			}
		}
	}
	raw, err := json.Marshal(selected)
	if err != nil {
		return out, err
	}
	digest := sha256.Sum256(raw)
	out.Fingerprint = hex.EncodeToString(digest[:])
	return out, nil
}

func (s *MemberPlaybackService) PreviewMemberDeletion(ctx context.Context, key string) (MemberDeletePreview, error) {
	if err := s.lockCreation(ctx); err != nil {
		return MemberDeletePreview{}, err
	}
	defer s.unlock()
	return s.previewDeletion(ctx, key)
}

func (s *MemberPlaybackService) DeleteMember(ctx context.Context, in MemberDeleteInput, actor string) (model.MemberDeletion, error) {
	var job model.MemberDeletion
	if err := s.lockCreation(ctx); err != nil {
		return job, err
	}
	defer s.unlock()
	jobs, err := s.db.MemberDeletions(ctx)
	if err != nil {
		return job, err
	}
	for _, v := range jobs {
		if v.ID == in.Key {
			job = v
			break
		}
	}
	if job.ID == "" {
		preview, err := s.previewDeletion(ctx, in.Key)
		if err != nil {
			return job, err
		}
		if preview.Fingerprint != in.Fingerprint || preview.Name != in.ConfirmName {
			return job, memberError("删除目标已变化或确认名称不一致，请重新预览")
		}
		if preview.Children > 0 && !in.IncludeChildren {
			return job, memberError("此用户还有子账号，请明确选择连同子账号一起删除")
		}
		job = model.MemberDeletion{ID: in.Key, Name: preview.Name, Fingerprint: preview.Fingerprint, Targets: preview.Targets, Stage: "deleting", Message: "已封锁新请求，准备删除", Actor: actor, CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()}
		// No request can pass admission between durable intent and memory fence.
		s.mu.Lock()
		err = s.db.SaveMemberDeletion(ctx, job)
		if err == nil {
			if s.deleting == nil {
				s.deleting = make(map[string]bool)
			}
			for _, t := range job.Targets {
				s.deleting[t.Key] = true
				if t.UserID != "" {
					s.deleting[t.UserID] = true
				}
				if t.ChildID != "" {
					s.deleting["child:"+t.ChildID] = true
				}
			}
		}
		s.mu.Unlock()
		if err != nil {
			return model.MemberDeletion{}, err
		}
		s.installDeletion(job)
	} else if in.Fingerprint != job.Fingerprint || in.ConfirmName != job.Name {
		return job, memberError("删除任务确认信息不一致")
	}
	if job.Stage == "completed" {
		return job, nil
	}
	if s.ClearDirectLinks != nil {
		s.ClearDirectLinks()
	}
	err = s.runMemberDeletion(ctx, &job)
	if err != nil {
		job.Stage, job.Message = "pending", "删除进度保存或本地清理失败，请检查存储后继续原任务"
		if errors.Is(err, ErrMemberPlayback) {
			job.Message = err.Error()
		}
		job.UpdatedAt = time.Now().Unix()
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if saveErr := s.db.SaveMemberDeletion(persist, job); saveErr != nil {
			return job, saveErr
		}
		return job, nil // Accepted task; the explicit stage carries pending failure.
	}
	job.Stage, job.Message = "completed", "Emby 用户及本地业务关联已清理"
	job.UpdatedAt = time.Now().Unix()
	if err := s.db.SaveMemberDeletion(ctx, job); err != nil {
		return job, err
	}
	return job, nil
}

func (s *MemberPlaybackService) runMemberDeletion(ctx context.Context, job *model.MemberDeletion) error {
	c := s.emby.snapshot()
	client := s.NewEmby(c.URL, c.APIKey)
	deleter, ok := client.(interface {
		DeleteManagedUser(context.Context, string) error
		ManagedUserAbsent(context.Context, string) (bool, error)
	})
	if !ok {
		return memberError("当前 Emby 客户端不支持删除")
	}
	for _, t := range job.Targets {
		if t.ServerID != c.ServerID || t.EmbyURL != c.URL || c.APIKey == "" {
			return memberError("Emby 服务已变化，停止删除，请恢复原服务后继续")
		}
	}
	for _, t := range job.Targets {
		s.mu.RLock()
		uses := s.memberUses[t.UserID]
		s.mu.RUnlock()
		if uses > 0 {
			return memberError("用户仍有正在执行的取链或秒传请求，新请求已封锁；请等待结束后继续删除")
		}
	}
	for i := range job.Targets {
		t := &job.Targets[i]
		if t.Completed {
			continue
		}
		if !t.RemoteDeleted && t.EmbyID != "" {
			users, err := client.Users(ctx)
			if err != nil {
				return memberError("无法查询 Emby，删除结果待确认")
			}
			found := false
			for _, u := range users {
				if u.ID == t.EmbyID {
					found = true
				}
			}
			if !found {
				absent, err := deleter.ManagedUserAbsent(ctx, t.EmbyID)
				if err != nil || !absent {
					return memberError("列表未发现用户，但原用户 ID 尚未确认不存在，保留本地关联")
				}
			}
			if found {
				u, err := client.ManagedUser(ctx, t.EmbyID)
				if err != nil || u.ID != t.EmbyID || u.Policy["IsAdministrator"] != false {
					return memberError("删除目标无法核验或已成为管理员，停止删除")
				}
				policy := playbackRestrictions(u.Policy)
				policy["IsDisabled"] = true
				if !saveProvisioningPolicy(ctx, client, t.EmbyID, policy) {
					return memberError("Emby 禁用登录尚未确认，请继续核查")
				}
				// Never interpret a timeout or 404 alone as proof of deletion.
				_ = deleter.DeleteManagedUser(ctx, t.EmbyID)
				if !confirmProvisioning(ctx, func(ctx context.Context) bool {
					users, err := client.Users(ctx)
					if err != nil {
						return false
					}
					for _, u := range users {
						if u.ID == t.EmbyID {
							return false
						}
					}
					absent, err := deleter.ManagedUserAbsent(ctx, t.EmbyID)
					return err == nil && absent
				}) {
					return memberError("Emby 删除结果未确认，原任务已保留，请继续核查")
				}
			}
		}
		t.RemoteDeleted = true
		if err := s.db.SaveMemberDeletion(ctx, *job); err != nil {
			return err
		}
		if t.UserID != "" {
			r := s.record(t.UserID)
			if r.UserID != "" {
				if _, err := s.accounts.UnbindMember(ctx, r); err != nil {
					return memberError("Emby 已删除，个人凭据清理未完成，请继续清理")
				}
			}
			delete(s.sessions, t.UserID)
		}
		if s.CleanupDeletedUser != nil {
			if err := s.CleanupDeletedUser(*t); err != nil {
				return memberError("Emby 已删除，任务配置清理未完成，请继续清理")
			}
		}
		if err := s.finishDeletionTarget(ctx, job, i); err != nil {
			return memberError("Emby 已删除，本地事务清理未完成，请继续清理")
		}
	}
	return nil
}

func (s *MemberPlaybackService) finishDeletionTarget(ctx context.Context, job *model.MemberDeletion, index int) error {
	t := job.Targets[index]
	u, p := s.UserPlayback, s.Pool
	u.mu.Lock()
	defer u.mu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	ud, pd := u.clone(), p.clone()
	for k, l := range ud.Leases {
		if l.UserID == t.UserID && t.UserID != "" {
			delete(ud.Leases, k)
			delete(ud.Stopped, l.EventKey)
		}
	}
	for k, l := range pd.Leases {
		if l.UserID == t.UserID && t.UserID != "" {
			delete(pd.Leases, k)
		}
	}
	job.Targets[index].Completed = true
	if err := s.db.FinishMemberDeletion(ctx, *job, t, ud, pd); err != nil {
		job.Targets[index].Completed = false
		return err
	}
	u.doc, p.doc = ud, pd
	delete(p.recent, DirectLinkKey("pool-preference", t.UserID))
	s.mu.Lock()
	delete(s.records, t.UserID)
	s.mu.Unlock()
	return nil
}

// Keep the client interface narrow; other workflows need not implement delete.
var _ interface {
	DeleteManagedUser(context.Context, string) error
} = (*emby.Client)(nil)
