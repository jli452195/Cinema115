package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

type CloudDownloadConfig struct {
	AccountID  string `json:"accountId"`
	TargetCID  string `json:"targetCID"`
	TargetPath string `json:"targetPath"`
}

type cloudDownloadDocument struct {
	Version int `json:"version"`
	CloudDownloadConfig
}

type CloudDownloadSettings struct {
	PrimaryID       func() string
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
	return s, nil
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
	raw, err := json.Marshal(document)
	if err != nil {
		return CloudDownloadConfig{}, fmt.Errorf("编码云下载配置: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.Save(string(raw)); err != nil {
		return CloudDownloadConfig{}, fmt.Errorf("保存云下载配置: %w", err)
	}
	s.value = document
	return input, nil
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
