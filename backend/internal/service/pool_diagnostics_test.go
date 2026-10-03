package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestPoolDiagnosticBoundedAndPrivate(t *testing.T) {
	ctx, d := WithPoolDiagnostic(context.Background())
	p, _ := poolTest(t)
	a := poolTestAccount("a", "1234567")
	b := poolTestAccount("b", "7654321")
	lease, err := p.Acquire(ctx, "session", "user", []P115Account{a, b}, func(_ context.Context, selected P115Account) error {
		if selected.ID == a.ID {
			return p115.ErrShareLoginRequired
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	encoded, _ := json.Marshal(d.Snapshot())
	if strings.Contains(string(encoded), "1234567") || !strings.Contains(string(encoded), "invalid_credentials") || !strings.Contains(string(encoded), "selected") {
		t.Fatal(string(encoded))
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); recordPoolCandidate(ctx, "secret", "probe", "ok", 1); _ = d.Snapshot() }()
	}
	wg.Wait()
	snapshot := d.Snapshot()
	if len(snapshot.Events) != 32 || snapshot.Dropped == 0 {
		t.Fatal(snapshot)
	}
	snapshot.Events[0].Reason = "mutated"
	if d.Snapshot().Events[0].Reason == "mutated" {
		t.Fatal("mutable snapshot")
	}
	ObservePoolAdmission(ctx, ErrPlaybackDeviceConflict)
	p.ObserveAdmissionOutcome(d.Snapshot().Outcome)
	ObservePoolAdmission(ctx, &PoolAdmissionError{Reason: PoolCapacityFull})
	p.ObserveAdmissionOutcome(d.Snapshot().Outcome)
	p.ObserveRedirect(time.Millisecond, false, 2)
	p.ObserveRedirect(time.Millisecond, true, 0)
	p.ObserveResolution(true, false, false) // New upload is not reuse.
	p.ObserveResolution(true, false, true)
	p.ObserveResolution(true, true, false) // Cached redirect avoids upload.
	p.ObserveResolution(false, false, false)
	m := p.Metrics()
	if m.Requests != 2 || m.SwitchedRequests != 1 || m.Switches != 2 || m.DeviceConflicts != 1 || m.CapacityDenials != 1 || m.Resolved != 4 || m.CopyResolutions != 3 || m.CopyReuses != 2 {
		t.Fatal(m)
	}
}
