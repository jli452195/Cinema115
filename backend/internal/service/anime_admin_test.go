package service

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type animePrimaryFake struct {
	account P115Account
	err     error
}

func (f *animePrimaryFake) Primary(expected string) (P115Account, error) {
	if f.err != nil {
		return P115Account{}, f.err
	}
	if expected != "" && expected != f.account.ID {
		return P115Account{}, ErrAccountChanged
	}
	return f.account, nil
}

type animeDirectoryFake struct {
	calls int
	hook  func()
	fail  error
}

func (f *animeDirectoryFake) ReadDirectory(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
	f.calls++
	if cookie != "synthetic-cookie" {
		return p115.DriveDirectory{}, errors.New("wrong credential snapshot")
	}
	if err := ctx.Err(); err != nil {
		return p115.DriveDirectory{}, err
	}
	if f.hook != nil {
		f.hook()
	}
	return p115.DriveDirectory{CID: cid, Path: "/server/" + cid}, f.fail
}

func animeAdminFixture(t *testing.T) (*AnimeAdminService, *bindingMikanFake, *animeDirectoryFake, AnimeSaveInput) {
	t.Helper()
	p, f, db, c, _ := previewService(t)
	account := P115Account{ID: "primary", UID: "1234", Name: "主账号", Revision: 1, Cookie: "synthetic-cookie", Primary: true, Ownership: "admin"}
	if err := db.SaveP115Accounts(context.Background(), storage.P115AccountState{Accounts: []storage.P115AccountRow{{ID: account.ID, UID: account.UID, Name: account.Name, Revision: 1, Primary: true, Ownership: "admin", Cookie: []byte("synthetic ciphertext")}}}); err != nil {
		t.Fatal(err)
	}
	d := &animeDirectoryFake{}
	s := &AnimeAdminService{Store: db, Bindings: p.Bindings, Previews: p, Accounts: &animePrimaryFake{account: account}, Directories: d}
	in := AnimeSaveInput{SettingsRevision: 1, Config: AnimeSaveConfig{Entry: AnimeEntryRef{ID: c.Chapter.ID}, Sources: c.Sources}}
	in.Config.Sources[0].RunWhenEnabled = true
	s.Naming = &AnimeNamingService{Metadata: &animeNamingFake{}}
	season := 2
	target, err := s.Naming.Target(context.Background(), AnimeNamingTargetInput{TMDBID: "117061", Season: &season}, "")
	if err != nil {
		t.Fatal(err)
	}
	in.Config.Naming = &model.AnimeNamingInput{TMDBID: target.TMDBID, Season: season, Fingerprint: target.Fingerprint, Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{First: 25, SourceBase: 25, TargetSeason: 2, TargetBase: 1}}}}}
	return s, f, d, in
}
func animeSaveReadySettings(t *testing.T, s *AnimeAdminService) model.AnimeSettings {
	t.Helper()
	v, err := s.SaveSettings(context.Background(), "admin", "settings_001", AnimeSettingsInput{ExpectedRevision: 1, DownloadCID: "100", OrganizeCID: bindingPtr("200"), ExpectedPrimaryAccountID: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAnimeAdminSettingsReadbackReplayAndAccountChange(t *testing.T) {
	s, _, d, _ := animeAdminFixture(t)
	ctx := context.Background()
	in := AnimeSettingsInput{ExpectedRevision: 1, DownloadCID: "100", OrganizeCID: bindingPtr("100"), ExpectedPrimaryAccountID: "primary"}
	first, err := s.SaveSettings(ctx, "admin", "settings_same", in)
	if err != nil || d.calls != 1 || *first.DownloadPath != "/server/100" || *first.OrganizePath != "/server/100" {
		t.Fatal(first, d.calls, err)
	}
	d.fail = errors.New("upstream secret Cookie must not reach response")
	again, err := s.SaveSettings(ctx, "admin", "settings_same", in)
	if err != nil || d.calls != 1 || !reflect.DeepEqual(first, again) {
		t.Fatal(again, d.calls, err)
	}
	in.DownloadCID = "200"
	if _, err := s.SaveSettings(ctx, "admin", "settings_same", in); !errors.Is(err, storage.ErrAnimeIdempotency) {
		t.Fatal(err)
	}
	in.ExpectedRevision = 2
	d.fail = nil
	d.hook = func() {
		state, e := s.Store.LoadP115Accounts(ctx)
		if e != nil {
			t.Fatal(e)
		}
		state.Accounts[0].Revision++
		state.Accounts[0].ID = "new-primary"
		if e := s.Store.SaveP115Accounts(ctx, state); e != nil {
			t.Fatal(e)
		}
		d.hook = nil
	}
	if _, err := s.SaveSettings(ctx, "admin", "settings_race", in); !errors.Is(err, storage.ErrAnimeAccountChanged) {
		t.Fatal("account changed during read accepted", err)
	}
	v, err := s.Store.GetAnimeSettings(ctx)
	if err != nil || v.Revision != 2 || *v.DownloadCID != "100" {
		t.Fatal(v, err)
	}
}

func TestAnimeAdminLocalValidationAndExecutionGate(t *testing.T) {
	s, f, _, in := animeAdminFixture(t)
	ctx := context.Background()
	bad := in
	bad.Config.Sources = append([]model.AnimeSourceConfig{}, in.Config.Sources...)
	bad.Config.Sources[0].Role = "unknown"
	if _, err := s.SaveDefinition(ctx, "admin", "invalid_001", "", bad); !errors.Is(err, storage.ErrAnimeInvalid) || f.reads != 0 {
		t.Fatal(err, f.reads)
	}
	in.Config.Sources[0].Status = "enabled"
	if _, err := s.SaveDefinition(ctx, "admin", "enabled_001", "", in); !errors.Is(err, storage.ErrAnimeCapability) || f.reads != 0 {
		t.Fatal(err, f.reads)
	}
	in.Config.Sources[0].Status = "paused"
	if _, err := s.SaveDefinition(ctx, "admin", "short", "", in); !errors.Is(err, storage.ErrAnimeInvalid) || f.reads != 0 {
		t.Fatal(err, f.reads)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.SaveDefinition(canceled, "admin", "canceled_01", "", in); !errors.Is(err, context.Canceled) || f.reads != 0 {
		t.Fatal(err, f.reads)
	}
}

func TestAnimeAdminMikanOnlyDefinitionAndLocalDetailAfterDeletion(t *testing.T) {
	s, f, _, in := animeAdminFixture(t)
	ctx := context.Background()
	in.Config.Entry = AnimeEntryRef{EditionKey: "mikan-edition"}
	in.SettingsRevision = animeSaveReadySettings(t, s).Revision
	r, err := s.SaveDefinition(ctx, "admin", "mikan_only_01", "", in)
	if err != nil {
		t.Fatal(err)
	}
	f.read = func(context.Context, mikan.SourceRequest) (mikan.SourcePage, error) {
		t.Fatal("local read called deleted upstream")
		return mikan.SourcePage{}, bangumi.ErrNotFound
	}
	v, err := s.Subscription(ctx, r.ID)
	if err != nil || v.Entry.Title != "测试番剧" || v.Entry.BangumiID != nil {
		t.Fatal(v, err)
	}
	list, err := s.Subscriptions(ctx, storage.AnimeSubscriptionFilter{Page: 1, PageSize: 1})
	if err != nil || list.Total != 1 || list.Items[0].ID != r.ID {
		t.Fatal(list, err)
	}
}

func animeHTTPFreeCompleteSource(prior model.AnimeSource) model.AnimeSourceConfig {
	v := animeSourceConfig(prior)
	v.Status = "paused"
	v.ItemID = bindingPtr("2259")
	v.GroupID = bindingPtr("45")
	v.GroupName = bindingPtr("client label")
	v.Binding = model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: bindingPtr(1), Last: bindingPtr(100)}}
	v.Rules = model.AnimeRules{SchemaVersion: 1, Match: []string{"(?i)1080p"}, MinimumEpisode: bindingPtr(1)}
	return v
}

func TestAnimeAdminSourceEffectiveStatesPreserveIntent(t *testing.T) {
	for _, tc := range []struct {
		parent, configured string
		run                bool
		state, blocked     string
	}{
		{"paused", "draft", true, "draft", ""}, {"removed", "draft", true, "draft", ""}, {"removed", "removed", true, "removed", ""},
		{"archived", "paused", true, "stopped", "subscription"}, {"enabled", "paused", true, "paused", "source"}, {"enabled", "enabled", false, "paused", "source"},
		{"paused", "enabled", true, "paused", "subscription"}, {"draft", "enabled", true, "paused", "subscription"}, {"enabled", "enabled", true, "enabled", ""},
	} {
		state, blocked := animeSourceEffectiveState(tc.parent, model.AnimeSource{Status: tc.configured, RunWhenEnabled: tc.run})
		if state != tc.state || blocked != tc.blocked {
			t.Fatal(tc, state, blocked)
		}
	}
}

func TestAnimeAdminConcurrentSameKeyReturnsOriginalReceipt(t *testing.T) {
	s, _, _, in := animeAdminFixture(t)
	ctx := context.Background()
	in.SettingsRevision = animeSaveReadySettings(t, s).Revision
	s.Naming.Metadata = &animeDefinitionConcurrentNaming{AnimeNamingMetadata: s.Naming.Metadata}
	const n = 8
	results := make([]model.AnimeConfigReceipt, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results[index], errs[index] = s.SaveDefinition(ctx, "admin", "parallel_same_key", "", in)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil || !reflect.DeepEqual(results[0], results[i]) {
			t.Fatal("same-key concurrent request diverged", i, results[i], err)
		}
	}
	list, err := s.Subscriptions(ctx, storage.AnimeSubscriptionFilter{})
	if err != nil || list.Total != 1 {
		t.Fatal("same-key duplicate write", list, err)
	}
}
