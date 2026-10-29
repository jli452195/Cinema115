package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestMediaOrganizerFailureDirectoryCorrectionAndRestart(t *testing.T) {
	s, f, store, rule := newMediaOrganizeTest(t)
	f.items = nil
	rule.Failure = t.TempDir()
	rule, err := s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestFile(t, rule.Source, "season/Unknown.S01E01.strm", "http://unchanged/")
	mediaTestFile(t, rule.Source, "season/Unknown.S01E01.ass", "subtitle")
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	if run.FailureCount != 2 || run.ReadyCount != 0 {
		t.Fatal("unmatched video and companion must be eligible", run)
	}
	if _, err := os.Stat(filepath.Join(rule.Failure, "season/Unknown.S01E01.strm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview must never move files", err)
	}
	queued, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &queued, "auto")
	for _, e := range queued.Entries {
		if e.File.Directory {
			if e.Status != "done" {
				t.Fatal("emptied source directory should be cleaned", e)
			}
			continue
		}
		if e.Failure == nil || e.Failure.State != "stored" || e.Failure.Message != "" {
			t.Fatal("failure move missing", e)
		}
		if _, err := os.Stat(filepath.Join(rule.Source, filepath.FromSlash(e.File.ID))); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("source retained after successful move", err)
		}
		if _, err := os.Stat(filepath.Join(rule.Failure, filepath.FromSlash(e.File.ID))); err != nil {
			t.Fatal("original hierarchy lost", err)
		}
	}
	if queued.FailureCount != 0 || queued.ReadyCount != 0 {
		t.Fatal("stored failures must not be offered for repeated moves", queued)
	}
	scan, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &scan, "auto")
	if len(scan.Entries) != 0 {
		t.Fatal("failure directory must not be scanned")
	}
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := restarted.Snapshot(queued.ID)
	snapshot.Entries[0].Failure.State = "tampered"
	snapshot, _ = restarted.Snapshot(queued.ID)
	if snapshot.Entries[0].Failure.State != "stored" {
		t.Fatal("failure snapshot aliases live state")
	}
	_, err = restarted.Retry(queued.ID, MediaOrganizeCorrection{FileID: queued.Entries[0].File.ID, Kind: "tv", TMDBID: 42})
	if err != nil {
		t.Fatal(err)
	}
	corrected, _ := restarted.Snapshot(queued.ID)
	mediaTestProcess(t, restarted, &corrected, "retry")
	for _, e := range corrected.Entries {
		if e.File.Directory {
			continue
		}
		if e.Status != "done" || e.Failure != nil {
			t.Fatal("failure recovery did not complete", e)
		}
		if _, err := os.Stat(filepath.Join(rule.Target, filepath.FromSlash(e.Relative))); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(rule.Failure, filepath.FromSlash(e.File.ID))); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failure copy was not removed", err)
		}
	}
}

func TestMediaOrganizerFailureDirectoryConflictAndTransient(t *testing.T) {
	t.Run("conflict keeps companion and destination", func(t *testing.T) {
		s, f, _, rule := newMediaOrganizeTest(t)
		f.items = nil
		rule.Failure = t.TempDir()
		rule, _ = s.SaveRule(rule)
		mediaTestFile(t, rule.Source, "Unknown.S01E01.mkv", "source")
		mediaTestFile(t, rule.Source, "Unknown.S01E01.ass", "subtitle")
		mediaTestFile(t, rule.Failure, "Unknown.S01E01.mkv", "existing")
		run, _ := s.Start(rule.ID)
		mediaTestProcess(t, s, &run, "auto")
		for _, name := range []string{"Unknown.S01E01.mkv", "Unknown.S01E01.ass"} {
			if _, err := os.Stat(filepath.Join(rule.Source, name)); err != nil {
				t.Fatal("failed quarantine must retain video and companion", err)
			}
		}
		if data, _ := os.ReadFile(filepath.Join(rule.Failure, "Unknown.S01E01.mkv")); string(data) != "existing" {
			t.Fatal("failure destination overwritten")
		}
		if run.Entries[0].Failure.Message == "" {
			t.Fatal("quarantine error missing")
		}
	})
	t.Run("metadata timeout retains source and retries", func(t *testing.T) {
		s, f, _, rule := newMediaOrganizeTest(t)
		f.err = context.DeadlineExceeded
		rule.Failure = t.TempDir()
		rule, _ = s.SaveRule(rule)
		mediaTestFile(t, rule.Source, "Work.S01E01.mkv", "source")
		mediaTestFile(t, rule.Source, "Work.S01E01.ass", "subtitle")
		run, _ := s.Start(rule.ID)
		mediaTestProcess(t, s, &run, "auto")
		if run.FailureCount != 0 {
			t.Fatal("metadata failure must not be quarantined or negatively cached", run)
		}
		for _, e := range run.Entries {
			if e.Failure != nil {
				t.Fatal(e)
			}
		}
		if _, err := os.Stat(filepath.Join(rule.Source, "Work.S01E01.mkv")); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMediaOrganizerCloudFailureMoveAndUncertainResume(t *testing.T) {
	manual, drive, accountID, _ := newOrganizeTest(t)
	f := &mediaRecognitionFake{}
	s, err := NewMediaOrganizer(&failingAccountStore{}, f, organizeDefaultTemplates{}, nil, manual.accounts, mediaOrganizeDirectoryFake{drive}, manual.ReserveDirectoryWork)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.SaveRule(MediaOrganizeRule{Name: "cloud", IntervalMinutes: 5, Storage: "115", AccountID: accountID, SourceID: "10", TargetID: "20", FailureID: "40", Source: "/inbox", Target: "/library", Failure: "/failed"})
	if err != nil {
		t.Fatal(err)
	}
	drive.dirs = map[string][]p115.ShareFile{"10": {{ID: "30", Name: "Unknown.S01E01.mkv", Size: 10}}, "20": {}, "40": {}}
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	drive.moveErr, drive.applyMove = context.DeadlineExceeded, true
	mediaTestProcess(t, s, &run, "execute")
	if run.Entries[0].Failure.State != "moving" || len(drive.dirs["40"]) != 1 {
		t.Fatal("ambiguous cloud move must retain its intent", run)
	}
	drive.moveErr = nil
	before := drive.moves
	mediaTestProcess(t, s, &run, "execute")
	if run.Entries[0].Failure.State != "stored" || drive.moves != before {
		t.Fatal("resume must reconcile exact file without repeating a completed cloud move", run)
	}
	if drive.renames != 0 {
		t.Fatal("quarantine must retain original filename")
	}
	_, err = s.Retry(run.ID, MediaOrganizeCorrection{FileID: run.Entries[0].File.ID, Kind: "tv", TMDBID: 42})
	if err != nil {
		t.Fatal(err)
	}
	corrected, _ := s.Snapshot(run.ID)
	mediaTestProcess(t, s, &corrected, "retry")
	if corrected.Status != "completed" || corrected.Entries[0].Failure != nil || len(drive.dirs["40"]) != 0 || drive.moves != before+1 {
		t.Fatal("cloud correction did not move from the recorded failure directory", corrected, drive.moves)
	}
}

func TestMediaOrganizerFailureDirectoryValidation(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	child := filepath.Join(rule.Source, "failed")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{rule.Source, rule.Target, child} {
		rule.Failure = root
		if _, err := s.SaveRule(rule); err == nil {
			t.Fatal("overlapping failure root accepted", root)
		}
	}
}

func TestMediaOrganizerFailureLocalMoveIntentRecovery(t *testing.T) {
	s, f, store, rule := newMediaOrganizeTest(t)
	f.items = nil
	rule.Failure = t.TempDir()
	rule, err := s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestFile(t, rule.Source, "Unknown.S01E01.strm", "http://original/")
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	run.Status = "running"
	run.Entries[0].Failure = &MediaOrganizeFailure{State: "moving", Reason: run.Entries[0].Message}
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := files.failureFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan := run.Entries[0]
	plan.Relative = plan.File.SourcePath
	if err := failed.move(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	// Simulate a restart between the physical move and its final checkpoint.
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := restarted.Snapshot(run.ID)
	if snapshot.Status != "interrupted" {
		t.Fatal(snapshot)
	}
	_, err = restarted.Retry(run.ID, MediaOrganizeCorrection{FileID: run.Entries[0].File.ID})
	queued, _ := restarted.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, restarted, &queued, "execute")
	if queued.Entries[0].Failure.State != "stored" || queued.Entries[0].Failure.Message != "" {
		t.Fatal("journal did not recover completed local move", queued)
	}
	if data, err := os.ReadFile(filepath.Join(rule.Failure, "Unknown.S01E01.strm")); err != nil || string(data) != "http://original/" {
		t.Fatal("STRM changed on recovery", err)
	}
}

func TestMediaOrganizerFailureHealthyWorkAndStorageError(t *testing.T) {
	t.Run("missing failure directory does not affect healthy work", func(t *testing.T) {
		s, _, _, rule := newMediaOrganizeTest(t)
		rule.Failure = t.TempDir()
		rule, err := s.SaveRule(rule)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(rule.Failure); err != nil {
			t.Fatal(err)
		}
		mediaTestFile(t, rule.Source, "Work.S01E01.strm", "http://original/")
		run, _ := s.Start(rule.ID)
		mediaTestProcess(t, s, &run, "auto")
		if run.Status != "completed" || run.Entries[0].Status != "done" {
			t.Fatal(run)
		}
	})
	t.Run("journal save failure prevents physical move", func(t *testing.T) {
		s, f, store, rule := newMediaOrganizeTest(t)
		f.items = nil
		rule.Failure = t.TempDir()
		rule, err := s.SaveRule(rule)
		if err != nil {
			t.Fatal(err)
		}
		mediaTestFile(t, rule.Source, "Unknown.S01E01.strm", "http://original/")
		run, _ := s.Start(rule.ID)
		mediaTestProcess(t, s, &run, "scan")
		files, err := s.files(context.Background(), rule)
		if err != nil {
			t.Fatal(err)
		}
		store.err = errors.New("disk full")
		if err := s.quarantine(context.Background(), files, &run, false); err == nil {
			t.Fatal("journal failure ignored")
		}
		if _, err := os.Stat(filepath.Join(rule.Source, "Unknown.S01E01.strm")); err != nil {
			t.Fatal("moved without journal", err)
		}
		if _, err := os.Stat(filepath.Join(rule.Failure, "Unknown.S01E01.strm")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("destination created without journal", err)
		}
	})
}

func TestMediaOrganizerFailureRecoveryPlanRetention(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	s.doc.Runs = []MediaOrganizeRun{{ID: "relocated", Rule: rule, Status: "partial", Entries: []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "unknown"}, MediaType: "video"}, Status: "needs_match", Failure: &MediaOrganizeFailure{State: "stored"}}}}}
	for i := range 25 {
		s.doc.Runs = append([]MediaOrganizeRun{{ID: fmt.Sprint(i), Rule: rule, Status: "completed", Entries: []MediaOrganizeEntry{{Status: "done"}}}}, s.doc.Runs...)
	}
	if _, err := s.Start(rule.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot("relocated"); err != nil {
		t.Fatal("retention discarded the recovery location", err)
	}
}
