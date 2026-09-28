package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

const maxPlaybackMaterials = 100000

type playbackMaterialOrigin struct{}
type playbackSHA1Hint struct{}

// A hint from Emby-authorized STRM metadata may locate already verified file
// data, but cannot create a trusted origin association on its own.
func WithPlaybackSHA1Hint(ctx context.Context, hash string) context.Context {
	if !validMaterialHash(hash) {
		return ctx
	}
	return context.WithValue(ctx, playbackSHA1Hint{}, strings.ToUpper(hash))
}

// File data is independent of the account that owns a cloud copy. Origin keys
// are one-way hashes, never raw share passwords, URLs or credentials.
type PlaybackMaterial struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Size       int64           `json:"size"`
	SHA1       string          `json:"sha1"`
	Origins    []string        `json:"origins"`
	Ranges     []PlaybackRange `json:"ranges"`
	CreatedAt  time.Time       `json:"createdAt"`
	VerifiedAt time.Time       `json:"verifiedAt"`
}

// Only byte hashes accepted by a successful 115 upload are committed. Request
// signatures/sign_key and the bytes themselves are never saved.
type PlaybackRange struct {
	Start      int64     `json:"start"`
	End        int64     `json:"end"`
	SHA1       string    `json:"sha1"`
	VerifiedAt time.Time `json:"verifiedAt"`
}

func playbackMaterialID(hash string, size int64) string {
	return DirectLinkKey("material-v1", strings.ToUpper(hash), fmt.Sprint(size))
}

func validMaterialHash(hash string) bool {
	return len(hash) == 40 && strings.Trim(strings.ToUpper(hash), "0123456789ABCDEF") == ""
}

func validatePlaybackMaterials(materials []PlaybackMaterial) error {
	seen := make(map[string]bool, len(materials))
	for _, m := range materials {
		if !validMaterialHash(m.SHA1) || m.Size <= 0 || m.Name == "" || len([]rune(m.Name)) > 255 || strings.ContainsAny(m.Name, "/\\\r\n\x00") || m.ID != playbackMaterialID(m.SHA1, m.Size) || seen[m.ID] || len(m.Origins) > 64 || len(m.Ranges) > 64 {
			return fmt.Errorf("秒传文件资料损坏")
		}
		for _, origin := range m.Origins {
			if len(origin) != 64 || strings.Trim(origin, "0123456789abcdef") != "" {
				return fmt.Errorf("秒传来源索引损坏")
			}
		}
		seen[m.ID] = true
		for _, r := range m.Ranges {
			if !validMaterialHash(r.SHA1) || r.Start < 0 || r.End < r.Start || r.End >= m.Size || r.End-r.Start+1 > p115.MaxInstantRangeBytes || r.VerifiedAt.IsZero() {
				return fmt.Errorf("秒传校验资料损坏")
			}
		}
	}
	return nil
}

func clonePlaybackMaterials(materials []PlaybackMaterial) []PlaybackMaterial {
	result := make([]PlaybackMaterial, len(materials))
	for i, m := range materials {
		m.Origins = append([]string{}, m.Origins...)
		m.Ranges = append([]PlaybackRange{}, m.Ranges...)
		result[i] = m
	}
	return result
}

// Called under the playback operation gate. Copy before editing so failed
// persistence cannot change the in-memory document.
func (s *PlaybackCache) putMaterial(m PlaybackMaterial) error {
	if err := validatePlaybackMaterials([]PlaybackMaterial{m}); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if store, ok := s.store.(interface{ PutMaterial(PlaybackMaterial) error }); ok {
		i, exists := s.index.materials[m.ID]
		if !exists && len(s.doc.Materials) >= maxPlaybackMaterials {
			return fmt.Errorf("文件资料达到上限")
		}
		if err := store.PutMaterial(m); err != nil {
			return err
		}
		m = clonePlaybackMaterials([]PlaybackMaterial{m})[0]
		if exists {
			s.unindexMaterialLocked(s.doc.Materials[i])
			s.doc.Materials[i] = m
		} else {
			i = len(s.doc.Materials)
			s.doc.Materials = append(s.doc.Materials, m)
		}
		s.indexMaterialLocked(m, i)
		return nil
	}
	doc := s.doc
	doc.Materials = clonePlaybackMaterials(doc.Materials)
	for i, old := range doc.Materials {
		if old.ID == m.ID {
			doc.Materials[i] = clonePlaybackMaterials([]PlaybackMaterial{m})[0]
			return s.saveLocked(doc)
		}
	}
	if len(doc.Materials) >= maxPlaybackMaterials {
		return fmt.Errorf("秒传资料达到上限，请移除不再需要的资料后重试")
	}
	doc.Materials = append(doc.Materials, clonePlaybackMaterials([]PlaybackMaterial{m})[0])
	return s.saveLocked(doc)
}

func (s *PlaybackCache) ForgetMaterial(ctx context.Context, id string) error {
	if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		return fmt.Errorf("秒传资料编号无效")
	}
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	doc.Materials = make([]PlaybackMaterial, 0, len(s.doc.Materials))
	for _, m := range s.doc.Materials {
		if m.ID != id {
			doc.Materials = append(doc.Materials, m)
		}
	}
	return s.saveLocked(doc)
}

type playbackRangeClient interface {
	PlaybackPickCodeSource(context.Context, string, string, string) (*p115.DownloadInfo, error)
	PlaybackRangeSHA1(context.Context, *p115.DownloadInfo, int64, int64) (string, error)
}

// Called with the gate held. Resolving a known file requires no source I/O;
// only a missing range triggers reads of verified copies, then the primary.
func (s *PlaybackCache) preparePlaybackMaterial(ctx context.Context, selected *PlaybackCacheSelection, report *PlaybackResolution, loadSource func() (*p115.DownloadInfo, error)) (*p115.DownloadInfo, string, func() error, error) {
	origin, _ := ctx.Value(playbackMaterialOrigin{}).(string)
	hint, _ := ctx.Value(playbackSHA1Hint{}).(string)
	material, byHash := s.lookupMaterial(origin, hint)
	var original *p115.DownloadInfo
	if material.ID == "" {
		var err error
		original, err = loadSource()
		if err != nil {
			return nil, "", nil, fmt.Errorf("源文件解析失败：%s", p115.SharePlaybackFailureReason(err))
		}
		if err := p115.ValidateInstantSource(original); err != nil {
			return nil, "", nil, err
		}
		report.UsedSource = true
		material = PlaybackMaterial{ID: playbackMaterialID(original.SHA1, original.Size), Name: original.Name, Size: original.Size, SHA1: strings.ToUpper(original.SHA1), CreatedAt: time.Now().UTC()}
		if old, ok := s.materialByID(material.ID); ok {
			material = old
		}
		if origin != "" {
			found := false
			for _, old := range material.Origins {
				if old == origin {
					found = true
					break
				}
			}
			if !found {
				if len(material.Origins) == 64 {
					material.Origins = material.Origins[1:]
				}
				material.Origins = append(material.Origins, origin)
			}
		}
		material.VerifiedAt = time.Now().UTC()
		if err := s.putMaterial(material); err != nil {
			return nil, "", nil, err
		}
	} else {
		if byHash {
			s.logs.Add(ctx, "INFO", "外部链接 SHA1 命中已验证资料，跳过主账号元数据查询")
		} else {
			s.logs.Add(ctx, "INFO", "秒传文件资料命中，跳过源文件元数据查询")
		}
	}
	copies := s.entriesByHash(material.SHA1)
	info := &p115.DownloadInfo{Name: material.Name, Size: material.Size, SHA1: material.SHA1}
	pending := make([]PlaybackRange, 0, 1)
	info.ResolveRangeSHA1 = func(readCtx context.Context, start, end int64) (string, error) {
		if err := readCtx.Err(); err != nil {
			return "", err
		}
		if start < 0 || end < start || end >= material.Size || end-start+1 > p115.MaxInstantRangeBytes {
			return "", fmt.Errorf("秒传校验范围无效")
		}
		for _, r := range material.Ranges {
			if r.Start == start && r.End == end {
				s.logs.Add(readCtx, "INFO", "秒传校验片段资料命中，无需读取源文件")
				return r.SHA1, nil
			}
		}
		reader, ok := s.client.(playbackRangeClient)
		if !ok {
			return "", fmt.Errorf("播放客户端不支持校验片段读取")
		}
		hash, err := s.readPlaybackMaterialRange(readCtx, reader, selected, copies, material, start, end)
		if err != nil && readCtx.Err() == nil {
			if original == nil {
				original, err = loadSource()
			}
			if original != nil {
				report.UsedSource = true
				if original.Size != material.Size || !strings.EqualFold(original.SHA1, material.SHA1) {
					return "", fmt.Errorf("源文件身份与秒传资料不一致，请核查资料")
				}
				hash, err = reader.PlaybackRangeSHA1(readCtx, original, start, end)
			}
		}
		if err != nil {
			if readCtx.Err() != nil {
				return "", readCtx.Err()
			}
			return "", p115.ErrPlaybackRangeUnavailable
		}
		if !validMaterialHash(hash) {
			return "", fmt.Errorf("校验片段 SHA1 无效")
		}
		pending = append(pending, PlaybackRange{Start: start, End: end, SHA1: strings.ToUpper(hash)})
		return strings.ToUpper(hash), nil
	}
	commit := func() error {
		if len(pending) == 0 {
			return nil
		}
		for _, r := range pending {
			r.VerifiedAt = time.Now().UTC()
			if len(material.Ranges) == 64 {
				material.Ranges = material.Ranges[1:]
			}
			material.Ranges = append(material.Ranges, r)
		}
		return s.putMaterial(material)
	}
	return info, material.ID, commit, nil
}

// At most three recent, known copies are probed. Deleted/unconfigured accounts
// and a UID mismatch cannot become donors. Destination credentials never change.
func (s *PlaybackCache) readPlaybackMaterialRange(ctx context.Context, reader playbackRangeClient, selected *PlaybackCacheSelection, entries []PlaybackCacheEntry, m PlaybackMaterial, start, end int64) (string, error) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].LastUsed.After(entries[j].LastUsed) })
	attempts := 0
	for _, e := range entries {
		if e.Status != "ready" || e.AccountID == selected.binding.AccountID || e.File.ID == "" || e.File.PickCode == "" || e.File.Size != m.Size || !strings.EqualFold(e.File.SHA1, m.SHA1) {
			continue
		}
		for _, account := range selected.donors {
			if account.ID != e.AccountID || account.UID != e.UID {
				continue
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			attempts++
			readCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			info, err := reader.PlaybackPickCodeSource(readCtx, account.ID, e.File.PickCode, account.Cookie)
			var hash string
			if err == nil && info != nil && info.Size == m.Size && strings.EqualFold(info.SHA1, m.SHA1) {
				hash, err = reader.PlaybackRangeSHA1(readCtx, info, start, end)
			}
			cancel()
			if err == nil && validMaterialHash(hash) {
				s.logs.Add(ctx, "INFO", "秒传校验片段由已有小号副本提供，账号="+PlaybackAccountLabel(account.Name, account.UID, account.ID))
				return hash, nil
			}
			if attempts >= 3 {
				return "", fmt.Errorf("已知小号副本暂不可读")
			}
			break
		}
	}
	return "", fmt.Errorf("没有可用的小号文件副本")
}
