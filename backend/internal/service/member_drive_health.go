package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"strm115/backend/internal/model"
)

// Health is an advisory snapshot, never playback authorization. It is shared by
// configuration ID, not UID, and rebuilt after restart. No credentials leave memory.
type DriveHealthView struct {
	State     string `json:"state"`
	Message   string `json:"message"`
	CheckedAt string `json:"checkedAt,omitempty"`
}

type driveHealthEntry struct {
	fingerprint [32]byte
	view        DriveHealthView
}

func driveHealthFingerprint(a P115Account) [32]byte {
	// Name, summary and revision can change without changing playback credentials.
	data, _ := json.Marshal([]any{a.ID, a.UID, a.Cookie, a.PlaybackParentCID, a.Primary, a.Ownership, a.OwnerUserID})
	return sha256.Sum256(data)
}

func (s *MemberPlaybackService) healthAccount(r model.MemberPlayback) (P115Account, error) {
	if s.accounts == nil {
		return P115Account{}, memberError("网盘配置未初始化")
	}
	if r.Suspended {
		return P115Account{}, memberError("个人网盘已解绑")
	}
	return s.playbackAccount(r)
}

func (s *MemberPlaybackService) DriveHealth(r model.MemberPlayback) DriveHealthView {
	a, err := s.healthAccount(r)
	if err != nil {
		return DriveHealthView{State: "unavailable", Message: err.Error()}
	}
	s.driveHealthMu.Lock()
	entry, ok := s.driveHealth[a.ID]
	s.driveHealthMu.Unlock()
	if ok && entry.fingerprint == driveHealthFingerprint(a) {
		return entry.view
	}
	s.requestHealthCheck()
	return DriveHealthView{State: "pending", Message: "等待检查"}
}

// Manual refresh invalidates only health snapshots; it cannot write Emby policy.
func (s *MemberPlaybackService) RequestDriveRefresh() {
	s.driveHealthMu.Lock()
	clear(s.driveHealth)
	s.driveHealthEpoch++
	s.driveHealthMu.Unlock()
	s.requestHealthCheck()
}

func (s *MemberPlaybackService) requestHealthCheck() {
	select {
	case s.healthWake <- struct{}{}:
	default:
	}
}

func (s *MemberPlaybackService) runDriveHealth(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		s.refreshMissingDriveHealth(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.healthWake:
		case <-ticker.C:
		}
	}
}

type MemberDriveStatus struct {
	UserID      string          `json:"userId"`
	DriveStatus DriveHealthView `json:"driveStatus"`
}

func (s *MemberPlaybackService) DriveStatuses() []MemberDriveStatus {
	records := s.Records()
	out := make([]MemberDriveStatus, 0, len(records))
	for _, r := range records {
		out = append(out, MemberDriveStatus{UserID: r.UserID, DriveStatus: s.DriveHealth(r)})
	}
	return out
}

func (s *MemberPlaybackService) refreshMissingDriveHealth(ctx context.Context) {
	if s.accounts == nil || s.checker == nil {
		return
	}
	seen := make(map[string]bool)
	for _, r := range s.Records() {
		if ctx.Err() != nil {
			return
		}
		a, err := s.healthAccount(r)
		if err != nil || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		if err := s.refreshDriveHealth(ctx, a, false); err != nil && ctx.Err() != nil {
			return
		}
	}
	// Retain only existing configurations, bounding the runtime cache across replacements.
	accounts := s.accounts.List()
	live := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		live[a.ID] = true
	}
	s.driveHealthMu.Lock()
	for id := range s.driveHealth {
		if !live[id] {
			delete(s.driveHealth, id)
		}
	}
	s.driveHealthMu.Unlock()
}

func (s *MemberPlaybackService) refreshDriveHealth(ctx context.Context, a P115Account, force bool) error {
	if s.accounts == nil || s.checker == nil {
		return memberError("网盘检查未初始化")
	}
	// One bounded probe at a time across both the worker and explicit single-user checks.
	select {
	case s.driveHealthGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.driveHealthGate }()
	fingerprint := driveHealthFingerprint(a)
	current, err := s.accounts.Get(a.ID)
	if err != nil || driveHealthFingerprint(current) != fingerprint {
		return nil
	}
	s.driveHealthMu.Lock()
	entry, ok := s.driveHealth[a.ID]
	epoch := s.driveHealthEpoch
	s.driveHealthMu.Unlock()
	if !force && ok && entry.fingerprint == fingerprint {
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	v := DriveHealthView{State: "ready", Message: "最近检查通过"}
	valid, err := s.checker.CheckCookie(probe, a.Cookie)
	switch {
	case err != nil:
		v.State, v.Message = "error", "暂时无法检查网盘登录，请稍后刷新"
	case !valid:
		v.State, v.Message = "invalid_login", "登录已失效，请重新登录"
	case !a.Primary:
		if err := s.drive.ValidateCacheParent(probe, a.PlaybackParentCID, a.Cookie); err != nil {
			v.State, v.Message = "invalid_directory", "目录暂不可用，请检查目录及账号"
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	v.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	current, err = s.accounts.Get(a.ID)
	if err != nil || driveHealthFingerprint(current) != fingerprint {
		return nil
	}
	s.driveHealthMu.Lock()
	if s.driveHealthEpoch == epoch {
		s.driveHealth[a.ID] = driveHealthEntry{fingerprint: fingerprint, view: v}
	}
	s.driveHealthMu.Unlock()
	return nil
}
