package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

type memoryUserPlaybackStore struct {
	doc  model.UserPlaybackDocument
	fail bool
}

func (s *memoryUserPlaybackStore) UserPlayback(context.Context) (model.UserPlaybackDocument, error) {
	return s.doc, nil
}
func (s *memoryUserPlaybackStore) SaveUserPlayback(_ context.Context, d model.UserPlaybackDocument) error {
	if s.fail {
		return errors.New("disk failure")
	}
	s.doc = d
	return nil
}
func userPlaybackFixture(t *testing.T) (*UserPlayback, *memoryUserPlaybackStore, model.MemberPlayback) {
	t.Helper()
	store := &memoryUserPlaybackStore{}
	p, err := NewUserPlayback(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	return p, store, model.MemberPlayback{ServerID: "server", UserID: "profile", EmbyID: "emby-user"}
}
func userPlaybackReq(id string) PoolRequest {
	return PoolRequest{Session: DirectLinkKey("token", id), PlaySession: id, Item: "movie", Source: "source"}
}

func TestUserPlaybackAtomicCompetition(t *testing.T) {
	p, _, r := userPlaybackFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	wins := make(chan *UserPlaybackLease, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, err := p.Acquire(ctx, r, userPlaybackReq(fmt.Sprint(i)))
			if err == nil {
				wins <- l
			} else if !errors.Is(err, ErrUserPlaybackConflict) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	close(wins)
	count := 0
	for l := range wins {
		count++
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 || len(p.List()) != 0 {
		t.Fatal(count, p.List())
	}
}
func TestUserPlaybackSameSessionReferencesAndIsolation(t *testing.T) {
	p, _, r := userPlaybackFixture(t)
	ctx := context.Background()
	req := userPlaybackReq("one")
	l, err := p.Acquire(ctx, r, req)
	if err != nil {
		t.Fatal(err)
	}
	other, err := p.Acquire(ctx, r, req)
	if err != nil {
		t.Fatal(err)
	}
	if l.Record.Generation != other.Record.Generation {
		t.Fatal("duplicate slot")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if !other.Current() {
		t.Fatal("first failure released live preparation")
	}
	if err := other.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	changed := req
	changed.Session = DirectLinkKey("other-token", req.PlaySession)
	if _, err := p.Acquire(ctx, r, changed); !errors.Is(err, ErrUserPlaybackConflict) {
		t.Fatal("other token joined", err)
	}
	changed = req
	changed.Source = "other-version"
	if _, err := p.Acquire(ctx, r, changed); !errors.Is(err, ErrUserPlaybackConflict) {
		t.Fatal(err)
	}
	r.EmbyID = "other-user"
	second, err := p.Acquire(ctx, r, req)
	if err != nil {
		t.Fatal("users share slot", err)
	}
	defer second.Close()
}
func TestUserPlaybackStopGenerationAndAdminRelease(t *testing.T) {
	p, _, r := userPlaybackFixture(t)
	ctx := context.Background()
	req := userPlaybackReq("one")
	l, err := p.Acquire(ctx, r, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Event(ctx, l.Record.EventKey, true, l.Record.Generation); err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(ctx); !errors.Is(err, ErrUserPlaybackStopped) {
		t.Fatal("late commit issued", err)
	}
	_ = l.Close()
	replay, err := p.Acquire(ctx, r, req)
	if err != nil {
		t.Fatal("normal stop blocked in-place replay", err)
	}
	if replay.Record.Generation == l.Record.Generation {
		t.Fatal("replay reused removed generation")
	}
	if matched, err := p.Event(ctx, l.Record.EventKey, true, l.Record.Generation); err != nil || matched || !replay.Current() {
		t.Fatal("delayed accepted stop released replay", err)
	}
	if _, err := p.Acquire(ctx, r, userPlaybackReq("two")); !errors.Is(err, ErrUserPlaybackConflict) {
		t.Fatal("replay did not exclude a second stream", err)
	}
	if err := replay.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := p.Acquire(ctx, r, userPlaybackReq("two"))
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err := p.Event(ctx, l.Record.EventKey, true, l.Record.Generation); err != nil {
		t.Fatal(err)
	}
	if !next.Current() {
		t.Fatal("old stop released new movie")
	}
	if err := p.Release(ctx, next.Record.Key, l.Record.Generation); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("stale admin release", err)
	}
	if err := p.Release(ctx, next.Record.Key, next.Record.Generation); err != nil {
		t.Fatal(err)
	}
	if next.Current() {
		t.Fatal("admin release did not fence old attempt")
	}
	if _, err := p.Acquire(ctx, r, userPlaybackReq("two")); !errors.Is(err, ErrUserPlaybackStopped) {
		t.Fatal("admin-released session revived", err)
	}
}
func TestUserPlaybackPersistenceRestartExpiryAndReconcile(t *testing.T) {
	p, store, r := userPlaybackFixture(t)
	now := time.Now()
	p.now = func() time.Time { return now }
	ctx := context.Background()
	req := userPlaybackReq("one")
	store.fail = true
	if _, err := p.Acquire(ctx, r, req); err == nil || len(p.List()) != 0 {
		t.Fatal("uncommitted admission published")
	}
	store.fail = false
	l, err := p.Acquire(ctx, r, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	restarted, err := NewUserPlayback(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = p.now
	if _, err := restarted.Acquire(ctx, r, userPlaybackReq("two")); !errors.Is(err, ErrUserPlaybackConflict) {
		t.Fatal("restart lost occupancy", err)
	}
	before := restarted.List()[0].ExpiresAt
	now = now.Add(time.Minute)
	if err := restarted.Reconcile(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if restarted.List()[0].ExpiresAt != before {
		t.Fatal("absent observation changed occupancy")
	}
	if err := restarted.Reconcile(ctx, map[string]bool{l.Record.ObservationKey: true}); err != nil {
		t.Fatal(err)
	}
	if restarted.List()[0].ExpiresAt <= before {
		t.Fatal("matching observation did not renew")
	}
	store.fail = true
	if err := restarted.Release(ctx, l.Record.Key, l.Record.Generation); err == nil || len(restarted.List()) != 1 {
		t.Fatal("failed release published")
	}
	store.fail = false
	now = now.Add(241 * time.Minute)
	if err := restarted.Reconcile(ctx, map[string]bool{l.Record.ObservationKey: true}); err != nil {
		t.Fatal(err)
	}
	if len(restarted.List()) != 0 {
		t.Fatal("expired revived")
	}
}

func TestUserPlaybackPreparationCancelAndCloseFailure(t *testing.T) {
	p, store, r := userPlaybackFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Acquire(ctx, r, userPlaybackReq("one")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	now := time.Now()
	p.now = func() time.Time { return now }
	l, err := p.Acquire(context.Background(), r, userPlaybackReq("one"))
	if err != nil {
		t.Fatal(err)
	}
	store.fail = true
	if err := l.Close(); err == nil {
		t.Fatal("failed cleanup hidden")
	}
	store.fail = false
	now = now.Add(96 * time.Second)
	next, err := p.Acquire(context.Background(), r, userPlaybackReq("two"))
	if err != nil {
		t.Fatal("preparing did not expire", err)
	}
	defer next.Close()
}

func TestUserPlaybackAllSourcesAdmissionBeforeCloud(t *testing.T) {
	for _, source := range []string{"primary", "personal", "assigned"} {
		t.Run(source, func(t *testing.T) {
			s, remote, id := memberFixture(t)
			ctx := context.Background()
			memberScan(t, s, id)
			r := s.record(id)
			// Reserve directly first; every authenticated source must reject the second
			// before resolving a target or touching the cloud.
			r.Source = source
			r.PlaybackOnly = false
			if err := s.save(ctx, r); err != nil {
				t.Fatal(err)
			}
			first, err := s.UserPlayback.Acquire(ctx, r, userPlaybackReq("one"))
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			request, holder := WithUserPlaybackRequest(WithPoolRequest(ctx, userPlaybackReq("two")))
			defer holder.Close()
			u := remote.users[r.EmbyID]
			_, err = s.BeginMediaAuthorization(request, r.EmbyURL, r.EmbyID, false, &u)
			if !errors.Is(err, ErrUserPlaybackConflict) {
				t.Fatal(source, err)
			}
		})
	}
}

func TestUserPlaybackChildIndependentAndDownloadUnrestricted(t *testing.T) {
	s, remote, owner, child := childFixture(t)
	ctx := context.Background()
	first, err := s.UserPlayback.Acquire(ctx, child, userPlaybackReq("child-one"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	request, holder := WithUserPlaybackRequest(WithPoolRequest(ctx, userPlaybackReq("child-two")))
	defer holder.Close()
	u := remote.users[child.EmbyID]
	_, err = s.BeginMediaAuthorization(request, child.EmbyURL, child.EmbyID, false, &u)
	if !errors.Is(err, ErrUserPlaybackConflict) {
		t.Fatal("child bypassed slot", err)
	}
	ownerRecord := s.record(owner)
	ownerReq, ownerHolder := WithUserPlaybackRequest(WithPoolRequest(ctx, userPlaybackReq("owner")))
	defer ownerHolder.Close()
	ownerUser := remote.users[ownerRecord.EmbyID]
	grant, err := s.BeginMediaAuthorization(ownerReq, ownerRecord.EmbyURL, ownerRecord.EmbyID, false, &ownerUser)
	if err != nil {
		t.Fatal("child blocked owner", err)
	}
	defer grant.Close()
	s.settings.AllowDownload = true
	ownerUser.Policy["EnableContentDownloading"] = true
	downloadReq, downloadHolder := WithUserPlaybackRequest(WithPoolRequest(ctx, userPlaybackReq("download")))
	defer downloadHolder.Close()
	download, err := s.BeginMediaAuthorization(downloadReq, ownerRecord.EmbyURL, ownerRecord.EmbyID, true, &ownerUser)
	if err != nil {
		t.Fatal("playback slot blocked authorized download", err)
	}
	defer download.Close()
	if download.userLease != nil {
		t.Fatal("download took playback slot")
	}
}
