package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

type AdoptionPreview struct {
	EmbyID           string   `json:"embyId"`
	EmbyName         string   `json:"embyName"`
	EmbyURL          string   `json:"embyUrl"`
	ServerID         string   `json:"serverId"`
	Fingerprint      string   `json:"fingerprint"`
	Source           string   `json:"source"`
	AccountID        string   `json:"accountId"`
	AccountName      string   `json:"accountName"`
	CurrentLibraries []string `json:"currentLibraries"`
	NewLibraries     []string `json:"newLibraries"`
	CurrentDownload  bool     `json:"currentDownload"`
	NewDownload      bool     `json:"newDownload"`
}

func (s *MemberPlaybackService) independentBinding(id, url string) (string, error) {
	if s.Cache == nil {
		return "", memberError("播放绑定服务未就绪")
	}
	s.Cache.mu.Lock()
	defer s.Cache.mu.Unlock()
	return adoptionBinding(s.Cache.doc.Config, id, url)
}
func adoptionBinding(c PlaybackCacheConfig, id, url string) (string, error) {
	for _, b := range c.Bindings {
		if b.UserID == id {
			if c.EmbyURL != url {
				return "", memberError("原播放绑定属于其他 Emby 服务")
			}
			return b.AccountID, nil
		}
	}
	return "", nil
}

// Called under the member operation gate. Preview is recomputed at every claim;
// names never determine ownership and stale policy/binding grants are rejected.
func (s *MemberPlaybackService) adoptionPreview(ctx context.Context, id string) (AdoptionPreview, error) {
	var p AdoptionPreview
	if !instantRunID.MatchString(id) || !s.Settings().Enabled {
		return p, memberError("请先开放自助播放并选择有效的 Emby 用户")
	}
	c, client, err := s.config()
	if err != nil {
		return p, err
	}
	for _, r := range s.Records() {
		if r.EmbyID == id {
			return p, memberError("该 Emby 用户已有关联，不能重复接管")
		}
	}
	u, err := client.ManagedUser(ctx, id)
	if err != nil || u.ID != id {
		return p, memberError("无法核验原 Emby 用户")
	}
	if admin, ok := u.Policy["IsAdministrator"].(bool); !ok || admin || u.Policy["IsDisabled"] != false || u.Policy["EnableMediaPlayback"] != true {
		return p, memberError("仅支持接管已启用播放的普通 Emby 用户，管理员或停用账号不可接管")
	}
	settings := s.Settings()
	if _, err := memberPolicySettings(ctx, client, settings); err != nil {
		return p, err
	}
	accountID, err := s.independentBinding(id, c.URL)
	if err != nil {
		return p, err
	}
	p = AdoptionPreview{EmbyID: id, EmbyName: u.Name, EmbyURL: c.URL, ServerID: c.ServerID, Source: "primary", AccountID: accountID, CurrentLibraries: []string{}, NewLibraries: []string{}, CurrentDownload: u.Policy["EnableContentDownloading"] == true, NewDownload: settings.AllowDownload}
	if accountID != "" {
		p.Source = "assigned"
		if s.accounts.IsPersonal(accountID) {
			return p, memberError("个人网盘不能转交其他用户")
		}
	}
	var account P115Account
	if accountID != "" {
		account, _ = s.accounts.Get(accountID)
	} else {
		account, _ = s.accounts.Primary("")
	}
	p.AccountName = account.Name
	if p.AccountName == "" {
		p.AccountName = "原账号不可用，关联后仍保留授权"
	}
	libraries, err := client.Libraries(ctx)
	if err != nil {
		return p, memberError("无法读取媒体库权限")
	}
	folders := map[string]bool{}
	switch ids := u.Policy["EnabledFolders"].(type) {
	case []string:
		for _, id := range ids {
			folders[id] = true
		}
	case []any:
		for _, id := range ids {
			if text, ok := id.(string); ok {
				folders[text] = true
			}
		}
	}
	for _, library := range libraries {
		if u.Policy["EnableAllFolders"] == true || folders[library.GUID] {
			p.CurrentLibraries = append(p.CurrentLibraries, library.Name)
		}
		for _, id := range settings.LibraryIDs {
			if id == library.ID {
				p.NewLibraries = append(p.NewLibraries, library.Name)
			}
		}
	}
	raw, err := json.Marshal(struct {
		Preview        AdoptionPreview
		Policy         map[string]any
		Settings       model.MemberPlaybackSettings
		AccountID, UID string
	}{p, u.Policy, settings, account.ID, account.UID})
	if err != nil {
		return p, fmt.Errorf("生成关联预览: %w", err)
	}
	hash := sha256.Sum256(raw)
	p.Fingerprint = hex.EncodeToString(hash[:])
	return p, nil
}

func (s *MemberPlaybackService) AdoptionPreview(ctx context.Context, id string) (AdoptionPreview, error) {
	if err := s.lock(ctx); err != nil {
		return AdoptionPreview{}, err
	}
	defer s.unlock()
	return s.adoptionPreview(ctx, id)
}

func (s *MemberPlaybackService) CreateAdoptionInvite(ctx context.Context, id, fingerprint, creator string) (GeneratedWebsiteInvite, error) {
	if err := s.lock(ctx); err != nil {
		return GeneratedWebsiteInvite{}, err
	}
	defer s.unlock()
	p, err := s.adoptionPreview(ctx, id)
	if err != nil {
		return GeneratedWebsiteInvite{}, err
	}
	if fingerprint == "" || p.Fingerprint != fingerprint {
		return GeneratedWebsiteInvite{}, memberError("用户权限或播放来源已变化，请重新预览")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return GeneratedWebsiteInvite{}, err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return GeneratedWebsiteInvite{}, err
	}
	code := hex.EncodeToString(nonce[:16])
	hash, err := inviteHash(code)
	if err != nil {
		return GeneratedWebsiteInvite{}, err
	}
	now := time.Now()
	i := model.WebsiteInvite{ID: hex.EncodeToString(nonce[16:]), CodeHash: hash, CreatedBy: creator, CreatedAt: now.Unix(), ExpiresAt: now.Add(24 * time.Hour).Unix(), PlaybackSource: p.Source, EmbyTarget: string(raw), EmbyName: p.EmbyName, Note: "关联已有 Emby 用户；仅开放个人网盘管理"}
	if err := s.db.CreateWebsiteInvites(ctx, []model.WebsiteInvite{i}); err != nil {
		return GeneratedWebsiteInvite{}, err
	}
	return GeneratedWebsiteInvite{WebsiteInvite: i, Code: code}, nil
}

func (s *MemberPlaybackService) adoptionRecord(userID string, p AdoptionPreview) model.MemberPlayback {
	return model.MemberPlayback{UserID: userID, EmbyID: p.EmbyID, EmbyName: p.EmbyName, EmbyURL: p.EmbyURL, ServerID: p.ServerID, Source: p.Source, AssignedAccountID: p.AccountID, Adopted: true, AllowPersonalSwitch: true, Provisioned: true, Stage: "pending", AccountRevision: 1, Message: "已关联原 Emby 账号，正在同步管理员确认的权限"}
}

func (s *MemberPlaybackService) persistAdoption(ctx context.Context, p AdoptionPreview, r model.MemberPlayback, save func() error) error {
	// Serialize with generic binding writes through the durable association.
	s.Cache.mu.Lock()
	defer s.Cache.mu.Unlock()
	current, err := adoptionBinding(s.Cache.doc.Config, p.EmbyID, p.EmbyURL)
	if err != nil {
		return err
	}
	if current != p.AccountID {
		return memberError("播放绑定已变化，请重新预览")
	}
	if err := save(); err != nil {
		return err
	}
	s.mu.Lock()
	s.records[r.UserID] = r
	s.mu.Unlock()
	s.RequestSync()
	return nil
}

func (s *MemberPlaybackService) LinkExistingUser(ctx context.Context, embyID, websiteID, fingerprint string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if _, err := s.websiteUser(ctx, websiteID); err != nil {
		return err
	}
	old := s.record(websiteID)
	if old.EmbyID != "" || old.AccountID != "" || old.UserID != "" && old.Stage != "unbound" {
		return memberError("网站用户已有账号或开通记录，不能覆盖")
	}
	p, err := s.adoptionPreview(ctx, embyID)
	if err != nil {
		return err
	}
	if fingerprint == "" || p.Fingerprint != fingerprint {
		return memberError("预览已过期，请重新检查")
	}
	r := s.adoptionRecord(websiteID, p)
	return s.persistAdoption(ctx, p, r, func() error { return s.db.LinkExistingMember(ctx, r) })
}

func (s *MemberPlaybackService) registerAdopted(ctx context.Context, hash string, user model.WebsiteUser, target string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	var expected AdoptionPreview
	if json.Unmarshal([]byte(target), &expected) != nil {
		return memberError("关联邀请内容无效")
	}
	p, err := s.adoptionPreview(ctx, expected.EmbyID)
	if err != nil {
		return err
	}
	if p.Fingerprint != expected.Fingerprint {
		return memberError("原 Emby 用户、权限或播放来源已变化，请管理员重新生成邀请")
	}
	r := s.adoptionRecord(user.ID, p)
	return s.persistAdoption(ctx, p, r, func() error { return s.db.RegisterWebsiteUser(ctx, hash, user, r) })
}
