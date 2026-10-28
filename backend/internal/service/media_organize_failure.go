package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// MediaOrganizeFailure records a move intent before external writes. File IDs and original
// paths stay stable so subtitle associations and corrections remain valid.
type MediaOrganizeFailure struct {
	State    string `json:"state"`
	ParentID string `json:"parentId,omitempty"`
	Reason   string `json:"reason"`
	Message  string `json:"message,omitempty"`
}

func mediaTransientGroups(run *MediaOrganizeRun) map[string]bool {
	groups := make(map[string]bool)
	for _, g := range run.Groups {
		if strings.HasPrefix(g.Message, "查询暂未完成") {
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
	transient := mediaTransientGroups(run)
	for _, e := range run.Entries {
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

func mediaRunCounts(run *MediaOrganizeRun) {
	run.EntryCount, run.PendingCount, run.ReadyCount, run.FailureCount = len(run.Entries), 0, 0, 0
	failures := mediaFailureEntries(run)
	for _, e := range run.Entries {
		if e.Status == "done" {
			continue
		}
		run.PendingCount++
		if failures[e.File.ID] {
			if e.Failure == nil || e.Failure.State != "stored" {
				run.FailureCount++
			}
		} else if e.Status == "ready" || e.Status == "error" && e.Relative != "" {
			run.ReadyCount++
		}
	}
}

func (f *mediaOrganizeFiles) failureFiles(ctx context.Context) (*mediaOrganizeFiles, error) {
	if !f.failureChecked {
		if f.rule.Storage == "115" {
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
		if e.Failure == nil {
			e.Failure = &MediaOrganizeFailure{State: "pending", Reason: e.Message}
			if e.Failure.Reason == "" {
				e.Failure.Reason = "相关视频需要人工处理"
			}
		}
		work, cancel := context.WithTimeout(ctx, 30*time.Second)
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
			return target.move(work, plan)
		}()
		cancel()
		if saveErr != nil {
			return saveErr
		}
		if err == nil {
			e.Failure.State, e.Failure.Message = "stored", ""
		} else {
			e.Failure.Message = organizePublicError(err, files.cookie)
		}
		e.Result = &MediaOrganizeResult{Status: "error", Message: e.Failure.Reason, At: time.Now(), Target: path.Join(run.Rule.Failure, e.File.SourcePath)}
		if err := s.checkpoint(run); err != nil {
			return err
		}
		if err != nil && !errors.Is(err, errMediaTargetExists) {
			// A failed provider or mount must not trigger one request per file.
			break
		}
	}
	return ctx.Err()
}
