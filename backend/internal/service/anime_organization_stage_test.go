package service

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

// Only the download resource directory is created before an organization
// journal exists. All organization writes use the durable-intent assertions
// of animeOrganizeDrive. No HTTP client or live cloud account is involved.
type animeOrganizationStageDrive struct {
	*animeOrganizeDrive
	downloadCreates int
	moveRejected    bool
}

func (d *animeOrganizationStageDrive) CreateDirectory(ctx context.Context, parent, name, cookie string) (string, error) {
	if parent == "100" {
		if cookie != animeDownloadTestCookie || !strings.HasPrefix(name, "anime-") {
			d.t.Fatal("wrong download directory or credential", parent, name)
		}
		d.downloadCreates++
		return "200", nil
	}
	return d.animeOrganizeDrive.CreateDirectory(ctx, parent, name, cookie)
}

func (d *animeOrganizationStageDrive) MoveAnimeEntries(ctx context.Context, ids []string, cid, cookie string) error {
	if d.moveRejected {
		d.intent("move", cookie)
		d.moves++
		return context.DeadlineExceeded
	}
	return d.animeOrganizeDrive.MoveAnimeEntries(ctx, ids, cid, cookie)
}

func TestAnimeOrganizationStageDownloadToArchiveAndRestart(t *testing.T) {
	for _, failure := range []string{"none", "mkdir", "rename", "move", "move_without_effect"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			f, _, job := animeEnabledStageWithConfig(t, func(f *animeDownloadFixture, c *model.AnimeSubscriptionConfig, row *animefeed.SourceRelease) {
				settings, err := f.service.SaveOrganizeDirectory(ctx, "admin", "stage_organize_directory", AnimeOrganizeDirectoryInput{ExpectedRevision: f.settings.Revision, CID: bindingPtr("900")})
				if err != nil {
					t.Fatal(err)
				}
				f.settings = settings
				c.Sources[0].Binding.Scope = &model.AnimeScope{Kind: "range", First: bindingPtr(25), Last: bindingPtr(36)}
				c.Sources[0].Rules.MinimumEpisode = bindingPtr(30)
				c.Naming = &model.AnimeNamingPlan{SchemaVersion: 1, Target: model.AnimeNamingTarget{TMDBID: "335790", Title: "黑暗召唤师在地下恋爱中", Year: "2026", Season: 2, Fingerprint: strings.Repeat("a", 64), Seasons: []model.AnimeNamingSeason{{Number: 2, Episodes: []model.AnimeNamingEpisode{{ID: "1006", Number: 6}}}}}, Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{First: 25, SourceBase: 25, TargetSeason: 2, TargetBase: 1}}}}}
				row.Title = "[字幕组] 番剧 - 30 [1080p]"
			})
			if job.Snapshot.Organization == nil || job.Snapshot.Naming == nil || job.Snapshot.Release.Parse.Episode == nil || *job.Snapshot.Release.Parse.Episode != 30 {
				t.Fatal("configuration did not reach the claimed task", job.Snapshot)
			}
			db, err := sql.Open("sqlite", f.path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			d := &animeOrganizationStageDrive{animeOrganizeDrive: &animeOrganizeDrive{t: t, db: db, jobID: job.ID, entries: map[string][]model.AnimeDriveEntry{
				"900": {},
				"200": {{ID: "400", ParentCID: "200", Kind: "file", Name: "30.MKV", Size: 1000}, {ID: "401", ParentCID: "200", Kind: "file", Name: "30.zh-Hans.forced.ASS", Size: 10}, {ID: "402", ParentCID: "200", Kind: "file", Name: "readme.txt", Size: 20}},
			}}, moveRejected: failure == "move_without_effect"}
			if failure != "none" && !d.moveRejected {
				d.lost = failure
			}
			inspects, submits, statuses := 0, 0, 0
			f.service.Directories = animeDownloadDirectoryFunc(func(context.Context, string, string) (p115.DriveDirectory, error) {
				t.Fatal("execution added a directory metadata request")
				return p115.DriveDirectory{}, nil
			})
			f.service.FileGateway, f.service.OrganizationGateway = d, d
			f.service.SubmitGateway = &animeSubmitGatewayStub{inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
				inspects++
				return p115.AnimeTaskObservation{}, nil
			}, submit: func(_ context.Context, cookie, _ string, cid string) error {
				submits++
				if cid != "200" || cookie != animeDownloadTestCookie {
					t.Fatal("submission did not retain directory 1")
				}
				return nil
			}}
			f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
				statuses++
				v := serviceAnimeStatus(job, "finished", 100)
				v.TargetCID, v.ResultID = "200", "200"
				return v, nil
			})
			now := time.Now().Add(time.Second).UnixMilli()
			runner := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }}
			tick := func() model.AnimeDownloadJob {
				t.Helper()
				if err := runner.PollOnce(ctx); err != nil {
					t.Fatal(err)
				}
				current, err := f.service.DownloadJob(ctx, job.ID)
				if err != nil {
					t.Fatal(err)
				}
				now += int64(3 * time.Minute / time.Millisecond)
				if current.NextRunAt != nil && now <= *current.NextRunAt {
					now = *current.NextRunAt + 1
				}
				return current
			}
			current := tick() // Durable stages now advance in one pass, without sleeps.
			if current.FileState != "verified" || statuses != 1 || submits != 1 || inspects != 1 {
				t.Fatal("download did not advance with one completion read", current, statuses, submits, inspects)
			}
			if failure == "none" && current.WorkflowState != "organized" {
				t.Fatal("successful stages still required another scheduler pass", current)
			}
			if failure != "none" {
				op, err := f.service.Store.GetAnimeOrganization(ctx, job.ID)
				if err != nil || op.Progress.State != "unknown" || op.Progress.Step == nil {
					t.Fatal("lost receipt was not retained", op, err)
				}
				reads := d.reads
				// Close the real SQLite store and rebuild the service/accounts/gates;
				// recovery must not depend on a live session or directory cache.
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				if err := f.service.Store.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := storage.Open(ctx, f.path)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				probe, err := sql.Open("sqlite", f.path)
				if err != nil {
					t.Fatal(err)
				}
				defer probe.Close()
				d.db = probe
				accounts, err := OpenP115AccountsSQLite(ctx, reopened, f.dir, "test-secret")
				if err != nil {
					t.Fatal(err)
				}
				old := f.service
				f.service = &AnimeAdminService{Store: reopened, Accounts: accounts, Directories: old.Directories, FileGateway: d, OrganizationGateway: d, SubmitGateway: old.SubmitGateway, StatusGateway: old.StatusGateway, DownloadCoordinator: &CloudDownloadCoordinator{}}
				runner = &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return time.UnixMilli(now) }}
				d.lost = ""
				current = tick()
				if d.creates > 2 || d.renames > 1 || d.moves > 1 || d.reads <= reads {
					t.Fatal("restart repeated a write or failed to reconcile the saved step", d)
				}
				if !d.moveRejected && current.WorkflowState != "organized" {
					t.Fatal("confirmed recovery did not continue immediately", current)
				}
			}
			if d.moveRejected {
				tick() // One extra delayed read allows for temporarily invisible results.
				view, err := f.service.JobView(ctx, job.ID)
				if err != nil || view.Organization.State != "needs_review" || view.Organization.Renamed != 2 || view.Organization.Moved != 0 || view.PermittedActions.Retry {
					t.Fatal("failed move lost successful rename or exposed replay", view, err)
				}
				reads := d.reads
				d.moveRejected = false
				for i := 0; i < 3; i++ {
					tick()
				}
				if d.reads != reads || d.renames != 1 || d.moves != 1 || len(d.entries["200"]) != 3 {
					t.Fatal("unconfirmed move was replayed", d)
				}
				return
			}
			for i := 0; i < 3; i++ {
				current = tick()
				if current.WorkflowState == "organized" {
					break
				}
			}
			view, err := f.service.JobView(ctx, job.ID)
			if err != nil || current.WorkflowState != "organized" || view.Organization.State != "done" || view.Organization.Moved != 2 || view.PermittedActions.Reconcile || view.PermittedActions.Retry {
				t.Fatal("same task did not finish", view, err)
			}
			wantStem := "黑暗召唤师在地下恋爱中.S02E06.字幕组"
			gotNames := make([]string, 0, len(d.entries["902"]))
			for _, entry := range d.entries["902"] {
				gotNames = append(gotNames, entry.Name)
			}
			if !reflect.DeepEqual(gotNames, []string{wantStem + ".MKV", wantStem + ".zh-Hans.forced.ASS"}) || len(d.entries["200"]) != 1 || d.entries["200"][0].ID != "402" {
				t.Fatal("incorrect offset, subtitle pairing or original-file move", d.entries)
			}
			if d.downloadCreates != 1 || inspects != 1 || submits != 1 || statuses != 1 || d.creates != 2 || d.renames != 1 || d.moves != 1 {
				t.Fatal("a successful step was repeated", d, inspects, submits, statuses)
			}
			wantReads := map[string]int{"none": 2, "mkdir": 4, "rename": 4, "move": 3}[failure]
			if d.reads != wantReads {
				t.Fatal("unexpected directory read count", d.reads, wantReads)
			}
			if view.Organization.TargetPath != job.Snapshot.Organization.Target.ParentPath {
				t.Fatal("missing historical target")
			}
			reads := d.reads
			delete(d.entries, "902") // User's later organization/deletion is final.
			for i := 0; i < 3; i++ {
				current = tick()
			}
			if d.reads != reads || d.renames != 1 || d.moves != 1 || submits != 1 || current.ID != job.ID {
				t.Fatal("completed history triggered cloud work", d)
			}
		})
	}
}
