package service

import (
	"strings"
	"time"
)

const maxPlaybackCopies = 100000

// Guarded by PlaybackCache.mu. Hot lookups copy only matching records, never
// the entire material library. Sets also preserve ambiguity instead of guessing.
type playbackIndex struct {
	materials, copies                    map[string]int
	origins, hashes, copyHashes, sources map[string]map[string]struct{}
	lastWritten                          map[string]time.Time
}

func indexAdd(index map[string]map[string]struct{}, key, id string) {
	if key == "" {
		return
	}
	if index[key] == nil {
		index[key] = make(map[string]struct{})
	}
	index[key][id] = struct{}{}
}
func indexRemove(index map[string]map[string]struct{}, key, id string) {
	delete(index[key], id)
	if len(index[key]) == 0 {
		delete(index, key)
	}
}
func (s *PlaybackCache) rebuildPlaybackIndexLocked() {
	s.index = playbackIndex{materials: make(map[string]int, len(s.doc.Materials)), copies: make(map[string]int, len(s.doc.Entries)), origins: make(map[string]map[string]struct{}), hashes: make(map[string]map[string]struct{}), copyHashes: make(map[string]map[string]struct{}), sources: make(map[string]map[string]struct{}), lastWritten: make(map[string]time.Time)}
	for i, m := range s.doc.Materials {
		s.indexMaterialLocked(m, i)
	}
	for i, e := range s.doc.Entries {
		s.indexEntryLocked(e, i)
		s.index.lastWritten[e.Key] = e.LastUsed
	}
}
func (s *PlaybackCache) indexMaterialLocked(m PlaybackMaterial, i int) {
	s.index.materials[m.ID] = i
	indexAdd(s.index.hashes, strings.ToUpper(m.SHA1), m.ID)
	for _, origin := range m.Origins {
		indexAdd(s.index.origins, origin, m.ID)
	}
}
func (s *PlaybackCache) unindexMaterialLocked(m PlaybackMaterial) {
	indexRemove(s.index.hashes, strings.ToUpper(m.SHA1), m.ID)
	for _, origin := range m.Origins {
		indexRemove(s.index.origins, origin, m.ID)
	}
}
func (s *PlaybackCache) indexEntryLocked(e PlaybackCacheEntry, i int) {
	s.index.copies[e.Key] = i
	indexAdd(s.index.copyHashes, strings.ToUpper(e.File.SHA1), e.Key)
	indexAdd(s.index.sources, e.SourceKey, e.Key)
}
func (s *PlaybackCache) unindexEntryLocked(e PlaybackCacheEntry) {
	indexRemove(s.index.copyHashes, strings.ToUpper(e.File.SHA1), e.Key)
	indexRemove(s.index.sources, e.SourceKey, e.Key)
}
func (s *PlaybackCache) materialByID(id string) (PlaybackMaterial, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.index.materials[id]
	if !ok {
		return PlaybackMaterial{}, false
	}
	return clonePlaybackMaterials(s.doc.Materials[i : i+1])[0], true
}
func (s *PlaybackCache) lookupMaterial(origin, hash string) (PlaybackMaterial, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var id string
	if ids := s.index.origins[origin]; len(ids) == 1 {
		for candidate := range ids {
			id = candidate
		}
	}
	// A conflicting known origin must be revalidated against the actual source.
	if id != "" {
		m := s.doc.Materials[s.index.materials[id]]
		if hash != "" && !strings.EqualFold(hash, m.SHA1) {
			return PlaybackMaterial{}, false
		}
		return clonePlaybackMaterials([]PlaybackMaterial{m})[0], false
	}
	if origin != "" && len(s.index.origins[origin]) > 1 {
		return PlaybackMaterial{}, false
	}
	ids := s.index.hashes[strings.ToUpper(hash)]
	if hash == "" || len(ids) != 1 {
		return PlaybackMaterial{}, false
	}
	for candidate := range ids {
		id = candidate
	}
	m := s.doc.Materials[s.index.materials[id]]
	if m.VerifiedAt.IsZero() {
		return PlaybackMaterial{}, false
	}
	return clonePlaybackMaterials([]PlaybackMaterial{m})[0], true
}
func (s *PlaybackCache) entryByKey(key string) (PlaybackCacheEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.index.copies[key]
	if !ok {
		return PlaybackCacheEntry{}, false
	}
	return s.doc.Entries[i], true
}
func (s *PlaybackCache) entriesByHash(hash string) []PlaybackCacheEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := s.index.copyHashes[strings.ToUpper(hash)]
	out := make([]PlaybackCacheEntry, 0, len(ids))
	for id := range ids {
		out = append(out, s.doc.Entries[s.index.copies[id]])
	}
	return out
}
func (s *PlaybackCache) entriesBySource(source string) []PlaybackCacheEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := s.index.sources[source]
	out := make([]PlaybackCacheEntry, 0, len(ids))
	for id := range ids {
		out = append(out, s.doc.Entries[s.index.copies[id]])
	}
	return out
}
func (s *PlaybackCache) bindingConfig() PlaybackCacheConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	config := s.doc.Config
	config.Bindings = append([]PlaybackBinding{}, config.Bindings...)
	return config
}
