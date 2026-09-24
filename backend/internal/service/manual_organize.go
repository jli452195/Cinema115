package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
)

var ErrOrganizeInput = errors.New("手动整理参数无效")
var ErrOrganizeConflict = errors.New("整理状态已变化")

type OrganizeDrive interface {
	DriveFiles(context.Context, string, int, string) (*p115.SharePage, error)
	CreateDirectory(context.Context, string, string, string) (string, error)
	RenameEntries(context.Context, []p115.RenameTarget, string) error
	MoveEntriesBatch(context.Context, []string, string, string) error
}

type OrganizeMetadata interface {
	Details(context.Context, string, int) (*tmdb.Detail, error)
	Season(context.Context, int, int) (*tmdb.SeasonDetail, error)
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
	ParentID   string `json:"parentId"`
	SourcePath string `json:"sourcePath"`
	MediaType  string `json:"mediaType"`
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	Suffix     string `json:"suffix"`
}

type OrganizeScan struct {
	ID        string         `json:"id"`
	Files     []OrganizeFile `json:"files"`
	ExpiresAt time.Time      `json:"expiresAt"`
	Config    OrganizeConfig `json:"-"`
	Revision  uint64         `json:"-"`
}

type OrganizeSelection struct {
	ID         string `json:"id"`
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	EndEpisode int    `json:"endEpisode"`
	Suffix     string `json:"suffix"`
}

type OrganizePreviewInput struct {
	ScanID    string              `json:"scanId"`
	Kind      string              `json:"kind"`
	TMDBID    int                 `json:"tmdbId"`
	Subtitles bool                `json:"subtitles"`
	Files     []OrganizeSelection `json:"files"`
}

type OrganizeItem struct {
	File       OrganizeFile `json:"file"`
	Directory  string       `json:"directory"`
	Name       string       `json:"name"`
	TargetPath string       `json:"targetPath"`
	TargetCID  string       `json:"targetCID,omitempty"`
	Conflict   string       `json:"conflict,omitempty"`
	Status     string       `json:"status"`
	Message    string       `json:"message,omitempty"`
}

type OrganizeJob struct {
	ID        string         `json:"id"`
	Config    OrganizeConfig `json:"config"`
	Revision  uint64         `json:"revision"`
	Kind      string         `json:"kind"`
	TMDBID    int            `json:"tmdbId"`
	Title     string         `json:"title"`
	Folder    string         `json:"folder"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	Status    string         `json:"status"`
	Message   string         `json:"message,omitempty"`
	Items     []OrganizeItem `json:"items"`
	Stats     OrganizeStats  `json:"stats"`
}

type organizeDocument struct {
	Version     int               `json:"version"`
	Config      OrganizeConfig    `json:"config"`
	Jobs        []OrganizeJob     `json:"jobs"`
	CleanupJobs []OrganizeCleanup `json:"cleanupJobs,omitempty"`
}

type organizePending struct {
	job    OrganizeJob
	cookie string
}

// Only the live worker keeps a credential snapshot. Neither plans nor history
// contain credentials. A restart interrupts work instead of rebinding accounts.
type ManualOrganize struct {
	mu           sync.Mutex
	store        accountSecretStore
	doc          organizeDocument
	accounts     *P115Accounts
	drive        OrganizeDrive
	metadata     OrganizeMetadata
	scans        map[string]OrganizeScan
	plans        map[string]OrganizeJob
	pending      *organizePending
	wake         chan struct{}
	cleanupPlans map[string]OrganizeCleanup
	cleaning     bool
}

func NewManualOrganize(store accountSecretStore, accounts *P115Accounts, drive OrganizeDrive, metadata OrganizeMetadata) (*ManualOrganize, error) {
	s := &ManualOrganize{store: store, accounts: accounts, drive: drive, metadata: metadata, scans: make(map[string]OrganizeScan), plans: make(map[string]OrganizeJob), wake: make(chan struct{}, 1), doc: organizeDocument{Version: 1, Jobs: []OrganizeJob{}}}
	if store.Value() != "" {
		if err := json.Unmarshal([]byte(store.Value()), &s.doc); err != nil || s.doc.Version != 1 {
			return nil, fmt.Errorf("手动整理配置无法解析")
		}
	}
	for i := range s.doc.Jobs {
		job := &s.doc.Jobs[i]
		if job.Status == "running" || job.Status == "queued" {
			job.Status, job.Message = "interrupted", "服务重启，任务已中断；请检查文件当前位置后重新扫描预览"
		}
	}
	for i := range s.doc.CleanupJobs {
		if s.doc.CleanupJobs[i].Status == "running" {
			s.doc.CleanupJobs[i].Status = "interrupted"
			s.doc.CleanupJobs[i].Message = "服务重启，清理已中断；请检查网盘和回收站后重新预览"
		}
	}
	if err := s.saveLocked(s.doc); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ManualOrganize) saveLocked(doc organizeDocument) error {
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
	return s.doc.Config
}

func (s *ManualOrganize) SaveConfig(c OrganizeConfig) error {
	if err := s.accounts.ValidateSelection(c.AccountID); err != nil {
		return err
	}
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

func (s *ManualOrganize) account(id string) (P115Account, error) {
	if id == "" {
		return P115Account{}, fmt.Errorf("%w：请先选择手动整理使用的 115 账号", ErrOrganizeInput)
	}
	a, err := s.accounts.Get(id)
	if err != nil {
		return a, err
	}
	if a.Cookie == "" {
		return a, fmt.Errorf("%w：所选账号未登录，请配置凭据", ErrOrganizeInput)
	}
	return a, nil
}

func (s *ManualOrganize) Browse(ctx context.Context, accountID, cid string, offset int) (*p115.SharePage, error) {
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
func (s *ManualOrganize) list(ctx context.Context, cid, cookie string) ([]p115.ShareFile, error) {
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
		if page == nil || page.Offset != offset || page.Count < offset+len(page.Files) || (total >= 0 && page.Count != total) {
			return nil, fmt.Errorf("115 目录分页发生变化，请刷新重试")
		}
		total = page.Count
		for _, file := range page.Files {
			if seen[file.ID] {
				return nil, fmt.Errorf("115 目录分页重复，请刷新重试")
			}
			seen[file.ID] = true
			files = append(files, file)
		}
		offset += len(page.Files)
		if offset >= page.Count {
			return files, nil
		}
		if len(page.Files) == 0 || offset >= 20000 {
			return nil, fmt.Errorf("目录过大或分页不完整，请选择较小目录")
		}
	}
}

var organizeEpisode = regexp.MustCompile(`(?i)\bS(\d{1,3})E(\d{1,4})(?:E|[- ]E?\d)`)
var organizeSingleEpisode = regexp.MustCompile(`(?i)S(\d{1,3})E(\d{1,4})(?:\D|$)`)
var organizeQuality = regexp.MustCompile(`(?i)(?:2160p|1080p|720p|480p|4k|remux|bluray|blu-ray|web-dl|webrip|hdtv|x265|x264|h[.]?265|h[.]?264|hevc|hdr10\+?|hdr|dovi)`)

func organizeMedia(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".mkv", ".mp4", ".avi", ".mov", ".m4v", ".ts", ".m2ts", ".wmv", ".iso":
		return "video"
	case ".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx":
		return "subtitle"
	}
	return ""
}

func (s *ManualOrganize) Scan(ctx context.Context, accountID, parentID, entryID string) (OrganizeScan, error) {
	c := s.Config()
	if accountID != c.AccountID || parentID != c.SourceCID || entryID == "0" || !organizeID(entryID) {
		return OrganizeScan{}, fmt.Errorf("%w：请保存来源目录后选择文件或文件夹", ErrOrganizeInput)
	}
	a, err := s.account(accountID)
	if err != nil {
		return OrganizeScan{}, err
	}
	files, err := s.list(ctx, parentID, a.Cookie)
	if err != nil {
		return OrganizeScan{}, err
	}
	var root *p115.ShareFile
	for i := range files {
		if files[i].ID == entryID {
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
		if match := organizeSingleEpisode.FindStringSubmatch(f.Name); match != nil && !organizeEpisode.MatchString(f.Name) {
			item.Season, _ = strconv.Atoi(match[1])
			item.Episode, _ = strconv.Atoi(match[2])
		}
		item.Suffix = strings.Join(organizeQuality.FindAllString(f.Name, -1), " ")
		result.Files = append(result.Files, item)
		return nil
	}
	if err := walk(*root, parentID, c.SourcePath, 0); err != nil {
		return OrganizeScan{}, err
	}
	// A selected single video may have companion subtitles in the same folder.
	if !root.Directory && organizeMedia(root.Name) == "video" {
		siblings := make([]OrganizeFile, 0, len(files))
		for _, file := range files {
			if !file.Directory {
				siblings = append(siblings, OrganizeFile{ShareFile: file, ParentID: parentID, MediaType: organizeMedia(file.Name)})
			}
		}
		for _, file := range files {
			if !file.Directory && organizeMedia(file.Name) == "subtitle" {
				owner, _ := organizeSubtitleOwner(OrganizeFile{ShareFile: file, ParentID: parentID}, siblings)
				if owner != root.ID {
					continue
				}
				if err := walk(file, parentID, c.SourcePath, 0); err != nil {
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

func organizeSubtitleOwner(sub OrganizeFile, files []OrganizeFile) (string, string) {
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
	return bestID, tail
}

func (s *ManualOrganize) pruneLocked() {
	for id, scan := range s.scans {
		if time.Now().After(scan.ExpiresAt) {
			delete(s.scans, id)
		}
	}
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

func (s *ManualOrganize) History() []OrganizeJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]OrganizeJob, len(s.doc.Jobs))
	for i, job := range s.doc.Jobs {
		jobs[i] = job
		jobs[i].Items = append([]OrganizeItem{}, job.Items...)
	}
	return jobs
}
