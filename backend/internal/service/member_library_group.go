package service

import "context"

// SetLibrarySyncGroup changes only who owns the user's media-library selection.
// Group membership is persisted before waking the normal reconciliation worker.
func (s *MemberPlaybackService) SetLibrarySyncGroup(ctx context.Context, id string, skip bool) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	r := s.record(id)
	if r.UserID == "" {
		return memberError("用户不存在；管理员固定属于不同步组")
	}
	c, client, err := s.config()
	if err != nil {
		return err
	}
	if r.ServerID != c.ServerID || r.EmbyURL != c.URL {
		return memberError("Emby 服务不一致")
	}
	u, err := client.ManagedUser(ctx, r.EmbyID)
	if err != nil {
		return memberError("无法确认 Emby 用户")
	}
	if u.Policy["IsAdministrator"] == true {
		return memberError("管理员固定属于不同步组")
	}
	if r.SkipLibrarySync == skip {
		return nil
	}
	r.SkipLibrarySync = skip
	if !skip {
		// Do not serve playback with the old manual selection while rejoining.
		r.PolicyEnabled = false
		r.DownloadEnabled = false
		r.AppliedRevision = 0
	}
	if err := s.save(ctx, r); err != nil {
		return err
	}
	if s.ClearDirectLinks != nil {
		s.ClearDirectLinks()
	}
	s.RequestSync()
	return nil
}
