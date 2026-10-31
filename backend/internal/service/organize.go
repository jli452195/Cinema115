package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/naming"
)

var ErrOrganizeInput = errors.New("整理参数无效")
var ErrOrganizeConflict = errors.New("整理状态已变化")
var errOrganizePagination = errors.New("115 目录分页尚未稳定")

type OrganizeDrive interface {
	DriveFiles(context.Context, string, int, string) (*p115.SharePage, error)
	CreateDirectory(context.Context, string, string, string) (string, error)
	RenameEntries(context.Context, []p115.RenameTarget, string) error
	MoveEntriesBatch(context.Context, []string, string, string) error
}

type OrganizeMetadata interface {
	Details(context.Context, string, int) (*tmdb.Detail, error)
}

type OrganizeConfig struct {
	AccountID  string `json:"accountId"`
	SourceCID  string `json:"sourceCID"`
	SourcePath string `json:"sourcePath"`
	TargetCID  string `json:"targetCID"`
	TargetPath string `json:"targetPath"`
}

type OrganizeFile struct {
	p115.ShareFile
	TitleOnly    bool                    `json:"titleOnly,omitempty"` // Naming intent; never a cloud file identity.
	ParentID     string                  `json:"parentId"`
	SourcePath   string                  `json:"sourcePath"`
	MediaType    string                  `json:"mediaType"`
	Season       int                     `json:"season"`
	Episode      int                     `json:"episode"`
	EndEpisode   int                     `json:"endEpisode,omitempty"`
	NamingValues map[naming.Field]string `json:"namingValues,omitempty"`
}

type OrganizeScan struct {
	Files    []OrganizeFile `json:"files"`
	Config   OrganizeConfig `json:"-"`
	Revision uint64         `json:"-"`
}

type OrganizeSelection struct {
	ID      string `json:"id"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
}

type OrganizePreviewInput struct {
	NamingRevision    *int64              `json:"namingRevision,omitempty"`
	NumberingRevision *int64              `json:"numberingRevision,omitempty"`
	Kind              string              `json:"kind"`
	TMDBID            int                 `json:"tmdbId"`
	Subtitles         bool                `json:"subtitles"`
	Files             []OrganizeSelection `json:"files"`
}

type OrganizeItem struct {
	File       OrganizeFile `json:"file"`
	Directory  string       `json:"directory"`
	Name       string       `json:"name"`
	Stem       string       `json:"stem,omitempty"`
	TargetPath string       `json:"targetPath"`
	TargetCID  string       `json:"targetCID,omitempty"`
	Conflict   string       `json:"conflict,omitempty"`
	Status     string       `json:"status"`
	Message    string       `json:"message,omitempty"`
}

type OrganizeJob struct {
	Naming    *naming.Snapshot    `json:"naming,omitempty"`
	Resumable bool                `json:"resumable,omitempty"`
	Download  *ResourceDownload   `json:"download,omitempty"`
	Numbering *tmdb.NumberingInfo `json:"numbering,omitempty"`
	ID        string              `json:"id"`
	Config    OrganizeConfig      `json:"config"`
	Revision  uint64              `json:"revision"`
	Kind      string              `json:"kind"`
	TMDBID    int                 `json:"tmdbId"`
	Title     string              `json:"title"`
	CreatedAt time.Time           `json:"createdAt"`
	UpdatedAt time.Time           `json:"updatedAt"`
	Status    string              `json:"status"`
	Message   string              `json:"message,omitempty"`
	Items     []OrganizeItem      `json:"items"`
	Stats     OrganizeStats       `json:"stats"`
}

type organizeDocument struct {
	Version        int                 `json:"version"`
	DownloadTarget CloudDownloadConfig `json:"downloadTarget"`
	Jobs           []OrganizeJob       `json:"jobs"`
}

// Shared planning and execution state. Credentials are supplied by each live
// download worker and never persisted in plans or history.
type OrganizeEngine struct {
	// Set at startup; called after successful persistence and releasing the writer.
	OnCompleted func(context.Context, OrganizeConfig)
	mu          sync.Mutex
	store       accountSecretStore
	doc         organizeDocument
	accounts    *P115Accounts
	drive       OrganizeDrive
	metadata    OrganizeMetadata
	templates   NamingTemplates
	plans       map[string]OrganizeJob
	cleaning    bool
}

// ReserveDirectoryWork serializes the shared directory mutation workflows.
// The caller must release it when its bounded directory operation finishes.
func (s *OrganizeEngine) ReserveDirectoryWork() (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleaning {
		return nil, fmt.Errorf("%w：已有目录操作执行中，请等待完成", ErrOrganizeConflict)
	}
	s.cleaning = true
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cleaning = false
	}, nil
}

func NewOrganizeEngine(store accountSecretStore, accounts *P115Accounts, drive OrganizeDrive, metadata OrganizeMetadata, templates NamingTemplates) (*OrganizeEngine, error) {
	s := &OrganizeEngine{store: store, accounts: accounts, drive: drive, metadata: metadata, templates: templates, plans: make(map[string]OrganizeJob), doc: organizeDocument{Version: 1, Jobs: []OrganizeJob{}}}
	if store.Value() != "" {
		if err := json.Unmarshal([]byte(store.Value()), &s.doc); err != nil || s.doc.Version != 1 {
			return nil, fmt.Errorf("整理任务存储无法解析")
		}
	}
	for i := range s.doc.Jobs {
		job := &s.doc.Jobs[i]
		if job.Status == "running" || job.Status == "queued" || job.Status == "downloading" || job.Status == "submitting" {
			if waitingDownloadShare(*job) {
				continue // Bound files awaiting collection have no result-directory writes.
			}
			job.Status, job.Message = "interrupted", "服务重启，任务已中断；请检查任务详情"
			if canContinueOrganize(*job) || canResumeDownload(*job) {
				job.Message = "服务重启，任务已中断；可在原任务详情中检查并继续"
			}
		}
	}
	if err := s.saveLocked(s.doc); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *OrganizeEngine) saveLocked(doc organizeDocument) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("编码整理记录: %w", err)
	}
	if err := s.store.Save(string(raw)); err != nil {
		return fmt.Errorf("保存整理记录: %w", err)
	}
	s.doc = doc
	return nil
}

func organizeID(id string) bool {
	n, err := strconv.ParseUint(id, 10, 64)
	return err == nil && strconv.FormatUint(n, 10) == id
}
func organizePath(p string) bool {
	return strings.HasPrefix(p, "/") && len([]rune(p)) <= 2048 && !strings.ContainsAny(p, "\x00\r\n")
}
func organizeToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("创建整理编号: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *OrganizeEngine) account(id string) (P115Account, error) {
	if id == "" {
		return P115Account{}, fmt.Errorf("%w：请先配置整理使用的主账号", ErrOrganizeInput)
	}
	a, err := s.accounts.Primary(id)
	if err != nil {
		return a, err
	}
	if a.Cookie == "" {
		return a, fmt.Errorf("%w：所选账号未登录，请配置凭据", ErrOrganizeInput)
	}
	return a, nil
}

// Exhaust pagination before checking names; an incomplete directory must never
// be treated as evidence that a destination is free.
func (s *OrganizeEngine) list(ctx context.Context, cid, cookie string) ([]p115.ShareFile, error) {
	return s.listUntil(ctx, cid, cookie, nil)
}

// A successful write can precede visibility of its results in directory reads.
// After bounded read retries, return the last valid snapshot so callers retain
// per-file confirmed/uncertain outcomes rather than replaying the write.
func (s *OrganizeEngine) listUntil(ctx context.Context, cid, cookie string, visible func([]p115.ShareFile) bool) ([]p115.ShareFile, error) {
	for attempt := 0; ; attempt++ {
		files, err := s.listOnce(ctx, cid, cookie)
		if attempt >= 2 || err != nil && !errors.Is(err, errOrganizePagination) || err == nil && (visible == nil || visible(files)) {
			return files, err
		}
		// Discard the entire inconsistent snapshot. Only reads are repeated;
		// rename/move requests are never replayed by directory reconciliation.
		timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *OrganizeEngine) listOnce(ctx context.Context, cid, cookie string) ([]p115.ShareFile, error) {
	return listOrganizeFiles(ctx, s.drive, cid, cookie)
}

func listOrganizeFiles(ctx context.Context, drive OrganizeDrive, cid, cookie string) ([]p115.ShareFile, error) {
	files := make([]p115.ShareFile, 0)
	seen := make(map[string]bool)
	total := -1
	for offset := 0; ; {
		started := time.Now()
		page, err := drive.DriveFiles(ctx, cid, offset, cookie)
		if stats := organizeStatsFrom(ctx); stats != nil {
			stats.DirectoryRequests++
			stats.DirectoryMS += time.Since(started).Milliseconds()
		}
		if err != nil {
			return nil, fmt.Errorf("读取网盘目录: %w", &organizeSafeError{err: err, message: organizePublicError(err, cookie)})
		}
		if page == nil {
			return nil, fmt.Errorf("%w：目录 %s，偏移 %d，返回空分页", errOrganizePagination, cid, offset)
		}
		if page.Offset != offset || page.Count < offset+len(page.Files) || (total >= 0 && page.Count != total) {
			return nil, fmt.Errorf("%w：目录 %s，请求偏移 %d，返回偏移 %d，本页 %d 项，总数 %d，前页总数 %d", errOrganizePagination, cid, offset, page.Offset, len(page.Files), page.Count, total)
		}
		total = page.Count
		for _, file := range page.Files {
			if seen[file.ID] {
				return nil, fmt.Errorf("%w：目录 %s，偏移 %d，条目 %s 重复", errOrganizePagination, cid, offset, file.ID)
			}
			seen[file.ID] = true
			files = append(files, file)
		}
		offset += len(page.Files)
		if offset >= page.Count {
			return files, nil
		}
		if len(page.Files) == 0 {
			return nil, fmt.Errorf("%w：目录 %s，偏移 %d，总数 %d，但本页为空", errOrganizePagination, cid, offset, page.Count)
		}
		if offset >= 20000 {
			return nil, fmt.Errorf("目录过大或分页不完整，请选择较小目录")
		}
	}
}

var organizeVideoExtensions = extensionSet("", DefaultVideoExtensions)

func organizeMedia(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if organizeVideoExtensions[ext] {
		return "video"
	}
	switch ext {
	case ".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx":
		return "subtitle"
	}
	return ""
}

type OrganizeSubtitle struct {
	File    OrganizeFile `json:"file"`
	VideoID string       `json:"videoId,omitempty"`
	Tail    string       `json:"tail,omitempty"`
}

// Keep associations based on the complete list, independent of video selection.
// Tail is already filename-safe so both preview clients and execution append it verbatim.
func organizeSubtitles(files []OrganizeFile) []OrganizeSubtitle {
	subtitles := make([]OrganizeSubtitle, 0)
	matcher := organizeSubtitleMatcher(files)
	for _, file := range files {
		if file.MediaType != "subtitle" {
			continue
		}
		owner, tail := organizeSubtitleOwner(file, files, matcher)
		if tail = organizeName(tail); tail != "" {
			tail = "." + tail
		}
		subtitles = append(subtitles, OrganizeSubtitle{File: file, VideoID: owner, Tail: tail})
	}
	return subtitles
}

func organizeSubtitleMatcher(files []OrganizeFile) *mediafacts.SubtitleMatcher {
	videos := make([]mediafacts.SubtitleVideo, 0, len(files))
	for _, file := range files {
		if file.MediaType == "video" {
			videos = append(videos, mediafacts.SubtitleVideo{ID: file.ID, ParentID: file.ParentID, Name: file.Name})
		}
	}
	return mediafacts.NewSubtitleMatcher(videos)
}

func organizeSubtitleOwner(sub OrganizeFile, files []OrganizeFile, matcher *mediafacts.SubtitleMatcher) (string, string) {
	bestID, tail, longest := "", "", -1
	stem := []rune(strings.TrimSuffix(sub.Name, path.Ext(sub.Name)))
	for _, video := range files {
		if video.MediaType != "video" || video.ParentID != sub.ParentID {
			continue
		}
		vstem := []rune(strings.TrimSuffix(video.Name, path.Ext(video.Name)))
		n := len(vstem)
		if n > len(stem) || !strings.EqualFold(string(stem[:n]), string(vstem)) || (n < len(stem) && stem[n] != '.') {
			continue
		}
		if n > longest {
			bestID, tail, longest = video.ID, string(stem[n:]), n
		} else if n == longest {
			bestID = ""
		}
	}
	if longest < 0 {
		owners, tail := matcher.Candidates(sub.ParentID, sub.Name)
		if len(owners) == 1 {
			return owners[0], tail
		}
	}
	return bestID, tail
}

func (s *OrganizeEngine) pruneLocked() {
	for id, plan := range s.plans {
		if time.Since(plan.CreatedAt) > 30*time.Minute {
			delete(s.plans, id)
		}
	}
}

func organizeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	return strings.Trim(name, " .")
}

func (s *OrganizeEngine) history() []OrganizeJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]OrganizeJob, 0, len(s.doc.Jobs))
	for _, job := range s.doc.Jobs {
		if job.Download == nil {
			continue
		}
		copy := cloneOrganizeJob(job)
		copy.Resumable = canContinueOrganize(job) || canResumeDownload(job) || pendingDownloadShare(job) || pendingDownloadSubtitles(job) || pendingDownloadCleanup(job)
		jobs = append(jobs, copy)
	}
	return jobs
}

func cloneOrganizeJob(job OrganizeJob) OrganizeJob {
	job.Items = append([]OrganizeItem{}, job.Items...)
	if job.Naming != nil {
		snapshot := *job.Naming
		snapshot.Template = snapshot.Template.Clone()
		job.Naming = &snapshot
	}
	if job.Download != nil {
		d := *job.Download
		d.CleanupDirs = append([]string(nil), d.CleanupDirs...)
		d.Subtitles = append([]DownloadSubtitle(nil), d.Subtitles...)
		if d.Share != nil {
			share := *d.Share
			d.Share = &share
		}
		job.Download = &d
	}
	return job
}

// Retain active work even when completed jobs fill the recent history.
func prependOrganizeJob(job OrganizeJob, jobs []OrganizeJob) []OrganizeJob {
	out := make([]OrganizeJob, 0, len(jobs)+1)
	out = append(out, job)
	for _, old := range jobs {
		sealed := unhandedDownloadShare(old) && old.Download.Share.LeaderID != ""
		if len(out) < 30 || old.Status == "queued" || old.Status == "submitting" || old.Status == "downloading" || old.Status == "running" || sealed || pendingDownloadCleanup(old) {
			out = append(out, old)
		}
	}
	return out
}
