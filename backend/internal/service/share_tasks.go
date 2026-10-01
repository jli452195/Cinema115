package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

type automationContextKey struct{}

var errSourceShareReview = errors.New("原分享审核中，尚未执行转存")

type ShareTaskStore interface {
	GetShareTask(context.Context, string) (model.ShareTask, error)
	CompareShareTask(context.Context, model.ShareTask, model.ShareTask) (bool, error)
	SaveShareTask(context.Context, model.ShareTask) error
	DueShareTasks(context.Context, time.Time) ([]model.ShareTask, error)
}

type shareTaskRequest struct {
	DirectoryLogID       string             `json:"directoryLogId,omitempty"`
	DirectoryFingerprint string             `json:"directoryFingerprint,omitempty"`
	DirectoryID          string             `json:"directoryId,omitempty"`
	DirectoryEnded       bool               `json:"directoryEnded,omitempty"`
	RetrySafe            bool               `json:"retrySafe,omitempty"`
	Merged               bool               `json:"merged,omitempty"`
	OwnShareCID          string             `json:"ownShareCID,omitempty"`
	OwnRootIDs           []string           `json:"ownRootIDs,omitempty"`
	Input                GenerateInput      `json:"input"`
	Settings             AutomationSettings `json:"settings"`
	CookieHash           string             `json:"cookieHash"`
}

// ShareTasks 为网页和 Bot 保存任务结果，审核等待不占用原请求连接。
type ShareTasks struct {
	DirectoryPrepare func(context.Context, GenerateInput, string, string) error
	RetryWake        chan struct{}
	Generator        MediaGenerator
	Store            ShareTaskStore
	Settings         *AutomationStore
	Cookie           func() string
	AccountCookie    func(string) string
	Notifier         ShareSuccessNotifier
	Logs             *GenerationLogs
}

func cookieHash(cookie string) string {
	sum := sha256.Sum256([]byte(cookie))
	return hex.EncodeToString(sum[:])
}

func (s *ShareTasks) Generate(ctx context.Context, input GenerateInput) (*GenerationResult, error) {
	ctx = s.pinAccount(ctx)
	task, req, err := s.prepareTask(ctx, input)
	if err != nil {
		return nil, err
	}
	return s.execute(ctx, task, req)
}

func (s *ShareTasks) prepareTask(ctx context.Context, input GenerateInput) (*model.ShareTask, shareTaskRequest, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, shareTaskRequest{}, fmt.Errorf("创建分享任务: %w", err)
	}
	return s.prepareTaskRecord(ctx, input, hex.EncodeToString(nonce[:]), "running")
}

func (s *ShareTasks) prepareTaskRecord(ctx context.Context, input GenerateInput, id, status string) (*model.ShareTask, shareTaskRequest, error) {
	settings := s.Settings.ShareValue()
	if saved, ok := ctx.Value(automationContextKey{}).(AutomationSettings); ok {
		settings = saved
	}
	mode := input.Mode
	if mode == "" {
		mode = "direct"
		if settings.ReshareEnabled {
			mode = "reshare"
		}
	}
	if mode != "direct" && mode != "reshare" {
		return nil, shareTaskRequest{}, generationInputError("分享模式无效")
	}
	input.Mode = mode
	link := ""
	if share, err := p115.ParseShareURL(input.URL); err == nil {
		if input.Password != "" {
			share.Password = strings.TrimSpace(input.Password)
		}
		link = "https://115cdn.com/s/" + url.PathEscape(share.Code)
		if share.Password != "" {
			link += "?password=" + url.QueryEscape(share.Password)
		}
	}
	source, _ := ctx.Value(generationSourceKey{}).(string)
	if source != "Telegram" && source != "订阅" && source != "追更" && source != "目录分享" {
		source = "网页"
	}
	now := time.Now().UTC()
	task := model.ShareTask{ID: id, Source: source, SourceURL: link, Mode: mode, Status: status, CreatedAt: now, UpdatedAt: now, MaxRetries: settings.ReviewRetryMax, RetryMinutes: settings.ReviewRetryMinutes}
	req := shareTaskRequest{Input: input, Settings: settings, CookieHash: cookieHash(generationCookie(ctx, s.Cookie))}
	req.DirectoryLogID, _ = ctx.Value(directoryGenerationKey{}).(string)
	req.DirectoryFingerprint, _ = ctx.Value(directoryFingerprintKey{}).(string)
	req.DirectoryID, _ = ctx.Value(directoryIDKey{}).(string)
	if err := s.save(ctx, &task, req); err != nil {
		return nil, shareTaskRequest{}, err
	}
	return &task, req, nil
}

// 生成设置和账号一次取快照，切换配置不会使同一任务的凭据与选项不一致。
func (s *ShareTasks) pinAccount(ctx context.Context) context.Context {
	settings, ok := ctx.Value(automationContextKey{}).(AutomationSettings)
	if !ok {
		settings = s.Settings.ShareValue()
	}
	current := s.Cookie
	if s.AccountCookie != nil {
		current = func() string { return s.AccountCookie(settings.AccountID) }
	}
	ctx = context.WithValue(ctx, automationContextKey{}, settings)
	return context.WithValue(ctx, generationCookieKey{}, generationCookie(ctx, current))
}

func (s *ShareTasks) save(ctx context.Context, task *model.ShareTask, req shareTaskRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("编码分享请求: %w", err)
	}
	task.Request, task.UpdatedAt = data, time.Now().UTC()
	return s.Store.SaveShareTask(ctx, *task)
}

func (s *ShareTasks) execute(ctx context.Context, task *model.ShareTask, req shareTaskRequest) (*GenerationResult, error) {
	started := time.Now()
	ctx = context.WithValue(ctx, automationContextKey{}, req.Settings)
	if len(req.OwnRootIDs) > 0 {
		ctx = context.WithValue(ctx, generationRootIDsKey{}, req.OwnRootIDs)
	}
	if req.DirectoryLogID != "" {
		ctx = context.WithValue(ctx, directoryGenerationKey{}, req.DirectoryLogID)
		cookie := generationCookie(ctx, s.Cookie)
		ctx = context.WithValue(ctx, generationLogKey{}, generationLogger{store: s.Logs, task: req.DirectoryLogID, source: "目录分享", redact: func(message string) string { return monitorLogDetail(message, cookie) }})
		if s.DirectoryPrepare != nil {
			if err := s.DirectoryPrepare(ctx, req.Input, req.DirectoryFingerprint, req.DirectoryID); err != nil {
				return s.finish(ctx, task, req, nil, err, time.Since(started))
			}
		}
	}
	result, err := s.Generator.Generate(WithGenerationSource(ctx, task.Source), req.Input)
	if req.DirectoryLogID != "" && err == nil && (result == nil || result.Written == 0 || result.Failed != 0 || result.MetadataFailed != 0) {
		err = errors.New("STRM 或元数据未全部生成成功，目录批次保留，重试会复用当前分享")
	}
	return s.finish(ctx, task, req, result, err, time.Since(started))
}

// 单条和合并执行共用审核重试判定及最终状态落盘。
func (s *ShareTasks) finish(ctx context.Context, task *model.ShareTask, req shareTaskRequest, result *GenerationResult, err error, elapsed time.Duration) (*GenerationResult, error) {
	if req.DirectoryLogID != "" && errors.Is(err, ErrNoMedia) {
		err = fmt.Errorf("%w；当前重试沿用本批规则：视频后缀=%s，最小=%d MB，递归=%t。文件已变动或需要应用新设置时，请结束本次目录任务再重新分享", err, req.Input.VideoExtensions, req.Input.MinSizeMB, req.Input.Recursive)
	}
	if result != nil && result.Mode == "direct" {
		task.Mode, req.Input.Mode, req.Merged = "direct", "direct", false
	}
	task.Elapsed += elapsed.Seconds()
	task.NextRetryAt = nil
	if result != nil {
		task.Written, task.Total = result.Written, len(result.Files)
		if result.ReshareURL != "" {
			task.ShareURL = result.ReshareURL
		}
	}
	task.Status, task.Error = "success", ""
	req.RetrySafe = false
	if err != nil {
		req.RetrySafe = req.Input.Mode == "direct" || errors.Is(err, errSourceShareReview)
		task.Status, task.Error = "failed", generationErrorDetail(err, req.Input)
		if errors.Is(err, p115.ErrShareUnderReview) || (req.DirectoryLogID != "" && errors.Is(err, p115.ErrShareSnapshotPending)) {
			task.Status = "review"
			// 只有只读生成或明确尚未转存的原分享可按原请求重试。
			safe := task.Mode == "direct" || req.Input.Mode == "direct" || errors.Is(err, errSourceShareReview)
			if result != nil && result.ReshareURL != "" && (!req.Merged || req.OwnShareCID != "" || len(req.OwnRootIDs) > 0) {
				// 新分享已经存在时复用该链接，不再转存或创建分享，也不自动删除临时目录。
				req.Input.URL, req.Input.Password, req.Input.CID, req.Input.Mode = result.ReshareURL, "", "0", "direct"
				req.Input.SharePath = ""
				if req.OwnShareCID != "" {
					req.Input.CID = req.OwnShareCID
				}
				task.Note = "复用已创建的新分享重试；临时目录保留，请在转存任务记录中核对后处理。"
				safe = true
			}
			if safe && req.Settings.ReviewRetryEnabled && s.Settings.Value().ReviewRetryEnabled && task.Retries < task.MaxRetries {
				next := time.Now().UTC().Add(time.Duration(task.RetryMinutes) * time.Minute)
				task.Status, task.NextRetryAt = "waiting_review", &next
				err = fmt.Errorf("%w；已安排审核重试，%d 分钟后执行（%d/%d）", err, task.RetryMinutes, task.Retries, task.MaxRetries)
			} else if task.Retries >= task.MaxRetries {
				task.Error += "；审核重试次数已用尽"
			}
			req.RetrySafe = safe
		}
	}
	// 请求取消后也保存最终状态，写入失败不会继续下一次云端操作。
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if saveErr := s.save(saveCtx, task, req); saveErr != nil {
		task.Status = "running"
		return result, errors.Join(err, saveErr)
	}
	s.notifySuccess(saveCtx, task.ID, req.Input, result, err)
	return result, err
}

// Run 由主进程管理生命周期；每个重试仍有独立的五分钟上限。
func (s *ShareTasks) Run(ctx context.Context) error {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.retryDue(ctx, time.Now().UTC()); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-s.RetryWake:
		}
	}
}

func (s *ShareTasks) retryDue(ctx context.Context, now time.Time) error {
	if !s.Settings.Value().ReviewRetryEnabled {
		now = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	tasks, err := s.Store.DueShareTasks(ctx, now)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		before := task
		manual := task.Status == "retry_queued"
		initial := task.Status == "queued"
		if err := ctx.Err(); err != nil {
			return err
		}
		var req shareTaskRequest
		if err := json.Unmarshal(task.Request, &req); err != nil {
			return fmt.Errorf("解析审核重试请求: %w", err)
		}
		if req.DirectoryEnded {
			continue
		}
		cookie := ""
		if s.AccountCookie != nil {
			cookie = s.AccountCookie(req.Settings.AccountID)
		} else if s.Cookie != nil {
			cookie = s.Cookie()
		}
		if (!manual && !initial && !s.Settings.Value().ReviewRetryEnabled) || (s.AccountCookie != nil && req.Settings.AccountID == "") || strings.TrimSpace(cookie) == "" || req.CookieHash != cookieHash(cookie) {
			task.Status, task.NextRetryAt = "cancelled", nil
			task.Error = "审核重试已关闭，或原任务的 115 账号及登录凭据不可用，本次重试已停止"
			task.UpdatedAt = time.Now().UTC()
			if _, err := s.Store.CompareShareTask(ctx, before, task); err != nil {
				return err
			}
			continue
		}
		task.Status, task.NextRetryAt = "running", nil
		if manual {
			task.ManualRetries++
		} else if !initial {
			task.Retries++
		}
		req.RetrySafe = false
		task.Request, err = json.Marshal(req)
		if err != nil {
			return fmt.Errorf("编码重试请求: %w", err)
		}
		task.UpdatedAt = time.Now().UTC()
		claimed, err := s.Store.CompareShareTask(ctx, before, task)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		retryCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		_, runErr := s.execute(context.WithValue(retryCtx, generationCookieKey{}, cookie), &task, req)
		cancel()
		// 业务失败已保存在任务中；若最终状态仍未落盘，结束工作循环防止丢失检查点。
		if runErr != nil && task.Status == "running" {
			return runErr
		}
	}
	return nil
}
