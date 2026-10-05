package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"strm115/backend/internal/model"
)

func TestReshareStatisticsSurviveReopenAndFailureCheckpoint(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stats.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	job := sampleJob("stats", "receiving")
	job.Stats = &model.ReshareStats{SourceFiles: 4848, ScanRounds: 2, WaitMS: 6400, StageMS: map[string]int64{"source": 62000, "verifying_transfer": 180000}, Directories: map[string]*model.ReshareDirectoryStats{"transfer/99": {Phase: "transfer", CID: "99", Requests: 15, FirstPages: 4, FullReads: 1, Retries: 2, Probes: 3, RequestMS: 6000}}}
	if err := s.SaveReshareJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	job.Finished = true
	job.LastError = "cancelled"
	if err := s.SaveReshareJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetReshareJob(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(got, job) {
		t.Fatal("statistics lost", got, err)
	}
	jobs, err := s.ListReshareJobs(ctx, "", 10, 0)
	if err != nil || len(jobs) != 1 || !reflect.DeepEqual(jobs[0].Stats, job.Stats) {
		t.Fatal(jobs, err)
	}
}

func TestReshareStatisticsSchemaUpgradePreservesExistingJobs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	job := sampleJob("old", "completed")
	if err := s.SaveReshareJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE reshare_jobs DROP COLUMN stats; PRAGMA user_version=35;`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetReshareJob(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(got, job) {
		t.Fatal(got, err)
	}
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
}
