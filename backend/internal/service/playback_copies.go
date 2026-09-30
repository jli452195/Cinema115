package service

import (
	"context"
	"fmt"
	"strings"
)

func (s *PlaybackCache) mappedSource(key string) string {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	return s.sourceMappings[key]
}
func (s *PlaybackCache) forgetSource(key string) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	delete(s.sourceMappings, key)
}

// A configuration is a credential handle, never the owner of a cloud file.
// Changing handles requires revalidation using the target's own credentials.
func (s *PlaybackCache) verifyReusableCopy(ctx context.Context, e PlaybackCacheEntry, selected *PlaybackCacheSelection) error {
	if e.UID != selected.uid {
		return fmt.Errorf("文件不属于当前目标 UID")
	}
	scope := DirectLinkKey(selected.binding.AccountID, selected.uid, selected.cookie, fmt.Sprint(selected.accountRevision))
	if e.TargetScope == scope {
		return nil
	}
	reader, ok := s.client.(playbackRangeClient)
	if !ok {
		return fmt.Errorf("目标文件需要重新核验")
	}
	info, err := reader.PlaybackPickCodeSource(ctx, selected.binding.AccountID, e.File.PickCode, selected.cookie)
	if err != nil || info == nil || info.FileID != e.File.ID || info.Size != e.File.Size || !strings.EqualFold(info.SHA1, e.File.SHA1) {
		return fmt.Errorf("目标文件身份核验失败")
	}
	return nil
}

// Atomically replace an upload intent with the verified UID/file record.
func (s *PlaybackCache) storeReadyCopy(e PlaybackCacheEntry) (PlaybackCacheEntry, error) {
	key := DirectLinkKey("copy", e.UID, e.File.ID)
	if e.UID == "" || e.File.ID == "" {
		return e, fmt.Errorf("文件副本身份无效")
	}
	if e.Key == key {
		return e, s.put(e)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if store, ok := s.store.(interface {
		ReplaceEntry(string, PlaybackCacheEntry) error
	}); ok {
		oldKey := e.Key
		e.Key = key
		_, oldExists := s.index.copies[oldKey]
		_, newExists := s.index.copies[key]
		if !oldExists && !newExists && len(s.doc.Entries) >= maxPlaybackCopies {
			return e, fmt.Errorf("账号副本记录达到上限")
		}
		if err := store.ReplaceEntry(oldKey, e); err != nil {
			return e, err
		}
		if i, ok := s.index.copies[oldKey]; ok {
			s.unindexEntryLocked(s.doc.Entries[i])
			delete(s.index.copies, oldKey)
			delete(s.index.lastWritten, oldKey)
			last := len(s.doc.Entries) - 1
			if i != last {
				s.doc.Entries[i] = s.doc.Entries[last]
				s.index.copies[s.doc.Entries[i].Key] = i
			}
			s.doc.Entries = s.doc.Entries[:last]
		}
		i, exists := s.index.copies[key]
		if exists {
			s.unindexEntryLocked(s.doc.Entries[i])
			s.doc.Entries[i] = e
		} else {
			i = len(s.doc.Entries)
			s.doc.Entries = append(s.doc.Entries, e)
		}
		s.indexEntryLocked(e, i)
		s.index.lastWritten[key] = e.LastUsed
		return e, nil
	}
	doc := s.doc
	doc.Entries = make([]PlaybackCacheEntry, 0, len(s.doc.Entries)+1)
	for _, old := range s.doc.Entries {
		if old.Key != e.Key && old.Key != key {
			doc.Entries = append(doc.Entries, old)
		}
	}
	if len(doc.Entries) >= maxPlaybackCopies {
		return e, fmt.Errorf("账号副本记录达到上限")
	}
	e.Key = key
	doc.Entries = append(doc.Entries, e)
	return e, s.saveLocked(doc)
}
