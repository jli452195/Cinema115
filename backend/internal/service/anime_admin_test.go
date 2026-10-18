package service

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/animepreview"
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
	in := AnimeSaveInput{SaveMode: "draft", SettingsRevision: 1, Config: AnimeSaveConfig{Entry: AnimeEntryRef{ID: c.Chapter.ID}, Sources: c.Sources}}
	return s, f, d, in
}
func animeSaveReadySettings(t *testing.T, s *AnimeAdminService) model.AnimeSettings {
	t.Helper()
	v, err := s.SaveSettings(context.Background(), "admin", "settings_001", AnimeSettingsInput{ExpectedRevision: 1, DownloadCID: "100", ExpectedPrimaryAccountID: "primary", ExpectedPrimaryAccountRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAnimeAdminDraftReplayAndLocalEdits(t *testing.T) {
	s, f, _, in := animeAdminFixture(t)
	ctx := context.Background()
	in.Config.FormSelections = []model.AnimeFormSelection{{SourceRef: "main", SelectedReleaseIDs: []string{"form-only-release"}, DismissedReleaseIDs: []string{"dismissed-form"}}}
	first, err := s.SaveConfiguration(ctx, "admin", "create_001", "", in)
	if err != nil {
		t.Fatal(err)
	}
	if f.reads != 1 {
		t.Fatal("save read RSS", f.reads)
	}
	sub, err := s.Store.GetAnimeSubscription(ctx, first.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if *sub.Sources[0].GroupName != "字幕组甲" || sub.FirstEnabledAt != nil || sub.Sources[0].ActiveBaselineID != nil || sub.Sources[0].NextCheckAt != nil || sub.Form.FormSelections[0].SourceRef != first.SourceIDs["main"] {
		t.Fatal(sub)
	}
	// Move current settings and make every upstream fail after the response was
	// supposedly lost. Replay must still return exactly the original receipt.
	animeSaveReadySettings(t, s)
	f.read = func(context.Context, mikan.SourceRequest) (mikan.SourcePage, error) {
		t.Fatal("replay/local edit read upstream")
		return mikan.SourcePage{}, mikan.ErrNetwork
	}
	in.Config.Sources[0].GroupName = bindingPtr("different browser label")
	again, err := s.SaveConfiguration(ctx, "admin", "create_001", "", in)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal(again, err)
	}
	in.SaveMode = "paused"
	if _, err := s.SaveConfiguration(ctx, "admin", "create_001", "", in); !errors.Is(err, storage.ErrAnimeIdempotency) {
		t.Fatal("key body conflict hidden", err)
	}
	edit := AnimeSaveInput{SaveMode: "draft", ExpectedRevision: sub.Revision, SettingsRevision: 2, Config: AnimeSaveConfig{Entry: AnimeEntryRef{ID: sub.EntryID}, Sources: []model.AnimeSourceConfig{animeSourceConfig(sub.Sources[0])}, FormSelections: sub.Form.FormSelections}}
	edit.Config.Sources[0].Rules.MinimumEpisode = bindingPtr(31)
	if _, err := s.SaveConfiguration(ctx, "admin", "edit_0001", sub.ID, edit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveConfiguration(ctx, "admin", "edit_0002", sub.ID, edit); !errors.Is(err, storage.ErrAnimeConflict) {
		t.Fatal("stale edit accepted", err)
	}
	v, err := s.Subscription(ctx, sub.ID)
	if err != nil || v.ReleaseStats.SelectedCount != 0 || v.JobStats.TotalCount != 0 || v.DataVersion == "" {
		t.Fatal(v, err)
	}
}

func TestAnimeAdminPausedReadinessAndDraftStandby(t *testing.T) {
	s, _, _, in := animeAdminFixture(t)
	ctx := context.Background()
	in.SaveMode = "paused"
	var incomplete *AnimeConfigError
	if _, err := s.SaveConfiguration(ctx, "admin", "incomplete_01", "", in); !errors.As(err, &incomplete) || incomplete.Code != "CONFIG_INCOMPLETE" {
		t.Fatal(err)
	}
	settings := animeSaveReadySettings(t, s)
	in.SettingsRevision = settings.Revision
	in.Config.Sources = append(in.Config.Sources, model.AnimeSourceConfig{Ref: "backup", Role: "standby", Position: 1, Status: "draft", Provider: "mikan", Binding: model.AnimeBinding{SchemaVersion: 1}, Rules: model.AnimeRules{SchemaVersion: 1}})
	r, err := s.SaveConfiguration(ctx, "admin", "paused_ready", "", in)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.Store.GetAnimeSubscription(ctx, r.ID, false)
	if err != nil || sub.Status != "paused" || len(sub.Sources) != 2 || sub.FirstEnabledAt != nil {
		t.Fatal(sub, err)
	}
	// Omission preserves the unfinished backup. An explicit removal needs CAS.
	edit := AnimeSaveInput{SaveMode: "paused", ExpectedRevision: 1, SettingsRevision: 2, Config: AnimeSaveConfig{Entry: AnimeEntryRef{ID: sub.EntryID}, Sources: []model.AnimeSourceConfig{animeSourceConfig(sub.Sources[0])}}}
	if _, err := s.SaveConfiguration(ctx, "admin", "omit_backup", sub.ID, edit); err != nil {
		t.Fatal(err)
	}
	sub, err = s.Store.GetAnimeSubscription(ctx, r.ID, false)
	if err != nil || len(sub.Sources) != 2 {
		t.Fatal(sub, err)
	}
	edit.ExpectedRevision = 2
	edit.Config.RemovedSourceIDs = []model.AnimeSourceRemoval{{ID: sub.Sources[1].ID, ExpectedRevision: 99}}
	if _, err := s.SaveConfiguration(ctx, "admin", "remove_stale", sub.ID, edit); !errors.Is(err, storage.ErrAnimeConflict) {
		t.Fatal(err)
	}
	edit.Config.RemovedSourceIDs[0].ExpectedRevision = sub.Sources[1].Revision
	if _, err := s.SaveConfiguration(ctx, "admin", "remove_good", sub.ID, edit); err != nil {
		t.Fatal(err)
	}
}

func TestAnimeAdminSettingsReadbackReplayAndAccountChange(t *testing.T) {
	s, _, d, _ := animeAdminFixture(t)
	ctx := context.Background()
	in := AnimeSettingsInput{ExpectedRevision: 1, DownloadCID: "100", OrganizeCID: bindingPtr("100"), ExpectedPrimaryAccountID: "primary", ExpectedPrimaryAccountRevision: 1}
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
	if _, err := s.SaveConfiguration(ctx, "admin", "invalid_001", "", bad); !errors.Is(err, storage.ErrAnimeInvalid) || f.reads != 0 {
		t.Fatal(err, f.reads)
	}
	in.SaveMode = "enabled"
	if _, err := s.SaveConfiguration(ctx, "admin", "enabled_001", "", in); !errors.Is(err, storage.ErrAnimeCapability) || f.reads != 0 {
		t.Fatal(err, f.reads)
	}
	in.SaveMode = "draft"
	if _, err := s.SaveConfiguration(ctx, "admin", "short", "", in); !errors.Is(err, storage.ErrAnimeInvalid) || f.reads != 0 {
		t.Fatal(err, f.reads)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.SaveConfiguration(canceled, "admin", "canceled_01", "", in); !errors.Is(err, context.Canceled) || f.reads != 0 {
		t.Fatal(err, f.reads)
	}
}

func TestAnimeAdminMikanOnlyDraftAndLocalDetailAfterDeletion(t *testing.T) {
	s, f, _, in := animeAdminFixture(t)
	ctx := context.Background()
	in.Config.Entry = AnimeEntryRef{EditionKey: "mikan-edition"}
	in.Config.Sources[0].GroupID = nil
	in.Config.Sources[0].Status = "draft"
	r, err := s.SaveConfiguration(ctx, "admin", "mikan_only_01", "", in)
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

func TestAnimeAdminSavesExpiredPreviewHintsWithoutConsuming(t *testing.T) {
	s, _, _, in := animeAdminFixture(t)
	ctx := context.Background()
	e, err := s.Store.GetAnimeEntry(ctx, in.Config.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.Store.GetAnimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := animepreview.Config{Chapter: animepreview.Chapter{ID: e.ID, BangumiID: e.BangumiID, EditionKey: e.EditionKey, Revision: e.Revision}, Settings: settings, Sources: in.Config.Sources}
	p, err := s.Previews.Generate(ctx, "admin", AnimePreviewInput{Config: c, SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	ref := servicePreviewRef(*p.Batch, 0)
	original := p.Batch.Sources[0]
	s.Previews.now = func() time.Time { return time.UnixMilli(original.ExpiresAt).Add(time.Minute) }
	if original.ValidAt(s.Previews.timestamp()) {
		t.Fatal("test preview is not expired")
	}
	in.Config.PreviewRefs = []model.AnimeFormPreviewRef{{BatchID: ref.BatchID, SourceRef: ref.SourceRef, SourcePreviewID: ref.PreviewID, LoadRevision: ref.LoadRevision}}
	in.Config.FormSelections = []model.AnimeFormSelection{{SourceRef: "main", SelectedReleaseIDs: original.SelectedIDs}}
	r, err := s.SaveConfiguration(ctx, "admin", "expired_hint_01", "", in)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.Store.GetAnimeSubscription(ctx, r.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Form.PreviewRefs[0].SourceRef != "main" || sub.Form.PreviewRefs[0].SourceID != r.SourceIDs["main"] || len(sub.Form.FormSelections[0].SelectedReleaseIDs) != 2 || !r.RequiresRepreview {
		t.Fatal(sub, r)
	}
	edit := AnimeSaveInput{SaveMode: "draft", ExpectedRevision: 1, SettingsRevision: 1, Config: AnimeSaveConfig{Entry: AnimeEntryRef{ID: sub.EntryID}, Sources: []model.AnimeSourceConfig{animeSourceConfig(sub.Sources[0])}, FormSelections: sub.Form.FormSelections, PreviewRefs: sub.Form.PreviewRefs}}
	if _, err := s.SaveConfiguration(ctx, "admin", "expired_hint_02", sub.ID, edit); err != nil {
		t.Fatal("restored hint cannot be saved", err)
	}
	after, err := s.Store.GetAnimePreview(ctx, "admin", ref.BatchID)
	if err != nil || after.Sources[0].T0 != original.T0 || after.Sources[0].ExpiresAt != original.ExpiresAt || after.Sources[0].LoadRevision != original.LoadRevision {
		t.Fatal("save renewed/consumed preview", err)
	}
	foreign := in
	foreign.Config.Entry = AnimeEntryRef{BangumiID: bindingPtr("456"), EditionKey: "original"}
	if _, err := s.SaveConfiguration(ctx, "another-admin", "foreign_hint_01", "", foreign); err == nil {
		t.Fatal("foreign administrator preview accepted")
	}
}

func TestAnimeAdminCompletesDraftSourceIdentityOnce(t *testing.T) {
	s, _, _, in := animeAdminFixture(t)
	ctx := context.Background()
	in.Config.Sources = []model.AnimeSourceConfig{{Ref: "main", Role: "main", Status: "draft", Provider: "mikan", Binding: model.AnimeBinding{SchemaVersion: 1}, Rules: model.AnimeRules{SchemaVersion: 1}}}
	r, err := s.SaveConfiguration(ctx, "admin", "partial_source_01", "", in)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.Store.GetAnimeSubscription(ctx, r.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	settings := animeSaveReadySettings(t, s)
	complete := animeHTTPFreeCompleteSource(sub.Sources[0])
	edit := AnimeSaveInput{SaveMode: "paused", ExpectedRevision: 1, SettingsRevision: settings.Revision, Config: AnimeSaveConfig{Entry: AnimeEntryRef{ID: sub.EntryID}, Sources: []model.AnimeSourceConfig{complete}}}
	// Completion can be previewed before saving the final source identity.
	e, err := s.Store.GetAnimeEntry(ctx, sub.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	c := animepreview.Config{Chapter: animepreview.Chapter{ID: e.ID, BangumiID: e.BangumiID, EditionKey: e.EditionKey, Revision: e.Revision}, SubscriptionID: sub.ID, Revision: 1, Settings: settings, Sources: edit.Config.Sources}
	if _, err := s.Previews.Generate(ctx, "admin", AnimePreviewInput{Config: c, SourceRef: complete.Ref}); err != nil {
		t.Fatal("partial source cannot be previewed", err)
	}
	if _, err := s.SaveConfiguration(ctx, "admin", "complete_source_01", sub.ID, edit); err != nil {
		t.Fatal("draft identity cannot be completed", err)
	}
	got, err := s.Store.GetAnimeSubscription(ctx, sub.ID, false)
	if err != nil || *got.Sources[0].SourceKey != "mikan:2259:45" || got.Sources[0].NamespaceID != sub.Sources[0].NamespaceID || got.Sources[0].Revision != 2 {
		t.Fatal(got, err)
	}
	edit.ExpectedRevision = 2
	edit.Config.Sources[0] = animeSourceConfig(got.Sources[0])
	edit.Config.Sources[0].GroupID = bindingPtr("99")
	if _, err := s.SaveConfiguration(ctx, "admin", "rebind_source_01", sub.ID, edit); !errors.Is(err, storage.ErrAnimeIdentity) {
		t.Fatal("chosen identity rebound", err)
	}
}

func animeHTTPFreeCompleteSource(prior model.AnimeSource) model.AnimeSourceConfig {
	v := animeSourceConfig(prior)
	v.Status = "paused"
	v.ItemID = bindingPtr("2259")
	v.GroupID = bindingPtr("45")
	v.GroupName = bindingPtr("client label")
	v.Binding = model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: bindingPtr(1), Last: bindingPtr(100)}}
	v.Rules = model.AnimeRules{SchemaVersion: 1, Resolution: bindingPtr("1080p"), MinimumEpisode: bindingPtr(1), InitialMode: "from_episode"}
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
	in.Config.Sources = nil
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
			results[index], errs[index] = s.SaveConfiguration(ctx, "admin", "parallel_same_key", "", in)
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
