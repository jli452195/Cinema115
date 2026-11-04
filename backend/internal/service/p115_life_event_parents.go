package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"strm115/backend/internal/client/p115"
)

type lifeEventParentReader interface {
	LifeEventParent(context.Context, string, string) (string, error)
}

type lifeEventParentResult struct {
	done    chan struct{}
	parent  string
	err     error
	expires time.Time
}

// 只有缺少父目录的事件才查询身份；两个消费者合并同一查询，不重复读取目录列表。
func (h *P115LifeEvents) resolveParent(ctx context.Context, batch P115LifeEventBatch, id string, account P115Account, reader lifeEventParentReader) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		h.mu.Lock()
		if batch.AccountID != h.accountID || batch.UID != h.uid || batch.epoch != h.epoch {
			h.mu.Unlock()
			return "", ErrLifeEventExpired
		}
		if entry := h.parents[id]; entry != nil {
			if entry.done != nil {
				done := entry.done
				h.mu.Unlock()
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-done:
				}
				continue
			}
			if entry.expires.After(time.Now()) {
				parent, err := entry.parent, entry.err
				h.mu.Unlock()
				return parent, err
			}
		}
		if h.parents == nil {
			h.parents = make(map[string]*lifeEventParentResult)
		}
		if len(h.parents) >= 2048 {
			for key, entry := range h.parents {
				if entry.done == nil {
					delete(h.parents, key)
				}
			}
		}
		entry := &lifeEventParentResult{done: make(chan struct{})}
		h.parents[id] = entry
		h.mu.Unlock()
		parent, err := reader.LifeEventParent(ctx, id, account.Cookie)
		h.mu.Lock()
		valid := h.parents[id] == entry && batch.epoch == h.epoch
		if valid && (err == nil || errors.Is(err, p115.ErrDriveFileMissing) || errors.Is(err, p115.ErrLifeEventIdentity)) {
			entry.parent, entry.err, entry.expires = parent, err, time.Now().Add(time.Minute)
		} else if valid {
			delete(h.parents, id)
		}
		close(entry.done)
		entry.done = nil
		h.mu.Unlock()
		if !valid {
			return "", ErrLifeEventExpired
		}
		return parent, err
	}
}

func (r *DriveSTRMEvents) resolveEventWork(ctx context.Context, work *lifeEventWork, account P115Account) error {
	reader, ok := r.client.(lifeEventParentReader)
	if !ok {
		work.directories = append([]string(nil), work.roots...)
		work.unresolved = nil
		return nil
	}
	for requests := 0; requests < 32 && work.resolved < len(work.unresolved); requests++ {
		id := work.unresolved[work.resolved]
		if work.parentID != "" {
			id = work.parentID
		}
		parent, err := r.Hub.resolveParent(ctx, work.batch, id, account, reader)
		if errors.Is(err, p115.ErrLifeEventIdentity) {
			// 只有精确身份仍不能定位时才补扫，不把网络故障当成范围未知。
			work.directories = append([]string(nil), work.roots...)
			work.unresolved = nil
			return nil
		}
		if err != nil && !errors.Is(err, p115.ErrDriveFileMissing) {
			return fmt.Errorf("定位生活事件父目录: %w", err)
		}
		if err == nil && work.knownScopes != nil {
			if work.parentOwned == nil {
				work.parentOwned = make(map[string]bool, work.scopeCount)
			}
			matched := false
			for _, mapping := range work.knownScopes[parent] {
				if !work.parentOwned[mapping] {
					work.parentOwned[mapping], matched = true, true
				}
			}
			if matched && !work.scopeSet[parent] {
				work.scopeSet[parent] = true
				work.directories = append(work.directories, parent)
			}
			// 分别定位每个来源的已知祖先；嵌套映射不能借用另一份映射的索引进度。
			if len(work.parentOwned) < work.scopeCount && parent != "0" {
				if work.parentSeen == nil {
					work.parentSeen = make(map[string]bool)
				}
				work.parentSeen[id] = true
				if parent == "" || work.parentSeen[parent] || len(work.parentSeen) >= 64 {
					work.directories = append([]string(nil), work.roots...)
					work.unresolved = nil
					return nil
				}
				work.parentID = parent
				continue
			}
		} else if err == nil && !work.scopeSet[parent] {
			work.scopeSet[parent] = true
			work.directories = append(work.directories, parent)
		}
		work.parentID, work.parentSeen, work.parentOwned = "", nil, nil
		work.resolved++
	}
	if work.resolved == len(work.unresolved) {
		sort.Strings(work.directories)
		work.scopeSet = nil
	}
	return nil
}
