package service

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

func TestAnimeInstantDownloadOrganizesInOnePassWithoutReadbacks(t *testing.T) {
	f, db, _, job, drive := animeOrganizeFixture(t)
	cleanup := animeEnableCleanup(f, drive)
	if _, err := db.Exec(`DELETE FROM anime_drive_operations WHERE download_job_id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anime_download_jobs SET download_state='queued',file_state='pending',files_json=NULL,result_object_id=NULL,result_object_kind=NULL,directory_cid=NULL,directory_operation_json=NULL,next_run_at=NULL WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	drive.entries["200"] = []model.AnimeDriveEntry{{ID: "300", ParentCID: "200", Kind: "directory", Name: "release"}}
	creates, submits, lookups, statuses := 0, 0, 0, 0
	f.service.FileGateway = &animeFileGatewayStub{
		create: func(ctx context.Context, parent, name, cookie string) (string, error) {
			if parent == job.ParentCID && name == job.DirectoryName {
				creates++
				return "200", nil
			}
			return drive.CreateDirectory(ctx, parent, name, cookie)
		},
		page: drive.ReadAnimeDrivePage,
	}
	forbidAnimeRecoveryDirectoryRead(t, f)
	f.service.SubmitGateway = &animeSubmitGatewayStub{
		inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
			lookups++
			return p115.AnimeTaskObservation{}, nil
		},
		submit: func(context.Context, string, string, string) error { submits++; return nil },
	}
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		statuses++
		return serviceAnimeStatus(job, "finished", 100), nil
	})
	now := time.Now().Add(time.Second)
	runner := &AnimeStatusRunner{Admin: f.service, now: func() time.Time { return now }}
	if err := runner.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil || got.WorkflowState != "organized" || creates != 1 || submits != 1 || lookups != 1 || statuses != 1 || drive.reads != 3 || drive.renames != 1 || drive.moves != 1 || cleanup.deletes != 1 {
		t.Fatal("expected one submit, one completion read, two source pages and one target listing; no post-write reads", got.WorkflowState, err, creates, submits, lookups, statuses, drive, cleanup.deletes)
	}
	now = now.Add(24 * time.Hour)
	if err := runner.PollOnce(context.Background()); err != nil || statuses != 1 || drive.reads != 3 || submits != 1 {
		t.Fatal("finished download was accessed again", err)
	}
}

func TestAnimeRunningDownloadUsesAtMostTenChecksInFirstHalfHour(t *testing.T) {
	f, _, job, runner, now := animeStatusFixture(t)
	start := *now
	reads := 0
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		reads++
		return serviceAnimeStatus(job, "running", 50), nil
	})
	for *now <= start+(30*time.Minute).Milliseconds() {
		before := reads
		if err := runner.PollOnce(context.Background()); err != nil || reads != before+1 {
			t.Fatal("due read missing", err, reads)
		}
		got, err := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
		if err != nil || got.NextRunAt == nil {
			t.Fatal(got, err)
		}
		*now = *got.NextRunAt - 1
		// Repeated local ticks/UI-independent wakeups cannot generate extra I/O.
		for i := 0; i < 3; i++ {
			if err := runner.PollOnce(context.Background()); err != nil || reads != before+1 {
				t.Fatal("wake bypassed the persisted due time", err, reads)
			}
		}
		*now++
	}
	if reads > 10 || reads < 8 {
		t.Fatal("unexpected half-hour request budget", reads)
	}
}
