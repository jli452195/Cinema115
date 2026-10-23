package service

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/model"
)

// GardenDownload is a one-off naming preset, independent of RSS subscriptions.
// Credentials only live in gardenPending and never enter plans/history.
type GardenDownload struct {
	Hash        string `json:"hash"`
	ParentCID   string `json:"parentCID"`
	ParentPath  string `json:"parentPath"`
	DirectoryID string `json:"directoryId,omitempty"`
}

type GardenOrganizeInput struct {
	GardenResourceInput
	Kind       string              `json:"kind"`
	TMDBID     int                 `json:"tmdbId"`
	AccountID  string              `json:"accountId"`
	TargetCID  string              `json:"targetCID"`
	TargetPath string              `json:"targetPath"`
	Files      []OrganizeSelection `json:"files"`
}

type gardenStatusReader interface {
	ReadAnimeTaskStatus(context.Context, string, string) (model.AnimeOfflineStatus, error)
}

type gardenPending struct {
	job                       OrganizeJob
	account                   P115Account
	next                      time.Time
	attempts, bindingAttempts int
	ready                     bool
	resultID                  string
}

type GardenOrganize struct {
	Organize  *ManualOrganize
	Garden    *GardenService
	Downloads *CloudDownloadService
	Settings  *CloudDownloadSettings
	Reader    gardenStatusReader
	mu        sync.Mutex
	live      map[string]*gardenPending
	wake      chan struct{}
}

func NewGardenOrganize(o *ManualOrganize, g *GardenService, downloads *CloudDownloadService, settings *CloudDownloadSettings, reader gardenStatusReader) *GardenOrganize {
	return &GardenOrganize{Organize: o, Garden: g, Downloads: downloads, Settings: settings, Reader: reader, live: make(map[string]*gardenPending), wake: make(chan struct{}, 1)}
}

func (s *GardenOrganize) Preview(ctx context.Context, in GardenOrganizeInput) (OrganizeJob, error) {
	download := s.Settings.View()
	if in.AccountID != download.AccountID || !organizeID(in.TargetCID) || !organizePath(in.TargetPath) {
		return OrganizeJob{}, fmt.Errorf("%w：请选择当前主账号的整理目录", ErrOrganizeInput)
	}
	a, err := s.Downloads.snapshot(download)
	if err != nil {
		return OrganizeJob{}, err
	}
	files, err := s.Garden.Files(ctx, in.GardenResourceInput)
	if err != nil {
		return OrganizeJob{}, err
	}
	if len(files.Files) == 0 {
		return OrganizeJob{}, fmt.Errorf("%w：%s", ErrOrganizeInput, files.Reason)
	}
	c := OrganizeConfig{AccountID: a.ID, SourceCID: download.TargetCID, SourcePath: download.TargetPath, TargetCID: in.TargetCID, TargetPath: in.TargetPath}
	job, err := s.Organize.buildPlan(ctx, OrganizeScan{Config: c, Revision: a.Revision, Files: files.Files}, OrganizePreviewInput{Kind: in.Kind, TMDBID: in.TMDBID, Files: in.Files})
	if err != nil {
		return OrganizeJob{}, err
	}
	if err = s.Organize.checkPlan(ctx, &job, a.Cookie, false); err != nil {
		return OrganizeJob{}, err
	}
	// Website IDs are paths, not cloud file IDs. Actual IDs are bound only after download.
	job.Download = &GardenDownload{Hash: files.Hash, ParentCID: download.TargetCID, ParentPath: download.TargetPath}
	return s.Organize.rememberPlan(job)
}

// Start persists before waking the server-owned worker. The preview ID is the
// idempotency key, so a lost HTTP response cannot submit the magnet twice.
func (s *GardenOrganize) Start(id string) (OrganizeJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.Organize
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, job := range o.doc.Jobs {
		if job.ID == id && job.Download != nil {
			return cloneOrganizeJob(job), nil
		}
	}
	o.pruneLocked()
	job, ok := o.plans[id]
	if !ok || job.Download == nil {
		return OrganizeJob{}, fmt.Errorf("%w：预览已过期，请重新确认", ErrOrganizeConflict)
	}
	a, err := o.account(job.Config.AccountID)
	if err != nil {
		return OrganizeJob{}, err
	}
	if a.Revision != job.Revision {
		return OrganizeJob{}, fmt.Errorf("%w：账号凭据已变化，请重新预览", ErrOrganizeConflict)
	}
	if len(s.live) >= 4 {
		return OrganizeJob{}, fmt.Errorf("%w：已有 4 个下载整理任务，请等待完成", ErrOrganizeConflict)
	}
	for _, p := range s.live {
		if p.job.Download.Hash == job.Download.Hash {
			return OrganizeJob{}, ErrCloudTaskBusy
		}
	}
	for _, item := range job.Items {
		if item.Conflict != "" {
			return OrganizeJob{}, fmt.Errorf("%w：请先处理命名冲突", ErrOrganizeInput)
		}
	}
	job = cloneOrganizeJob(job)
	job.Status, job.UpdatedAt = "queued", time.Now()
	job.Message = "等待提交 115，整条磁力下载后仅整理所选文件"
	doc := o.doc
	doc.Jobs = prependOrganizeJob(job, doc.Jobs)
	if err = o.saveLocked(doc); err != nil {
		return OrganizeJob{}, err
	}
	s.live[id] = &gardenPending{job: cloneOrganizeJob(job), account: a}
	delete(o.plans, id)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return job, nil
}

func (s *GardenOrganize) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-s.wake:
		}
		s.mu.Lock()
		pending := make([]*gardenPending, 0, len(s.live))
		for _, p := range s.live {
			if !time.Now().Before(p.next) {
				pending = append(pending, p)
			}
		}
		s.mu.Unlock()
		for _, p := range pending {
			stepCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
			done, err := s.advance(stepCtx, p)
			cancel()
			if err != nil {
				return err
			} // Persistence failure stops writes.
			if done {
				s.mu.Lock()
				delete(s.live, p.job.ID)
				s.mu.Unlock()
			}
		}
	}
}

func (s *GardenOrganize) stop(p *gardenPending, status, message string) (bool, error) {
	p.job.Status, p.job.Message = status, message
	return true, s.Organize.checkpoint(&p.job)
}

func (s *GardenOrganize) advance(ctx context.Context, p *gardenPending) (bool, error) {
	o, job, cookie := s.Organize, &p.job, p.account.Cookie
	if job.Stats.StartedAt.IsZero() {
		job.Stats.StartedAt = time.Now()
	}
	ctx = withOrganizeStats(ctx, job)
	if time.Since(job.CreatedAt) > 24*time.Hour {
		return s.stop(p, "interrupted", "等待离线超过 24 小时，已停止自动整理；文件保留在本次下载目录")
	}
	if job.Status == "queued" {
		job.Status, job.Message = "submitting", "正在创建独立下载目录并提交 115"
		if err := o.checkpoint(job); err != nil {
			return false, err
		}
		started := time.Now()
		dir, err := o.drive.CreateDirectory(ctx, job.Download.ParentCID, "garden-"+job.ID, cookie)
		job.Stats.DirectoryCreates++
		job.Stats.MutationMS += time.Since(started).Milliseconds()
		if err != nil {
			return s.stop(p, "uncertain", "下载目录创建结果未确认，请核对云盘；未重复创建或提交")
		}
		if !organizeID(dir) || dir == "0" || dir == job.Download.ParentCID || dir == job.Config.TargetCID {
			return s.stop(p, "blocked", "115 返回的下载目录无效，未提交离线")
		}
		job.Download.DirectoryID = dir
		job.Config.SourceCID, job.Config.SourcePath = dir, path.Join(job.Download.ParentPath, "garden-"+job.ID)
		if err = o.checkpoint(job); err != nil {
			return false, err
		}
		children, err := o.list(ctx, job.Download.ParentCID, cookie)
		if err != nil {
			return s.stop(p, "blocked", "无法确认本次下载目录，未提交离线；请核对已创建的目录")
		}
		confirmed, err := organizeFindDirectory(children, "garden-"+job.ID)
		if err != nil || confirmed != dir {
			return s.stop(p, "blocked", "本次下载目录身份不一致，未提交离线")
		}
		_, err = s.Downloads.submitSnapshot(ctx, CloudDownloadConfig{AccountID: p.account.ID, TargetCID: dir, TargetPath: job.Config.SourcePath}, p.account, "magnet:?xt=urn:btih:"+job.Download.Hash)
		if err != nil {
			return s.stop(p, "uncertain", organizePublicError(fmt.Errorf("%s", CloudDownloadErrorMessage(err)), cookie)+"；下载目录已保留，请核对 115 任务")
		}
		job.Status, job.Message = "downloading", "115 离线中，完成后自动整理所选文件"
		if err = o.checkpoint(job); err != nil {
			return false, err
		}
		// Check once immediately: instant downloads need no fixed waiting period.
	}
	if !p.ready {
		observation, err := s.Reader.ReadAnimeTaskStatus(ctx, cookie, job.Download.Hash)
		p.attempts++
		delays := []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}
		p.next = time.Now().Add(delays[min(p.attempts-1, len(delays)-1)])
		if err != nil || !observation.Present {
			message := "暂未读到可确认的 115 任务状态，稍后再检查；不会重复提交离线"
			if job.Message != message {
				job.Message = message
				return false, o.checkpoint(job)
			}
			return false, nil
		} // No resubmission on read errors/absence.
		if !observation.Valid() || observation.InfoHash != job.Download.Hash || observation.TargetCID != "" && observation.TargetCID != job.Download.DirectoryID {
			return s.stop(p, "blocked", "115 任务身份或下载目录不一致，已停止整理")
		}
		if observation.State == "failed" {
			return s.stop(p, "blocked", "115 离线失败或已停止，文件与下载目录已保留")
		}
		if observation.State != "completed" || observation.TargetCID == "" && observation.ResultID == "" {
			message := "115 离线中，完成并确认下载目录后自动整理所选文件"
			if job.Message != message {
				job.Message = message
				return false, o.checkpoint(job)
			}
			return false, nil
		}
		p.ready = true
		p.resultID = observation.ResultID
	}
	release, err := o.ReserveDirectoryWork()
	if err != nil {
		p.next = time.Now().Add(3 * time.Second)
		return false, nil
	}
	notify := false
	defer func() {
		release()
		if notify && o.OnCompleted != nil {
			o.OnCompleted(ctx, job.Config)
		}
	}()
	files, dirs, err := s.downloadFiles(ctx, job, cookie)
	// The exact API can omit wp_path_id. Prove the returned object belongs to
	// our staging directory using the scan already required for file binding.
	if err == nil && p.resultID != "" && !slices.Contains(dirs, p.resultID) && !slices.ContainsFunc(files, func(f OrganizeFile) bool { return f.ID == p.resultID }) {
		err = fmt.Errorf("115 离线结果不在本次下载目录中")
	}
	if err == nil {
		err = bindGardenFiles(job, files)
	}
	if err != nil {
		p.bindingAttempts++
		if p.bindingAttempts < 3 {
			p.next = time.Now().Add(10 * time.Second)
			return false, nil
		}
		return s.stop(p, "blocked", organizePublicError(err, cookie)+"；未执行重命名或移动，请在手动整理中检查下载目录")
	}
	// runBatch revalidates source identities and destination collisions just
	// before writes; another whole-plan scan would only duplicate those reads.
	if err = o.runJob(ctx, job, cookie); err != nil {
		return false, err
	}
	if job.Status == "completed" {
		job.Message = "所选文件已整理；未选文件保留在下载目录"
		// Remove only directories observed empty, deepest first, never leftovers.
		if recycle, ok := o.drive.(organizeRecycleDrive); ok {
			for i := len(dirs) - 1; i >= 0; i-- {
				children, e := o.list(ctx, dirs[i], cookie)
				if e != nil {
					break
				}
				if len(children) == 0 {
					if e = recycle.DeleteEntries(ctx, []string{dirs[i]}, cookie); e != nil {
						break
					}
				}
			}
		}
		if err = o.checkpoint(job); err != nil {
			return false, err
		}
		notify = true
	}
	return true, nil
}

func (s *GardenOrganize) downloadFiles(ctx context.Context, job *OrganizeJob, cookie string) ([]OrganizeFile, []string, error) {
	files := make([]OrganizeFile, 0, len(job.Items))
	dirs := make([]string, 0, 16)
	seen := make(map[string]bool)
	var walk func(string, string, int) error
	walk = func(cid, prefix string, depth int) error {
		if depth > 12 || len(dirs) >= 300 || seen[cid] || cid == job.Config.TargetCID {
			return fmt.Errorf("下载目录层级过深或目录重复，已停止整理")
		}
		seen[cid] = true
		dirs = append(dirs, cid)
		children, err := s.Organize.list(ctx, cid, cookie)
		if err != nil {
			return err
		}
		for _, f := range children {
			if f.Directory {
				if err = walk(f.ID, path.Join(prefix, f.Name), depth+1); err != nil {
					return err
				}
				continue
			}
			if len(files) >= 5000 {
				return fmt.Errorf("下载目录文件过多，已停止整理")
			}
			files = append(files, OrganizeFile{ShareFile: f, ParentID: cid, SourcePath: path.Join(prefix, f.Name), MediaType: organizeMedia(f.Name)})
		}
		return nil
	}
	err := walk(job.Download.DirectoryID, "", 0)
	return files, dirs, err
}

func bindGardenFiles(job *OrganizeJob, files []OrganizeFile) error {
	index := make(map[string][]OrganizeFile, len(files))
	for _, f := range files {
		index[f.SourcePath] = append(index[f.SourcePath], f)
	}
	bound := make([]OrganizeItem, len(job.Items))
	used := make(map[string]bool, len(job.Items))
	for i, item := range job.Items {
		matches := index[strings.TrimPrefix(item.File.SourcePath, "/")]
		if len(matches) != 1 || used[matches[0].ID] {
			return fmt.Errorf("下载目录中无法唯一匹配原路径：%s", item.File.SourcePath)
		}
		f := matches[0]
		if f.Directory || f.MediaType != "video" || strings.EqualFold(path.Ext(f.Name), ".iso") {
			return fmt.Errorf("实际文件不是支持的视频：%s", f.Name)
		}
		used[f.ID] = true
		f.SourcePath = path.Join(job.Config.SourcePath, f.SourcePath)
		item.File = f
		bound[i] = item
	}
	job.Items = bound
	return nil
}
