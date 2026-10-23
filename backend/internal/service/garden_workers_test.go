package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

type gardenReaderFunc func(context.Context, string, string) (model.AnimeOfflineStatus, error)

func (f gardenReaderFunc) ReadAnimeTaskStatus(ctx context.Context, cookie, hash string) (model.AnimeOfflineStatus, error) {
	return f(ctx, cookie, hash)
}

// The existing in-memory drive is deliberately simple. Serialize its maps,
// while keeping simulated network stalls outside that bookkeeping lock.
type gardenWorkerDrive struct {
	*organizeFakeDrive
	mu           sync.Mutex
	beforeCreate func(context.Context, string) error
	beforeRename func(context.Context) error
}

func (d *gardenWorkerDrive) DriveFiles(ctx context.Context, cid string, offset int, cookie string) (*p115.SharePage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.organizeFakeDrive.DriveFiles(ctx, cid, offset, cookie)
}

func (d *gardenWorkerDrive) CreateDirectory(ctx context.Context, parent, name, cookie string) (string, error) {
	if d.beforeCreate != nil {
		if err := d.beforeCreate(ctx, name); err != nil {
			return "", err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return d.organizeFakeDrive.CreateDirectory(ctx, parent, name, cookie)
}

func (d *gardenWorkerDrive) RenameEntries(ctx context.Context, entries []p115.RenameTarget, cookie string) error {
	if d.beforeRename != nil {
		if err := d.beforeRename(ctx); err != nil {
			return err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.organizeFakeDrive.RenameEntries(ctx, entries, cookie)
}

func (d *gardenWorkerDrive) MoveEntriesBatch(ctx context.Context, ids []string, cid, cookie string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.organizeFakeDrive.MoveEntriesBatch(ctx, ids, cid, cookie)
}

func gardenWorkerFixture(t *testing.T, count int) (*GardenOrganize, *gardenWorkerDrive, []OrganizeJob) {
	t.Helper()
	s, drive, _, _, _, in := gardenFixture(t)
	plan, err := s.Preview(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	account, err := s.Organize.account(plan.Config.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	jobs := make([]OrganizeJob, count)
	for i := range jobs {
		job := cloneOrganizeJob(plan)
		job.ID, job.Download.Hash = fmt.Sprintf("%032x", i+1), fmt.Sprintf("%040x", i+1)
		job.Status = "downloading"
		job.Config.TargetCID = fmt.Sprint(100 + i)
		job.Config.SourceCID, job.Download.DirectoryID = fmt.Sprint(200+i), fmt.Sprint(200+i)
		job.Config.SourcePath = path.Join(job.Download.ParentPath, "garden-"+job.ID)
		drive.dirs[job.Config.TargetCID] = []p115.ShareFile{}
		drive.dirs[job.Config.SourceCID] = []p115.ShareFile{{ID: fmt.Sprint(300 + i), Name: "show", Directory: true}}
		drive.dirs[fmt.Sprint(300+i)] = []p115.ShareFile{{ID: fmt.Sprint(400 + i), Name: "[Group] Show [01].mkv", Size: 1200}}
		jobs[i] = cloneOrganizeJob(job)
		s.live[job.ID] = &gardenPending{job: job, account: account}
	}
	doc := s.Organize.doc
	doc.Jobs = jobs
	if err = s.Organize.saveLocked(doc); err != nil {
		t.Fatal(err)
	}
	d := &gardenWorkerDrive{organizeFakeDrive: drive}
	s.Organize.drive = d
	return s, d, jobs
}

func gardenWorkerStatus(job OrganizeJob, state string) model.AnimeOfflineStatus {
	status, progress := int64(1), 50.0
	if state == "completed" {
		progress = 100
	}
	if state == "failed" {
		status = -1
	}
	return model.AnimeOfflineStatus{SchemaVersion: 1, Source: model.AnimeOfflineExact, Present: true, InfoHash: job.Download.Hash, TargetCID: job.Download.DirectoryID, Status: &status, Progress: &progress, DisplayStatus: state, State: state}
}

func gardenWait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("garden workers did not make independent progress")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func gardenJobState(s *GardenOrganize, id string) string {
	for _, job := range s.Organize.History() {
		if job.ID == id {
			return job.Status
		}
	}
	return ""
}

func gardenRunTest(t *testing.T, s *GardenOrganize) (context.CancelFunc, <-chan struct{}, *error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var err error
	go func() {
		err = s.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("garden worker or network request leaked after shutdown")
		}
	})
	return cancel, done, &err
}

func TestGardenWorkersIsolateSlowStatusAndSubmission(t *testing.T) {
	for _, phase := range []string{"status", "submission"} {
		t.Run(phase, func(t *testing.T) {
			s, drive, jobs := gardenWorkerFixture(t, 2)
			entered := make(chan struct{})
			var once sync.Once
			stall := func(ctx context.Context) error {
				deadline, bounded := ctx.Deadline()
				limit := 30 * time.Second
				if phase == "submission" {
					limit = 2 * time.Minute
				}
				if !bounded || time.Until(deadline) > limit {
					t.Error("network phase has no bounded deadline")
				}
				once.Do(func() { close(entered) })
				<-ctx.Done()
				return ctx.Err()
			}
			if phase == "submission" {
				p := s.live[jobs[0].ID]
				p.job.Status = "queued"
				p.job.Download.DirectoryID = ""
				drive.beforeCreate = func(ctx context.Context, name string) error {
					if name == "garden-"+jobs[0].ID {
						return stall(ctx)
					}
					return nil
				}
			}
			var reads atomic.Int32
			s.Reader = gardenReaderFunc(func(ctx context.Context, _, hash string) (model.AnimeOfflineStatus, error) {
				if hash == jobs[0].Download.Hash {
					return model.AnimeOfflineStatus{}, stall(ctx)
				}
				reads.Add(1)
				return gardenWorkerStatus(jobs[1], "completed"), nil
			})
			cancel, done, runErr := gardenRunTest(t, s)
			gardenWait(t, func() bool {
				select {
				case <-entered:
					return gardenJobState(s, jobs[1].ID) == "completed"
				default:
					return false
				}
			})
			if reads.Load() != 1 {
				t.Fatal("completed peer was polled again", reads.Load())
			}
			cancel()
			gardenWait(t, func() bool {
				select {
				case <-done:
					return true
				default:
					return false
				}
			})
			if *runErr != nil {
				t.Fatal(*runErr)
			}
			want := "interrupted"
			if phase == "submission" {
				want = "uncertain" // A canceled write must never become retryable automatically.
			}
			if gardenJobState(s, jobs[0].ID) != want || drive.moves != 1 {
				t.Fatal("canceled task replayed work or lost its outcome", s.Organize.History())
			}
		})
	}
}

func TestGardenWorkersSlowOrganizationKeepsTrackingAndCapacity(t *testing.T) {
	s, drive, jobs := gardenWorkerFixture(t, 4)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var renaming atomic.Int32
	drive.beforeRename = func(ctx context.Context) error {
		renaming.Add(1)
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var calls [4]atomic.Int32
	s.Reader = gardenReaderFunc(func(ctx context.Context, _, hash string) (model.AnimeOfflineStatus, error) {
		for i, job := range jobs {
			if hash != job.Download.Hash {
				continue
			}
			calls[i].Add(1)
			if i == 3 {
				<-ctx.Done() // One hung status request must not block the remaining three.
				return model.AnimeOfflineStatus{}, ctx.Err()
			}
			if i == 2 {
				select {
				case <-entered:
					return gardenWorkerStatus(job, "failed"), nil
				case <-ctx.Done():
					return model.AnimeOfflineStatus{}, ctx.Err()
				}
			}
			return gardenWorkerStatus(job, "completed"), nil
		}
		return gardenWorkerStatus(OrganizeJob{Download: &GardenDownload{Hash: hash}}, "downloading"), nil
	})
	cancel, done, runErr := gardenRunTest(t, s)
	gardenWait(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.live) == 3 && gardenJobState(s, jobs[2].ID) == "blocked"
	})
	// Add a new task while another task holds the organizer reservation.
	next := cloneOrganizeJob(jobs[2])
	next.ID, next.Download.Hash, next.Download.DirectoryID = "00000000000000000000000000000005", fmt.Sprintf("%040x", 5), ""
	next.Config.SourceCID, next.Config.SourcePath = next.Download.ParentCID, next.Download.ParentPath
	s.Organize.mu.Lock()
	s.Organize.plans[next.ID] = next
	s.Organize.mu.Unlock()
	if _, err := s.Start(next.ID); err != nil {
		t.Fatal("failed task did not release capacity", err)
	}
	gardenWait(t, func() bool { return gardenJobState(s, next.ID) == "downloading" })
	sixth := cloneOrganizeJob(next)
	sixth.ID, sixth.Download.Hash = "00000000000000000000000000000006", fmt.Sprintf("%040x", 6)
	s.Organize.mu.Lock()
	s.Organize.plans[sixth.ID] = sixth
	s.Organize.mu.Unlock()
	if _, err := s.Start(sixth.ID); err == nil {
		t.Fatal("more than four live tasks were accepted")
	}
	if renaming.Load() != 1 {
		t.Fatal("multiple tasks entered directory mutation together", renaming.Load())
	}
	close(release)
	gardenWait(t, func() bool {
		return gardenJobState(s, jobs[0].ID) == "completed" && gardenJobState(s, jobs[1].ID) == "completed"
	})
	if calls[0].Load() != 1 || calls[1].Load() != 1 || calls[2].Load() != 1 {
		t.Fatal("completed/failed task was polled again")
	}
	cancel()
	gardenWait(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if *runErr != nil || drive.moves != 2 || drive.renames != 2 {
		t.Fatal("peer progress or shutdown failed", *runErr, drive.moves, drive.renames)
	}
}

func TestGardenWorkersPersistenceFailureCancelsPeers(t *testing.T) {
	s, _, jobs := gardenWorkerFixture(t, 2)
	entered := make(chan struct{})
	want := errors.New("disk full")
	s.Reader = gardenReaderFunc(func(ctx context.Context, _, hash string) (model.AnimeOfflineStatus, error) {
		if hash == jobs[0].Download.Hash {
			close(entered)
			<-ctx.Done()
			return model.AnimeOfflineStatus{}, ctx.Err()
		}
		select {
		case <-entered:
		case <-ctx.Done():
			return model.AnimeOfflineStatus{}, ctx.Err()
		}
		s.Organize.mu.Lock()
		s.Organize.store.(*failingAccountStore).err = want
		s.Organize.mu.Unlock()
		return gardenWorkerStatus(jobs[1], "downloading"), nil
	})
	_, done, runErr := gardenRunTest(t, s)
	gardenWait(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if !errors.Is(*runErr, want) {
		t.Fatal("persistence failure was hidden", *runErr)
	}
}

func TestGardenWorkersExpiredAndUnreadableTasksStayIsolated(t *testing.T) {
	s, drive, jobs := gardenWorkerFixture(t, 3)
	p := s.live[jobs[0].ID]
	p.job.CreatedAt = time.Now().Add(-25 * time.Hour)
	var reads [3]atomic.Int32
	s.Reader = gardenReaderFunc(func(_ context.Context, _, hash string) (model.AnimeOfflineStatus, error) {
		for i, job := range jobs {
			if hash != job.Download.Hash {
				continue
			}
			reads[i].Add(1)
			if i == 1 {
				return model.AnimeOfflineStatus{}, context.DeadlineExceeded
			}
			return model.AnimeOfflineStatus{}, nil
		}
		return model.AnimeOfflineStatus{}, nil
	})
	cancel, done, runErr := gardenRunTest(t, s)
	gardenWait(t, func() bool {
		return gardenJobState(s, jobs[0].ID) == "interrupted" && reads[1].Load() == 1 && reads[2].Load() == 1
	})
	cancel()
	gardenWait(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if *runErr != nil || reads[0].Load() != 0 || drive.creates != 0 || drive.renames != 0 || drive.moves != 0 {
		t.Fatal("expired/unreadable task issued unexpected work", *runErr, reads[0].Load())
	}
}

func TestGardenWorkersCancelDuringWriteRetainsUncertainResult(t *testing.T) {
	s, drive, jobs := gardenWorkerFixture(t, 1)
	entered := make(chan struct{})
	drive.beforeRename = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	s.Reader = gardenReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		return gardenWorkerStatus(jobs[0], "completed"), nil
	})
	cancel, done, runErr := gardenRunTest(t, s)
	gardenWait(t, func() bool {
		select {
		case <-entered:
			return true
		default:
			return false
		}
	})
	cancel()
	gardenWait(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	job := s.Organize.History()[0]
	if *runErr != nil || job.Status != "partial" || job.Items[0].Status != "uncertain" || job.Resumable || job.Stats.RenameRequests != 1 || job.Stats.MoveRequests != 0 {
		t.Fatal("unknown write outcome became replayable", *runErr, job)
	}
	if release, err := s.Organize.ReserveDirectoryWork(); err != nil {
		t.Fatal("directory reservation leaked", err)
	} else {
		release()
	}
}
