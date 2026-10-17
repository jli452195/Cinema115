package service

import (
	"context"
	"errors"
	"strings"

	"strm115/backend/internal/model"
)

var ErrProxyPlaybackConflict = errors.New("反代播放设置已变化，请重新读取后重试")

// This controls only new playback authorizations through Cinema115. It never
// changes Emby account status, login, download permissions or other users.
type MemberProxyPlaybackState struct {
	UserID   string `json:"userId"`
	Enabled  bool   `json:"enabled"`
	Revision uint64 `json:"revision"`
}

func proxyPlaybackState(r model.MemberPlayback) MemberProxyPlaybackState {
	return MemberProxyPlaybackState{UserID: r.UserID, Enabled: !r.ProxyPlaybackDisabled, Revision: r.ProxyPlaybackRevision}
}

func (s *MemberPlaybackService) proxyPlaybackTarget(id string) (model.MemberPlayback, error) {
	r := s.record(id)
	c := s.emby.snapshot()
	if c.ServerID == "" || r.UserID == "" || !r.Provisioned || r.PlaybackOnly || r.Administrator ||
		r.EmbyID == "" || r.UserID != embyProfileID(c.ServerID, r.EmbyID) || r.ServerID != c.ServerID || r.EmbyURL != c.URL ||
		strings.HasPrefix(strings.ToLower(r.EmbyName), "c115_") || s.deletionBlocked(id) {
		return r, memberError("仅支持当前 Emby 服务的普通账号；请先核实用户身份")
	}
	return r, nil
}

func (s *MemberPlaybackService) ReadMemberProxyPlayback(ctx context.Context, id string) (MemberProxyPlaybackState, error) {
	if err := s.lock(ctx); err != nil {
		return MemberProxyPlaybackState{}, err
	}
	defer s.unlock()
	r, err := s.proxyPlaybackTarget(id)
	if err != nil {
		return MemberProxyPlaybackState{}, err
	}
	return proxyPlaybackState(r), nil
}

func (s *MemberPlaybackService) SetMemberProxyPlayback(ctx context.Context, id string, enabled bool, expectedRevision uint64) (MemberProxyPlaybackState, error) {
	if err := s.lock(ctx); err != nil {
		return MemberProxyPlaybackState{}, err
	}
	defer s.unlock()
	release, err := s.useMember(id)
	if err != nil {
		return MemberProxyPlaybackState{}, err
	}
	defer release()
	r, err := s.proxyPlaybackTarget(id)
	if err != nil {
		return MemberProxyPlaybackState{}, err
	}
	state := proxyPlaybackState(r)
	if state.Enabled == enabled {
		return state, nil // A repeated desired state has no additional effects.
	}
	if state.Revision != expectedRevision {
		return state, ErrProxyPlaybackConflict
	}
	r.ProxyPlaybackDisabled = !enabled
	r.ProxyPlaybackRevision++
	if err := s.save(ctx, r); err != nil {
		return MemberProxyPlaybackState{}, err
	}
	// All link paths recheck local authorization, including cached URLs. Do not
	// release active capacity, clear other users' caches or write to Emby.
	return proxyPlaybackState(r), nil
}
