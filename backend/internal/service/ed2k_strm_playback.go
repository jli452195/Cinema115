package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/strm"
)

// ResolveURL 使用本次播放器 UA 核验直链；账号与凭据范围进入内存缓存键。
func (s *ED2KSTRMService) ResolveURL(ctx context.Context, claim strm.ED2K, ua string, allowRestore bool) (string, error) {
	v, known, err := s.claimResource(claim)
	if err != nil {
		return "", err
	}
	a, err := s.sourceAccount(ctx)
	if err != nil {
		return "", err
	}
	if known && (v.AccountID != a.ID || v.UID != a.UID) {
		return "", generationInputError("此资源缓存不属于当前主账号")
	}
	key := DirectLinkKey("ed2k-primary-url", ed2kCredentialScope(a), strings.ToUpper(claim.SHA1), strconv.FormatInt(claim.Size, 10), ua)
	value, _, _, err := s.links.Get(ctx, key, 5*time.Minute, DirectLinkCacheEntry{}, func(ctx context.Context) (DirectLinkValue, error) {
		if known && v.FileID != "" && v.PickCode != "" {
			link, err := s.Client.VerifiedPlaybackURL(ctx, p115.DrivePlaybackFile{ID: v.FileID, PickCode: v.PickCode, SHA1: claim.SHA1, Size: claim.Size, Name: claim.Name}, a.Cookie, ua)
			if err == nil {
				return DirectLinkValue{URL: link}, nil
			}
			if !errors.Is(err, p115.ErrDriveFileMissing) {
				return DirectLinkValue{}, err
			}
		}
		file, err := s.Client.FindPlaybackFileBySHA1Quick(ctx, claim.SHA1, a.Cookie)
		if err != nil {
			return DirectLinkValue{}, err
		}
		if file == nil {
			return DirectLinkValue{}, s.requestSourceRestore(ctx, claim, v, known, allowRestore, a)
		}
		if file.Size != claim.Size || !strings.EqualFold(file.SHA1, claim.SHA1) {
			return DirectLinkValue{}, generationInputError("主账号查询文件与 STRM 内容身份冲突")
		}
		link, err := s.Client.VerifiedPlaybackURL(ctx, *file, a.Cookie, ua)
		if err != nil {
			return DirectLinkValue{}, err
		}
		if known {
			s.rememberLocation(ctx, v, *file)
		}
		return DirectLinkValue{URL: link}, nil
	})
	return value.URL, err
}

func (s *ED2KSTRMService) rememberLocation(ctx context.Context, v model.ED2KSTRMResource, file p115.DrivePlaybackFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ed2kResourceKey(v.Hash, v.Size)
	current, ok := s.resources[key]
	if !ok || s.active == key || current.FileID == file.ID && current.PickCode == file.PickCode {
		return
	}
	current.FileID, current.PickCode, current.CacheState = file.ID, file.PickCode, "ready"
	if err := s.Store.SaveED2KSTRMResource(ctx, current); err == nil {
		s.resources[key] = current
	}
}
