package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
)

// Prepared manifests retain verified file identities while a user edits a large
// selection. They are separate from each site's raw-response cache: a batch of
// 100 single episodes must not evict and re-fetch its first 68 torrents.
type downloadManifest struct {
	source DownloadResourceInput
	body   []byte
	at     time.Time
}

type DownloadBatchInput struct {
	DownloadOrganizeInput
	Resources []DownloadOrganizeInput `json:"resources"`
}

type DownloadBatchResult struct {
	ID   string        `json:"id"`
	Jobs []OrganizeJob `json:"jobs"`
}

type downloadBatch struct {
	jobs []OrganizeJob
	at   time.Time
}

func newDownloadPending(job OrganizeJob, account P115Account) *downloadPending {
	p := &downloadPending{job: cloneOrganizeJob(job), account: account, targets: make([]string, 0, len(job.Items))}
	for _, item := range job.Items {
		p.targets = append(p.targets, downloadTargetKey(item))
	}
	return p
}

func (s *DownloadOrganize) Files(ctx context.Context, in DownloadResourceInput) (DownloadFiles, error) {
	if len(in.Title)+len(in.Fansub)+len(in.Magnet)+len(in.TorrentURL)+len(in.Source)+len(in.Provider)+len(in.ProviderID) > 16<<10 {
		return DownloadFiles{}, ErrOrganizeInput
	}
	out, err := s.resourceFiles(ctx, in)
	if err != nil || len(out.Files) == 0 {
		return out, err
	}
	prepareDownloadNaming(&out, in)
	body, err := json.Marshal(out)
	if err != nil {
		return DownloadFiles{}, fmt.Errorf("缓存资源清单: %w", err)
	}
	if len(body) > 8<<20 {
		return DownloadFiles{}, fmt.Errorf("%w：文件清单过大，请使用普通离线", ErrOrganizeInput)
	}
	id, err := organizeToken()
	if err != nil {
		return DownloadFiles{}, err
	}
	if err = ctx.Err(); err != nil {
		return DownloadFiles{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prunePreviewsLocked()
	bytes := len(body)
	for _, entry := range s.manifests {
		bytes += len(entry.body)
	}
	for len(s.manifests) >= 128 || bytes > 16<<20 {
		oldest := ""
		for key, entry := range s.manifests {
			if oldest == "" || entry.at.Before(s.manifests[oldest].at) {
				oldest = key
			}
		}
		bytes -= len(s.manifests[oldest].body)
		delete(s.manifests, oldest)
	}
	s.manifests[id] = downloadManifest{source: in, body: body, at: time.Now()}
	out.ManifestID = id
	return out, nil
}

// Keep naming facts identical with or without an already-confirmed manifest.
func prepareDownloadNaming(out *DownloadFiles, in DownloadResourceInput) {
	for i := range out.Files {
		spec := out.Specs[out.Files[i].ID]
		if spec.Fansub == "" {
			spec.Fansub = strings.TrimSpace(in.Fansub)
		}
		out.Files[i].NamingValues = mediaFileValues(out.Files[i].Name, spec)
	}
	mediaDirectorySubtitleValues(out.Files)
	// A single video's post title can fill missing cosmetic tags. For a pack,
	// only each file's metadata applies; the pack title cannot describe all rows.
	if len(out.Files) == 1 && !out.Files[0].TitleOnly && in.Title != "" {
		spec := out.Specs[out.Files[0].ID]
		title := resourceSearchSpec(in.Title)
		if spec.Resolution == "" {
			spec.Resolution = title.Resolution
		}
		if len(spec.Languages) == 0 {
			spec.Languages = title.Languages
		}
		if spec.Fansub == "" {
			spec.Fansub = downloadFansub(in.Title)
		}
		if out.Specs == nil {
			out.Specs = make(map[string]DownloadFileSpec, 1)
		}
		out.Specs[out.Files[0].ID] = spec
		for field, value := range mediaFileValues(out.Files[0].Name, spec) {
			if out.Files[0].NamingValues[field] == "" {
				out.Files[0].NamingValues[field] = value
			}
		}
	}
}

func (s *DownloadOrganize) manifest(ctx context.Context, in DownloadOrganizeInput) (DownloadFiles, error) {
	if in.ManifestID == "" {
		out, err := s.resourceFiles(ctx, in.DownloadResourceInput)
		if err == nil {
			prepareDownloadNaming(&out, in.DownloadResourceInput)
		}
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return DownloadFiles{}, err
	}
	s.mu.Lock()
	entry, ok := s.manifests[in.ManifestID]
	s.mu.Unlock()
	if !ok || time.Since(entry.at) > 30*time.Minute || entry.source != in.DownloadResourceInput {
		return DownloadFiles{}, fmt.Errorf("%w：资源清单已过期或来源不一致，请重新打开整理弹窗", ErrOrganizeConflict)
	}
	var out DownloadFiles
	if err := json.Unmarshal(entry.body, &out); err != nil {
		return out, fmt.Errorf("读取已确认的文件清单: %w", err)
	}
	return out, nil
}

// Build one combined plan so metadata, destination reads and cross-resource
// collisions are checked once. Namespace identities, then restore original
// torrent paths when splitting into the existing per-magnet worker jobs.
func (s *DownloadOrganize) previewResources(ctx context.Context, common DownloadOrganizeInput, resources []DownloadOrganizeInput) ([]OrganizeJob, error) {
	if len(resources) < 1 || len(resources) > 100 {
		return nil, ErrOrganizeInput
	}
	count := 0
	for _, resource := range resources {
		count += len(resource.Files)
		if len(resource.Files) == 0 || count > 1000 {
			return nil, fmt.Errorf("%w：每批最多 100 项资源、1000 个所选视频", ErrOrganizeInput)
		}
	}
	download := s.Settings.View()
	// Share STRM owns a separate result wrapper at the main drive root. The
	// ordinary organize destination is neither required nor updated by it.
	if common.ShareSTRM {
		common.TargetCID, common.TargetPath = "0", "/"
	}
	if common.AccountID != download.AccountID || !organizeID(common.TargetCID) || !organizePath(common.TargetPath) {
		return nil, fmt.Errorf("%w：请选择当前主账号的整理目录", ErrOrganizeInput)
	}
	a, err := s.Downloads.snapshot(download)
	if err != nil {
		return nil, err
	}
	var shareSettings AutomationSettings
	var shareDirectory string
	if common.ShareSTRM {
		shareSettings, err = s.ShareSettings(a.ID)
		if err != nil {
			return nil, fmt.Errorf("%w：%s", ErrOrganizeInput, err)
		}
		token, err := organizeToken()
		if err != nil {
			return nil, err
		}
		shareDirectory = "Cinema115-" + token
	}
	c := OrganizeConfig{AccountID: a.ID, SourceCID: download.TargetCID, SourcePath: download.TargetPath, TargetCID: common.TargetCID, TargetPath: common.TargetPath}
	files := make([]OrganizeFile, 0, count)
	selections := make([]OrganizeSelection, 0, count)
	originals := make(map[string]OrganizeFile, count)
	owners := make(map[string]int, count)
	hashes := make(map[string]bool, len(resources))
	downloads := make([]*ResourceDownload, 0, len(resources))
	for index, resource := range resources {
		manifest, err := s.manifest(ctx, resource)
		if err != nil {
			return nil, err
		}
		if len(manifest.Files) == 0 || hashes[manifest.Hash] {
			return nil, fmt.Errorf("%w：资源没有可整理视频或磁力重复", ErrOrganizeInput)
		}
		hashes[manifest.Hash] = true
		downloads = append(downloads, &ResourceDownload{Hash: manifest.Hash, ParentCID: download.TargetCID, ParentPath: download.TargetPath})
		if common.ShareSTRM {
			downloads[index].Share = &DownloadShare{Directory: shareDirectory, Settings: shareSettings}
		}
		prefix := manifest.Hash + "/"
		add := func(file OrganizeFile) {
			key := prefix + file.ID
			originals[key], owners[key] = file, index
			file.ID, file.ParentID, file.SourcePath = key, prefix+file.ParentID, "/"+prefix+strings.TrimPrefix(file.SourcePath, "/")
			files = append(files, file)
		}
		for _, file := range manifest.Files {
			add(file)
		}
		for _, subtitle := range manifest.Subtitles {
			add(subtitle.File)
		}
		if len(files) > 5000 {
			return nil, fmt.Errorf("%w：整批媒体清单超过 5000 个文件，请分批选择", ErrOrganizeInput)
		}
		for _, selection := range resource.Files {
			selection.ID = prefix + selection.ID
			selections = append(selections, selection)
		}
	}
	combined, err := s.Organize.buildPlan(ctx, OrganizeScan{Config: c, Revision: a.Revision, Files: files}, OrganizePreviewInput{NamingRevision: common.NamingRevision, Kind: common.Kind, TMDBID: common.TMDBID, Files: selections, Subtitles: true})
	if err != nil {
		return nil, err
	}
	for i := range combined.Items {
		item := &combined.Items[i]
		if share := downloads[owners[item.File.ID]].Share; share != nil {
			item.Directory = path.Join(share.Directory, item.Directory)
			item.TargetPath = path.Join(c.TargetPath, item.Directory, item.Name)
		}
	}
	if err = s.Organize.checkPlan(ctx, &combined, a.Cookie, false); err != nil {
		return nil, err
	}
	// A title-only intent has no extension yet. Reserve video stems so mixed
	// title/file previews cannot silently acquire the same name after binding.
	targets := make(map[string][]int, len(combined.Items))
	for i, item := range combined.Items {
		key := downloadTargetKey(item)
		targets[key] = append(targets[key], i)
	}
	for _, indexes := range targets {
		if len(indexes) > 1 {
			for _, i := range indexes {
				combined.Items[i].Conflict = "本次多个文件目标同名，请调整自定义标记或季集号"
			}
		}
	}
	jobs := make([]OrganizeJob, len(resources))
	for i := range jobs {
		jobs[i] = combined
		jobs[i].Items = []OrganizeItem{}
		jobs[i].Download = downloads[i]
	}
	for _, item := range combined.Items {
		index := owners[item.File.ID]
		item.File = originals[item.File.ID]
		jobs[index].Items = append(jobs[index].Items, item)
	}
	return jobs, nil
}

func (s *DownloadOrganize) PreviewBatch(ctx context.Context, in DownloadBatchInput) (DownloadBatchResult, error) {
	jobs, err := s.previewResources(ctx, in.DownloadOrganizeInput, in.Resources)
	if err != nil {
		return DownloadBatchResult{}, err
	}
	id, err := organizeToken()
	if err != nil {
		return DownloadBatchResult{}, err
	}
	for i := range jobs {
		jobs[i].ID, err = organizeToken()
		if err != nil {
			return DownloadBatchResult{}, err
		}
		jobs[i].Download.BatchID = id
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prunePreviewsLocked()
	if len(s.batches) >= 8 {
		oldest := ""
		for key, batch := range s.batches {
			if oldest == "" || batch.at.Before(s.batches[oldest].at) {
				oldest = key
			}
		}
		delete(s.batches, oldest)
	}
	s.batches[id] = downloadBatch{jobs: jobs, at: time.Now()}
	return DownloadBatchResult{ID: id, Jobs: cloneDownloadJobs(jobs)}, nil
}

// One durable commit accepts the whole batch, before any worker can write.
// A lost HTTP receipt reuses BatchID; it cannot partially enqueue or resubmit.
func (s *DownloadOrganize) StartBatch(id string) (DownloadBatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.Organize
	o.mu.Lock()
	defer o.mu.Unlock()
	accepted := []OrganizeJob{}
	for _, job := range o.doc.Jobs {
		if job.Download != nil && job.Download.BatchID == id && id != "" {
			accepted = append(accepted, cloneOrganizeJob(job))
		}
	}
	if len(accepted) > 0 {
		return DownloadBatchResult{ID: id, Jobs: accepted}, nil
	}
	s.prunePreviewsLocked()
	batch, ok := s.batches[id]
	if !ok {
		return DownloadBatchResult{}, fmt.Errorf("%w：批量预览已过期，请重新确认", ErrOrganizeConflict)
	}
	if s.trackedDownloadsLocked()+len(batch.jobs) > 100 {
		return DownloadBatchResult{}, fmt.Errorf("%w：下载整理队列最多 100 项，请等待或减少选择", ErrOrganizeConflict)
	}
	var account P115Account
	for i, job := range batch.jobs {
		a, err := s.snapshotJob(job)
		if err != nil {
			return DownloadBatchResult{}, err
		}
		if i > 0 && (account.ID != a.ID || account.Revision != a.Revision) {
			return DownloadBatchResult{}, ErrOrganizeConflict
		}
		account = a
	}
	jobs := cloneDownloadJobs(batch.jobs)
	doc := o.doc
	for i := range jobs {
		jobs[i].Status, jobs[i].UpdatedAt, jobs[i].CreatedAt = "queued", time.Now(), time.Now()
		jobs[i].Message = "已加入批量下载整理队列，完成一项即整理一项"
		if jobs[i].Download.Share != nil {
			jobs[i].Message = "已加入批量下载队列，就绪资源将合并整理与分享；晚到资源自动补批"
		}
		doc.Jobs = prependOrganizeJob(jobs[i], doc.Jobs)
	}
	c := jobs[0].Config
	if jobs[0].Download.Share == nil {
		doc.DownloadTarget = CloudDownloadConfig{AccountID: c.AccountID, TargetCID: c.TargetCID, TargetPath: c.TargetPath}
	}
	if err := o.saveLocked(doc); err != nil {
		return DownloadBatchResult{}, err
	}
	for _, job := range jobs {
		s.live[job.ID] = newDownloadPending(job, account)
	}
	delete(s.batches, id)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return DownloadBatchResult{ID: id, Jobs: cloneDownloadJobs(jobs)}, nil
}

func cloneDownloadJobs(jobs []OrganizeJob) []OrganizeJob {
	out := make([]OrganizeJob, len(jobs))
	for i, job := range jobs {
		out[i] = cloneOrganizeJob(job)
	}
	return out
}

func (s *DownloadOrganize) prunePreviewsLocked() {
	for key, manifest := range s.manifests {
		if time.Since(manifest.at) > 30*time.Minute {
			delete(s.manifests, key)
		}
	}
	for key, batch := range s.batches {
		if time.Since(batch.at) > 30*time.Minute {
			delete(s.batches, key)
		}
	}
}
