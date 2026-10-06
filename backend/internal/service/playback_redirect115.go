package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

// Unknown, unambiguous redirect115 files can use the target as their first
// metadata source. Existing source conflicts and pending copies keep recovery's
// original rules. Never promote URL metadata or an unverified search candidate.
func (s *PlaybackCache) lookupRedirect115Target(ctx context.Context, fast fastPlaybackClient, selected *PlaybackCacheSelection, scope, sourceKey, ua string, report *PlaybackResolution, recovery *playbackRecovery) (string, bool, *playbackSearchPrefetch, error) {
	// Complete play115 declarations use the normal target lookup with the
	// declared size, including when an older origin mapping disagrees.
	if _, ok := ctx.Value(play115SourceKey{}).(Play115Source); ok {
		return "", false, nil, nil
	}
	enabled, _ := ctx.Value(playbackTargetSHA1Lookup{}).(bool)
	hash, _ := ctx.Value(playbackSHA1Hint{}).(string)
	if !enabled || !validMaterialHash(hash) || len(recovery.tried) != 0 {
		return "", false, nil, nil
	}
	quick, ok := fast.(quickPlaybackSearch)
	if !ok {
		return "", false, nil, nil
	}
	if _, ok := s.client.(interface {
		VerifiedPlaybackURL(context.Context, p115.DrivePlaybackFile, string, string) (string, error)
	}); !ok {
		return "", false, nil, nil
	}
	origin, _ := ctx.Value(playbackMaterialOrigin{}).(string)
	s.mu.Lock()
	known := len(s.index.origins[origin]) != 0 || len(s.index.hashes[strings.ToUpper(hash)]) != 0
	s.mu.Unlock()
	if known || len(s.entriesByTarget(selected.uid, hash)) != 0 {
		return "", false, nil, nil
	}
	quickKey := DirectLinkKey("quick-search", selected.uid, strings.ToUpper(hash))
	if err := s.waitPlaybackSearch(ctx, quickKey, scope); err != nil {
		return "", false, nil, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	file, err := quickTargetQuery(queryCtx, quick, hash, selected.cookie)
	cancel()
	if ctx.Err() != nil {
		return "", false, nil, ctx.Err()
	}
	if err != nil {
		s.backoffPlaybackSearch(quickKey, scope, time.Now().Add(5*time.Second), err)
		return "", false, nil, &playbackTargetError{stage: "目标 SHA1 优先查询失败，未执行秒传", err: err}
	}
	// Completed result is shared with the normal path, including a definite
	// miss. No second quick query and no detached work on source failure.
	prefetch := &playbackSearchPrefetch{hash: hash, file: file, cancel: func() {}}
	if file == nil {
		s.logs.Add(ctx, "INFO", "目标 SHA1 优先查询未命中，继续查询主账号资料；复用本次未命中结果")
		return "", false, prefetch, nil
	}
	material := PlaybackMaterial{ID: playbackMaterialID(hash, file.Size), Name: file.Name, Size: file.Size, SHA1: strings.ToUpper(hash), CreatedAt: time.Now().UTC()}
	if file.ID == "" || file.PickCode == "" || !strings.EqualFold(file.SHA1, hash) {
		return "", false, nil, fmt.Errorf("目标 SHA1 优先查询文件身份无效")
	}
	if err := validatePlaybackMaterials([]PlaybackMaterial{material}); err != nil {
		return "", false, nil, err
	}
	entry := PlaybackCacheEntry{AccountID: selected.binding.AccountID, UID: selected.uid, ParentCID: selected.binding.ParentCID, DirectoryID: file.CID, File: *file, Status: "ready", SourceKey: sourceKey, TargetScope: scope, MaterialID: material.ID, CreatedAt: time.Now().UTC()}
	link, err := recovery.reuse(ctx, s, entry, selected, ua, report)
	if err != nil {
		if errors.Is(err, p115.ErrDriveFileMissing) || errors.Is(err, errPlaybackNeedsSearch) {
			return "", false, prefetch, nil
		}
		return "", false, nil, err
	}
	// Search + verified target response establishes file data, not the external
	// PickCode's ownership. Deliberately leave Origins empty.
	material.VerifiedAt = time.Now().UTC()
	if err := s.putMaterial(material); err != nil {
		return "", false, nil, err
	}
	s.rememberSource(sourceKey, report.FileCacheKey)
	s.logs.Add(ctx, "INFO", "redirect115 SHA1 命中目标文件并通过核验，跳过主账号元数据查询；无需秒传")
	return link, true, nil, nil
}
