package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

// CloudTaskReader exposes exact observations without manual delete/redo access.
type CloudTaskReader interface {
	LookupTask(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error)
}

// CloudURLAdder is the upstream surface used only by manual cloud downloads.
type CloudURLAdder interface {
	CloudTaskReader
	AddURL(context.Context, string, string, string) (json.RawMessage, error)
	DeleteTaskKeepFiles(context.Context, string, string) error
}

type cloudDownloadClient interface {
	CloudURLAdder
	SubmitURL(context.Context, string, string, string) error
	AddURLs(context.Context, string, []string, string) ([]p115.CloudURLAddResult, error)
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
	Accounts    *P115Accounts
	Client      cloudDownloadClient
	Coordinator *CloudDownloadCoordinator
}

func (s *CloudDownloadService) snapshot(config CloudDownloadConfig) (P115Account, error) {
	if s == nil || s.Accounts == nil || s.Client == nil || s.Coordinator == nil {
		return P115Account{}, generationInputError("云下载服务尚未初始化")
	}
	account, err := s.Accounts.CloudDownloadPrimary(strings.TrimSpace(config.AccountID))
	if err != nil {
		return P115Account{}, err
	}
	if err := validateP115TargetDirectory(strings.TrimSpace(config.TargetCID), strings.TrimSpace(config.TargetPath)); err != nil {
		return P115Account{}, generationInputError(err.Error())
	}
	return account, nil
}

// Submit 直接新增普通云下载任务，只使用提交接口本次返回的结果。
func (s *CloudDownloadService) Submit(ctx context.Context, config CloudDownloadConfig, rawURL string) error {
	account, err := s.snapshot(config)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, CloudDownloadTimeout)
	defer cancel()
	resource, err := p115.ParseCloudResource(rawURL)
	if err != nil {
		return err
	}
	release, err := s.acquireDirectResources(ctx, account, []p115.CloudResource{resource})
	if err != nil {
		return err
	}
	defer release()
	return s.addDirectURL(ctx, account.Cookie, rawURL, strings.TrimSpace(config.TargetCID))
}

func (s *CloudDownloadService) acquireDirectResources(ctx context.Context, account P115Account, resources []p115.CloudResource) (func(), error) {
	release, err := s.Coordinator.acquireResources(ctx, p115.CookieUserID(account.Cookie), resources)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, &cloudDirectSubmitError{cause: err, message: "提交取消或等待超时，请求未发送"}
		}
		return nil, err
	}
	if err := s.Accounts.ValidatePrimary(account.ID); err != nil {
		release()
		return nil, &CloudDownloadAccountError{cause: err}
	}
	return release, nil
}

// 调用方已预留同 UID 和资源名额。旧任务核对只留在 submitSnapshot。
func (s *CloudDownloadService) addDirectURL(ctx context.Context, cookie, rawURL, cid string) error {
	if err := s.Client.SubmitURL(ctx, cookie, rawURL, cid); err != nil {
		return directCloudSubmitError(err)
	}
	return nil
}

func (s *CloudDownloadService) submitSnapshot(ctx context.Context, config CloudDownloadConfig, account P115Account, rawURL string) (CloudDownloadReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, CloudDownloadTimeout)
	defer cancel()
	if err := s.Accounts.ValidatePrimary(account.ID); err != nil {
		return CloudDownloadReceipt{}, err
	}
	resource, err := p115.ParseCloudResource(rawURL)
	if err != nil {
		return CloudDownloadReceipt{}, err
	}
	receipt := CloudDownloadReceipt{AccountName: account.Name, TargetPath: strings.TrimSpace(config.TargetPath)}
	// Use the pinned credential's actual UID, independent of configuration ID,
	// target directory or Cookie device suffix. Automatic BT v1 uses this gate too.
	release, err := s.Coordinator.Acquire(ctx, p115.CookieUserID(account.Cookie), resource.Protocol, resource.Hash)
	if err != nil {
		return receipt, err
	}
	defer release()
	result, err := submitCloudResource(ctx, s.Client, account.Cookie, resource, rawURL, strings.TrimSpace(config.TargetCID), func() error {
		return s.Accounts.ValidatePrimary(account.ID)
	})
	result.AccountName, result.TargetPath = receipt.AccountName, receipt.TargetPath
	return result, err
}

// Callers hold the UID/resource gate and pin credentials. RSS also commits its
// durable submit intent before the first write; uncertain writes are reconciled
// by its existing runner, never replayed through this helper.
func submitCloudResource(ctx context.Context, client CloudURLAdder, cookie string, resource p115.CloudResource, rawURL, cid string, beforeWrite func() error) (CloudDownloadReceipt, error) {
	var receipt CloudDownloadReceipt
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	task, found, err := client.LookupTask(ctx, cookie, resource)
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
	}
	if beforeWrite != nil {
		if err := beforeWrite(); err != nil {
			return receipt, err
		}
	}
	if found {
		if err := ctx.Err(); err != nil {
			return receipt, err
		}
		if err := client.DeleteTaskKeepFiles(ctx, cookie, resource.Hash); err != nil {
			return receipt, fmt.Errorf("移除原云下载任务记录: %w", err)
		}
		receipt.RemovedOldTask = true
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	if found && beforeWrite != nil {
		if err := beforeWrite(); err != nil {
			return receipt, err
		}
	}
	raw, err := client.AddURL(ctx, cookie, rawURL, cid)
	if err != nil {
		return receipt, fmt.Errorf("提交 115 云下载: %w", err)
	}
	receipt.Task, _ = p115.ParseCloudTask(raw)
	return receipt, nil
}

// CloudDownloadAccountError 表示写入前未能确认主账号可用或仍归属该配置。
type CloudDownloadAccountError struct{ cause error }

func (e *CloudDownloadAccountError) Error() string { return e.cause.Error() }
func (e *CloudDownloadAccountError) Unwrap() error { return e.cause }

// CloudDownloadPrimary 返回当前主账号的有效凭据快照，并核对凭据中的 UID。
func (s *P115Accounts) CloudDownloadPrimary(expected string) (P115Account, error) {
	account, err := s.Primary(expected)
	if err != nil {
		return P115Account{}, &CloudDownloadAccountError{cause: err}
	}
	uid := p115.CookieUserID(account.Cookie)
	if uid == "" || uid != p115.CookieUserID("UID="+account.UID) {
		return P115Account{}, &CloudDownloadAccountError{cause: generationInputError("无法确认所选 115 账号 UID，请重新登录")}
	}
	return account, nil
}

// 网络中断不能证明上游未执行操作，提示用户核对而非自动重试。
func CloudDownloadUncertain(err error) bool {
	var direct *cloudDirectSubmitError
	if errors.As(err, &direct) {
		return direct.uncertain
	}
	var deferred *p115.CloudRequestDeferredError
	if errors.As(err, &deferred) {
		return false
	}
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, p115.ErrCloudSubmissionUncertain) || errors.As(err, &network)
}

// 直接提交对外只显示安全错误类别，底层原因仅用于分类。
type cloudDirectSubmitError struct {
	cause     error
	message   string
	uncertain bool
}

func (e *cloudDirectSubmitError) Error() string { return e.message }
func (e *cloudDirectSubmitError) Unwrap() error { return e.cause }

func directCloudSubmitError(err error) error {
	var upstream *p115.HTTPError
	var syntax *json.SyntaxError
	var invalidType *json.UnmarshalTypeError
	var deferred *p115.CloudRequestDeferredError
	if errors.As(err, &deferred) {
		return &cloudDirectSubmitError{cause: err, message: "115 请求已限速延后，本次请求未发送"}
	}
	isHTTP := errors.As(err, &upstream)
	if CloudDownloadUncertain(err) || isHTTP && upstream.Status >= http.StatusInternalServerError || errors.As(err, &syntax) || errors.As(err, &invalidType) {
		return &cloudDirectSubmitError{cause: err, message: "115 提交结果未确认", uncertain: true}
	}
	if isHTTP {
		return &cloudDirectSubmitError{cause: err, message: fmt.Sprintf("115 拒绝提交（HTTP %d）", upstream.Status)}
	}
	var rejection *p115.CloudSubmissionRejection
	if errors.As(err, &rejection) {
		return &cloudDirectSubmitError{cause: err, message: rejection.Error()}
	}
	return &cloudDirectSubmitError{cause: err, message: "115 拒绝提交"}
}

func CloudDownloadErrorMessage(err error) string {
	var direct *cloudDirectSubmitError
	switch {
	case errors.Is(err, p115.ErrCloudInput):
		return "链接格式无效，请发送完整的 magnet 或 ED2K 链接"
	case errors.Is(err, ErrCloudTaskRunning):
		return "相同任务仍在 115 中运行或等待，未删除或重新提交"
	case errors.Is(err, ErrCloudTaskUnclear):
		return "无法安全确认旧任务状态，未执行删除"
	case errors.Is(err, ErrCloudTaskBusy):
		return ErrCloudTaskBusy.Error()
	case errors.As(err, &direct):
		return direct.message
	case CloudDownloadUncertain(err):
		return "连接 115 超时或中断，处理结果尚未确认，请先检查 115 任务列表再决定是否重试"
	case errors.Is(err, ErrGenerationInput):
		return strings.TrimPrefix(err.Error(), ErrGenerationInput.Error()+"：")
	default:
		return err.Error()
	}
}
