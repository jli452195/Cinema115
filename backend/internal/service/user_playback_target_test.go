package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestUserPlaybackTargetSuccessFailureAndRestart(t *testing.T) {
	ctx := context.Background()
	p, store, r := userPlaybackFixture(t)
	r.Source = "assigned"
	now := time.Now()
	p.now = func() time.Time { return now }
	l, err := p.Acquire(ctx, r, userPlaybackReq("phone"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if p.List()[0].Target.AccountID != "" {
		t.Fatal("preparation advertised a successful target")
	}
	first := model.UserPlaybackTarget{AccountID: "config-a", AccountName: "小号 A", Source: r.Source}
	if err := l.commitTarget(ctx, first); err != nil {
		t.Fatal(err)
	}
	want := first
	want.IssuedAt = now.UnixMilli()
	if got := p.List()[0].Target; got != want {
		t.Fatal("wrong issued target", got)
	}
	// A failed switch cannot replace the last successful target in memory or disk.
	store.fail = true
	now = now.Add(time.Second)
	second := model.UserPlaybackTarget{AccountID: "config-b", AccountName: "小号 B", Source: "personal"}
	if err := l.commitTarget(ctx, second); err == nil || p.List()[0].Target != want || store.doc.Leases[l.Record.Key].Target != want {
		t.Fatal("failed switch published target", err)
	}
	store.fail = false
	if err := l.commitTarget(ctx, second); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewUserPlayback(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = p.now
	want = second
	want.IssuedAt = now.UnixMilli()
	if got := restarted.List()[0].Target; got != want {
		t.Fatal("restart lost actual successful configuration", got)
	}
	if matched, err := restarted.Event(ctx, l.Record.EventKey, true, l.Record.Generation); err != nil || !matched || len(restarted.List()) != 0 {
		t.Fatal("stopped target still listed", err)
	}
}

func TestUserPlaybackTargetNonPoolSources(t *testing.T) {
	for _, source := range []string{"primary", "personal", "personal_shared"} {
		t.Run(source, func(t *testing.T) {
			s, r, _, now := deviceSlotFixture(t)
			ctx := context.Background()
			if err := s.UserPlayback.ConfigureDevicePolicy(ctx, model.PlaybackDevicePolicy{Mode: DevicePolicyHeartbeat}); err != nil {
				t.Fatal(err)
			}
			r.Source = source
			q := handoffRequest("non-pool")
			l, err := s.UserPlayback.Acquire(ctx, r, q)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			a := &MediaAuthorization{service: s, record: r, userLease: l, account: P115Account{ID: "pinned", Name: "请求时的配置", Cookie: "private-cookie"}}
			if err := a.commitNonPoolDevicePolicy(ctx); err != nil {
				t.Fatal(err)
			}
			want := model.UserPlaybackTarget{AccountID: "pinned", AccountName: "请求时的配置", Source: source, IssuedAt: now.UnixMilli()}
			if got := s.UserPlayback.List()[0].Target; got != want {
				t.Fatal("non-pool target mismatch", got)
			}
			raw, err := json.Marshal(s.UserPlayback.List())
			if err != nil || strings.Contains(string(raw), "private-cookie") {
				t.Fatal("target exposed credential", err)
			}
		})
	}
}

func TestUserPlaybackTargetDeviceTransactionAndConcurrentConfigurations(t *testing.T) {
	s, r, accounts, _ := deviceSlotFixture(t)
	accounts[1] = poolTestAccount("b", accounts[0].UID)
	accounts[0].Name, accounts[1].Name = "同名配置", "同名配置"
	settings := s.Pool.Settings()
	settings.MaxSessions = 2
	ctx := context.Background()
	if err := s.Pool.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	first := deviceSlotIssue(t, s, r, accounts[:1], "phone", "first")
	next, err := deviceSlotPrepare(t, s, r, accounts[1:2], "tv", "second")
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := next.commitDevicePlayback(canceled); err == nil {
		t.Fatal("canceled commit succeeded")
	}
	for _, l := range s.UserPlayback.List() {
		if l.Key == next.userLease.Record.Key && l.Target.AccountID != "" {
			t.Fatal("failed transaction published target")
		}
	}
	if err := next.commitDevicePlayback(ctx); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, 2)
	for _, l := range s.UserPlayback.List() {
		got[l.Target.AccountID] = true
	}
	if len(got) != 2 || !got[first.account.ID] || !got[next.account.ID] {
		t.Fatal("different configurations on one UID were merged", got)
	}
	restarted, err := NewUserPlayback(ctx, s.db)
	if err != nil || len(restarted.List()) != 2 {
		t.Fatal("atomic target snapshots were not persisted", err)
	}
}

func TestUserPlaybackTargetHandoffSnapshot(t *testing.T) {
	s, r, _, _, _ := handoffFixture(t)
	a := prepareHandoff(t, s, r)
	a.account, a.record = a.poolLease.Account, r
	a.account.Name = "接替配置"
	if err := a.commitHandoff(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := s.UserPlayback.List()[0].Target
	if want.AccountID != a.account.ID || want.AccountName != "接替配置" {
		t.Fatal("handoff target not captured", want)
	}
	// A later playback event supplies evidence, not a new credential selection.
	if err := s.ApplyPlaybackEvent(context.Background(), deviceSlotEvent(s, a), false); err != nil {
		t.Fatal(err)
	}
	if s.UserPlayback.List()[0].Target != want {
		t.Fatal("playback evidence replaced successful target")
	}
}
