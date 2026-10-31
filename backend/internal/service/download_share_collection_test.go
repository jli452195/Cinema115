package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

type downloadCollectionFixture struct {
	s       *DownloadOrganize
	drive   *organizeFakeDrive
	tasks   *ShareTasks
	db      *storage.Store
	client  *downloadShareFake
	jobs    []OrganizeJob
	tracker *gardenTrackerFake
}

type downloadCollectionClient struct{ *downloadShareFake }

func (f *downloadCollectionClient) ReadMetadata(context.Context, *p115.ShareInfo, string, string) ([]byte, error) {
	return []byte(strings.Repeat("s", 30)), nil
}

func newDownloadCollectionFixture(t *testing.T, count int) *downloadCollectionFixture {
	t.Helper()
	s, drive, input, _ := downloadBatchFixture(t, count)
	_, tasks, db, _, _ := downloadShareFixture(t)
	client := &downloadShareFake{cleanupFakeDrive: &cleanupFakeDrive{organizeFakeDrive: drive}}
	withMetadata := &downloadCollectionClient{client}
	s.Organize.drive = client.cleanupFakeDrive
	tasks.OwnClient, tasks.AccountCookie = withMetadata, s.Organize.accounts.CookieFor
	tasks.Settings.PrimaryID, tasks.Settings.ValidateAccount = s.Organize.accounts.PrimaryID, s.Organize.accounts.ValidatePrimary
	settings := tasks.Settings.ShareValue()
	settings.AccountID = s.Organize.accounts.PrimaryID()
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	tasks.Generator = &STRMGenerator{Client: withMetadata, Root: tasks.Settings.root}
	s.Shares = tasks
	input.ShareSTRM = true
	preview, err := s.PreviewBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := s.StartBatch(preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	tracker := &gardenTrackerFake{}
	s.Reader = tracker
	return &downloadCollectionFixture{s, drive, tasks, db, client, accepted.Jobs, tracker}
}

func (f *downloadCollectionFixture) ready(t *testing.T, index int) {
	t.Helper()
	p := f.s.live[f.jobs[index].ID]
	parent, video, subtitle := fmt.Sprint(20000+index), fmt.Sprint(30000+index), fmt.Sprint(40000+index)
	f.tracker.resultID = parent
	f.tracker.beforeRead = func() {
		f.drive.dirs[p.job.Download.DirectoryID] = []p115.ShareFile{{ID: parent, Name: "same", Directory: true}}
		f.drive.dirs[parent] = []p115.ShareFile{{ID: video, Name: "Show [01].mkv", Size: 1200}, {ID: subtitle, Name: "Show [01].zh-CN.ass", Size: 30}}
	}
	if done, err := f.s.advance(context.Background(), p); err != nil || !done || !waitingDownloadShare(p.job) {
		t.Fatal("download did not enter local collection", done, err, p.job)
	}
	delete(f.s.live, p.job.ID) // runPending's normal completion boundary.
}

func (f *downloadCollectionFixture) leader(t *testing.T) *downloadPending {
	t.Helper()
	for _, p := range f.s.live {
		if p.job.Download.Share.LeaderID == p.job.ID {
			return p
		}
	}
	t.Fatal("no sealed collection")
	return nil
}

func (f *downloadCollectionFixture) finish(t *testing.T, p *downloadPending) {
	t.Helper()
	if done, err := f.s.advance(context.Background(), p); err != nil || !done || p.job.Status != "completed" || !p.job.Download.Share.HandedOff {
		t.Fatal("sealed collection did not organize and hand off", done, err, p.job)
	}
	delete(f.s.live, p.job.ID)
	if err := f.tasks.retryDue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if task, err := f.db.GetShareTask(context.Background(), p.job.ID); err != nil || task.Status != "success" {
		t.Fatal("share generation failed", task, err)
	}
}

func TestDownloadShareCollectionCombinesReadyFilesAndSubtitles(t *testing.T) {
	f := newDownloadCollectionFixture(t, 3)
	for i := range f.jobs {
		f.ready(t, i)
	}
	if f.drive.moves != 0 || f.drive.renames != 0 {
		t.Fatal("collection moved files before sealing")
	}
	if err := f.s.sealShareCollections(time.Now()); err != nil {
		t.Fatal(err)
	}
	p := f.leader(t)
	if len(p.job.Items) != 6 || len(f.s.live) != 1 {
		t.Fatal("members did not merge", len(p.job.Items), len(f.s.live))
	}
	f.finish(t, p)
	history := taskHistory(t, f.db)
	if len(history) != 1 || history[0].Status != "success" || history[0].Written != 3 || f.client.sends != 1 || f.client.configures != 1 || f.tracker.calls != 3 {
		t.Fatal("per-magnet sharing or repeated offline reads", history, f.client.sends, f.tracker.calls)
	}
	for _, job := range f.s.History() {
		if job.Status != "completed" || !job.Download.Share.HandedOff || job.Download.Share.LeaderID != p.job.ID {
			t.Fatal("member lost batch outcome", job)
		}
	}
	if err := f.s.sealShareCollections(time.Now().Add(time.Hour)); err != nil || len(f.s.live) != 0 {
		t.Fatal("sealed batch replayed", err)
	}
}

func TestDownloadShareCollectionDeadMagnetDoesNotHoldReadyOrLateFiles(t *testing.T) {
	f := newDownloadCollectionFixture(t, 4)
	f.ready(t, 0)
	f.ready(t, 1)
	now := time.Now()
	if err := f.s.sealShareCollections(now.Add(29 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(f.s.live) != 2 {
		t.Fatal("collection closed before quiet window")
	}
	if err := f.s.sealShareCollections(now.Add(31 * time.Second)); err != nil {
		t.Fatal(err)
	}
	first := f.leader(t)
	f.finish(t, first)
	if f.client.sends != 1 || len(first.job.Items) != 4 || len(f.s.live) != 2 {
		t.Fatal("slow magnet held completed files")
	}
	f.ready(t, 2)
	if err := f.s.sealShareCollections(time.Now().Add(31 * time.Second)); err != nil {
		t.Fatal(err)
	}
	second := f.leader(t)
	if second.job.Download.Share.Directory == first.job.Download.Share.Directory || len(second.job.Items) != 2 {
		t.Fatal("late resource modified frozen directory")
	}
	f.finish(t, second)
	if f.client.sends != 2 || len(taskHistory(t, f.db)) != 2 || len(f.s.live) != 1 || f.tracker.calls != 3 {
		t.Fatal("late resource did not form an independent supplement")
	}
}

func TestDownloadShareCollectionMaximumWindowAndAtomicSeal(t *testing.T) {
	f := newDownloadCollectionFixture(t, 4)
	for i := 0; i < 3; i++ {
		f.ready(t, i)
	}
	now := time.Now()
	for i := range f.s.Organize.doc.Jobs {
		job := &f.s.Organize.doc.Jobs[i]
		if waitingDownloadShare(*job) {
			job.Download.Share.ReadyAt = now.Add(-10 * time.Second)
		}
	}
	f.s.Organize.doc.Jobs[3].Download.Share.ReadyAt = now.Add(-2 * time.Minute)
	store := f.s.Organize.store.(*failingAccountStore)
	before := store.data
	store.err = errors.New("disk full")
	if err := f.s.sealShareCollections(now); err == nil || store.data != before || len(f.s.live) != 1 {
		t.Fatal("failed seal partially accepted")
	}
	for _, job := range f.s.History() {
		if job.Download.Share.LeaderID != "" {
			t.Fatal("failed seal mutated source plan")
		}
	}
	store.err = nil
	if err := f.s.sealShareCollections(now); err != nil {
		t.Fatal(err)
	}
	if len(f.leader(t).job.Items) != 6 {
		t.Fatal("continuous arrivals extended hard deadline")
	}
}

func TestDownloadShareCollectionRestartAndUnknownMoveResumeOriginalBatch(t *testing.T) {
	f := newDownloadCollectionFixture(t, 2)
	f.ready(t, 0)
	f.ready(t, 1)
	if err := f.s.sealShareCollections(time.Now()); err != nil {
		t.Fatal(err)
	}
	p := f.leader(t)
	f.drive.moveErr = errors.New("timeout")
	f.drive.moveApplyIDs = map[string]bool{"30001": true}
	if done, err := f.s.advance(context.Background(), p); err != nil || !done || p.job.Status != "partial" {
		t.Fatal("lost move did not stop safely", done, err, p.job.Status)
	}
	if len(taskHistory(t, f.db)) != 0 || f.client.sends != 0 {
		t.Fatal("partial organization was shared")
	}
	for i := 0; i < 35; i++ {
		f.s.Organize.doc.Jobs = prependOrganizeJob(OrganizeJob{ID: fmt.Sprint(i), Status: "blocked", Download: &ResourceDownload{Share: &DownloadShare{}}}, f.s.Organize.doc.Jobs)
	}
	if err := f.s.Organize.saveLocked(f.s.Organize.doc); err != nil {
		t.Fatal(err)
	}
	engine, err := NewOrganizeEngine(f.s.Organize.store, f.s.Organize.accounts, f.s.Organize.drive, f.s.Organize.metadata, f.s.Organize.templates)
	if err != nil {
		t.Fatal(err)
	}
	f.s = NewDownloadOrganize(engine, f.s.Resources, f.s.Downloads, f.s.Settings, f.tracker)
	f.s.Shares = f.tasks
	resumed, err := f.s.Resume(f.jobs[0].ID) // Any member resumes the same leader.
	if err != nil || resumed.ID != p.job.ID {
		t.Fatal("member could not recover original batch", resumed.ID, err)
	}
	f.drive.moveErr, f.drive.moveApplyIDs = nil, nil
	f.finish(t, f.s.live[resumed.ID])
	if f.client.sends != 1 || f.tracker.calls != 2 {
		t.Fatal("restart resubmitted download or split sharing")
	}
}

func TestDownloadShareCollectionPreviewRejectsCrossMagnetDuplicateNames(t *testing.T) {
	s, _, input, _ := downloadBatchFixture(t, 2)
	_, tasks, _, _, _ := downloadShareFixture(t)
	tasks.Settings.PrimaryID, tasks.Settings.ValidateAccount = s.Organize.accounts.PrimaryID, s.Organize.accounts.ValidatePrimary
	s.Shares, input.ShareSTRM = tasks, true
	input.Resources[1].Files[0].Episode = 1
	preview, err := s.PreviewBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Jobs[0].Items[0].Conflict, "同名") {
		t.Fatal("shared wrapper hid duplicate targets")
	}
	if _, err := s.StartBatch(preview.ID); err == nil {
		t.Fatal("conflicting share batch was accepted")
	}
}

func TestDownloadShareCollectionReadyFilesSurviveRestartAndHistoryPruning(t *testing.T) {
	f := newDownloadCollectionFixture(t, 2)
	f.ready(t, 0)
	f.ready(t, 1)
	engine := f.s.Organize
	for i := 0; i < 35; i++ {
		engine.doc.Jobs = prependOrganizeJob(OrganizeJob{ID: fmt.Sprint(i), Status: "completed", Download: &ResourceDownload{}}, engine.doc.Jobs)
	}
	if err := engine.saveLocked(engine.doc); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewOrganizeEngine(engine.store, engine.accounts, engine.drive, engine.metadata, engine.templates)
	if err != nil {
		t.Fatal(err)
	}
	f.s = NewDownloadOrganize(restarted, f.s.Resources, f.s.Downloads, f.s.Settings, f.tracker)
	f.s.Shares = f.tasks
	if err := f.s.sealShareCollections(time.Now()); err != nil {
		t.Fatal(err)
	}
	p := f.leader(t)
	if len(p.job.Items) != 4 {
		t.Fatal("history trimming lost collected files")
	}
	f.finish(t, p)
	if f.tracker.calls != 2 {
		t.Fatal("restart rechecked offline tasks")
	}
}

func TestDownloadShareCollectionRejectsChangedAccountWithoutWrites(t *testing.T) {
	f := newDownloadCollectionFixture(t, 2)
	f.ready(t, 0)
	f.ready(t, 1)
	account, err := f.s.Organize.account(f.jobs[0].Config.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=456_other; SEID=changed-account"
	if err := f.s.Organize.accounts.Update(account.ID, account.Revision, "changed", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := f.s.sealShareCollections(time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(f.s.live) != 0 || f.drive.moves != 0 || f.client.sends != 0 {
		t.Fatal("collection rebound credentials")
	}
	for _, job := range f.s.History() {
		if job.Status != "interrupted" {
			t.Fatal("changed account did not stop original collection")
		}
	}
}

func TestDownloadShareCollectionAcceptsSameAccountRefresh(t *testing.T) {
	f := newDownloadCollectionFixture(t, 2)
	f.ready(t, 0)
	f.ready(t, 1)
	account, err := f.s.Organize.account(f.jobs[0].Config.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_new_device; SEID=refreshed-secret"
	if err := f.s.Organize.accounts.Update(account.ID, account.Revision, "renamed account", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := f.s.sealShareCollections(time.Now()); err != nil {
		t.Fatal(err)
	}
	p := f.leader(t)
	if p.account.ID != account.ID || p.account.Cookie != cookie {
		t.Fatal("collection lost its original account or retained stale credentials")
	}
	f.finish(t, p)
	if f.client.sends != 1 || len(taskHistory(t, f.db)) != 1 {
		t.Fatal("same-account refresh split or blocked the original collection")
	}
}

func TestDownloadShareCollectionCleanupRecyclesMemberRootsAndKeepsOtherDownloads(t *testing.T) {
	f := newDownloadCollectionFixture(t, 2)
	f.ready(t, 0)
	f.ready(t, 1)
	f.drive.dirs["20000"] = append(f.drive.dirs["20000"], p115.ShareFile{ID: "50000", Name: "unselected.mkv", Size: 10})
	parent := f.jobs[0].Download.ParentCID
	roots := []string{}
	for _, job := range f.s.History() {
		roots = append(roots, job.Download.DirectoryID)
	}
	reads := f.drive.listCalls[parent]
	if err := f.s.sealShareCollections(time.Now()); err != nil {
		t.Fatal(err)
	}
	f.finish(t, f.leader(t))
	if f.drive.listCalls[parent] != reads || slices.Contains(f.client.deleted, parent) || slices.Contains(f.client.deleted, "20000") || slices.Contains(f.client.deleted, "50000") {
		t.Fatal("collection added scans or cleaned the common parent", f.client.deleted)
	}
	for _, root := range roots {
		if !slices.Contains(f.client.deleted, root) {
			t.Fatal("member staging root was not recycled", f.client.deleted)
		}
	}
	if _, exists := f.drive.dirs["20000"]; exists {
		t.Fatal("unselected files survived collection cleanup")
	}
}

func TestDownloadShareCollectionHandoffRecoveryDoesNotReadRecycledSources(t *testing.T) {
	f := newDownloadCollectionFixture(t, 2)
	f.ready(t, 0)
	f.ready(t, 1)
	if err := f.s.sealShareCollections(time.Now()); err != nil {
		t.Fatal(err)
	}
	p := f.leader(t)
	f.s.Shares = nil
	if done, err := f.s.advance(context.Background(), p); err != nil || !done || p.job.Status != "interrupted" {
		t.Fatal("handoff failure lost the original collection", done, err, p.job.Status)
	}
	delete(f.s.live, p.job.ID)
	f.s.Shares = f.tasks
	resumed, err := f.s.Resume(f.jobs[0].ID)
	if err != nil || resumed.ID != p.job.ID {
		t.Fatal("member could not resume handoff", resumed.ID, err)
	}
	reads, moves := f.drive.listCalls[f.jobs[0].Download.ParentCID], f.drive.moves
	f.finish(t, f.s.live[resumed.ID])
	if f.drive.moves != moves || f.drive.listCalls[f.jobs[0].Download.ParentCID] != reads || f.tracker.calls != 2 || f.client.sends != 1 {
		t.Fatal("handoff recovery replayed collection or download")
	}
}

func TestDownloadShareCollectionRunAutomaticallySealsAndJoinsWorker(t *testing.T) {
	f := newDownloadCollectionFixture(t, 2)
	f.ready(t, 0)
	f.ready(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("collection worker outlived cancellation")
		}
	})
	for deadline := time.Now().Add(5 * time.Second); ; {
		jobs := f.s.History()
		if len(jobs) == 2 && jobs[0].Download.Share.HandedOff && jobs[1].Download.Share.HandedOff {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ready collection required manual action", jobs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
