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
	"unicode/utf8"
)

var ErrCloudTargetChanged = errors.New("转存目标归属已变化，请刷新后重试")
var ErrCloudTargetMissing = errors.New("转存目标不存在")

type CloudDownloadConfig struct {
	AccountID  string `json:"accountId"`
	TargetCID  string `json:"targetCID"`
	TargetPath string `json:"targetPath"`
}

type cloudDownloadDocument struct {
	Version int `json:"version"`
	CloudDownloadConfig
	Targets []CloudDownloadTarget `json:"targets,omitempty"`
}

type CloudDownloadTarget struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	AccountID     string `json:"accountId"`
	TargetCID     string `json:"targetCID"`
	TargetPath    string `json:"targetPath"`
	AssignedBotID string `json:"assignedBotId,omitempty"`
}

type CloudDownloadSettings struct {
	PrimaryID       func() string
	OnChange        func()
	mu              sync.RWMutex
	store           *SessionStore
	value           cloudDownloadDocument
	ValidateAccount func(string) error
}

func NewCloudDownloadSettings(store *SessionStore) (*CloudDownloadSettings, error) {
	s := &CloudDownloadSettings{store: store, value: cloudDownloadDocument{Version: 1, CloudDownloadConfig: CloudDownloadConfig{TargetCID: "0", TargetPath: "/"}}}
	if strings.TrimSpace(store.Value()) == "" {
		return s, nil
	}
	if err := json.Unmarshal([]byte(store.Value()), &s.value); err != nil || s.value.Version != 1 {
		return nil, fmt.Errorf("云下载配置无法解析")
	}
	if err := validateCloudDownloadTarget(s.value.TargetCID, s.value.TargetPath); err != nil {
		return nil, fmt.Errorf("云下载配置损坏: %w", err)
	}
	if s.value.Targets == nil {
		s.value.Targets = []CloudDownloadTarget{}
	}
	if err := validateCloudDownloadTargets(s.value.Targets); err != nil {
		return nil, fmt.Errorf("云下载配置损坏: %w", err)
	}
	return s, nil
}

func validateCloudDownloadTargets(targets []CloudDownloadTarget) error {
	if len(targets) > 50 {
		return fmt.Errorf("云下载目标配置最多 50 个")
	}
	seenIDs, seenNames := make(map[string]bool, len(targets)), make(map[string]bool, len(targets))
	for _, target := range targets {
		name := strings.TrimSpace(target.Name)
		if target.ID == "" || seenIDs[target.ID] || name == "" || utf8.RuneCountInString(name) > 50 || strings.ContainsAny(name, "\r\n\x00") {
			return fmt.Errorf("云下载目标配置无效")
		}
		if seenNames[strings.ToLower(name)] {
			return fmt.Errorf("云下载目标名称不能重复")
		}
		if err := validateCloudDownloadTarget(target.TargetCID, target.TargetPath); err != nil {
			return err
		}
		seenIDs[target.ID], seenNames[strings.ToLower(name)] = true, true
	}
	return nil
}

func (s *CloudDownloadSettings) Targets() []CloudDownloadTarget {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]CloudDownloadTarget{}, s.value.Targets...)
}

func (s *CloudDownloadSettings) Target(id string) (CloudDownloadTarget, bool) {
	for _, target := range s.Targets() {
		if target.ID == id {
			return target, true
		}
	}
	return CloudDownloadTarget{}, false
}

func (s *CloudDownloadSettings) TargetReady(target CloudDownloadTarget) bool {
	if s.PrimaryID != nil && target.AccountID != s.PrimaryID() {
		return false
	}
	return s.ValidateAccount != nil && s.ValidateAccount(target.AccountID) == nil && validateCloudDownloadTarget(target.TargetCID, target.TargetPath) == nil
}

func (s *CloudDownloadSettings) SaveTarget(input CloudDownloadTarget) (CloudDownloadTarget, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.TargetCID = strings.TrimSpace(input.TargetCID)
	input.TargetPath = strings.TrimSpace(input.TargetPath)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 50 || strings.ContainsAny(input.Name, "\r\n\x00") {
		return CloudDownloadTarget{}, fmt.Errorf("%w：名称应为 1～50 字", ErrAccountInput)
	}
	if s.PrimaryID != nil {
		id := s.PrimaryID()
		if input.AccountID != "" && input.AccountID != id {
			return CloudDownloadTarget{}, fmt.Errorf("%w：主账号已变化，请重新选择下载目录", ErrAccountInput)
		}
		input.AccountID = id
	}
	if s.ValidateAccount == nil {
		return CloudDownloadTarget{}, fmt.Errorf("云下载账号校验尚未初始化")
	}
	if err := s.ValidateAccount(input.AccountID); err != nil {
		return CloudDownloadTarget{}, err
	}
	if err := validateCloudDownloadTarget(input.TargetCID, input.TargetPath); err != nil {
		return CloudDownloadTarget{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	document := s.value
	document.Targets = append([]CloudDownloadTarget{}, s.value.Targets...)
	if input.ID == "" {
		if len(document.Targets) >= 50 {
			return CloudDownloadTarget{}, fmt.Errorf("云下载目标配置最多 50 个")
		}
		var nonce [12]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return CloudDownloadTarget{}, fmt.Errorf("生成云下载目标编号: %w", err)
		}
		input.ID, input.AssignedBotID = hex.EncodeToString(nonce[:]), ""
		document.Targets = append(document.Targets, input)
	} else {
		found := false
		for i, current := range document.Targets {
			if current.ID != input.ID {
				continue
			}
			input.AssignedBotID = current.AssignedBotID
			document.Targets[i], found = input, true
			break
		}
		if !found {
			return CloudDownloadTarget{}, fmt.Errorf("云下载目标不存在")
		}
	}
	if err := validateCloudDownloadTargets(document.Targets); err != nil {
		return CloudDownloadTarget{}, fmt.Errorf("校验云下载目标: %w", err)
	}
	if err := s.commit(document); err != nil {
		return CloudDownloadTarget{}, err
	}
	if s.OnChange != nil {
		s.OnChange()
	}
	return input, nil
}

func (s *CloudDownloadSettings) DeleteTarget(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	document := s.value
	document.Targets = append([]CloudDownloadTarget{}, s.value.Targets...)
	for i, target := range document.Targets {
		if target.ID != id {
			continue
		}
		document.Targets = append(document.Targets[:i], document.Targets[i+1:]...)
		if err := s.commit(document); err != nil {
			return err
		}
		if s.OnChange != nil {
			s.OnChange()
		}
		return nil
	}
	return fmt.Errorf("云下载目标不存在")
}

func (s *CloudDownloadSettings) BindTarget(id, expectedBotID, botID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	document := s.value
	document.Targets = append([]CloudDownloadTarget{}, s.value.Targets...)
	for i, target := range document.Targets {
		if target.ID != id {
			continue
		}
		if target.AssignedBotID != expectedBotID {
			return ErrCloudTargetChanged
		}
		if target.AssignedBotID == botID {
			return nil
		}
		target.AssignedBotID = botID
		document.Targets[i] = target
		if err := s.commit(document); err != nil {
			return err
		}
		if s.OnChange != nil {
			s.OnChange()
		}
		return nil
	}
	return ErrCloudTargetMissing
}

func (s *CloudDownloadSettings) View() CloudDownloadConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value := s.value.CloudDownloadConfig
	if s.PrimaryID != nil {
		if id := s.PrimaryID(); value.AccountID != id {
			value.AccountID = id
			value.TargetCID, value.TargetPath = "", ""
		}
	}
	return value
}

func (s *CloudDownloadSettings) Save(input CloudDownloadConfig) (CloudDownloadConfig, error) {
	if s.PrimaryID != nil {
		id := s.PrimaryID()
		if input.AccountID != "" && input.AccountID != id {
			return CloudDownloadConfig{}, generationInputError("主账号已变化，请重新选择下载目录")
		}
		input.AccountID = id
	}
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.TargetCID = strings.TrimSpace(input.TargetCID)
	input.TargetPath = strings.TrimSpace(input.TargetPath)
	if s.ValidateAccount == nil {
		return CloudDownloadConfig{}, fmt.Errorf("云下载账号校验尚未初始化")
	}
	if err := s.ValidateAccount(input.AccountID); err != nil {
		return CloudDownloadConfig{}, err
	}
	if err := validateCloudDownloadTarget(input.TargetCID, input.TargetPath); err != nil {
		return CloudDownloadConfig{}, err
	}
	document := cloudDownloadDocument{Version: 1, CloudDownloadConfig: input}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Preserve named Telegram targets when saving the manual-transfer default.
	document.Targets = append([]CloudDownloadTarget{}, s.value.Targets...)
	if err := s.commit(document); err != nil {
		return CloudDownloadConfig{}, err
	}
	return input, nil
}

func (s *CloudDownloadSettings) commit(document cloudDownloadDocument) error {
	raw, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("编码云下载配置: %w", err)
	}
	if err := s.store.Save(string(raw)); err != nil {
		return fmt.Errorf("保存云下载配置: %w", err)
	}
	s.value = document
	return nil
}

func validateCloudDownloadTarget(cid, path string) error {
	if _, err := strconv.ParseUint(cid, 10, 64); err != nil {
		return fmt.Errorf("%w：云下载目标目录编号无效", ErrAccountInput)
	}
	if path == "" || len([]rune(path)) > 1024 || strings.ContainsAny(path, "\r\n\x00") {
		return fmt.Errorf("%w：云下载目标目录路径无效", ErrAccountInput)
	}
	return nil
}
