package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type FolderMonitorStore interface {
	SaveFolderMonitor(context.Context, model.FolderMonitor) error
	GetFolderMonitor(context.Context, string) (model.FolderMonitor, error)
	ListFolderMonitors(context.Context) ([]model.FolderMonitor, error)
	DeleteFolderMonitor(context.Context, string) error
}

type FolderMonitorClient interface {
	DriveFiles(context.Context, string, int, string) (*p115.SharePage, error)
	CreateDirectory(context.Context, string, string, string) (string, error)
	MoveEntries(context.Context, []string, string, string) error
	SendShare(context.Context, []string, string) (*p115.ShareInfo, error)
	ConfigureShare(context.Context, *p115.ShareInfo, string, string) error
	List(context.Context, *p115.ShareInfo, string, int, string) (*p115.SharePage, error)
	DownloadURL(context.Context, *p115.ShareInfo, string, string) (string, error)
	DeleteEntries(context.Context, []string, string) error
}

type FolderMonitorGenerator interface {
	Generate(context.Context, GenerateInput) (*GenerationResult, error)
}

type folderManifestEntry struct {
	Path      string
	ID        string
	Size      int64
	Directory bool
}

// FolderMonitorService treats each configured directory as an inbox. A batch
// freezes the discovered entries into an isolated temporary directory, then shares that
// directory's direct children. Cookies exist only in the current execution
// context.
type FolderMonitorService struct {
	Store     FolderMonitorStore
	Accounts  *P115Accounts
	Client    FolderMonitorClient
	Generator FolderMonitorGenerator
	Tasks     *ShareTasks
	Root      string
	Logs      *GenerationLogs
	// SharedSettings supplies the explicitly selected transfer account, parent
	// directory and optional fixed share password. It is read when a batch
	// starts; an in-flight batch keeps its persisted account and password.
	SharedSettings func() AutomationSettings
	ReserveWork    func() (func(), error)
	wake           chan struct{}
	mu             sync.Mutex
	running        map[string]bool
	// 仅在启动前设置；零值采用 90 秒等待上限和 5 秒轮询间隔。
	snapshotTimeout  time.Duration
	snapshotInterval time.Duration
}

func NewFolderMonitorService(store FolderMonitorStore, accounts *P115Accounts, client FolderMonitorClient, generator FolderMonitorGenerator, root string, logs *GenerationLogs) *FolderMonitorService {
	return &FolderMonitorService{Store: store, Accounts: accounts, Client: client, Generator: generator, Root: root, Logs: logs, wake: make(chan struct{}, 1), running: make(map[string]bool)}
}

func (s *FolderMonitorService) SetSharedSettings(provider func() AutomationSettings) {
	s.SharedSettings = provider
}

func (s *FolderMonitorService) generationInput(value model.FolderMonitor, shareURL string) GenerateInput {
	// The temporary directory is only an internal staging container.  The
	// share is created from its direct children, so generation must read the
	// share root rather than attempting to descend through the staging CID.
	input := GenerateInput{URL: shareURL, CID: "0", Mode: "direct", BaseURL: value.BaseURL, OutputDir: value.OutputDir, VideoExtensions: value.VideoExtensions, MetadataExtensions: value.MetadataExtensions, MinSizeMB: value.MinSizeMB, Recursive: value.Recursive}
	return input
}

func (s *FolderMonitorService) validate(value model.FolderMonitor) error {
	if s == nil || s.Store == nil || s.Accounts == nil || s.Client == nil || s.Generator == nil {
		return generationInputError("目录监控服务尚未初始化")
	}
	if strings.TrimSpace(value.Name) == "" || len([]rune(value.Name)) > 80 || strings.ContainsAny(value.Name, "\r\n\x00") {
		return generationInputError("监控名称应为 1 到 80 个字符")
	}
	if err := s.Accounts.ValidatePrimary(strings.TrimSpace(value.AccountID)); err != nil {
		return err
	}
	if value.SourceCID == "" || value.SourceCID == "0" {
		return generationInputError("请选择非根目录作为监控收件箱")
	}
	if _, err := strconv.ParseUint(value.SourceCID, 10, 64); err != nil {
		return generationInputError("监控目录编号无效")
	}

	return ValidateGenerationOptions(s.Root, s.generationInput(value, ""))
}

func (s *FolderMonitorService) Save(ctx context.Context, value model.FolderMonitor) (model.FolderMonitor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value.Name = strings.TrimSpace(value.Name)
	account, err := s.Accounts.Primary(strings.TrimSpace(value.AccountID))
	if err != nil {
		return model.FolderMonitor{}, err
	}
	value.AccountID = account.ID
	value.SourceCID = strings.TrimSpace(value.SourceCID)
	value.SourcePath = strings.TrimSpace(value.SourcePath)
	value.Enabled, value.IntervalSeconds, value.NextScanAt = false, 0, time.Time{}
	if strings.TrimSpace(value.VideoExtensions) == "" {
		value.VideoExtensions = DefaultVideoExtensions
	}
	if strings.TrimSpace(value.MetadataExtensions) == "" {
		value.MetadataExtensions = DefaultMetadataExtensions
	}
	if err := s.validate(value); err != nil {
		return model.FolderMonitor{}, err
	}
	now := time.Now().UTC()
	if value.ID == "" {
		var nonce [12]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return model.FolderMonitor{}, fmt.Errorf("创建目录监控编号: %w", err)
		}
		value.ID = hex.EncodeToString(nonce[:])
		value.Stage = "waiting"
	} else {
		existing, err := s.Store.GetFolderMonitor(ctx, value.ID)
		if err != nil {
			return model.FolderMonitor{}, err
		}
		if s.running[value.ID] || existing.TaskPending || existing.BatchCreationPending || existing.BatchDirectoryID != "" || !monitorEditable(existing.Stage) {
			return model.FolderMonitor{}, generationInputError("当前批次正在处理，不能修改监控配置")
		}
		copyMonitorRuntime(&value, existing)
		value.Enabled, value.NextScanAt = false, time.Time{}
		value.ScheduleVersion = existing.ScheduleVersion + 1
		value.SchedulePaused, value.ScheduleMessage, value.StableFingerprint = "", "", ""
		value.StableScans = 0
	}
	value.UpdatedAt = now
	if err := s.Store.SaveFolderMonitor(ctx, value); err != nil {
		return model.FolderMonitor{}, err
	}
	s.Wake()
	return value, nil
}

// SaveUsingSharedSettings is the simplified folder-monitor entry point. The
// monitor page deliberately has no account selector: the explicitly selected
// account in /115/share is copied into this feature's persisted accountId.
// Runtime fields are inherited from the same share configuration so the two
// pages cannot silently drift apart.
func (s *FolderMonitorService) SaveUsingSharedSettings(ctx context.Context, value model.FolderMonitor) (model.FolderMonitor, error) {
	settings := s.sharedSettings(&value)
	if strings.TrimSpace(settings.AccountID) == "" {
		return model.FolderMonitor{}, generationInputError("请先在 115 分享设置中选择已登录账号")
	}
	if value.ID == "" {
		values, err := s.Store.ListFolderMonitors(ctx)
		if err != nil {
			return model.FolderMonitor{}, err
		}
		if len(values) > 0 {
			return model.FolderMonitor{}, generationInputError("已存在收件箱监控，请直接修改现有配置")
		}
	}
	if strings.TrimSpace(value.AccountID) != strings.TrimSpace(settings.AccountID) {
		return model.FolderMonitor{}, generationInputError("分享 STRM 账号已变化，请使用当前账号重新选择目录")
	}
	value.AccountID = strings.TrimSpace(settings.AccountID)
	value.Name = "目录分享"
	value.BaseURL = settings.Generation.BaseURL
	value.OutputDir = settings.Generation.OutputDir
	value.VideoExtensions = settings.Generation.VideoExtensions
	value.MetadataExtensions = settings.Generation.MetadataExtensions
	value.MinSizeMB = settings.Generation.MinSizeMB
	value.Recursive = settings.Generation.Recursive
	return s.Save(ctx, value)
}

func monitorEditable(stage string) bool {
	return stage == "waiting" || stage == "empty" || stage == "completed" || stage == "error" || stage == "ended"
}

func copyMonitorRuntime(dst *model.FolderMonitor, src model.FolderMonitor) {
	dst.RecentRuns = append([]model.FolderRun(nil), src.RecentRuns...)
	dst.LogTaskID, dst.LogAttempt = src.LogTaskID, src.LogAttempt
	dst.EndedBatches = append([]model.EndedFolderBatch(nil), src.EndedBatches...)
	dst.GenerationTaskID, dst.GenerationSettings = src.GenerationTaskID, append(json.RawMessage(nil), src.GenerationSettings...)
	dst.TaskPending, dst.TaskTrigger = src.TaskPending, src.TaskTrigger
	dst.Stage, dst.ShareURL, dst.SharePassword, dst.ShareReviewing = src.Stage, src.ShareURL, src.SharePassword, src.ShareReviewing
	dst.BatchIDs, dst.BatchFingerprint = append([]string(nil), src.BatchIDs...), src.BatchFingerprint
	dst.BatchDirectoryID, dst.BatchDirectoryName, dst.BatchMoveIndex = src.BatchDirectoryID, src.BatchDirectoryName, src.BatchMoveIndex
	dst.BatchCreationPending, dst.RecoveryMessage = src.BatchCreationPending, src.RecoveryMessage
	dst.BatchParentCID = src.BatchParentCID
	dst.CleanupIndex = src.CleanupIndex
	dst.StableFingerprint, dst.StableScans = src.StableFingerprint, src.StableScans
	dst.LastScanAt, dst.NextScanAt, dst.LastError = src.LastScanAt, src.NextScanAt, src.LastError
}

func (s *FolderMonitorService) List(ctx context.Context) ([]model.FolderMonitor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.Store.ListFolderMonitors(ctx)
	for i := range values {
		values[i].Running = s.running[values[i].ID]
	}
	return values, err
}

func (s *FolderMonitorService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.Store.GetFolderMonitor(ctx, id)
	if err != nil {
		return err
	}
	if s.running[id] || value.TaskPending || value.BatchCreationPending || value.BatchDirectoryID != "" || !monitorEditable(value.Stage) {
		return generationInputError("当前批次正在处理，不能删除监控配置")
	}
	return s.Store.DeleteFolderMonitor(ctx, id)
}

var ErrFolderMonitorBusy = errors.New("当前任务已排队或正在处理")

// SetSchedule retains the default interval for internal callers.
func (s *FolderMonitorService) SetSchedule(ctx context.Context, id string, enabled bool) (model.FolderMonitor, error) {
	return s.UpdateSchedule(ctx, id, enabled, 600)
}

func (s *FolderMonitorService) UpdateSchedule(ctx context.Context, id string, enabled bool, interval int) (model.FolderMonitor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.Store.GetFolderMonitor(ctx, id)
	if err != nil {
		return model.FolderMonitor{}, err
	}
	if enabled {
		if interval != 300 && interval != 600 && interval != 1800 && interval != 3600 {
			return value, generationInputError("请选择 5、10、30 或 60 分钟检查间隔")
		}
		if s.running[id] || value.TaskPending || value.BatchCreationPending || value.BatchDirectoryID != "" || !monitorIdleStage(value.Stage) {
			return value, generationInputError("请先完成或结束当前批次，再开启自动执行")
		}
		if s.Accounts == nil {
			return value, generationInputError("账号服务尚未初始化")
		}
		if err := s.pinSettings(&value); err != nil {
			return value, err
		}
		account, err := s.Accounts.Primary(value.AccountID)
		if err != nil || account.Cookie == "" {
			return value, generationInputError("请先登录主账号")
		}
	}
	value.NextScanAt, value.IntervalSeconds = time.Time{}, 0
	value.ScheduleVersion++
	value.SchedulePaused, value.ScheduleMessage = "", ""
	if enabled {
		value.StableFingerprint, value.StableScans = "", 0
		value.IntervalSeconds, value.NextScanAt = interval, time.Now().UTC().Add(time.Duration(interval)*time.Second)
		value.Stage, value.LastError = "waiting", ""
	}
	value.Enabled, value.UpdatedAt = enabled, time.Now().UTC()
	if err := s.Store.SaveFolderMonitor(ctx, value); err != nil {
		return model.FolderMonitor{}, err
	}
	value.Running = s.running[id]
	s.Wake()
	return value, nil
}

func (s *FolderMonitorService) ScanNow(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.Store.GetFolderMonitor(ctx, id)
	if err != nil {
		return err
	}
	if s.running[id] || value.TaskPending {
		return ErrFolderMonitorBusy
	}
	if value.BatchCreationPending {
		return generationInputError("临时目录创建结果待核对，请先核对残留")
	}
	if value.Stage == "blocked" || value.Stage == "recovering" || value.Stage == "sharing_legacy" || (value.Stage == "error" && value.BatchDirectoryID != "") {
		return generationInputError("当前批次需要人工核对，不能直接重试")
	}
	if value.BatchDirectoryID == "" && monitorIdleStage(value.Stage) {
		value.RecoveryMessage = ""
		value.Stage, value.LastError = "waiting", ""
		if err := s.pinSettings(&value); err != nil {
			return err
		}
		value.GenerationTaskID = ""
		value.ShareURL, value.SharePassword, value.ShareReviewing = "", "", false
		var nonce [12]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return fmt.Errorf("创建目录任务编号: %w", err)
		}
		value.LogTaskID, value.LogAttempt = "STRM-MON-"+hex.EncodeToString(nonce[:]), 0
	}
	if value.GenerationTaskID != "" && value.Stage == "generating" && s.Tasks != nil {
		task, err := s.Tasks.Store.GetShareTask(ctx, value.GenerationTaskID)
		if err != nil {
			return err
		}
		switch task.Status {
		case "queued", "running", "retry_queued", "waiting_review":
			return ErrFolderMonitorBusy
		case "success": // Only the remaining directory verification/cleanup needs retrying.
		default:
			results, err := s.Tasks.RetryTasks(ctx, []string{value.GenerationTaskID})
			if err != nil {
				return err
			}
			if len(results) != 1 || !results[0].Accepted {
				return generationInputError("分享任务暂不能重试，请查看分享任务日志")
			}
		}
	}
	value.NextScanAt = time.Time{}
	value.LogAttempt++
	value.TaskPending, value.TaskTrigger, value.LastError = true, "manual", ""
	value.UpdatedAt = time.Now().UTC()
	if err := s.Store.SaveFolderMonitor(ctx, value); err != nil {
		return err
	}
	s.Wake()
	return nil
}

func (s *FolderMonitorService) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *FolderMonitorService) Run(ctx context.Context) error {
	timer := time.NewTimer(15 * time.Second)
	timer.Stop()
	defer timer.Stop()
	for {
		if err := s.runDue(ctx); err != nil && ctx.Err() == nil && s.Logs != nil {
			s.Logs.Append("STRM", "目录监控", "目录监控", "ERROR", err.Error())
		}
		values, _ := s.Store.ListFolderMonitors(ctx) // A failed read falls back to the normal cadence.
		timer.Reset(folderWakeDelay(values, time.Now().UTC()))
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		case <-s.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}
}

func (s *FolderMonitorService) runDue(ctx context.Context) error {
	if err := s.checkScheduled(ctx); err != nil {
		return err
	}
	values, err := s.Store.ListFolderMonitors(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, value := range values {
		if !monitorDue(value, now) {
			continue
		}
		if value.Stage == "generating" && value.GenerationTaskID != "" && s.Tasks != nil {
			task, taskErr := s.Tasks.Store.GetShareTask(ctx, value.GenerationTaskID)
			if taskErr == nil {
				switch task.Status {
				case "queued", "running", "retry_queued", "waiting_review":
					continue
				case "success":
					if value.LastError != "" && !task.UpdatedAt.After(value.UpdatedAt) {
						continue
					}
				case "failed", "review", "cancelled", "interrupted":
					if value.LastError != "" {
						continue
					}
				}
			}
		}
		started := time.Now()
		if err := s.runOne(ctx, value.ID); err != nil {
			// 调度边界统一记录失败；下层只返回错误，避免重复刷屏。
			s.logFailure(ctx, value.ID, err, time.Since(started))
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// A failed batch may only release verified empty or missing staging
			// directories. Recover never deletes ungenerated source files.
			// Failure leaves the original error/checkpoint intact; do not log a
			// second failure for a nonempty or temporarily unreadable directory.
			_, _ = s.Recover(ctx, value.ID, value.LogTaskID)
		} else if latest, err := s.Store.GetFolderMonitor(ctx, value.ID); err == nil && latest.LogTaskID != "" {
			if latest.TaskPending && !latest.NextScanAt.IsZero() {
				continue // Waiting for the writer is not a completed execution.
			}
			level, message := "INFO", "本次执行结束"
			if latest.Stage == "completed" {
				message = "整批处理完成：STRM 已生成，临时目录已清理"
			} else if latest.Stage == "empty" {
				level, message = "DEBUG", "监控目录为空，本次检查结束"
			}
			s.Logs.Append("STRM", latest.LogTaskID, "目录监控", level, fmt.Sprintf("%s，第 %d 次执行耗时 %.2f 秒", message, latest.LogAttempt, time.Since(started).Seconds()))
		}
	}
	return nil
}

func monitorDue(value model.FolderMonitor, now time.Time) bool {
	if value.TaskPending && !value.NextScanAt.IsZero() && now.Before(value.NextScanAt) {
		return false
	}
	if value.Stage == "blocked" {
		return false
	}
	// Submitted batches advance independently of the discovery schedule switch.
	return value.Stage == "checking_share" || (value.Stage == "cleaning" && value.CleanupIndex >= 1) || value.TaskPending || (value.BatchDirectoryID != "" && value.LastError == "") || (value.Stage == "generating" && value.GenerationTaskID != "")
}

func (s *FolderMonitorService) runOne(root context.Context, id string) error {
	s.mu.Lock()
	if s.running[id] {
		s.mu.Unlock()
		return nil
	}
	value, err := s.Store.GetFolderMonitor(root, id)
	if err != nil || !monitorDue(value, time.Now().UTC()) {
		s.mu.Unlock()
		return err
	}
	if !value.TaskPending && (value.BatchDirectoryID == "" || value.LastError != "") {
		value.TaskTrigger = "manual"
	}
	if value.LogTaskID == "" {
		var nonce [12]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			s.mu.Unlock()
			return fmt.Errorf("创建监控日志编号: %w", err)
		}
		value.LogTaskID, value.LogAttempt = "STRM-MON-"+hex.EncodeToString(nonce[:]), 0
	}
	if value.LogAttempt == 0 {
		value.LogAttempt = 1
	}
	value.TaskPending, value.LastError, value.UpdatedAt = true, "", time.Now().UTC()
	if err := s.Store.SaveFolderMonitor(root, value); err != nil {
		s.mu.Unlock()
		return err
	}
	s.running[id] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.running, id); s.mu.Unlock() }()
	if s.ReserveWork != nil {
		release, err := s.ReserveWork()
		if err != nil {
			value.NextScanAt = time.Now().UTC().Add(folderBusyRetryDelay)
			value.ScheduleMessage = "整理、清理或字幕导入正在执行，5 秒后重试"
			return s.checkpoint(root, &value)
		} // Keep the submitted task queued until the writer finishes.
		defer release()
	}
	value.NextScanAt = time.Time{}
	root = context.WithValue(root, generationLogKey{}, generationLogger{store: s.Logs, task: value.LogTaskID, source: "目录监控", redact: func(message string) string {
		return generationErrorDetail(errors.New(message), GenerateInput{})
	}})
	logGeneration(root, "INFO", fmt.Sprintf("开始目录监控处理：触发=%s，第 %d 次执行，阶段=%s，目录=%s（%s），账号编号=%s", value.TaskTrigger, value.LogAttempt, value.Stage, value.SourcePath, value.SourceCID, value.AccountID))
	// Old post-cleanup failures only need a local checkpoint update. Do not
	// require the old account, share or local STRM paths to remain available.
	if value.Stage == "checking_share" || (value.Stage == "cleaning" && value.CleanupIndex >= 1) {
		return s.completeCleanedBatch(root, &value)
	}
	account, err := s.Accounts.Primary(value.AccountID)
	if err != nil || account.Cookie == "" {
		return s.failAt(root, &value, value.Stage, "所选 115 账号已删除或未登录")
	}
	ctx, cancel := context.WithTimeout(root, 10*time.Minute)
	defer cancel()
	ctx = context.WithValue(ctx, generationCookieKey{}, account.Cookie)
	ctx = context.WithValue(ctx, generationLogKey{}, generationLogger{store: s.Logs, task: value.LogTaskID, source: "目录监控", redact: func(message string) string {
		return monitorLogDetail(message, account.Cookie)
	}})
	ctx = p115.WithRequestProgress(ctx, func(message string) { logGeneration(ctx, "WARNING", message) })
	logGeneration(ctx, "INFO", "已固定本次执行的账号凭据："+account.Name)
	switch value.Stage {
	case "creating":
		return s.failAt(ctx, &value, "blocked", "临时目录创建结果尚未确认，正在核对残留；不会重复创建")
	case "waiting", "empty", "completed", "error", "ended":
		if value.BatchDirectoryID != "" {
			return s.failAt(ctx, &value, "blocked", "旧批次保留了临时目录但缺少恢复阶段，请人工核对；不会创建新批次")
		}
		return s.discover(ctx, &value, account)
	case "sharing_legacy":
		return s.failAt(ctx, &value, "blocked", "创建分享时服务曾中断，无法确认结果；为避免重复分享已停止")
	case "blocked":
		return nil
	case "moving":
		if err := s.moveBatch(ctx, &value, account); err != nil {
			return err
		}
		if value.Stage == "sharing" {
			return s.resume(ctx, &value, account)
		}
		return nil
	case "stabilizing":
		// Compatibility for checkpoints written by the previous implementation:
		// no new stability scan is performed; continue directly with sharing.
		value.Stage, value.StableFingerprint, value.StableScans = "sharing", "", 0
		if err := s.checkpoint(ctx, &value); err != nil {
			return err
		}
		return s.resume(ctx, &value, account)
	case "sharing":
		return s.resume(ctx, &value, account)
	case "configuring", "verifying", "generating", "cleaning", "checking_share":
		return s.resume(ctx, &value, account)
	default:
		return s.failAt(ctx, &value, "blocked", "目录监控状态无法识别，已停止自动处理")
	}
}

func monitorIdleStage(stage string) bool {
	switch stage {
	case "waiting", "empty", "completed", "error", "ended":
		return true
	default:
		return false
	}
}

func (s *FolderMonitorService) discover(ctx context.Context, value *model.FolderMonitor, account P115Account) error {
	logGeneration(ctx, "INFO", "开始检查监控目录")
	// Capture shared feature settings once, before any remote work. Never
	// silently switch to another account when the stored binding is invalid.
	if len(value.GenerationSettings) == 0 {
		if err := s.pinSettings(value); err != nil {
			return s.fail(ctx, value, err.Error())
		}
	}
	settings, err := s.batchSettings(value)
	if err != nil {
		return s.fail(ctx, value, err.Error())
	}
	roots, err := s.listRootEntries(ctx, value.SourceCID, account.Cookie)
	if err != nil {
		return s.fail(ctx, value, "扫描监控目录失败："+err.Error())
	}
	now := time.Now().UTC()
	value.LastScanAt, value.NextScanAt = now, time.Time{}
	if len(roots) == 0 {
		value.TaskPending = false
		value.Stage, value.StableFingerprint, value.StableScans, value.LastError = "empty", "", 0, ""
		return s.checkpoint(ctx, value)
	}
	if len(roots) > 1000 {
		return s.fail(ctx, value, "单次分享最多支持 1000 个顶层条目，请缩小目录范围")
	}
	ids := make([]string, 0, len(roots))
	for _, entry := range roots {
		ids = append(ids, entry.ID)
	}
	manifest, foundIDs, err := s.scanDrive(ctx, value.SourceCID, ids, account.Cookie)
	if err != nil {
		return s.fail(ctx, value, "读取目录内容失败："+err.Error())
	}
	if len(foundIDs) != len(ids) {
		return s.fail(ctx, value, "读取期间来源条目发生变化，请重新提交；源文件未移动")
	}
	if value.TaskTrigger == "scheduled" && value.StableFingerprint != manifestFingerprint(manifest) {
		value.TaskPending, value.Stage = false, "waiting"
		value.StableFingerprint, value.StableScans = manifestFingerprint(manifest), 1
		value.ScheduleMessage = "目录内容发生变化，5 秒后重新核对"
		value.NextScanAt = time.Now().UTC().Add(folderStabilityDelay)
		return s.checkpoint(ctx, value)
	}
	media := 0
	for _, entry := range manifest {
		if !entry.Directory && monitorVideoFile(*value, entry.Path) && entry.Size >= value.MinSizeMB*(1<<20) && (value.Recursive || !strings.Contains(entry.Path, "/")) {
			media++
		}
	}
	if media == 0 {
		return s.fail(ctx, value, "目录中没有符合生成规则的视频，请检查视频后缀、最小大小及递归设置；源文件未移动")
	}
	password, err := newSharePassword(settings.FixedSharePassword)
	if err != nil {
		return s.fail(ctx, value, err.Error())
	}
	parent := settings.ParentCID
	if parent == "" {
		parent = "0"
	}
	if _, err := strconv.ParseUint(parent, 10, 64); err != nil {
		return s.fail(ctx, value, "转存父目录 ID 无效："+err.Error())
	}
	name := batchDirectoryName()
	// Persist the exact creation intent before the non-idempotent remote call.
	// A restart reconciles this name under this parent and never creates again.
	parents, err := s.listRootEntries(ctx, parent, account.Cookie)
	if err != nil {
		return s.fail(ctx, value, "核对临时目录父目录失败："+err.Error())
	}
	for _, entry := range parents {
		if entry.Name == name {
			return s.fail(ctx, value, "临时目录名称已存在，请重新提交")
		}
	}
	if err := s.Accounts.ValidatePrimary(value.AccountID); err != nil {
		return s.fail(ctx, value, err.Error())
	}
	value.BatchIDs, value.BatchFingerprint = ids, manifestFingerprint(manifest)
	value.BatchDirectoryName, value.BatchParentCID = name, parent
	value.BatchCreationPending, value.Stage = true, "creating"
	value.SharePassword = password
	value.ShareURL, value.CleanupIndex, value.BatchMoveIndex = "", 0, 0
	if err := s.checkpoint(ctx, value); err != nil {
		return err
	}
	logGeneration(ctx, "INFO", fmt.Sprintf("发现 %d 个顶层条目，开始创建临时目录：%s，父目录=%s", len(roots), name, parent))
	directoryID, err := s.Client.CreateDirectory(ctx, parent, name, account.Cookie)
	if err != nil {
		return s.failAt(ctx, value, "blocked", "创建临时目录结果待核对："+err.Error())
	}
	if directoryID == "" || directoryID == "0" || directoryID == value.SourceCID || directoryID == parent {
		return s.fail(ctx, value, "115 返回了无效的临时目录编号，已停止")
	}
	// Persist a fixed range: later arrivals must never join this batch.
	value.BatchIDs, value.BatchFingerprint = ids, manifestFingerprint(manifest)
	value.BatchDirectoryID, value.BatchDirectoryName, value.BatchMoveIndex = directoryID, name, 0
	value.BatchCreationPending = false
	value.BatchParentCID = parent
	value.CleanupIndex, value.ShareURL, value.SharePassword, value.LastError = 0, "", password, ""
	value.StableFingerprint, value.StableScans, value.Stage = "", 0, "moving"
	logGeneration(ctx, "INFO", "临时目录创建完成："+directoryID)
	if err := s.checkpoint(ctx, value); err != nil {
		return err
	}
	if err := s.moveBatch(ctx, value, account); err != nil {
		return err
	}
	if value.Stage == "sharing" {
		return s.resume(ctx, value, account)
	}
	return nil
}

func (s *FolderMonitorService) sharedSettings(value *model.FolderMonitor) AutomationSettings {
	if s.SharedSettings == nil {
		return AutomationSettings{AccountID: value.AccountID, ParentCID: "0"}
	}
	return s.SharedSettings()
}

func (s *FolderMonitorService) sharedParentCID(value *model.FolderMonitor) string {
	if value.BatchParentCID != "" {
		return value.BatchParentCID
	}
	cid := s.sharedSettings(value).ParentCID
	if cid == "" {
		return "0"
	}
	return cid
}
func batchDirectoryName() string {
	// Keep the name independent of the monitor label (which may contain
	// arbitrary user text) while making every batch easy to identify and
	// collision-resistant in the configured parent directory.
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err == nil {
		return "Cinema115-临时-" + hex.EncodeToString(nonce[:])
	}
	return fmt.Sprintf("Cinema115-临时-%d", time.Now().UnixNano())
}

// moveSelected reconciles a fixed manifest against both directories before
// issuing a move, making recovery safe even after a lost move response.
func (s *FolderMonitorService) moveSelected(ctx context.Context, value *model.FolderMonitor, account P115Account) error {
	for value.BatchMoveIndex < len(value.BatchIDs) {
		source, err := s.listRootEntries(ctx, value.SourceCID, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "moving", "读取来源目录失败："+err.Error())
		}
		batch, err := s.listRootEntries(ctx, value.BatchDirectoryID, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "moving", "读取临时目录失败："+err.Error())
		}
		inSource, inBatch := make(map[string]bool, len(source)), make(map[string]bool, len(batch))
		for _, entry := range source {
			inSource[entry.ID] = true
		}
		for _, entry := range batch {
			inBatch[entry.ID] = true
		}
		end := value.BatchMoveIndex + 1000
		if end > len(value.BatchIDs) {
			end = len(value.BatchIDs)
		}
		ids := make([]string, 0, end-value.BatchMoveIndex)
		for _, id := range value.BatchIDs[value.BatchMoveIndex:end] {
			if inSource[id] && inBatch[id] {
				return s.failAt(ctx, value, "blocked", "文件同时出现在来源和临时目录，请人工核对")
			}
			if !inSource[id] && !inBatch[id] {
				return s.failAt(ctx, value, "blocked", "批次条目已从来源和临时目录消失，请人工核对")
			}
			if !inBatch[id] {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			if err := s.Accounts.ValidatePrimary(value.AccountID); err != nil {
				return s.failAt(ctx, value, "moving", err.Error())
			}
			if err := s.Client.MoveEntries(ctx, ids, value.BatchDirectoryID, account.Cookie); err != nil {
				return s.failAt(ctx, value, "moving", "移动到临时目录失败："+err.Error())
			}
			batch, err = s.listRootEntries(ctx, value.BatchDirectoryID, account.Cookie)
			if err != nil {
				return s.failAt(ctx, value, "moving", "确认移动结果失败："+err.Error())
			}
			for _, entry := range batch {
				inBatch[entry.ID] = true
			}
			for _, id := range ids {
				if !inBatch[id] {
					return s.failAt(ctx, value, "moving", "移动结果尚未确认，请稍后重试")
				}
			}
		}
		value.BatchMoveIndex = end
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
	}
	value.Stage, value.StableFingerprint, value.StableScans = "sharing", "", 0
	return s.checkpoint(ctx, value)
}

func (s *FolderMonitorService) moveBatch(ctx context.Context, value *model.FolderMonitor, account P115Account) error {
	logGeneration(ctx, "INFO", "开始批量移动本次选定内容到临时目录")
	if value.BatchDirectoryID == "" || value.BatchDirectoryID == "0" || value.BatchDirectoryID == value.SourceCID {
		return s.failAt(ctx, value, "blocked", "批次目录编号无效，已停止")
	}
	if len(value.BatchIDs) == 0 {
		// Capture a fixed range when recovering a legacy batch without IDs.
		entries, err := s.listRootEntries(ctx, value.SourceCID, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "moving", "读取旧批次来源失败："+err.Error())
		}
		for _, entry := range entries {
			value.BatchIDs = append(value.BatchIDs, entry.ID)
		}
		value.BatchMoveIndex = 0
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
	}
	if err := s.moveSelected(ctx, value, account); err != nil {
		return err
	}
	logGeneration(ctx, "INFO", fmt.Sprintf("批量移动完成：本批 %d 个顶层条目，新增内容留待下次处理", value.BatchMoveIndex))
	if value.BatchFingerprint == "" {
		manifest, _, err := s.scanDrive(ctx, value.BatchDirectoryID, nil, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "sharing", "读取目录内容失败："+err.Error())
		}
		value.BatchFingerprint = manifestFingerprint(manifest)
	}
	return s.checkpoint(ctx, value)
}

func (s *FolderMonitorService) resume(ctx context.Context, value *model.FolderMonitor, account P115Account) error {
	if value.Stage == "checking_share" || (value.Stage == "cleaning" && value.CleanupIndex >= 1) {
		return s.completeCleanedBatch(ctx, value)
	}
	if value.Stage == "sharing" {
		logGeneration(ctx, "INFO", "开始读取临时目录顶层内容并创建分享")
		entries, err := s.listRootEntries(ctx, value.BatchDirectoryID, account.Cookie)
		roots := make([]string, 0, len(entries))
		for _, entry := range entries {
			roots = append(roots, entry.ID)
		}
		if err != nil || len(roots) == 0 {
			if err == nil {
				err = errors.New("临时目录中没有可分享的条目")
			}
			return s.failAt(ctx, value, "sharing", "读取临时目录失败："+err.Error())
		}
		if len(roots) > 1000 {
			return s.failAt(ctx, value, "sharing", "单次分享最多支持 1000 个顶层条目")
		}
		if err := s.Accounts.ValidatePrimary(value.AccountID); err != nil {
			return s.failAt(ctx, value, "sharing", err.Error())
		}
		// Persist the uncertainty boundary before a non-idempotent share call.
		value.Stage = "sharing_legacy"
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
		share, err := s.Client.SendShare(ctx, roots, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "blocked", "创建批次分享结果不确定，已停止："+err.Error())
		}
		password := value.SharePassword
		if password == "" {
			password, err = newSharePassword("")
			if err != nil {
				return s.fail(ctx, value, err.Error())
			}
		}
		value.ShareURL, value.SharePassword, value.Stage = share.URL, password, "configuring"
		logGeneration(ctx, "INFO", fmt.Sprintf("分享创建完成：分享码=%s，顶层条目=%d", share.Code, len(roots)))
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
	}
	share, err := p115.ParseShareURL(value.ShareURL)
	if err != nil {
		return s.fail(ctx, value, "已创建分享记录无效，未清理源文件")
	}
	if value.Stage == "configuring" {
		logGeneration(ctx, "INFO", "开始设置长期分享与提取码")
		if err := s.Accounts.ValidatePrimary(value.AccountID); err != nil {
			return s.failAt(ctx, value, "configuring", err.Error())
		}
		if err := s.Client.ConfigureShare(ctx, share, value.SharePassword, account.Cookie); err != nil {
			return s.failAt(ctx, value, "configuring", "设置长期分享失败，源文件已保留："+err.Error())
		}
		value.ShareURL, value.Stage = share.URL, "verifying"
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
	}
	share, _ = p115.ParseShareURL(value.ShareURL)
	if value.Stage == "verifying" && s.Tasks != nil {
		value.Stage = "generating"
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
	}
	if value.Stage == "verifying" {
		logGeneration(ctx, "INFO", "开始读取分享快照，使用本批次固定账号凭据")
		manifest, mediaID, reviewing, err := s.waitShareSnapshot(ctx, value, share, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "verifying", "读取分享快照未完成，临时目录已保留："+err.Error(), err)
		}
		if value.BatchFingerprint != "" && !manifestMatchesFingerprint(manifest, value.BatchFingerprint) {
			return s.failAt(ctx, value, "verifying", "分享快照内容尚未与本批文件一致，临时目录已保留，请稍后重试")
		}
		if mediaID == "" {
			return s.failAt(ctx, value, "verifying", "分享中没有符合条件的视频，源文件已保留")
		}
		if _, err := s.Client.DownloadURL(ctx, share, mediaID, account.Cookie); err != nil {
			return s.failAt(ctx, value, "verifying", "分享文件暂时无法读取，源文件已保留："+err.Error())
		}
		value.ShareReviewing = reviewing
		value.Stage = "generating"
		logGeneration(ctx, "INFO", "分享文件可读性验证通过")
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
	}
	if value.Stage == "generating" {
		if s.Tasks != nil {
			ready, err := s.sharedGeneration(ctx, value)
			if err != nil {
				return err
			}
			if !ready {
				return nil
			}
		} else {
			generationStarted := time.Now()
			logGeneration(ctx, "INFO", "开始生成 STRM，输出目录："+value.OutputDir)
			input := s.generationInput(*value, value.ShareURL)
			settings := AutomationSettings{AccountID: value.AccountID, ParentCID: "0", Generation: input, ReviewRetryMinutes: 30, ReviewRetryMax: 6}
			generationCtx := context.WithValue(ctx, automationContextKey{}, settings)
			generationCtx = context.WithValue(generationCtx, generationCookieKey{}, account.Cookie)
			generationCtx = WithGenerationSource(generationCtx, "目录监控")
			result, err := s.Generator.Generate(generationCtx, input)
			if result != nil {
				logGeneration(ctx, "INFO", fmt.Sprintf("STRM 生成结果：写入 %d，失败 %d；元数据写入 %d，失败 %d；耗时 %.2f 秒", result.Written, result.Failed, result.MetadataWritten, result.MetadataFailed, time.Since(generationStarted).Seconds()))
			}
			if err != nil || result == nil || result.Failed != 0 || result.MetadataFailed != 0 {
				if err == nil {
					err = errors.New("STRM 或元数据没有全部生成成功")
				}
				return s.failAt(ctx, value, "generating", "STRM 生成未完成，源文件已保留："+err.Error(), err)
			}
		}
		if value.BatchFingerprint != "" {
			manifest, _, err := s.scanDrive(ctx, value.BatchDirectoryID, nil, account.Cookie)
			if err != nil || (value.BatchFingerprint != "" && !manifestMatchesFingerprint(manifest, value.BatchFingerprint)) {
				if err == nil {
					err = errors.New("冻结批次内容在处理期间发生变化")
				}
				return s.failAt(ctx, value, "generating", "STRM 已生成，但源目录内容变化，未执行清理："+err.Error())
			}
		}
		value.Stage = "cleaning"
		logGeneration(ctx, "INFO", "STRM 生成完成，准备清理临时目录")
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
	}
	if value.Stage == "cleaning" {
		logGeneration(ctx, "INFO", "开始清理临时目录（移入 115 回收站）")
		for value.CleanupIndex < 1 {
			id := value.BatchDirectoryID
			if id == "" || id == "0" || id == value.SourceCID || id == s.sharedParentCID(value) {
				return s.failAt(ctx, value, "blocked", "清理目录归属无效，已停止")
			}
			exists, err := s.driveRootContains(ctx, s.sharedParentCID(value), id, account.Cookie)
			if err != nil {
				return s.failAt(ctx, value, "cleaning", "核对待清理条目失败："+err.Error())
			}
			if !exists {
				missing, missingErr := s.directoryMissing(ctx, id, account.Cookie)
				if missingErr != nil || !missing {
					return s.failAt(ctx, value, "cleaning", "临时目录不在原位置，无法确认已删除；已保留批次，请核对残留")
				}
				value.CleanupIndex++
				if err := s.checkpoint(ctx, value); err != nil {
					return err
				}
				continue
			}
			// This check also runs after restart or retry of the cleaning stage.
			manifest, _, verifyErr := s.scanDrive(ctx, id, nil, account.Cookie)
			if verifyErr != nil {
				return s.failAt(ctx, value, "cleaning", "清理前核对临时目录失败："+verifyErr.Error())
			}
			if value.BatchFingerprint == "" || !manifestMatchesFingerprint(manifest, value.BatchFingerprint) {
				return s.failAt(ctx, value, "cleaning", "临时目录内容已变化，已停止清理，请核对残留")
			}
			if err := s.Accounts.ValidatePrimary(value.AccountID); err != nil {
				return s.failAt(ctx, value, "cleaning", err.Error())
			}
			if err := s.Client.DeleteEntries(ctx, []string{id}, account.Cookie); err != nil {
				return s.failAt(ctx, value, "cleaning", "STRM 已生成，但部分源文件移入回收站失败："+err.Error())
			}
			stillPresent, verifyErr := s.driveRootContains(ctx, s.sharedParentCID(value), id, account.Cookie)
			if verifyErr != nil || stillPresent {
				return s.failAt(ctx, value, "cleaning", "清理响应已收到，但目录移除结果尚未确认，请核对残留")
			}
			value.CleanupIndex = 1
			if err := s.checkpoint(ctx, value); err != nil {
				return err
			}
		}
		logGeneration(ctx, "INFO", "临时目录清理完成")
		return s.completeCleanedBatch(ctx, value)
	}
	return nil
}

// completeCleanedBatch is a local-only terminal transition. A persisted cleanup
// receipt is required; the old verification stage alone is not proof of cleanup.
func (s *FolderMonitorService) completeCleanedBatch(ctx context.Context, value *model.FolderMonitor) error {
	if value.CleanupIndex < 1 {
		return s.failAt(ctx, value, "blocked", "旧任务缺少清理完成记录，请人工核对；可结束本次任务解除占用")
	}
	value.Stage, value.LastError, value.TaskPending = "completed", "", false
	value.StableFingerprint, value.StableScans = "", 0
	value.BatchIDs, value.BatchFingerprint = nil, ""
	value.BatchDirectoryID, value.BatchDirectoryName, value.BatchMoveIndex, value.BatchParentCID = "", "", 0, ""
	value.BatchCreationPending = false
	value.CleanupIndex = 0
	value.ShareReviewing = false
	value.ScheduleMessage = "本批处理完成，立即检查下一批"
	value.NextScanAt = time.Now().UTC()
	return s.checkpoint(ctx, value)
}

func (s *FolderMonitorService) listRootEntries(ctx context.Context, cid, cookie string) ([]p115.ShareFile, error) {
	return listDriveEntries(ctx, s.Client, cid, cookie)
}

type drivePageLister interface {
	DriveFiles(context.Context, string, int, string) (*p115.SharePage, error)
}

func listDriveEntries(ctx context.Context, client drivePageLister, cid, cookie string) ([]p115.ShareFile, error) {
	entries := make([]p115.ShareFile, 0)
	for offset := 0; ; {
		page, err := client.DriveFiles(ctx, cid, offset, cookie)
		if err != nil {
			return nil, err
		}
		if page == nil || page.Count < 0 || len(page.Files) == 0 && offset < page.Count {
			return nil, errors.New("115 收件目录分页不完整")
		}
		entries = append(entries, page.Files...)
		if len(entries) > 20000 {
			return nil, errors.New("目录条目超过 20000 个")
		}
		offset += len(page.Files)
		if offset >= page.Count {
			return entries, nil
		}
	}
}

func (s *FolderMonitorService) driveRootContains(ctx context.Context, cid, target, cookie string) (bool, error) {
	for offset := 0; ; {
		page, err := s.Client.DriveFiles(ctx, cid, offset, cookie)
		if err != nil {
			return false, err
		}
		if page == nil || page.Count < 0 || len(page.Files) == 0 && offset < page.Count {
			return false, errors.New("115 目录分页不完整")
		}
		for _, file := range page.Files {
			if file.ID == target {
				return true, nil
			}
		}
		offset += len(page.Files)
		if offset >= page.Count {
			return false, nil
		}
	}
}

func (s *FolderMonitorService) checkpoint(ctx context.Context, value *model.FolderMonitor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.Store.GetFolderMonitor(ctx, value.ID)
	if err != nil {
		return err
	}
	if value.ScheduleVersion != current.ScheduleVersion {
		value.Enabled, value.IntervalSeconds, value.NextScanAt = current.Enabled, current.IntervalSeconds, current.NextScanAt
		value.ScheduleVersion = current.ScheduleVersion
		value.SchedulePaused, value.ScheduleMessage = current.SchedulePaused, current.ScheduleMessage
		value.StableFingerprint, value.StableScans = current.StableFingerprint, current.StableScans
	}
	if (!value.Enabled || value.SchedulePaused != "") && !value.TaskPending {
		value.NextScanAt = time.Time{}
	}
	if value.Enabled && value.SchedulePaused == "" && value.NextScanAt.IsZero() && !value.TaskPending && value.BatchDirectoryID == "" && monitorIdleStage(value.Stage) {
		value.NextScanAt = time.Now().UTC().Add(time.Duration(value.IntervalSeconds) * time.Second)
	}
	recordFolderRun(value)
	value.UpdatedAt = time.Now().UTC()
	return s.Store.SaveFolderMonitor(ctx, *value)
}

func (s *FolderMonitorService) fail(ctx context.Context, value *model.FolderMonitor, message string) error {
	if value.BatchDirectoryID != "" {
		return s.failAt(ctx, value, "blocked", message)
	}
	return s.failAt(ctx, value, "error", message)
}

func (s *FolderMonitorService) failAt(ctx context.Context, value *model.FolderMonitor, stage, message string, causes ...error) error {
	cookie, _ := ctx.Value(generationCookieKey{}).(string)
	message = monitorLogDetail(message, cookie)
	value.TaskPending = false
	value.Stage, value.LastError = stage, message
	if value.Enabled {
		value.SchedulePaused = "上次任务未完成，请核对后重新开启自动执行"
	}
	value.NextScanAt = time.Time{}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := errors.New(message)
	if len(causes) > 0 {
		err = &folderMonitorFailure{message: message, cause: errors.Join(causes...)}
	}
	if saveErr := s.checkpoint(saveCtx, value); saveErr != nil {
		return errors.Join(err, saveErr)
	}
	return err
}

func (s *FolderMonitorService) scanDrive(ctx context.Context, cid string, selected []string, cookie string) ([]folderManifestEntry, []string, error) {
	selectedSet := make(map[string]bool, len(selected))
	for _, id := range selected {
		selectedSet[id] = true
	}
	manifest := make([]folderManifestEntry, 0)
	roots := make([]string, 0)
	entries, pages := 0, 0
	var walk func(string, string, int, bool) error
	walk = func(folderID, parent string, depth int, root bool) error {
		if depth > 20 {
			return errors.New("目录层级超过 20 层")
		}
		for offset := 0; ; {
			pages++
			if pages > 500 {
				return errors.New("目录分页超过 500 页")
			}
			page, err := s.Client.DriveFiles(ctx, folderID, offset, cookie)
			if err != nil {
				return err
			}
			if page == nil || page.Count < 0 || len(page.Files) == 0 && offset < page.Count {
				return errors.New("115 目录分页不完整")
			}
			for _, file := range page.Files {
				entries++
				if entries > 20000 {
					return errors.New("目录条目超过 20000 个")
				}
				if root && len(selectedSet) > 0 && !selectedSet[file.ID] {
					continue
				}
				if root {
					roots = append(roots, file.ID)
				}
				relative := path.Join(parent, file.Name)
				manifest = append(manifest, folderManifestEntry{Path: relative, ID: file.ID, Size: file.Size, Directory: file.Directory})
				if file.Directory {
					if err := walk(file.ID, relative, depth+1, false); err != nil {
						return err
					}
				}
			}
			offset += len(page.Files)
			if offset >= page.Count {
				break
			}
		}
		return nil
	}
	if err := walk(cid, "", 0, true); err != nil {
		return nil, nil, err
	}
	return manifest, roots, nil
}

func (s *FolderMonitorService) scanShare(ctx context.Context, value *model.FolderMonitor, share *p115.ShareInfo, cookie string) ([]folderManifestEntry, string, bool, error) {
	manifest := make([]folderManifestEntry, 0)
	mediaID := ""
	reviewing := false
	entries, pages := 0, 0
	var walk func(string, string, int) error
	walk = func(cid, parent string, depth int) error {
		if depth > 20 {
			return errors.New("分享目录层级超过 20 层")
		}
		for offset := 0; ; {
			pages++
			if pages > 500 {
				return errors.New("分享分页超过 500 页")
			}
			page, err := s.Client.List(ctx, share, cid, offset, cookie)
			if err != nil {
				return err
			}
			if page == nil || page.Count < 0 || len(page.Files) == 0 && offset < page.Count {
				return errors.New("分享分页不完整")
			}
			reviewing = reviewing || page.UnderReview
			for _, file := range page.Files {
				entries++
				if entries > 20000 {
					return errors.New("分享条目超过 20000 个")
				}
				relative := path.Join(parent, file.Name)
				manifest = append(manifest, folderManifestEntry{Path: relative, ID: file.ID, Size: file.Size, Directory: file.Directory})
				if file.Directory {
					if err := walk(file.ID, relative, depth+1); err != nil {
						return err
					}
				} else if mediaID == "" && monitorVideoFile(*value, file.Name) && file.Size >= value.MinSizeMB*(1<<20) && (value.Recursive || depth == 0) {
					mediaID = file.ID
				}
			}
			offset += len(page.Files)
			if offset >= page.Count {
				break
			}
		}
		return nil
	}
	if err := walk("0", "", 0); err != nil {
		return nil, "", false, err
	}
	return manifest, mediaID, reviewing, nil
}

func monitorVideoFile(value model.FolderMonitor, name string) bool {
	extension := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	for _, item := range strings.Split(normalizedExtensionList(value.VideoExtensions, DefaultVideoExtensions), ",") {
		if strings.TrimSpace(strings.TrimPrefix(strings.ToLower(item), ".")) == extension {
			return true
		}
	}
	return false
}

func manifestFingerprint(entries []folderManifestEntry) string {
	return "v2:" + manifestFingerprintHash(entries, false, false)
}

// manifestMatchesFingerprint supports checkpoints written before the v2
// normalization. The old drive fingerprint included directory sizes, which
// are represented as zero by /files but as aggregate bytes by /share/snap.
// File sizes remain part of both formats and are never relaxed.
func manifestMatchesFingerprint(entries []folderManifestEntry, expected string) bool {
	if expected == manifestFingerprint(entries) {
		return true
	}
	if strings.HasPrefix(expected, "v2:") {
		return false
	}
	return expected == manifestFingerprintHash(entries, true, false) ||
		expected == manifestFingerprintHash(entries, true, true)
}

func manifestFingerprintHash(entries []folderManifestEntry, includeDirectorySize, zeroDirectorySize bool) string {
	items := append([]folderManifestEntry(nil), entries...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Path != items[j].Path {
			return items[i].Path < items[j].Path
		}
		return items[i].ID < items[j].ID
	})
	h := sha256.New()
	for _, item := range items {
		size := item.Size
		if item.Directory && !includeDirectorySize || item.Directory && zeroDirectorySize {
			size = 0
		}
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00%t\n", item.Path, item.ID, size, item.Directory)
	}
	return hex.EncodeToString(h.Sum(nil))
}

var _ FolderMonitorStore = (*storage.Store)(nil)
