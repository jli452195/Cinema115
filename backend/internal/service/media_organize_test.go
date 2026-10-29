package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
)

type mediaRecognitionFake struct {
	browseCalls int
	items       []tmdb.Item
	err         error
}

type mediaOrganizeDirectoryFake struct{ *organizeFakeDrive }

func (d mediaOrganizeDirectoryFake) ReadDirectory(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
	if err := ctx.Err(); err != nil {
		return p115.DriveDirectory{}, err
	}
	d.cookies = append(d.cookies, cookie)
	name := map[string]string{"10": "/inbox", "20": "/library", "40": "/failed"}[cid]
	return p115.DriveDirectory{CID: cid, Path: name}, nil
}

func (f *mediaRecognitionFake) Browse(context.Context, tmdb.Options) (*tmdb.Page, error) {
	f.browseCalls++
	return &tmdb.Page{Items: f.items}, f.err
}
func (f *mediaRecognitionFake) Details(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, Title: "作品", OriginalTitle: "Work", Date: "2024-01-01"}, OriginalLanguage: "zh"}, nil
}
func (f *mediaRecognitionFake) AlternativeNames(context.Context, string, int) ([]string, error) {
	return []string{"作品别名"}, nil
}

type mediaWaitingRecognition struct {
	*mediaRecognitionFake
	started chan struct{}
	release <-chan struct{}
}

func (f mediaWaitingRecognition) Browse(ctx context.Context, options tmdb.Options) (*tmdb.Page, error) {
	f.started <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.release:
		return f.mediaRecognitionFake.Browse(ctx, options)
	}
}

func newMediaOrganizeTest(t *testing.T) (*MediaOrganizer, *mediaRecognitionFake, *failingAccountStore, MediaOrganizeRule) {
	t.Helper()
	f := &mediaRecognitionFake{items: []tmdb.Item{{ID: 42, Kind: "tv", Title: "作品", OriginalTitle: "Work", Date: "2024-01-01"}}}
	store := &failingAccountStore{}
	s, err := NewMediaOrganizer(store, f, organizeDefaultTemplates{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.SaveRule(MediaOrganizeRule{Name: "临时验收", Storage: "local", Source: t.TempDir(), Target: t.TempDir(), IntervalMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	return s, f, store, rule
}
func mediaTestFile(t *testing.T, root, name, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(full, old, old); err != nil {
		t.Fatal(err)
	}
}
func mediaTestProcess(t *testing.T, s *MediaOrganizer, run *MediaOrganizeRun, action string) {
	t.Helper()
	s.mu.Lock()
	delete(s.actions, run.ID)
	s.mu.Unlock()
	if action == "scan" {
		files, err := s.files(context.Background(), run.Rule)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.scan(context.Background(), files, run); err != nil {
			t.Fatal(err)
		}
		// Pause between recognition and execution to inspect or mutate the plan.
		run.Status = "interrupted"
		if len(run.Entries) == 0 {
			run.Status = "completed"
		}
		if err := s.checkpoint(run); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := s.process(context.Background(), run, action); err != nil {
		t.Fatal(err)
	}
}

func TestMediaOrganizerLocalScanExecuteAndPersistence(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	const body = "http://example.invalid/play115/path?token=unchanged\r\n"
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", body)
	mediaTestFile(t, rule.Source, "Work.S01E01.zh-CN.ass", "subtitle")
	mediaTestFile(t, rule.Source, "Work.S01E02.mkv", "original-video")
	mediaTestFile(t, rule.Source, "unmatched.ass", "keep")
	mediaTestFile(t, rule.Source, "disc.iso", "excluded")
	if err := os.WriteFile(filepath.Join(rule.Source, "Work.S01E03.mkv"), []byte("writing"), 0644); err != nil {
		t.Fatal(err)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "scan")
	if run.Status != "interrupted" || len(run.Entries) != 4 {
		t.Fatalf("unexpected scan %+v", run)
	}
	state := mediaTestState(s, 1)
	if state.History.Total != 2 || state.History.Items[0].Status != "error" {
		t.Fatal("interrupted primary files must remain visible for recovery", state)
	}
	for _, e := range run.Entries {
		if _, err := os.Stat(filepath.Join(rule.Source, filepath.FromSlash(e.File.ID))); err != nil {
			t.Fatal("preview moved a file", err)
		}
	}
	queued, err := s.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	queued.Entries[0].Status = "tampered"
	snapshot, _ := s.Snapshot(run.ID)
	if snapshot.Entries[0].Status == "tampered" {
		t.Fatal("apply exposed shared snapshot")
	}
	mediaTestProcess(t, s, &snapshot, "execute")
	if snapshot.Status != "partial" {
		t.Fatalf("unmatched subtitle should remain visible: %+v", snapshot)
	}
	for _, e := range snapshot.Entries {
		if e.File.Name == "unmatched.ass" {
			if e.Status != "unmatched" {
				t.Fatal(e)
			}
			continue
		}
		if e.Status != "done" {
			t.Fatal(e)
		}
		data, err := os.ReadFile(filepath.Join(rule.Target, filepath.FromSlash(e.Relative)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(e.Relative, ".strm") && string(data) != body {
			t.Fatalf("STRM changed: %q", data)
		}
		if _, err := os.Stat(filepath.Join(rule.Source, filepath.FromSlash(e.File.ID))); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("source not removed", err)
		}
	}
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := restarted.Snapshot(run.ID)
	if len(saved.Entries) != 4 || saved.Status != "partial" {
		t.Fatal(saved)
	}
	state = mediaTestState(restarted, 1)
	if state.History.Total != 2 {
		t.Fatal("history must contain only executed files", state)
	}
	for _, item := range state.History.Items {
		if item.Status != "done" || item.Title != "作品" || item.Kind != "tv" || item.At.IsZero() || item.Target == "" || !strings.Contains(item.Source, item.Name) {
			t.Fatal("incomplete file history", item)
		}
	}
	saved.Entries[0].Result.Status = "tampered"
	fresh, _ := restarted.Snapshot(run.ID)
	if fresh.Entries[0].Result.Status != "done" {
		t.Fatal("snapshot result is not detached")
	}
}

func TestMediaOrganizerStandardNumberingPreviewAndExecution(t *testing.T) {
	s, metadata, _, rule := newMediaOrganizeTest(t)
	metadata.items = append(metadata.items, tmdb.Item{ID: 43, Kind: "tv", Title: "魔法骑士雷阿斯", Date: "2026-01-01"})
	cases := []struct {
		name            string
		season, episode int
	}{
		{"魔法骑士雷阿斯 (2026) - S01E04 - 1080p.Crunchyroll.WEB-DL.H264.AAC.mkv", 1, 4},
		{"Work.S02E05 - 2160p.strm", 2, 5},
		{"Work (2024)/Season 02/E06.strm", 2, 6},
		{"Work.S1000E11 - 1080p.strm", 1000, 11},
		{"[Nix-Raws] Work [07][1080p].mkv", 1, 7},
		{"Work.S01E08.5.mkv", 1, 0},
		{"Work.S01E09 [10].mkv", 1, 0},
	}
	const content = "http://example.invalid/original-file\r\n"
	for _, tc := range cases {
		mediaTestFile(t, rule.Source, tc.name, content)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "scan")
	if len(run.Entries) != len(cases) {
		t.Fatal("scan lost files", run)
	}
	for _, e := range run.Entries {
		for _, tc := range cases {
			if e.File.ID != tc.name {
				continue
			}
			if e.File.Season != tc.season || e.File.Episode != tc.episode {
				t.Fatalf("%s: got S%dE%d, want S%dE%d", tc.name, e.File.Season, e.File.Episode, tc.season, tc.episode)
			}
			if tc.episode == 0 {
				if e.Status != "invalid" || e.Target != "" {
					t.Fatal("ambiguous numbering became executable", e)
				}
			} else if e.Status != "ready" || !strings.Contains(e.Relative, fmt.Sprintf("S%02dE%02d", tc.season, tc.episode)) {
				t.Fatal("numbering did not reach naming preview", e)
			}
		}
	}
	run, err = s.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "execute")
	for _, e := range run.Entries {
		if e.File.Episode == 0 {
			if _, err := os.Stat(filepath.Join(rule.Source, filepath.FromSlash(e.File.ID))); err != nil {
				t.Fatal("ambiguous source was moved", e, err)
			}
			continue
		}
		body, err := os.ReadFile(filepath.Join(rule.Target, filepath.FromSlash(e.Relative)))
		if err != nil || e.Status != "done" || string(body) != content {
			t.Fatal("execution lost original file content or numbering", e, err)
		}
	}
}

func TestMediaOrganizerManualMatchPreservesSubtitles(t *testing.T) {
	s, f, _, rule := newMediaOrganizeTest(t)
	f.items = nil
	mediaTestFile(t, rule.Source, "Unknown.S01E01.mkv", "video")
	mediaTestFile(t, rule.Source, "Unknown.S01E01.ass", "sub")
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	_, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: run.Entries[0].File.ID, Kind: "tv", TMDBID: 42})
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := s.Snapshot(run.ID)
	mediaTestProcess(t, s, &updated, "retry")
	if len(updated.Entries) != 2 {
		t.Fatal("subtitle lost from scan snapshot")
	}
	for _, e := range updated.Entries {
		if e.Status != "done" {
			t.Fatal(e)
		}
	}
}

func TestMediaOrganizerConflictsMutationAndSubtitleDependency(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	mediaTestFile(t, rule.Source, "Work.S01E01.mkv", "video")
	mediaTestFile(t, rule.Source, "Work.S01E01.ass", "sub")
	mediaTestFile(t, rule.Source, "Work.S01E02.mkv", "video2")
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	for _, e := range run.Entries {
		if e.File.Name == "Work.S01E02.mkv" {
			mediaTestFile(t, rule.Target, e.Relative, "existing")
		}
	}
	if err := os.WriteFile(filepath.Join(rule.Source, "Work.S01E01.mkv"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "execute")
	for _, e := range run.Entries {
		if e.Status == "done" {
			t.Fatal("conflict or dependency unexpectedly moved", e)
		}
	}
	if data, err := os.ReadFile(filepath.Join(rule.Source, "Work.S01E01.ass")); err != nil || string(data) != "sub" {
		t.Fatal("subtitle moved without video", err)
	}
	state := mediaTestState(s, 1)
	if state.History.Total != 2 {
		t.Fatal("each failed video/subtitle attempt must be recorded", state)
	}
	for _, item := range state.History.Items {
		if item.Status != "error" && item.Status != "conflict" || item.Message == "" || item.At.IsZero() {
			t.Fatal("failure reason or time lost", item)
		}
	}
	before := state.History.Items[0]
	if _, err := s.Task(run.ID, run.Entries[0].File.ID); err != nil {
		t.Fatal(err)
	}
	after := mediaTestState(s, 1).History.Items[0]
	if after.Status != before.Status || after.Message != before.Message || !after.At.Equal(before.At) {
		t.Fatal("preview update changed execution result", before, after)
	}
}

func TestMediaOrganizerEmptyScansDoNotEvictFileHistory(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	for i := range 20 {
		s.doc.Runs = append(s.doc.Runs, MediaOrganizeRun{ID: "saved-" + strconv.Itoa(i), Rule: rule, Status: "completed", Entries: []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: p115.ShareFile{ID: strconv.Itoa(i)}, MediaType: "video"}, Status: "done"}}})
	}
	for i := range 25 {
		if i == 10 {
			mediaTestFile(t, rule.Source, "unowned/font.ttf", "retained font")
		}
		run, err := s.Start(rule.ID)
		if err != nil {
			t.Fatal(err)
		}
		action := "scan"
		if i%2 != 0 {
			action = "auto"
		}
		mediaTestProcess(t, s, &run, action)
		state := mediaTestState(s, 1)
		if run.Status != "completed" || state.History.Total != 20 || len(s.doc.Runs) != 21 {
			t.Fatal("empty scans must stay hidden and preserve actual history", run, state)
		}
	}
	s.doc.Runs = append(s.doc.Runs, MediaOrganizeRun{ID: "failed", Rule: rule, Status: "failed", Message: "来源目录不可访问"})
	if failed, err := s.Snapshot("failed"); err != nil || failed.Message == "" || mediaTestState(s, 1).History.Total != 20 {
		t.Fatal("scan failure must not create a file execution record", failed, err)
	}
}

func TestMediaOrganizerFileHistoryPagination(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	run := MediaOrganizeRun{ID: "batch", Rule: rule, Status: "completed", Groups: []MediaOrganizeGroup{{ID: "1", Title: "作品", Kind: "tv"}}}
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := range 45 {
		name := "Work.S01E" + strconv.Itoa(i+1) + ".strm"
		run.Entries = append(run.Entries, MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: name, Name: name}, SourcePath: name, Season: 1, Episode: i + 1, MediaType: "video"}, Group: "1", Status: "done", Target: "/library/" + name, Result: &MediaOrganizeResult{Status: "done", At: base.Add(time.Duration(i) * time.Second)}})
	}
	s.doc.Runs = []MediaOrganizeRun{run}
	for _, tt := range []struct{ requested, page, count, firstEpisode int }{{0, 1, 20, 45}, {2, 2, 20, 25}, {99, 3, 5, 5}} {
		t.Run(strconv.Itoa(tt.requested), func(t *testing.T) {
			state := mediaTestState(s, tt.requested)
			h := state.History
			if h.Page != tt.page || h.Pages != 3 || h.Total != 45 || len(h.Items) != tt.count || h.Items[0].Episode != tt.firstEpisode {
				t.Fatal("incorrect history pagination", state)
			}
			if h.Items[0].RunID != "batch" || h.Items[0].Season != 1 || h.Items[0].Source != filepath.ToSlash(rule.Source)+"/"+h.Items[0].Name {
				t.Fatal("history lost the original file identity", h.Items[0])
			}
		})
	}
}

func TestMediaOrganizerSourceRescanAndRollback(t *testing.T) {
	s, f, store, rule := newMediaOrganizeTest(t)
	f.items = nil
	mediaTestFile(t, rule.Source, "Unknown.S01E01.mkv", "v")
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	calls := f.browseCalls
	run, _ = s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	if f.browseCalls <= calls || len(run.Entries) != 1 {
		t.Fatal("source files must be eligible again, regardless of prior history")
	}
	if err := os.Remove(filepath.Join(rule.Source, "Unknown.S01E01.mkv")); err != nil {
		t.Fatal(err)
	}
	run, _ = s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	store.err = errors.New("disk full")
	rule.ID = ""
	rule.Name = "should not save"
	if _, err := s.SaveRule(rule); err == nil {
		t.Fatal("expected storage error")
	}
	state := mediaTestState(s, 1)
	if len(state.Rules) != 1 {
		t.Fatal("save failure did not roll back", state.Rules)
	}
}

func TestMediaOrganizerMonitoringToggleDuringWork(t *testing.T) {
	s, metadata, store, rule := newMediaOrganizeTest(t)
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "http://unchanged/")
	rule.Enabled = true
	if _, err := s.SaveRule(rule); err != nil {
		t.Fatal(err)
	}
	if err := s.schedule(); err != nil {
		t.Fatal(err)
	}
	runID := s.doc.Runs[0].ID
	lastScan := mediaTestState(s, 1).Rules[0].LastScan
	rule.Enabled = false
	if _, err := s.SaveRule(rule); err != nil {
		t.Fatal("queued work must not prevent pausing", err)
	}
	started, release := make(chan struct{}, 1), make(chan struct{})
	s.metadata = mediaWaitingRecognition{metadata, started, release}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("monitoring worker did not stop")
		}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("worker did not start")
	}
	for _, enabled := range []bool{true, false} {
		rule.Enabled = enabled
		saved, err := s.SaveRule(rule)
		if err != nil || saved.Enabled != enabled || !saved.LastScan.Equal(lastScan) {
			t.Fatal("monitoring toggle changed the scan schedule or was blocked", saved, err)
		}
	}
	changed := rule
	changed.Target = t.TempDir()
	if _, err := s.SaveRule(changed); !errors.Is(err, ErrMediaOrganizeBusy) {
		t.Fatal("running work must still block configuration changes", err)
	}
	store.err = errors.New("disk full")
	rule.Enabled = true
	_, saveErr := s.SaveRule(rule)
	store.err = nil
	if saveErr == nil || mediaTestState(s, 1).Rules[0].Enabled {
		t.Fatal("failed toggle must preserve the persisted pause", saveErr)
	}
	close(release)
	for ctx.Err() == nil {
		run, err := s.Snapshot(runID)
		if err == nil && run.Status == "completed" {
			if !run.Rule.Enabled || mediaTestState(s, 1).Rules[0].Enabled || run.ReadyCount != 0 || run.FailureCount != 0 {
				t.Fatal("pause changed the frozen task or counts", run)
			}
			data, err := os.ReadFile(filepath.Join(rule.Target, filepath.FromSlash(run.Entries[0].Relative)))
			if err != nil || string(data) != "http://unchanged/" {
				t.Fatal("in-flight work did not finish after pausing", err)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("paused monitoring interrupted in-flight work")
}

func TestMediaOrganizerMonitoringToggleWithUnavailableStorage(t *testing.T) {
	t.Run("missing local directory", func(t *testing.T) {
		s, _, _, rule := newMediaOrganizeTest(t)
		rule.Enabled = true
		if _, err := s.SaveRule(rule); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(rule.Source); err != nil { // Empty, isolated t.TempDir only.
			t.Fatal(err)
		}
		rule.Enabled = false
		if _, err := s.SaveRule(rule); err != nil || mediaTestState(s, 1).Rules[0].Enabled {
			t.Fatal("unavailable mounts must still allow pausing", err)
		}
		rule.Classify = !rule.Classify
		if _, err := s.SaveRule(rule); err == nil {
			t.Fatal("configuration changes must still validate storage")
		}
	})
	t.Run("removed primary account", func(t *testing.T) {
		s, drive, rule := newMediaCleanupCloud(t)
		rule.Enabled = true
		if _, err := s.SaveRule(rule); err != nil {
			t.Fatal(err)
		}
		a, err := s.accounts.Primary(rule.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.accounts.Delete(a.ID, a.Revision); err != nil {
			t.Fatal(err)
		}
		rule.Enabled = false
		if _, err := s.SaveRule(rule); err != nil || mediaTestState(s, 1).Rules[0].Enabled || len(drive.cookies) != 0 {
			t.Fatal("pausing must not require a primary account or drive request", err)
		}
		if _, err := s.files(context.Background(), rule); err == nil {
			t.Fatal("execution must still reject a removed account")
		}
	})
}

func TestMediaOrganizerRecognitionIsConservative(t *testing.T) {
	f := &mediaRecognitionFake{items: []tmdb.Item{{ID: 1, Kind: "movie", Title: "Different", Date: "2024-01-01"}}}
	detail, err := recognizeMedia(context.Background(), f, mediafacts.Identity{Name: "作品别名", Year: "2024", Season: 1})
	if err != nil || detail == nil {
		t.Fatal(detail, err)
	}
	f.items = []tmdb.Item{{ID: 1, Kind: "tv", Title: "Work", Date: "2024-01-01"}, {ID: 2, Kind: "movie", Title: "Work", Date: "2024-01-01"}}
	detail, err = recognizeMedia(context.Background(), f, mediafacts.Identity{Name: "Work", Year: "2024", Season: 1})
	if err != nil || detail != nil {
		t.Fatal("ambiguous names must need confirmation", detail, err)
	}
	f.err = context.DeadlineExceeded
	if _, err = recognizeMedia(context.Background(), f, mediafacts.Identity{Name: "Work"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestMediaOrganizerCloudRetryPaginationAndFrozenCredentials(t *testing.T) {
	manual, drive, accountID, _ := newOrganizeTest(t)
	f := &mediaRecognitionFake{}
	s, err := NewMediaOrganizer(&failingAccountStore{}, f, organizeDefaultTemplates{}, nil, manual.accounts, mediaOrganizeDirectoryFake{drive}, manual.ReserveDirectoryWork)
	if err != nil {
		t.Fatal(err)
	}
	rule := MediaOrganizeRule{Storage: "115", AccountID: accountID, SourceID: "10", TargetID: "20", Source: "/inbox", Target: "/library"}
	drive.dirs = map[string][]p115.ShareFile{"10": {{ID: "30", Name: "Work.S01E01.mkv", Size: 10}, {ID: "31", Name: "Work.S01E02.mkv", Size: 11}}, "20": {}}
	drive.pageSize = 1
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := files.scan(context.Background())
	if err != nil || len(entries) != 2 || drive.listCalls["10"] != 2 {
		t.Fatal(entries, err, drive.listCalls)
	}
	newCookie := "UID=123_snapshot; SEID=changed"
	if err := manual.accounts.Update(accountID, 2, "整理账号", &newCookie); err != nil {
		t.Fatal(err)
	}
	entry := entries[0]
	entry.Relative = "renamed.mkv"
	drive.moveErr = errors.New("temporary failure")
	if err = files.move(context.Background(), entry); err == nil {
		t.Fatal("expected failed move")
	}
	drive.moveErr = nil
	if err = files.move(context.Background(), entry); err != nil {
		t.Fatal("rename-then-move retry", err)
	}
	if err = files.move(context.Background(), entry); err != nil {
		t.Fatal("completed retry", err)
	}
	for _, cookie := range drive.cookies {
		if cookie != files.cookie {
			t.Fatal("credentials changed within work")
		}
	}
	if drive.renames != 1 || drive.moves != 2 {
		t.Fatal("replayed writes", drive.renames, drive.moves)
	}
	entry = entries[1]
	entry.Relative = "occupied.mkv"
	drive.dirs["20"] = append(drive.dirs["20"], p115.ShareFile{ID: "99", Name: "occupied.mkv", Size: 1})
	if err = files.move(context.Background(), entry); !errors.Is(err, errMediaTargetExists) {
		t.Fatal("cached directory missed concurrent conflict", err)
	}
	if drive.renames != 1 || drive.moves != 2 {
		t.Fatal("conflict produced a write")
	}
}

func TestMediaOrganizerPathGuardsAndCancellation(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	bad := rule
	bad.Target = rule.Source
	if _, err := s.SaveRule(bad); err == nil {
		t.Fatal("overlapping roots accepted")
	}
	if _, err := mediaLocalPath(rule.Source, "../outside", false); err == nil {
		t.Fatal("path escape accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	files, _ := s.files(context.Background(), rule)
	if _, err := files.scan(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("scan ignored cancellation", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(rule.Source, "link")); err == nil {
		if _, err := mediaLocalPath(rule.Source, "link/escape", true); err == nil {
			t.Fatal("followed symlink")
		}
	}
}

func TestMediaOrganizerOwnedWorker(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "http://unchanged/")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("worker leaked")
		}
	}()
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(status string) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-deadline:
				t.Fatal("worker did not reach", status)
			case <-tick.C:
				snapshot, _ := s.Snapshot(run.ID)
				if snapshot.Status == status {
					return
				}
			}
		}
	}
	wait("completed")
}

func TestMediaOrganizerCheckpointCopiesAllMutableFields(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	mediaTestFile(t, rule.Source, "Work.S01E01.1080p.mkv", "v")
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	run.Entries[0].File.NamingValues["resolution"] = "mutated"
	run.Entries[0].Status = "mutated"
	run.Groups[0].Title = "mutated"
	saved, _ := s.Snapshot(run.ID)
	if saved.Entries[0].Status == "mutated" || saved.Entries[0].File.NamingValues["resolution"] == "mutated" || saved.Groups[0].Title == "mutated" {
		t.Fatal("worker shares mutable state with readers")
	}
	saved.Status = "running"
	if err := s.checkpoint(&saved); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := restarted.Snapshot(run.ID)
	if snapshot.Status != "interrupted" {
		t.Fatal("restart should not silently continue writes")
	}
	if err := restarted.DeleteRule(rule.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Snapshot(run.ID); err == nil {
		t.Fatal("rule deletion retained obsolete history")
	}
}
