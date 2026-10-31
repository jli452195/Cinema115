package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/storage"
)

type animeOrganizeDrive struct {
	t                              *testing.T
	db                             *sql.DB
	jobID                          string
	entries                        map[string][]model.AnimeDriveEntry
	reads, creates, renames, moves int
	lost                           string
	partial                        bool
	readErr                        error
	renameErr                      error
	hook                           func(string)
}

func (d *animeOrganizeDrive) intent(kind, cookie string) {
	d.t.Helper()
	if cookie != animeDownloadTestCookie {
		d.t.Fatal("credentials were replaced")
	}
	var current string
	if err := d.db.QueryRow(`SELECT json_extract(result_json,'$.step.kind') FROM anime_drive_operations WHERE download_job_id=? AND kind='organize' AND state='dispatched' AND authorized_at IS NOT NULL`, d.jobID).Scan(&current); err != nil || current != kind {
		d.t.Fatal("external write preceded durable permission", current, err)
	}
	if d.hook != nil {
		d.hook(kind)
	}
}
func (d *animeOrganizeDrive) CreateDirectory(_ context.Context, parent, name, cookie string) (string, error) {
	d.intent("mkdir", cookie)
	d.creates++
	cid := fmt.Sprint(900 + d.creates)
	d.entries[parent] = append(d.entries[parent], model.AnimeDriveEntry{ID: cid, ParentCID: parent, Kind: "directory", Name: name})
	d.entries[cid] = []model.AnimeDriveEntry{}
	if d.lost == "mkdir" {
		return "", context.DeadlineExceeded
	}
	return cid, nil
}
func (d *animeOrganizeDrive) ReadAnimeDrivePage(_ context.Context, cid string, offset int, cookie string) (p115.AnimeDrivePage, error) {
	d.reads++
	if d.readErr != nil {
		return p115.AnimeDrivePage{}, d.readErr
	}
	if offset != 0 || cookie != animeDownloadTestCookie {
		d.t.Fatal("unexpected read scope")
	}
	entries, ok := d.entries[cid]
	if !ok {
		return p115.AnimeDrivePage{}, errors.New("missing directory")
	}
	return p115.AnimeDrivePage{CID: cid, Count: len(entries), Entries: append([]model.AnimeDriveEntry{}, entries...)}, nil
}
func (d *animeOrganizeDrive) RenameAnimeEntries(_ context.Context, names []p115.RenameTarget, cookie string) error {
	d.intent("rename", cookie)
	d.renames++
	for _, n := range names {
		for cid, entries := range d.entries {
			for i, e := range entries {
				if e.ID == n.ID {
					d.entries[cid][i].Name = n.Name
				}
			}
		}
	}
	if d.lost == "rename" {
		return context.DeadlineExceeded
	}
	return d.renameErr
}
func (d *animeOrganizeDrive) MoveAnimeEntries(_ context.Context, ids []string, cid, cookie string) error {
	d.intent("move", cookie)
	d.moves++
	for index, id := range ids {
		if d.partial && index > 0 {
			continue
		}
		for parent, entries := range d.entries {
			if parent == cid {
				continue
			}
			for i, e := range entries {
				if e.ID == id {
					e.ParentCID = cid
					d.entries[cid] = append(d.entries[cid], e)
					d.entries[parent] = append(entries[:i], entries[i+1:]...)
					break
				}
			}
		}
	}
	if d.lost == "move" || d.partial {
		return context.DeadlineExceeded
	}
	return nil
}

func animeOrganizeFixture(t *testing.T) (*animeDownloadFixture, *sql.DB, *AnimeDownloadSession, model.AnimeDownloadJob, *animeOrganizeDrive) {
	t.Helper()
	f, _, job := animeEnabledStage(t)
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`UPDATE anime_download_jobs SET directory_cid='200' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.ResumeDownloadTarget(context.Background(), job.Snapshot.Target)
	if err != nil {
		t.Fatal(err)
	}
	submit, err := f.service.Store.PrepareAnimeSubmit(context.Background(), job.ID, job.Generation, session.Target())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.DispatchAnimeSubmit(context.Background(), submit.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.RecordAnimeSubmitOutcome(context.Background(), submit.ID, "confirmed", "", model.AnimeSubmitObservation{SchemaVersion: 1, Kind: "matched", TargetCID: "200", ResultID: "300"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_download_jobs SET download_state='completed',result_object_id='300' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job.Snapshot.Naming = &model.AnimeNamingPlan{SchemaVersion: 1, Target: model.AnimeNamingTarget{TMDBID: "335790", Title: "黑暗召唤师在地下恋爱中", Year: "2026", Season: 1, Fingerprint: strings.Repeat("a", 64), Seasons: []model.AnimeNamingSeason{{Number: 1, Episodes: []model.AnimeNamingEpisode{{ID: "1001", Number: 1}}}}}, Sources: []model.AnimeSourceMapping{{SourceRef: job.Snapshot.Source.SourceID, Segments: []model.AnimeMappingSegment{{First: 1, SourceBase: 1, TargetSeason: 1, TargetBase: 1}}}}}
	target := job.Snapshot.Target
	target.ParentCID, target.ParentPath = "900", "/整理"
	template, _ := naming.Default(naming.TV)
	job.Snapshot.Organization = &model.AnimeOrganizationSnapshot{Naming: naming.Snapshot{Template: template}, SchemaVersion: 1, PolicyVersion: 1, PlanRevision: 1, Target: target}
	raw, _ := json.Marshal(job.Snapshot)
	if _, err := db.Exec(`UPDATE anime_download_jobs SET snapshot_json=? WHERE id=?`, string(raw), job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	entries := []model.AnimeDriveEntry{animeFileRoot(job), {ID: "300", ParentCID: "200", Name: "release", Kind: "directory"}, {ID: "400", ParentCID: "300", Name: "01.mkv", Kind: "file", Size: 1000}, {ID: "401", ParentCID: "300", Name: "01.chs.ass", Kind: "file", Size: 10}, {ID: "402", ParentCID: "300", Name: "readme.txt", Kind: "file", Size: 20}}
	manifest := model.AnimeFileManifest{SchemaVersion: 1, UID: job.UID, ResultID: "300", CollectedAt: time.Now().UnixMilli(), Entries: entries}
	if err := f.service.Store.SaveAnimeFiles(context.Background(), job, manifest); err != nil {
		t.Fatal(err)
	}
	job, err = f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	d := &animeOrganizeDrive{t: t, db: db, jobID: job.ID, entries: map[string][]model.AnimeDriveEntry{"900": {}, "300": append([]model.AnimeDriveEntry{}, entries[2:]...)}}
	f.service.FileGateway, f.service.OrganizationGateway = d, d
	f.service.SubmitGateway = &animeSubmitGatewayStub{inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
		t.Error("organizer inspected offline task")
		return p115.AnimeTaskObservation{}, nil
	}, submit: func(context.Context, string, string, string) error {
		t.Error("organizer submitted offline task")
		return nil
	}}
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		t.Error("organizer polled offline task")
		return model.AnimeOfflineStatus{}, nil
	})
	return f, db, session, job, d
}

func organizeTestCall(t *testing.T, f *animeDownloadFixture, session *AnimeDownloadSession, id string) model.AnimeOrganizeOperation {
	t.Helper()
	if err := f.service.OrganizeDownloadedFiles(context.Background(), session, id); err != nil {
		t.Fatal(err)
	}
	op, err := f.service.Store.GetAnimeOrganization(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestAnimeOrganizeMinimumRequestsAndNoTrackingAfterCompletion(t *testing.T) {
	f, _, session, job, d := animeOrganizeFixture(t)
	op := organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "done" || d.reads != 1 || d.creates != 2 || d.renames != 1 || d.moves != 1 {
		t.Fatal("expected root list, two creates, one rename and one move, without readbacks", op.Progress, d)
	}
	if len(d.entries["300"]) != 1 || d.entries["300"][0].ID != "402" {
		t.Fatal("attachments moved or originals copied", d.entries)
	}
	v, err := f.service.JobView(context.Background(), job.ID)
	if err != nil || v.WorkflowState != "organized" || v.Organization.Moved != 2 || v.PermittedActions.Reconcile || v.PermittedActions.Retry {
		t.Fatal(v, err)
	}
	if path.Ext(d.entries["902"][0].Name) != ".mkv" || !strings.Contains(d.entries["902"][0].Name, "S01E01") {
		t.Fatal(d.entries)
	}
	// The user may reorganize/delete the result later. No repair or rescan.
	delete(d.entries, "902")
	r := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.Now().Add(24 * time.Hour) }}
	for i := 0; i < 3; i++ {
		if err := r.PollOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := f.service.OrganizeDownloadedFiles(context.Background(), session, job.ID); err != nil {
			t.Fatal(err)
		}
	}
	if d.reads != 1 || d.moves != 1 || d.renames != 1 {
		t.Fatal("completed work revisited", d)
	}
}

func TestAnimeOrganizeUnknownOnlyRecoversFixedStep(t *testing.T) {
	for _, kind := range []string{"mkdir", "rename", "move"} {
		t.Run(kind, func(t *testing.T) {
			f, _, session, job, d := animeOrganizeFixture(t)
			d.lost = kind
			op := organizeTestCall(t, f, session, job.ID)
			if op.Progress.State != "unknown" || op.Progress.Step == nil || op.Progress.Step.Kind != kind {
				t.Fatal(op)
			}
			writes := d.creates + d.renames + d.moves
			reads := d.reads
			// A fresh service cache simulates restart. The persisted object IDs and
			// exact step, not current settings, are the recovery source.
			f.service.organizationCache = animeOrganizationCache{}
			d.lost = ""
			op = organizeTestCall(t, f, session, job.ID)
			if d.creates+d.renames+d.moves != writes || d.reads != reads+1 || op.Progress.Step != nil {
				t.Fatal("recovery replayed a write or scanned beyond the saved parent", op.Progress, d)
			}
			for i := 0; i < 2 && op.Progress.State != "done"; i++ {
				op = organizeTestCall(t, f, session, job.ID)
			}
			if op.Progress.State != "done" || d.creates != 2 || d.renames != 1 || d.moves != 1 {
				t.Fatal("saved steps were repeated", op.Progress, d)
			}
		})
	}
}

func TestAnimeOrganizeRetainsSafeReceiptIssueAndClearsAfterConfirmation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"state":true,"data":{"secret-upstream-content":false}}`)
	}))
	defer server.Close()
	client := p115.NewShareClient()
	client.WebURL = server.URL
	receiptErr := client.RenameAnimeEntries(context.Background(), []p115.RenameTarget{{ID: "400", Name: "episode.mkv"}}, "test-cookie")
	f, _, session, job, drive := animeOrganizeFixture(t)
	drive.renameErr = receiptErr
	op := organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "unknown" || op.Progress.ReceiptIssue != "data_object" || drive.renames != 1 || drive.moves != 0 {
		t.Fatal("safe receipt diagnostic lost", op.Progress)
	}
	var raw string
	if err := drive.db.QueryRow(`SELECT result_json FROM anime_drive_operations WHERE download_job_id=? AND kind='organize'`, job.ID).Scan(&raw); err != nil || strings.Contains(raw, "secret-upstream-content") || strings.Contains(raw, "test-cookie") {
		t.Fatal("upstream content persisted", err)
	}
	op = organizeTestCall(t, f, session, job.ID)
	if op.Progress.Step != nil || op.Progress.ReceiptIssue != "" || drive.renames != 1 || drive.moves != 0 {
		t.Fatal("confirmed recovery retained stale failure or replayed rename", op.Progress)
	}
}

func TestAnimeOrganizePartialMoveKeepsSuccessAndNeverReplays(t *testing.T) {
	f, _, session, job, d := animeOrganizeFixture(t)
	d.partial = true
	organizeTestCall(t, f, session, job.ID)
	op := organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "unknown" || op.Progress.RecoveryChecks != 1 || !op.Progress.Files[0].Moved || d.moves != 1 {
		t.Fatal("early partial result lost progress or replayed a write", op.Progress, d)
	}
	op = organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "needs_review" || op.Progress.Code != "ORGANIZE_RESULT_UNRESOLVED" || !op.Progress.Files[0].Moved || op.Progress.Files[1].Moved {
		t.Fatal(op.Progress)
	}
	for i := 0; i < 2; i++ {
		organizeTestCall(t, f, session, job.ID)
	}
	if d.moves != 1 || d.renames != 1 {
		t.Fatal("partial batch replayed", d)
	}
	v, err := f.service.JobView(context.Background(), job.ID)
	if err != nil || !v.PermittedActions.Reconcile || v.PermittedActions.Retry {
		t.Fatal("partial result exposed replay or lost read recovery", v, err)
	}
	if _, err := f.service.JobAction(context.Background(), "admin", "organize_partial_read", job.ID, "reconcile", AnimeControlInput{ExpectedRevision: v.Revision}); err != nil {
		t.Fatal(err)
	}
	current, err := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil || current.FileState != "verified" {
		t.Fatal("reconcile erased the saved manifest", current, err)
	}
	r := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.Now().Add(time.Minute) }}
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.moves != 1 || d.renames != 1 {
		t.Fatal("scheduled reconcile replayed a write", d)
	}
	var raw string
	if err := d.db.QueryRow(`SELECT request_json||result_json FROM anime_drive_operations WHERE kind='organize'`).Scan(&raw); err != nil || strings.Contains(raw, "UID=") || strings.Contains(strings.ToLower(raw), "sha1") {
		t.Fatal("unsafe or excessive snapshot", err)
	}
}

func TestAnimeOrganizeExistingVideoSkipsAndMissingSubtitleContinues(t *testing.T) {
	f, _, session, job, d := animeOrganizeFixture(t)
	d.entries["900"] = []model.AnimeDriveEntry{{ID: "901", ParentCID: "900", Kind: "directory", Name: "黑暗召唤师在地下恋爱中 (2026) {tmdb-335790}"}}
	d.entries["901"] = []model.AnimeDriveEntry{{ID: "902", ParentCID: "901", Kind: "directory", Name: "Season 01"}}
	d.entries["902"] = []model.AnimeDriveEntry{{ID: "999", ParentCID: "902", Kind: "file", Name: "黑暗召唤师在地下恋爱中.S01E01.1080p-" + job.Snapshot.Source.GroupName + ".mkv", Size: 99}}
	op := organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "done" || op.Progress.Code != "" || op.Progress.Files[0].ExistingID != "999" || op.Progress.Files[0].Moved || !op.Progress.Files[1].Moved || d.renames != 1 || d.moves != 1 || d.creates != 0 || d.reads != 3 {
		t.Fatal(op.Progress, d)
	}
	if d.entries["902"][0].ID != "999" || d.entries["902"][0].Size != 99 {
		t.Fatal("existing file was replaced", d.entries["902"])
	}
}

func TestAnimeOrganizePauseKeepsReceiptAndStopsNewWrites(t *testing.T) {
	f, _, session, job, d := animeOrganizeFixture(t)
	d.hook = func(kind string) {
		if kind == "rename" {
			serviceControlAnime(t, f.service, job.Snapshot.Source.SubscriptionID, "pause")
		}
	}
	err := f.service.OrganizeDownloadedFiles(context.Background(), session, job.ID)
	if !errors.Is(err, storage.ErrAnimeWriteUnauthorized) || d.moves != 0 || d.renames != 1 {
		t.Fatal("pause failed to stop next write", err, d)
	}
	op, e := f.service.Store.GetAnimeOrganization(context.Background(), job.ID)
	if e != nil || !op.Progress.Files[0].Renamed || op.Progress.Step != nil {
		t.Fatal("lost accepted rename receipt", op, e)
	}
	serviceControlAnime(t, f.service, job.Snapshot.Source.SubscriptionID, "resume")
	d.hook = nil
	op = organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "done" || d.renames != 1 || d.moves != 1 {
		t.Fatal(op.Progress, d)
	}
}

func TestAnimeOrganizeRunnerUsesExistingLeaseAndFinishes(t *testing.T) {
	f, _, _, job, d := animeOrganizeFixture(t)
	r := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.Now().Add(time.Minute) }}
	if err := r.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, err := f.service.JobView(context.Background(), job.ID)
	if err != nil || v.WorkflowState != "organized" || d.moves != 1 {
		t.Fatal(v, err, d)
	}
}

func TestAnimeOrganizeSharesManualDirectoryReservation(t *testing.T) {
	f, _, session, job, d := animeOrganizeFixture(t)
	f.service.ReserveOrganization = func() (func(), error) { return nil, ErrOrganizeConflict }
	if err := f.service.OrganizeDownloadedFiles(context.Background(), session, job.ID); !errors.Is(err, ErrCloudTaskBusy) {
		t.Fatal(err)
	}
	if _, err := f.service.Store.GetAnimeOrganization(context.Background(), job.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("reserved writer created an operation", err)
	}
	if d.reads+d.creates+d.renames+d.moves != 0 {
		t.Fatal("busy directory was accessed", d)
	}
}

func TestAnimeOrganizeBatchesShareOneTargetListing(t *testing.T) {
	f, db, session, job, d := animeOrganizeFixture(t)
	n := job.Snapshot.Naming
	n.Target.Seasons[0].Episodes = make([]model.AnimeNamingEpisode, 0, 60)
	entries := append([]model.AnimeDriveEntry{}, job.Files.Entries[:2]...)
	for i := 1; i <= 60; i++ {
		n.Target.Seasons[0].Episodes = append(n.Target.Seasons[0].Episodes, model.AnimeNamingEpisode{ID: fmt.Sprint(1000 + i), Number: i})
		entries = append(entries, model.AnimeDriveEntry{ID: fmt.Sprint(400 + i), ParentCID: "300", Name: fmt.Sprintf("[组] 作品 - %02d.mkv", i), Kind: "file", Size: 100})
	}
	manifest := *job.Files
	manifest.Entries = entries
	snapshotRaw, _ := json.Marshal(job.Snapshot)
	filesRaw, _ := json.Marshal(manifest)
	if _, err := db.Exec(`UPDATE anime_download_jobs SET snapshot_json=?,files_json=? WHERE id=?`, string(snapshotRaw), string(filesRaw), job.ID); err != nil {
		t.Fatal(err)
	}
	d.entries["300"] = append([]model.AnimeDriveEntry{}, entries[2:]...)
	op := organizeTestCall(t, f, session, job.ID)
	if op.Progress.State != "done" || len(op.Progress.Files) != 60 || d.reads != 1 || d.renames != 2 || d.moves != 2 {
		t.Fatal("per-file reads or batching regression", op.Progress.State, d)
	}
}

func TestAnimeOrganizeLargeCollectionYieldsToAnotherRSS(t *testing.T) {
	f, db, _, job, drive := animeOrganizeCollection(t, 60) // 180 video/subtitle files.
	cleanup := animeEnableCleanup(f, drive)
	ctx := context.Background()
	source, err := f.service.Store.GetAnimeSource(ctx, job.Snapshot.Source.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second).UnixMilli()
	addDiscoveryServiceBackup(t, db, source, now)
	if _, err := db.Exec(`UPDATE anime_subscription_sources SET provider='mikan',item_id='2259',binding_json=json_remove(binding_json,'$.review') WHERE id='standby-source'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now+3600000, source.ID); err != nil {
		t.Fatal(err)
	}
	discovery := &AnimeDiscoveryRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }, Feeds: animeRSSFunc(func(_ context.Context, _ *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		page := discoveryServicePage(in, now, 1)
		page.Releases[0].Title = "[字幕组] 另一来源 - 02 [1080p]"
		return page, nil
	})}
	f.service.Torrents = animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
		return []byte("d4:infod6:lengthi1e4:name1:y12:piece lengthi1e6:pieces20:" + strings.Repeat("p", 20) + "ee"), nil
	})
	if err := discovery.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := discovery.VerifyOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var otherID string
	if err := db.QueryRow(`SELECT job_id FROM anime_job_selections WHERE source_id='standby-source'`).Scan(&otherID); err != nil {
		var state string
		_ = db.QueryRow(`SELECT COALESCE(error_code,'') || ':' || COALESCE((SELECT group_concat(disposition || ':' || COALESCE(reason_code,'')) FROM anime_release_decisions WHERE source_id=s.id),'no decisions') FROM anime_subscription_sources s WHERE id='standby-source'`).Scan(&state)
		t.Fatal("other RSS did not create its intent", state, err)
	}
	// The large job goes first. Its due time is advanced after a bounded turn,
	// so the other source's old queued intent wins the next admission.
	if _, err := db.Exec(`UPDATE anime_download_jobs SET next_run_at=CASE WHEN id=? THEN NULL ELSE ? END`, job.ID, now-1); err != nil {
		t.Fatal(err)
	}
	creates, submits, statuses := 0, 0, 0
	f.service.FileGateway = &animeFileGatewayStub{page: drive.ReadAnimeDrivePage, create: func(ctx context.Context, parent, name, cookie string) (string, error) {
		if name == "anime-"+otherID {
			creates++
			return "500", nil
		}
		return drive.CreateDirectory(ctx, parent, name, cookie)
	}}
	f.service.SubmitGateway = &animeSubmitGatewayStub{
		inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
			return p115.AnimeTaskObservation{}, nil
		},
		submit: func(context.Context, string, string, string) error { submits++; return nil },
	}
	other, err := f.service.Store.GetAnimeDownloadJob(ctx, otherID)
	if err != nil {
		t.Fatal(err)
	}
	f.service.StatusGateway = animeStatusReaderFunc(func(_ context.Context, _, hash string) (model.AnimeOfflineStatus, error) {
		if hash != other.Snapshot.InfoHash {
			t.Error("organization reread its finished offline task")
		}
		statuses++
		status := serviceAnimeStatus(other, "running", 50)
		status.ResultID = "501"
		return status, nil
	})
	runner := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }}
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	op, err := f.service.Store.GetAnimeOrganization(ctx, job.ID)
	if err != nil || op.Progress.State != "working" || op.Progress.Step != nil || drive.renames != 4 || drive.moves != 0 || cleanup.deletes != 0 || submits != 0 {
		t.Fatal("large organization did not yield at a confirmed boundary", op.Progress, err, drive.renames, drive.moves, submits)
	}
	now += 5001 // Preserve the existing UID admission interval.
	if err := runner.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	other, err = f.service.Store.GetAnimeDownloadJob(ctx, otherID)
	if err != nil || other.DownloadState != "downloading" || creates != 1 || submits != 1 || statuses != 1 || drive.moves != 0 {
		t.Fatal("other RSS stayed behind the collection", other.DownloadState, err, creates, submits, statuses, drive.moves)
	}
	for turn := 0; turn < 4 && op.Progress.State != "done"; turn++ {
		now += 5001
		if err := runner.PollOnce(ctx); err != nil {
			t.Fatal(err)
		}
		op, err = f.service.Store.GetAnimeOrganization(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err != nil || op.Progress.State != "done" || drive.renames != 4 || drive.moves != 4 || cleanup.deletes != 1 || drive.reads != 2 {
		t.Fatal("continuation repeated writes or per-file reads", op.Progress.State, err, drive.renames, drive.moves, cleanup.deletes, drive.reads)
	}
}

func TestAnimeOrganizeYieldSurvivesRestartAndUnknownReceipt(t *testing.T) {
	f, db, _, job, drive := animeOrganizeCollection(t, 60)
	cleanup := animeEnableCleanup(f, drive)
	now := time.Now().Add(time.Second).UnixMilli()
	runner := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }}
	if err := runner.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if drive.renames != 4 || drive.moves != 0 {
		t.Fatal("collection did not stop after four confirmed batches", drive.renames, drive.moves)
	}
	reopenAnimeRecoveryFixture(t, f, db)
	var err error
	drive.db, err = sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = drive.db.Close() })
	f.service.organizationCache = animeOrganizationCache{}
	drive.lost = "move"
	now += 5001
	if err := runner.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := f.service.Store.GetAnimeOrganization(context.Background(), job.ID)
	if err != nil || op.Progress.State != "unknown" || drive.renames != 4 || drive.moves != 1 || cleanup.deletes != 0 {
		t.Fatal("restart replayed confirmed batches or lost the uncertain move", op.Progress.State, err, drive.renames, drive.moves)
	}
	drive.lost = ""
	now += 5001
	if err := runner.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err = f.service.Store.GetAnimeOrganization(context.Background(), job.ID)
	if err != nil || op.Progress.State != "done" || drive.renames != 4 || drive.moves != 4 || cleanup.deletes != 1 {
		t.Fatal("recovery replayed the uncertain move or lost remaining files", op.Progress.State, err, drive.renames, drive.moves, cleanup.deletes)
	}
}

func TestAnimeOrganizeSlowPreflightStillMakesProgressBeforeYield(t *testing.T) {
	f, _, session, job, drive := animeOrganizeFixture(t)
	op, err := f.service.Store.PrepareAnimeOrganization(context.Background(), job, "")
	if err != nil {
		t.Fatal(err)
	}
	run := animeOrganizationRun{service: f.service, session: session, job: job, op: op,
		listings: make(map[string][]model.AnimeDriveEntry), started: time.Now().Add(-time.Minute), limit: 4}
	if err := run.run(context.Background()); !errors.Is(err, errAnimeOrganizationYield) {
		t.Fatal("slow turn did not yield", err)
	}
	if drive.creates != 1 || len(run.op.Progress.Directories) != 1 || run.op.Progress.Step != nil {
		t.Fatal("slow preflight caused a turn with no progress or lost its receipt", drive.creates, run.op.Progress)
	}
}

func TestAnimeOrganizeReadFailureCooldownAndCredentialDenial(t *testing.T) {
	for _, status := range []int{429, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f, _, session, job, d := animeOrganizeFixture(t)
			d.readErr = &p115.HTTPError{Status: status, RetryAfter: time.Hour}
			op := organizeTestCall(t, f, session, job.ID)
			got, err := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status == 429 && (op.Progress.Code != "TASK_RATE_LIMITED" || got.NextRunAt == nil || *got.NextRunAt < time.Now().Add(59*time.Minute).UnixMilli()) {
				t.Fatal("Retry-After lost", op.Progress, got.NextRunAt)
			}
			if status == 403 && (op.Progress.State != "working" || op.Progress.Code != "TASK_ACCESS_DENIED" || got.NextRunAt == nil || *got.NextRunAt < time.Now().Add(4*time.Minute).UnixMilli()) {
				t.Fatal("credential denial lost its recovery cooldown", op.Progress)
			}
			r := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.Now().Add(time.Minute) }}
			if err := r.PollOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if d.reads != 1 || d.creates+d.renames+d.moves != 0 {
				t.Fatal("blocked account was accessed again", d)
			}
		})
	}
}

func TestAnimeOrganizeAccountChangeAfterReceiptStopsNextWrite(t *testing.T) {
	f, _, session, job, d := animeOrganizeFixture(t)
	d.hook = func(kind string) {
		if kind == "rename" {
			a, err := f.accounts.Primary("")
			if err != nil {
				t.Fatal(err)
			}
			cookie := "UID=999_A; SEID=changed"
			if err := f.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.service.OrganizeDownloadedFiles(context.Background(), session, job.ID); !errors.Is(err, storage.ErrAnimeAccountChanged) {
		t.Fatal(err)
	}
	op, err := f.service.Store.GetAnimeOrganization(context.Background(), job.ID)
	if err != nil || !op.Progress.Files[0].Renamed || d.moves != 0 || d.renames != 1 {
		t.Fatal("new credential replaced task identity", op.Progress, err, d)
	}
}

func TestAnimeOrganizeSameUIDRefreshKeepsPinnedCredentials(t *testing.T) {
	f, _, session, job, drive := animeOrganizeFixture(t)
	refreshed := false
	drive.hook = func(kind string) {
		if kind != "rename" || refreshed {
			return
		}
		refreshed = true
		account, err := f.accounts.Primary(job.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		cookie := "UID=123_B; SEID=new-login"
		if err := f.accounts.Update(account.ID, account.Revision, "new display name", &cookie); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.service.OrganizeDownloadedFiles(context.Background(), session, job.ID); err != nil {
		t.Fatal(err)
	}
	got, err := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil || !refreshed || got.WorkflowState != "organized" || drive.moves == 0 || session.cookie != animeDownloadTestCookie {
		t.Fatal("same-account login stopped organization or changed its credential", got.WorkflowState, err)
	}
}
