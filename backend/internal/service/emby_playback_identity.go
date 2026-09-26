package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type embyPlaybackIdentity struct {
	UserID  string    `json:"userId"`
	Created time.Time `json:"created"`
}

// EmbyPlaybackIdentities records identities from trusted upstream login responses.
// It stores only a server-scoped token digest, never the token or password.
// A mapping is not authorization: playback must still recheck with Emby each time.
type EmbyPlaybackIdentities struct {
	mu      sync.Mutex
	store   *SessionStore
	entries map[string]embyPlaybackIdentity
}

func NewEmbyPlaybackIdentities(store *SessionStore) (*EmbyPlaybackIdentities, error) {
	s := &EmbyPlaybackIdentities{store: store, entries: make(map[string]embyPlaybackIdentity)}
	if store != nil && store.Value() != "" {
		if err := json.Unmarshal([]byte(store.Value()), &s.entries); err != nil || s.entries == nil || len(s.entries) > 4096 {
			return nil, fmt.Errorf("Emby 播放身份记录无效")
		}
	}
	return s, nil
}

func embyIdentityKey(server, token string) string {
	digest := sha256.Sum256([]byte(server + "\x00" + token))
	return hex.EncodeToString(digest[:])
}

func (s *EmbyPlaybackIdentities) UserID(server, token string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[embyIdentityKey(server, token)]
	if time.Since(entry.Created) > 180*24*time.Hour {
		return ""
	}
	return entry.UserID
}

// Remember must only be called with an authenticated upstream login result.
func (s *EmbyPlaybackIdentities) Remember(server, token, userID string) error {
	if server == "" || token == "" || len(token) > 2048 || userID == "" || len(userID) > 128 {
		return fmt.Errorf("Emby 登录身份无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := embyIdentityKey(server, token)
	if prior, ok := s.entries[key]; ok && prior.UserID != userID {
		return fmt.Errorf("Emby 登录身份冲突")
	}
	entries := make(map[string]embyPlaybackIdentity, len(s.entries)+1)
	for k, v := range s.entries {
		if time.Since(v.Created) <= 180*24*time.Hour {
			entries[k] = v
		}
	}
	if len(entries) >= 4096 {
		oldest := ""
		for k, v := range entries {
			if oldest == "" || v.Created.Before(entries[oldest].Created) {
				oldest = k
			}
		}
		delete(entries, oldest)
	}
	entries[key] = embyPlaybackIdentity{UserID: userID, Created: time.Now()}
	if s.store != nil {
		data, err := json.Marshal(entries)
		if err != nil {
			return fmt.Errorf("编码 Emby 播放身份: %w", err)
		}
		if err := s.store.Save(string(data)); err != nil {
			return fmt.Errorf("保存 Emby 播放身份: %w", err)
		}
	}
	s.entries = entries
	return nil
}
