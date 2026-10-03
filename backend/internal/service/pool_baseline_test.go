package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

// This baseline uses independent caches per worker, one fixed film per cache.
// It measures real service/SQLite time plus 2 ms per mocked client operation.
// Client operations are NOT claimed to be physical 115 HTTP request counts.
type baselineDrive struct {
	*fastCacheFake
	calls int
}

func (f *baselineDrive) tick(ctx context.Context) error {
	f.calls++
	timer := time.NewTimer(2 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (f *baselineDrive) PickCodeInstantInfo(ctx context.Context, pc, cookie string) (*p115.DownloadInfo, error) {
	if err := f.tick(ctx); err != nil {
		return nil, err
	}
	return f.fastCacheFake.PickCodeInstantInfo(ctx, pc, cookie)
}
func (f *baselineDrive) PreparePlaybackAccount(ctx context.Context, cookie, uid string) (*p115.PlaybackUploadTicket, error) {
	if err := f.tick(ctx); err != nil {
		return nil, err
	}
	return f.fastCacheFake.PreparePlaybackAccount(ctx, cookie, uid)
}
func (f *baselineDrive) ValidatePlaybackDirectory(ctx context.Context, a, b, c string) error {
	if err := f.tick(ctx); err != nil {
		return err
	}
	return f.fastCacheFake.ValidatePlaybackDirectory(ctx, a, b, c)
}
func (f *baselineDrive) FindPlaybackFileBySHA1Excluding(ctx context.Context, hash, cookie string, missing map[string]bool) (*p115.DrivePlaybackFile, error) {
	if err := f.tick(ctx); err != nil {
		return nil, err
	}
	return f.fastCacheFake.FindPlaybackFileBySHA1Excluding(ctx, hash, cookie, missing)
}
func (f *baselineDrive) VerifyPlaybackFile(ctx context.Context, file p115.DrivePlaybackFile, cookie string) error {
	if err := f.tick(ctx); err != nil {
		return err
	}
	return f.fastCacheFake.VerifyPlaybackFile(ctx, file, cookie)
}
func (f *baselineDrive) DriveDownloadURLWithUA(ctx context.Context, id, pc, cookie, ua string) (string, error) {
	if err := f.tick(ctx); err != nil {
		return "", err
	}
	return f.fastCacheFake.DriveDownloadURLWithUA(ctx, id, pc, cookie, ua)
}
func (f *baselineDrive) UploadPlayback(ctx context.Context, info *p115.DownloadInfo, scope, cid, cookie string, ticket *p115.PlaybackUploadTicket, progress func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	if err := f.tick(ctx); err != nil {
		return nil, err
	}
	return f.fastCacheFake.UploadPlayback(ctx, info, scope, cid, cookie, ticket, progress)
}
func (f *baselineDrive) CompletePlaybackUpload(ctx context.Context, info *p115.DownloadInfo, a string, r *p115.InstantUploadResult, b, c string) (p115.DrivePlaybackFile, string, error) {
	if err := f.tick(ctx); err != nil {
		return p115.DrivePlaybackFile{}, "", err
	}
	return f.fastCacheFake.CompletePlaybackUpload(ctx, info, a, r, b, c)
}

type poolBaselineResult struct {
	Scenario       string         `json:"scenario"`
	Concurrency    int            `json:"concurrency"`
	Requests       int            `json:"requests"`
	P50Micros      int64          `json:"p50Micros"`
	P95Micros      int64          `json:"p95Micros"`
	MockOperations int            `json:"mockClientOperations"`
	Writes         int            `json:"mockUploads"`
	PeakQueue      int            `json:"peakApiQueue"`
	PeakOccupancy  int            `json:"peakLogicalOccupancy"`
	Errors         map[string]int `json:"errors"`
}

func emitPoolBaseline(t *testing.T, r poolBaselineResult, latencies []int64) {
	t.Helper()
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	r.Requests = len(latencies)
	if len(latencies) > 0 {
		r.P50Micros = latencies[(len(latencies)*50+99)/100-1]
		r.P95Micros = latencies[(len(latencies)*95+99)/100-1]
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("POOL_BASELINE " + string(data))
}
func TestPoolSimulationBaseline(t *testing.T) {
	const workers = 4
	ctx := context.Background()
	t.Run("cold_and_cached", func(t *testing.T) {
		caches := make([]*PlaybackCache, workers)
		drives := make([]*baselineDrive, workers)
		selections := make([]*PlaybackCacheSelection, workers)
		for i := 0; i < workers; i++ {
			cache, f, sel := setupFastCache(t)
			drives[i] = &baselineDrive{fastCacheFake: f}
			cache.client = drives[i]
			caches[i], selections[i] = cache, sel
		}
		for _, round := range []struct {
			name  string
			count int
		}{{"cold", 1}, {"cached_copy", 20}} {
			var wg sync.WaitGroup
			var mu sync.Mutex
			latencies := make([]int64, 0, workers*round.count)
			failures := map[string]int{}
			beforeCalls, beforeWrites := 0, 0
			for _, d := range drives {
				beforeCalls += d.calls
				beforeWrites += d.uploads
			}
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					for n := 0; n < round.count; n++ {
						started := time.Now()
						_, report, err := caches[i].ResolvePickCodeWithReport(ctx, selections[i], fmt.Sprintf("pick%c", 'a'+i), "source", "player", "source")
						elapsed := time.Since(started).Microseconds()
						mu.Lock()
						latencies = append(latencies, elapsed)
						if err != nil {
							failures[ClassifyPoolFault(PoolFaultTarget, err).Code]++
						}
						if err == nil && round.name == "cached_copy" && !report.ReusedCopy {
							failures["missing_reuse_evidence"]++
						}
						mu.Unlock()
					}
				}(i)
			}
			wg.Wait()
			calls, writes := 0, 0
			for _, d := range drives {
				calls += d.calls
				writes += d.uploads
			}
			calls -= beforeCalls
			writes -= beforeWrites
			if len(failures) != 0 || round.name == "cold" && writes != workers || round.name == "cached_copy" && writes != 0 {
				t.Fatal("baseline violated", round.name, writes, failures)
			}
			emitPoolBaseline(t, poolBaselineResult{Scenario: round.name, Concurrency: workers, MockOperations: calls, Writes: writes, Errors: failures}, latencies)
		}
	})
	t.Run("same_uid_and_rollback", func(t *testing.T) {
		s, r, accounts, now := deviceSlotFixture(t)
		enableHeartbeatPolicy(t, s)
		last := deviceSlotIssue(t, s, r, accounts[:1], "device-0", "play-0")
		latencies := make([]int64, 0, 20)
		for i := 1; i <= 20; i++ {
			*now = now.Add(90 * time.Second)
			started := time.Now()
			last = deviceSlotIssue(t, s, r, accounts[:1], fmt.Sprint("device-", i), fmt.Sprint("play-", i))
			latencies = append(latencies, time.Since(started).Microseconds())
			if s.Pool.AccountStatus(accounts[0]).Sessions != 1 {
				t.Fatal("capacity leak")
			}
		}
		emitPoolBaseline(t, poolBaselineResult{Scenario: "same_uid_transfer", Concurrency: 1, PeakOccupancy: 1, MockOperations: 20, Errors: map[string]int{}}, latencies)
		*now = now.Add(90 * time.Second)
		if err := s.Pool.report(ctx, last.account, p115.ErrShareLoginRequired); err != nil {
			t.Fatal(err)
		}
		latencies = nil
		for i := 0; i < 20; i++ {
			started := time.Now()
			pending, err := deviceSlotPrepare(t, s, r, accounts[1:2], "next", fmt.Sprint("failed-", i))
			if err != nil {
				t.Fatal(err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			err = pending.commitDevicePlayback(cancelled)
			if !errors.Is(err, context.Canceled) {
				t.Fatal("expected failed transaction", err)
			}
			_ = pending.Close()
			_ = pending.userLease.Close()
			latencies = append(latencies, time.Since(started).Microseconds())
			if !last.userLease.Current() || s.Pool.AccountStatus(accounts[1]).Sessions != 0 {
				t.Fatal("rollback changed old ownership")
			}
		}
		emitPoolBaseline(t, poolBaselineResult{Scenario: "cross_uid_rollback", Concurrency: 1, PeakOccupancy: 2, MockOperations: 20, Errors: map[string]int{"canceled_commit": 20}}, latencies)
	})
	t.Run("api_queue", func(t *testing.T) {
		var budget p115.RequestBudget
		budget.Configure(time.Millisecond, time.Millisecond)
		ctx := p115.WithRequestBudget(ctx, &budget)
		latencies := make([]int64, workers)
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				started := time.Now()
				errs <- p115.WaitRequestBudget(ctx, "UID=7_A")
				latencies[i] = time.Since(started).Microseconds()
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		emitPoolBaseline(t, poolBaselineResult{Scenario: "api_queue", Concurrency: workers, PeakQueue: budget.Stats().PeakWaiting, Errors: map[string]int{}}, latencies)
	})
}
