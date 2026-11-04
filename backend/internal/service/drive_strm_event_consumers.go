package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"strm115/backend/internal/client/p115"
)

// ConsumeOnce 允许同步交接一批缓存事件，不重新请求生活事件接口。
func (r *DriveSTRMEvents) ConsumeOnce(ctx context.Context, name string) error {
	r.Hub.mu.Lock()
	s := r.Hub.consumers[name]
	r.Hub.mu.Unlock()
	if s == nil {
		return fmt.Errorf("生活事件消费者不存在")
	}
	_, err := r.consume(ctx, s)
	return err
}

func (r *DriveSTRMEvents) consumeLoop(ctx context.Context, s *P115LifeSubscription) {
	failures := 0
	for ctx.Err() == nil {
		batchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		worked, err := r.consume(batchCtx, s)
		cancel()
		if errors.Is(err, ErrLifeEventExpired) {
			continue
		}
		delay := 5 * time.Second
		if err != nil {
			s.failed(err)
			failures++
			delay = time.Duration(1<<min(failures, 5)) * time.Second
		} else {
			failures = 0
			if worked {
				continue
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-s.wake:
			timer.Stop()
		}
	}
}

func (r *DriveSTRMEvents) consume(ctx context.Context, s *P115LifeSubscription) (bool, error) {
	s.consumeMu.Lock()
	defer s.consumeMu.Unlock()
	batch, ok := s.read(false)
	if !ok {
		return false, r.Hub.checkpoint(ctx)
	}
	account, err := r.service.Accounts.Primary("")
	if err != nil {
		return true, err
	}
	if account.ID != batch.AccountID || p115.CookieUserID(account.Cookie) != batch.UID {
		// 旧主账号的缓存只能退休，不能转交给新账号或重新解释其目录编号。
		return true, s.Acknowledge(ctx, batch)
	}
	if s.work == nil || s.work.batch.sequence != batch.sequence || s.work.batch.epoch != batch.epoch {
		work, err := r.prepareEventWork(ctx, s.name, batch)
		if err != nil {
			return true, err
		}
		s.work = work
	}
	work := s.work
	if work.resolved < len(work.unresolved) {
		if err := r.resolveEventWork(ctx, work, account); err != nil {
			return true, err
		}
		if work.resolved < len(work.unresolved) {
			return true, nil
		}
	}
	end := min(work.offset+32, len(work.directories))
	ids := work.directories[work.offset:end]
	switch s.name {
	case "strm":
		if len(ids) > 0 {
			err = r.service.NotifyDirectories(ctx, batch.AccountID, ids)
		}
	case "organize":
		if r.Organizer != nil {
			invalidated := work.invalidated
			work.invalidated = nil
			err = r.Organizer.consume115EventDirectories(ctx, batch.AccountID, ids, invalidated, fmt.Sprintf("%d:%d:%d", batch.epoch, batch.sequence, work.offset))
			r.Organizer.lifeEventConsumerStatus(err)
		}
	default:
		return true, fmt.Errorf("此生活事件订阅应由所属功能独立消费")
	}
	if err != nil {
		return true, err
	}
	work.offset = end
	if end == len(work.directories) {
		err = s.Acknowledge(ctx, batch)
		s.work = nil
	}
	return true, err
}

func (r *DriveSTRMEvents) prepareEventWork(ctx context.Context, consumer string, batch P115LifeEventBatch) (*lifeEventWork, error) {
	work := &lifeEventWork{batch: batch}
	parents, structural := make(map[string]bool), make(map[string]bool)
	missingParent := make(map[string]bool)
	for _, event := range batch.Events {
		if !event.ChangesDirectory() {
			continue
		}
		if event.ParentID != "" {
			parents[event.ParentID] = true
		} else {
			missingParent[event.FileID] = true
		}
		if event.FileID != "" && (event.Type == 5 || event.Type == 6 || event.Type == 20) {
			structural[event.FileID] = true
		}
	}
	for id := range structural {
		work.invalidated = append(work.invalidated, id)
	}
	sort.Strings(work.invalidated)
	roots := make([]string, 0)
	known := make(map[string]bool)
	switch consumer {
	case "strm":
		work.knownScopes = make(map[string][]string)
		indexedIDs := make(map[string]bool, len(missingParent)+len(structural))
		for id := range missingParent {
			indexedIDs[id] = true
		}
		for id := range structural {
			indexedIDs[id] = true
		}
		values, err := r.service.Store.ListDriveSTRM(ctx)
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			if value.Enabled && value.AccountID == batch.AccountID {
				roots = append(roots, value.SourceCID)
				known[value.SourceCID] = true
				work.scopeCount++
				work.knownScopes[value.SourceCID] = append(work.knownScopes[value.SourceCID], value.ID)
				if len(indexedIDs) > 0 {
					directories, err := r.service.Store.ListDriveSTRMDirectories(ctx, value.ID)
					if err != nil {
						return nil, err
					}
					for _, directory := range directories {
						if directory.Directory {
							work.knownScopes[directory.FileID] = append(work.knownScopes[directory.FileID], value.ID)
						}
					}
					ids := make([]string, 0, len(indexedIDs))
					for id := range indexedIDs {
						if id != "" {
							ids = append(ids, id)
						}
					}
					files, err := r.service.Store.ListDriveSTRMFilesByIDs(ctx, value.ID, ids)
					if err != nil {
						return nil, err
					}
					for _, file := range files {
						known[file.FileID] = true
						if file.ParentCID != "" {
							parents[file.ParentCID] = true
						}
					}
				}
			}
		}
		for id := range structural {
			parents[id] = true
		}
	case "organize":
		if r.Organizer == nil {
			return work, nil
		}
		roots = r.Organizer.event115Roots(batch.AccountID)
		knownIDs := r.Organizer.event115KnownDirectories(batch.AccountID, work.invalidated)
		knownIDs = append(knownIDs, batch.KnownDirectories...)
		for _, id := range knownIDs {
			parents[id] = true
			known[id] = true
		}
		for _, id := range roots {
			known[id] = true
		}
		// 失效由采集端在新事件到达时执行，重试和分块不再重复清空核验缓存。
		work.invalidated = nil
	}
	if len(roots) == 0 {
		return &lifeEventWork{batch: batch}, nil
	}
	for id := range missingParent {
		// 移动后的父目录可能变化；保留本地旧父目录的同时核验当前归属。
		if !known[id] || structural[id] || consumer == "strm" && work.scopeCount > 1 {
			if id == "" {
				batch.Repair = true
				break
			}
			work.unresolved = append(work.unresolved, id)
		}
	}
	if batch.Repair {
		work.unresolved = nil
		parents = make(map[string]bool, len(roots))
		for _, id := range roots {
			parents[id] = true
		}
	}
	work.roots = roots
	work.scopeSet = parents
	sort.Strings(work.unresolved)
	for id := range parents {
		work.directories = append(work.directories, id)
	}
	sort.Strings(work.directories)
	return work, nil
}
