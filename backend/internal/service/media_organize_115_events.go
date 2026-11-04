package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"path"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

type media115EventDirectory struct {
	directory p115.DriveDirectory
	expires   time.Time
}

// 启用共享事件消费时，已有规则从安全事件游标续接；新规则仍按原流程首次补扫。
func (s *MediaOrganizer) EnableLifeEventConsumer() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range s.doc.Rules {
		if rule.Storage == "115" && rule.RealtimeEnabled {
			runtime := s.cloudRuntimeLocked(rule)
			runtime.initialized = true
			runtime.status.Mode = "connecting"
		}
	}
}

func (s *MediaOrganizer) invalidate115EventDirectories(accountID string, ids []string) {
	if s == nil || len(ids) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range s.doc.Rules {
		if rule.Storage == "115" && rule.RealtimeEnabled && rule.AccountID == accountID {
			mediaInvalidate115EventDirectories(s.cloudRuntimeLocked(rule), ids)
		}
	}
}

func mediaInvalidate115EventDirectories(runtime *mediaCloudRuntime, ids []string) {
	if len(ids) == 0 {
		return
	}
	runtime.eventRevision++
	// 移动可能使原本位于范围外的目录进入来源，不能沿用旧的范围外结论。
	clear(runtime.eventDirectories)
	for _, id := range ids {
		if relative, exists := runtime.directories[id]; exists {
			for child, childPath := range runtime.directories {
				if child == id || relative == "" || strings.HasPrefix(childPath, relative+"/") {
					delete(runtime.directories, child)
				}
			}
		}
	}
}

func (s *MediaOrganizer) known115EventDirectory(accountID, id string) bool {
	return len(s.event115KnownDirectories(accountID, []string{id})) != 0
}

func (s *MediaOrganizer) event115KnownDirectories(accountID string, ids []string) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	known := make(map[string]bool, len(ids))
	for _, rule := range s.doc.Rules {
		if rule.Storage != "115" || !rule.RealtimeEnabled || rule.AccountID != accountID {
			continue
		}
		for _, id := range ids {
			if rule.SourceID == id {
				known[id] = true
			}
			if runtime := s.cloudMonitors[rule.ID]; runtime != nil {
				if _, exists := runtime.directories[id]; exists {
					known[id] = true
				}
			}
		}
	}
	result := make([]string, 0, len(known))
	for _, id := range ids {
		if known[id] {
			result = append(result, id)
			delete(known, id)
		}
	}
	return result
}

func (s *MediaOrganizer) event115Roots(accountID string) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	roots := make([]string, 0)
	for _, rule := range s.doc.Rules {
		if rule.Storage == "115" && rule.RealtimeEnabled && (accountID == "" || rule.AccountID == accountID) {
			roots = append(roots, rule.SourceID)
		}
	}
	return roots
}

func (s *MediaOrganizer) lifeEventStatus(ok bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range s.doc.Rules {
		if rule.Storage != "115" || !rule.RealtimeEnabled {
			continue
		}
		runtime := s.cloudRuntimeLocked(rule)
		runtime.status.Mode, runtime.status.Message = "event", ""
		if !ok {
			runtime.status.Mode, runtime.status.Message = "unavailable", "生活事件读取失败，将自动重试；完整扫描仍按计划执行"
		}
	}
}

func (s *MediaOrganizer) lifeEventConsumerStatus(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range s.doc.Rules {
		if rule.Storage != "115" || !rule.RealtimeEnabled {
			continue
		}
		runtime := s.cloudRuntimeLocked(rule)
		runtime.status.Mode, runtime.status.Message = "event", ""
		if err != nil {
			runtime.status.Message = "事件已接收，目录核验或扫描安排暂缓，将自动重试"
		}
	}
}

// 目录编号仅在固定主账号内解析；未知编号只读目录身份，不遍历来源之外的内容。
func (s *MediaOrganizer) notify115Directories(ctx context.Context, accountID string, ids []string) error {
	return s.receive115EventDirectories(ctx, accountID, ids, nil, false, "")
}

// 消费成功表示扫描范围已经交接到可恢复的现有任务队列，不只是写入内存。
func (s *MediaOrganizer) consume115EventDirectories(ctx context.Context, accountID string, ids, invalidated []string, receipt ...string) error {
	token := ""
	if len(receipt) > 0 {
		token = receipt[0]
	}
	return s.receive115EventDirectories(ctx, accountID, ids, invalidated, true, token)
}

func (s *MediaOrganizer) receive115EventDirectories(ctx context.Context, accountID string, ids, invalidated []string, durable bool, receipt string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	rules := make([]MediaOrganizeRule, 0)
	for _, rule := range s.doc.Rules {
		if rule.Storage == "115" && rule.RealtimeEnabled && rule.AccountID == accountID {
			rules = append(rules, rule)
		}
	}
	s.mu.Unlock()
	if len(rules) == 0 {
		return nil
	}
	drive, ok := s.drive.(mediaOrganizeDrive)
	if !ok {
		return fmt.Errorf("生活事件目录核验服务尚未初始化")
	}
	account, err := s.accounts.Primary(accountID)
	if err != nil {
		return err
	}
	uid := accountUID(account.Cookie)
	revisions := make(map[string]uint64, len(rules))
	s.mu.Lock()
	for _, rule := range rules {
		runtime := s.cloudRuntimeLocked(rule)
		if runtime.eventUID != "" && runtime.eventUID != uid {
			clear(runtime.directories)
			runtime.directories[rule.SourceID] = ""
			clear(runtime.eventDirectories)
		}
		runtime.eventUID = uid
		if runtime.eventDirectories == nil {
			runtime.eventDirectories = make(map[string]media115EventDirectory)
		}
		mediaInvalidate115EventDirectories(runtime, invalidated)
		revisions[rule.ID] = runtime.eventRevision
	}
	s.mu.Unlock()
	directories := make(map[string]p115.DriveDirectory, len(ids))
	for _, id := range ids {
		if _, exists := directories[id]; exists {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var directory p115.DriveDirectory
		known := false
		s.mu.Lock()
		for _, rule := range rules {
			runtime := s.cloudMonitors[rule.ID]
			if runtime == nil {
				continue
			}
			if relative, exists := runtime.directories[id]; exists {
				directory = p115.DriveDirectory{CID: id, Path: path.Join(rule.Source, relative)}
				known = true
				break
			}
			if cached, exists := runtime.eventDirectories[id]; exists && cached.expires.After(s.localMonitorSettings().now()) {
				directory, known = cached.directory, true
				break
			}
		}
		s.mu.Unlock()
		if !known {
			directory, err = drive.ReadDirectory(ctx, id, account.Cookie)
			if err != nil {
				if errors.Is(err, p115.ErrDriveFileMissing) {
					continue
				}
				missing, probeErr := media115EventDirectoryMissing(ctx, drive, id, account.Cookie, err)
				if probeErr != nil {
					return probeErr
				}
				if missing {
					continue
				}
				return fmt.Errorf("核验生活事件目录: %w", err)
			}
		}
		if directory.CID != id || directory.Path == "" {
			return fmt.Errorf("生活事件目录身份不完整")
		}
		directories[id] = directory
		s.mu.Lock()
		// 只缓存成功核验的目录；缓存有界且短期有效，不缓存网络失败。
		runtime := s.cloudMonitors[rules[0].ID]
		if runtime != nil && runtime.eventUID == uid && runtime.eventRevision == revisions[rules[0].ID] && mediaSameRuleSettings(runtime.rule, rules[0]) {
			now := s.localMonitorSettings().now()
			for cachedID, cached := range runtime.eventDirectories {
				if !cached.expires.After(now) {
					delete(runtime.eventDirectories, cachedID)
				}
			}
			if len(runtime.eventDirectories) >= 1024 {
				clear(runtime.eventDirectories)
			}
			runtime.eventDirectories[id] = media115EventDirectory{directory: directory, expires: now.Add(time.Minute)}
		}
		s.mu.Unlock()
	}
	current, err := s.accounts.Primary(accountID)
	if err != nil {
		return err
	}
	if current.Revision != account.Revision || accountUID(current.Cookie) != uid {
		return fmt.Errorf("提交生活事件目录时主账号已变化: %w", ErrAccountChanged)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 与账号切换串行提交，避免网络核验结束后仍把旧来源交接到新主账号。
	s.accounts.mu.RLock()
	defer s.accounts.mu.RUnlock()
	accountMatches := false
	for _, value := range s.accounts.value.Accounts {
		if value.Primary && value.ID == accountID && value.Revision == account.Revision && accountUID(value.Cookie) == uid {
			accountMatches = true
			break
		}
	}
	if !accountMatches {
		return fmt.Errorf("提交生活事件目录时主账号已变化: %w", ErrAccountChanged)
	}
	for _, snapshot := range rules {
		if runtime := s.cloudMonitors[snapshot.ID]; runtime != nil && runtime.eventRevision != revisions[snapshot.ID] {
			return fmt.Errorf("生活事件目录索引已更新，等待重新核验: %w", ErrMediaOrganizeBusy)
		}
	}
	if durable {
		for _, snapshot := range rules {
			for _, rule := range s.doc.Rules {
				if rule.ID != snapshot.ID || !rule.RealtimeEnabled || !mediaSameRuleSettings(rule, snapshot) {
					continue
				}
				runtime := s.cloudRuntimeLocked(rule)
				if receipt != "" && runtime.eventReceipt == receipt {
					continue
				}
				for _, directory := range directories {
					if mediaCloudWithin(rule.Source, directory.Path) {
						if _, err := s.pendingCloudScanLocked(rule); err != nil {
							return fmt.Errorf("交接生活事件整理扫描: %w", err)
						}
						break
					}
				}
			}
		}
	}
	for _, snapshot := range rules {
		for _, rule := range s.doc.Rules {
			if rule.ID != snapshot.ID || !rule.RealtimeEnabled || !mediaSameRuleSettings(rule, snapshot) {
				continue
			}
			runtime := s.cloudRuntimeLocked(rule)
			if durable && receipt != "" && runtime.eventReceipt == receipt {
				break
			}
			previousPending, previousFull := maps.Clone(runtime.pending), runtime.full
			previousFirst, previousDue := runtime.first, runtime.due
			changed := false
			for id, directory := range directories {
				if !mediaCloudWithin(rule.Source, directory.Path) {
					continue
				}
				relative := strings.TrimPrefix(strings.TrimPrefix(directory.Path, rule.Source), "/")
				runtime.directories[id] = relative
				mediaMarkCloudScope(runtime, mediaCloudWorkScope(runtime, id, relative), s.localMonitorSettings().now())
				runtime.status.LastEventAt = s.localMonitorSettings().now()
				changed = true
			}
			if durable && changed {
				runtime.due = s.localMonitorSettings().now()
				if _, err := s.queueCloudChangesLocked(rule, runtime.due); err != nil {
					runtime.pending, runtime.full, runtime.first, runtime.due = previousPending, previousFull, previousFirst, previousDue
					return fmt.Errorf("交接生活事件整理扫描: %w", err)
				}
				runtime.eventReceipt = receipt
			}
			break
		}
	}
	s.notify()
	return nil
}

// 目录列表重定向不是删除证据；只有精确文件身份接口确认不存在才能忽略。
func media115EventDirectoryMissing(ctx context.Context, drive mediaOrganizeDrive, id, cookie string, readErr error) (bool, error) {
	if ctx.Err() != nil || errors.Is(readErr, p115.ErrShareLoginRequired) || errors.Is(readErr, p115.ErrShareThrottled) || errors.Is(readErr, p115.ErrRateQueueFull) {
		return false, nil
	}
	var networkErr net.Error
	var httpErr *p115.HTTPError
	if errors.As(readErr, &networkErr) || errors.As(readErr, &httpErr) {
		return false, nil
	}
	checker, ok := drive.(directoryMissingClient)
	if !ok {
		return false, nil
	}
	missing, err := checker.DirectoryMissing(ctx, id, cookie)
	if err != nil {
		return false, fmt.Errorf("复核生活事件目录是否删除: %w", err)
	}
	return missing, nil
}
