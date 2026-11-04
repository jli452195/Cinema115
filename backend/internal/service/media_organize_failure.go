package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"strings"
	"time"
)

// 失败搬运保留原任务关联键；去重完成后另存实际保留身份用于纠正重试。
type MediaOrganizeFailure struct {
	State     string `json:"state"`
	ParentID  string `json:"parentId,omitempty"`
	FileID    string `json:"fileId,omitempty"`
	CD2FileID string `json:"cd2FileId,omitempty"`
	Reason    string `json:"reason"`
	Message   string `json:"message,omitempty"`
}

func mediaTransientGroups(run *MediaOrganizeRun) map[string]bool {
	groups := make(map[string]bool)
	for _, g := range run.Groups {
		if g.ResultCode == "METADATA_QUERY_FAILED" || strings.HasPrefix(g.Message, "查询暂未完成") {
			groups[g.ID] = true
		}
	}
	return groups
}

func mediaFailureEntries(run *MediaOrganizeRun) map[string]bool {
	eligible := make(map[string]bool)
	if run.Rule.Failure == "" {
		return eligible
	}
	pending := make(map[string]bool)
	for _, entry := range run.Entries {
		if entry.P115Move != nil || entry.AttachmentKeep != nil && entry.AttachmentKeep.Phase != "retained" {
			pending[entry.File.ID] = true
		}
	}
	transient := mediaTransientGroups(run)
	for _, e := range run.Entries {
		if e.File.MediaType == "cleanup" || pending[e.File.ID] || pending[e.VideoID] || !mediaInRunTask(run, e) {
			continue
		}
		if e.Status != "done" && e.Failure != nil && e.Failure.State == "moving" {
			eligible[e.File.ID] = true
			continue
		}
		if e.Status == "done" || transient[e.Group] {
			continue
		}
		if e.Status == "needs_match" || e.Status == "invalid" || e.Status == "conflict" || e.Status == "unmatched" {
			eligible[e.File.ID] = true
		}
	}
	for _, e := range run.Entries {
		if e.Status != "done" && e.VideoID != "" && eligible[e.VideoID] {
			eligible[e.File.ID] = true
		}
	}
	return eligible
}

func (f *mediaOrganizeFiles) failureFiles(ctx context.Context) (*mediaOrganizeFiles, error) {
	if !f.failureChecked {
		if f.rule.Storage == "cd2" {
			if err := f.cd2CheckDirectory(ctx, f.rule.Failure); err != nil {
				return nil, err
			}
		} else if f.rule.Storage == "115" {
			drive, ok := f.drive.(mediaOrganizeDrive)
			if !ok {
				return nil, fmt.Errorf("115 目录服务未配置")
			}
			dir, err := drive.ReadDirectory(ctx, f.rule.FailureID, f.cookie)
			if err != nil {
				return nil, err
			}
			if dir.CID != f.rule.FailureID || dir.Path != f.rule.Failure {
				return nil, fmt.Errorf("整理失败目录已变化，请重新选择目录")
			}
		} else if root, err := mediaLocalRoot(f.rule.Failure); err != nil {
			return nil, err
		} else if root != f.rule.Failure {
			return nil, fmt.Errorf("整理失败目录已变化，请重新保存配置")
		}
		f.failureChecked = true
	}
	target := *f
	target.rule.Target, target.rule.TargetID = f.rule.Failure, f.rule.FailureID
	return &target, nil
}

// Quarantine runs only during execution, never while rendering a preview.
// Resume uncertain writes first; new failures are isolated after ordinary work.
func (s *MediaOrganizer) quarantine(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, resume bool) error {
	eligible := mediaFailureEntries(run)
	for i := range run.Entries {
		e := &run.Entries[i]
		moving := e.Failure != nil && e.Failure.State == "moving"
		if e.Status == "done" || !eligible[e.File.ID] || resume != moving || e.Failure != nil && e.Failure.State == "stored" {
			continue
		}
		// Keep associated subtitles with the video if its quarantine move failed.
		if e.VideoID != "" && eligible[e.VideoID] {
			videoStored := false
			for _, video := range run.Entries {
				if video.File.ID == e.VideoID {
					videoStored = video.Failure != nil && video.Failure.State == "stored"
					break
				}
			}
			if !videoStored {
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		stop, err := s.quarantineEntry(ctx, files, run, i, moving)
		if err != nil {
			return err
		}
		if stop {
			break
		}
	}
	return ctx.Err()
}

// 每次仅保护当前文件所属视频，避免整个失败队列阻止其他历史记录删除。
func (s *MediaOrganizer) quarantineEntry(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, index int, moving bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	indices, releaseEntry := s.beginMediaEntries(run, []int{index})
	if len(indices) == 0 {
		return false, nil
	}
	defer releaseEntry()
	e := &run.Entries[index]
	if e.Failure == nil {
		e.Failure = &MediaOrganizeFailure{State: "pending", Reason: e.Message}
		if e.Failure.Reason == "" {
			e.Failure.Reason = "相关视频需要人工处理"
		}
	}
	work, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var saveErr error
	err := func() error {
		var release func()
		if run.Rule.Storage == "115" && s.reserve != nil {
			var err error
			release, err = s.reserve()
			if err != nil {
				return err
			}
			defer release()
		}
		target, err := files.failureFiles(work)
		if err != nil {
			return err
		}
		plan := *e
		plan.Relative = e.File.SourcePath
		if run.Rule.Storage == "115" {
			e.Failure.ParentID, err = target.cloudDestination(work, plan.Relative, true)
			if err != nil {
				return err
			}
		}
		if !moving {
			if err := target.conflict(work, plan); err != nil {
				return err
			}
		}
		e.Failure.State = "moving"
		if saveErr = s.checkpoint(run); saveErr != nil {
			return saveErr
		}
		if err := target.move(work, plan); err != nil {
			var receipt *mediaDuplicateReceipt
			if !errors.As(err, &receipt) {
				return err
			}
			return mediaStoreFailureDuplicate(e, run.Rule, receipt)
		}
		if run.Rule.Storage == "cd2" {
			actual, err := target.cd2.Stat(work, path.Join(run.Rule.Failure, plan.Relative), true)
			if err != nil {
				return err
			}
			e.Failure.CD2FileID = actual.ID
		}
		return nil
	}()
	if saveErr != nil {
		return false, saveErr
	}
	if err == nil {
		e.Failure.State, e.Failure.Message = "stored", ""
	} else {
		e.Failure.Message = organizePublicError(err, files.cookie)
	}
	e.Result = &MediaOrganizeResult{Status: "error", Message: e.Failure.Reason, At: time.Now(), Target: path.Join(run.Rule.Failure, e.File.SourcePath)}
	if err := s.checkpoint(run); err != nil {
		return false, err
	}
	files.forget115SourceEntry(*e)
	s.tryCleanup115EmptyDirectories(ctx, files, run)
	// 网盘或挂载失败后结束当前轮次，避免对余下每个文件重复请求。
	return err != nil && !errors.Is(err, errMediaTargetExists), ctx.Err()
}

func mediaStoreFailureDuplicate(entry *MediaOrganizeEntry, rule MediaOrganizeRule, receipt *mediaDuplicateReceipt) error {
	if receipt == nil || receipt.fileID == "" || receipt.size < 0 || entry.Failure == nil {
		return fmt.Errorf("失败目录去重保留身份未确认")
	}
	switch rule.Storage {
	case "115":
		if receipt.cd2 != nil || receipt.fileID == entry.File.ID || receipt.parentID == "" || receipt.parentID != entry.Failure.ParentID {
			return fmt.Errorf("115 失败目录去重保留身份不一致")
		}
		entry.Failure.FileID = receipt.fileID
	case "cd2":
		file := receipt.cd2
		expected, err := mediaCD2Relative(rule.Failure, entry.File.SourcePath)
		if err != nil || file == nil || file.ID != receipt.fileID || file.Size != receipt.size || file.Directory || file.ReadOnly ||
			path.Dir(file.Path) != path.Dir(expected) || file.Name != path.Base(file.Path) || !strings.EqualFold(file.Name, path.Base(expected)) || !mediaCD2SameSHA1(*entry, *file) {
			return fmt.Errorf("CD2 失败目录去重保留身份不一致")
		}
		modified := int64(0)
		if !file.Modified.IsZero() {
			modified = file.Modified.UnixNano()
		}
		entry.Failure.CD2FileID = file.ID
		entry.CD2Current = &MediaCD2Location{Path: file.Path, FileID: file.ID, Modified: modified}
		entry.CD2Hashes = maps.Clone(file.Hashes)
		entry.CD2Duplicate = false
	default:
		return fmt.Errorf("当前存储未提供可核验的失败目录去重回执")
	}
	entry.File.Size = receipt.size
	return nil
}
