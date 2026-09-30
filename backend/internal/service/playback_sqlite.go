package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"strm115/backend/internal/storage"
)

type playbackDatabase interface {
	LoadPlayback(context.Context) (storage.PlaybackBatch, error)
	SavePlayback(context.Context, storage.PlaybackBatch) error
}

// Access is serialized by PlaybackCache.mu. Snapshots contain no credentials.
type playbackSQLiteStore struct {
	db                playbackDatabase
	ctx               context.Context
	initial           string
	settings          string
	materials, copies map[string]string
}

func NewSQLitePlaybackCache(ctx context.Context, db playbackDatabase, legacy *SessionStore, accounts *P115Accounts, client playbackCacheClient, logs *EmbyProxyLogs) (*PlaybackCache, error) {
	rows, err := db.LoadPlayback(ctx)
	if err != nil {
		return nil, fmt.Errorf("加载播放资料库: %w", err)
	}
	adapter := &playbackSQLiteStore{db: db, ctx: ctx, materials: make(map[string]string, len(rows.Materials)), copies: make(map[string]string, len(rows.Copies))}
	doc := PlaybackCacheStatus{Config: PlaybackCacheConfig{RetentionHours: 72, Bindings: []PlaybackBinding{}}, Entries: []PlaybackCacheEntry{}, Materials: []PlaybackMaterial{}}
	if rows.Settings == nil && legacy != nil && legacy.Value() != "" {
		// One atomic import of the existing document; subsequent starts use SQLite
		// exclusively. Keep the encrypted source untouched as a rollback copy.
		if err := json.Unmarshal([]byte(legacy.Value()), &doc); err != nil {
			return nil, fmt.Errorf("读取原播放配置: %w", err)
		}
		if err := validatePlaybackMaterials(doc.Materials); err != nil {
			return nil, err
		}
	} else {
		if rows.Settings != nil {
			if err := json.Unmarshal([]byte(*rows.Settings), &doc); err != nil {
				return nil, fmt.Errorf("解析播放设置: %w", err)
			}
			adapter.settings = *rows.Settings
		}
		for _, row := range rows.Materials {
			var m PlaybackMaterial
			if err := json.Unmarshal([]byte(row.Record), &m); err != nil {
				return nil, fmt.Errorf("解析文件资料: %w", err)
			}
			if m.ID != row.ID || m.SHA1 != row.SHA1 || m.Size != row.Size {
				return nil, fmt.Errorf("文件资料索引不一致")
			}
			m.Origins = row.Origins
			for _, r := range row.Ranges {
				var v PlaybackRange
				if err := json.Unmarshal([]byte(r.Record), &v); err != nil {
					return nil, err
				}
				if v.Start != r.Start || v.End != r.End {
					return nil, fmt.Errorf("片段索引不一致")
				}
				m.Ranges = append(m.Ranges, v)
			}
			doc.Materials = append(doc.Materials, m)
			raw, err := json.Marshal(m)
			if err != nil {
				return nil, err
			}
			adapter.materials[m.ID] = string(raw)
		}
		for _, row := range rows.Copies {
			var e PlaybackCacheEntry
			if err := json.Unmarshal([]byte(row.Record), &e); err != nil {
				return nil, err
			}
			if e.Key != row.ID || e.AccountID != row.AccountID || e.File.SHA1 != row.SHA1 || e.SourceKey != row.SourceKey {
				return nil, fmt.Errorf("账号副本索引不一致")
			}
			doc.Entries = append(doc.Entries, e)
			adapter.copies[e.Key] = row.Record
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if rows.Settings == nil {
		if err := adapter.Save(string(raw)); err != nil {
			return nil, err
		}
	}
	adapter.initial = string(raw)
	s, err := newPlaybackCache(ctx, adapter, accounts, client, logs)
	adapter.initial = ""
	return s, err
}

func (s *playbackSQLiteStore) Value() string { return s.initial }
func materialRow(m PlaybackMaterial) (storage.PlaybackMaterialRow, error) {
	row := storage.PlaybackMaterialRow{ID: m.ID, SHA1: m.SHA1, Size: m.Size, Origins: m.Origins, Ranges: make([]storage.PlaybackRangeRow, 0, len(m.Ranges))}
	for _, r := range m.Ranges {
		raw, err := json.Marshal(r)
		if err != nil {
			return row, err
		}
		row.Ranges = append(row.Ranges, storage.PlaybackRangeRow{Start: r.Start, End: r.End, Record: string(raw)})
	}
	m.Origins = nil
	m.Ranges = nil
	raw, err := json.Marshal(m)
	row.Record = string(raw)
	return row, err
}
func copyRow(e PlaybackCacheEntry, raw string) storage.PlaybackCopyRow {
	return storage.PlaybackCopyRow{ID: e.Key, AccountID: e.AccountID, SHA1: e.File.SHA1, SourceKey: e.SourceKey, Record: raw}
}

func (s *playbackSQLiteStore) ReplaceEntry(oldKey string, e PlaybackCacheEntry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	batch := storage.PlaybackBatch{Copies: []storage.PlaybackCopyRow{copyRow(e, string(raw))}}
	if oldKey != e.Key {
		batch.DeleteCopies = []string{oldKey}
	}
	if err := s.write(batch); err != nil {
		return err
	}
	delete(s.copies, oldKey)
	s.copies[e.Key] = string(raw)
	return nil
}
func (s *playbackSQLiteStore) write(batch storage.PlaybackBatch) error {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	if err := s.db.SavePlayback(ctx, batch); err != nil {
		return fmt.Errorf("保存播放资料库: %w", err)
	}
	return nil
}
func (s *playbackSQLiteStore) PutEntry(e PlaybackCacheEntry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := s.write(storage.PlaybackBatch{Copies: []storage.PlaybackCopyRow{copyRow(e, string(raw))}}); err != nil {
		return err
	}
	s.copies[e.Key] = string(raw)
	return nil
}
func (s *playbackSQLiteStore) PutMaterial(m PlaybackMaterial) error {
	row, err := materialRow(m)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := s.write(storage.PlaybackBatch{Materials: []storage.PlaybackMaterialRow{row}}); err != nil {
		return err
	}
	s.materials[m.ID] = string(raw)
	return nil
}

// Infrequent configuration/removal operations diff the document and write only
// changed rows. Playback updates use PutEntry/PutMaterial directly.
func (s *playbackSQLiteStore) Save(raw string) error {
	var doc PlaybackCacheStatus
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return err
	}
	settings, err := json.Marshal(struct {
		Config      PlaybackCacheConfig      `json:"config"`
		Directories []PlaybackCacheDirectory `json:"directories,omitempty"`
	}{doc.Config, doc.Directories})
	if err != nil {
		return err
	}
	batch := storage.PlaybackBatch{}
	value := string(settings)
	if value != s.settings {
		batch.Settings = &value
	}
	materials := make(map[string]string, len(doc.Materials))
	copies := make(map[string]string, len(doc.Entries))
	for _, m := range doc.Materials {
		encoded, err := json.Marshal(m)
		if err != nil {
			return err
		}
		materials[m.ID] = string(encoded)
		if s.materials[m.ID] != string(encoded) {
			row, err := materialRow(m)
			if err != nil {
				return err
			}
			batch.Materials = append(batch.Materials, row)
		}
	}
	for _, e := range doc.Entries {
		encoded, err := json.Marshal(e)
		if err != nil {
			return err
		}
		copies[e.Key] = string(encoded)
		if s.copies[e.Key] != string(encoded) {
			batch.Copies = append(batch.Copies, copyRow(e, string(encoded)))
		}
	}
	for id := range s.materials {
		if _, ok := materials[id]; !ok {
			batch.DeleteMaterials = append(batch.DeleteMaterials, id)
		}
	}
	for id := range s.copies {
		if _, ok := copies[id]; !ok {
			batch.DeleteCopies = append(batch.DeleteCopies, id)
		}
	}
	if err := s.write(batch); err != nil {
		return err
	}
	s.settings = value
	s.materials = materials
	s.copies = copies
	return nil
}
