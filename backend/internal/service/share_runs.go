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
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
	Cached          bool   `json:"cached,omitempty"`
	Mode            string `json:"mode,omitempty"`
	Count           int    `json:"count"`
	Failed          int    `json:"failed,omitempty"`
	Total           int    `json:"total,omitempty"`
	MetadataCount   int    `json:"metadataCount"`
	MetadataFailed  int    `json:"metadataFailed,omitempty"`
	MetadataTotal   int    `json:"metadataTotal,omitempty"`
	ConflictRenamed int    `json:"conflictRenamed,omitempty"`
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
	view        ShareRunView
	digest      [32]byte
	fingerprint string
	cacheKey    string
	ctx         context.Context
	cancel      context.CancelFunc
	tasks       []*model.ShareTask
	requests    []shareTaskRequest
	readyAt     time.Time
	finished    time.Time
	done        chan struct{}
}

type shareRunCacheStore interface {
	LoadShareRunCache(context.Context, string) (string, bool, error)
	SaveShareRunCache(context.Context, string, string) error
	DeleteShareRunCache(context.Context, string) error
}

type shareRunCacheRecord struct {
	Items []ShareRunItem `json:"items"`
	Paths []string       `json:"paths"`
}

// ShareRuns 将浏览器请求与执行生命周期分离。队列串行执行，不创建无主协程。
type ShareRuns struct {
	mu                  sync.Mutex
	root                context.Context
	tasks               *ShareTasks
	runs                map[string]*shareRun
	activeByFingerprint map[string]string
	queue               []string
	wake                chan struct{}
}

func NewShareRuns(root context.Context, tasks *ShareTasks) *ShareRuns {
	return &ShareRuns{root: root, tasks: tasks, runs: make(map[string]*shareRun), activeByFingerprint: make(map[string]string), wake: make(chan struct{}, 1)}
}

type shareRunFingerprintInput struct {
	AccountID          string `json:"accountId"`
	CredentialHash     string `json:"credentialHash"`
	ShareURL           string `json:"shareUrl"`
	CID                string `json:"cid"`
	Mode               string `json:"mode"`
	OutputDir          string `json:"outputDir"`
	BaseURL            string `json:"baseUrl"`
	VideoExtensions    string `json:"videoExtensions"`
	MetadataExtensions string `json:"metadataExtensions"`
	MinSizeMB          int64  `json:"minSizeMB"`
	Recursive          bool   `json:"recursive"`
	ParentCID          string `json:"parentCID"`
	FixedSharePassword string `json:"fixedSharePassword"`
	ReviewRetryEnabled bool   `json:"reviewRetryEnabled"`
	ReviewRetryMinutes int    `json:"reviewRetryMinutes"`
	ReviewRetryMax     int    `json:"reviewRetryMax"`
}

func normalizedExtensionList(raw, fallback string) string {
	if strings.TrimSpace(raw) == "" {
		raw = fallback
	}
	seen := make(map[string]bool)
	for _, item := range strings.Split(strings.ReplaceAll(raw, "，", ","), ",") {
		item = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(item), "."))
		if item != "" {
			seen[item] = true
		}
	}
	items := make([]string, 0, len(seen))
	for item := range seen {
		items = append(items, item)
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

// shareRunFingerprint 同时用于活动队列和成功快照；调用方决定是否传入 Cookie 摘要。
func shareRunFingerprint(settings AutomationSettings, credentialHash string, inputs []GenerateInput) (string, error) {
	items := make([]shareRunFingerprintInput, len(inputs))
	for i, input := range inputs {
		share, err := p115.ParseShareURL(input.URL)
		if err != nil {
			return "", generationInputError(err.Error())
		}
		password := strings.TrimSpace(input.Password)
		if password == "" {
			password = share.Password
		}
		canonical := &url.URL{Scheme: "https", Host: "115cdn.com", Path: "/s/" + share.Code}
		if password != "" {
			query := url.Values{}
			query.Set("password", password)
			canonical.RawQuery = query.Encode()
		}
		mode := input.Mode
		if mode == "" {
			mode = "direct"
			if settings.ReshareEnabled {
				mode = "reshare"
			}
		}
		cid := strings.TrimSpace(input.CID)
		if cid == "" {
			cid = "0"
		}
		if value, err := strconv.ParseUint(cid, 10, 64); err == nil {
			cid = strconv.FormatUint(value, 10)
		}
		parentCID, fixedPassword := "", ""
		if mode == "reshare" {
			parentCID, fixedPassword = settings.ParentCID, settings.FixedSharePassword
		}
		items[i] = shareRunFingerprintInput{
			AccountID: settings.AccountID, CredentialHash: credentialHash, ShareURL: canonical.String(), CID: cid,
			Mode: mode, OutputDir: strings.TrimSpace(input.OutputDir), BaseURL: strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"),
			VideoExtensions: normalizedExtensionList(input.VideoExtensions, DefaultVideoExtensions), MetadataExtensions: normalizedExtensionList(input.MetadataExtensions, ""),
			MinSizeMB: input.MinSizeMB, Recursive: input.Recursive, ParentCID: parentCID,
			FixedSharePassword: fixedPassword, ReviewRetryEnabled: settings.ReviewRetryEnabled,
			ReviewRetryMinutes: settings.ReviewRetryMinutes, ReviewRetryMax: settings.ReviewRetryMax,
		}
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("计算分享任务指纹: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
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

func cachedRunFilesExist(root string, paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	for _, raw := range paths {
		relative := filepath.Clean(filepath.FromSlash(raw))
		if raw == "" || !filepath.IsLocal(relative) || relative == "." {
			return false
		}
		current := root
		segments := strings.Split(relative, string(os.PathSeparator))
		for i, segment := range segments {
			current = filepath.Join(current, segment)
			info, err := os.Lstat(current)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return false
			}
			if i < len(segments)-1 && !info.IsDir() || i == len(segments)-1 && !info.Mode().IsRegular() {
				return false
			}
		}
	}
	return true
}

func successfulRunCachePaths(root string, outcomes []BatchOutcome) ([]string, bool) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, false
	}
	paths := make([]string, 0)
	seen := make(map[string]bool)
	for _, outcome := range outcomes {
		result := outcome.Result
		if outcome.Err != nil || result == nil || result.Failed != 0 || result.MetadataFailed != 0 || len(result.Files) == 0 {
			return nil, false
		}
		for _, item := range result.Files {
			paths = appendCachedRunPath(paths, seen, root, result.Directory, item.Path)
			if paths == nil {
				return nil, false
			}
		}
		for _, item := range result.MetadataFiles {
			paths = appendCachedRunPath(paths, seen, root, result.Directory, item.Path)
			if paths == nil {
				return nil, false
			}
		}
	}
	return paths, len(paths) > 0
}

func appendCachedRunPath(paths []string, seen map[string]bool, root, directory, path string) []string {
	destination := filepath.Join(directory, filepath.FromSlash(path))
	relative, err := filepath.Rel(root, destination)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return nil
	}
	relative = filepath.ToSlash(relative)
	if !seen[relative] {
		seen[relative] = true
		paths = append(paths, relative)
	}
	return paths
}

func (s *ShareRuns) restoreSuccessfulRun(ctx context.Context, id string, digest [32]byte, fingerprint, cacheKey string, inputCount int, source string) (ShareRunView, bool) {
	store, ok := s.tasks.Store.(shareRunCacheStore)
	if !ok {
		return ShareRunView{}, false
	}
	cacheCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, found, err := store.LoadShareRunCache(cacheCtx, cacheKey)
	if err != nil {
		if s.tasks.Logs != nil {
			s.tasks.Logs.Append("STRM", id, source, "WARNING", "读取分享快照缓存失败，已改为正常生成："+err.Error())
		}
		return ShareRunView{}, false
	}
	if !found {
		return ShareRunView{}, false
	}
	var record shareRunCacheRecord
	valid := json.Unmarshal([]byte(raw), &record) == nil && len(record.Items) == inputCount && cachedRunFilesExist(s.tasks.Settings.root, record.Paths)
	if valid {
		for i := range record.Items {
			item := &record.Items[i]
			if item.Status != "success" || item.Result == nil {
				valid = false
				break
			}
			item.Message = "本地文件完整，已复用成功快照"
			item.Result.Cached = true
		}
	}
	if !valid {
		if err := store.DeleteShareRunCache(cacheCtx, cacheKey); err != nil && s.tasks.Logs != nil {
			s.tasks.Logs.Append("STRM", id, source, "WARNING", "清理失效分享快照缓存失败："+err.Error())
		}
		return ShareRunView{}, false
	}
	done := make(chan struct{})
	close(done)
	view := ShareRunView{ID: id, Status: "success", Items: record.Items}
	s.runs[id] = &shareRun{view: view, digest: digest, fingerprint: fingerprint, cacheKey: cacheKey, finished: time.Now(), done: done}
	if s.tasks.Logs != nil {
		s.tasks.Logs.Append("STRM", id, source, "INFO", "本地文件完整，已复用成功分享快照，未再次请求 115")
	}
	return cloneShareRun(view), true
}

func (s *ShareRuns) saveSuccessfulRun(cacheKey string, view ShareRunView, outcomes []BatchOutcome) error {
	store, ok := s.tasks.Store.(shareRunCacheStore)
	if !ok {
		return nil
	}
	paths, ok := successfulRunCachePaths(s.tasks.Settings.root, outcomes)
	if !ok {
		return nil
	}
	record, err := json.Marshal(shareRunCacheRecord{Items: cloneShareRun(view).Items, Paths: paths})
	if err != nil {
		return fmt.Errorf("编码分享快照缓存: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.root), 5*time.Second)
	defer cancel()
	return store.SaveShareRunCache(ctx, cacheKey, string(record))
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
	source, _ := ctx.Value(generationSourceKey{}).(string)
	if source != "Telegram" && source != "订阅" {
		source = "网页"
	}
	base := WithGenerationSource(s.root, source)
	if settings, ok := ctx.Value(automationContextKey{}).(AutomationSettings); ok {
		base = context.WithValue(base, automationContextKey{}, settings)
	}
	if batch, ok := ctx.Value(generationBatchKey{}).(generationBatch); ok {
		base = context.WithValue(base, generationBatchKey{}, batch)
	}
	if managed, _ := ctx.Value(shareReplyManagedKey{}).(bool); managed {
		base = WithShareReplyManaged(base)
	}
	pinned := s.tasks.pinAccount(base)
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
	settings, _ := pinned.Value(automationContextKey{}).(AutomationSettings)
	fingerprint, err := shareRunFingerprint(settings, cookieHash(generationCookie(pinned, s.tasks.Cookie)), inputs)
	if err != nil {
		return ShareRunView{}, err
	}
	cacheKey, err := shareRunFingerprint(settings, "", inputs)
	if err != nil {
		return ShareRunView{}, err
	}
	if existingID := s.activeByFingerprint[fingerprint]; existingID != "" {
		if existing := s.runs[existingID]; existing != nil && existing.finished.IsZero() {
			return cloneShareRun(existing.view), nil
		}
		delete(s.activeByFingerprint, fingerprint)
	}
	if cached, ok := s.restoreSuccessfulRun(pinned, id, digest, fingerprint, cacheKey, len(inputs), source); ok {
		return cached, nil
	}
	if active >= 20 {
		return ShareRunView{}, ErrShareRunBusy
	}
	runCtx, cancel := context.WithCancel(pinned)
	readyAt := time.Now()
	// Telegram 已在消息接收阶段完成同一窗口的收集；订阅批次在公共队列中应用相同等待设置。
	if source == "订阅" {
		readyAt = readyAt.Add(time.Duration(settings.TGBatchWaitSeconds) * time.Second)
	}
	run := &shareRun{view: ShareRunView{ID: id, Status: "queued", Items: make([]ShareRunItem, len(inputs))}, digest: digest, fingerprint: fingerprint, cacheKey: cacheKey, ctx: runCtx, cancel: cancel, tasks: make([]*model.ShareTask, len(inputs)), requests: make([]shareTaskRequest, len(inputs)), readyAt: readyAt, done: make(chan struct{})}
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
	s.activeByFingerprint[fingerprint] = id
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

func (s *ShareRuns) Wait(ctx context.Context, id string) (ShareRunView, error) {
	s.mu.Lock()
	run := s.runs[id]
	if run == nil {
		s.mu.Unlock()
		return ShareRunView{}, ErrShareRunMissing
	}
	done := run.done
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return ShareRunView{}, ctx.Err()
	case <-done:
		return s.Get(id)
	}
}

func newInternalShareRunID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("创建后台批次编号: %w", err)
	}
	return hex.EncodeToString(id[:]), nil
}

// Generate 和 GenerateBatch 让 Telegram 等后台调用方与网页共用 ShareRuns FIFO 队列。
func (s *ShareRuns) Generate(ctx context.Context, input GenerateInput) (*GenerationResult, error) {
	outcomes, err := s.GenerateBatch(ctx, []GenerateInput{input})
	if len(outcomes) != 1 {
		return nil, err
	}
	return outcomes[0].Result, errors.Join(err, outcomes[0].Err)
}

func (s *ShareRuns) GenerateBatch(ctx context.Context, inputs []GenerateInput) ([]BatchOutcome, error) {
	id, err := newInternalShareRunID()
	if err != nil {
		return nil, err
	}
	accepted, err := s.Submit(ctx, id, inputs)
	if err != nil {
		return nil, err
	}
	// 提交成功后由队列根上下文负责生命周期，调用方的排队时间不占执行超时。
	view, err := s.Wait(s.root, accepted.ID)
	if err != nil {
		return nil, err
	}
	outcomes := make([]BatchOutcome, len(view.Items))
	for i, item := range view.Items {
		if item.Result != nil {
			outcomes[i].Result = &GenerationResult{Cached: item.Result.Cached, Mode: item.Result.Mode, Written: item.Result.Count, Failed: item.Result.Failed, Files: make([]GeneratedSTRM, item.Result.Total), MetadataWritten: item.Result.MetadataCount, MetadataFailed: item.Result.MetadataFailed, MetadataFiles: make([]GeneratedMetadata, item.Result.MetadataTotal), ConflictRenamed: item.Result.ConflictRenamed, Directory: item.Result.Directory, ReshareURL: item.Result.ReshareURL, TempDirectoryID: item.Result.TempDirectoryID, Cleaned: item.Result.Cleaned}
		}
		if item.Status != "success" {
			outcomes[i].Err = errors.New(item.Error)
		}
	}
	return outcomes, nil
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
			if delay := time.Until(run.readyAt); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-run.ctx.Done():
				case <-timer.C:
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
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
	inputs := make([]GenerateInput, len(run.requests))
	s.mu.Lock()
	for i := range run.requests {
		inputs[i] = run.requests[i].Input
		run.view.Items[i].Status = "running"
		run.view.Items[i].Message = "正在后台生成 STRM"
	}
	s.mu.Unlock()
	itemCtx, cancel := context.WithTimeout(run.ctx, 5*time.Minute)
	itemCtx = WithGenerationProgress(itemCtx, func(progress GenerationProgress) {
		s.mu.Lock()
		for i := range run.view.Items {
			if run.view.Items[i].Status == "running" {
				run.view.Items[i].Message = progress.Message
			}
		}
		s.mu.Unlock()
	})
	outcomes, batchErr := s.tasks.executePreparedBatch(itemCtx, run.tasks, run.requests, inputs)
	cancel()
	if len(outcomes) != len(inputs) {
		outcomes = make([]BatchOutcome, len(inputs))
		batchErr = errors.Join(batchErr, errors.New("后台批次未返回完整结果"))
	}
	for i := range outcomes {
		result, err := outcomes[i].Result, outcomes[i].Err
		if result == nil && err == nil {
			err = batchErr
		}
		s.mu.Lock()
		item := &run.view.Items[i]
		item.Status, item.Message = "success", "生成成功"
		if result != nil {
			item.Result = &ShareRunResult{Cached: result.Cached, Mode: result.Mode, Count: result.Written, Failed: result.Failed, Total: len(result.Files), MetadataCount: result.MetadataWritten, MetadataFailed: result.MetadataFailed, MetadataTotal: len(result.MetadataFiles), ConflictRenamed: result.ConflictRenamed, Directory: result.Directory, ReshareURL: result.ReshareURL, TempDirectoryID: result.TempDirectoryID, Cleaned: result.Cleaned}
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
	if batchErr == nil && run.ctx.Err() == nil {
		s.mu.Lock()
		cacheView := cloneShareRun(run.view)
		cacheView.Status = "success"
		cacheable := true
		for _, item := range cacheView.Items {
			if item.Status != "success" || item.Result == nil {
				cacheable = false
				break
			}
		}
		taskID := run.view.ID
		source, _ := run.ctx.Value(generationSourceKey{}).(string)
		s.mu.Unlock()
		if cacheable {
			if err := s.saveSuccessfulRun(run.cacheKey, cacheView, outcomes); err != nil && s.tasks.Logs != nil {
				s.tasks.Logs.Append("STRM", taskID, source, "WARNING", "保存分享快照缓存失败："+err.Error())
			}
		}
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
	close(run.done)
	if s.activeByFingerprint[run.fingerprint] == run.view.ID {
		delete(s.activeByFingerprint, run.fingerprint)
	}
	// 结束后释放内存中的 Cookie 和生成设置，只保留不含凭据的页面状态。
	run.ctx, run.cancel, run.requests, run.tasks = nil, nil, nil, nil
	s.mu.Unlock()
}
