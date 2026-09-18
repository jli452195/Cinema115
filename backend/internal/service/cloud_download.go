package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"strm115/backend/internal/client/p115"
)

// CloudURLAdder is the small upstream surface needed by the shared cloud
// download workflow. Keeping it narrow makes the account-binding rules easy
// to test without a live 115 account.
type CloudURLAdder interface {
	AddURL(context.Context, string, string, string) (json.RawMessage, error)
}

type CloudDownloadReceipt struct {
	AccountName string
	TargetPath  string
	Task        p115.CloudTask
}

// CloudDownloadService is shared by the administrator page and Telegram.
// Each call resolves one explicit account and snapshots its cookie before the
// upstream request; it never falls back to another account.
type CloudDownloadService struct {
	Accounts *P115Accounts
	Client   CloudURLAdder
}

func (s *CloudDownloadService) ValidateConfig(config CloudDownloadConfig) error {
	if s == nil || s.Accounts == nil || s.Client == nil {
		return generationInputError("云下载服务尚未初始化")
	}
	config.AccountID = strings.TrimSpace(config.AccountID)
	config.TargetCID = strings.TrimSpace(config.TargetCID)
	config.TargetPath = strings.TrimSpace(config.TargetPath)
	if err := s.Accounts.ValidateSelection(config.AccountID); err != nil {
		return err
	}
	if err := validateCloudDownloadTarget(config.TargetCID, config.TargetPath); err != nil {
		return generationInputError(err.Error())
	}
	return nil
}

func (s *CloudDownloadService) Submit(ctx context.Context, config CloudDownloadConfig, rawURL string) (CloudDownloadReceipt, error) {
	if err := s.ValidateConfig(config); err != nil {
		return CloudDownloadReceipt{}, err
	}
	account, err := s.Accounts.Get(strings.TrimSpace(config.AccountID))
	if err != nil {
		return CloudDownloadReceipt{}, generationInputError("所选 115 配置已删除，请重新选择")
	}
	// account is a value copy. Its cookie and the target below stay fixed for
	// this operation even if the administrator edits the feature meanwhile.
	raw, err := s.Client.AddURL(ctx, account.Cookie, rawURL, strings.TrimSpace(config.TargetCID))
	if err != nil {
		return CloudDownloadReceipt{}, fmt.Errorf("提交 115 云下载: %w", err)
	}
	task, _ := p115.ParseCloudTask(raw)
	return CloudDownloadReceipt{AccountName: account.Name, TargetPath: strings.TrimSpace(config.TargetPath), Task: task}, nil
}
