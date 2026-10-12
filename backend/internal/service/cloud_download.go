package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

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
	ErrCloudTaskBusy    = errors.New("相同账号的这个资源正在处理，请勿重复提交")
)

const CloudDownloadTimeout = 90 * time.Second

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
	mu       sync.Mutex
	inflight map[string]struct{}
}

func (s *CloudDownloadService) ValidateConfig(config CloudDownloadConfig) error {
	if s == nil || s.Accounts == nil || s.Client == nil {
		return generationInputError("云下载服务尚未初始化")
	}
	config.AccountID = strings.TrimSpace(config.AccountID)
	config.TargetCID = strings.TrimSpace(config.TargetCID)
	config.TargetPath = strings.TrimSpace(config.TargetPath)
	if err := s.Accounts.ValidatePrimary(config.AccountID); err != nil {
		return err
	}
	if err := validateCloudDownloadTarget(config.TargetCID, config.TargetPath); err != nil {
		return generationInputError(err.Error())
	}
	return nil
}

// 保留旧调用入口，所有入口默认使用同一套安全重提流程。
func (s *CloudDownloadService) Redo(ctx context.Context, config CloudDownloadConfig, rawURL string) (CloudDownloadReceipt, error) {
	return s.Submit(ctx, config, rawURL)
}

// 先精确查询；仅删除已结束的任务记录，始终保留文件，不自动循环重试。
func (s *CloudDownloadService) Submit(ctx context.Context, config CloudDownloadConfig, rawURL string) (CloudDownloadReceipt, error) {
	account, err := s.snapshot(config)
	if err != nil {
		return CloudDownloadReceipt{}, err
	}
	return s.submitSnapshot(ctx, config, account, rawURL)
}

func (s *CloudDownloadService) submitSnapshot(ctx context.Context, config CloudDownloadConfig, account P115Account, rawURL string) (CloudDownloadReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, CloudDownloadTimeout)
	defer cancel()
	resource, err := p115.ParseCloudResource(rawURL)
	if err != nil {
		return CloudDownloadReceipt{}, err
	}
	receipt := CloudDownloadReceipt{AccountName: account.Name, TargetPath: strings.TrimSpace(config.TargetPath)}
	// 锁不包含目录，防止不同入口将同一资源同时提交到不同目录；只保存编号，不保存凭据。
	key := account.ID + ":" + resource.Protocol + ":" + resource.Hash
	s.mu.Lock()
	if _, exists := s.inflight[key]; exists {
		s.mu.Unlock()
		return receipt, ErrCloudTaskBusy
	}
	if s.inflight == nil {
		s.inflight = make(map[string]struct{})
	}
	s.inflight[key] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, key)
		s.mu.Unlock()
	}()
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	// 本次查询、删除及提交始终使用这里取得的账号凭据和目录快照。
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
		if err := ctx.Err(); err != nil {
			return receipt, err
		}
		if err := s.Client.DeleteTaskKeepFiles(ctx, account.Cookie, resource.Hash); err != nil {
			return receipt, fmt.Errorf("移除原云下载任务记录: %w", err)
		}
		receipt.RemovedOldTask = true
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	raw, err := s.Client.AddURL(ctx, account.Cookie, rawURL, strings.TrimSpace(config.TargetCID))
	if err != nil {
		return receipt, fmt.Errorf("提交 115 云下载: %w", err)
	}
	receipt.Task, _ = p115.ParseCloudTask(raw)
	return receipt, nil
}

func (s *CloudDownloadService) snapshot(config CloudDownloadConfig) (P115Account, error) {
	if err := s.ValidateConfig(config); err != nil {
		return P115Account{}, err
	}
	account, err := s.Accounts.Primary(strings.TrimSpace(config.AccountID))
	if err != nil {
		return P115Account{}, generationInputError("所选 115 配置已删除，请重新选择")
	}
	if strings.TrimSpace(account.Cookie) == "" {
		return P115Account{}, generationInputError("所选 115 账号凭据已清空，请重新登录")
	}
	return account, nil
}

// 网络中断不能证明上游未执行操作，提示用户核对而非自动重试。
func CloudDownloadUncertain(err error) bool {
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &network)
}

func CloudDownloadErrorMessage(err error) string {
	switch {
	case errors.Is(err, p115.ErrCloudInput):
		return "链接格式无效，请发送完整的 magnet 或 ED2K 链接"
	case errors.Is(err, ErrCloudTaskRunning):
		return "相同任务仍在 115 中运行或等待，未删除或重新提交"
	case errors.Is(err, ErrCloudTaskUnclear):
		return "无法安全确认旧任务状态，未执行删除"
	case errors.Is(err, ErrCloudTaskBusy):
		return ErrCloudTaskBusy.Error()
	case CloudDownloadUncertain(err):
		return "连接 115 超时或中断，处理结果尚未确认，请先检查 115 任务列表再决定是否重试"
	case errors.Is(err, ErrGenerationInput):
		return strings.TrimPrefix(err.Error(), ErrGenerationInput.Error()+"：")
	default:
		return err.Error()
	}
}
