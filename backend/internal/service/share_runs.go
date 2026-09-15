package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

var ErrShareRunMissing = errors.New("后台任务不存在或服务已重启，请查看分享任务日志，勿直接重复转存")
var ErrShareRunBusy = errors.New("后台生成队列已满，请稍后重试")
var shareRunIDPattern = regexp.MustCompile("^[a-f0-9]{32}$")

// ShareRunResult 仅保留页面需要的结果，避免轮询传输完整文件清单。
type ShareRunResult struct {
	Count           int    `json:"count"`
	MetadataCount   int    `json:"metadataCount"`
	Directory       string `json:"directory"`
	ReshareURL      string `json:"reshareUrl,omitempty"`
	TempDirectoryID string `json:"tempDirectoryID,omitempty"`
	Cleaned         bool   `json:"cleaned"`
}
type ShareRunItem struct {
	TaskID  string          `json:"taskId"`
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Error   string          `json:"error,omitempty"`
	Result  *ShareRunResult `json:"result,omitempty"`
}
type ShareRunView struct {
	ID     string         `json:"id"`
	Status string         `json:"status"`
	Items  []ShareRunItem `json:"items"`
}
type shareRun struct {
	view     ShareRunView
	digest   [32]byte
	ctx      context.Context
	cancel   context.CancelFunc
	tasks    []*model.ShareTask
	requests []shareTaskRequest
	finished time.Time
}

// ShareRuns 将浏览器请求与执行生命周期分离。队列串行执行，不创建无主协程。
type ShareRuns struct {
	mu    sync.Mutex
	root  context.Context
	tasks *ShareTasks
	runs  map[string]*shareRun
	queue []string
	wake  chan struct{}
}

func NewShareRuns(root context.Context, tasks *ShareTasks) *ShareRuns {
	return &ShareRuns{root: root, tasks: tasks, runs: make(map[string]*shareRun), wake: make(chan struct{}, 1)}
}

func cloneShareRun(view ShareRunView) ShareRunView {
	out := view
	out.Items = append([]ShareRunItem(nil), view.Items...)
	for i := range out.Items {
		if out.Items[i].Result != nil {
			result := *out.Items[i].Result
			out.Items[i].Result = &result
		}
	}
	return out
}

// Submit 在返回接收成功前保存全部子任务，并固定账号凭据、模式和设置。
// 客户端生成的请求编号用于处理响应丢失，重交同编号不会重复转存。
func (s *ShareRuns) Submit(ctx context.Context, id string, inputs []GenerateInput) (ShareRunView, error) {
	if !shareRunIDPattern.MatchString(id) || len(inputs) < 1 || len(inputs) > 100 {
		return ShareRunView{}, generationInputError("任务编号无效或链接数量不在 1～100 范围内")
	}
	data, err := json.Marshal(inputs)
	if err != nil {
		return ShareRunView{}, fmt.Errorf("编码后台任务: %w", err)
	}
	digest := sha256.Sum256(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.runs[id]; existing != nil {
		if existing.digest != digest {
			return ShareRunView{}, generationInputError("同一任务编号不能提交不同链接或生成参数")
		}
		return cloneShareRun(existing.view), nil
	}
	if err := ctx.Err(); err != nil {
		return ShareRunView{}, err
	}
	if err := s.root.Err(); err != nil {
		return ShareRunView{}, err
	}
	active := 0
	for key, run := range s.runs {
		if run.finished.IsZero() {
			active++
		} else if time.Since(run.finished) > 24*time.Hour {
			delete(s.runs, key)
		}
	}
	if active >= 20 {
		return ShareRunView{}, ErrShareRunBusy
	}
	// 有界保留最近完成任务，轮询与重开弹窗无需保留文件列表或登录凭据。
	for len(s.runs) >= 100 {
		oldestKey := ""
		var oldest time.Time
		for key, run := range s.runs {
			if !run.finished.IsZero() && (oldestKey == "" || run.finished.Before(oldest)) {
				oldestKey, oldest = key, run.finished
			}
		}
		if oldestKey == "" {
			return ShareRunView{}, ErrShareRunBusy
		}
		delete(s.runs, oldestKey)
	}
	pinned := s.tasks.pinAccount(WithGenerationSource(s.root, "网页"))
	if generationCookie(pinned, s.tasks.Cookie) == "" {
		return ShareRunView{}, generationInputError("请在分享基础设置中选择已登录的 115 账号配置")
	}
	for _, input := range inputs {
		if err := ValidateGenerationOptions(s.tasks.Settings.root, input); err != nil {
			return ShareRunView{}, err
		}
		if _, err := p115.ParseShareURL(input.URL); err != nil {
			return ShareRunView{}, generationInputError(err.Error())
		}
		if input.Mode != "" && input.Mode != "direct" && input.Mode != "reshare" {
			return ShareRunView{}, generationInputError("分享模式无效")
		}
	}
	runCtx, cancel := context.WithCancel(pinned)
	run := &shareRun{view: ShareRunView{ID: id, Status: "queued", Items: make([]ShareRunItem, len(inputs))}, digest: digest, ctx: runCtx, cancel: cancel, tasks: make([]*model.ShareTask, len(inputs)), requests: make([]shareTaskRequest, len(inputs))}
	// 入队只写本地数据库，浏览器断开不会撤销已经接收的任务。
	saveCtx, saveCancel := context.WithTimeout(runCtx, 10*time.Second)
	defer saveCancel()
	for i, input := range inputs {
		task, req, err := s.tasks.prepareTask(saveCtx, input)
		if err != nil {
			cancel()
			for j := 0; j < i; j++ {
				_, saveErr := s.tasks.finish(runCtx, run.tasks[j], run.requests[j], nil, err, 0)
				err = errors.Join(err, saveErr)
			}
			return ShareRunView{}, err
		}
		run.tasks[i], run.requests[i] = task, req
		run.view.Items[i] = ShareRunItem{TaskID: task.ID, Status: "queued", Message: "已提交后台，等待执行"}
	}
	s.runs[id] = run
	s.queue = append(s.queue, id)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return cloneShareRun(run.view), nil
}

func (s *ShareRuns) Get(id string) (ShareRunView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[id]
	if run == nil {
		return ShareRunView{}, ErrShareRunMissing
	}
	return cloneShareRun(run.view), nil
}
func (s *ShareRuns) Cancel(id string) (ShareRunView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[id]
	if run == nil {
		return ShareRunView{}, ErrShareRunMissing
	}
	if run.finished.IsZero() {
		run.view.Status = "cancelling"
		run.cancel()
	}
	return cloneShareRun(run.view), nil
}

// Run 由主进程等待退出；停机时取消执行并给未执行的子任务保存结束状态。
func (s *ShareRuns) Run() {
	for {
		s.mu.Lock()
		var run *shareRun
		if len(s.queue) > 0 {
			run = s.runs[s.queue[0]]
			s.queue = s.queue[1:]
		} else if s.root.Err() != nil {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		if run != nil {
			s.execute(run)
			continue
		}
		select {
		case <-s.root.Done():
			// 等待可能正在入队的提交释放锁，再处理其已落盘子任务。
		case <-s.wake:
		}
	}
}

func (s *ShareRuns) execute(run *shareRun) {
	s.mu.Lock()
	if run.ctx.Err() == nil {
		run.view.Status = "running"
	}
	s.mu.Unlock()
	for i, task := range run.tasks {
		var result *GenerationResult
		var err error
		if run.ctx.Err() != nil {
			result, err = s.tasks.finish(run.ctx, task, run.requests[i], nil, run.ctx.Err(), 0)
		} else {
			s.mu.Lock()
			run.view.Items[i].Status = "running"
			run.view.Items[i].Message = "正在后台生成 STRM"
			s.mu.Unlock()
			itemCtx, cancel := context.WithTimeout(run.ctx, 5*time.Minute)
			itemCtx = WithGenerationProgress(itemCtx, func(progress GenerationProgress) {
				s.mu.Lock()
				run.view.Items[i].Message = progress.Message
				s.mu.Unlock()
			})
			result, err = s.tasks.execute(itemCtx, task, run.requests[i])
			cancel()
		}
		s.mu.Lock()
		item := &run.view.Items[i]
		item.Status, item.Message = "success", "生成成功"
		if result != nil {
			item.Result = &ShareRunResult{Count: result.Written, MetadataCount: result.MetadataWritten, Directory: result.Directory, ReshareURL: result.ReshareURL, TempDirectoryID: result.TempDirectoryID, Cleaned: result.Cleaned}
		}
		if err != nil {
			item.Status = "failed"
			if errors.Is(err, context.Canceled) {
				item.Status = "cancelled"
			}
			item.Error = generationErrorDetail(err, run.requests[i].Input)
			item.Message = item.Error
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	run.view.Status = "success"
	for _, item := range run.view.Items {
		if item.Status != "success" {
			run.view.Status = "failed"
			break
		}
	}
	if run.ctx.Err() != nil {
		run.view.Status = "cancelled"
	}
	run.finished = time.Now()
	run.cancel()
	// 结束后释放内存中的 Cookie 和生成设置，只保留不含凭据的页面状态。
	run.ctx, run.cancel, run.requests, run.tasks = nil, nil, nil, nil
	s.mu.Unlock()
}
