package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"crypto/rand"
	"encoding/hex"
)

var (
	ErrShareTransferTargetChanged = errors.New("115 分享转存目标已变化，请刷新后重试")
	ErrShareTransferTargetMissing = errors.New("115 分享转存目标不存在")
)

type ShareTransferTarget struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	AccountID     string `json:"accountId"`
	TargetCID     string `json:"targetCID"`
	TargetPath    string `json:"targetPath"`
	AssignedBotID string `json:"assignedBotId,omitempty"`
}

type shareTransferDocument struct {
	Targets []ShareTransferTarget `json:"targets"`
}

type ShareTransferSettings struct {
	PrimaryID       func() string
	ValidateAccount func(string) error
	OnChange        func()
	mu              sync.RWMutex
	store           *SessionStore
	targets         []ShareTransferTarget
}

func NewShareTransferSettings(store *SessionStore) (*ShareTransferSettings, error) {
	s := &ShareTransferSettings{store: store, targets: []ShareTransferTarget{}}
	if strings.TrimSpace(store.Value()) == "" {
		return s, nil
	}
	var document shareTransferDocument
	if err := json.Unmarshal([]byte(store.Value()), &document); err != nil {
		return nil, fmt.Errorf("解析 115 分享转存配置: %w", err)
	}
	if err := validateShareTransferTargets(document.Targets); err != nil {
		return nil, fmt.Errorf("115 分享转存配置无效: %w", err)
	}
	s.targets = append(s.targets, document.Targets...)
	return s, nil
}

func validateShareTransferTargets(targets []ShareTransferTarget) error {
	if len(targets) > 50 {
		return fmt.Errorf("115 分享转存目标最多 50 个")
	}
	ids, names := make(map[string]bool, len(targets)), make(map[string]bool, len(targets))
	for _, target := range targets {
		name := strings.TrimSpace(target.Name)
		if target.ID == "" || ids[target.ID] || name == "" || utf8.RuneCountInString(name) > 50 || strings.ContainsAny(name, "\r\n\x00") {
			return fmt.Errorf("115 分享转存目标配置无效")
		}
		key := strings.ToLower(name)
		if names[key] {
			return fmt.Errorf("115 分享转存目标名称不能重复")
		}
		if err := validateP115TargetDirectory(target.TargetCID, target.TargetPath); err != nil {
			return err
		}
		ids[target.ID], names[key] = true, true
	}
	return nil
}

func (s *ShareTransferSettings) Targets() []ShareTransferTarget {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]ShareTransferTarget{}, s.targets...)
}

func (s *ShareTransferSettings) Target(id string) (ShareTransferTarget, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, target := range s.targets {
		if target.ID == id {
			return target, true
		}
	}
	return ShareTransferTarget{}, false
}

func (s *ShareTransferSettings) TargetReady(target ShareTransferTarget) bool {
	if s.PrimaryID != nil && target.AccountID != s.PrimaryID() {
		return false
	}
	return s.ValidateAccount != nil && s.ValidateAccount(target.AccountID) == nil && validateP115TargetDirectory(target.TargetCID, target.TargetPath) == nil
}

func (s *ShareTransferSettings) ValidateSnapshot(snapshot ShareTransferTarget) error {
	current, ok := s.Target(snapshot.ID)
	if !ok {
		return ErrShareTransferTargetMissing
	}
	if current != snapshot {
		return ErrShareTransferTargetChanged
	}
	if !s.TargetReady(current) {
		return fmt.Errorf("115 分享转存目标不可用，请检查主账号和目标目录")
	}
	return nil
}

func (s *ShareTransferSettings) SaveTarget(input ShareTransferTarget) (ShareTransferTarget, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.TargetCID = strings.TrimSpace(input.TargetCID)
	input.TargetPath = strings.TrimSpace(input.TargetPath)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 50 || strings.ContainsAny(input.Name, "\r\n\x00") {
		return ShareTransferTarget{}, fmt.Errorf("%w：名称应为 1～50 字", ErrAccountInput)
	}
	if s.PrimaryID != nil {
		id := s.PrimaryID()
		if input.AccountID != "" && input.AccountID != id {
			return ShareTransferTarget{}, fmt.Errorf("%w：主账号已变化，请重新选择目标目录", ErrAccountInput)
		}
		input.AccountID = id
	}
	if s.ValidateAccount == nil {
		return ShareTransferTarget{}, fmt.Errorf("115 分享转存账号校验尚未初始化")
	}
	if err := s.ValidateAccount(input.AccountID); err != nil {
		return ShareTransferTarget{}, err
	}
	if err := validateP115TargetDirectory(input.TargetCID, input.TargetPath); err != nil {
		return ShareTransferTarget{}, err
	}
	s.mu.Lock()
	targets := append([]ShareTransferTarget{}, s.targets...)
	if input.ID == "" {
		if len(targets) >= 50 {
			s.mu.Unlock()
			return ShareTransferTarget{}, fmt.Errorf("115 分享转存目标最多 50 个")
		}
		var nonce [12]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			s.mu.Unlock()
			return ShareTransferTarget{}, fmt.Errorf("生成 115 分享转存目标编号: %w", err)
		}
		input.ID = hex.EncodeToString(nonce[:])
		targets = append(targets, input)
	} else {
		found := false
		for i, current := range targets {
			if current.ID != input.ID {
				continue
			}
			input.AssignedBotID = current.AssignedBotID
			targets[i], found = input, true
			break
		}
		if !found {
			s.mu.Unlock()
			return ShareTransferTarget{}, ErrShareTransferTargetMissing
		}
	}
	if err := validateShareTransferTargets(targets); err != nil {
		s.mu.Unlock()
		return ShareTransferTarget{}, err
	}
	if err := s.commitLocked(targets); err != nil {
		s.mu.Unlock()
		return ShareTransferTarget{}, err
	}
	onChange := s.OnChange
	s.mu.Unlock()
	if onChange != nil {
		onChange()
	}
	return input, nil
}

func (s *ShareTransferSettings) DeleteTarget(id string) error {
	s.mu.Lock()
	targets := append([]ShareTransferTarget{}, s.targets...)
	for i, target := range targets {
		if target.ID != id {
			continue
		}
		targets = append(targets[:i], targets[i+1:]...)
		if err := s.commitLocked(targets); err != nil {
			s.mu.Unlock()
			return err
		}
		onChange := s.OnChange
		s.mu.Unlock()
		if onChange != nil {
			onChange()
		}
		return nil
	}
	s.mu.Unlock()
	return ErrShareTransferTargetMissing
}

func (s *ShareTransferSettings) BindTarget(id, expectedBotID, botID string) error {
	s.mu.Lock()
	targets := append([]ShareTransferTarget{}, s.targets...)
	for i, target := range targets {
		if target.ID != id {
			continue
		}
		if target.AssignedBotID != expectedBotID {
			s.mu.Unlock()
			return ErrShareTransferTargetChanged
		}
		if target.AssignedBotID == botID {
			s.mu.Unlock()
			return nil
		}
		target.AssignedBotID = botID
		targets[i] = target
		if err := s.commitLocked(targets); err != nil {
			s.mu.Unlock()
			return err
		}
		onChange := s.OnChange
		s.mu.Unlock()
		if onChange != nil {
			onChange()
		}
		return nil
	}
	s.mu.Unlock()
	return ErrShareTransferTargetMissing
}

func (s *ShareTransferSettings) commitLocked(targets []ShareTransferTarget) error {
	raw, err := json.Marshal(shareTransferDocument{Targets: targets})
	if err != nil {
		return fmt.Errorf("编码 115 分享转存配置: %w", err)
	}
	if err := s.store.Save(string(raw)); err != nil {
		return fmt.Errorf("保存 115 分享转存配置: %w", err)
	}
	s.targets = targets
	return nil
}
