package service

import (
	"context"
	"time"

	"strm115/backend/internal/model"
)

func (s *EmbyService) RecentUpdateGroups(ctx context.Context, start, end time.Time, limit, offset int) ([]model.MediaUpdateGroup, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.config
	scope := embyScope(c)
	state, err := s.db.EmbyState(ctx, scope)
	if err != nil {
		return nil, "failed", err
	}
	if state.Active == "" {
		return []model.MediaUpdateGroup{}, "waiting", nil
	}
	status := "waiting"
	if state.UpdatesGeneration == state.Active {
		status = "building"
		if state.UpdatesError != "" {
			status = "failed"
		}
		if state.UpdatesReady {
			status = "ready"
		}
	}
	if status != "ready" {
		return []model.MediaUpdateGroup{}, status, nil
	}
	groups, err := s.db.MediaUpdateGroups(ctx, scope, start.Unix(), end.Unix(), limit, offset)
	return groups, status, err
}
