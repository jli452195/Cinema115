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

var ErrOrganizeInput = errors.New("手动整理参数无效")
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
	ID        string         `json:"id"`
	Files     []OrganizeFile `json:"files"`
	ExpiresAt time.Time      `json:"expiresAt"`
	Config    OrganizeConfig `json:"-"`
	Revision  uint64         `json:"-"`
}

type OrganizeScanInput struct {
	AccountID  string `json:"accountId"`
	ParentID   string `json:"parentId"`
	ParentPath string `json:"parentPath"`
	EntryID    string `json:"entryId"`
}

type OrganizeSelection struct {
	ID      string `json:"id"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
}

type OrganizePreviewInput struct {
	NamingRevision    *int64              `json:"namingRevision,omitempty"`
	NumberingRevision *int64              `json:"numberingRevision,omitempty"`
	ScanID            string              `json:"scanId"`
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
	Config         OrganizeConfig      `json:"config"`
	DownloadTarget CloudDownloadConfig `json:"downloadTarget"`
	Jobs           []OrganizeJob       `json:"jobs"`
	CleanupJobs    []OrganizeCleanup   `json:"cleanupJobs,omitempty"`
	SubtitleJobs   []SubtitleImport    `json:"subtitleJobs,omitempty"`
}

type organizePending struct {
	job    OrganizeJob
	cookie string
}

// Only the live worker keeps a credential snapshot. Neither plans nor history
// contain credentials. A restart interrupts work instead of rebinding accounts.
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
	pending     *organizePending
	wake        chan struct{}
	cleaning    bool
}

// Manual-only scans and import previews never belong to download organization.
type ManualOrganize struct {
	*OrganizeEngine
	scans               map[string]OrganizeScan
	cleanupPlans        map[string]OrganizeCleanup
	subtitlePlans       map[string]SubtitleImport
	subtitleSources     map[string]subtitleSourcePlan
	subtitleSourceReads int
}

// ReserveDirectoryWork shares the same reservation as organize/cleanup/upload.
// The caller must release it when its bounded directory operation finishes.
func (s *OrganizeEngine) ReserveDirectoryWork() (func(), error) {
	if err := s.reserveCleanup(); err != nil {
		return nil, err
	}
	return s.releaseCleanup, nil
}

func NewManualOrganize(engine *OrganizeEngine) *ManualOrganize {
	return &ManualOrganize{OrganizeEngine: engine, scans: make(map[string]OrganizeScan)}
}

func NewOrganizeEngine(store accountSecretStore, accounts *P115Accounts, drive OrganizeDrive, metadata OrganizeMetadata, templates NamingTemplates) (*OrganizeEngine, error) {
	s := &OrganizeEngine{store: store, accounts: accounts, drive: drive, metadata: metadata, templates: templates, plans: make(map[string]OrganizeJob), wake: make(chan struct{}, 1), doc: organizeDocument{Version: 1, Jobs: []OrganizeJob{}}}
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
	for i := range s.doc.CleanupJobs {
		if s.doc.CleanupJobs[i].Status == "running" {
			s.doc.CleanupJobs[i].Status = "interrupted"
			s.doc.CleanupJobs[i].Message = "服务重启，清理已中断；请检查网盘和回收站后重新预览"
		}
	}
	for i := range s.doc.SubtitleJobs {
		if s.doc.SubtitleJobs[i].Status == "running" {
			s.doc.SubtitleJobs[i].Status = "interrupted"
			s.doc.SubtitleJobs[i].Message = "服务重启，字幕导入已中断；请核实目标目录后重新预览"
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

func (s *ManualOrganize) Config() OrganizeConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.doc.Config
	if id := s.accounts.PrimaryID(); c.AccountID != id {
		c = OrganizeConfig{AccountID: id}
	}
	return c
}

func (s *ManualOrganize) SaveConfig(c OrganizeConfig) error {
	account, err := s.accounts.Primary(c.AccountID)
	if err != nil {
		return err
	}
	c.AccountID = account.ID
	if !organizeID(c.SourceCID) || !organizeID(c.TargetCID) || !organizePath(c.SourcePath) || !organizePath(c.TargetPath) {
		return fmt.Errorf("%w：请选择来源和目标目录", ErrOrganizeInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	doc.Config = c
	return s.saveLocked(doc)
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

func (s *OrganizeEngine) Browse(ctx context.Context, accountID, cid string, offset int) (*p115.SharePage, error) {
	if !organizeID(cid) || offset < 0 {
		return nil, ErrOrganizeInput
	}
	a, err := s.account(accountID)
	if err != nil {
		return nil, err
	}
	page, err := s.drive.DriveFiles(ctx, cid, offset, a.Cookie)
	if err != nil {
		return nil, &organizeSafeError{err: err, message: organizePublicError(err, a.Cookie)}
	}
	return page, nil
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
	files := make([]p115.ShareFile, 0)
	seen := make(map[string]bool)
	total := -1
	for offset := 0; ; {
		started := time.Now()
		page, err := s.drive.DriveFiles(ctx, cid, offset, cookie)
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

func (s *ManualOrganize) Scan(ctx context.Context, input OrganizeScanInput) (OrganizeScan, error) {
	c := s.Config()
	if input.AccountID != c.AccountID || !organizeID(input.ParentID) || !organizePath(input.ParentPath) || input.EntryID == "0" || !organizeID(input.EntryID) || !organizeID(c.TargetCID) || !organizePath(c.TargetPath) {
		return OrganizeScan{}, fmt.Errorf("%w：请选择有效来源，并保存目标目录设置", ErrOrganizeInput)
	}
	// Browsing a job's source or a subdirectory changes only this scan's
	// snapshot. The saved defaults and their target directory stay separate.
	c.SourceCID, c.SourcePath = input.ParentID, path.Clean(input.ParentPath)
	a, err := s.account(input.AccountID)
	if err != nil {
		return OrganizeScan{}, err
	}
	files, err := s.list(ctx, input.ParentID, a.Cookie)
	if err != nil {
		return OrganizeScan{}, err
	}
	var root *p115.ShareFile
	for i := range files {
		if files[i].ID == input.EntryID {
			root = &files[i]
			break
		}
	}
	if root == nil {
		return OrganizeScan{}, fmt.Errorf("%w：所选条目已移动或删除", ErrOrganizeConflict)
	}
	result := OrganizeScan{Config: c, Revision: a.Revision, Files: []OrganizeFile{}, ExpiresAt: time.Now().Add(30 * time.Minute)}
	visited := make(map[string]bool)
	var walk func(p115.ShareFile, string, string, int) error
	walk = func(f p115.ShareFile, parent, prefix string, depth int) error {
		if depth > 12 || (f.Directory && len(visited) >= 300) {
			return fmt.Errorf("%w：单次最多 1000 个媒体文件、300 个目录、12 层目录，请缩小选择范围", ErrOrganizeInput)
		}
		if f.Directory {
			if f.ID == c.TargetCID {
				return fmt.Errorf("%w：目标目录不能是所选文件夹或其子目录", ErrOrganizeInput)
			}
			if visited[f.ID] {
				return fmt.Errorf("网盘目录重复，请重新扫描")
			}
			visited[f.ID] = true
			children, err := s.list(ctx, f.ID, a.Cookie)
			if err != nil {
				return err
			}
			for _, child := range children {
				if err := walk(child, f.ID, path.Join(prefix, f.Name), depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		media := organizeMedia(f.Name)
		if media == "" {
			return nil
		}
		if len(result.Files) >= 1000 {
			return fmt.Errorf("%w：单次最多 1000 个媒体文件，请缩小选择范围", ErrOrganizeInput)
		}
		item := OrganizeFile{ShareFile: f, ParentID: parent, SourcePath: path.Join(prefix, f.Name), MediaType: media, Season: 1}
		organizeFileNumbering(&item)
		item.NamingValues = mediaFileValues(f.Name, DownloadFileSpec{})
		result.Files = append(result.Files, item)
		return nil
	}
	if err := walk(*root, input.ParentID, c.SourcePath, 0); err != nil {
		return OrganizeScan{}, err
	}
	if root.Directory {
		mediaDirectorySubtitleValues(result.Files)
	}
	// A selected single video may have companion subtitles in the same folder.
	if !root.Directory && organizeMedia(root.Name) == "video" {
		siblings := make([]OrganizeFile, 0, len(files))
		for _, file := range files {
			if !file.Directory {
				siblings = append(siblings, OrganizeFile{ShareFile: file, ParentID: input.ParentID, SourcePath: path.Join(c.SourcePath, file.Name), MediaType: organizeMedia(file.Name)})
			}
		}
		mediaDirectorySubtitleValues(siblings)
		subtitleMatcher := organizeSubtitleMatcher(siblings)
		for _, file := range siblings {
			if file.ID == root.ID {
				result.Files[0].NamingValues = file.NamingValues
				break
			}
		}
		for _, file := range files {
			if !file.Directory && organizeMedia(file.Name) == "subtitle" {
				owner, _ := organizeSubtitleOwner(OrganizeFile{ShareFile: file, ParentID: input.ParentID}, siblings, subtitleMatcher)
				if owner != root.ID {
					continue
				}
				if err := walk(file, input.ParentID, c.SourcePath, 0); err != nil {
					return OrganizeScan{}, err
				}
			}
		}
	}
	result.ID, err = organizeToken()
	if err != nil {
		return OrganizeScan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if len(s.scans) >= 8 {
		oldest := ""
		var at time.Time
		for id, scan := range s.scans {
			if oldest == "" || scan.ExpiresAt.Before(at) {
				oldest, at = id, scan.ExpiresAt
			}
		}
		delete(s.scans, oldest)
	}
	s.scans[result.ID] = result
	return result, nil
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

func (s *ManualOrganize) pruneLocked() {
	s.OrganizeEngine.pruneLocked()
	for id, scan := range s.scans {
		if time.Now().After(scan.ExpiresAt) {
			delete(s.scans, id)
		}
	}
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

func (s *ManualOrganize) History() []OrganizeJob { return s.history(false) }

func (s *OrganizeEngine) history(download bool) []OrganizeJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]OrganizeJob, 0, len(s.doc.Jobs))
	for _, job := range s.doc.Jobs {
		if (job.Download != nil) != download {
			continue
		}
		copy := cloneOrganizeJob(job)
		copy.Resumable = canContinueOrganize(job) || download && (canResumeDownload(job) || pendingDownloadShare(job))
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
		if d.Share != nil {
			share := *d.Share
			share.SourceDirs = append([]string(nil), share.SourceDirs...)
			d.Share = &share
		}
		job.Download = &d
	}
	return job
}

// Retain active work even when ordinary/manual jobs fill the recent history.
func prependOrganizeJob(job OrganizeJob, jobs []OrganizeJob) []OrganizeJob {
	out := make([]OrganizeJob, 0, len(jobs)+1)
	out = append(out, job)
	counts := map[bool]int{job.Download != nil: 1}
	for _, old := range jobs {
		download := old.Download != nil
		sealed := unhandedDownloadShare(old) && old.Download.Share.LeaderID != ""
		if counts[download] < 30 || old.Status == "queued" || old.Status == "submitting" || old.Status == "downloading" || old.Status == "running" || sealed {
			out = append(out, old)
			counts[download]++
		}
	}
	return out
}
