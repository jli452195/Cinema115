package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"strm115/backend/internal/strm"
)

const ed2kOutputReceiptTTL = 7 * 24 * time.Hour

const ed2kExportMissingMaterial = "首次文件资料尚未就绪，请检查资源缓存后重试"

// Tasks 只展示当前与失败输出；完成收据不再保存输出路径，也不参与文件监控。
func (s *ED2KSTRMService) Tasks() []model.ED2KSTRMTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.ED2KSTRMTask, 0, len(s.tasks))
	for _, v := range s.tasks {
		if v.Status == "completed" {
			continue
		}
		if v.Status == "writing" {
			v.Status = "waiting"
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *ED2KSTRMService) saveExport(ctx context.Context, t model.ED2KSTRMTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.UpdatedAt = time.Now().UTC()
	if err := s.Store.SaveED2KSTRMTask(ctx, t); err != nil {
		return err
	}
	s.tasks[t.ID] = t
	return nil
}

func (s *ED2KSTRMService) RetryTask(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	v := s.resources[ed2kResourceKey(t.Hash, t.Size)]
	if !ok {
		return storage.ErrNotFound
	}
	if t.Status != "failed" {
		return generationInputError("只有失败的输出任务可以重试")
	}
	if v.SHA1 == "" {
		if _, err := s.requestLocked(ctx, t.Hash, t.Size, false); err != nil {
			return err
		}
	}
	t.Status, t.Message = "queued", ""
	t.UpdatedAt = time.Now().UTC()
	if err := s.Store.SaveED2KSTRMTask(ctx, t); err != nil {
		return err
	}
	s.tasks[t.ID] = t
	s.notify()
	return nil
}

func (s *ED2KSTRMService) DeleteTask(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return storage.ErrNotFound
	}
	if t.Status != "failed" {
		return generationInputError("只能清理失败的输出任务")
	}
	// 清理失败任务也留短期收据，旧 Bot 回调或网络重放不能重新创建输出。
	t = completedED2KTask(t)
	t.UpdatedAt = time.Now().UTC()
	if err := s.Store.SaveED2KSTRMTask(ctx, t); err != nil {
		return err
	}
	s.tasks[id] = t
	return nil
}

func completedED2KTask(t model.ED2KSTRMTask) model.ED2KSTRMTask {
	t.Status = "completed"
	t.Name, t.TargetID, t.TargetName, t.OutputDir, t.BaseURL, t.Message = "", "", "", "", "", ""
	return t
}

// advanceExport 单步执行一个本地输出；缓存核验和恢复不调用此输出逻辑。
func (s *ED2KSTRMService) advanceExport(ctx context.Context) (bool, error) {
	s.mu.Lock()
	var task model.ED2KSTRMTask
	var resource model.ED2KSTRMResource
	for _, t := range s.tasks {
		if t.Status == "completed" {
			if time.Since(t.UpdatedAt) > ed2kOutputReceiptTTL {
				if err := s.Store.DeleteED2KSTRMTask(ctx, t.ID); err != nil {
					s.mu.Unlock()
					return false, err
				}
				delete(s.tasks, t.ID)
				s.mu.Unlock()
				return true, nil
			}
			continue
		}
		if t.Status == "failed" {
			continue
		}
		v, ok := s.resources[ed2kResourceKey(t.Hash, t.Size)]
		if ok && v.SHA1 == "" && ed2kCredentialPaused(v) {
			continue
		}
		if t.Status == "waiting" && ok && v.SHA1 == "" && v.Status != "failed" && v.Status != "blocked" {
			continue
		}
		if task.ID == "" || t.CreatedAt.Before(task.CreatedAt) {
			task, resource = t, v
		}
	}
	s.mu.Unlock()
	if task.ID == "" {
		return false, nil
	}
	if resource.Hash == "" {
		task.Status, task.Message = "failed", "资源资料不存在，请重新导入 ED2K"
		return true, s.saveExportFailure(ctx, task)
	}
	if resource.SHA1 == "" {
		if resource.Status == "blocked" || resource.Status == "failed" {
			task.Status, task.Message = "failed", ed2kExportMissingMaterial
			return true, s.saveExportFailure(ctx, task)
		}
		task.Status, task.Message = "waiting", "等待首次文件资料核验"
		return true, s.saveExport(ctx, task)
	}
	reconcileOnly := task.Status == "writing"
	if !reconcileOnly {
		task.Status, task.Message = "writing", "正在输出 STRM"
		if err := s.saveExport(ctx, task); err != nil {
			return false, err
		}
	}
	if err := s.writeExport(ctx, task, resource, reconcileOnly); err != nil {
		task.Status = "failed"
		task.Message = "STRM 输出未完成，请检查本地目录权限后重试"
		if errors.Is(err, ErrGenerationInput) {
			task.Message = err.Error()
		}
		return true, s.saveExportFailure(ctx, task)
	}
	if err := s.saveExport(ctx, completedED2KTask(task)); err != nil {
		return false, err
	}
	if s.Logs != nil {
		s.Logs.Append("STRM", task.ID, "ED2K STRM", "INFO", "已投递到目录："+task.TargetName+" / "+safeSTRMName(task.Name))
	}
	return true, nil
}

func (s *ED2KSTRMService) saveExportFailure(ctx context.Context, task model.ED2KSTRMTask) error {
	if err := s.saveExport(ctx, task); err != nil {
		return fmt.Errorf("保存 ED2K STRM 输出失败状态: %w", err)
	}
	s.Logs.Append("STRM", task.ID, "ED2K STRM", "ERROR", "本地生成："+task.Message)
	return nil
}

func (s *ED2KSTRMService) writeExport(ctx context.Context, t model.ED2KSTRMTask, v model.ED2KSTRMResource, reconcileOnly bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := ResolveOutputDirectory(s.Root, t.OutputDir)
	if err != nil {
		return err
	}
	claim := strm.ED2K{Hash: v.Hash, SHA1: v.SHA1, Size: v.Size, Name: t.Name}
	link, err := strm.ED2KURL(t.BaseURL, claim)
	if err != nil {
		return err
	}
	name := safeSTRMName(strings.TrimSuffix(t.Name, path.Ext(t.Name))) + ".strm"
	destination := filepath.Join(directory, name)
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return generationInputError("输出文件名被其他文件占用，请调整名称或输出目录")
		}
		if info.Size() > 16384 {
			return generationInputError("同名文件内容不同，不会覆盖，请更换输出目录或调整文件名")
		}
		data, err := os.ReadFile(destination)
		if err != nil {
			return err
		}
		u, err := url.Parse(strings.TrimSpace(string(data)))
		if err == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" {
			old, parseErr := strm.ParseED2KPath(u.EscapedPath())
			if parseErr == nil && old.Hash == claim.Hash && old.Size == claim.Size && strings.EqualFold(old.SHA1, claim.SHA1) {
				return nil
			}
		}
		return generationInputError("同名文件内容不同，不会覆盖，请更换输出目录或调整文件名")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if reconcileOnly {
		return generationInputError("原输出结果尚未确认，文件可能已移走；需要时请明确重试输出")
	}
	if err := ensureSTRMDirectory(directory); err != nil {
		return err
	}
	// 独占创建阻止并发同名覆盖；写入意图已落盘，重启只核对结果，不补写缺失文件。
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	content := link + "\n"
	if n, err := io.WriteString(f, content); err != nil {
		return err
	} else if n != len(content) {
		return io.ErrShortWrite
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
