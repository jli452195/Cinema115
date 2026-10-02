package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"strm115/backend/internal/model"
)

// Reserved IDs remain a defensive guard; persisted ownership is authoritative.
func IsMemberAccount(id string) bool { return strings.HasPrefix(id, "member_") }
func (s *P115Accounts) ManagedList() []P115AccountView {
	all := s.List()
	out := make([]P115AccountView, 0, len(all))
	for _, a := range all {
		if a.OwnerUserID == "" && a.Ownership != "member" && !IsMemberAccount(a.ID) {
			out = append(out, a)
		}
	}
	return out
}

func (s *P115Accounts) IsPersonal(id string) bool {
	a, err := s.Get(id)
	return IsMemberAccount(id) || err == nil && (a.OwnerUserID != "" || a.Ownership == "member")
}

func (s *P115Accounts) commitMember(ctx context.Context, value accountDocument, r model.MemberPlayback) error {
	store, ok := s.store.(interface {
		SaveMember(context.Context, string, model.MemberPlayback) error
	})
	if !ok {
		return fmt.Errorf("个人网盘需要支持事务的账号存储")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := store.SaveMember(ctx, string(raw), r); err != nil {
		return err
	}
	s.value = value
	s.notifyPlayback()
	return nil
}

// A new UID receives a new configuration. Its directory is prepared before the
// active association changes. The current account is never mutated into another identity.
func (s *P115Accounts) BindMember(ctx context.Context, r model.MemberPlayback, name, cookie string, expected uint64, replace ...bool) (model.MemberPlayback, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	uid := accountUID(cookie)
	if uid == "" || r.Source != "personal" && !r.AllowPersonalSwitch {
		return r, ErrAccountInput
	}
	value := s.copy()
	active := -1
	target := -1
	for i, a := range value.Accounts {
		if a.ID == r.AccountID {
			active = i
		}
		if a.UID == uid && a.Ownership == "member" && a.OwnerUserID == r.UserID {
			target = i
		}
	}
	if r.AccountID != "" && (active < 0 || value.Accounts[active].Revision != expected) {
		return r, ErrAccountChanged
	}
	if active >= 0 && target != active && (len(replace) == 0 || !replace[0]) {
		return r, memberError("扫码账号与原账号不同，请使用更换 115 账号入口")
	}
	if target < 0 {
		if len(value.Accounts) >= 120 {
			return r, memberError("网盘配置数量达到上限")
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return r, err
		}
		value.Accounts = append(value.Accounts, P115Account{ID: "member_" + hex.EncodeToString(random[:]), Name: "个人网盘 " + name, Ownership: "member", OwnerUserID: r.UserID, UID: uid})
		target = len(value.Accounts) - 1
	}
	a := value.Accounts[target]
	a.Cookie = cookie
	a.Revision++
	value.Accounts[target] = a
	if target == active {
		r.Suspended = false
	} else {
		if r.PendingAccountID != "" && r.PendingAccountID != a.ID {
			return r, memberError("已有待完成的换号，请先完成或取消")
		}
		if r.PendingAccountID == "" {
			r.PendingDirectoryStarted = false
			for _, id := range r.UncertainDirectories {
				if id == a.ID {
					r.PendingDirectoryStarted = true
				}
			}
		}
		r.PendingAccountID = a.ID
	}
	return r, s.commitMember(ctx, value, r)
}
func (s *P115Accounts) ActivateMember(ctx context.Context, r model.MemberPlayback) (model.MemberPlayback, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.copy()
	updated, err := activateMemberDocument(&value, r)
	if err != nil {
		return r, err
	}
	return updated, s.commitMember(ctx, value, updated)
}

func activateMemberDocument(value *accountDocument, r model.MemberPlayback) (model.MemberPlayback, error) {
	var pending P115Account
	for _, a := range value.Accounts {
		if a.ID == r.PendingAccountID {
			pending = a
		}
	}
	if pending.ID == "" || pending.OwnerUserID != r.UserID || pending.Ownership != "member" || pending.Cookie == "" || pending.PlaybackParentCID == "" {
		return r, ErrAccountChanged
	}
	for i, a := range value.Accounts {
		if a.ID == r.AccountID && a.ID != pending.ID {
			a.Cookie = ""
			a.Revision++
			value.Accounts[i] = a
		}
	}
	uncertain := make([]string, 0, len(r.UncertainDirectories))
	for _, id := range r.UncertainDirectories {
		if id != pending.ID {
			uncertain = append(uncertain, id)
		}
	}
	r.UncertainDirectories = uncertain
	r.AccountID = pending.ID
	r.DirectoryID = pending.PlaybackParentCID
	r.DirectoryStarted = true
	r.PendingAccountID = ""
	r.PendingDirectoryStarted = false
	r.Suspended = false
	return r, nil
}

// SetMemberDirectory commits the directory and binding together, then publishes memory.
func (s *P115Accounts) SetMemberDirectory(ctx context.Context, r model.MemberPlayback, id string, revision uint64, cid string) (model.MemberPlayback, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.copy()
	target := r.AccountID
	if r.PendingAccountID != "" {
		target = r.PendingAccountID
	}
	if id == "" || id != target || !memberDirectoryID(cid, false) {
		return r, ErrAccountChanged
	}
	found := false
	for i, a := range value.Accounts {
		if a.ID != id {
			continue
		}
		if a.Primary || a.Ownership != "member" || a.OwnerUserID != r.UserID || a.Revision != revision || a.Cookie == "" || a.UID == "" || accountUID(a.Cookie) != a.UID {
			return r, ErrAccountChanged
		}
		a.PlaybackParentCID = cid
		a.Revision++
		value.Accounts[i] = a
		found = true
	}
	if !found {
		return r, ErrAccountChanged
	}
	if r.PendingAccountID != "" {
		var err error
		r, err = activateMemberDocument(&value, r)
		if err != nil {
			return r, err
		}
	} else {
		r.DirectoryID, r.DirectoryStarted = cid, true
	}
	uncertain := make([]string, 0, len(r.UncertainDirectories))
	for _, account := range r.UncertainDirectories {
		if account != id {
			uncertain = append(uncertain, account)
		}
	}
	r.UncertainDirectories = uncertain
	return r, s.commitMember(ctx, value, r)
}
func (s *P115Accounts) CancelPendingMember(ctx context.Context, r model.MemberPlayback) (model.MemberPlayback, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.copy()
	for i, a := range value.Accounts {
		if a.ID == r.PendingAccountID && a.OwnerUserID == r.UserID && a.Ownership == "member" {
			a.Cookie = ""
			a.Revision++
			value.Accounts[i] = a
		}
	}
	if r.PendingDirectoryStarted {
		found := false
		for _, id := range r.UncertainDirectories {
			if id == r.PendingAccountID {
				found = true
			}
		}
		if !found {
			r.UncertainDirectories = append(append([]string{}, r.UncertainDirectories...), r.PendingAccountID)
		}
	}
	r.PendingAccountID = ""
	r.PendingDirectoryStarted = false
	return r, s.commitMember(ctx, value, r)
}

// UnbindMember atomically removes only this owner's configurations and associations.
// Cloud files and playback copy history are deliberately left untouched.
func (s *P115Accounts) UnbindMember(ctx context.Context, r model.MemberPlayback) (model.MemberPlayback, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.copy()
	kept := make([]P115Account, 0, len(value.Accounts))
	for _, a := range value.Accounts {
		if a.Ownership != "member" || a.OwnerUserID != r.UserID {
			kept = append(kept, a)
		}
	}
	value.Accounts = kept
	r.AccountID, r.DirectoryID, r.PendingAccountID = "", "", ""
	r.DirectoryStarted, r.PendingDirectoryStarted = false, false
	r.UncertainDirectories = nil
	if r.Source == "personal" {
		r.Suspended = true
		r.Stage, r.Message = "paused", "个人网盘已解绑，重新扫码后恢复播放"
	}
	return r, s.commitMember(ctx, value, r)
}
