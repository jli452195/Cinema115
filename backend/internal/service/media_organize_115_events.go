package service

import (
	"context"
	"path"
	"strings"
	"strm115/backend/internal/client/p115"
)

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

// 目录编号仅在固定主账号内解析；未知编号只读目录身份，不遍历来源之外的内容。
func (s *MediaOrganizer) notify115Directories(ctx context.Context, accountID string, ids []string) error {
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
		return nil
	}
	account, err := s.accounts.Primary(accountID)
	if err != nil {
		return err
	}
	for _, id := range ids {
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
		}
		s.mu.Unlock()
		if !known {
			directory, err = drive.ReadDirectory(ctx, id, account.Cookie)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
		}
		s.mu.Lock()
		for _, rule := range s.doc.Rules {
			if rule.Storage != "115" || !rule.RealtimeEnabled || rule.AccountID != accountID || !mediaCloudWithin(rule.Source, directory.Path) {
				continue
			}
			runtime := s.cloudRuntimeLocked(rule)
			relative := strings.TrimPrefix(strings.TrimPrefix(directory.Path, rule.Source), "/")
			runtime.directories[id] = relative
			mediaMarkCloudScope(runtime, mediaCloudWorkScope(runtime, id, relative), s.localMonitorSettings().now())
			runtime.status.LastEventAt = s.localMonitorSettings().now()
		}
		s.mu.Unlock()
	}
	s.notify()
	return nil
}
