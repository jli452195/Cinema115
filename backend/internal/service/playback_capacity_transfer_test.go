package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestDevicePolicyTransferHundredDevices(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		t.Run(fmt.Sprint(stopped), func(t *testing.T) {
			s, r, accounts, now := deviceSlotFixture(t)
			enableHeartbeatPolicy(t, s)
			ctx := context.Background()
			last := deviceSlotIssue(t, s, r, accounts[:1], "device-0", "play-0")
			uid := last.account.UID
			for i := 1; i <= 100; i++ {
				e := deviceSlotEvent(s, last)
				if stopped {
					if err := s.ApplyPlaybackEvent(ctx, e, true); err != nil {
						t.Fatal(err)
					}
				} else {
					*now = now.Add(90 * time.Second)
				}
				next, err := deviceSlotPrepare(t, s, r, accounts[:1], fmt.Sprintf("device-%d", i), fmt.Sprintf("play-%d", i))
				if err != nil {
					t.Fatal(err)
				}
				multiUIDCounts(t, s.Pool, accounts, 1, 0, map[bool]int{true: 1, false: 0}[stopped])
				if next.account.UID != uid || next.poolLease.Record.BorrowedKey == "" {
					t.Fatal("full UID not inherited")
				}
				if err := next.commitDevicePlayback(ctx); err != nil {
					t.Fatal(err)
				}
				_ = next.Close()
				_ = next.userLease.Close()
				for _, stop := range []bool{false, true} {
					if err := s.ApplyPlaybackEvent(ctx, e, stop); err != nil {
						t.Fatal(err)
					}
				}
				multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
				if len(s.Pool.doc.Leases) != 1 || !next.userLease.Current() {
					t.Fatal("transfer leaked records or late event retired successor")
				}
				last = next
			}
			t.Log("100 device transfers with one UID at capacity=1: preparation and commit stay at one slot; stops and stale events do not accumulate residuals")
		})
	}
}

func TestDevicePolicyTransferCrossUIDFailureAndCapacity(t *testing.T) {
	s, r, accounts, now := deviceSlotFixture(t)
	enableHeartbeatPolicy(t, s)
	ctx := context.Background()
	a := deviceSlotIssue(t, s, r, accounts[:1], "phone", "A")
	*now = now.Add(90 * time.Second)
	// Different users' downloads/tasks never provide transferable capacity.
	other, err := s.Pool.Acquire(WithPoolRequest(ctx, PoolRequest{Download: true}), "download", r.UserID, accounts[1:2], poolProbe)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = other.Close()
	if err := s.Pool.report(ctx, a.account, p115.ErrShareLoginRequired); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceSlotPrepare(t, s, r, accounts[:2], "tv", "B"); err == nil {
		t.Fatal("full new UID or download was borrowed")
	}
	if !a.userLease.Current() {
		t.Fatal("failed cross UID retired A")
	}
	b, err := deviceSlotPrepare(t, s, r, accounts[2:3], "tv", "B")
	if err != nil {
		t.Fatal(err)
	}
	if b.account.UID == a.account.UID {
		t.Fatal("failed UID reused")
	}
	if _, ok := s.Pool.doc.Leases[a.poolLease.Record.Key]; !ok {
		t.Fatal("preparation deleted A")
	}
	if err := b.commitDevicePlayback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Pool.doc.Leases[a.poolLease.Record.Key]; ok {
		t.Fatal("cross UID residual retained")
	}
	if _, ok := s.Pool.doc.Leases[other.Record.Key]; !ok {
		t.Fatal("download retired by device policy")
	}
	if s.Pool.AccountStatus(a.account).Sessions != 0 || s.Pool.AccountStatus(b.account).Sessions != 1 {
		t.Fatal("cross UID accounting wrong")
	}
}

func TestDevicePolicyTransferExpiredBorrowCannotOverbook(t *testing.T) {
	s, r, accounts, now := deviceSlotFixture(t)
	enableHeartbeatPolicy(t, s)
	ctx := context.Background()
	a := deviceSlotIssue(t, s, r, accounts[:1], "phone", "A")
	*now = now.Add(90 * time.Second)
	b, err := deviceSlotPrepare(t, s, r, accounts[:1], "tv", "B")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate predecessor expiry while preparation is still valid. The pending
	// lease must now count independently and block unrelated reservations.
	s.Pool.mu.Lock()
	old := s.Pool.doc.Leases[a.poolLease.Record.Key]
	old.ExpiresAt = now.Add(-time.Millisecond).UnixMilli()
	s.Pool.doc.Leases[old.Key] = old
	s.Pool.mu.Unlock()
	if _, err := s.Pool.Acquire(ctx, "unrelated", "another-user", accounts[:1], poolProbe); err == nil {
		t.Fatal("expired borrowing hid pending capacity")
	}
	// Lowering configured capacity is disallowed elsewhere; inject an occupied
	// final document to exercise commit's independent capacity validation.
	s.Pool.mu.Lock()
	other := old
	other.Key = "other"
	other.UserID = "other"
	other.SlotKey = "other"
	other.ExpiresAt = now.Add(time.Minute).UnixMilli()
	s.Pool.doc.Leases[other.Key] = other
	s.Pool.mu.Unlock()
	if err := b.commitDevicePlayback(ctx); !errors.Is(err, ErrPoolBusy) {
		t.Fatal("commit overbooked final UID", err)
	}
	if !a.userLease.Current() {
		t.Fatal("failed capacity check retired user")
	}
}
