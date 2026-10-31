package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type downloadCleanupFixture struct {
	s       *DownloadOrganize
	drive   *cleanupFakeDrive
	adder   *cloudAdderStub
	tracker *gardenTrackerFake
	p       *downloadPending
}

func newDownloadCleanupFixture(t *testing.T) downloadCleanupFixture {
	t.Helper()
	s, drive, adder, tracker, _, input := gardenFixture(t)
	cleanup := &cleanupFakeDrive{organizeFakeDrive: drive}
	s.Organize.drive = cleanup
	preview, err := s.Preview(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Start(preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := s.live[job.ID]
	tracker.resultID = "801"
	tracker.beforeRead = func() {
		drive.dirs[p.job.Download.DirectoryID] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
		drive.dirs["801"] = []p115.ShareFile{
			{ID: "901", Name: "[Group] Show [01].mkv", Size: 1200},
			{ID: "902", Name: "[Group] Show [02].mkv", Size: 1300},
			{ID: "903", Name: "unselected.ass", Size: 30},
			{ID: "802", Name: "extras", Directory: true},
		}
		drive.dirs["802"] = []p115.ShareFile{{ID: "904", Name: "subtitles.zip", Size: 40}, {ID: "905", Name: "readme.txt", Size: 10}}
	}
	return downloadCleanupFixture{s, cleanup, adder, tracker, p}
}

func (f downloadCleanupFixture) finish(t *testing.T) {
	t.Helper()
	if done, err := f.s.advance(context.Background(), f.p); err != nil || !done || f.p.job.Status != "completed" {
		t.Fatal("download organization did not finish", done, err, f.p.job.Status)
	}
	delete(f.s.live, f.p.job.ID)
}

func TestDownloadCleanupRecyclesOnlyTaskRootIncludingUnselectedFiles(t *testing.T) {
	f := newDownloadCleanupFixture(t)
	f.finish(t)
	root := f.p.job.Download.DirectoryID
	if !slices.Equal(f.drive.deleted, []string{root}) || len(f.p.job.Download.CleanupDirs) != 0 {
		t.Fatal("cleanup did not finish with one root deletion", f.drive.deleted, f.p.job.Download.CleanupDirs)
	}
	for _, cid := range []string{root, "801", "802"} {
		if _, exists := f.drive.dirs[cid]; exists {
			t.Fatal("task leftovers survived recursive recycling", cid)
		}
	}
	item := f.p.job.Items[0]
	if item.Status != "done" || !slicesContainFile(f.drive.dirs[item.TargetCID], "901") || !slicesContainFile(f.drive.dirs["10"], "30") {
		t.Fatal("cleanup affected organized media or another source directory")
	}
	// The binding walk is the only read of the staging root and extras.
	if f.drive.listCalls[root] != 1 || f.drive.listCalls["802"] != 1 || f.drive.listCalls["10"] != 1 {
		t.Fatal("cleanup added empty-directory scans", f.drive.listCalls)
	}
	if len(f.s.History()) != 1 || f.s.History()[0].Resumable {
		t.Fatal("completed cleanup remained resumable", f.s.History())
	}
}

func TestDownloadCleanupRetainsFailedSelection(t *testing.T) {
	f := newDownloadCleanupFixture(t)
	f.drive.moveErr = errors.New("move unavailable")
	f.drive.beforeRead = func(cid string, _ int) error {
		if f.drive.moves > 0 && cid == f.p.job.Items[0].TargetCID {
			return errors.New("target temporarily unavailable")
		}
		return nil
	}
	if done, err := f.s.advance(context.Background(), f.p); err != nil || !done || f.p.job.Status != "partial" {
		t.Fatal("failed move lost its recovery state", done, err, f.p.job.Status)
	}
	if len(f.drive.deleted) != 0 || !slicesContainFile(f.drive.dirs["801"], "901") || len(f.p.job.Download.CleanupDirs) != 1 {
		t.Fatal("failed selection caused source deletion", f.drive.deleted, f.p.job.Download.CleanupDirs)
	}
}

func TestDownloadCleanupFailureRestartsRefreshesAndResumesOnlyCleanup(t *testing.T) {
	f := newDownloadCleanupFixture(t)
	f.drive.deleteErr = errors.New("delete unavailable")
	f.finish(t)
	root, parent := f.p.job.Download.DirectoryID, f.p.job.Download.ParentCID
	if !slices.Equal(f.p.job.Download.CleanupDirs, []string{root}) || !strings.Contains(f.p.job.Message, "清理") || !f.s.History()[0].Resumable {
		t.Fatal("cleanup failure was hidden or lost", f.p.job.Message, f.s.History())
	}
	// API/history callers cannot mutate the persistent pending roots.
	history := f.s.History()
	history[0].Download.CleanupDirs[0] = parent
	if f.s.History()[0].Download.CleanupDirs[0] != root {
		t.Fatal("history exposes mutable cleanup state")
	}
	engine, err := NewOrganizeEngine(f.s.Organize.store, f.s.Organize.accounts, f.drive, f.s.Organize.metadata, f.s.Organize.templates)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewDownloadOrganize(engine, f.s.Resources, f.s.Downloads, f.s.Settings, f.tracker)
	if !restarted.History()[0].Resumable {
		t.Fatal("restart lost cleanup recovery")
	}
	f.drive.deleteErr = nil
	f.adder.url = ""
	account, err := engine.accounts.Primary(f.p.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_refreshed; SEID=cleanup-refresh"
	if err := engine.accounts.Update(account.ID, account.Revision, "renamed account", &cookie); err != nil {
		t.Fatal(err)
	}
	reads, creates, renames, moves, polls := 0, f.drive.creates, f.drive.renames, f.drive.moves, f.tracker.calls
	f.drive.beforeRead = func(cid string, _ int) error {
		reads++
		return errors.New("cleanup recovery must not scan source or target " + cid)
	}
	if _, err := restarted.Resume(f.p.job.ID); err != nil {
		t.Fatal(err)
	}
	p := restarted.live[f.p.job.ID]
	if p == nil || p.account.Cookie != cookie {
		t.Fatal("cleanup recovery did not use the original account's current credentials")
	}
	if done, err := restarted.advance(context.Background(), p); err != nil || !done || p.job.Status != "completed" || len(p.job.Download.CleanupDirs) != 0 {
		t.Fatal("cleanup recovery did not finish", done, err, p.job)
	}
	if reads != 0 || f.drive.creates != creates || f.drive.renames != renames || f.drive.moves != moves || f.tracker.calls != polls || f.adder.url != "" {
		t.Fatal("cleanup recovery replayed download or organization", reads, f.drive.creates, f.drive.renames, f.drive.moves, f.tracker.calls)
	}
	if !slices.Equal(f.drive.deleted, []string{root, root}) {
		t.Fatal("cleanup recovery escaped the original root", f.drive.deleted)
	}
}

func TestDownloadCleanupAcceptsLostDeleteReceiptAfterParentCheck(t *testing.T) {
	f := newDownloadCleanupFixture(t)
	f.drive.deleteErr = errors.New("delete response lost")
	f.drive.applyOnError = true
	f.finish(t)
	if len(f.p.job.Download.CleanupDirs) != 0 || f.s.History()[0].Resumable || len(f.drive.deleted) != 1 {
		t.Fatal("confirmed recycling kept retrying deletion", f.p.job.Download.CleanupDirs, f.drive.deleted)
	}
	if f.drive.listCalls[f.p.job.Download.ParentCID] != 2 {
		t.Fatal("lost receipt did not use one scoped parent check", f.drive.listCalls)
	}
}

func TestDownloadCleanupRecoveryRejectsChangedAccount(t *testing.T) {
	f := newDownloadCleanupFixture(t)
	f.drive.deleteErr = errors.New("delete unavailable")
	f.finish(t)
	account, err := f.s.Organize.accounts.Get(f.p.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=456_changed; SEID=changed-credential"
	if err := f.s.Organize.accounts.Update(account.ID, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	deletes := len(f.drive.deleted)
	if _, err := f.s.Resume(f.p.job.ID); err == nil || len(f.s.live) != 0 || len(f.drive.deleted) != deletes {
		t.Fatal("changed account was allowed to resume cleanup", err, f.drive.deleted)
	}
}
