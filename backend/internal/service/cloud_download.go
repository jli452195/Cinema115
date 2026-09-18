package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"strm115/backend/internal/client/p115"
)

// CloudURLAdder is the small upstream surface needed by the shared cloud
// download workflow. Keeping it narrow makes the account-binding rules easy
// to test without a live 115 account.
type CloudURLAdder interface {
	AddURL(context.Context, string, string, string) (json.RawMessage, error)
	LookupTask(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error)
	DeleteTaskKeepFiles(context.Context, string, string) error
}

var (
	ErrCloudTaskRunning = errors.New("相同云下载任务仍在运行")
	ErrCloudTaskUnclear = errors.New("无法安全确认云下载任务状态")
)

type CloudDownloadReceipt struct {
	AccountName    string
	TargetPath     string
	Task           p115.CloudTask
	RemovedOldTask bool
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
	account, err := s.snapshot(config)
	if err != nil {
		return CloudDownloadReceipt{}, err
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

// Redo removes one exact completed/stopped/failed task record while keeping
// all downloaded files, then submits the original link again. Running tasks
// and tasks whose status cannot be verified are never deleted.
func (s *CloudDownloadService) Redo(ctx context.Context, config CloudDownloadConfig, rawURL string) (CloudDownloadReceipt, error) {
	account, err := s.snapshot(config)
	if err != nil {
		return CloudDownloadReceipt{}, err
	}
	resource, err := p115.ParseCloudResource(rawURL)
	if err != nil {
		return CloudDownloadReceipt{}, err
	}
	receipt := CloudDownloadReceipt{AccountName: account.Name, TargetPath: strings.TrimSpace(config.TargetPath)}
	task, found, err := s.Client.LookupTask(ctx, account.Cookie, resource)
	if err != nil {
		return receipt, fmt.Errorf("查询原云下载任务: %w", err)
	}
	if found {
		if !resource.MatchesTaskHash(task.InfoHash) {
			return receipt, ErrCloudTaskUnclear
		}
		ready, known := task.RedoReady()
		if !known {
			return receipt, ErrCloudTaskUnclear
		}
		if !ready {
			return receipt, ErrCloudTaskRunning
		}
		if err := s.Client.DeleteTaskKeepFiles(ctx, account.Cookie, task.InfoHash); err != nil {
			return receipt, fmt.Errorf("移除原云下载任务记录: %w", err)
		}
		receipt.RemovedOldTask = true
	}
	raw, err := s.Client.AddURL(ctx, account.Cookie, rawURL, strings.TrimSpace(config.TargetCID))
	if err != nil {
		return receipt, fmt.Errorf("重新提交 115 云下载: %w", err)
	}
	receipt.Task, _ = p115.ParseCloudTask(raw)
	return receipt, nil
}

func (s *CloudDownloadService) snapshot(config CloudDownloadConfig) (P115Account, error) {
	if err := s.ValidateConfig(config); err != nil {
		return P115Account{}, err
	}
	account, err := s.Accounts.Get(strings.TrimSpace(config.AccountID))
	if err != nil {
		return P115Account{}, generationInputError("所选 115 配置已删除，请重新选择")
	}
	return account, nil
}
