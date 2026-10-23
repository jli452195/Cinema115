package service

import (
	"context"
	"errors"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

// These batch methods require a successful synchronous receipt. Unknown or
// partial receipts return an error; neither method retries a write.
type AnimeOrganizationGateway interface {
	RenameAnimeEntries(context.Context, []p115.RenameTarget, string) error
	MoveAnimeEntries(context.Context, []string, string, string) error
}

// Cleanup is scoped to the saved resource directory and uses the recycle bin.
type AnimeOrganizationCleanupGateway interface {
	DeleteDirectory(context.Context, string, string) error
}

type animeDirectoryCacheEntry struct {
	cid     string
	expires time.Time
}
type animeOrganizationCache struct {
	mu          sync.Mutex
	directories map[string]animeDirectoryCacheEntry
}

func (c *animeOrganizationCache) get(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.directories[key]
	if time.Now().Before(v.expires) {
		return v.cid
	}
	return ""
}
func (c *animeOrganizationCache) put(key, cid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.directories) >= 256 {
		clear(c.directories)
	}
	if c.directories == nil {
		c.directories = make(map[string]animeDirectoryCacheEntry, 32)
	}
	c.directories[key] = animeDirectoryCacheEntry{cid: cid, expires: time.Now().Add(10 * time.Minute)}
}

type animeOrganizationRun struct {
	service  *AnimeAdminService
	session  *AnimeDownloadSession
	job      model.AnimeDownloadJob
	op       model.AnimeOrganizeOperation
	listings map[string][]model.AnimeDriveEntry
}

func (s *AnimeAdminService) OrganizeDownloadedFiles(ctx context.Context, session *AnimeDownloadSession, jobID string) error {
	if s.OrganizationGateway == nil || s.FileGateway == nil {
		return ErrAnimeOfflineUnavailable
	}
	return s.withDownloadResource(ctx, session, jobID, func(ctx context.Context, job model.AnimeDownloadJob) error {
		if job.WorkflowState == "organized" {
			return nil
		}
		if s.ReserveOrganization != nil {
			release, err := s.ReserveOrganization()
			if err != nil {
				return ErrCloudTaskBusy
			}
			defer release()
		}
		op, err := s.Store.PrepareAnimeOrganization(ctx, job, session.leaseToken())
		if err != nil {
			return err
		}
		r := animeOrganizationRun{service: s, session: session, job: job, op: op, listings: make(map[string][]model.AnimeDriveEntry, 8)}
		if op.Progress.State == "needs_review" {
			s.logAnimeJobIssue(job, op.Progress.Code)
			return nil // Only an explicit reconcile action reopens this saved step.
		}
		// A persisted in-flight step is read-only, including after a restart.
		// Finish this pass after reconciliation; do not mix it with new writes.
		if op.Progress.Step != nil {
			return r.reconcile(ctx)
		}
		if op.Progress.State != "working" {
			return nil
		}
		err = r.run(ctx)
		var readErr *animeOrganizationReadFailure
		if errors.As(err, &readErr) {
			return r.readFailure(ctx, readErr)
		}
		return err
	})
}

func (r *animeOrganizationRun) save(ctx context.Context, p model.AnimeOrganizeProgress, dispatch bool) error {
	writeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	op, err := r.service.Store.CheckpointAnimeOrganization(writeCtx, r.op, p, dispatch, r.session.leaseToken())
	if err == nil {
		r.service.logAnimeOrganizationResult(r.job, r.op.Progress, op.Progress)
		r.op = op
	}
	return err
}
func (r *animeOrganizationRun) progress() model.AnimeOrganizeProgress {
	p := r.op.Progress
	p.Files = append([]model.AnimeOrganizeFileProgress{}, p.Files...)
	p.Directories = append([]model.AnimeOrganizeDirectory{}, p.Directories...)
	return p
}
func (r *animeOrganizationRun) stop(ctx context.Context, code string) error {
	p := r.progress()
	p.State, p.Code = "needs_review", code
	return r.save(ctx, p, false)
}
func (r *animeOrganizationRun) list(ctx context.Context, cid string) ([]model.AnimeDriveEntry, error) {
	if v, ok := r.listings[cid]; ok {
		return v, nil
	}
	v, err := r.service.readAnimeDirectory(ctx, r.session, cid, 4096)
	if err != nil {
		return nil, animeOrganizationReadError(err)
	}
	r.listings[cid] = v
	return v, nil
}
func animeOrganizeShareFiles(entries []model.AnimeDriveEntry) []p115.ShareFile {
	files := make([]p115.ShareFile, 0, len(entries))
	for _, e := range entries {
		files = append(files, p115.ShareFile{ID: e.ID, Name: e.Name, Directory: e.Kind == "directory"})
	}
	return files
}
func (r *animeOrganizationRun) directoryCID(relative string) string {
	if relative == "" || relative == "." {
		return r.op.Request.Target.ParentCID
	}
	for _, d := range r.op.Progress.Directories {
		if d.Path == relative {
			return d.CID
		}
	}
	return ""
}
func (r *animeOrganizationRun) cacheKey(relative string) string {
	t := r.op.Request.Target
	return t.UID + ":" + t.AccountID + ":" + strconv.FormatInt(t.AccountRevision, 10) + ":" + t.ParentCID + ":" + relative
}
func (r *animeOrganizationRun) rememberDirectory(ctx context.Context, relative, cid string) error {
	p := r.progress()
	p.Directories = append(p.Directories, model.AnimeOrganizeDirectory{Path: relative, CID: cid})
	if err := r.save(ctx, p, false); err != nil {
		return err
	}
	r.service.organizationCache.put(r.cacheKey(relative), cid)
	return nil
}

func (r *animeOrganizationRun) resolveDirectory(ctx context.Context, relative string) (string, error) {
	if cid := r.directoryCID(relative); cid != "" {
		return cid, nil
	}
	prefix := ""
	for _, part := range strings.Split(relative, "/") {
		prefix = path.Join(prefix, part)
		if r.directoryCID(prefix) != "" {
			continue
		}
		if cached := r.service.organizationCache.get(r.cacheKey(prefix)); cached != "" {
			if err := r.rememberDirectory(ctx, prefix, cached); err != nil {
				return "", err
			}
			continue
		}
		parent := r.directoryCID(path.Dir(prefix))
		entries, err := r.list(ctx, parent)
		if err != nil {
			return "", err
		}
		cid, err := organizeFindDirectory(animeOrganizeShareFiles(entries), part)
		if err != nil {
			return "", r.stop(ctx, "ORGANIZE_TARGET_CONFLICT")
		}
		if cid != "" {
			if err := r.rememberDirectory(ctx, prefix, cid); err != nil {
				return "", err
			}
			continue
		}
		step := model.AnimeOrganizeStep{Kind: "mkdir", CID: parent, Path: prefix}
		if err := r.dispatch(ctx, step); err != nil {
			return "", err
		}
		cid, err = r.service.FileGateway.CreateDirectory(ctx, parent, part, r.session.cookie)
		if err != nil || !model.ValidAnimeExternalID(cid) || cid == parent {
			return "", r.unknown(ctx, err)
		}
		if err := r.confirm(ctx, nil, cid, false); err != nil {
			return "", err
		}
		// A positive create receipt supplies an empty new directory. No GET.
		r.listings[cid] = []model.AnimeDriveEntry{}
		r.listings[parent] = append(entries, model.AnimeDriveEntry{ID: cid, ParentCID: parent, Name: part, Kind: "directory"})
		r.service.organizationCache.put(r.cacheKey(prefix), cid)
	}
	return r.directoryCID(relative), nil
}

func (r *animeOrganizationRun) dispatch(ctx context.Context, step model.AnimeOrganizeStep) error {
	if err := r.session.Validate(ctx); err != nil {
		return err
	}
	p := r.progress()
	p.Step = &step
	p.State, p.Code = "working", ""
	p.RetryAt = 0
	p.ReceiptIssue = ""
	p.RecoveryChecks = 0
	if err := r.save(ctx, p, true); err != nil {
		return err
	}
	// Permission was durable before the write. Cancellation/change leaves a
	// read-only intent; it cannot cause an automatic replay on the next pass.
	return r.session.Validate(ctx)
}
func (r *animeOrganizationRun) unknown(ctx context.Context, writeErr error) error {
	p := r.progress()
	p.State, p.Code = "unknown", "ORGANIZE_UNCERTAIN"
	var receiptErr *p115.AnimeBatchReceiptError
	if errors.As(writeErr, &receiptErr) {
		p.ReceiptIssue = receiptErr.Reason()
	}
	if writeErr != nil {
		code, retryAt := animeStatusReadFailure(writeErr, time.Now().UnixMilli())
		if code == "TASK_RATE_LIMITED" {
			p.Code, p.RetryAt = code, retryAt
		}
		if code == "TASK_ACCESS_DENIED" {
			p.State, p.Code = "needs_review", code
		}
	}
	return r.save(ctx, p, false)
}

func (r *animeOrganizationRun) confirm(ctx context.Context, ids []string, cid string, partial bool) error {
	p := r.progress()
	step := p.Step
	if step.Kind == "mkdir" && cid != "" {
		p.Directories = append(p.Directories, model.AnimeOrganizeDirectory{Path: step.Path, CID: cid})
	}
	for i, f := range p.Files {
		for _, id := range ids {
			if f.ID == id {
				p.Files[i].Renamed = true
				if step.Kind == "move" {
					p.Files[i].Moved = true
				}
			}
		}
	}
	p.State, p.Code = "working", ""
	p.RetryAt = 0
	if partial {
		// An early check may precede the drive's visible update. Allow one
		// later scoped read, never another write or an unbounded scan loop.
		p.RecoveryChecks = min(p.RecoveryChecks+1, 2)
		p.State, p.Code = "unknown", "ORGANIZE_UNCERTAIN"
		p.RetryAt = time.Now().Add(30 * time.Second).UnixMilli()
		if p.RecoveryChecks >= 2 {
			p.State, p.Code = "needs_review", "ORGANIZE_RESULT_UNRESOLVED"
			p.RetryAt = 0
		}
	} else {
		p.Step = nil
		p.ReceiptIssue = ""
		p.RecoveryChecks = 0
	}
	all := len(p.Files) > 0
	for _, f := range p.Files {
		all = all && f.Moved
	}
	if all && p.Step == nil {
		if _, ok := r.service.OrganizationGateway.(AnimeOrganizationCleanupGateway); !ok {
			p.State, p.Cleanup = "done", "skipped"
		}
	}
	return r.save(ctx, p, false)
}

func (r *animeOrganizationRun) finishCleanup(ctx context.Context, outcome string) error {
	p := r.progress()
	p.State, p.Code, p.Cleanup, p.Step, p.RetryAt = "done", "", outcome, nil, 0
	p.ReceiptIssue = ""
	return r.save(ctx, p, false)
}

func (r *animeOrganizationRun) cleanup(ctx context.Context) error {
	gateway, ok := r.service.OrganizationGateway.(AnimeOrganizationCleanupGateway)
	if !ok {
		return r.finishCleanup(ctx, "skipped")
	}
	if r.job.DirectoryCID == nil {
		return storage.ErrAnimeConflict
	}
	// The transaction checks all moved receipts, the saved root identity and
	// current authorization. No extra drive listing or metadata read is needed.
	if err := r.dispatch(ctx, model.AnimeOrganizeStep{Kind: "cleanup", CID: *r.job.DirectoryCID}); err != nil {
		if errors.Is(err, storage.ErrAnimeWriteUnauthorized) {
			return r.finishCleanup(ctx, "skipped")
		}
		return err
	}
	outcome := "recycled"
	if err := gateway.DeleteDirectory(ctx, *r.job.DirectoryCID, r.session.cookie); err != nil {
		outcome = "uncertain"
	}
	// Cleanup failure does not invalidate completed media moves. An uncertain
	// delete is never replayed, including when its receipt cannot be persisted.
	return r.finishCleanup(ctx, outcome)
}

func (r *animeOrganizationRun) reconcile(ctx context.Context) error {
	step := r.op.Progress.Step
	if step.Kind == "cleanup" {
		return r.finishCleanup(ctx, "uncertain")
	}
	entries, err := r.list(ctx, step.CID)
	if err != nil {
		var readErr *animeOrganizationReadFailure
		if errors.As(err, &readErr) {
			return r.readFailure(ctx, readErr)
		}
		return err
	}
	if step.Kind == "mkdir" {
		cid, err := organizeFindDirectory(animeOrganizeShareFiles(entries), path.Base(step.Path))
		if err != nil {
			return r.stop(ctx, "ORGANIZE_RESULT_UNRESOLVED")
		}
		if cid == "" {
			return r.confirm(ctx, nil, "", true)
		}
		return r.confirm(ctx, nil, cid, false)
	}
	confirmed := make([]string, 0, len(step.IDs))
	for _, id := range step.IDs {
		for _, f := range r.op.Request.Files {
			if f.ID != id {
				continue
			}
			for _, entry := range entries {
				if entry.ID == id && entry.Kind == "file" && entry.Name == path.Base(f.TargetPath) && !organizeCollision(animeOrganizeShareFiles(entries), entry.Name, id) {
					confirmed = append(confirmed, id)
					break
				}
			}
		}
	}
	return r.confirm(ctx, confirmed, "", len(confirmed) != len(step.IDs))
}

func (r *animeOrganizationRun) run(ctx context.Context) error {
	allMoved := len(r.op.Progress.Files) > 0
	for _, file := range r.op.Progress.Files {
		allMoved = allMoved && file.Moved
	}
	if allMoved {
		return r.cleanup(ctx)
	}
	// Resolve each path once; all batches share its conflict listing in this pass.
	for _, f := range r.op.Request.Files {
		cid, err := r.resolveDirectory(ctx, path.Dir(f.TargetPath))
		if err != nil {
			return err
		}
		if cid == "" {
			return nil
		} // a recorded unknown/conflict stopped resolution
	}
	// Check every planned destination before changing the first source name.
	for i, f := range r.op.Request.Files {
		if r.op.Progress.Files[i].Moved {
			continue
		}
		entries, err := r.list(ctx, r.directoryCID(path.Dir(f.TargetPath)))
		if err != nil {
			return err
		}
		if organizeCollision(animeOrganizeShareFiles(entries), path.Base(f.TargetPath), f.ID) {
			return r.stop(ctx, "ORGANIZE_TARGET_CONFLICT")
		}
		if !r.op.Progress.Files[i].Renamed {
			// The isolated source tree was already listed once. Do not scan it
			// again or request per-file metadata just to repeat that evidence.
			for _, e := range r.job.Files.Entries {
				if e.ParentCID == f.SourceCID && e.ID != f.ID && strings.EqualFold(e.Name, path.Base(f.TargetPath)) {
					return r.stop(ctx, "ORGANIZE_TARGET_CONFLICT")
				}
			}
		}
	}
	for _, kind := range []string{"rename", "move"} {
		for {
			step := model.AnimeOrganizeStep{Kind: kind, IDs: []string{}}
			for i, f := range r.op.Request.Files {
				p := r.op.Progress.Files[i]
				if p.Moved || kind == "rename" && p.Renamed {
					continue
				}
				cid := f.SourceCID
				if kind == "move" {
					cid = r.directoryCID(path.Dir(f.TargetPath))
				}
				if step.CID == "" {
					step.CID = cid
				}
				if cid == step.CID && len(step.IDs) < p115.DriveMutationBatchLimit {
					step.IDs = append(step.IDs, f.ID)
				}
			}
			if len(step.IDs) == 0 {
				break
			}
			if err := r.dispatch(ctx, step); err != nil {
				return err
			}
			var writeErr error
			if kind == "rename" {
				names := make([]p115.RenameTarget, 0, len(step.IDs))
				for _, id := range step.IDs {
					for _, f := range r.op.Request.Files {
						if f.ID == id {
							names = append(names, p115.RenameTarget{ID: id, Name: path.Base(f.TargetPath)})
						}
					}
				}
				writeErr = r.service.OrganizationGateway.RenameAnimeEntries(ctx, names, r.session.cookie)
			} else {
				writeErr = r.service.OrganizationGateway.MoveAnimeEntries(ctx, step.IDs, step.CID, r.session.cookie)
			}
			if writeErr != nil {
				return r.unknown(ctx, writeErr)
			}
			if err := r.confirm(ctx, step.IDs, "", false); err != nil {
				return err
			}
			if r.op.Progress.State == "done" {
				return nil
			}
		}
	}
	return r.cleanup(ctx)
}

// Upstream read failures remain a recoverable stage, not a worker-wide stop.
func animeOrganizationReadError(err error) error {
	if errors.Is(err, storage.ErrAnimeAccountChanged) || errors.Is(err, storage.ErrAnimeConflict) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	code, retryAt := animeStatusReadFailure(err, time.Now().UnixMilli())
	if code == "TASK_READ_FAILED" {
		code = "ORGANIZE_READ_FAILED"
	}
	return &animeOrganizationReadFailure{code: code, retryAt: retryAt}
}

type animeOrganizationReadFailure struct {
	code    string
	retryAt int64
}

func (e *animeOrganizationReadFailure) Error() string { return e.code }
func (r *animeOrganizationRun) readFailure(ctx context.Context, e *animeOrganizationReadFailure) error {
	p := r.progress()
	p.Code, p.RetryAt = e.code, e.retryAt
	p.State = "working"
	if p.Step != nil {
		p.State = "unknown"
	}
	if e.code == "TASK_ACCESS_DENIED" {
		p.State = "needs_review"
	}
	return r.save(ctx, p, false)
}
