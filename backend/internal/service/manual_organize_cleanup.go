package service

import (
	"context"
	"fmt"
	"path"
	"time"

	"strm115/backend/internal/client/p115"
)

type organizeRecycleDrive interface {
	DeleteEntries(context.Context, []string, string) error
}

type OrganizeCleanupInput struct {
	AccountID  string   `json:"accountId"`
	ParentID   string   `json:"parentId"`
	ParentPath string   `json:"parentPath"`
	Mode       string   `json:"mode"` // selected or empty
	IDs        []string `json:"ids"`
}

type OrganizeCleanupEntry struct {
	p115.ShareFile
	ParentID string `json:"parentId"`
	Path     string `json:"path"`
}

type OrganizeCleanupItem struct {
	OrganizeCleanupEntry
	Ancestors []OrganizeCleanupEntry `json:"ancestors,omitempty"`
	Status    string                 `json:"status"`
	Message   string                 `json:"message,omitempty"`
}

// Plans and durable results contain IDs and metadata only, never credentials.
type OrganizeCleanup struct {
	ID         string                `json:"id"`
	AccountID  string                `json:"accountId"`
	Revision   uint64                `json:"revision"`
	ParentID   string                `json:"parentId"`
	ParentPath string                `json:"parentPath"`
	Mode       string                `json:"mode"`
	Status     string                `json:"status"`
	Message    string                `json:"message,omitempty"`
	CreatedAt  time.Time             `json:"createdAt"`
	UpdatedAt  time.Time             `json:"updatedAt"`
	Items      []OrganizeCleanupItem `json:"items"`
}

func (s *ManualOrganize) reserveCleanup() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil || s.cleaning {
		return fmt.Errorf("%w：已有整理、目录分享、清理或字幕导入执行中，请等待完成", ErrOrganizeConflict)
	}
	s.cleaning = true
	return nil
}

func (s *ManualOrganize) releaseCleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleaning = false
}

func (s *ManualOrganize) PreviewCleanup(ctx context.Context, input OrganizeCleanupInput) (OrganizeCleanup, error) {
	if !organizeID(input.ParentID) || !organizePath(input.ParentPath) ||
		(input.Mode != "selected" && input.Mode != "empty") ||
		(input.Mode == "selected" && (len(input.IDs) == 0 || len(input.IDs) > 100)) ||
		(input.Mode == "empty" && len(input.IDs) != 0) {
		return OrganizeCleanup{}, ErrOrganizeInput
	}
	a, err := s.account(input.AccountID)
	if err != nil {
		return OrganizeCleanup{}, err
	}
	if _, ok := s.drive.(organizeRecycleDrive); !ok {
		return OrganizeCleanup{}, fmt.Errorf("当前网盘不支持移入回收站")
	}
	if err := s.reserveCleanup(); err != nil {
		return OrganizeCleanup{}, err
	}
	defer s.releaseCleanup()
	selected := make(map[string]bool, len(input.IDs))
	for _, id := range input.IDs {
		if !organizeID(id) || id == "0" || id == input.ParentID || selected[id] {
			return OrganizeCleanup{}, ErrOrganizeInput
		}
		selected[id] = true
	}
	plan := OrganizeCleanup{AccountID: a.ID, Revision: a.Revision, ParentID: input.ParentID,
		ParentPath: input.ParentPath, Mode: input.Mode, Status: "preview", CreatedAt: time.Now(), Items: []OrganizeCleanupItem{}}
	files, err := s.list(ctx, input.ParentID, a.Cookie)
	if err != nil {
		return OrganizeCleanup{}, err
	}
	visited := map[string]bool{input.ParentID: true}
	var walk func(p115.ShareFile, string, string, []OrganizeCleanupEntry) (bool, error)
	walk = func(file p115.ShareFile, parent, prefix string, ancestors []OrganizeCleanupEntry) (bool, error) {
		if !file.Directory {
			return false, nil
		}
		if visited[file.ID] || !organizeID(file.ID) || file.ID == "0" {
			return false, fmt.Errorf("%w：目录结构发生变化，请刷新后重试", ErrOrganizeConflict)
		}
		if len(visited) > 300 || len(ancestors) >= 12 {
			return false, fmt.Errorf("%w：清理最多扫描 300 个目录、12 层，请进入较小目录", ErrOrganizeInput)
		}
		visited[file.ID] = true
		entry := OrganizeCleanupEntry{ShareFile: file, ParentID: parent, Path: path.Join(prefix, file.Name)}
		children, err := s.list(ctx, file.ID, a.Cookie)
		if err != nil {
			return false, err
		}
		empty := true
		chain := append(append([]OrganizeCleanupEntry{}, ancestors...), entry)
		for _, child := range children {
			childEmpty, err := walk(child, file.ID, entry.Path, chain)
			if err != nil {
				return false, err
			}
			empty = empty && childEmpty
		}
		if empty {
			plan.Items = append(plan.Items, OrganizeCleanupItem{OrganizeCleanupEntry: entry, Ancestors: ancestors, Status: "pending"})
		}
		return empty, nil
	}
	for _, file := range files {
		if input.Mode == "empty" {
			if _, err := walk(file, input.ParentID, input.ParentPath, nil); err != nil {
				return OrganizeCleanup{}, err
			}
		} else if selected[file.ID] {
			plan.Items = append(plan.Items, OrganizeCleanupItem{OrganizeCleanupEntry: OrganizeCleanupEntry{
				ShareFile: file, ParentID: input.ParentID, Path: path.Join(input.ParentPath, file.Name)}, Status: "pending"})
		}
	}
	if input.Mode == "selected" && len(plan.Items) != len(selected) {
		return OrganizeCleanup{}, fmt.Errorf("%w：所选条目已移动或删除，请刷新", ErrOrganizeConflict)
	}
	plan.ID, err = organizeToken()
	if err != nil {
		return OrganizeCleanup{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleanupPlans == nil {
		s.cleanupPlans = make(map[string]OrganizeCleanup)
	}
	for id, old := range s.cleanupPlans {
		if time.Since(old.CreatedAt) > 10*time.Minute {
			delete(s.cleanupPlans, id)
		}
	}
	if len(s.cleanupPlans) >= 8 {
		oldest := ""
		for id, old := range s.cleanupPlans {
			if oldest == "" || old.CreatedAt.Before(s.cleanupPlans[oldest].CreatedAt) {
				oldest = id
			}
		}
		delete(s.cleanupPlans, oldest)
	}
	s.cleanupPlans[plan.ID] = cloneCleanup(plan)
	return plan, nil
}

func cloneCleanup(job OrganizeCleanup) OrganizeCleanup {
	job.Items = append([]OrganizeCleanupItem{}, job.Items...)
	for i := range job.Items {
		job.Items[i].Ancestors = append([]OrganizeCleanupEntry{}, job.Items[i].Ancestors...)
	}
	return job
}

func (s *ManualOrganize) CleanupHistory() []OrganizeCleanup {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]OrganizeCleanup, len(s.doc.CleanupJobs))
	for i, job := range s.doc.CleanupJobs {
		jobs[i] = cloneCleanup(job)
	}
	return jobs
}

func (s *ManualOrganize) cleanupCheckpoint(job *OrganizeCleanup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job.UpdatedAt = time.Now()
	doc := s.doc
	doc.CleanupJobs = append([]OrganizeCleanup{}, doc.CleanupJobs...)
	found := false
	for i := range doc.CleanupJobs {
		if doc.CleanupJobs[i].ID == job.ID {
			doc.CleanupJobs[i], found = cloneCleanup(*job), true
			break
		}
	}
	if !found {
		doc.CleanupJobs = append([]OrganizeCleanup{cloneCleanup(*job)}, doc.CleanupJobs...)
	}
	if len(doc.CleanupJobs) > 10 {
		doc.CleanupJobs = doc.CleanupJobs[:10]
	}
	return s.saveLocked(doc)
}

// Confirmation accepts only a server-issued plan, never replacement IDs.
func (s *ManualOrganize) ExecuteCleanup(ctx context.Context, accountID, previewID string) (OrganizeCleanup, error) {
	a, err := s.account(accountID)
	if err != nil {
		return OrganizeCleanup{}, err
	}
	s.mu.Lock()
	for _, old := range s.doc.CleanupJobs {
		if old.ID == previewID && old.AccountID == accountID {
			s.mu.Unlock()
			return cloneCleanup(old), nil
		}
	}
	plan, ok := s.cleanupPlans[previewID]
	s.mu.Unlock()
	if !ok || plan.AccountID != accountID || plan.Revision != a.Revision || time.Since(plan.CreatedAt) > 10*time.Minute {
		return OrganizeCleanup{}, fmt.Errorf("%w：清理预览已过期或账号已变化，请重新预览", ErrOrganizeConflict)
	}
	if err := s.reserveCleanup(); err != nil {
		return OrganizeCleanup{}, err
	}
	defer s.releaseCleanup()
	// Recheck after reserving: a concurrent confirmation may have completed.
	s.mu.Lock()
	_, ok = s.cleanupPlans[previewID]
	if ok {
		delete(s.cleanupPlans, previewID)
	}
	s.mu.Unlock()
	if !ok {
		return OrganizeCleanup{}, fmt.Errorf("%w：清理已提交，请查看清理记录", ErrOrganizeConflict)
	}
	job := cloneCleanup(plan)
	job.Status = "running"
	if err := s.cleanupCheckpoint(&job); err != nil {
		return OrganizeCleanup{}, err
	}
	err = s.runCleanup(ctx, &job, a.Cookie)
	job.Status, job.Message = "completed", "清理完成，已删除条目移入 115 回收站"
	for _, item := range job.Items {
		if item.Status != "done" {
			job.Status, job.Message = "partial", "部分条目未删除，请查看明细"
			break
		}
	}
	if err != nil {
		job.Status, job.Message = "partial", organizePublicError(err, a.Cookie)
	}
	if saveErr := s.cleanupCheckpoint(&job); saveErr != nil {
		// Keep the last durable item states, but release the live "running"
		// status so a storage failure does not lock the UI until restart.
		s.mu.Lock()
		for i := range s.doc.CleanupJobs {
			if s.doc.CleanupJobs[i].ID == job.ID {
				s.doc.CleanupJobs[i].Status = "interrupted"
				s.doc.CleanupJobs[i].Message = "清理已停止，结果保存失败；请核实网盘和回收站"
				break
			}
		}
		s.mu.Unlock()
		return OrganizeCleanup{}, saveErr
	}
	return job, nil
}

func cleanupMatches(files []p115.ShareFile, entry OrganizeCleanupEntry) bool {
	for _, file := range files {
		if file.ID == entry.ID {
			return file.Name == entry.Name && file.Directory == entry.Directory && (file.Directory || file.Size == entry.Size)
		}
	}
	return false
}

func (s *ManualOrganize) runCleanup(ctx context.Context, job *OrganizeCleanup, cookie string) error {
	drive, ok := s.drive.(organizeRecycleDrive)
	if !ok {
		return fmt.Errorf("当前网盘不支持移入回收站")
	}
	for i := range job.Items {
		item := &job.Items[i]
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("清理中断，请检查记录后重新预览: %w", err)
		}
		chain := append(append([]OrganizeCleanupEntry{}, item.Ancestors...), item.OrganizeCleanupEntry)
		valid := true
		for _, entry := range chain {
			files, err := s.list(ctx, entry.ParentID, cookie)
			if err != nil {
				return err
			}
			if !cleanupMatches(files, entry) {
				valid = false
				break
			}
		}
		if !valid {
			item.Status, item.Message = "skipped", "条目或上级目录已变化，已跳过"
		} else if job.Mode == "empty" {
			files, err := s.list(ctx, item.ID, cookie)
			if err != nil {
				return err
			}
			if len(files) > 0 {
				item.Status, item.Message = "skipped", "目录已有内容，已保留"
			}
		}
		if item.Status == "skipped" {
			if err := s.cleanupCheckpoint(job); err != nil {
				return err
			}
			continue
		}
		item.Status, item.Message = "uncertain", "删除请求即将发送或已发送，结果待核实"
		// Persist the exact intent before issuing a non-retriable write.
		if err := s.cleanupCheckpoint(job); err != nil {
			return err
		}
		if err := drive.DeleteEntries(ctx, []string{item.ID}, cookie); err != nil {
			return fmt.Errorf("删除结果待核实，已停止后续清理: %w", &organizeSafeError{err: err, message: organizePublicError(err, cookie)})
		}
		files, err := s.list(ctx, item.ParentID, cookie)
		if err != nil {
			return err
		}
		for _, file := range files {
			if file.ID == item.ID {
				return fmt.Errorf("删除后条目仍存在，已停止后续清理，请刷新核实")
			}
		}
		item.Status, item.Message = "done", "已移入 115 回收站"
		if err := s.cleanupCheckpoint(job); err != nil {
			return err
		}
	}
	return nil
}
