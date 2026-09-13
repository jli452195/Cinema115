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
	SaveShareTask(context.Context, model.ShareTask) error
	DueShareTasks(context.Context, time.Time) ([]model.ShareTask, error)
}

type shareTaskRequest struct {
	Merged      bool               `json:"merged,omitempty"`
	OwnShareCID string             `json:"ownShareCID,omitempty"`
	OwnRootIDs  []string           `json:"ownRootIDs,omitempty"`
	Input       GenerateInput      `json:"input"`
	Settings    AutomationSettings `json:"settings"`
	CookieHash  string             `json:"cookieHash"`
}

// ShareTasks 为网页和 Bot 保存任务结果，审核等待不占用原请求连接。
type ShareTasks struct {
	Generator MediaGenerator
	Store     ShareTaskStore
	Settings  *AutomationStore
	Cookie    func() string
}

func cookieHash(cookie string) string {
	sum := sha256.Sum256([]byte(cookie))
	return hex.EncodeToString(sum[:])
}

func (s *ShareTasks) Generate(ctx context.Context, input GenerateInput) (*GenerationResult, error) {
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
	settings := s.Settings.Value()
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
		if mode == "reshare" && settings.FixedSharePassword != "" && settings.FixedSharePassword == share.Password {
			mode = "direct"
		}
	}
	source, _ := ctx.Value(generationSourceKey{}).(string)
	if source != "Telegram" {
		source = "网页"
	}
	now := time.Now().UTC()
	task := model.ShareTask{ID: hex.EncodeToString(nonce[:]), Source: source, SourceURL: link, Mode: mode, Status: "running", CreatedAt: now, UpdatedAt: now, MaxRetries: settings.ReviewRetryMax, RetryMinutes: settings.ReviewRetryMinutes}
	req := shareTaskRequest{Input: input, Settings: settings, CookieHash: cookieHash(s.Cookie())}
	if err := s.save(ctx, &task, req); err != nil {
		return nil, shareTaskRequest{}, err
	}
	return &task, req, nil
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
	result, err := s.Generator.Generate(WithGenerationSource(ctx, task.Source), req.Input)
	return s.finish(ctx, task, req, result, err, time.Since(started))
}

// 单条和合并执行共用审核重试判定及最终状态落盘。
func (s *ShareTasks) finish(ctx context.Context, task *model.ShareTask, req shareTaskRequest, result *GenerationResult, err error, elapsed time.Duration) (*GenerationResult, error) {
	task.Elapsed += elapsed.Seconds()
	task.NextRetryAt = nil
	if result != nil {
		task.Written, task.Total = result.Written, len(result.Files)
		if result.ReshareURL != "" {
			task.ShareURL = result.ReshareURL
		}
	}
	task.Status, task.Error = "success", ""
	if err != nil {
		task.Status, task.Error = "failed", generationErrorDetail(err, req.Input)
		if errors.Is(err, p115.ErrShareUnderReview) {
			task.Status = "review"
			// 只有只读生成或明确尚未转存的原分享可按原请求重试。
			safe := task.Mode == "direct" || req.Input.Mode == "direct" || errors.Is(err, errSourceShareReview)
			if result != nil && result.ReshareURL != "" && (!req.Merged || req.OwnShareCID != "" || len(req.OwnRootIDs) > 0) {
				// 新分享已经存在时复用该链接，不再转存或创建分享，也不自动删除临时目录。
				req.Input.URL, req.Input.Password, req.Input.CID, req.Input.Mode = result.ReshareURL, "", "0", "direct"
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
		}
	}
	// 请求取消后也保存最终状态，写入失败不会继续下一次云端操作。
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if saveErr := s.save(saveCtx, task, req); saveErr != nil {
		task.Status = "running"
		return result, errors.Join(err, saveErr)
	}
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
		if err := ctx.Err(); err != nil {
			return err
		}
		var req shareTaskRequest
		if err := json.Unmarshal(task.Request, &req); err != nil {
			return fmt.Errorf("解析审核重试请求: %w", err)
		}
		if !s.Settings.Value().ReviewRetryEnabled || req.CookieHash != cookieHash(s.Cookie()) {
			task.Status, task.NextRetryAt = "cancelled", nil
			task.Error = "审核重试已关闭，或 115 登录已变化，本任务停止自动重试"
			if err := s.save(ctx, &task, req); err != nil {
				return err
			}
			continue
		}
		task.Status, task.NextRetryAt = "running", nil
		task.Retries++
		if err := s.save(ctx, &task, req); err != nil {
			return err
		}
		retryCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		_, runErr := s.execute(retryCtx, &task, req)
		cancel()
		// 业务失败已保存在任务中；若最终状态仍未落盘，结束工作循环防止丢失检查点。
		if runErr != nil && task.Status == "running" {
			return runErr
		}
	}
	return nil
}
