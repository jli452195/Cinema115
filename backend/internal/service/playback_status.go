package service

import "sync/atomic"

type playbackStats struct{ targetHits, materialHits, rangeHits, donorReads, primaryReads atomic.Uint64 }
type PlaybackStatsView struct {
	TargetHits   uint64 `json:"targetHits"`
	MaterialHits uint64 `json:"materialHits"`
	RangeHits    uint64 `json:"rangeHits"`
	DonorReads   uint64 `json:"donorReads"`
	PrimaryReads uint64 `json:"primaryReads"`
}
type PlaybackAdminStatus struct {
	PlaybackCacheStatus
	Stats         PlaybackStatsView `json:"stats"`
	PrivateCopies int               `json:"privateCopies"`
}

// Expose administrative drives only. Unconfigured history is withheld because
// a deleted personal configuration must not reveal its former UID/directory.
func (s *PlaybackCache) AdminStatus() PlaybackAdminStatus {
	out := PlaybackAdminStatus{PlaybackCacheStatus: s.Status()}
	s.accounts.mu.RLock()
	publicIDs := make(map[string]bool, len(s.accounts.value.Accounts))
	for _, a := range s.accounts.value.Accounts {
		if a.OwnerUserID == "" && a.Ownership != "member" && !IsMemberAccount(a.ID) {
			publicIDs[a.ID] = true
		}
	}
	s.accounts.mu.RUnlock()
	entries := make([]PlaybackCacheEntry, 0, len(out.Entries))
	for _, e := range out.Entries {
		if !publicIDs[e.AccountID] {
			out.PrivateCopies++
			continue
		}
		e.SourceKey, e.TargetScope = "", ""
		e.AccountID, e.ParentCID = "", ""
		entries = append(entries, e)
	}
	out.Entries = entries
	out.Config.Bindings = nil
	out.Directories = nil
	for i := range out.Materials {
		out.Materials[i].Origins = nil
	}
	out.Stats = PlaybackStatsView{s.stats.targetHits.Load(), s.stats.materialHits.Load(), s.stats.rangeHits.Load(), s.stats.donorReads.Load(), s.stats.primaryReads.Load()}
	return out
}
