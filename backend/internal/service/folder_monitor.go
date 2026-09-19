package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
// drains the inbox into an isolated temporary directory, then shares that
// directory's direct children. Cookies exist only in the current execution
// context.
type FolderMonitorService struct {
	Store     FolderMonitorStore
	Accounts  *P115Accounts
	Client    FolderMonitorClient
	Generator FolderMonitorGenerator
	Root      string
	Logs      *GenerationLogs
	// SharedSettings supplies the explicitly selected transfer account, parent
	// directory and optional fixed share password. It is read when a batch
	// starts; an in-flight batch keeps its persisted account and password.
	SharedSettings func() AutomationSettings
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
	if err := s.Accounts.ValidateSelection(strings.TrimSpace(value.AccountID)); err != nil {
		return err
	}
	if value.SourceCID == "" || value.SourceCID == "0" {
		return generationInputError("请选择非根目录作为监控收件箱")
	}
	if _, err := strconv.ParseUint(value.SourceCID, 10, 64); err != nil {
		return generationInputError("监控目录编号无效")
	}
	if value.IntervalSeconds < 30 || value.IntervalSeconds > 86400 {
		return generationInputError("扫描间隔必须为 30 秒到 24 小时")
	}
	return ValidateGenerationOptions(s.Root, s.generationInput(value, ""))
}

func (s *FolderMonitorService) Save(ctx context.Context, value model.FolderMonitor) (model.FolderMonitor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value.Name = strings.TrimSpace(value.Name)
	value.AccountID = strings.TrimSpace(value.AccountID)
	value.SourceCID = strings.TrimSpace(value.SourceCID)
	value.SourcePath = strings.TrimSpace(value.SourcePath)
	if value.IntervalSeconds == 0 {
		value.IntervalSeconds = 300
	}
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
		value.Stage, value.NextScanAt = "waiting", now.Add(time.Duration(value.IntervalSeconds)*time.Second)
	} else {
		existing, err := s.Store.GetFolderMonitor(ctx, value.ID)
		if err != nil {
			return model.FolderMonitor{}, err
		}
		if s.running[value.ID] || existing.TaskPending || existing.BatchDirectoryID != "" || !monitorEditable(existing.Stage) {
			return model.FolderMonitor{}, generationInputError("当前批次正在处理，不能修改监控配置")
		}
		copyMonitorRuntime(&value, existing)
		// Scheduling has its own endpoint; a stale settings form must not undo it.
		value.Enabled = existing.Enabled
		if value.IntervalSeconds != existing.IntervalSeconds {
			value.NextScanAt = now.Add(time.Duration(value.IntervalSeconds) * time.Second)
		}
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
	value.AccountID = strings.TrimSpace(settings.AccountID)
	value.Name = "收件箱监控"
	value.BaseURL = settings.Generation.BaseURL
	value.OutputDir = settings.Generation.OutputDir
	value.VideoExtensions = settings.Generation.VideoExtensions
	value.MetadataExtensions = settings.Generation.MetadataExtensions
	value.MinSizeMB = settings.Generation.MinSizeMB
	value.Recursive = settings.Generation.Recursive
	return s.Save(ctx, value)
}

func monitorEditable(stage string) bool {
	return stage == "waiting" || stage == "empty" || stage == "completed" || stage == "error"
}

func copyMonitorRuntime(dst *model.FolderMonitor, src model.FolderMonitor) {
	dst.LogTaskID, dst.LogAttempt = src.LogTaskID, src.LogAttempt
	dst.TaskPending, dst.TaskTrigger = src.TaskPending, src.TaskTrigger
	dst.Stage, dst.ShareURL, dst.SharePassword, dst.ShareReviewing = src.Stage, src.ShareURL, src.SharePassword, src.ShareReviewing
	dst.BatchIDs, dst.BatchFingerprint = append([]string(nil), src.BatchIDs...), src.BatchFingerprint
	dst.BatchDirectoryID, dst.BatchDirectoryName, dst.BatchMoveIndex = src.BatchDirectoryID, src.BatchDirectoryName, src.BatchMoveIndex
	dst.BatchParentCID = src.BatchParentCID
	dst.BatchAccountRevision, dst.BatchCredentialHash, dst.CleanupIndex = src.BatchAccountRevision, src.BatchCredentialHash, src.CleanupIndex
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
	if s.running[id] || value.TaskPending || (!monitorEditable(value.Stage) && value.Stage != "blocked") {
		return generationInputError("当前批次正在处理，不能删除监控配置")
	}
	return s.Store.DeleteFolderMonitor(ctx, id)
}

var ErrFolderMonitorBusy = errors.New("当前任务已排队或正在处理")

// SetSchedule only controls future timed checks; it never cancels a batch.
func (s *FolderMonitorService) SetSchedule(ctx context.Context, id string, enabled bool) (model.FolderMonitor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.Store.GetFolderMonitor(ctx, id)
	if err != nil {
		return model.FolderMonitor{}, err
	}
	if enabled && !value.Enabled {
		if err := s.validate(value); err != nil {
			return model.FolderMonitor{}, err
		}
		value.NextScanAt = time.Now().UTC().Add(time.Duration(value.IntervalSeconds) * time.Second)
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
	if value.Stage == "blocked" || value.Stage == "sharing_legacy" || (value.Stage == "error" && value.BatchDirectoryID != "") {
		return generationInputError("当前批次需要人工核对，不能直接重试")
	}
	if value.BatchDirectoryID == "" && monitorIdleStage(value.Stage) {
		value.Stage, value.LastError = "waiting", ""
	}
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
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.runDue(ctx); err != nil && ctx.Err() == nil && s.Logs != nil {
			s.Logs.Append("STRM", "目录监控", "目录监控", "ERROR", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

func (s *FolderMonitorService) runDue(ctx context.Context) error {
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
			level, message := "INFO", "本次执行结束"
			if latest.Stage == "completed" {
				message = "整批处理完成：STRM 已生成，临时目录已清理，分享复核通过"
			} else if latest.Stage == "empty" {
				level, message = "DEBUG", "监控目录为空，本次检查结束"
			}
			s.Logs.Append("STRM", latest.LogTaskID, "目录监控", level, fmt.Sprintf("%s，第 %d 次执行耗时 %.2f 秒", message, latest.LogAttempt, time.Since(started).Seconds()))
		}
	}
	return nil
}

func monitorDue(value model.FolderMonitor, now time.Time) bool {
	if value.Stage == "blocked" {
		return false
	}
	// One-shot requests and interrupted, nonfailed batches survive disabling
	// the timer and service restarts. Failed batches retry only on a new trigger.
	return value.TaskPending || (value.BatchDirectoryID != "" && value.LastError == "") ||
		(value.Enabled && !value.NextScanAt.After(now))
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
		value.TaskTrigger = "scheduled"
	}
	if value.LogTaskID == "" || value.BatchDirectoryID == "" && (value.Stage == "waiting" || value.Stage == "empty" || value.Stage == "completed") {
		var nonce [12]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			s.mu.Unlock()
			return fmt.Errorf("创建监控日志编号: %w", err)
		}
		value.LogTaskID, value.LogAttempt = "STRM-MON-"+hex.EncodeToString(nonce[:]), 0
	}
	value.LogAttempt++
	value.TaskPending, value.LastError, value.UpdatedAt = true, "", time.Now().UTC()
	if err := s.Store.SaveFolderMonitor(root, value); err != nil {
		s.mu.Unlock()
		return err
	}
	s.running[id] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.running, id); s.mu.Unlock() }()
	root = context.WithValue(root, generationLogKey{}, generationLogger{store: s.Logs, task: value.LogTaskID, source: "目录监控", redact: func(message string) string {
		return generationErrorDetail(errors.New(message), GenerateInput{})
	}})
	logGeneration(root, "INFO", fmt.Sprintf("开始目录监控处理：触发=%s，第 %d 次执行，阶段=%s，目录=%s（%s），账号编号=%s", value.TaskTrigger, value.LogAttempt, value.Stage, value.SourcePath, value.SourceCID, value.AccountID))
	account, err := s.Accounts.Get(value.AccountID)
	if err != nil || account.Cookie == "" {
		return s.fail(root, &value, "所选 115 账号已删除或未登录")
	}
	ctx, cancel := context.WithTimeout(root, 10*time.Minute)
	defer cancel()
	ctx = context.WithValue(ctx, generationCookieKey{}, account.Cookie)
	ctx = context.WithValue(ctx, generationLogKey{}, generationLogger{store: s.Logs, task: value.LogTaskID, source: "目录监控", redact: func(message string) string {
		return monitorLogDetail(message, account.Cookie)
	}})
	ctx = p115.WithRequestProgress(ctx, func(message string) { logGeneration(ctx, "WARNING", message) })
	logGeneration(ctx, "INFO", "已固定本次执行的账号凭据："+account.Name)
	if value.BatchAccountRevision != 0 && (value.BatchAccountRevision != account.Revision || value.BatchCredentialHash != cookieHash(account.Cookie)) {
		return s.failAt(ctx, &value, "blocked", "本批次启动后账号凭据已经变化，已停止；请人工核对")
	}
	switch value.Stage {
	case "waiting", "empty", "completed", "error":
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
	case "waiting", "empty", "completed", "error":
		return true
	default:
		return false
	}
}

func (s *FolderMonitorService) discover(ctx context.Context, value *model.FolderMonitor, account P115Account) error {
	logGeneration(ctx, "INFO", "开始检查监控目录")
	// Capture shared feature settings once, before any remote work. Never
	// silently switch to another account when the stored binding is invalid.
	settings := s.sharedSettings(value)
	if settings.AccountID == "" || settings.AccountID != value.AccountID {
		return s.fail(ctx, value, "请在 115 分享设置中确认账号，并重新保存目录自动分享配置")
	}
	if s.SharedSettings != nil {
		value.BaseURL, value.OutputDir = settings.Generation.BaseURL, settings.Generation.OutputDir
		value.VideoExtensions, value.MetadataExtensions = settings.Generation.VideoExtensions, settings.Generation.MetadataExtensions
		value.MinSizeMB, value.Recursive = settings.Generation.MinSizeMB, settings.Generation.Recursive
	}
	if err := s.validate(*value); err != nil {
		return s.fail(ctx, value, err.Error())
	}
	roots, err := s.listRootEntries(ctx, value.SourceCID, account.Cookie)
	if err != nil {
		return s.fail(ctx, value, "扫描监控目录失败："+err.Error())
	}
	now := time.Now().UTC()
	value.LastScanAt, value.NextScanAt = now, now.Add(time.Duration(value.IntervalSeconds)*time.Second)
	if len(roots) == 0 {
		value.TaskPending = false
		value.Stage, value.StableFingerprint, value.StableScans, value.LastError = "empty", "", 0, ""
		return s.checkpoint(ctx, value)
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
	logGeneration(ctx, "INFO", fmt.Sprintf("发现 %d 个顶层条目，开始创建临时目录：%s，父目录=%s", len(roots), name, parent))
	directoryID, err := s.Client.CreateDirectory(ctx, parent, name, account.Cookie)
	if err != nil {
		return s.fail(ctx, value, "创建监控批次目录失败："+err.Error())
	}
	// Root IDs are intentionally not persisted. The temporary directory is the
	// recovery boundary; the inbox is drained again after a restart.
	value.BatchIDs, value.BatchFingerprint = nil, ""
	value.BatchDirectoryID, value.BatchDirectoryName, value.BatchMoveIndex = directoryID, name, 0
	value.BatchParentCID = parent
	value.BatchAccountRevision, value.BatchCredentialHash = account.Revision, cookieHash(account.Cookie)
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

func (s *FolderMonitorService) moveBatchLegacy(ctx context.Context, value *model.FolderMonitor, account P115Account) error {
	if value.BatchDirectoryID == "" || value.BatchDirectoryID == "0" {
		return s.failAt(ctx, value, "blocked", "监控批次目录编号无效，已停止")
	}
	for value.BatchMoveIndex < len(value.BatchIDs) {
		id := value.BatchIDs[value.BatchMoveIndex]
		inSource, err := s.driveRootContains(ctx, value.SourceCID, id, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "moving", "核对收件目录失败："+err.Error())
		}
		inBatch, err := s.driveRootContains(ctx, value.BatchDirectoryID, id, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "moving", "核对批次目录失败："+err.Error())
		}
		if inSource && !inBatch {
			if err := s.Client.MoveEntries(ctx, []string{id}, value.BatchDirectoryID, account.Cookie); err != nil {
				return s.failAt(ctx, value, "moving", "移动到批次目录失败："+err.Error())
			}
			inBatch, err = s.driveRootContains(ctx, value.BatchDirectoryID, id, account.Cookie)
			if err != nil {
				return s.failAt(ctx, value, "moving", "确认移动结果失败："+err.Error())
			}
		}
		if inSource && !inBatch {
			return s.failAt(ctx, value, "moving", "移动结果无法确认，已暂停避免重复移动")
		}
		if !inSource && !inBatch {
			return s.failAt(ctx, value, "blocked", "批次文件已同时从两处消失："+id)
		}
		value.BatchMoveIndex++
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
	}
	value.Stage, value.StableFingerprint, value.StableScans = "sharing", "", 0
	value.NextScanAt = time.Now().UTC()
	return s.checkpoint(ctx, value)
}

func (s *FolderMonitorService) moveBatch(ctx context.Context, value *model.FolderMonitor, account P115Account) error {
	logGeneration(ctx, "INFO", "开始批量移动监控目录内容到临时目录")
	if value.BatchDirectoryID == "" || value.BatchDirectoryID == "0" {
		return s.failAt(ctx, value, "blocked", "监控批次目录编号无效，已停止")
	}
	for pass := 0; pass < 100; pass++ {
		entries, err := s.listRootEntries(ctx, value.SourceCID, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "moving", "读取收件目录失败："+err.Error())
		}
		if len(entries) == 0 {
			logGeneration(ctx, "INFO", fmt.Sprintf("监控目录已排空，累计移动 %d 个顶层条目；之后到达的内容留给下次处理", value.BatchMoveIndex))
			// The inbox is intentionally fire-and-forget: once this pass is empty,
			// share whatever is currently visible in the temporary directory. A
			// file arriving in the inbox after this point belongs to the next batch.
			value.Stage, value.StableFingerprint, value.StableScans = "sharing", "", 0
			value.NextScanAt = time.Now().UTC()
			return s.checkpoint(ctx, value)
		}
		for start := 0; start < len(entries); start += 1000 {
			end := start + 1000
			if end > len(entries) {
				end = len(entries)
			}
			ids := make([]string, 0, end-start)
			for _, entry := range entries[start:end] {
				ids = append(ids, entry.ID)
			}
			if err := s.Client.MoveEntries(ctx, ids, value.BatchDirectoryID, account.Cookie); err != nil {
				return s.failAt(ctx, value, "moving", "批量移动到临时目录失败："+err.Error())
			}
			value.BatchMoveIndex += len(ids)
			logGeneration(ctx, "INFO", fmt.Sprintf("批量移动完成：本次 %d 个，累计 %d 个顶层条目", len(ids), value.BatchMoveIndex))
			if err := s.checkpoint(ctx, value); err != nil {
				return err
			}
		}
		if pass == 99 {
			return s.failAt(ctx, value, "moving", "收件目录持续有新文件，暂未完成排空")
		}
	}
	return nil
}

func (s *FolderMonitorService) stabilizeBatch(ctx context.Context, value *model.FolderMonitor, account P115Account) error {
	manifest, _, err := s.scanDrive(ctx, value.BatchDirectoryID, nil, account.Cookie)
	if err != nil {
		return s.failAt(ctx, value, "stabilizing", "扫描批次目录失败："+err.Error())
	}
	fingerprint := manifestFingerprint(manifest)
	if value.StableFingerprint != fingerprint {
		value.StableFingerprint, value.StableScans = fingerprint, 1
		value.NextScanAt = time.Now().UTC().Add(15 * time.Second)
		return s.checkpoint(ctx, value)
	}
	value.StableScans++
	if value.StableScans < 2 {
		value.NextScanAt = time.Now().UTC().Add(15 * time.Second)
		return s.checkpoint(ctx, value)
	}
	value.BatchFingerprint, value.Stage, value.NextScanAt = fingerprint, "sharing", time.Now().UTC()
	return s.checkpoint(ctx, value)
}

func (s *FolderMonitorService) resume(ctx context.Context, value *model.FolderMonitor, account P115Account) error {
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
		if err := s.Client.ConfigureShare(ctx, share, value.SharePassword, account.Cookie); err != nil {
			return s.failAt(ctx, value, "configuring", "设置长期分享失败，源文件已保留："+err.Error())
		}
		value.ShareURL, value.Stage = share.URL, "verifying"
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
	}
	share, _ = p115.ParseShareURL(value.ShareURL)
	if value.Stage == "verifying" {
		logGeneration(ctx, "INFO", "开始读取分享快照，使用本批次固定账号凭据")
		_, mediaID, reviewing, err := s.waitShareSnapshot(ctx, value, share, account.Cookie)
		if err != nil {
			return s.failAt(ctx, value, "verifying", "读取分享快照未完成，临时目录已保留："+err.Error(), err)
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
			exists, err := s.driveRootContains(ctx, s.sharedParentCID(value), id, account.Cookie)
			if err != nil {
				return s.failAt(ctx, value, "cleaning", "核对待清理条目失败："+err.Error())
			}
			if !exists {
				value.CleanupIndex++
				if err := s.checkpoint(ctx, value); err != nil {
					return err
				}
				continue
			}
			if err := s.Client.DeleteEntries(ctx, []string{id}, account.Cookie); err != nil {
				return s.failAt(ctx, value, "cleaning", "STRM 已生成，但部分源文件移入回收站失败："+err.Error())
			}
			value.CleanupIndex = 1
			if err := s.checkpoint(ctx, value); err != nil {
				return err
			}
		}
		value.Stage = "checking_share"
		logGeneration(ctx, "INFO", "临时目录清理完成，开始复核分享")
		if err := s.checkpoint(ctx, value); err != nil {
			return err
		}
	}
	if value.Stage == "checking_share" {
		logGeneration(ctx, "INFO", "正在复核清理后的分享可读性")
		manifest, _, reviewing, err := s.waitShareSnapshot(ctx, value, share, account.Cookie)
		if err != nil || (value.BatchFingerprint != "" && !manifestMatchesFingerprint(manifest, value.BatchFingerprint)) {
			if err == nil {
				err = errors.New("清理后分享内容发生变化")
			}
			return s.failAt(ctx, value, "checking_share", "临时目录已清理，但分享复核失败："+err.Error(), err)
		}
		value.ShareReviewing = reviewing
		value.Stage, value.LastError = "completed", ""
		value.TaskPending = false
		value.StableFingerprint, value.StableScans = "", 0
		value.BatchIDs, value.BatchFingerprint = nil, ""
		value.BatchDirectoryID, value.BatchDirectoryName, value.BatchMoveIndex, value.BatchParentCID = "", "", 0, ""
		value.BatchAccountRevision, value.BatchCredentialHash, value.CleanupIndex = 0, "", 0
		value.NextScanAt = time.Now().UTC().Add(time.Duration(value.IntervalSeconds) * time.Second)
		return s.checkpoint(ctx, value)
	}
	return nil
}

func (s *FolderMonitorService) listRootEntries(ctx context.Context, cid, cookie string) ([]p115.ShareFile, error) {
	entries := make([]p115.ShareFile, 0)
	for offset := 0; ; {
		page, err := s.Client.DriveFiles(ctx, cid, offset, cookie)
		if err != nil {
			return nil, err
		}
		if page == nil || page.Count < 0 || len(page.Files) == 0 && offset < page.Count {
			return nil, errors.New("115 收件目录分页不完整")
		}
		entries = append(entries, page.Files...)
		offset += len(page.Files)
		if offset >= page.Count {
			return entries, nil
		}
		if len(entries) > 20000 {
			return nil, errors.New("收件目录条目超过 20000 个")
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
	latest, err := s.Store.GetFolderMonitor(ctx, value.ID)
	if err != nil {
		return err
	}
	// The worker owns batch fields, the schedule endpoint owns Enabled.
	value.Enabled = latest.Enabled
	if value.TaskPending && value.LastError == "" {
		value.NextScanAt = latest.NextScanAt
	}
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
	value.NextScanAt = time.Now().UTC().Add(time.Duration(value.IntervalSeconds) * time.Second)
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
				} else if mediaID == "" && monitorVideoFile(*value, file.Name) {
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
	for _, item := range strings.Split(value.VideoExtensions, ",") {
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
