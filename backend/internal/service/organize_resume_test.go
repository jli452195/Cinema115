package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"strm115/backend/internal/client/p115"
)

// Recovery exercises the live download workflow with an already-bound plan.
func organizeResumeFixture(t *testing.T, count int) (*OrganizeEngine, *organizeFakeDrive, string, OrganizeJob) {
	t.Helper()
	s, drive, account, plan := organizeBatchFixture(t, count)
	plan.Download = &ResourceDownload{Hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", DirectoryID: "10", ParentCID: "0", ParentPath: "/"}
	return s, drive, account, plan
}

func runResumedDownload(t *testing.T, s *OrganizeEngine, id string) OrganizeJob {
	t.Helper()
	download := NewDownloadOrganize(s, nil, nil, nil, nil)
	job, err := download.Resume(id)
	if err != nil {
		t.Fatal(err)
	}
	if repeated, err := download.Resume(id); err != nil || repeated.ID != id {
		t.Fatal("duplicate resume", repeated, err)
	}
	if p := download.live[id]; p != nil {
		if done, err := download.advance(context.Background(), p); err != nil || !done {
			t.Fatal("continuation did not finish", done, err)
		}
	}
	if len(download.History()) != 1 || job.ID != download.History()[0].ID {
		t.Fatal("continuation created another task")
	}
	return download.History()[0]
}

func TestOrganizeContinueOriginal150FileTask(t *testing.T) {
	s, drive, account, plan := organizeResumeFixture(t, 150)
	drive.beforeRead = func(cid string, offset int) error {
		if cid == "30" && drive.moves == 1 {
			return fmt.Errorf("temporary directory read failure")
		}
		return nil
	}
	first := organizeTestRun(t, s, account, plan)
	if first.Status != "partial" || first.Items[49].Status != "done" || !first.Resumable {
		t.Fatal("expected 50/150 partial", first.Status)
	}

	drive.beforeRead = nil
	// The user may rearrange already-confirmed files. Never recheck or replay them.
	drive.dirs["50"] = nil
	job := runResumedDownload(t, s, plan.ID)
	if job.Status != "completed" || job.ID != first.ID || !job.CreatedAt.Equal(first.CreatedAt) || job.Config.TargetCID != "20" || len(drive.dirs["50"]) != 100 {
		t.Fatal("original plan/progress changed", job.Status)
	}
	if drive.renames != 3 || drive.moves != 3 || job.Stats.RenameRequests != 3 || job.Stats.MoveRequests != 3 {
		t.Fatal("completed writes replayed or counters reset", drive.renames, drive.moves, job.Stats)
	}
	if job := runResumedDownload(t, s, plan.ID); job.Status != "completed" || drive.moves != 3 {
		t.Fatal("completed task was replayed", job)
	}
}

func TestOrganizeContinueReconcilesUnknownWrites(t *testing.T) {
	for _, phase := range []string{"rename", "move"} {
		t.Run(phase, func(t *testing.T) {
			s, drive, account, plan := organizeResumeFixture(t, 3)
			if phase == "rename" {
				drive.renameApplyIDs = map[string]bool{plan.Items[0].File.ID: true}
			} else {
				drive.moveApplyIDs = map[string]bool{plan.Items[0].File.ID: true}
			}
			first := organizeTestRun(t, s, account, plan)
			if first.Status != "partial" {
				t.Fatal("expected partial write")
			}

			// Simulate the last write completing after the failed read-back. The
			// same original ID is still the only acceptable evidence of success.
			if phase == "move" {
				file := drive.dirs["30"][0]
				drive.dirs["30"] = drive.dirs["30"][1:]
				drive.dirs["50"] = append(drive.dirs["50"], file)
			}
			drive.renameApplyIDs, drive.moveApplyIDs = nil, nil
			job := runResumedDownload(t, s, plan.ID)
			if job.Status != "completed" || len(drive.dirs["50"]) != 3 {
				t.Fatal("continuation failed", job.Status)
			}
			last := drive.moveBatches[len(drive.moveBatches)-1]
			want := 3
			if phase == "move" {
				want = 1
			}
			if len(last) != want {
				t.Fatal("already moved file replayed", last)
			}
			if phase == "rename" && len(drive.renameBatches[1]) != 2 {
				t.Fatal("already renamed file replayed")
			}
		})
	}
}

func TestOrganizeContinueRejectsChangedIdentityAndTarget(t *testing.T) {
	for _, mode := range []string{"size", "name", "different-id", "missing", "target-directory", "target-deleted", "target-conflict"} {
		t.Run(mode, func(t *testing.T) {
			s, drive, _, plan := organizeResumeFixture(t, 1)
			organizeTestPersist(t, s, plan)
			job := cloneOrganizeJob(s.doc.Jobs[0])
			job.Status = "partial"
			job.Items[0].TargetCID = "50"
			if err := s.checkpoint(&job); err != nil {
				t.Fatal(err)
			}

			switch mode {
			case "size":
				drive.dirs["30"][0].Size++
			case "name":
				drive.dirs["30"][0].Name = "user-renamed.mkv"
			case "different-id":
				drive.dirs["30"][0].ID = "99999"
			case "missing":
				drive.dirs["30"] = nil
			case "target-directory":
				drive.dirs["40"][0].ID = "51"
				drive.dirs["51"] = nil
			case "target-deleted":
				drive.dirs["40"] = nil
			case "target-conflict":
				drive.dirs["50"] = []p115.ShareFile{{ID: "99999", Name: job.Items[0].Name, Size: job.Items[0].File.Size}}
			}
			result := runResumedDownload(t, s, job.ID)
			if result.Status != "partial" || drive.renames != 0 || drive.moves != 0 || drive.creates != 0 {
				t.Fatal("changed identity caused a write", mode, result.Status)
			}
		})
	}
}

func TestOrganizeContinueAccountPersistenceAndWriterGuards(t *testing.T) {
	for _, mode := range []string{"account", "persist"} {
		t.Run(mode, func(t *testing.T) {
			s, drive, account, plan := organizeResumeFixture(t, 1)
			plan.Status = "interrupted"
			s.doc.Jobs = []OrganizeJob{plan}
			switch mode {
			case "account":
				a, _ := s.accounts.Get(account)
				if err := s.accounts.Update(account, a.Revision, "changed", nil); err != nil {
					t.Fatal(err)
				}
			case "persist":
				s.store.(*failingAccountStore).err = errors.New("disk full")
			}
			download := NewDownloadOrganize(s, nil, nil, nil, nil)
			if _, err := download.Resume(plan.ID); err == nil || len(download.live) != 0 || s.doc.Jobs[0].Status != "interrupted" {
				t.Fatal("unsafe resume accepted", err)
			}
			if drive.renames != 0 || drive.moves != 0 {
				t.Fatal("rejected resume wrote files")
			}
		})
	}
}

func TestGardenContinueBoundTaskKeepsWorkflowAndSkipsDownload(t *testing.T) {
	s, drive, adder, tracker, id := interruptedGarden(t, true)
	if _, err := s.Resume(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(id); err != nil || len(s.live) != 1 {
		t.Fatal("duplicate continuation", err)
	}
	p := s.live[id]
	if done, err := s.advance(context.Background(), p); err != nil || !done || p.job.Status != "completed" {
		t.Fatal("download continuation failed", err)
	}
	if tracker.calls != 0 || adder.url != "" || adder.deleted != "" || drive.renames != 1 || drive.moves != 1 {
		t.Fatal("continuation queried/submitted download or replayed writes")
	}
	if len(s.History()) != 1 {
		t.Fatal("continuation changed task origin")
	}
}

func TestDownloadContinuationWaitsForDirectoryWorkAndNotifiesAfterRelease(t *testing.T) {
	s, drive, _, plan := organizeResumeFixture(t, 1)
	plan.Status = "interrupted"
	s.doc.Jobs = []OrganizeJob{plan}
	download := NewDownloadOrganize(s, nil, nil, nil, nil)
	if _, err := download.Resume(plan.ID); err != nil {
		t.Fatal(err)
	}
	release, err := s.ReserveDirectoryWork()
	if err != nil {
		t.Fatal(err)
	}
	p := download.live[plan.ID]
	if done, err := download.advance(context.Background(), p); err != nil || done || drive.renames+drive.moves != 0 {
		t.Fatal("busy directory performed writes", done, err)
	}
	release()
	called := false
	s.OnCompleted = func(_ context.Context, config OrganizeConfig) {
		called = true
		if config != plan.Config || download.History()[0].Status != "completed" {
			t.Error("notification preceded committed completion")
		}
		release, err := s.ReserveDirectoryWork()
		if err != nil {
			t.Error("writer not released before notification", err)
		} else {
			release()
		}
	}
	if done, err := download.advance(context.Background(), p); err != nil || !done || !called {
		t.Fatal("continuation did not resume or notify", done, err, called)
	}
}
