package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
)

func TestMediaOrganizerDoesNotAdoptUnrelatedNamedAttachments(t *testing.T) {
	s, metadata, _, rule := newMediaOrganizeTest(t)
	metadata.items = append(metadata.items, tmdb.Item{ID: 303272, Kind: "tv", Title: "魔法骑士雷阿斯", Date: "2026-01-01"})
	const video = "魔法骑士雷阿斯 (2026) - S01E04 - 1080p.Crunchyroll.WEB-DL.H264.AAC"
	const other = "Transformers.Rise.of.the.Beasts.2023.2160p.WEB-DL.DDP5.1.Atmos.HDR.H.265-APEX"
	for _, name := range []string{video + ".mkv", video + ".nfo", video + "-thumb.jpg", "poster.png"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	orphans := []string{other + ".nfo", other + ".xml", other + ".json", other + ".ass", video + ".Other.Movie-poster.jpg", video + "-other.nfo"}
	for _, suffix := range []string{"banner.jpg", "clearart.png", "discart.png", "fanart.jpg", "keyart.jpg", "landscape.jpg", "logo.png", "poster.jpg"} {
		orphans = append(orphans, other+"-"+suffix)
	}
	for _, name := range orphans {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, _ := s.Scan(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	for _, e := range run.Entries {
		if !slices.Contains(orphans, e.File.Name) {
			continue
		}
		if e.VideoID != "" || e.Target != "" || e.Status == "ready" {
			t.Fatal("unrelated metadata adopted the only remaining work", e)
		}
		if _, err := os.Stat(filepath.Join(rule.Source, e.File.Name)); err != nil {
			t.Fatal("preview deleted an orphan", e, err)
		}
	}
	run, _ = s.Scan(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	deleted := make(map[string]bool)
	for _, e := range run.Entries {
		if slices.Contains(orphans, e.File.Name) {
			if e.Status != "done" || e.Target != "" || e.VideoID != "" || !strings.Contains(e.Message, "已从来源删除") {
				t.Fatal("orphan must be deleted without a target-work association", e)
			}
			deleted[e.File.Name] = true
		} else if !e.File.Directory {
			body, err := os.ReadFile(filepath.Join(rule.Target, filepath.FromSlash(e.Relative)))
			if err != nil || e.Status != "done" || string(body) != e.File.Name {
				t.Fatal("valid video or companion did not move intact", e, err)
			}
		}
	}
	for _, name := range orphans {
		if _, err := os.Stat(filepath.Join(rule.Source, name)); !errors.Is(err, os.ErrNotExist) || !deleted[name] {
			t.Fatal("unowned sidecar remains or has no cleanup record", name, err)
		}
	}
	if err := filepath.WalkDir(rule.Target, func(full string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(entry.Name(), "Transformers") {
			t.Fatal("unrelated metadata reached the target library", full)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMediaOrganizerAttachmentsAndAutoCleanup(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	for _, name := range []string{"Work.S01E01.mkv", "Work.S01E01.nfo", "Work.S01E01-thumb.jpg", "tvshow.nfo", "poster.png", "unassigned/cover.jpg", "font.ttf", "audio.mka", "disc.iso", "readme.TXT", "subs.ZIP", "ads/note.rar"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, _ := s.Scan(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	if len(run.Entries) != 6 || len(s.State(1).History.Items) != 0 {
		t.Fatal("preview should include media companions, not unrelated files", run)
	}
	mediaTestProcess(t, s, &run, "execute")
	for _, e := range run.Entries {
		if e.File.SourcePath == "unassigned/cover.jpg" {
			if e.Status != "retained" || e.Result != nil {
				t.Fatal("unowned image must remain without an execution record", e)
			}
			continue
		}
		if e.Status != "done" {
			t.Fatal("companion did not follow video", e)
		}
		body, err := os.ReadFile(filepath.Join(rule.Target, filepath.FromSlash(e.Relative)))
		if err != nil || string(body) != e.File.Name {
			t.Fatal("companion content changed", e, err)
		}
		if strings.HasSuffix(e.File.Name, "-thumb.jpg") && !strings.HasSuffix(e.Relative, "-thumb.jpg") {
			t.Fatal("artwork suffix lost", e)
		}
		if e.File.Name == "tvshow.nfo" && strings.Contains(e.Relative, "Season 01") {
			t.Fatal("series NFO must be placed at series level", e)
		}
	}
	for _, name := range []string{"readme.TXT", "subs.ZIP", "ads/note.rar"} {
		if _, err := os.Stat(filepath.Join(rule.Source, filepath.FromSlash(name))); err != nil {
			t.Fatal("manual preview/execute deleted unrelated file", name, err)
		}
	}
	run, _ = s.Scan(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	if run.Status != "completed" || !strings.Contains(run.Message, "6 项") {
		t.Fatal("cleanup-only pass did not finish", run)
	}
	for _, name := range []string{"readme.TXT", "subs.ZIP", "ads/note.rar", "unassigned/cover.jpg"} {
		if _, err := os.Stat(filepath.Join(rule.Source, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("unrelated file was not removed", name, err)
		}
	}
	for _, name := range []string{"font.ttf", "audio.mka", "disc.iso"} {
		if _, err := os.Stat(filepath.Join(rule.Source, filepath.FromSlash(name))); err != nil {
			t.Fatal("retained media file removed", name, err)
		}
	}
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h := restarted.State(1).History; h.Total != 11 {
		t.Fatal("cleanup did not reuse durable per-file history", h)
	}
}

func TestMediaOrganizerRechecksAttachmentOwnershipBeforeMoving(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	mediaTestFile(t, rule.Source, "Work.S01E01.mkv", "video")
	mediaTestFile(t, rule.Source, "Other-poster.jpg", "unrelated artwork")
	run, _ := s.Scan(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	var video MediaOrganizeEntry
	for _, e := range run.Entries {
		if e.File.MediaType == "video" {
			video = e
		}
	}
	// Simulate a saved plan produced before the ownership boundary was fixed.
	for i := range run.Entries {
		e := &run.Entries[i]
		if e.File.MediaType == "attachment" {
			e.Group, e.VideoID, e.Status = video.Group, video.File.ID, "ready"
			e.Relative = path.Join(path.Dir(video.Relative), e.File.Name)
			e.Target = path.Join(rule.Target, e.Relative)
		}
	}
	mediaTestProcess(t, s, &run, "execute")
	for _, e := range run.Entries {
		if e.File.MediaType == "attachment" && (e.Status != "retained" || e.VideoID != "" || e.Target != "" || e.Result != nil) {
			t.Fatal("saved unsafe attachment association was executed", e)
		}
	}
	if body, err := os.ReadFile(filepath.Join(rule.Source, "Other-poster.jpg")); err != nil || string(body) != "unrelated artwork" {
		t.Fatal("execution moved an unrelated attachment", err)
	}
}

func TestMediaOrganizerSharedArtworkIncludesIncrementallySkippedVideos(t *testing.T) {
	s, metadata, _, rule := newMediaOrganizeTest(t)
	metadata.items = nil
	mediaTestFile(t, rule.Source, "Work.S01E01.mkv", "unmatched video")
	run, _ := s.Scan(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	metadata.items = []tmdb.Item{{ID: 43, Kind: "tv", Title: "Other", Date: "2024-01-01"}}
	mediaTestFile(t, rule.Source, "Other.S01E01.mkv", "new matched video")
	mediaTestFile(t, rule.Source, "poster.png", "ambiguous folder artwork")
	run, _ = s.Scan(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	for _, e := range run.Entries {
		if e.File.Name == "poster.png" && (e.Status != "retained" || e.VideoID != "" || e.Target != "") {
			t.Fatal("shared artwork ignored a present but skipped work", e)
		}
		if e.File.MediaType == "video" && e.Status != "done" {
			t.Fatal("retained artwork blocked the independent matched video", e)
		}
	}
	if _, err := os.Stat(filepath.Join(rule.Source, "poster.png")); err != nil {
		t.Fatal("mixed-folder artwork must remain for review", err)
	}
}

func TestMediaOrganizerCleanupScopeChangesAndCancellation(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	rule.Failure = t.TempDir()
	rule, _ = s.SaveRule(rule)
	for _, root := range []string{rule.Target, rule.Failure} {
		mediaTestFile(t, root, "keep.txt", "outside source")
	}
	for _, name := range []string{"changed.txt", "replaced.zip", "stable.7z"} {
		mediaTestFile(t, rule.Source, name, "old")
	}
	if err := os.WriteFile(filepath.Join(rule.Source, "upload.txt"), []byte("writing"), 0644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	mediaTestFile(t, outside, "outside.txt", "outside")
	_ = os.Symlink(outside, filepath.Join(rule.Source, "link")) // Some Windows users lack symlink permission.
	run, _ := s.Scan(rule.ID)
	f, _ := s.files(context.Background(), rule)
	if _, err := f.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rule.Source, "changed.txt"), []byte("new contents"), 0644); err != nil {
		t.Fatal(err)
	}
	// Preserve size and timestamp but replace file identity.
	replaced := filepath.Join(rule.Source, "replaced.zip")
	snapshot := f.localSnapshots["replaced.zip"]
	replacement := filepath.Join(outside, "replacement")
	if err := os.WriteFile(replacement, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, snapshot.ModTime(), snapshot.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(replaced); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, replaced); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.cleanup(ctx, f, &run); !errors.Is(err, context.Canceled) {
		t.Fatal("cleanup ignored cancellation", err)
	}
	store.err = errors.New("storage unavailable")
	if err := s.cleanup(context.Background(), f, &run); err == nil {
		t.Fatal("delete must wait for durable intent")
	}
	if _, err := os.Stat(filepath.Join(rule.Source, "stable.7z")); err != nil {
		t.Fatal("file deleted despite failed intent persistence", err)
	}
	store.err = nil
	run, _ = s.Snapshot(run.ID)
	if err := s.cleanup(context.Background(), f, &run); err != nil {
		t.Fatal(err)
	}
	for _, full := range []string{filepath.Join(rule.Source, "changed.txt"), replaced, filepath.Join(rule.Source, "upload.txt"), filepath.Join(outside, "outside.txt"), filepath.Join(rule.Target, "keep.txt"), filepath.Join(rule.Failure, "keep.txt")} {
		if _, err := os.Stat(full); err != nil {
			t.Fatal("changed, fresh or out-of-scope file removed", full, err)
		}
	}
	if _, err := os.Stat(filepath.Join(rule.Source, "stable.7z")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stable unrelated file not removed", err)
	}
}

type mediaCleanupDrive struct {
	*cleanupFakeDrive
	paths   map[string]string
	batches [][]string
}

func (d *mediaCleanupDrive) ReadDirectory(_ context.Context, cid, cookie string) (p115.DriveDirectory, error) {
	d.cookies = append(d.cookies, cookie)
	return p115.DriveDirectory{CID: cid, Path: d.paths[cid]}, nil
}

func (d *mediaCleanupDrive) DeleteEntries(ctx context.Context, ids []string, cookie string) error {
	d.batches = append(d.batches, slices.Clone(ids))
	return d.cleanupFakeDrive.DeleteEntries(ctx, ids, cookie)
}

func newMediaCleanupCloud(t *testing.T) (*MediaOrganizer, *mediaCleanupDrive, MediaOrganizeRule) {
	t.Helper()
	manual, fake, accountID, _ := newOrganizeTest(t)
	d := &mediaCleanupDrive{cleanupFakeDrive: &cleanupFakeDrive{organizeFakeDrive: fake}, paths: map[string]string{"10": "/inbox", "20": "/library", "40": "/failed"}}
	d.dirs = map[string][]p115.ShareFile{"10": {}, "20": {}, "40": {}}
	s, err := NewMediaOrganizer(&failingAccountStore{}, &mediaRecognitionFake{}, organizeDefaultTemplates{}, manual.accounts, d, manual.ReserveDirectoryWork)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.SaveRule(MediaOrganizeRule{Name: "模拟云端", Storage: "115", AccountID: accountID, SourceID: "10", Source: "/inbox", TargetID: "20", Target: "/library", IntervalMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	return s, d, rule
}

func TestMediaOrganizerCloudCleanupBatchingAndFrozenCredentials(t *testing.T) {
	s, d, rule := newMediaCleanupCloud(t)
	for i := range 205 {
		d.dirs["10"] = append(d.dirs["10"], p115.ShareFile{ID: fmt.Sprint(300 + i), Name: fmt.Sprintf("junk-%d.zip", i), Size: 3})
	}
	d.dirs["10"] = append(d.dirs["10"], p115.ShareFile{ID: "800", Name: "orphan.ass", Size: 3})
	d.dirs["20"] = []p115.ShareFile{{ID: "900", Name: "keep.zip"}}
	run, _ := s.Scan(rule.ID)
	f, _ := s.files(context.Background(), rule)
	if _, err := f.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	newCookie := "UID=123_snapshot; SEID=updated"
	if err := s.accounts.Update(rule.AccountID, 2, "整理账号", &newCookie); err != nil {
		t.Fatal(err)
	}
	d.beforeDelete = func() {
		snapshot, _ := s.Snapshot(run.ID)
		if len(snapshot.Entries) < len(d.deleted) || snapshot.Entries[len(d.deleted)-1].Result.Status != "error" {
			t.Fatal("delete intent was not persisted first")
		}
	}
	if err := s.cleanup(context.Background(), f, &run); err != nil {
		t.Fatal(err)
	}
	if len(d.batches) != 3 || len(d.batches[0]) != 100 || len(d.batches[2]) != 5 || len(d.deleted) != 205 || d.listCalls["10"] != 2 {
		t.Fatal("expected one rescan per parent and batched deletes", len(d.deleted), d.batches, d.listCalls)
	}
	if len(d.dirs["10"]) != 1 || d.dirs["10"][0].ID != "800" || len(d.dirs["20"]) != 1 {
		t.Fatal("cleanup escaped its exact unrelated file snapshots", d.dirs)
	}
	for _, cookie := range d.cookies {
		if cookie != f.cookie {
			t.Fatal("cleanup changed credentials during work")
		}
	}
}

func TestMediaOrganizerCloudCleanupStopsOnTimeoutAndChangedParent(t *testing.T) {
	for _, scenario := range []string{"timeout", "moved parent", "changed file"} {
		t.Run(scenario, func(t *testing.T) {
			s, d, rule := newMediaCleanupCloud(t)
			d.dirs["10"] = []p115.ShareFile{{ID: "500", Name: "junk.txt", Size: 3}, {ID: "30", Name: "nested", Directory: true}}
			d.paths["30"] = "/inbox/nested"
			d.dirs["30"] = []p115.ShareFile{{ID: "501", Name: "junk.zip", Size: 3}}
			run, _ := s.Scan(rule.ID)
			f, _ := s.files(context.Background(), rule)
			if _, err := f.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "timeout":
				d.deleteErr, d.applyOnError = context.DeadlineExceeded, true
			case "moved parent":
				d.paths["10"] = "/library/now-outside"
			case "changed file":
				d.dirs["10"][0].Name = "keep.ass"
			}
			err := s.cleanup(context.Background(), f, &run)
			if scenario == "changed file" {
				if err != nil || slices.Contains(d.deleted, "500") || !slices.Contains(d.deleted, "501") {
					t.Fatal("changed file should be skipped, not block other parents", d.deleted, err)
				}
				return
			}
			if err == nil || slices.Contains(d.deleted, "501") {
				t.Fatal("subsequent cleanup must stop on provider uncertainty", d.deleted, err)
			}
			if scenario == "timeout" && (len(run.Entries) != 1 || run.Entries[0].Status == "done") {
				t.Fatal("timed-out deletion incorrectly reported success", run)
			}
			if scenario == "moved parent" && len(d.deleted) != 0 {
				t.Fatal("moved source parent was deleted", d.deleted)
			}
		})
	}
}

func TestMediaOrganizerSharedArtworkAndFailureCompanions(t *testing.T) {
	t.Run("multiple seasons", func(t *testing.T) {
		s, _, _, rule := newMediaOrganizeTest(t)
		for _, name := range []string{"Show/Season 01/Work.S01E01.mkv", "Show/Season 02/Work.S02E01.mkv", "Show/tvshow.nfo", "Show/.actors/actor.jpg", "Show/Season 01/season.nfo", "unowned/other.nfo"} {
			mediaTestFile(t, rule.Source, name, name)
		}
		run, _ := s.Scan(rule.ID)
		mediaTestProcess(t, s, &run, "auto")
		if run.Status != "completed" {
			t.Fatal(run)
		}
		for _, e := range run.Entries {
			if e.File.MediaType == "video" || strings.HasPrefix(e.File.SourcePath, "unowned/") {
				continue
			}
			if e.Status != "done" {
				t.Fatal("shared series/season metadata did not move", e)
			}
			if strings.Contains(e.File.SourcePath, ".actors/") && !strings.Contains(e.Relative, ".actors/") {
				t.Fatal("actor directory structure lost", e)
			}
		}
	})
	t.Run("conflict quarantine", func(t *testing.T) {
		s, _, _, rule := newMediaOrganizeTest(t)
		rule.Failure = t.TempDir()
		rule, _ = s.SaveRule(rule)
		mediaTestFile(t, rule.Source, "Work.S01E01.mkv", "video")
		mediaTestFile(t, rule.Source, "Work.S01E01.nfo", "metadata")
		run, _ := s.Scan(rule.ID)
		mediaTestProcess(t, s, &run, "scan")
		for _, e := range run.Entries {
			if e.File.MediaType == "video" {
				mediaTestFile(t, rule.Target, e.Relative, "existing")
			}
		}
		mediaTestProcess(t, s, &run, "execute")
		for _, e := range run.Entries {
			if e.Failure == nil || e.Failure.State != "stored" {
				t.Fatal("metadata did not follow its conflicting video", e)
			}
			if _, err := os.Stat(filepath.Join(rule.Failure, e.File.SourcePath)); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestMediaOrganizerOrphanSubtitlesAndEmptyDirectories(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	rule.Failure = t.TempDir()
	rule, _ = s.SaveRule(rule)
	for _, name := range []string{"show/Work.S01E01.mkv", "show/Work.S01E01.ass", "subonly/orphan.ass", "subonly/orphan.sup"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	if err := os.MkdirAll(filepath.Join(rule.Source, "empty/a/b"), 0755); err != nil {
		t.Fatal(err)
	}
	run, _ := s.Scan(rule.ID)
	mediaTestProcess(t, s, &run, "scan")
	for _, name := range []string{"subonly/orphan.ass", "subonly/orphan.sup", "empty/a/b"} {
		if _, err := os.Stat(filepath.Join(rule.Source, name)); err != nil {
			t.Fatal("manual operations must not clean orphan subtitles or folders", name, err)
		}
	}
	run, _ = s.Scan(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	if run.Status != "partial" {
		t.Fatal("orphan subtitles must be reported as failed media", run)
	}
	for _, name := range []string{"subonly", "empty", "show"} {
		if _, err := os.Stat(filepath.Join(rule.Source, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("empty directory retained", name, err)
		}
	}
	for _, root := range []string{rule.Source, rule.Target, rule.Failure} {
		if _, err := os.Stat(root); err != nil {
			t.Fatal("configured root must remain", root, err)
		}
	}
	orphans := 0
	for _, item := range s.State(1).History.Items {
		if strings.Contains(item.Name, "orphan") {
			orphans++
			if item.Status != "error" || !strings.Contains(item.Message, "已从来源删除") {
				t.Fatal("orphan removal must retain failure reason and deletion outcome", item)
			}
		}
	}
	if orphans != 2 {
		t.Fatal("missing orphan subtitle records", orphans)
	}
}

func TestMediaOrganizerDoesNotDiscardSubtitlesOfUnprocessedVideos(t *testing.T) {
	for _, scenario := range []string{"settling", "incremental skip"} {
		t.Run(scenario, func(t *testing.T) {
			s, metadata, _, rule := newMediaOrganizeTest(t)
			mediaTestFile(t, rule.Source, "Work.S01E01.mkv", "video")
			for _, name := range []string{"Work.S01E01.ass", "Work.S01E01.nfo", "Work.S01E01-thumb.jpg", "poster.png"} {
				mediaTestFile(t, rule.Source, name, name)
			}
			if scenario == "settling" {
				now := time.Now()
				if err := os.Chtimes(filepath.Join(rule.Source, "Work.S01E01.mkv"), now, now); err != nil {
					t.Fatal(err)
				}
			} else {
				metadata.items = nil
			}
			for range 2 {
				run, _ := s.Scan(rule.ID)
				mediaTestProcess(t, s, &run, "auto")
				for _, name := range []string{"Work.S01E01.ass", "Work.S01E01.nfo", "Work.S01E01-thumb.jpg", "poster.png"} {
					if _, err := os.Stat(filepath.Join(rule.Source, name)); err != nil {
						t.Fatal("sidecar belongs to a present but unprocessed video", name, err)
					}
				}
			}
		})
	}
}

func TestMediaOrganizerCloudEmptyDirectoriesRecheck(t *testing.T) {
	for _, incoming := range []bool{false, true} {
		t.Run(fmt.Sprint(incoming), func(t *testing.T) {
			s, d, rule := newMediaCleanupCloud(t)
			d.dirs["10"] = []p115.ShareFile{{ID: "30", Name: "nested", Directory: true}}
			d.dirs["30"] = []p115.ShareFile{{ID: "31", Name: "leaf", Directory: true}}
			d.dirs["31"] = []p115.ShareFile{}
			d.paths["30"], d.paths["31"] = "/inbox/nested", "/inbox/nested/leaf"
			run, _ := s.Scan(rule.ID)
			f, _ := s.files(context.Background(), rule)
			if _, err := f.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			if incoming {
				d.beforeRead = func(cid string, _ int) error {
					if cid == "31" {
						d.dirs["31"] = []p115.ShareFile{{ID: "900", Name: "new.mkv", Size: 3}}
					}
					return nil
				}
			}
			if err := s.cleanup(context.Background(), f, &run); err != nil {
				t.Fatal(err)
			}
			if incoming {
				if len(d.deleted) != 0 || s.State(1).History.Total != 0 {
					t.Fatal("nonempty folder was deleted or falsely recorded", d.deleted)
				}
			} else if !slices.Equal(d.deleted, []string{"31", "30"}) || len(d.dirs["10"]) != 0 {
				t.Fatal("empty directories must be removed inside out, keeping root", d.deleted, d.dirs)
			}
		})
	}
}

func TestMediaOrganizerKeepsSidecarsWhenVideoArrivesAfterScan(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	names := []string{"Work.S01E01.ass", "Work.S01E01.nfo", "Work.S01E01-thumb.jpg", "poster.png"}
	for _, name := range names {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, _ := s.Scan(rule.ID)
	f, _ := s.files(context.Background(), rule)
	entries, err := f.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	run.Entries = entries
	mediaDiscardOrphanSidecars(f, &run, entries)
	if len(f.unrelated) != len(names) {
		t.Fatal("expected orphan sidecar candidates", f.unrelated)
	}
	mediaTestFile(t, rule.Source, "Work.S01E01.mkv", "new video")
	if err := s.cleanup(context.Background(), f, &run); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(rule.Source, name)); err != nil {
			t.Fatal("sidecar deleted despite a newly arrived video", name, err)
		}
	}
}

func TestMediaOrganizerCloudOrphanAttachments(t *testing.T) {
	s, d, rule := newMediaCleanupCloud(t)
	for i, name := range []string{"Other.nfo", "Other-banner.jpg", "Other.ass", "poster.png"} {
		d.dirs["10"] = append(d.dirs["10"], p115.ShareFile{ID: fmt.Sprint(i + 500), Name: name, Size: 3})
	}
	run, _ := s.Scan(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	if len(d.deleted) != 4 || len(d.batches) != 1 || d.listCalls["10"] != 2 {
		t.Fatal("orphan sidecars must share one recheck and deletion batch", d.deleted, d.batches, d.listCalls)
	}
	for _, e := range run.Entries {
		if e.Status != "done" || !strings.Contains(e.Message, "115 回收站") || e.Target != "" {
			t.Fatal("cloud orphan cleanup misreported its result", e)
		}
	}
	for _, scenario := range []string{"new video", "changed attachment"} {
		t.Run(scenario, func(t *testing.T) {
			s, d, rule := newMediaCleanupCloud(t)
			d.dirs["10"] = []p115.ShareFile{{ID: "500", Name: "Work.S01E01-thumb.jpg", Size: 3}}
			run, _ := s.Scan(rule.ID)
			f, _ := s.files(context.Background(), rule)
			entries, err := f.scan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			run.Entries = entries
			mediaDiscardOrphanSidecars(f, &run, entries)
			if scenario == "new video" {
				d.dirs["10"] = append(d.dirs["10"], p115.ShareFile{ID: "501", Name: "Work.S01E01.mkv", Size: 9})
			} else {
				d.dirs["10"][0].Name = "Work.S01E02-thumb.jpg"
			}
			if err := s.cleanup(context.Background(), f, &run); err != nil || len(d.deleted) != 0 {
				t.Fatal("newly owned or changed metadata was deleted", d.deleted, err)
			}
			if len(d.dirs["10"]) == 0 || !strings.HasSuffix(d.dirs["10"][0].Name, ".jpg") {
				t.Fatal("source metadata disappeared")
			}
		})
	}
}

func TestMediaOrganizerKeepsSharedArtworkWhenVideoArrivesAfterScan(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprint(nested), func(t *testing.T) {
			s, _, _, rule := newMediaOrganizeTest(t)
			for _, name := range []string{".actors/actor.jpg", "poster.png"} {
				mediaTestFile(t, rule.Source, name, name)
			}
			run, _ := s.Scan(rule.ID)
			f, _ := s.files(context.Background(), rule)
			entries, err := f.scan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			run.Entries = entries
			mediaDiscardOrphanSidecars(f, &run, entries)
			video := "Work.S01E01.mkv"
			if nested {
				video = "Season 01/" + video
			}
			mediaTestFile(t, rule.Source, video, "new video")
			if err := s.cleanup(context.Background(), f, &run); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{".actors/actor.jpg", "poster.png"} {
				if _, err := os.Stat(filepath.Join(rule.Source, name)); err != nil {
					t.Fatal("shared artwork lost its newly arrived work", name, err)
				}
			}
		})
	}
}

func TestMediaOrganizerCloudArtworkScopeRecheck(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, scenario := range []string{"orphan", "new video", "moved scope"} {
			t.Run(fmt.Sprintf("nested=%t/%s", nested, scenario), func(t *testing.T) {
				s, d, rule := newMediaCleanupCloud(t)
				scopeID, scopePath, actorID := "10", "/inbox", "30"
				if nested {
					scopeID, scopePath, actorID = "30", "/inbox/show", "31"
					d.dirs["10"] = []p115.ShareFile{{ID: scopeID, Name: "show", Directory: true}}
					d.paths[scopeID] = scopePath
				}
				d.dirs[scopeID] = []p115.ShareFile{{ID: actorID, Name: ".actors", Directory: true}}
				d.paths[actorID] = scopePath + "/.actors"
				d.dirs[actorID] = []p115.ShareFile{{ID: "500", Name: "actor.jpg", Size: 3}, {ID: "501", Name: "other.png", Size: 4}}
				run, _ := s.Scan(rule.ID)
				f, _ := s.files(context.Background(), rule)
				entries, err := f.scan(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				run.Entries = entries
				mediaDiscardOrphanSidecars(f, &run, entries)
				if scenario == "new video" {
					d.dirs[scopeID] = append(d.dirs[scopeID], p115.ShareFile{ID: "900", Name: "Work.S01E01.mkv", Size: 9})
				} else if scenario == "moved scope" {
					d.paths[scopeID] = "/library/no-longer-source"
				}
				err = s.cleanup(context.Background(), f, &run)
				if scenario == "moved scope" {
					if err == nil || len(d.deleted) != 0 {
						t.Fatal("changed artwork scope must stop cleanup", d.deleted, err)
					}
				} else if err != nil {
					t.Fatal(err)
				} else if scenario == "orphan" {
					if !slices.Contains(d.deleted, "500") || !slices.Contains(d.deleted, "501") || slices.Contains(d.deleted, "10") {
						t.Fatal("orphan actor artwork was not cleaned within source", d.deleted)
					}
				} else if len(d.deleted) != 0 || d.listCalls[scopeID] != 2 || d.listCalls[actorID] != 2 {
					t.Fatal("shared scope must be rechecked once for all images", d.deleted, d.listCalls)
				}
			})
		}
	}
}

func TestMediaOrganizerRescanReplacesUnusedPreviews(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	mediaTestFile(t, rule.Source, "Work.S01E01.mkv", "video")
	for range 4 {
		run, err := s.Scan(rule.ID)
		if err != nil {
			t.Fatal(err)
		}
		mediaTestProcess(t, s, &run, "scan")
		if len(s.doc.Runs) != 1 || s.doc.Runs[0].ID != run.ID || s.State(1).History.Total != 0 {
			t.Fatal("rescanning must replace an unexecuted preview, not accumulate it", s.doc.Runs)
		}
	}
}
