package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"time"
)

type ChildCreateInput struct {
	RequestID string `json:"requestId"`
	Name      string `json:"name"`
	Password  string `json:"password"`
}
type ChildActionInput struct {
	Action   string `json:"action"`
	Revision uint64 `json:"revision"`
	Password string `json:"password"`
}
type ChildView struct {
	RequestID string `json:"requestId"`
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	Completed bool   `json:"completed"`
	Paused    bool   `json:"paused"`
	Disabled  bool   `json:"disabled"`
	Revoked   bool   `json:"revoked"`
	Blocked   bool   `json:"blocked"`
	Revision  uint64 `json:"revision"`
	Stage     string `json:"stage"`
	Message   string `json:"message"`
}
type ChildrenView struct {
	Limit int         `json:"limit"`
	Used  int         `json:"used"`
	Items []ChildView `json:"items"`
}

func (s *MemberPlaybackService) childOwner(ctx context.Context, id string) (model.MemberPlayback, error) {
	r, err := s.ensure(ctx, id)
	if err != nil {
		return r, err
	}
	if r.PlaybackOnly || r.Administrator {
		return r, memberError("当前不能管理播放子账号")
	}
	return r, nil
}

func (s *MemberPlaybackService) Children(ctx context.Context, owner string) (ChildrenView, error) {
	if _, err := s.portalUser(ctx, owner); err != nil {
		return ChildrenView{}, err
	}
	r := s.record(owner)
	jobs, err := s.db.ChildCreations(ctx, owner)
	if err != nil {
		return ChildrenView{}, err
	}
	out := ChildrenView{Limit: r.ChildLimit, Items: make([]ChildView, 0, len(jobs))}
	for _, j := range jobs {
		child := s.record(embyProfileID(j.ServerID, j.EmbyID))
		v := ChildView{RequestID: j.ID, Name: j.Name, Completed: j.Completed, Stage: "creating", Message: "创建未完成，请使用原任务重试"}
		if child.PlaybackOnly && child.OwnerUserID == owner {
			v.UserID = child.UserID
			v.Paused = child.ChildPaused
			v.Disabled = child.ChildDisabled
			v.Revoked = child.ChildRevoked
			v.Blocked = child.Administrator
			v.Revision = child.ChildRevision
			v.Stage = child.Stage
			v.Message = child.Message
			if child.CheckError != "" {
				v.Message = child.CheckError
			}
		}
		if !v.Revoked {
			out.Used++
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}

func (s *MemberPlaybackService) SetChildLimit(ctx context.Context, id string, limit int) error {
	if limit < 0 || limit > 20 {
		return memberError("子账号配额需为 0–20，不包含本人")
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	r := s.record(id)
	if r.UserID == "" || r.PlaybackOnly || r.Administrator {
		return memberError("只能给普通主用户配置子账号名额")
	}
	u, err := s.playbackUser(ctx, r)
	if err != nil {
		return err
	}
	if u.Policy["IsAdministrator"] == true {
		return memberError("管理员不能作为子账号所有者")
	}
	r.ChildLimit = limit
	return s.save(ctx, r)
}

func (s *MemberPlaybackService) CreateChild(ctx context.Context, owner string, input ChildCreateInput) error {
	name := strings.TrimSpace(input.Name)
	if len(input.RequestID) < 16 || len(input.RequestID) > 64 || strings.Trim(input.RequestID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" || !memberUsername.MatchString(name) || strings.HasPrefix(strings.ToLower(name), "c115_") || len(input.Password) < 8 || len(input.Password) > 128 {
		return memberError("创建参数无效：用户名需 3–32 位，密码需 8–128 字节")
	}
	if err := s.lockCreation(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if s.deletionBlocked("child:"+input.RequestID) || s.deletionBlocked(owner) {
		return memberError("用户或创建任务正在删除或已删除")
	}
	parent, err := s.childOwner(ctx, owner)
	if err != nil {
		return err
	}
	// Creating children never grants access to the parent's administrative playback source.
	if _, err := s.sharedPersonalAccount(parent); err != nil {
		return err
	}
	c, client, err := s.config()
	if err != nil {
		return err
	}
	jobs, err := s.db.ChildCreations(ctx, owner)
	if err != nil {
		return err
	}
	var existing model.ChildCreation
	for _, job := range jobs {
		if job.ID == input.RequestID {
			existing = job
		}
	}
	settings := s.Settings()
	if !existing.Completed && existing.ActivationPolicy == nil {
		settings, err = memberPolicySettings(ctx, client, settings)
		if err != nil {
			return err
		}
		if len(settings.LibraryIDs) == 0 {
			return memberError("尚未配置新账号默认媒体库，请联系管理员")
		}
	}
	users, err := client.Users(ctx)
	if err != nil {
		return memberError("无法读取 Emby 用户，请使用原任务重试")
	}
	for _, u := range users {
		if strings.EqualFold(u.Name, name) && u.ID != existing.EmbyID {
			return memberError("Emby 用户名已存在，不能认领现有账号")
		}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	job, err := s.db.ReserveChild(ctx, model.ChildCreation{ID: input.RequestID, OwnerID: owner, Name: name, ReservedName: "c115_" + hex.EncodeToString(nonce[:]), ServerID: c.ServerID, EmbyURL: c.URL})
	if errors.Is(err, storage.ErrChildQuota) {
		return memberError("播放子账号名额已用完；创建中和停用账号也占名额")
	}
	if err != nil {
		return err
	}
	if job.Completed {
		return nil
	}
	if job.EmbyID == "" {
		if !job.Started {
			job.Started = true
			if err := s.db.SaveChildCreation(ctx, job); err != nil {
				return err
			}
			u, err := client.CreateManagedUser(ctx, job.ReservedName)
			if err == nil {
				job.EmbyID = u.ID
			}
		}
		if job.EmbyID == "" {
			job.EmbyID = confirmReservedUser(ctx, client, job.ReservedName)
		}
		if job.EmbyID == "" {
			return memberError("Emby 创建结果待确认；名额保留，不会重复创建，请使用原任务重试或联系管理员核查")
		}
		if err := s.db.SaveChildCreation(ctx, job); err != nil {
			return err
		}
	}
	remote, err := client.ManagedUser(ctx, job.EmbyID)
	if err != nil || remote.ID != job.EmbyID || remote.Policy["IsAdministrator"] == true || (remote.Name != job.ReservedName && remote.Name != name) {
		return memberError("创建中的 Emby 身份已变化，请联系管理员")
	}
	if job.ActivationPolicy != nil && remote.Name != name {
		return memberError("创建中的用户名已变化，请联系管理员")
	}
	ownerRemote, err := client.ManagedUser(ctx, parent.EmbyID)
	if err != nil || ownerRemote.ID != parent.EmbyID || ownerRemote.Policy["IsAdministrator"] == true || ownerRemote.Policy["IsDisabled"] == true {
		return memberError("所有者身份不可用")
	}
	profile := model.PortalUser{ID: embyProfileID(c.ServerID, job.EmbyID), ServerID: c.ServerID, EmbyID: job.EmbyID, Username: name, Role: "user", TokenVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	r := s.record(profile.ID)
	if r.UserID != "" && (!r.PlaybackOnly || r.OwnerUserID != owner || r.ChildRevoked) {
		return memberError("子账号归属或状态已变化")
	}
	if r.UserID == "" {
		r = model.MemberPlayback{UserID: profile.ID, ServerID: c.ServerID, EmbyURL: c.URL, EmbyID: job.EmbyID, EmbyName: name, CreatedByCinema: true, PlaybackOnly: true, OwnerUserID: owner, Source: "personal_shared", ChildRevision: 1, Stage: "creating"}
	}
	// Install the permanent playback-only identity before the reserved name is removed.
	if err := s.db.CommitChild(ctx, job, profile, r); err != nil {
		return err
	}
	s.publishChild(r)
	if job.ActivationPolicy == nil {
		policy := memberPolicy(nil, false, settings, false)
		// Copy only the owner's rating restrictions at creation. Later edits belong to administrators.
		for _, key := range []string{"MaxParentalRating", "BlockUnratedItems", "BlockedTags", "AllowedTags"} {
			if v, ok := ownerRemote.Policy[key]; ok {
				policy[key] = v
			}
		}
		if !saveProvisioningPolicy(ctx, client, r.EmbyID, policy) {
			return memberError("子账号禁播策略未确认，请重试")
		}
		if err := client.SetManagedPassword(ctx, r.EmbyID, input.Password); err != nil {
			return memberError("密码设置结果未确认，请重新输入并重试")
		}
		if remote.Name != name {
			if !renameProvisionedUser(ctx, client, r.EmbyID, name) {
				return memberError("用户名设置结果未确认，请使用原任务重试")
			}
		}
		policy = playbackRestrictions(policy)
		policy["EnableMediaPlayback"] = true
		policy["IsDisabled"] = false
		job.ActivationPolicy = policy
		if err := s.db.SaveChildCreation(ctx, job); err != nil {
			return err
		}
	}
	if !saveProvisioningPolicy(ctx, client, r.EmbyID, job.ActivationPolicy) {
		return memberError("子账号已创建，登录权限尚未确认，请使用原任务继续创建")
	}
	job.Completed = true
	job.ActivationPolicy = nil
	r.Provisioned = true
	r.ChildManagedDisabled = false
	r.Stage, r.Message = "ready", "子账号已创建，播放时实时检查个人网盘"
	if err := s.db.CommitChild(ctx, job, profile, r); err != nil {
		disableUncommittedUser(ctx, client, r.EmbyID, remote.Policy)
		return err
	}
	s.publishChild(r)
	s.requestHealthCheck()
	return nil
}

func childRatingMatches(actual, expected map[string]any) bool {
	for _, key := range []string{"MaxParentalRating", "BlockUnratedItems", "BlockedTags", "AllowedTags"} {
		value, ok := expected[key]
		if !ok {
			continue
		}
		want, err := json.Marshal(value)
		if err != nil {
			return false
		}
		got, err := json.Marshal(actual[key])
		if err != nil || !bytes.Equal(want, got) {
			return false
		}
	}
	return true
}

// Local revocation applies to existing Emby sessions as well as new logins.
func (s *MemberPlaybackService) ChildSessionDenied(server, id string) bool {
	if id == "" {
		return false
	}
	serverID := s.emby.snapshot().ServerID
	if s.deletionBlocked(embyProfileID(serverID, id)) {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if r, ok := s.records[embyProfileID(serverID, id)]; ok {
		return r.PlaybackOnly && (r.EmbyURL != server || !r.Provisioned || r.ChildDisabled || r.ChildRevoked || r.Administrator)
	}
	for _, r := range s.records {
		if r.EmbyID == id && r.PlaybackOnly {
			return r.EmbyURL != server || !r.Provisioned || r.ChildDisabled || r.ChildRevoked || r.Administrator
		}
	}
	return false
}

func (s *MemberPlaybackService) publishChild(r model.MemberPlayback) {
	s.mu.Lock()
	s.records[r.UserID] = r
	s.mu.Unlock()
}

func (s *MemberPlaybackService) ChildAction(ctx context.Context, owner, id string, input ChildActionInput) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if _, err := s.childOwner(ctx, owner); err != nil {
		return err
	}
	r := s.record(id)
	if s.deletionBlocked(id) {
		return memberError("用户正在删除或已删除")
	}
	if !r.PlaybackOnly || r.OwnerUserID != owner || r.ChildRevision != input.Revision || r.ChildRevoked || !r.Provisioned {
		return memberError("子账号归属或状态已变化，请刷新")
	}
	c, client, err := s.config()
	if err != nil {
		return err
	}
	if r.ServerID != c.ServerID || r.EmbyURL != c.URL {
		return memberError("子账号 Emby 服务已变化")
	}
	u, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil || u.ID != r.EmbyID {
		return memberError("无法核验子账号身份")
	}
	if u.Policy["IsAdministrator"] == true || r.Administrator {
		return memberError("该账号已变为管理员，停止托管，请联系管理员")
	}
	switch input.Action {
	case "pause":
		r.ChildPaused = true
	case "resume":
		r.ChildPaused = false
	case "disable":
		r.ChildDisabled = true
	case "enable":
		r.ChildDisabled = false
	case "revoke":
		r.ChildRevoked = true
		r.ChildDisabled = true
	case "password":
		if len(input.Password) < 8 || len(input.Password) > 128 {
			return memberError("密码需 8–128 字节，且账号不能被管理员停用")
		}
		if err := client.SetManagedPassword(ctx, r.EmbyID, input.Password); err != nil {
			return memberError("密码设置结果未确认，请重新输入后重试")
		}
	default:
		return memberError("子账号操作无效")
	}
	r.ChildRevision++
	if err := s.save(ctx, r); err != nil {
		return err
	}
	if s.ClearDirectLinks != nil {
		s.ClearDirectLinks()
	}
	s.RequestSync()
	return s.reconcile(ctx, r)
}

func (s *MemberPlaybackService) sharedPersonalAccount(owner model.MemberPlayback) (P115Account, error) {
	if s.deletionBlocked(owner.UserID) {
		return P115Account{}, memberError("所有者正在删除或已删除")
	}
	if owner.UserID == "" || owner.PlaybackOnly || owner.Administrator || owner.Suspended || !owner.Provisioned || owner.ServerID != s.Settings().ServerID || owner.EmbyURL != s.Settings().EmbyURL {
		return P115Account{}, memberError("所有者共享已暂停")
	}
	a, err := s.accounts.Get(owner.AccountID)
	if err != nil || a.Primary || a.Ownership != "member" || a.OwnerUserID != owner.UserID || a.Cookie == "" || a.UID == "" || accountUID(a.Cookie) != a.UID || owner.DirectoryID == "" || a.PlaybackParentCID != owner.DirectoryID {
		return P115Account{}, memberError("所有者个人网盘未绑定或已失效，不回退其他网盘")
	}
	return a, nil
}

func (s *MemberPlaybackService) authorizeChildOwner(ctx context.Context, r model.MemberPlayback) error {
	owner := s.record(r.OwnerUserID)
	if owner.ServerID != r.ServerID || owner.EmbyURL != r.EmbyURL {
		return memberError("共享服务已变化")
	}
	if _, err := s.sharedPersonalAccount(owner); err != nil {
		return err
	}
	u, err := s.playbackUser(ctx, owner)
	if err != nil {
		return err
	}
	if u.Policy["IsAdministrator"] == true {
		return memberError("所有者身份已变化，共享暂停")
	}
	return nil
}
