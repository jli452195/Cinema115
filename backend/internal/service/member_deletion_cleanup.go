package service

import "strm115/backend/internal/model"

// Manual task history contains no credentials and is retained as diagnostics.
// Its selected user must not remain a runnable configuration after deletion.
func (s *InstantUploads) ForgetDeletedMember(t model.MemberDeletionTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doc.Config.UserID != t.EmbyID || t.EmbyID == "" {
		return nil
	}
	if s.running {
		return ErrInstantBusy
	}
	doc := s.doc
	doc.Config = InstantUploadConfig{}
	return s.saveLocked(doc)
}

func (s *PlaybackCache) ForgetDeletedMember(t model.MemberDeletionTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doc.Config.EmbyURL != t.EmbyURL || t.EmbyID == "" {
		return nil
	}
	doc := s.doc
	doc.Config.Bindings = make([]PlaybackBinding, 0, len(s.doc.Config.Bindings))
	for _, b := range s.doc.Config.Bindings {
		if b.UserID != t.EmbyID {
			doc.Config.Bindings = append(doc.Config.Bindings, b)
		}
	}
	if len(doc.Config.Bindings) == len(s.doc.Config.Bindings) {
		return nil
	}
	return s.saveLocked(doc)
}
