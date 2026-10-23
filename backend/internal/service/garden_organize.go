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
	resuming                  bool
	continuing                bool
	waitUntil                 time.Time
	running                   bool // guarded by GardenOrganize.mu; other fields have one worker owner
}

type GardenOrganize struct {
	Organize  *OrganizeEngine
	Garden    *GardenService
	Downloads *CloudDownloadService
	Settings  *CloudDownloadSettings
	Reader    gardenStatusReader
	mu        sync.Mutex
	live      map[string]*gardenPending
	wake      chan struct{}
}

func NewGardenOrganize(o *OrganizeEngine, g *GardenService, downloads *CloudDownloadService, settings *CloudDownloadSettings, reader gardenStatusReader) *GardenOrganize {
	return &GardenOrganize{Organize: o, Garden: g, Downloads: downloads, Settings: settings, Reader: reader, live: make(map[string]*gardenPending), wake: make(chan struct{}, 1)}
}

func (s *GardenOrganize) History() []OrganizeJob { return s.Organize.history(true) }

func (s *GardenOrganize) Target() CloudDownloadConfig {
	o := s.Organize
	o.mu.Lock()
	defer o.mu.Unlock()
	c := o.doc.DownloadTarget
	if id := o.accounts.PrimaryID(); c.AccountID != id {
		return CloudDownloadConfig{AccountID: id}
	}
	return c
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
	a, err := s.snapshotJob(job)
	if err != nil {
		return OrganizeJob{}, err
	}
	job = cloneOrganizeJob(job)
	job.Status, job.UpdatedAt = "queued", time.Now()
	job.Message = "等待提交 115，整条磁力下载后仅整理所选文件"
	doc := o.doc
	doc.Jobs = prependOrganizeJob(job, doc.Jobs)
	doc.DownloadTarget = CloudDownloadConfig{AccountID: job.Config.AccountID, TargetCID: job.Config.TargetCID, TargetPath: job.Config.TargetPath}
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

// Called with s.mu held; both creation and explicit recovery pin credentials
// and share the same concurrency/hash gate without making network requests.
func (s *GardenOrganize) snapshotJob(job OrganizeJob) (P115Account, error) {
	a, err := s.Organize.account(job.Config.AccountID)
	if err != nil {
		return a, err
	}
	if a.Revision != job.Revision {
		return a, fmt.Errorf("%w：账号配置已变化，请重新确认账号和目录", ErrOrganizeConflict)
	}
	if len(s.live) >= 4 {
		return a, fmt.Errorf("%w：已有 4 个下载整理任务，请等待完成", ErrOrganizeConflict)
	}
	for _, p := range s.live {
		if p.job.Download.Hash == job.Download.Hash {
			return a, ErrCloudTaskBusy
		}
	}
	for _, item := range job.Items {
		if item.Conflict != "" {
			return a, fmt.Errorf("%w：请先处理命名冲突", ErrOrganizeInput)
		}
	}
	return a, nil
}

func canResumeGarden(job OrganizeJob) bool {
	d := job.Download
	if job.Status != "interrupted" || d == nil || !organizeID(d.DirectoryID) || d.DirectoryID == "0" || job.Config.SourceCID != d.DirectoryID || job.Config.SourcePath != path.Join(d.ParentPath, "garden-"+job.ID) || d.DirectoryID == job.Config.TargetCID || len(job.Items) == 0 || job.Stats.RenameRequests != 0 || job.Stats.MoveRequests != 0 {
		return false
	}
	for _, item := range job.Items {
		// A persisted renaming/moving intent can precede an unknown outcome;
		// zero completed files or zero recorded requests cannot prove safety.
		if item.Status != "pending" && item.Status != "preparing" {
			return false
		}
	}
	return true
}

// Resume only rejoins tracking for an existing download. It never enters the
// queued/submitting branch, including after a lost response or another restart.
func (s *GardenOrganize) Resume(id string) (OrganizeJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.Organize
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, saved := range o.doc.Jobs {
		if saved.ID != id || saved.Download == nil {
			continue
		}
		if s.live[id] != nil || saved.Status == "completed" {
			return cloneOrganizeJob(saved), nil
		}
		continuing := canContinueOrganize(saved)
		if !continuing && !canResumeGarden(saved) {
			return OrganizeJob{}, fmt.Errorf("%w：此任务暂不能继续，请核对下载目录与任务详情", ErrOrganizeConflict)
		}
		a, err := s.snapshotJob(saved)
		if err != nil {
			return OrganizeJob{}, err
		}
		job := cloneOrganizeJob(saved)
		job.Status, job.UpdatedAt = "downloading", time.Now()
		job.Stats.StartedAt = time.Now().Add(-time.Duration(job.Stats.ElapsedMS) * time.Millisecond)
		job.Message = "正在检查原 115 任务，确认后继续整理；不会重新提交离线"
		if continuing {
			job.Status, job.Message = "running", "正在检查原任务剩余文件，确认后继续整理；不会重新下载"
		}
		doc := o.doc
		doc.Jobs = append([]OrganizeJob{}, doc.Jobs...)
		doc.Jobs[i] = job
		if err = o.saveLocked(doc); err != nil {
			return OrganizeJob{}, err
		}
		s.live[id] = &gardenPending{job: cloneOrganizeJob(job), account: a, resuming: !continuing, continuing: continuing, ready: continuing, waitUntil: time.Now().Add(24 * time.Hour)}
		select {
		case s.wake <- struct{}{}:
		default:
		}
		return job, nil
	}
	return OrganizeJob{}, fmt.Errorf("%w：下载整理任务不存在或已不在最近记录中", ErrOrganizeInput)
}

func (s *GardenOrganize) Run(ctx context.Context) (runErr error) {
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	failures := make(chan error, 1)
	defer func() {
		cancel()
		workers.Wait()
		select {
		case err := <-failures:
			runErr = err
		default:
		}
	}()
	for {
		s.mu.Lock()
		for id, p := range s.live {
			if p.running {
				continue
			}
			p.running = true
			workers.Add(1)
			go func(id string, p *gardenPending) {
				defer workers.Done()
				if err := s.runPending(ctx, p); err != nil {
					select {
					case failures <- err:
					default:
					}
					cancel() // A persistence failure stops every worker before further writes.
				}
				s.mu.Lock()
				delete(s.live, id)
				s.mu.Unlock()
			}(id, p)
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil
		case <-s.wake:
		}
	}
}

// At most four independent workers track downloads. Only the shared directory
// reservation serializes organization; no worker holds it while polling/sleeping.
func (s *GardenOrganize) runPending(ctx context.Context, p *gardenPending) error {
	for {
		if ctx.Err() != nil {
			_, err := s.stop(p, "interrupted", "服务停止，任务已中断；请核对任务记录后继续")
			return err
		}
		if delay := time.Until(p.next); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				continue
			case <-timer.C:
			}
		}
		done, err := s.advance(ctx, p)
		if err != nil || done {
			return err
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
	if ctx.Err() != nil {
		return s.stop(p, "interrupted", "服务停止，任务已中断；请核对任务记录后继续")
	}
	if p.waitUntil.IsZero() {
		p.waitUntil = job.CreatedAt.Add(24 * time.Hour)
	}
	if time.Now().After(p.waitUntil) {
		message := "等待离线超过 24 小时，已停止自动整理；文件保留在本次下载目录"
		if p.ready {
			message = "等待整理超过任务时限，已停止自动整理；文件保留在本次下载目录"
		}
		return s.stop(p, "interrupted", message)
	}
	if job.Status == "queued" {
		if done, err := s.submit(ctx, p); err != nil || done {
			return done, err
		}
		// Check once immediately: instant downloads need no fixed waiting period.
	}
	if !p.ready {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		observation, err := s.Reader.ReadAnimeTaskStatus(readCtx, cookie, job.Download.Hash)
		cancel()
		if ctx.Err() != nil {
			return s.stop(p, "interrupted", "服务停止，任务已中断；请核对任务记录后继续")
		}
		p.attempts++
		delays := []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}
		p.next = time.Now().Add(delays[min(p.attempts-1, len(delays)-1)])
		if p.resuming && (err != nil || !observation.Present) {
			message := "未找到原 115 离线任务，请核对下载目录；未重新提交离线"
			if err != nil {
				message = "暂时无法查询原 115 任务，可稍后再次检查并继续；未重新提交离线"
			}
			return s.stop(p, "interrupted", message)
		}
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
		p.resuming = false
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
	return s.organize(ctx, p)
}

func (s *GardenOrganize) submit(ctx context.Context, p *gardenPending) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	o, job, cookie := s.Organize, &p.job, p.account.Cookie
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
	return false, o.checkpoint(job)
}

func (s *GardenOrganize) organize(ctx context.Context, p *gardenPending) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	o, job, cookie := s.Organize, &p.job, p.account.Cookie
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
	scanCtx, cancelScan := context.WithTimeout(ctx, 90*time.Second)
	var files []OrganizeFile
	var dirs []string
	if !p.continuing {
		files, dirs, err = s.downloadFiles(scanCtx, job, cookie)
	}
	cancelScan()
	// The exact API can omit wp_path_id. Prove the returned object belongs to
	// our staging directory using the scan already required for file binding.
	if err == nil && p.resultID != "" && !slices.Contains(dirs, p.resultID) && !slices.ContainsFunc(files, func(f OrganizeFile) bool { return f.ID == p.resultID }) {
		err = fmt.Errorf("115 离线结果不在本次下载目录中")
	}
	if err == nil && !p.continuing {
		err = bindGardenFiles(job, files, p.resultID)
	}
	if err != nil {
		p.bindingAttempts++
		if p.bindingAttempts < 3 {
			p.next = time.Now().Add(10 * time.Second)
			return false, nil
		}
		return s.stop(p, "blocked", organizePublicError(err, cookie)+"；未执行重命名或移动，请检查本次下载目录")
	}
	// runBatch revalidates source identities and destination collisions just
	// before writes; another whole-plan scan would only duplicate those reads.
	if err = o.runJob(ctx, job, cookie); err != nil {
		return false, err
	}
	if job.Status == "completed" {
		job.Message = "所选文件已整理；未选文件保留在下载目录"
		if p.continuing {
			_, dirs, _ = s.downloadFiles(ctx, job, cookie)
		}
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

func bindGardenFiles(job *OrganizeJob, files []OrganizeFile, resultID string) error {
	index := make(map[string][]OrganizeFile, len(files))
	for _, f := range files {
		index[f.SourcePath] = append(index[f.SourcePath], f)
	}
	bound := make([]OrganizeItem, len(job.Items))
	used := make(map[string]bool, len(job.Items))
	for i, item := range job.Items {
		// A restart after binding/preparing preserves cloud paths rather than
		// website paths. Normalize either representation to the original relative path.
		relative := strings.TrimPrefix(item.File.SourcePath, strings.TrimRight(job.Config.SourcePath, "/")+"/")
		relative = strings.TrimPrefix(relative, "/")
		matches := index[relative]
		// 115 can wrap a single-file torrent in a directory with that exact
		// filename. Accept only this one-level shape, with one actual file and
		// a verified result ID; retain its actual cloud path for recovery.
		if len(matches) == 0 && len(job.Items) == 1 && len(files) == 1 && resultID != "" {
			f := files[0]
			if relative == f.Name && f.SourcePath == path.Join(relative, relative) && (resultID == f.ID || resultID == f.ParentID) {
				matches = files
			}
		}
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
