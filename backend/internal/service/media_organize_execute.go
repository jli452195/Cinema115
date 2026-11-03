package service

import (
	"context"
	"errors"
	"time"
)

// 单项执行与批量回退共用相同的字幕依赖、预约和结果检查点。
func (s *MediaOrganizer) executeMediaEntry(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun, index int) error {
	e := &run.Entries[index]
	if !mediaInRunTask(run, *e) || e.Status != "ready" && e.Status != "error" || e.Relative == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.VideoID != "" {
		videoDone := false
		for _, video := range run.Entries {
			if video.File.ID == e.VideoID {
				videoDone = video.Status == "done"
				break
			}
		}
		if !videoDone {
			e.Status, e.Message = "error", "对应视频尚未整理成功，关联文件保留来源"
			e.Result = &MediaOrganizeResult{Status: e.Status, Message: e.Message, At: time.Now()}
			return s.checkpoint(run)
		}
	}
	var release func()
	var err error
	if run.Rule.Storage == "115" && s.reserve != nil {
		release, err = s.reserve()
		if err != nil {
			e.Status, e.Message = "error", "115 目录忙，请稍后继续"
			e.Result = &MediaOrganizeResult{Status: e.Status, Message: e.Message, At: time.Now()}
			return s.checkpoint(run)
		}
	}
	err = files.move(ctx, *e)
	if release != nil {
		release()
	}
	if err != nil && !errors.Is(err, errMediaDuplicateDone) {
		e.Status = "error"
		if errors.Is(err, errMediaTargetExists) {
			e.Status = "conflict"
		}
		e.Message = organizePublicError(err, files.cookie)
	} else {
		e.Status, e.Message, e.Failure = "done", "", nil
	}
	e.Result = &MediaOrganizeResult{Status: e.Status, Message: e.Message, At: time.Now(), Target: e.Target}
	if errors.Is(err, errMediaDuplicateDone) {
		e.Result.Action, e.Result.Message = "duplicate", "SHA1 一致，已删除来源重复文件"
	}
	if err := s.checkpoint(run); err != nil {
		return err
	}
	files.forget115SourceEntry(*e)
	s.tryCleanup115EmptyDirectories(ctx, files, run)
	return nil
}
