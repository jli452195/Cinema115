package service

import (
	"context"
	"strings"
	"sync"

	"strm115/backend/internal/client/p115"
)

type quickPlaybackSearch interface {
	FindPlaybackFileBySHA1Quick(context.Context, string, string) (*p115.DrivePlaybackFile, error)
}

func quickTargetQuery(ctx context.Context, quick quickPlaybackSearch, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	if !preparingPlaybackFile(ctx) {
		if candidate, ok := quick.(interface {
			FindPlaybackCandidateBySHA1(context.Context, string, string) (*p115.DrivePlaybackFile, error)
		}); ok {
			// Only use the candidate optimization with a client that also verifies
			// the actual downloaded file before persisting a ready copy.
			if _, ok := quick.(interface {
				VerifiedPlaybackURL(context.Context, p115.DrivePlaybackFile, string, string) (string, error)
			}); ok {
				return candidate.FindPlaybackCandidateBySHA1(ctx, hash, cookie)
			}
		}
	}
	return quick.FindPlaybackFileBySHA1Quick(ctx, hash, cookie)
}

type playbackSearchPrefetch struct {
	hash   string
	file   *p115.DrivePlaybackFile
	err    error
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (p *playbackSearchPrefetch) stop() {
	if p != nil {
		p.cancel()
		p.wg.Wait()
	}
}

// Only a fresh external hash arms speculation. It remains untrusted and cannot
// create an origin or allow upload until source metadata agrees with it.
func (s *PlaybackCache) startPlaybackSearch(ctx context.Context, fast fastPlaybackClient, selected *PlaybackCacheSelection, scope string) *playbackSearchPrefetch {
	quick, ok := fast.(quickPlaybackSearch)
	if !ok {
		return nil
	}
	hash, _ := ctx.Value(playbackSHA1Hint{}).(string)
	if !validMaterialHash(hash) {
		return nil
	}
	origin, _ := ctx.Value(playbackMaterialOrigin{}).(string)
	if m, _ := s.lookupMaterial(origin, hash); m.ID != "" {
		return nil
	}
	for _, e := range s.entriesByTarget(selected.uid, hash) {
		if e.UID == selected.uid {
			return nil
		}
	}
	if s.waitPlaybackSearch(ctx, DirectLinkKey("quick-search", selected.uid, strings.ToUpper(hash)), scope) != nil {
		return nil
	}
	workCtx, cancel := context.WithCancel(ctx)
	p := &playbackSearchPrefetch{hash: hash, cancel: cancel}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.file, p.err = quickTargetQuery(workCtx, quick, hash, selected.cookie)
	}()
	s.logs.Add(ctx, "INFO", "目标 SHA1 专用查询与源文件资料并行；仅在源文件核验一致后采用结果")
	return p
}

func (s *PlaybackCache) searchPlaybackTarget(ctx context.Context, fast fastPlaybackClient, selected *PlaybackCacheSelection, info *p115.DownloadInfo, copies []PlaybackCacheEntry, prefetch *playbackSearchPrefetch) (*p115.DrivePlaybackFile, error) {
	for _, e := range copies {
		if e.UID == selected.uid && e.Status != "ready" {
			prefetch.stop()
			s.logs.Add(ctx, "INFO", "目标存在未确认上传记录，使用普通 SHA1 搜索核实，避免重复写入")
			return fast.FindPlaybackFileBySHA1(ctx, info.SHA1, selected.cookie)
		}
	}
	if prefetch != nil {
		if strings.EqualFold(prefetch.hash, info.SHA1) {
			prefetch.wg.Wait()
			return prefetch.file, prefetch.err
		}
		prefetch.stop()
		s.logs.Add(ctx, "INFO", "外部 SHA1 与源文件不一致，丢弃提前查询结果并按已核验 SHA1 重新查询")
	}
	if quick, ok := fast.(quickPlaybackSearch); ok {
		return quickTargetQuery(ctx, quick, info.SHA1, selected.cookie)
	}
	return fast.FindPlaybackFileBySHA1(ctx, info.SHA1, selected.cookie)
}
