package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type DriveSTRMLifeClient interface {
	LifeEvents(context.Context, int, int, string) (p115.LifeEventPage, error)
}

type DriveSTRMEventState struct {
	LastCheckedAt time.Time `json:"lastCheckedAt,omitempty"`
	LastError     string    `json:"lastError,omitempty"`
}

// DriveSTRMEvents owns one read-only history reader for the primary account.
// Its watermark advances only after affected directory queues are durable;
// scans may then run independently without holding up history discovery.
type DriveSTRMEvents struct {
	service *DriveSTRMService
	client  DriveSTRMLifeClient
	gate    chan struct{}
	mu      sync.RWMutex
	state   DriveSTRMEventState
}

func NewDriveSTRMEvents(service *DriveSTRMService, client DriveSTRMLifeClient) *DriveSTRMEvents {
	return &DriveSTRMEvents{service: service, client: client, gate: make(chan struct{}, 1)}
}

func (r *DriveSTRMEvents) State() DriveSTRMEventState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

// Check serializes manual and background reads with a cancellable wait. It
// checks all enabled mappings together, never one upstream reader per mapping.
func (r *DriveSTRMEvents) Check(ctx context.Context) error {
	select {
	case r.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-r.gate }()
	err := r.check(ctx)
	r.mu.Lock()
	r.state.LastCheckedAt = time.Now().UTC()
	r.state.LastError = ""
	if err != nil {
		r.state.LastError = err.Error()
	}
	r.mu.Unlock()
	return err
}

// Run is a blocking worker. The owner starts and joins it with its process
// context, alongside the independent scan worker. An upstream failure backs
// off rather than immediately replaying pages or delaying ordinary playback.
func (r *DriveSTRMEvents) Run(ctx context.Context) {
	r.run(ctx, 5*time.Second)
}

// A lightweight local refresh lets period changes take effect while a long
// interval or disabled watcher is waiting. It never creates extra readers.
func (r *DriveSTRMEvents) run(ctx context.Context, refresh time.Duration) {
	if r.service == nil || r.client == nil {
		return
	}
	ticker := time.NewTicker(refresh)
	defer ticker.Stop()
	var lastAttempt time.Time
	failures := 0
	for {
		if ctx.Err() != nil {
			return
		}
		settings, err := r.service.Store.GetDriveSTRMSettings(ctx)
		if err == nil && driveSTRMEventDue(settings, lastAttempt, failures, time.Now()) {
			pollCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			err = r.Check(pollCtx)
			cancel()
			lastAttempt = time.Now()
			if err == nil {
				failures = 0
			} else {
				failures++
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func driveSTRMEventDue(settings model.DriveSTRMSettings, last time.Time, failures int, now time.Time) bool {
	if settings.EventIntervalSeconds == 0 {
		return false
	}
	interval := time.Duration(settings.EventIntervalSeconds) * time.Second
	delay := min(interval*time.Duration(1<<min(failures, 4)), max(interval, 5*time.Minute))
	return last.IsZero() || !last.Add(delay).After(now)
}

func (r *DriveSTRMEvents) check(ctx context.Context) error {
	if r.service == nil || r.client == nil {
		return fmt.Errorf("目录 STRM 事件服务尚未初始化")
	}
	s := r.service
	if _, err := s.Store.GetDriveSTRMSettings(ctx); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		return err
	}
	values, err := s.Store.ListDriveSTRM(ctx)
	if err != nil {
		return err
	}
	accountID := s.Accounts.PrimaryID()
	roots := make([]string, 0, len(values))
	for _, value := range values {
		if value.Enabled && value.AccountID == accountID {
			roots = append(roots, value.SourceCID)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	account, err := s.Accounts.Primary(accountID)
	if err != nil {
		return err
	}
	cursor, err := s.Store.LoadDriveSTRMCursor(ctx, account.ID)
	if err != nil {
		return err
	}
	var watermark uint64
	if cursor != "" {
		watermark, err = strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			return fmt.Errorf("目录 STRM 生活事件游标无效: %w", err)
		}
	}
	latest := watermark
	scopes := make(map[string]bool)
	queueRoots := func() {
		for _, cid := range roots {
			scopes[cid] = true
		}
	}
	for offset := 0; ; {
		limit := 1000
		if offset == 0 {
			limit = 64
		}
		if cursor == "" {
			limit = 1
		} else if offset+limit > 10000 {
			limit = 10000 - offset
		}
		page, err := r.client.LifeEvents(ctx, offset, limit, account.Cookie)
		if err != nil {
			return err
		}
		reached := false
		for _, event := range page.Events {
			id, err := strconv.ParseUint(event.ID, 10, 64)
			if err != nil || id == 0 {
				return fmt.Errorf("目录 STRM 收到无效生活事件编号")
			}
			if offset == 0 && id > latest {
				latest = id
			}
			// New history can arrive between offset pages. Keep the first-page
			// watermark fixed so unseen newer rows remain for the next check.
			if offset > 0 && id > latest {
				continue
			}
			if id <= watermark {
				reached = true
				continue
			}
			if !event.ChangesDirectory() {
				continue
			}
			if event.ParentID != "" {
				scopes[event.ParentID] = true
			}
			// A directory rename/move can change every descendant output path.
			// NotifyDirectories admits only IDs indexed as directories and the
			// scanner verifies that each is still inside its configured root.
			if event.FileID != "" && (event.Type == 5 || event.Type == 6 || event.Type == 20) {
				scopes[event.FileID] = true
			}
		}
		if cursor == "" {
			// Capture a watermark before queuing initial full scopes. Even when
			// an initial scan is already running this leaves a durable follow-up,
			// so new files in a directory already visited are not swallowed.
			queueRoots()
			break
		}
		offset += len(page.Events)
		if reached {
			break
		}
		if len(page.Events) == 0 || offset >= 10000 || offset >= page.Count {
			// History may be truncated, expired or cleared by another tool.
			// If no retained boundary can be found, repair once and establish a
			// new baseline. An empty baseline is zero so idle checks do not
			// repeatedly rescan the library after history has been cleared.
			if watermark > 0 {
				queueRoots()
				if offset == 0 {
					latest = 0
				}
			}
			break
		}
	}
	if len(scopes) > 0 {
		cids := make([]string, 0, len(scopes))
		for cid := range scopes {
			cids = append(cids, cid)
		}
		sort.Strings(cids)
		if err := s.NotifyDirectories(ctx, account.ID, cids); err != nil {
			return fmt.Errorf("保存目录 STRM 变更队列: %w", err)
		}
	}
	if cursor == "" || latest != watermark {
		if err := s.Store.SaveDriveSTRMCursor(ctx, account.ID, strconv.FormatUint(latest, 10)); err != nil {
			return fmt.Errorf("保存目录 STRM 事件水位: %w", err)
		}
	}
	return nil
}
