package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func interruptedGarden(t *testing.T, bound bool) (*GardenOrganize, *organizeFakeDrive, *cloudAdderStub, *gardenTrackerFake, string) {
	t.Helper()
	s, drive, adder, tracker, _, in := gardenFixture(t)
	plan, err := s.Preview(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Start(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := s.live[job.ID]
	if _, err = s.advance(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	dir := p.job.Download.DirectoryID
	drive.dirs[dir] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
	drive.dirs["801"] = []p115.ShareFile{{ID: "901", Name: "[Group] Show [01].mkv", Size: 1200}, {ID: "902", Name: "[Group] Show [02].mkv", Size: 1300}}
	if bound {
		files, _, err := s.downloadFiles(context.Background(), &p.job, p.account.Cookie)
		if err != nil {
			t.Fatal(err)
		}
		if err = bindGardenFiles(&p.job, files, ""); err != nil {
			t.Fatal(err)
		}
		p.job.Items[0].Status = "preparing"
		p.job.Status = "running"
	}
	// A manual recovery gets its own bounded wait budget, without changing
	// the task's original creation time or reusing an expired preview.
	p.job.CreatedAt = time.Now().Add(-48 * time.Hour)
	if err = s.Organize.checkpoint(&p.job); err != nil {
		t.Fatal(err)
	}
	o, err := newManualOrganizeTest(s.Organize.store, s.Organize.accounts, drive, nil)
	if err != nil {
		t.Fatal(err)
	}
	s = NewGardenOrganize(o.OrganizeEngine, nil, s.Downloads, s.Settings, tracker)
	*adder = cloudAdderStub{}
	tracker.calls, tracker.resultID = 0, "801" // Real completed responses can omit target CID.
	return s, drive, adder, tracker, job.ID
}

func TestGardenResumeAfterRestartWithoutResubmission(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(map[bool]string{false: "website-paths", true: "bound-cloud-paths"}[bound], func(t *testing.T) {
			s, drive, adder, tracker, id := interruptedGarden(t, bound)
			if !s.History()[0].Resumable {
				t.Fatal("eligible task not exposed to history")
			}
			job, err := s.Resume(id)
			if err != nil || job.Status != map[bool]string{false: "downloading", true: "running"}[bound] || len(s.History()) != 1 {
				t.Fatal("resume did not reuse record", job, err)
			}
			if _, err = s.Resume(id); err != nil || len(s.live) != 1 || tracker.calls != 0 {
				t.Fatal("duplicate click replayed recovery", err, tracker.calls)
			}
			p := s.live[id]
			cookie := p.account.Cookie
			a, _ := s.Organize.accounts.Get(job.Config.AccountID)
			replacement := "UID=456_new; SEID=replacement"
			if err = s.Organize.accounts.Update(a.ID, a.Revision, a.Name, &replacement); err != nil {
				t.Fatal(err)
			}
			if done, err := s.advance(context.Background(), p); err != nil || !done || p.job.Status != "completed" {
				t.Fatal("recovery did not complete", done, err, p.job)
			}
			if adder.cookie != "" || adder.url != "" || adder.deleted != "" || drive.creates != 3 || tracker.calls != map[bool]int{false: 1, true: 0}[bound] || !bound && tracker.cookie != cookie {
				t.Fatal("recovery resubmitted, recreated staging, or rebound account", adder, drive.creates, tracker.calls)
			}
			if len(drive.dirs["801"]) != 1 || drive.dirs["801"][0].ID != "902" || drive.renames != 1 || drive.moves != 1 {
				t.Fatal("selection or remaining files changed", drive.dirs["801"])
			}
			delete(s.live, id)
			if job, err = s.Resume(id); err != nil || job.Status != "completed" || len(s.live) != 0 || tracker.calls != map[bool]int{false: 1, true: 0}[bound] {
				t.Fatal("completed recovery replayed", err)
			}
			if s.History()[0].Resumable || strings.Contains(s.Organize.store.Value(), cookie) {
				t.Fatal("invalid history or persisted credential")
			}
		})
	}
}

func TestGardenResumeRefusesFileWriteIntentAndInvalidState(t *testing.T) {
	for _, state := range []string{"renaming", "renamed", "moving", "done", "uncertain", "blocked", "skipped", "no-directory", "partial", "ordinary", "account-changed", "persistence-failure", "other-live-hash"} {
		t.Run(state, func(t *testing.T) {
			s, drive, adder, tracker, id := interruptedGarden(t, false)
			job := &s.Organize.doc.Jobs[0]
			switch state {
			case "no-directory":
				job.Download.DirectoryID = ""
			case "partial":
				job.Status = "partial"
			case "ordinary":
				job.Download = nil
			case "account-changed":
				a, _ := s.Organize.accounts.Get(job.Config.AccountID)
				if err := s.Organize.accounts.Update(a.ID, a.Revision, a.Name+" updated", nil); err != nil {
					t.Fatal(err)
				}
			case "persistence-failure":
				s.Organize.store.(*failingAccountStore).err = errors.New("disk full")
			case "other-live-hash":
				s.live["other"] = &gardenPending{job: cloneOrganizeJob(*job)}
			default:
				job.Items[0].Status = state
			}
			if _, err := s.Resume(id); err == nil || s.live[id] != nil {
				t.Fatal("unsafe recovery was accepted", err)
			}
			if tracker.calls != 0 || adder.cookie != "" || drive.renames != 0 || drive.moves != 0 || drive.creates != 1 {
				t.Fatal("rejected recovery performed remote work")
			}
		})
	}
}

func TestGardenResumeReadFailureStopsWithoutSubmitting(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "read-error", true: "missing-task"}[missing], func(t *testing.T) {
			s, drive, adder, tracker, id := interruptedGarden(t, false)
			tracker.missing = missing
			if !missing {
				tracker.err = errors.New("timeout")
			}
			if _, err := s.Resume(id); err != nil {
				t.Fatal(err)
			}
			p := s.live[id]
			if done, err := s.advance(context.Background(), p); err != nil || !done || p.job.Status != "interrupted" || !s.History()[0].Resumable {
				t.Fatal("unconfirmed recovery should remain manually retryable", done, err, p.job.Status)
			}
			if adder.cookie != "" || drive.creates != 1 || drive.renames != 0 || drive.moves != 0 || tracker.calls != 1 {
				t.Fatal("failed recovery replayed writes")
			}
		})
	}
}

func TestGardenResumeRejoinsDownloadingWithBackoff(t *testing.T) {
	s, drive, adder, tracker, id := interruptedGarden(t, false)
	tracker.downloading = true
	if _, err := s.Resume(id); err != nil {
		t.Fatal(err)
	}
	p := s.live[id]
	if done, err := s.advance(context.Background(), p); err != nil || done || p.job.Status != "downloading" || !p.next.After(time.Now()) || p.resuming {
		t.Fatal("did not rejoin normal tracking", done, err, p.job.Status)
	}
	if drive.renames != 0 || drive.moves != 0 || tracker.calls != 1 || adder.cookie != "" {
		t.Fatal("incomplete download mutated or resubmitted")
	}
	tracker.downloading = false
	if done, err := s.advance(context.Background(), p); err != nil || !done || p.job.Status != "completed" || tracker.calls != 2 || adder.cookie != "" {
		t.Fatal("tracked download failed to organize on completion", done, err, p.job.Status)
	}
}
