package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMediaOrganizerTaskRetryOnlyFailedCompanions(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	for _, name := range []string{"Work.S01E01.strm", "Work.S01E01.ass", "Work.S01E02.mkv"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	var subtitleTarget string
	for _, e := range run.Entries {
		if e.File.MediaType == "subtitle" {
			subtitleTarget = e.Target
			mediaTestFile(t, rule.Target, e.Relative, "conflict")
		}
	}
	mediaTestProcess(t, s, &run, "execute")
	task, err := s.Task(run.ID, "Work.S01E01.strm")
	if err != nil || task.Status != "partial" || len(task.Entries) != 2 || s.State(1).History.Total != 2 {
		t.Fatal("one card per primary; failed companion must be partial", task, err)
	}
	other, _ := s.Task(run.ID, "Work.S01E02.mkv")
	inProgress := run
	inProgress.Status = "running"
	for _, projected := range mediaTasks(inProgress) {
		if projected.FileID == other.FileID && projected.Status != "done" {
			t.Fatal("a completed task must stay completed while its batch is running", projected)
		}
	}
	videoResult := task.Entries[0].Result
	s.metadata = nil // Failed companions use the saved plan, without querying TMDB.
	if err := os.Remove(subtitleTarget); err != nil {
		t.Fatal(err)
	}
	queued, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: task.FileID})
	if err != nil {
		t.Fatal(err)
	}
	queued.Entries[0].Result.Message = "mutated"
	snapshot, _ := s.Snapshot(run.ID)
	mediaTestProcess(t, s, &snapshot, "retry")
	finished, _ := s.Task(run.ID, task.FileID)
	unchanged, _ := s.Task(run.ID, other.FileID)
	if finished.Status != "done" || !reflect.DeepEqual(videoResult, finished.Entries[0].Result) || !reflect.DeepEqual(other, unchanged) {
		t.Fatal("retry moved completed primary, changed sibling, or lost attachment", finished, unchanged)
	}
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: task.FileID}); err == nil {
		t.Fatal("completed task should not queue again")
	}
}

func TestMediaOrganizerSeparateVersionsAndSharedArtwork(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	for _, name := range []string{"a/Work.S01E01.mkv", "b/Work.S01E01.mkv", "poster.jpg"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	state := s.State(1)
	if state.History.Total != 2 {
		t.Fatal("different file versions merged", state)
	}
	attachments := 0
	for _, item := range state.History.Items {
		attachments += item.AttachmentCount
	}
	if attachments != 1 {
		t.Fatal("shared artwork duplicated or lost", state)
	}
	for _, id := range []string{"a/Work.S01E01.mkv", "b/Work.S01E01.mkv"} {
		task, err := s.Task(run.ID, id)
		if err != nil || task.FileID != id || task.Entries[0].File.ID != id {
			t.Fatal("task identity lost", task, err)
		}
	}
}

func TestMediaOrganizerTaskCorrectionAndRestartScope(t *testing.T) {
	s, metadata, store, rule := newMediaOrganizeTest(t)
	metadata.items = nil
	rule.Failure = t.TempDir()
	rule, _ = s.SaveRule(rule)
	for _, name := range []string{"Unknown.S01E01.strm", "Unknown.S01E01.ass", "Unknown.S01E02.strm"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	other, _ := s.Task(run.ID, "Unknown.S01E02.strm")
	_, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: "Unknown.S01E01.strm", Kind: "tv", TMDBID: 42})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := restarted.Snapshot(run.ID)
	if snapshot.Status != "interrupted" || snapshot.TaskFileID != "Unknown.S01E01.strm" {
		t.Fatal("restart lost task scope", snapshot)
	}
	_, err = restarted.Retry(run.ID, MediaOrganizeCorrection{FileID: snapshot.TaskFileID})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ = restarted.Snapshot(run.ID)
	mediaTestProcess(t, restarted, &snapshot, "retry")
	finished, _ := restarted.Task(run.ID, "Unknown.S01E01.strm")
	unchanged, _ := restarted.Task(run.ID, other.FileID)
	want, marshalErr := json.Marshal(other)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	got, marshalErr := json.Marshal(unchanged)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if finished.Status != "done" || string(want) != string(got) {
		t.Fatal("single correction changed another task", finished, unchanged)
	}
	if _, err := os.Stat(filepath.Join(rule.Failure, other.FileID)); err != nil {
		t.Fatal("other failed task was moved", err)
	}
}

// A small real-filesystem run: STRM content, video, artwork, cleanup, duplicate
// submission and a second scan use the same owned worker as the application.
func TestMediaOrganizerGrayRun(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	const strm = "http://example.invalid/original\r\n"
	mediaTestFile(t, rule.Source, "show/Work.S01E01.strm", strm)
	for _, name := range []string{"show/Work.S01E01.ass", "show/Work.S01E02.mkv", "show/poster.jpg", "Other.Movie.2023-poster.jpg", "ads.txt"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.Start(rule.ID)
	if err != nil || duplicate.ID != run.ID {
		t.Fatal("duplicate submission", duplicate, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	wait := func(id string) MediaOrganizeRun {
		t.Helper()
		until := time.Now().Add(5 * time.Second)
		for time.Now().Before(until) {
			r, _ := s.Snapshot(id)
			if !mediaRunActive(r.Status) {
				return r
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("worker did not complete")
		return MediaOrganizeRun{}
	}
	run = wait(run.ID)
	state := s.State(1)
	if state.History.Total != 2 || len(state.Cleanup) != 1 || state.Rules[0].Enabled {
		t.Fatal("incorrect task/cleanup projection or monitoring toggled", state)
	}
	task, err := s.Task(run.ID, "show/Work.S01E01.strm")
	if err != nil || task.Status != "done" {
		t.Fatal(task, err)
	}
	data, err := os.ReadFile(task.Entries[0].Target)
	if err != nil || string(data) != strm {
		t.Fatal("STRM changed", err)
	}
	for _, name := range []string{"Other.Movie.2023-poster.jpg", "ads.txt", "show"} {
		if _, err := os.Stat(filepath.Join(rule.Source, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("cleanup failed", name, err)
		}
	}
	next, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	wait(next.ID)
	if s.State(1).History.Total != 2 {
		t.Fatal("empty run duplicated history")
	}
	t.Log("gray run: 2 media tasks, matched subtitles/artwork, orphan cleanup, unchanged STRM, duplicate submission and empty rescan passed")
}
