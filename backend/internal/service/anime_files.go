package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

// Reuse the normal drive client; no playback, copy, delete or move is exposed.
type AnimeFileGateway interface {
	CreateDirectory(context.Context, string, string, string) (string, error)
	ReadAnimeDrivePage(context.Context, string, int, string) (p115.AnimeDrivePage, error)
}

var (
	ErrAnimeFilesIncomplete  = errors.New("anime download files are incomplete or ambiguous")
	ErrAnimeFilesOutside     = errors.New("anime download result is outside the task directory")
	ErrAnimeDirectoryPending = errors.New("anime resource directory creation needs reconciliation")
	errAnimeFilesPending     = fmt.Errorf("%w: directory listing is not settled", ErrAnimeFilesIncomplete)
)

// EnsureDownloadDirectory creates one randomly named resource directory once.
// A lost receipt is recovered by its saved name under the fixed parent only.
func (s *AnimeAdminService) EnsureDownloadDirectory(ctx context.Context, session *AnimeDownloadSession, jobID string) (model.AnimeDownloadJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var out model.AnimeDownloadJob
	err := s.withDownloadResource(ctx, session, jobID, func(ctx context.Context, job model.AnimeDownloadJob) error {
		out = job
		if s.FileGateway == nil {
			return ErrAnimeOfflineUnavailable
		}
		if job.DirectoryCID != nil {
			return nil
		}
		current, create, err := s.Store.StartAnimeDirectory(ctx, job.ID, job.Generation, session.Target(), session.leaseToken())
		if err != nil {
			return err
		}
		out = current
		if current.DirectoryOperation == nil || current.DirectoryOperation.Generation != job.Generation || current.DirectoryOperation.State == "failed" {
			return ErrAnimeDirectoryPending
		}
		var directory *model.AnimeDriveEntry
		code := ""
		if create {
			if err := session.Validate(ctx); err != nil {
				code := "DIRECTORY_CREATE_UNCERTAIN"
				if errors.Is(err, storage.ErrAnimeAccountChanged) {
					code = "DOWNLOAD_ACCOUNT_CHANGED"
				}
				if writeErr := s.finishAnimeDirectory(ctx, session, current, nil, code); writeErr != nil {
					return writeErr
				}
				return err
			}
			cid, createErr := s.FileGateway.CreateDirectory(ctx, job.ParentCID, job.DirectoryName, session.cookie)
			if createErr != nil {
				code = "DIRECTORY_CREATE_UNCERTAIN"
			} else {
				entry := model.AnimeDriveEntry{ID: cid, ParentCID: job.ParentCID, Name: job.DirectoryName, Kind: "directory"}
				if !entry.Valid() {
					code = "DIRECTORY_CREATE_UNCERTAIN"
				} else {
					directory = &entry
				}
			}
		} else {
			// Recovery only: no repeat POST, and no global/fuzzy search.
			entries, readErr := s.readAnimeDirectory(ctx, session, job.ParentCID, 4096)
			if readErr != nil {
				code = "DIRECTORY_READ_FAILED"
			} else {
				for _, entry := range entries {
					if entry.Name == job.DirectoryName {
						if directory != nil || entry.Kind != "directory" {
							code = "DIRECTORY_CONFLICT"
							break
						}
						copy := entry
						directory = &copy
					}
				}
				if code == "" && directory == nil {
					code = "DIRECTORY_NOT_FOUND"
				}
			}
		}
		if directory != nil && (directory.Kind != "directory" || directory.ParentCID != job.ParentCID || directory.Name != job.DirectoryName) {
			directory = nil
			code = "DIRECTORY_CONFLICT"
		}
		if code != "" {
			directory = nil
		}
		if err := session.Validate(ctx); err != nil {
			directory = nil
			code = "DIRECTORY_READ_FAILED"
			if errors.Is(err, storage.ErrAnimeAccountChanged) {
				code = "DOWNLOAD_ACCOUNT_CHANGED"
			}
		}
		if err := s.finishAnimeDirectory(ctx, session, current, directory, code); err != nil {
			return err
		}
		if directory == nil {
			return ErrAnimeDirectoryPending
		}
		return nil
	})
	if out.ID != "" {
		readCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		current, readErr := s.Store.GetAnimeDownloadJob(readCtx, jobID)
		if readErr == nil {
			out = current
		} else if err == nil {
			err = readErr
		}
	}
	return out, err
}

func (s *AnimeAdminService) finishAnimeDirectory(ctx context.Context, session *AnimeDownloadSession, job model.AnimeDownloadJob, directory *model.AnimeDriveEntry, code string) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := s.Store.FinishAnimeDirectory(writeCtx, job, directory, code, session.leaseToken())
	if errors.Is(err, storage.ErrAnimeAccountChanged) {
		if writeErr := s.Store.FinishAnimeDirectory(writeCtx, job, nil, "DOWNLOAD_ACCOUNT_CHANGED", session.leaseToken()); writeErr != nil {
			return writeErr
		}
		return storage.ErrAnimeAccountChanged
	}
	return err
}

// readAnimeDirectory consumes only this CID's complete bounded pages. Count
// changes, duplicate entries and short/missing pages never become file facts.
func (s *AnimeAdminService) readAnimeDirectory(ctx context.Context, session *AnimeDownloadSession, cid string, limit int) ([]model.AnimeDriveEntry, error) {
	entries := make([]model.AnimeDriveEntry, 0, min(limit, 100))
	seen := make(map[string]bool, min(limit, 100))
	count := -1
	for offset := 0; ; {
		if err := session.Validate(ctx); err != nil {
			return nil, err
		}
		page, err := s.FileGateway.ReadAnimeDrivePage(ctx, cid, offset, session.cookie)
		if err != nil {
			return nil, err
		}
		if page.CID != cid || page.Offset != offset || page.Count < 0 || page.Count > limit || len(page.Entries) > 100 {
			return nil, ErrAnimeFilesIncomplete
		}
		if count != -1 && page.Count != count || offset+len(page.Entries) > page.Count || len(page.Entries) == 0 && offset < page.Count {
			return nil, errAnimeFilesPending
		}
		count = page.Count
		for _, entry := range page.Entries {
			if !entry.Valid() || entry.ParentCID != cid || entry.ID == cid {
				return nil, ErrAnimeFilesIncomplete
			}
			if seen[entry.ID] {
				return nil, errAnimeFilesPending
			}
			seen[entry.ID] = true
			entries = append(entries, entry)
		}
		offset += len(page.Entries)
		if offset == count {
			return entries, nil
		}
	}
}

// CollectDownloadedFiles normally lists the saved task directory once after completion.
// Only the result's subdirectories need extra pages. Finished work is never
// revisited, even when the user subsequently moves or renames its files.
func (s *AnimeAdminService) CollectDownloadedFiles(ctx context.Context, session *AnimeDownloadSession, jobID string) (model.AnimeDownloadJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var out model.AnimeDownloadJob
	err := s.withDownloadResource(ctx, session, jobID, func(ctx context.Context, job model.AnimeDownloadJob) error {
		out = job
		if s.FileGateway == nil {
			return ErrAnimeOfflineUnavailable
		}
		if job.FileState == "verified" {
			return nil
		}
		if !job.CanCollectFiles() {
			return storage.ErrAnimeConflict
		}
		manifest, readErr := s.readAnimeFiles(ctx, session, job)
		if readErr == nil {
			readErr = session.Validate(ctx)
		}
		if readErr != nil {
			if errors.Is(readErr, storage.ErrAnimeConflict) {
				return readErr
			}
			code, retryAt := animeFileReadFailure(readErr, time.Now().UnixMilli())
			writeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			if err := s.Store.RecordAnimeFilesFailure(writeCtx, job, code, retryAt, session.leaseToken()); err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrAnimeFilesIncomplete
		}
		return s.Store.SaveAnimeFiles(ctx, job, manifest, session.leaseToken())
	})
	if out.ID != "" {
		readCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		current, readErr := s.Store.GetAnimeDownloadJob(readCtx, jobID)
		if readErr == nil {
			s.logAnimeJobResult(out, current)
			out = current
		} else if err == nil {
			err = readErr
		}
	}
	return out, err
}

func animeFileReadFailure(err error, now int64) (string, int64) {
	code, retry := animeStatusReadFailure(err, now)
	if code == "DOWNLOAD_ACCOUNT_CHANGED" || code == "TASK_ACCESS_DENIED" || code == "TASK_RATE_LIMITED" || code == "TASK_POLL_CANCELLED" {
		if code == "TASK_POLL_CANCELLED" {
			retry = now
		}
		return code, retry
	}
	if errors.Is(err, ErrAnimeFilesOutside) {
		return "FILES_OUTSIDE_TASK", 0
	}
	if errors.Is(err, errAnimeFilesPending) {
		return "FILES_INCOMPLETE", now
	}
	if errors.Is(err, ErrAnimeFilesIncomplete) || errors.Is(err, p115.ErrAnimeDriveResponse) {
		return "FILES_INCOMPLETE", 0
	}
	var response *p115.HTTPError
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &network) || errors.As(err, &response) && response.Status >= 500 {
		return "FILES_READ_FAILED", now
	}
	return "FILES_READ_FAILED", 0
}

func (s *AnimeAdminService) readAnimeFiles(ctx context.Context, session *AnimeDownloadSession, job model.AnimeDownloadJob) (model.AnimeFileManifest, error) {
	manifest := model.AnimeFileManifest{SchemaVersion: 1, UID: job.UID, ResultID: *job.ResultObjectID, Entries: make([]model.AnimeDriveEntry, 0, 8)}
	root := model.AnimeDriveEntry{ID: *job.DirectoryCID, ParentCID: job.ParentCID, Name: job.DirectoryName, Kind: "directory"}
	manifest.Entries = append(manifest.Entries, root)
	rootEntries, err := s.readAnimeDirectory(ctx, session, root.ID, 511)
	if err != nil {
		return manifest, err
	}
	result := root
	if manifest.ResultID != root.ID {
		found := false
		for _, entry := range rootEntries {
			if entry.ID == manifest.ResultID {
				result, found = entry, true
				break
			}
		}
		if !found {
			return manifest, ErrAnimeFilesOutside
		}
		manifest.Entries = append(manifest.Entries, result)
	}
	// Only the bound result tree is enumerated; never other task directories.
	type folder struct {
		entry model.AnimeDriveEntry
		depth int
	}
	queue := make([]folder, 0, 8)
	if result.Kind == "directory" {
		queue = append(queue, folder{entry: result})
	}
	seen := map[string]bool{root.ID: true, result.ID: true}
	for index := 0; index < len(queue); index++ {
		if len(queue) > 32 || queue[index].depth >= 8 {
			return manifest, ErrAnimeFilesIncomplete
		}
		entries := rootEntries
		if queue[index].entry.ID != root.ID {
			var err error
			entries, err = s.readAnimeDirectory(ctx, session, queue[index].entry.ID, 512-len(manifest.Entries))
			if err != nil {
				return manifest, err
			}
		}
		for _, entry := range entries {
			if seen[entry.ID] {
				return manifest, ErrAnimeFilesIncomplete
			}
			seen[entry.ID] = true
			manifest.Entries = append(manifest.Entries, entry)
			if entry.Kind == "directory" {
				queue = append(queue, folder{entry: entry, depth: queue[index].depth + 1})
			}
		}
	}
	manifest.CollectedAt = time.Now().UnixMilli()
	files := 0
	for _, entry := range manifest.Entries {
		if entry.Kind == "file" {
			files++
		}
	}
	if files == 0 {
		return manifest, errAnimeFilesPending
	}
	if manifest.Validate(job) != nil {
		return manifest, ErrAnimeFilesIncomplete
	}
	return manifest, nil
}
