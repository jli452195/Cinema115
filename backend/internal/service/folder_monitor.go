package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	DeleteEntries(context.Context, []string, string) error
}

// FolderMonitorService 将收件箱的本批条目移入独立目录并创建分享。
type FolderMonitorService struct {
	Store    FolderMonitorStore
	Accounts *P115Accounts
	Client   FolderMonitorClient
	Tasks    *ShareTasks
	Root     string
	Logs     *GenerationLogs
	// 提交任务时固定主账号、分享提取码和可选的生成设置。
	SharedSettings func() AutomationSettings
	ReserveWork    func() (func(), error)
	wake           chan struct{}
	mu             sync.Mutex
	running        map[string]bool
}

func NewFolderMonitorService(store FolderMonitorStore, accounts *P115Accounts, client FolderMonitorClient, root string, logs *GenerationLogs) *FolderMonitorService {
	return &FolderMonitorService{Store: store, Accounts: accounts, Client: client, Root: root, Logs: logs, wake: make(chan struct{}, 1), running: make(map[string]bool)}
}

func (s *FolderMonitorService) SetSharedSettings(provider func() AutomationSettings) {
	s.SharedSettings = provider
}

func (s *FolderMonitorService) generationInput(value model.FolderMonitor, shareURL string) GenerateInput {
	// 分享根目录直接包含本批条目。
	return GenerateInput{URL: shareURL, CID: "0", Mode: "direct", BaseURL: value.BaseURL, OutputDir: value.OutputDir, VideoExtensions: value.VideoExtensions, MetadataExtensions: value.MetadataExtensions, MinSizeMB: value.MinSizeMB, Recursive: value.Recursive}
}

func (s *FolderMonitorService) validate(value model.FolderMonitor) error {
	if s == nil || s.Store == nil || s.Accounts == nil || s.Client == nil {
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
	if cid, err := strconv.ParseUint(value.SourceCID, 10, 64); err != nil || cid == 0 {
		return generationInputError("监控目录编号无效")
	}

	return nil
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
		if s.running[value.ID] || existing.TaskPending || existing.BatchCreationPending || existing.BatchDirectoryID != "" || !monitorIdleStage(existing.Stage) {
			return model.FolderMonitor{}, generationInputError("当前批次正在处理，不能修改监控配置")
		}
		copyMonitorRuntime(&value, existing)
		value.Enabled, value.NextScanAt = false, time.Time{}
		value.ScheduleVersion = existing.ScheduleVersion + 1
		value.SchedulePaused, value.ScheduleMessage = "", ""
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
	applyFolderGenerationOptions(&value, settings.Generation)
	return s.Save(ctx, value)
}

func copyMonitorRuntime(dst *model.FolderMonitor, src model.FolderMonitor) {
	dst.RecentRuns = append([]model.FolderRun(nil), src.RecentRuns...)
	dst.LogTaskID, dst.LogAttempt = src.LogTaskID, src.LogAttempt
	dst.EndedBatches = append([]model.EndedFolderBatch(nil), src.EndedBatches...)
	dst.GenerationTaskID, dst.GenerationSettings = src.GenerationTaskID, append(json.RawMessage(nil), src.GenerationSettings...)
	dst.TaskPending, dst.TaskTrigger = src.TaskPending, src.TaskTrigger
	dst.Stage, dst.ShareURL, dst.SharePassword = src.Stage, src.ShareURL, src.SharePassword
	dst.BatchIDs = append([]string(nil), src.BatchIDs...)
	dst.BatchDirectoryID, dst.BatchDirectoryName, dst.BatchMoveIndex = src.BatchDirectoryID, src.BatchDirectoryName, src.BatchMoveIndex
	dst.BatchCreationPending, dst.RecoveryMessage = src.BatchCreationPending, src.RecoveryMessage
	dst.BatchParentCID = src.BatchParentCID
	dst.CleanupIndex = src.CleanupIndex
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
	if s.running[id] || value.TaskPending || value.BatchCreationPending || value.BatchDirectoryID != "" || !monitorIdleStage(value.Stage) {
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
		if err := s.validate(value); err != nil {
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
		value.ShareURL, value.SharePassword = "", ""
		value.LogTaskID, err = newFolderLogID()
		if err != nil {
			return err
		}
		value.LogAttempt = 0
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
		started := time.Now()
		if err := s.runOne(ctx, value.ID); err != nil {
			// 调度边界统一记录失败；下层只返回错误，避免重复刷屏。
			s.logFailure(ctx, value.ID, err, time.Since(started))
			if ctx.Err() != nil {
				return ctx.Err()
			}
		} else if latest, err := s.Store.GetFolderMonitor(ctx, value.ID); err == nil && latest.LogTaskID != "" {
			if latest.TaskPending && !latest.NextScanAt.IsZero() {
				continue // Waiting for the writer is not a completed execution.
			}
			level, message := "INFO", "本次执行结束"
			if latest.Stage == "completed" {
				message = "分享已保存，本批占用已解除"
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
	return configuredFolderShare(value.Stage) || value.TaskPending || (value.BatchDirectoryID != "" && value.LastError == "")
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
	if value.LogTaskID == "" {
		value.LogTaskID, err = newFolderLogID()
		if err != nil {
			s.mu.Unlock()
			return err
		}
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
	if configuredFolderShare(value.Stage) {
		return s.finishSharedBatch(root, &value)
	}
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
		return s.failAt(ctx, &value, "blocked", "临时目录创建结果尚未确认，请核对残留")
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
		return s.moveAndShare(ctx, &value, account, nil)
	case "stabilizing":
		// 旧批次已搬运完成，可以继续创建分享。
		value.Stage = "sharing"
		return s.resume(ctx, &value, account)
	case "sharing", "configuring":
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
		value.Stage, value.LastError = "empty", ""
		if value.TaskTrigger == "scheduled" {
			value.LogTaskID, value.LogAttempt, value.TaskTrigger = "", 0, ""
			value.ScheduleMessage = "目录为空，等待下次检查"
		}
		return s.checkpoint(ctx, value)
	}
	if len(roots) > 1000 {
		return s.fail(ctx, value, "单次分享最多支持 1000 个顶层条目，请缩小目录范围")
	}
	ids := make([]string, 0, len(roots))
	for _, entry := range roots {
		ids = append(ids, entry.ID)
	}
	password, err := newSharePassword(settings.FixedSharePassword)
	if err != nil {
		return s.fail(ctx, value, err.Error())
	}
	const parent = "0"
	name := batchDirectoryName()
	// 先确认随机目录名未被占用，供创建结果不明时核对归属。
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
	value.BatchIDs = ids
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
	value.BatchDirectoryID, value.BatchCreationPending, value.Stage = directoryID, false, "moving"
	logGeneration(ctx, "INFO", "临时目录创建完成："+directoryID)
	if err := s.checkpoint(ctx, value); err != nil {
		return err
	}
	return s.moveAndShare(ctx, value, account, roots)
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
	return "0"
}
func batchDirectoryName() string {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err == nil {
		return "Cinema115-临时-" + hex.EncodeToString(nonce[:])
	}
	return fmt.Sprintf("Cinema115-临时-%d", time.Now().UnixNano())
}

// moveAndShare 对照固定范围确认搬运结果；重试只移动尚未到位的条目。
func (s *FolderMonitorService) moveAndShare(ctx context.Context, value *model.FolderMonitor, account P115Account, source []p115.ShareFile) error {
	if value.BatchDirectoryID == "" || value.BatchDirectoryID == "0" || value.BatchDirectoryID == value.SourceCID || value.BatchDirectoryID == s.sharedParentCID(value) {
		return s.failAt(ctx, value, "blocked", "批次目录编号无效，已停止")
	}
	if len(value.BatchIDs) == 0 || len(value.BatchIDs) > 1000 {
		return s.failAt(ctx, value, "blocked", "缺少有效的本批搬运范围，请核对临时目录")
	}
	if source == nil {
		var err error
		source, err = s.listRootEntries(ctx, value.SourceCID, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "moving", "读取来源目录失败："+err.Error())
		}
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
	pending := make([]string, 0, len(value.BatchIDs))
	for _, id := range value.BatchIDs {
		if inBatch[id] {
			if inSource[id] {
				return s.failAt(ctx, value, "blocked", "条目同时出现在来源和临时目录，请人工核对")
			}
			continue
		}
		if !inSource[id] {
			return s.failAt(ctx, value, "blocked", "批次条目已从来源和临时目录消失，请人工核对")
		}
		pending = append(pending, id)
	}
	if len(pending) > 0 {
		if err := s.Accounts.ValidatePrimary(value.AccountID); err != nil {
			return s.failAt(ctx, value, "moving", err.Error())
		}
		if err := s.Client.MoveEntries(ctx, pending, value.BatchDirectoryID, account.Cookie); err != nil {
			return s.failAt(ctx, value, "moving", "移动到临时目录失败："+err.Error())
		}
		batch, err = s.listRootEntries(ctx, value.BatchDirectoryID, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "moving", "确认移动结果失败："+err.Error())
		}
		clear(inBatch)
		for _, entry := range batch {
			inBatch[entry.ID] = true
		}
		for _, id := range value.BatchIDs {
			if !inBatch[id] {
				return s.failAt(ctx, value, "moving", "移动结果尚未确认，请稍后重试")
			}
		}
	}
	value.BatchMoveIndex, value.Stage = len(value.BatchIDs), "sharing"
	if err := s.checkpoint(ctx, value); err != nil {
		return err
	}
	logGeneration(ctx, "INFO", fmt.Sprintf("已移入本批 %d 个顶层条目", value.BatchMoveIndex))
	return s.resume(ctx, value, account)
}

func (s *FolderMonitorService) resume(ctx context.Context, value *model.FolderMonitor, account P115Account) error {
	if value.Stage == "sharing" {
		if len(value.BatchIDs) == 0 || value.BatchMoveIndex != len(value.BatchIDs) {
			return s.failAt(ctx, value, "blocked", "本批搬运范围尚未确认，请核对临时目录")
		}
		if err := s.Accounts.ValidatePrimary(value.AccountID); err != nil {
			return s.failAt(ctx, value, "sharing", err.Error())
		}
		// 分享接口不可幂等重放，先保存请求即将发出的状态。
		value.Stage = "sharing_legacy"
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
		share, err := s.Client.SendShare(ctx, value.BatchIDs, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "blocked", "创建批次分享结果待核对："+err.Error())
		}
		value.ShareURL, value.Stage = share.URL, "configuring"
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.checkpoint(saveCtx, value); err != nil {
			return err
		}
		logGeneration(ctx, "INFO", fmt.Sprintf("已创建分享，顶层条目=%d", len(value.BatchIDs)))
	}
	share, err := p115.ParseShareURL(value.ShareURL)
	if err != nil {
		return s.failAt(ctx, value, "blocked", "已创建分享的链接无效，请核对 115 分享记录")
	}
	if err := s.Accounts.ValidatePrimary(value.AccountID); err != nil {
		return s.failAt(ctx, value, "configuring", err.Error())
	}
	if err := s.Client.ConfigureShare(ctx, share, value.SharePassword, account.Cookie); err != nil {
		return s.failAt(ctx, value, "configuring", "设置长期分享失败，源文件已保留："+err.Error())
	}
	value.ShareURL, value.Stage = share.URL, "shared"
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.checkpoint(saveCtx, value); err != nil {
		return err
	}
	return s.finishSharedBatch(ctx, value)
}

func configuredFolderShare(stage string) bool {
	switch stage {
	case "shared", "verifying", "generating", "cleaning", "checking_share":
		return true
	default:
		return false
	}
}

func (s *FolderMonitorService) finishSharedBatch(ctx context.Context, value *model.FolderMonitor) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := p115.ParseShareURL(value.ShareURL); err != nil {
		if value.CleanupIndex == 0 {
			return s.failAt(ctx, value, "blocked", "缺少已创建分享的有效链接，请核对 115 分享记录")
		}
		value.ShareURL = ""
	}
	// STRM 交给公共队列，目录监控只保存已创建的分享。
	if value.ShareURL != "" && value.Stage != "cleaning" && value.Stage != "checking_share" {
		if err := s.queueFolderGeneration(ctx, value); err != nil {
			cookie, _ := ctx.Value(generationCookieKey{}).(string)
			detail := monitorLogDetail(err.Error(), cookie)
			s.Logs.Append("STRM", value.LogTaskID, "目录分享", "WARNING", "分享已创建，提交 STRM 任务失败："+detail)
		}
	}
	value.Stage, value.LastError, value.TaskPending = "completed", "", false
	recordFolderRun(value)
	clearFolderBatch(value)
	value.SchedulePaused = ""
	value.ScheduleMessage = "本批分享完成，立即检查下一批"
	value.NextScanAt = time.Now().UTC()
	// 入队错误不占用此批。即使入队超时，也给分享结果独立的落盘时间。
	saveCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	return s.checkpoint(saveCtx, value)
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

func (s *FolderMonitorService) failAt(ctx context.Context, value *model.FolderMonitor, stage, message string) error {
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
	if saveErr := s.checkpoint(saveCtx, value); saveErr != nil {
		return errors.Join(err, saveErr)
	}
	return err
}

var _ FolderMonitorStore = (*storage.Store)(nil)
