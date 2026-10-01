package service

import "strm115/backend/internal/model"

// Metadata never advances the credential revision or crosses account entries.
func (s *P115Accounts) SaveSummary(snapshot P115Account, summary model.P115Summary) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.copy()
	for i, current := range value.Accounts {
		if current.ID != snapshot.ID {
			continue
		}
		if current.Revision != snapshot.Revision || current.Cookie != snapshot.Cookie {
			return ErrAccountChanged
		}
		if current.Ownership == "member" || current.OwnerUserID != "" || current.Cookie == "" {
			return ErrAccountInput
		}
		if current.Summary == summary {
			return nil
		}
		value.Accounts[i].Summary = summary
		return s.commit(value)
	}
	return ErrAccountMissing
}
