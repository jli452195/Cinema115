package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

const playbackDonorBudget = 4 * time.Second

var errPlaybackDonorIdentity = errors.New("校验来源文件身份不一致")

type playbackDonorState struct {
	busy            bool
	lastUsed, until time.Time
}
type playbackDonor struct {
	account  P115Account
	entry    PlaybackCacheEntry
	lastUsed time.Time
}

func (s *PlaybackCache) donorCandidates(selected *PlaybackCacheSelection, entries []PlaybackCacheEntry, m PlaybackMaterial) []playbackDonor {
	s.donorMu.Lock()
	defer s.donorMu.Unlock()
	byUID := make(map[string]P115Account, len(selected.donors))
	for _, a := range selected.donors {
		if a.UID == "" || a.UID == selected.uid || p115.CookieUserID(a.Cookie) != a.UID {
			continue
		}
		if old, ok := byUID[a.UID]; !ok || a.ID < old.ID {
			byUID[a.UID] = a
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].LastUsed.After(entries[j].LastUsed) })
	out := make([]playbackDonor, 0, len(byUID))
	for _, e := range entries {
		a, ok := byUID[e.UID]
		state := s.donorStates[e.UID]
		if !ok || state.busy || time.Now().Before(state.until) || time.Now().Before(s.donorFiles[e.Key]) || e.Status != "ready" || e.File.ID == "" || e.File.PickCode == "" || e.File.Size != m.Size || !strings.EqualFold(e.File.SHA1, m.SHA1) {
			continue
		}
		out = append(out, playbackDonor{a, e, state.lastUsed})
		delete(byUID, e.UID)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].lastUsed.Before(out[j].lastUsed) })
	return out
}

func (s *PlaybackCache) leaseDonor(uid string) bool {
	s.donorMu.Lock()
	defer s.donorMu.Unlock()
	state := s.donorStates[uid]
	if state.busy || time.Now().Before(state.until) {
		return false
	}
	if s.donorStates == nil {
		s.donorStates = make(map[string]playbackDonorState)
	}
	// Bound runtime history; active/cooling entries cannot be evicted.
	if len(s.donorStates) >= 2048 {
		for k, v := range s.donorStates {
			if !v.busy && time.Now().After(v.until) {
				delete(s.donorStates, k)
			}
		}
		if len(s.donorStates) >= 2048 {
			return false
		}
	}
	state.busy, state.lastUsed = true, time.Now()
	s.donorStates[uid] = state
	return true
}
func (s *PlaybackCache) finishDonor(uid, fileKey string, err error) {
	s.donorMu.Lock()
	defer s.donorMu.Unlock()
	state := s.donorStates[uid]
	state.busy = false
	var httpFailure *p115.HTTPError
	fileFailure := errors.Is(err, errPlaybackDonorIdentity) || errors.As(err, &httpFailure) && httpFailure.Status == 404
	if fileFailure {
		if s.donorFiles == nil {
			s.donorFiles = make(map[string]time.Time)
		}
		if len(s.donorFiles) >= 2048 {
			for k, until := range s.donorFiles {
				if time.Now().After(until) {
					delete(s.donorFiles, k)
				}
			}
		}
		if len(s.donorFiles) < 2048 {
			s.donorFiles[fileKey] = time.Now().Add(time.Minute)
		}
		err = nil // Other verified files in this UID remain usable.
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		delay := 15 * time.Second
		var h *p115.HTTPError
		if errors.As(err, &h) {
			if h.Status == 401 || h.Status == 403 {
				delay = time.Minute
			}
			if h.Status == 429 {
				delay = max(time.Minute, min(h.RetryAfter, 5*time.Minute))
			}
		}
		state.until = time.Now().Add(delay)
	}
	s.donorStates[uid] = state
}

// All probes share one deadline. Busy UIDs are skipped, never waited on while
// holding a target lease. Donor credentials come only from the request snapshot.
func (s *PlaybackCache) readPlaybackMaterialRange(ctx context.Context, reader playbackRangeClient, selected *PlaybackCacheSelection, entries []PlaybackCacheEntry, m PlaybackMaterial, start, end int64) (string, error) {
	probeCtx, cancel := context.WithTimeout(p115.WithDonorBudget(ctx), playbackDonorBudget)
	defer cancel()
	attempts := 0
	for _, candidate := range s.donorCandidates(selected, entries, m) {
		if err := probeCtx.Err(); err != nil {
			return "", err
		}
		if !s.leaseDonor(candidate.account.UID) {
			continue
		}
		attempts++
		a, e := candidate.account, candidate.entry
		s.stats.donorReads.Add(1)
		var info *p115.DownloadInfo
		var err error
		if a.Ownership == "member" || a.OwnerUserID != "" {
			if s.Members == nil {
				err = fmt.Errorf("个人来源不可用")
			} else {
				err = s.Members.authorizePersonalDonor(probeCtx, a)
			}
		}
		if err == nil {
			if fused, ok := reader.(playbackRangeSourceClient); ok {
				info, err = fused.PlaybackPickCodeRangeSource(probeCtx, a.ID, e.File.PickCode, a.Cookie)
			} else {
				info, err = reader.PlaybackPickCodeSource(probeCtx, a.ID, e.File.PickCode, a.Cookie)
			}
		}
		var hash string
		if err == nil {
			if info == nil || info.FileID != e.File.ID || info.Size != m.Size || !strings.EqualFold(info.SHA1, m.SHA1) {
				err = errPlaybackDonorIdentity
			} else {
				hash, err = reader.PlaybackRangeSHA1(probeCtx, info, start, end)
			}
		}
		if err == nil && !validMaterialHash(hash) {
			err = fmt.Errorf("校验片段无效")
		}
		s.finishDonor(a.UID, e.Key, err)
		if err == nil {
			label := "共享小号"
			if a.OwnerUserID != "" || a.Ownership == "member" || IsMemberAccount(a.ID) {
				label = "个人网盘"
			}
			s.logs.Add(ctx, "INFO", "秒传校验片段由已有"+label+"副本提供")
			return hash, nil
		}
		if attempts >= 3 {
			break
		}
	}
	return "", fmt.Errorf("已知副本暂不可读")
}
