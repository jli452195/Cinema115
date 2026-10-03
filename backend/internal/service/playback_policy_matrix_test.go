package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestDevicePolicyConfiguredBoundaries(t *testing.T) {
	for _, seconds := range []int{0, 30, 60, 90, 180, 29, 181} {
		t.Run(fmt.Sprint(seconds), func(t *testing.T) {
			s, r, accounts, now := deviceSlotFixture(t)
			err := s.UserPlayback.ConfigureDevicePolicy(context.Background(), model.PlaybackDevicePolicy{Mode: DevicePolicyHeartbeat, SilenceSeconds: seconds})
			if seconds == 29 || seconds == 181 {
				if err == nil {
					t.Fatal("invalid threshold accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := seconds
			if want == 0 {
				want = 60
			}
			if s.UserPlayback.DevicePolicy().SilenceSeconds != want {
				t.Fatal("wrong effective threshold")
			}
			deviceSlotIssue(t, s, r, accounts, "phone", "A")
			*now = now.Add(time.Duration(want)*time.Second - time.Millisecond)
			if _, err := deviceSlotPrepare(t, s, r, accounts, "tv", "B"); !errors.Is(err, ErrPlaybackDeviceConflict) {
				t.Fatal("early takeover", err)
			}
			*now = now.Add(time.Millisecond)
			deviceSlotIssue(t, s, r, accounts, "tv", "B")
		})
	}
}

func TestDevicePolicyCrossStateMatrix(t *testing.T) {
	ctx := context.Background()
	t.Run("parallel_to_heartbeat", func(t *testing.T) {
		s, r, accounts, now := deviceSlotFixture(t)
		cfg := s.Pool.Settings()
		cfg.MaxSessions = 2
		if err := s.Pool.Configure(ctx, cfg); err != nil {
			t.Fatal(err)
		}
		a := deviceSlotIssue(t, s, r, accounts[:1], "phone", "A")
		b := deviceSlotIssue(t, s, r, accounts[:1], "tv", "B")
		enableHeartbeatPolicy(t, s)
		if len(s.UserPlayback.List()) != 2 || !a.poolLease.Current() || !b.poolLease.Current() {
			t.Fatal("policy change retired existing sessions")
		}
		if _, err := deviceSlotPrepare(t, s, r, accounts[:1], "tablet", "C"); !errors.Is(err, ErrPlaybackDeviceConflict) {
			t.Fatal(err)
		}
		*now = now.Add(90 * time.Second)
		deviceSlotIssue(t, s, r, accounts[:1], "tablet", "C")
		multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
	})
	for _, mode := range []string{DevicePolicyParallel, DevicePolicyHeartbeat} {
		t.Run("prepare_change_"+mode, func(t *testing.T) {
			s, r, accounts, now := deviceSlotFixture(t)
			enableHeartbeatPolicy(t, s)
			a := deviceSlotIssue(t, s, r, accounts[:1], "phone", "A")
			*now = now.Add(90 * time.Second)
			b, err := deviceSlotPrepare(t, s, r, accounts[:1], "tv", "B")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.UserPlayback.ConfigureDevicePolicy(ctx, model.PlaybackDevicePolicy{Mode: mode, SilenceSeconds: 180}); err != nil {
				t.Fatal(err)
			}
			if err := b.commitDevicePlayback(ctx); err == nil {
				t.Fatal("old policy preparation committed")
			}
			_ = b.Close()
			_ = b.userLease.Close()
			if len(s.UserPlayback.List()) != 1 || !a.poolLease.Current() {
				t.Fatal("failed preparation retired incumbent")
			}
		})
	}
	for _, mode := range []string{DevicePolicyParallel, DevicePolicyHeartbeat} {
		t.Run("stopped_into_"+mode, func(t *testing.T) {
			s, r, accounts, _ := deviceSlotFixture(t)
			a := deviceSlotIssue(t, s, r, accounts[:1], "phone", "A")
			if err := s.ApplyPlaybackEvent(ctx, deviceSlotEvent(s, a), true); err != nil {
				t.Fatal(err)
			}
			if err := s.UserPlayback.ConfigureDevicePolicy(ctx, model.PlaybackDevicePolicy{Mode: mode, SilenceSeconds: 60}); err != nil {
				t.Fatal(err)
			}
			b, err := deviceSlotPrepare(t, s, r, accounts[:1], "tv", "B")
			if mode == DevicePolicyParallel {
				if !errors.Is(err, ErrPoolBusy) {
					t.Fatal("residual not counted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := b.commitDevicePlayback(ctx); err != nil {
				t.Fatal(err)
			}
			multiUIDCounts(t, s.Pool, accounts, 1, 0, 0)
		})
	}
	t.Run("capacity_reduction", func(t *testing.T) {
		s, r, accounts, now := deviceSlotFixture(t)
		enableHeartbeatPolicy(t, s)
		cfg := s.Pool.Settings()
		cfg.MaxSessions = 2
		if err := s.Pool.Configure(ctx, cfg); err != nil {
			t.Fatal(err)
		}
		deviceSlotIssue(t, s, r, accounts[:1], "phone", "A")
		other := r
		other.UserID = "other"
		other.EmbyID = "other"
		deviceSlotIssue(t, s, other, accounts[:1], "other-device", "other")
		cfg.MaxSessions = 1
		if err := s.Pool.Configure(ctx, cfg); err != nil {
			t.Fatal(err)
		}
		deviceSlotIssue(t, s, r, accounts[:1], "phone", "A")
		*now = now.Add(90 * time.Second)
		deviceSlotIssue(t, s, r, accounts[:1], "tv", "B")
		if got := s.Pool.AccountStatus(accounts[0]).Sessions; got != 2 {
			t.Fatal("existing capacity changed", got)
		}
		other.UserID = "new"
		other.EmbyID = "new"
		if _, err := deviceSlotPrepare(t, s, other, accounts[:1], "new-device", "new"); !errors.Is(err, ErrPoolBusy) {
			t.Fatal("new capacity admitted", err)
		}
	})
}
