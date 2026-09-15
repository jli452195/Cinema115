package service

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

var ErrAccountMissing = errors.New("账号配置不存在，请刷新页面")
var ErrAccountChanged = errors.New("账号配置已发生变化，请刷新后重试")
var ErrAccountInput = errors.New("账号配置无效")

type P115Account struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Cookie   string `json:"cookie"`
	Revision uint64 `json:"revision"`
}
type P115AccountView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	Configured bool   `json:"configured"`
	Revision   uint64 `json:"revision"`
}
type accountDocument struct {
	Version int `json:"version"`
	// 仅解码旧文件，用于迁移为功能明确绑定，不能作为账号读取的默认值。
	LegacyActiveID string        `json:"activeId,omitempty"`
	Accounts       []P115Account `json:"accounts"`
}
type accountSecretStore interface {
	Value() string
	Save(string) error
}
type P115Accounts struct {
	mu    sync.RWMutex
	store accountSecretStore
	value accountDocument
}

func newP115Accounts(store accountSecretStore, legacy string) (*P115Accounts, error) {
	s := &P115Accounts{store: store}
	if store.Value() != "" {
		if json.Unmarshal([]byte(store.Value()), &s.value) != nil {
			return nil, fmt.Errorf("115 多账号配置无法解析")
		}
		if s.value.Version != 1 || len(s.value.Accounts) > 20 {
			return nil, fmt.Errorf("115 多账号配置版本或数量无效")
		}
		seen := make(map[string]bool, len(s.value.Accounts))
		for _, a := range s.value.Accounts {
			if a.ID == "" || seen[a.ID] || a.Revision == 0 || !validAccountName(a.Name) {
				return nil, fmt.Errorf("115 多账号配置损坏")
			}
			seen[a.ID] = true
		}
		if s.value.LegacyActiveID != "" && !seen[s.value.LegacyActiveID] {
			return nil, fmt.Errorf("115 旧账号配置缺失")
		}
		s.value.LegacyActiveID = ""
		return s, nil
	}
	initial := accountDocument{Version: 1, Accounts: []P115Account{}}
	if legacy != "" {
		initial.Accounts = append(initial.Accounts, P115Account{ID: "default", Name: "账号 1", Cookie: legacy, Revision: 1})
	}
	if err := s.commit(initial); err != nil {
		return nil, err
	}
	return s, nil
}
func validAccountName(name string) bool {
	return strings.TrimSpace(name) != "" && len([]rune(name)) <= 50 && !strings.ContainsAny(name, "\r\n\x00")
}
func accountUID(cookie string) string {
	for _, part := range strings.Split(cookie, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && key == "UID" {
			id, _, _ := strings.Cut(value, "_")
			if n, err := strconv.ParseUint(id, 10, 64); err == nil && n > 0 {
				return id
			}
		}
	}
	return ""
}
func (s *P115Accounts) commit(value accountDocument) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("编码账号配置: %w", err)
	}
	if err := s.store.Save(string(raw)); err != nil {
		return fmt.Errorf("保存账号配置失败: %w", err)
	}
	s.value = value
	return nil
}
func (s *P115Accounts) copy() accountDocument {
	value := s.value
	value.Accounts = append([]P115Account{}, value.Accounts...)
	return value
}
func (s *P115Accounts) List() []P115AccountView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]P115AccountView, 0, len(s.value.Accounts))
	for _, a := range s.value.Accounts {
		result = append(result, P115AccountView{a.ID, a.Name, accountUID(a.Cookie), a.Cookie != "", a.Revision})
	}
	return result
}
func (s *P115Accounts) Get(id string) (P115Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.value.Accounts {
		if a.ID == id {
			return a, nil
		}
	}
	return P115Account{}, ErrAccountMissing
}

// 固定配置缺失或已清空时返回空凭据，禁止退回其他账号。
func (s *P115Accounts) CookieFor(id string) string { a, _ := s.Get(id); return a.Cookie }

func (s *P115Accounts) ValidateSelection(id string) error {
	if id == "" {
		return generationInputError("请先选择此功能使用的 115 账号配置")
	}
	a, err := s.Get(id)
	if err != nil {
		return generationInputError("所选 115 配置已删除，请重新选择")
	}
	if a.Cookie == "" {
		return generationInputError("所选 115 配置尚未登录，请先在 115 助手中保存凭据")
	}
	return nil
}
func (s *P115Accounts) Add(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !validAccountName(name) {
		return "", fmt.Errorf("%w：名称应为 1～50 字", ErrAccountInput)
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(nonce[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.value.Accounts) >= 20 {
		return "", fmt.Errorf("%w：最多保存 20 个账号配置", ErrAccountInput)
	}
	value := s.copy()
	value.Accounts = append(value.Accounts, P115Account{ID: id, Name: name, Revision: 1})
	if err := s.commit(value); err != nil {
		return "", err
	}
	return id, nil
}

// 版本校验避免迟到的 Cookie 验证和扫码响应覆盖后来保存的凭据。
func (s *P115Accounts) Update(id string, revision uint64, name string, cookie *string) error {
	name = strings.TrimSpace(name)
	if !validAccountName(name) {
		return fmt.Errorf("%w：名称应为 1～50 字", ErrAccountInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.copy()
	for i, a := range value.Accounts {
		if a.ID != id {
			continue
		}
		if a.Revision != revision {
			return ErrAccountChanged
		}
		a.Name = name
		a.Revision++
		if cookie != nil {
			a.Cookie = *cookie
		}
		value.Accounts[i] = a
		return s.commit(value)
	}
	return ErrAccountMissing
}
func (s *P115Accounts) Delete(id string, revision uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.copy()
	for i, a := range value.Accounts {
		if a.ID != id {
			continue
		}
		if a.Revision != revision {
			return ErrAccountChanged
		}
		value.Accounts = append(value.Accounts[:i], value.Accounts[i+1:]...)
		return s.commit(value)
	}
	return ErrAccountMissing
}
