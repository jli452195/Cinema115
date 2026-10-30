package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"strm115/backend/internal/strm"
)

var ErrDriveSTRMBusy = errors.New("目录 STRM 正在扫描，请完成后再修改")

type DriveSTRMStore interface {
	SaveDriveSTRM(context.Context, model.DriveSTRM) error
	SaveDriveSTRMConfiguration(context.Context, model.DriveSTRM, bool) error
	GetDriveSTRM(context.Context, string) (model.DriveSTRM, error)
	ListDriveSTRM(context.Context) ([]model.DriveSTRM, error)
	DeleteDriveSTRM(context.Context, string) error
	ListDriveSTRMDirectories(context.Context, string) ([]model.DriveSTRMFile, error)
	SaveDriveSTRMFiles(context.Context, []model.DriveSTRMFile) error
	ListDriveSTRMFilesByIDs(context.Context, string, []string) ([]model.DriveSTRMFile, error)
	ListDriveSTRMFilesByPaths(context.Context, string, []string) ([]model.DriveSTRMFile, error)
	SaveDriveSTRMExport(context.Context, string, string) error
	ListDriveSTRMExports(context.Context) (map[string]string, error)
	DeleteDriveSTRMExport(context.Context, string) error
	LoadDriveSTRMCursor(context.Context, string) (string, error)
	SaveDriveSTRMCursor(context.Context, string, string) error
}

type DriveSTRMClient interface {
	DriveFiles(context.Context, string, int, string) (*p115.SharePage, error)
	ReadDirectory(context.Context, string, string) (p115.DriveDirectory, error)
}

type driveSTRMMetadataReader interface {
	ReadDriveMetadata(context.Context, p115.ShareFile, string) ([]byte, error)
}

// DriveSTRMService never changes source files and has no anime dependencies.
// Only its own generated directory-tree exports are created and cleaned up.
// A single tracked worker freezes credentials for each scan. Life events only
// enqueue known directory scopes; full reconciliation repairs missed changes.
type DriveSTRMService struct {
	Store      DriveSTRMStore
	Accounts   *P115Accounts
	Client     DriveSTRMClient
	Root       string
	mu         sync.Mutex
	wake       chan struct{}
	active     string
	cancel     context.CancelFunc
	running    bool
	lastPicked string
}

const driveSTRMReconcileInterval = 24 * time.Hour
const driveSTRMFullInterval = 7 * 24 * time.Hour

func NewDriveSTRMService(store DriveSTRMStore, accounts *P115Accounts, client DriveSTRMClient, root string) *DriveSTRMService {
	return &DriveSTRMService{Store: store, Accounts: accounts, Client: client, Root: root, wake: make(chan struct{}, 1)}
}

func (s *DriveSTRMService) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *DriveSTRMService) List(ctx context.Context) ([]model.DriveSTRM, error) {
	return s.Store.ListDriveSTRM(ctx)
}

func driveSTRMExtensions(raw string) (map[string]bool, error) {
	return parseMetadataExtensions(raw, nil)
}

func (s *DriveSTRMService) Save(ctx context.Context, value model.DriveSTRM) (model.DriveSTRM, error) {
	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" || len([]rune(value.Name)) > 80 || strings.ContainsAny(value.Name, "\r\n\x00") {
		return value, generationInputError("名称应为 1 到 80 个字符")
	}
	if value.AccountID == "" || !organizeID(value.SourceCID) {
		return value, generationInputError("请使用当前主账号选择来源目录")
	}
	account, err := s.Accounts.Primary(value.AccountID)
	if err != nil {
		return value, err
	}
	base, err := ValidateBaseURL(value.BaseURL)
	if err != nil {
		return value, err
	}
	value.BaseURL = base
	if _, err := strm.DriveURL(base, strm.Drive{PickCode: "abcdef", FileID: "1", SHA1: strings.Repeat("A", 40), Size: 1, Name: "validate.mkv"}); err != nil {
		return value, generationInputError(err.Error())
	}
	output, err := ResolveOutputDirectory(s.Root, value.OutputDir)
	if err != nil {
		return value, err
	}
	if strings.TrimSpace(value.VideoExtensions) == "" {
		value.VideoExtensions = DefaultVideoExtensions
	}
	videos, err := driveSTRMExtensions(value.VideoExtensions)
	if err != nil {
		return value, err
	}
	if len(videos) == 0 {
		return value, generationInputError("至少需要一个视频后缀")
	}
	if _, err := parseMetadataExtensions(value.MetadataExtensions, videos); err != nil {
		return value, err
	}
	if value.MinSizeMB < 0 || value.MinSizeMB > 1<<30 {
		return value, generationInputError("最小文件大小无效")
	}
	s.mu.Lock()
	active := value.ID != "" && s.active == value.ID
	s.mu.Unlock()
	if active {
		return value, ErrDriveSTRMBusy
	}
	// Validate the remote selection without holding the worker/control mutex.
	directory, err := s.Client.ReadDirectory(ctx, value.SourceCID, account.Cookie)
	if err != nil {
		return value, fmt.Errorf("核验来源目录: %w", err)
	}
	if directory.CID != value.SourceCID {
		return value, generationInputError("来源目录身份已变化")
	}
	value.SourcePath = directory.Path
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.Accounts.Primary(value.AccountID)
	if err != nil {
		return value, err
	}
	if current.Revision != account.Revision {
		return value, ErrAccountChanged
	}
	old := model.DriveSTRM{}
	if value.ID != "" {
		old, err = s.Store.GetDriveSTRM(ctx, value.ID)
		if err != nil {
			return value, err
		}
		if s.active == value.ID {
			return value, ErrDriveSTRMBusy
		}
	}
	all, err := s.Store.ListDriveSTRM(ctx)
	if err != nil {
		return value, err
	}
	for _, other := range all {
		if other.ID == value.ID {
			continue
		}
		otherOutput, err := ResolveOutputDirectory(s.Root, other.OutputDir)
		if err != nil {
			return value, err
		}
		if mediaWithin(otherOutput, output) || mediaWithin(output, otherOutput) {
			return value, generationInputError("输出目录不能与其他目录 STRM 映射重叠")
		}
	}
	if value.ID == "" {
		value.ID, err = organizeToken()
		if err != nil {
			return value, err
		}
	}
	value.Status, value.LastError = "queued", ""
	value.LastScanAt, value.Stats = old.LastScanAt, old.Stats
	value.LastFullScanAt = old.LastFullScanAt
	value.Queued, value.FullScan, value.PendingDirectories = true, true, nil
	value.Mode = "full"
	value.UpdatedAt = time.Now().UTC()
	value.NextScanAt = value.UpdatedAt
	reset := old.ID != "" && (old.AccountID != value.AccountID || old.SourceCID != value.SourceCID || old.OutputDir != value.OutputDir)
	if err := s.Store.SaveDriveSTRMConfiguration(ctx, value, reset); err != nil {
		return value, err
	}
	s.Wake()
	return value, nil
}

func (s *DriveSTRMService) SetMonitoring(ctx context.Context, id string, enabled bool) (model.DriveSTRM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.Store.GetDriveSTRM(ctx, id)
	if err != nil {
		return value, err
	}
	if value.Enabled == enabled {
		return value, nil
	}
	if enabled {
		if _, err := s.Accounts.Primary(value.AccountID); err != nil {
			return value, err
		}
		value.Queued, value.FullScan = true, true
		value.LastError, value.FailureCount = "", 0
		if s.active != id {
			value.Status = "queued"
		}
	}
	value.Enabled, value.UpdatedAt = enabled, time.Now().UTC()
	if err := s.Store.SaveDriveSTRM(ctx, value); err != nil {
		return value, err
	}
	s.Wake()
	return value, nil
}

func (s *DriveSTRMService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == id {
		return ErrDriveSTRMBusy
	}
	return s.Store.DeleteDriveSTRM(ctx, id)
}

func (s *DriveSTRMService) Queue(ctx context.Context, id string, full bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.Store.GetDriveSTRM(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.Accounts.Primary(value.AccountID); err != nil {
		return err
	}
	value.Queued = true
	value.FullScan = value.FullScan || full || len(value.PendingDirectories) == 0
	value.LastError, value.FailureCount = "", 0
	if s.active != id {
		value.Status = "queued"
	}
	value.UpdatedAt = time.Now().UTC()
	if err := s.Store.SaveDriveSTRM(ctx, value); err != nil {
		return err
	}
	s.Wake()
	return nil
}

// Stop ends only the current attempt. Unwritten work can be retried explicitly;
// the automatic-monitoring preference is left unchanged.
func (s *DriveSTRMService) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.Store.GetDriveSTRM(ctx, id)
	if err != nil {
		return err
	}
	value.Queued, value.FullScan, value.PendingDirectories = false, true, nil
	value.Status, value.LastError = "stopped", "已停止，可重新检查并补齐"
	value.NextScanAt, value.UpdatedAt = time.Now().Add(driveSTRMReconcileInterval).UTC(), time.Now().UTC()
	if err := s.Store.SaveDriveSTRM(ctx, value); err != nil {
		return err
	}
	if s.active == id && s.cancel != nil {
		s.cancel()
	}
	return nil
}

// NotifyDirectories commits all affected queues before returning. Callers may
// then advance their life-event cursor; a partial failure is safe to replay.
func (s *DriveSTRMService) NotifyDirectories(ctx context.Context, accountID string, cids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.Store.ListDriveSTRM(ctx)
	if err != nil {
		return err
	}
	changed := make(map[string]bool, len(cids))
	for _, cid := range cids {
		changed[cid] = true
	}
	for _, value := range values {
		if !value.Enabled || value.AccountID != accountID {
			continue
		}
		// A queued complete scan already covers events received before it starts.
		if value.Queued && value.FullScan && s.active != value.ID {
			continue
		}
		files, err := s.Store.ListDriveSTRMDirectories(ctx, value.ID)
		if err != nil {
			return err
		}
		known := map[string]bool{value.SourceCID: true}
		for _, file := range files {
			if file.Directory {
				known[file.FileID] = true
			}
		}
		pending := make(map[string]bool, len(value.PendingDirectories))
		for _, cid := range value.PendingDirectories {
			pending[cid] = true
		}
		added := false
		for cid := range changed {
			if known[cid] && !pending[cid] {
				value.PendingDirectories = append(value.PendingDirectories, cid)
				pending[cid], added = true, true
			}
		}
		if !added {
			continue
		}
		value.Queued = true
		if value.Status == "stopped" {
			value.LastError, value.FailureCount = "", 0
		}
		if s.active != value.ID {
			value.Status = "queued"
		}
		value.UpdatedAt = time.Now().UTC()
		if err := s.Store.SaveDriveSTRM(ctx, value); err != nil {
			return err
		}
	}
	s.Wake()
	return nil
}

func (s *DriveSTRMService) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return ErrDriveSTRMBusy
	}
	s.running = true
	// A process exit can happen after bytes were written and before indexing.
	// Reconciliation adopts identical bytes and recreates missing output safely.
	values, err := s.Store.ListDriveSTRM(ctx)
	if err == nil {
		for _, value := range values {
			if value.Status != "running" {
				continue
			}
			value.Status, value.Queued, value.FullScan = "queued", true, true
			if err = s.Store.SaveDriveSTRM(ctx, value); err != nil {
				break
			}
		}
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
	if err != nil {
		return err
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var lastCleanup time.Time
	var cleanupError string
	for {
		if ctx.Err() != nil {
			return nil
		}
		// Retry only our persisted export files, including when all mappings
		// have been removed. Pending upstream jobs must finish before reuse.
		if time.Since(lastCleanup) >= 30*time.Second {
			lastCleanup = time.Now()
			message := ""
			if err := s.cleanDriveSTRMExports(ctx); err != nil && ctx.Err() == nil {
				message = err.Error()
			}
			if message != "" && message != cleanupError {
				log.Printf("目录 STRM 导出清理暂未完成，将重试：%s", message)
			}
			cleanupError = message
		}
		value, scanCtx, found, err := s.next(ctx)
		if err != nil {
			return err
		}
		if found {
			stats, scanErr := s.scan(scanCtx, value)
			if err := s.finish(ctx, value.ID, stats, scanErr); err != nil {
				return err
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

func (s *DriveSTRMService) next(ctx context.Context) (model.DriveSTRM, context.Context, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.Store.ListDriveSTRM(ctx)
	if err != nil {
		return model.DriveSTRM{}, nil, false, err
	}
	now := time.Now().UTC()
	// Round-robin avoids a noisy mapping starving other queued directories.
	sort.SliceStable(values, func(i, j int) bool {
		a, b := values[i].ID > s.lastPicked, values[j].ID > s.lastPicked
		if a != b {
			return a
		}
		return values[i].ID < values[j].ID
	})
	for _, value := range values {
		if value.Queued && value.LastError != "" && value.NextScanAt.After(now) {
			continue
		}
		if !value.Queued && !(value.Enabled && !value.NextScanAt.After(now)) {
			continue
		}
		if !value.Queued || value.Enabled && !value.NextScanAt.After(now) {
			value.FullScan = value.FullScan || value.LastFullScanAt.IsZero() || now.Sub(value.LastFullScanAt) >= driveSTRMFullInterval
			// Frequent life events must not postpone periodic reconciliation.
			value.PendingDirectories = nil
		}
		snapshot := value
		value.Queued, value.FullScan, value.PendingDirectories = false, false, nil
		value.Status, value.LastError, value.UpdatedAt = "running", "", now
		value.Stats = model.DriveSTRMStats{}
		if snapshot.FullScan {
			value.Mode, snapshot.Mode = "full", "full"
		} else if len(snapshot.PendingDirectories) == 0 {
			value.Mode, snapshot.Mode = "tree", "tree"
		} else {
			value.Mode, snapshot.Mode = "changes", "changes"
		}
		if err := s.Store.SaveDriveSTRM(ctx, value); err != nil {
			return value, nil, false, err
		}
		s.active = value.ID
		s.lastPicked = value.ID
		scanCtx, cancel := context.WithCancel(ctx)
		s.cancel = cancel
		return snapshot, scanCtx, true, nil
	}
	return model.DriveSTRM{}, nil, false, nil
}

func (s *DriveSTRMService) finish(ctx context.Context, id string, stats model.DriveSTRMStats, scanErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.active, s.cancel = "", nil
	// Finishing must survive cancellation of the process-level worker context.
	processCanceled := ctx.Err() != nil
	if processCanceled {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	value, err := s.Store.GetDriveSTRM(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	value.Stats = stats
	if scanErr == nil && value.Mode == "full" {
		value.LastFullScanAt = time.Now().UTC()
	}
	value.LastScanAt, value.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	if value.Mode != "changes" || value.NextScanAt.IsZero() {
		value.NextScanAt = value.LastScanAt.Add(driveSTRMReconcileInterval)
	}
	if value.Status != "stopped" {
		value.Status, value.LastError = "idle", ""
		if scanErr != nil {
			value.Status, value.LastError = "error", scanErr.Error()
			value.FullScan = true
			value.FailureCount++
			delay := time.Minute
			if value.FailureCount > 1 {
				delay = 5 * time.Minute
			}
			if value.FailureCount > 2 {
				delay = 30 * time.Minute
			}
			if value.FailureCount > 3 {
				delay = driveSTRMReconcileInterval
			}
			value.NextScanAt = value.LastScanAt.Add(delay)
		} else {
			value.FailureCount = 0
		}
		if processCanceled {
			value.Queued, value.FullScan = true, true
			value.LastError, value.FailureCount = "", 0
			value.NextScanAt = value.LastScanAt
		}
		if value.Queued {
			value.Status = "queued"
		}
	}
	return s.Store.SaveDriveSTRM(ctx, value)
}
