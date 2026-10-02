package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestPoolAdmissionReasonsAndOccupancySnapshot(t *testing.T) {
	for _, tc := range []struct {
		name string
		want PoolAdmissionReason
	}{
		{"capacity", PoolCapacityFull}, {"recovery", PoolRecoveryBusy}, {"draining", PoolDraining}, {"mixed", PoolMixedBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolTest(t)
			now := time.Unix(1000, 0)
			p.now = func() time.Time { return now }
			a := poolTestAccount("a", "1")
			accounts := []P115Account{a}
			p.doc.Health[a.ID] = model.PlaybackPoolHealth{Revision: a.Revision, State: "ready"}
			p.doc.Leases["active"] = model.PlaybackPoolLease{Key: "active", UID: a.UID, Issued: true, ExpiresAt: now.Add(time.Hour).UnixMilli()}
			p.doc.Leases["preparing"] = model.PlaybackPoolLease{Key: "preparing", UID: a.UID, ExpiresAt: now.Add(time.Minute).UnixMilli()}
			p.doc.Leases["residual"] = model.PlaybackPoolLease{Key: "old", UID: a.UID, Issued: true, ExpiresAt: now.Add(2 * time.Minute).UnixMilli()}
			p.doc.Leases["expired"] = model.PlaybackPoolLease{Key: "expired", UID: a.UID, ExpiresAt: now.UnixMilli()}
			switch tc.name {
			case "recovery":
				p.doc.Settings.MaxSessions = 4
				delete(p.doc.Health, a.ID)
			case "draining":
				p.doc.Modes[a.ID] = "draining"
			case "mixed":
				b := poolTestAccount("b", "2")
				accounts = append(accounts, b)
				p.doc.Modes[b.ID] = "draining"
			}
			probes := 0
			_, err := p.Acquire(context.Background(), "new", "user", accounts, func(context.Context, P115Account) error { probes++; return nil })
			var admission *PoolAdmissionError
			if !errors.As(err, &admission) || !errors.Is(err, ErrPoolBusy) || admission.Reason != tc.want || probes != 0 {
				t.Fatalf("wrong rejection: %v, probes=%d", err, probes)
			}
			for _, part := range []string{"准备=1", "活跃=1", "残余=1", "最近到期=1970-01-01T00:17:40Z"} {
				if !strings.Contains(admission.Diagnostic, part) {
					t.Fatal("missing snapshot", part, admission.Diagnostic)
				}
			}
			if strings.Contains(admission.Error(), "UID=") || strings.Contains(admission.Diagnostic, a.Cookie) {
				t.Fatal("sensitive diagnostic")
			}
			saved := admission.Diagnostic
			clear(p.doc.Leases)
			if admission.Diagnostic != saved {
				t.Fatal("diagnostic changed after decision")
			}
		})
	}
}

func TestPoolAdmissionSystemLimits(t *testing.T) {
	for _, reason := range []PoolAdmissionReason{PoolWaiterLimit, PoolRequestLimit, PoolLeaseLimit} {
		t.Run(string(reason), func(t *testing.T) {
			p, _ := poolTest(t)
			switch reason {
			case PoolWaiterLimit:
				p.waiting = 128
			case PoolRequestLimit:
				for i := 0; i < 128; i++ {
					p.busy[fmt.Sprint(i)] = true
				}
			case PoolLeaseLimit:
				for i := 0; i < 4096; i++ {
					key := fmt.Sprint(i)
					p.doc.Leases[key] = model.PlaybackPoolLease{Key: key, UID: "other", Issued: true, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}
				}
			}
			_, err := p.Acquire(context.Background(), "new", "user", []P115Account{poolTestAccount("a", "1")}, poolProbe)
			var admission *PoolAdmissionError
			if !errors.As(err, &admission) || admission.Reason != reason {
				t.Fatal(err)
			}
			if p.busy["new"] {
				t.Fatal("rejected request leaked session lock")
			}
		})
	}
}

func TestPoolAdmissionDiagnosticsBoundedAndUIDShared(t *testing.T) {
	p, _ := poolTest(t)
	accounts := make([]P115Account, 0, 20)
	for i := 0; i < 20; i++ {
		a := poolTestAccount(fmt.Sprint(i), fmt.Sprint(i/2+1))
		accounts = append(accounts, a)
		p.doc.Modes[a.ID] = "draining"
	}
	_, err := p.Acquire(context.Background(), "x", "u", accounts, poolProbe)
	var admission *PoolAdmissionError
	if !errors.As(err, &admission) || strings.Count(admission.Diagnostic, "UID=") != 8 || !strings.Contains(admission.Diagnostic, "另有 2 个 UID") {
		t.Fatal(err, admission)
	}
}
