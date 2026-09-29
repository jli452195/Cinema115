package service

import (
	"context"
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

func (s *P115Accounts) ClearMemberCookie(userID string, revision uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.copy()
	for i, a := range value.Accounts {
		if a.OwnerUserID != userID || a.Ownership != "member" {
			continue
		}
		if a.Revision != revision {
			return ErrAccountChanged
		}
		a.Cookie = ""
		a.Revision++
		value.Accounts[i] = a
		return s.commit(value)
	}
	return ErrAccountMissing
}

// Cleanup never mutates an administrator account, including an assigned target.
func (s *P115Accounts) clearMemberCredentials(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.copy()
	changed := false
	for i, a := range value.Accounts {
		if a.OwnerUserID != userID || a.Ownership != "member" {
			continue
		}
		if a.Primary {
			return ErrAccountInput
		}
		if a.Cookie == "" {
			continue
		}
		a.Cookie = ""
		a.Revision++
		value.Accounts[i] = a
		changed = true
	}
	if !changed {
		return nil
	}
	return s.commit(value)
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
	return nil
}

// The cookie and owner association become visible only after one transaction.
func (s *P115Accounts) BindMember(ctx context.Context, r model.MemberPlayback, name, cookie string, expected uint64) (model.MemberPlayback, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	uid := accountUID(cookie)
	if uid == "" || r.Source != "personal" && !r.AllowPersonalSwitch {
		return r, ErrAccountInput
	}
	id := "member_" + r.UserID
	if len(r.UserID) != 32 || strings.Trim(r.UserID, "0123456789abcdef") != "" || r.AccountID != "" && r.AccountID != id {
		return r, ErrAccountInput
	}
	value := s.copy()
	index := -1
	count := 0
	for i, a := range value.Accounts {
		if a.OwnerUserID != "" || a.Ownership == "member" || IsMemberAccount(a.ID) {
			count++
		}
		if a.ID == id {
			index = i
		} else if a.UID == uid {
			return r, fmt.Errorf("%w：该 UID 已属于其他账号配置", ErrAccountInput)
		}
	}
	a := P115Account{ID: id, Name: "个人网盘 " + name, Ownership: "member", OwnerUserID: r.UserID}
	if index >= 0 {
		a = value.Accounts[index]
		if a.Primary || a.OwnerUserID != r.UserID || a.Ownership != "member" || a.Revision != expected || a.UID != "" && a.UID != uid {
			return r, ErrAccountChanged
		}
	} else if expected != 0 || r.AccountID != "" {
		return r, ErrAccountChanged
	} else if count >= 100 || len(value.Accounts) >= 120 {
		return r, fmt.Errorf("个人网盘数量达到上限")
	}
	a.Cookie = cookie
	a.UID = uid
	a.Revision++
	if index >= 0 {
		value.Accounts[index] = a
	} else {
		value.Accounts = append(value.Accounts, a)
	}
	r.AccountID = id
	if r.Source == "personal" {
		r.Suspended = false
	}
	if r.EmbyID == "" {
		r.Stage = "bound"
	}
	if err := s.commitMember(ctx, value, r); err != nil {
		return r, err
	}
	return r, nil
}

// Repair only a record explicitly owned by this website user. No UID or cloud
// data is discarded. Reference checks are performed by the playback service.
func (s *P115Accounts) RepairMember(ctx context.Context, r model.MemberPlayback, removeEmpty bool) (model.MemberPlayback, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.copy()
	for i, a := range value.Accounts {
		if a.ID != r.AccountID {
			continue
		}
		if a.ID != "member_"+r.UserID || a.Primary || a.OwnerUserID != "" && a.OwnerUserID != r.UserID {
			return r, ErrAccountInput
		}
		if removeEmpty && a.UID == "" && a.Cookie == "" && a.PlaybackParentCID == "" && r.DirectoryID == "" && !r.DirectoryStarted {
			value.Accounts = append(value.Accounts[:i], value.Accounts[i+1:]...)
			r.AccountID = ""
		} else {
			if a.OwnerUserID == r.UserID && a.Ownership == "member" {
				return r, nil
			}
			a.OwnerUserID = r.UserID
			a.Ownership = "member"
			a.Revision++
			value.Accounts[i] = a
		}
		return r, s.commitMember(ctx, value, r)
	}
	return r, nil
}
