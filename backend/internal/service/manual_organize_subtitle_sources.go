package service

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
)

type subtitleSourceReader interface {
	ReadSubtitleSource(context.Context, string, p115.ShareFile, string) ([]byte, error)
}

type SubtitleSourceFile struct {
	p115.ShareFile
	ParentID string `json:"parentId"`
}

type SubtitleSourcesInput struct {
	AccountID string               `json:"accountId"`
	JobID     string               `json:"jobId"`
	Files     []SubtitleSourceFile `json:"files"`
}

type SubtitleSources struct {
	ID        string               `json:"id"`
	Files     []SubtitleSourceFile `json:"files"`
	ExpiresAt time.Time            `json:"expiresAt"`
}

type subtitleSourcePlan struct {
	SubtitleSources
	accountID string
	revision  uint64
	cookie    string
}

var subtitleSplitName = regexp.MustCompile(`(?i)(\.part\d+\.rar|\.[rz]\d{2,3}|\.(rar|7z|zip)\.\d{3,})$`)

func validateSubtitleSource(file p115.ShareFile) error {
	if subtitleSplitName.MatchString(file.Name) {
		return fmt.Errorf("%w：不支持分卷压缩包，请先解压后选择字幕", ErrOrganizeInput)
	}
	ext := strings.ToLower(path.Ext(file.Name))
	archive := ext == ".zip" || ext == ".rar" || ext == ".7z"
	if file.Directory || (!archive && !subtitleExtension(file.Name)) || file.Size <= 0 || file.Size > p115.MaxSubtitleSourceBytes || (!archive && file.Size > p115.MaxSubtitleBytes) {
		return fmt.Errorf("%w：请选择 ZIP / RAR / 7z 或字幕文件，压缩包总计 64 MiB，单字幕 16 MiB", ErrOrganizeInput)
	}
	return nil
}

// PrepareSubtitleSources verifies the complete selection using a single pinned
// primary credential. The short-lived snapshot only exists in memory.
func (s *ManualOrganize) PrepareSubtitleSources(ctx context.Context, input SubtitleSourcesInput) (SubtitleSources, error) {
	if len(input.Files) == 0 || len(input.Files) > 100 {
		return SubtitleSources{}, ErrOrganizeInput
	}
	a, err := s.account(input.AccountID)
	if err != nil {
		return SubtitleSources{}, err
	}
	if _, ok := s.drive.(subtitleSourceReader); !ok {
		return SubtitleSources{}, fmt.Errorf("当前网盘不支持读取字幕来源")
	}
	if _, err := s.subtitleVideos(ctx, a.ID, input.JobID, a.Cookie); err != nil {
		return SubtitleSources{}, err
	}
	files := make([]SubtitleSourceFile, 0, len(input.Files))
	seen := make(map[string]bool, len(input.Files))
	parents := make(map[string][]p115.ShareFile)
	var total int64
	for _, requested := range input.Files {
		if !organizeID(requested.ID) || !organizeID(requested.ParentID) || seen[requested.ID] {
			return SubtitleSources{}, ErrOrganizeInput
		}
		if err := validateSubtitleSource(requested.ShareFile); err != nil {
			return SubtitleSources{}, err
		}
		total += requested.Size
		if total > p115.MaxSubtitleSourceBytes {
			return SubtitleSources{}, fmt.Errorf("%w：所选来源文件总大小不能超过 64 MiB", ErrOrganizeInput)
		}
		seen[requested.ID] = true
		entries, ok := parents[requested.ParentID]
		if !ok {
			entries, err = s.list(ctx, requested.ParentID, a.Cookie)
			if err != nil {
				return SubtitleSources{}, err
			}
			parents[requested.ParentID] = entries
		}
		found := false
		for _, actual := range entries {
			if actual.ID == requested.ID && !actual.Directory && actual.Name == requested.Name && actual.Size == requested.Size {
				files = append(files, SubtitleSourceFile{ShareFile: actual, ParentID: requested.ParentID})
				found = true
				break
			}
		}
		if !found {
			return SubtitleSources{}, fmt.Errorf("%w：字幕来源已移动或发生变化，请刷新目录重新选择", ErrOrganizeConflict)
		}
	}
	id, err := organizeToken()
	if err != nil {
		return SubtitleSources{}, err
	}
	current, err := s.account(a.ID)
	if err != nil || current.Revision != a.Revision {
		return SubtitleSources{}, fmt.Errorf("%w：主账号已变化，请重新选择字幕来源", ErrOrganizeConflict)
	}
	result := SubtitleSources{ID: id, Files: files, ExpiresAt: time.Now().Add(10 * time.Minute)}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subtitleSources == nil {
		s.subtitleSources = make(map[string]subtitleSourcePlan)
	}
	for key, plan := range s.subtitleSources {
		if time.Now().After(plan.ExpiresAt) {
			delete(s.subtitleSources, key)
		}
	}
	if len(s.subtitleSources) >= 8 {
		return SubtitleSources{}, fmt.Errorf("%w：正在准备的字幕来源过多，请稍后重试", ErrOrganizeConflict)
	}
	s.subtitleSources[id] = subtitleSourcePlan{SubtitleSources: result, accountID: a.ID, revision: a.Revision, cookie: a.Cookie}
	return result, nil
}

// The reservation lasts through the HTTP response, so slow clients cannot keep
// an unbounded number of verified 64 MiB buffers alive after the CDN read ends.
type SubtitleSourceContent struct {
	Data    []byte
	release func()
}

func (c *SubtitleSourceContent) Close() { c.release() }

func (s *ManualOrganize) ReadSubtitleSource(ctx context.Context, accountID, sourceID, fileID string) (*SubtitleSourceContent, error) {
	a, err := s.account(accountID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	plan, ok := s.subtitleSources[sourceID]
	if !ok || plan.accountID != a.ID || plan.revision != a.Revision || time.Now().After(plan.ExpiresAt) {
		if ok && (time.Now().After(plan.ExpiresAt) || plan.accountID == a.ID && plan.revision != a.Revision) {
			delete(s.subtitleSources, sourceID)
		}
		s.mu.Unlock()
		return nil, fmt.Errorf("%w：字幕来源已过期或主账号已变化，请重新选择", ErrOrganizeConflict)
	}
	var file *SubtitleSourceFile
	for i := range plan.Files {
		if plan.Files[i].ID == fileID {
			file = &plan.Files[i]
			break
		}
	}
	if file == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w：来源文件未选中，请重新选择", ErrOrganizeConflict)
	}
	if s.subtitleSourceReads >= 2 {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w：其他字幕来源正在下载，请稍后重试", ErrOrganizeConflict)
	}
	s.subtitleSourceReads++
	s.mu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { s.mu.Lock(); s.subtitleSourceReads--; s.mu.Unlock() }) }
	success := false
	defer func() {
		if !success {
			release()
		}
	}()
	reader, ok := s.drive.(subtitleSourceReader)
	if !ok {
		return nil, fmt.Errorf("当前网盘不支持读取字幕来源")
	}
	data, err := reader.ReadSubtitleSource(ctx, file.ParentID, file.ShareFile, plan.cookie)
	if err != nil {
		return nil, &organizeSafeError{err: err, message: organizePublicError(err, plan.cookie)}
	}
	if int64(len(data)) != file.Size {
		return nil, fmt.Errorf("字幕来源下载大小不一致，请重新选择")
	}
	success = true
	return &SubtitleSourceContent{Data: data, release: release}, nil
}

func (s *ManualOrganize) ReleaseSubtitleSources(accountID, sourceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if plan, ok := s.subtitleSources[sourceID]; ok && plan.accountID == accountID {
		delete(s.subtitleSources, sourceID)
	}
}
