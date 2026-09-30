package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/client/p115"
)

type playbackTargetError struct {
	stage string
	err   error
}

func (e *playbackTargetError) Error() string {
	return e.stage + "：" + p115.SharePlaybackFailureReason(e.err)
}
func (e *playbackTargetError) Unwrap() error { return e.err }

// One request owns all recovery reads. Failed files are deduplicated by ID,
// not local mapping key. Only confirmed missing IDs may be excluded in search.
type playbackRecovery struct {
	tried   map[string]bool
	missing map[string]bool
	ctx     context.Context
	cancel  context.CancelFunc
	checks  int
	failed  map[string]PlaybackCacheEntry
}

var errPlaybackNeedsSearch = errors.New("目标文件取链被拒绝，需查询当前 UID 文件")

func newPlaybackRecovery() *playbackRecovery {
	return &playbackRecovery{tried: make(map[string]bool), missing: make(map[string]bool), failed: make(map[string]PlaybackCacheEntry)}
}
func (r *playbackRecovery) close() {
	if r.cancel != nil {
		r.cancel()
	}
}
func (r *playbackRecovery) budget(ctx context.Context) context.Context {
	if r.ctx == nil {
		r.ctx, r.cancel = context.WithTimeout(ctx, 5*time.Second)
	}
	return r.ctx
}

func (r *playbackRecovery) reuse(ctx context.Context, s *PlaybackCache, e PlaybackCacheEntry, selected *PlaybackCacheSelection, ua string, report *PlaybackResolution) (string, error) {
	if r.checks >= 3 {
		return "", fmt.Errorf("目标文件恢复核查达到 3 个候选上限，未执行秒传")
	}
	if r.missing[e.File.ID] {
		return "", p115.ErrDriveFileMissing
	}
	if r.tried[e.File.ID] {
		if previous, ok := r.failed[e.File.ID]; ok {
			return "", r.confirmMissing(ctx, s, previous, selected, &playbackTargetError{stage: "目标文件仍被索引返回但无法取链", err: p115.ErrDriveRejected})
		}
		return "", fmt.Errorf("目标文件本次已尝试，停止重复取链")
	}
	r.tried[e.File.ID] = true
	readCtx := ctx
	if r.ctx != nil {
		readCtx = r.ctx
	}
	link, err := s.reuseFastEntry(readCtx, e, selected, ua, report)
	if err == nil {
		return link, nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	// Only an API rejection can need an existence check. Network, login,
	// throttling and invalid/restricted links must never initiate a write.
	if !errors.Is(err, p115.ErrDriveRejected) && !errors.Is(err, p115.ErrDriveFileMissing) {
		return "", err
	}
	r.failed[e.File.ID] = e
	s.logs.Add(ctx, "INFO", "目标取链被拒绝，先查询当前 UID 的同 SHA1 文件，搜索仍返回旧文件时才精确核查；fileID="+e.File.ID)
	return "", errPlaybackNeedsSearch
}

func (r *playbackRecovery) confirmMissing(ctx context.Context, s *PlaybackCache, e PlaybackCacheEntry, selected *PlaybackCacheSelection, original error) error {
	checker, ok := s.client.(interface {
		VerifyPlaybackFile(context.Context, p115.DrivePlaybackFile, string) error
	})
	if !ok {
		return original
	}
	if r.checks >= 3 {
		return fmt.Errorf("目标文件恢复核查达到 3 个候选上限，未执行秒传")
	}
	r.checks++
	s.logs.Add(ctx, "INFO", "目标取链被拒绝，绕过缓存按文件 ID 核查；fileID="+e.File.ID)
	err := checker.VerifyPlaybackFile(r.budget(ctx), e.File, selected.cookie)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return original
	}
	if !errors.Is(err, p115.ErrDriveFileMissing) {
		return &playbackTargetError{stage: "目标文件失效核查失败，未执行秒传", err: err}
	}
	if err := s.invalidateMissingCopy(selected.uid, e.File); err != nil {
		return err
	}
	r.missing[e.File.ID] = true
	s.logs.Add(ctx, "INFO", "精确核查确认旧文件已不存在，清除该账号副本及来源映射，保留秒传资料；fileID="+e.File.ID)
	return p115.ErrDriveFileMissing
}

func (r *playbackRecovery) search(ctx context.Context, fast fastPlaybackClient, selected *PlaybackCacheSelection, hash string) (*p115.DrivePlaybackFile, error) {
	searcher, ok := fast.(interface {
		FindPlaybackFileBySHA1Excluding(context.Context, string, string, map[string]bool) (*p115.DrivePlaybackFile, error)
	})
	if !ok {
		return nil, fmt.Errorf("播放客户端不支持失效文件恢复查询")
	}
	f, err := searcher.FindPlaybackFileBySHA1Excluding(r.budget(ctx), hash, selected.cookie, r.missing)
	if err == nil && f != nil && r.missing[f.ID] {
		return nil, fmt.Errorf("恢复搜索仍返回已排除文件，未执行秒传")
	}
	return f, err
}

// Transaction first, then invalidate runtime handles. Remove only ready copies
// of this target UID/file; never erase uncertain upload intent or file material.
func (s *PlaybackCache) invalidateMissingCopy(uid string, file p115.DrivePlaybackFile) error {
	s.mu.Lock()
	doc := s.doc
	doc.Entries = make([]PlaybackCacheEntry, 0, len(s.doc.Entries))
	removed := make(map[string]bool)
	for _, e := range s.doc.Entries {
		if e.UID == uid && e.File.ID == file.ID && e.Status == "ready" {
			removed[e.Key] = true
			continue
		}
		doc.Entries = append(doc.Entries, e)
	}
	if len(removed) > 0 {
		if err := s.saveLocked(doc); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	s.runtimeMu.Lock()
	for source, key := range s.sourceMappings {
		if removed[key] {
			delete(s.sourceMappings, source)
		}
	}
	s.runtimeMu.Unlock()
	if invalidator, ok := s.client.(interface{ ForgetPlaybackFileMetadata(p115.DrivePlaybackFile) }); ok {
		invalidator.ForgetPlaybackFileMetadata(file)
	}
	return nil
}
