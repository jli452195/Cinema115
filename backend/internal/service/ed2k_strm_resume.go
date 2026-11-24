package service

import (
	"context"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

var errED2KCredentialPaused = generationInputError("账号或凭据已变化，任务已暂停，请明确重新检查原账号资源")

func sameED2KCredential(v model.ED2KSTRMResource, a P115Account) bool {
	// 按保存时的版本计算摘要，账号改名等非凭据更新无需暂停。
	a.Revision = v.AccountRevision
	return v.CredentialScope == ed2kCredentialScope(a)
}

func ed2kCredentialPaused(v model.ED2KSTRMResource) bool {
	if v.Status != "blocked" {
		return false
	}
	// 原版本会持久化以下固定原因，身份仍由原 ID 和 UID 判断。
	switch v.Message {
	case errED2KCredentialPaused.Error(), "生成配置无效：请先在 115 助手中登录主账号", "生成配置无效：请先登录明确配置的主账号":
		return true
	default:
		return false
	}
}

func (s *ED2KSTRMService) CredentialPausedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.account()
	if err != nil {
		return 0
	}
	count := 0
	for key, v := range s.resources {
		if key != s.active && ed2kCredentialPaused(v) && v.AccountID == a.ID && v.UID == a.UID {
			count++
		}
	}
	return count
}

// ResumeCredentials 以当前主账号凭据重新入队，保留原目录和未确认操作的阶段。
func (s *ED2KSTRMService) ResumeCredentials(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.account()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	scope := ed2kCredentialScope(a)
	resumed := make(map[string]struct{})
	var resources []model.ED2KSTRMResource
	for key, v := range s.resources {
		if key == s.active || !ed2kCredentialPaused(v) || v.AccountID != a.ID || v.UID != a.UID {
			continue
		}
		v.AccountRevision, v.CredentialScope = a.Revision, scope
		v.Status, v.Message, v.Attempts = "queued", "已确认原主账号，等待继续缓存任务", 0
		v.NextRunAt = time.Time{}
		v.UpdatedAt = now
		resumed[key] = struct{}{}
		resources = append(resources, v)
	}
	if len(resources) == 0 {
		return 0, nil
	}
	var tasks []model.ED2KSTRMTask
	for _, t := range s.tasks {
		if _, ok := resumed[ed2kResourceKey(t.Hash, t.Size)]; ok && t.Status == "failed" && t.Message == ed2kExportMissingMaterial {
			t.Status, t.Message, t.UpdatedAt = "waiting", "等待首次文件资料核验", now
			tasks = append(tasks, t)
		}
	}
	if err := s.Store.SaveED2KSTRMResume(ctx, resources, tasks); err != nil {
		return 0, fmt.Errorf("继续已暂停的 ED2K STRM 任务: %w", err)
	}
	for _, v := range resources {
		key := ed2kResourceKey(v.Hash, v.Size)
		s.resources[key], s.pinned[key] = v, a
	}
	for _, t := range tasks {
		s.tasks[t.ID] = t
	}
	s.notify()
	if s.Logs != nil {
		s.Logs.Append("STRM", "ED2K缓存", "ED2K STRM", "INFO", fmt.Sprintf("同一主账号重新入队 %d 个缓存任务，同时继续 %d 个待资料的生成任务", len(resources), len(tasks)))
	}
	return len(resources), nil
}
