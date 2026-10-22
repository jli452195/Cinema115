package service

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

// The provider fixtures deliberately represent different shows. This verifies
// composition after explicit source confirmation, not a real cross-site match.
func TestAnimeStageMixedProvidersSaveAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, m, _, in := animeAdminFixture(t)
	accounts, err := s.Store.LoadP115Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.Store.GetAnimeEntry(ctx, in.Config.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Own the path so closing and reopening really replaces the SQLite handle.
	dbPath := filepath.Join(t.TempDir(), "stage.sqlite")
	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	accounts.Revision = 0 // The new database has no account-state revision yet.
	if err := db.SaveP115Accounts(ctx, accounts); err != nil {
		t.Fatal(err)
	}
	entry.ID, entry.Revision = "", 0
	entry, err = db.SaveAnimeEntry(ctx, entry)
	if err != nil {
		t.Fatal(err)
	}
	s.Store, s.Bindings.Entries, s.Previews.Repository = db, db, db
	// Provider fixtures timestamp reads with the wall clock. Do not retain the
	// Mikan-only helper's frozen clock, which could precede a later site read.
	s.Previews.now = nil
	settings := animeSaveReadySettings(t, s)
	in.SettingsRevision, in.Config.Entry.ID = settings.Revision, entry.ID
	a := NewAnibtService(&anibtFake{})
	a.spacing = 0
	g := NewGardenService(&gardenFake{})
	g.spacing = 0
	s.Bindings.Anibt, s.Bindings.Garden = a, g
	s.Naming = &AnimeNamingService{Metadata: &animeNamingFake{}}
	season := 2
	target, err := s.Naming.Target(ctx, AnimeNamingTargetInput{TMDBID: "117061", Season: &season}, "")
	if err != nil {
		t.Fatal(err)
	}
	in.Config.Naming = &model.AnimeNamingInput{TMDBID: target.TMDBID, Season: season, Fingerprint: target.Fingerprint}
	for i, spec := range []struct {
		ref, provider, item, group string
		minimum                    int
	}{
		{"main", "mikan", "2259", "45", 30},
		{"backup-anibt", "anibt", "630160", "lolihouse", 2},
		{"backup-garden", "garden", "623249", "dmhy_f776", 1},
	} {
		source := model.AnimeSourceConfig{
			Ref: spec.ref, Role: "standby", Position: i, Status: "paused", RunWhenEnabled: true,
			Provider: spec.provider, ItemID: bindingPtr(spec.item), GroupID: bindingPtr(spec.group), GroupName: bindingPtr("browser label"),
			Binding: model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: spec.item, GroupID: spec.group}},
			Rules:   model.AnimeRules{SchemaVersion: 1, MinimumEpisode: bindingPtr(spec.minimum), Match: []string{"(?i)1080p"}},
		}
		if i == 0 {
			source.Role = "main"
			in.Config.Sources = []model.AnimeSourceConfig{source}
		} else {
			in.Config.Sources = append(in.Config.Sources, source)
		}
		in.Config.Naming.Sources = append(in.Config.Naming.Sources, model.AnimeSourceMapping{SourceRef: spec.ref, Segments: []model.AnimeMappingSegment{{First: spec.minimum, SourceBase: spec.minimum, TargetSeason: 2, TargetBase: 1}}})
	}
	c := animepreview.Config{Chapter: animepreview.Chapter{ID: entry.ID, BangumiID: entry.BangumiID, EditionKey: entry.EditionKey, Revision: entry.Revision}, Settings: settings, Sources: in.Config.Sources}
	var batch animepreview.Batch
	refs := make([]storage.AnimePreviewRef, 0, 3)
	for _, source := range in.Config.Sources {
		out, err := s.Previews.Generate(ctx, "admin", AnimePreviewInput{Config: c, SourceRef: source.Ref, Reuse: refs})
		if err != nil || out.Batch == nil || len(out.Issues) != 0 {
			t.Fatalf("generate %s: %+v %v", source.Ref, out, err)
		}
		batch = *out.Batch
		refs = refs[:0]
		for i := range batch.Sources {
			refs = append(refs, servicePreviewRef(batch, i))
		}
	}
	if len(batch.Sources) != 3 || batch.Budget().TotalRawCount != 7 {
		t.Fatal("mixed-provider snapshots or shared budget lost", batch.Budget(), len(batch.Sources))
	}
	receipt, err := s.SaveDefinition(ctx, "admin", "stage_create_001", "", in)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Store.GetAnimeSubscription(ctx, receipt.ID, false)
	if err != nil || before.Naming == nil || len(before.Sources) != 3 {
		t.Fatalf("mixed configuration: %+v %v", before, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	// Rebuild every service and cache. An accidental upstream read must fail;
	// retaining the previous in-memory caches would hide such a regression.
	bindings, offlineMikan, subjects := bindingTestService()
	offlineMikan.read = func(context.Context, mikan.SourceRequest) (mikan.SourcePage, error) {
		return mikan.SourcePage{}, errors.New("upstream unavailable")
	}
	subjects.err = errors.New("subject deleted")
	offlineAnibt, offlineGarden := &anibtFake{err: errors.New("site unavailable")}, &gardenFake{err: errors.New("site unavailable")}
	bindings.Entries, bindings.Anibt, bindings.Garden = reopened, NewAnibtService(offlineAnibt), NewGardenService(offlineGarden)
	naming := &animeNamingFake{offline: true}
	s = &AnimeAdminService{Store: reopened, Bindings: bindings, Previews: &AnimePreviewService{Repository: reopened, Bindings: bindings}, Accounts: s.Accounts, Naming: &AnimeNamingService{Metadata: naming}}
	after, err := reopened.GetAnimeSubscription(ctx, receipt.ID, false)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("restart changed configuration", err)
	}
	restored, err := reopened.GetAnimePreview(ctx, "admin", batch.ID)
	if err != nil || batch.ID != restored.ID || len(restored.Sources) != len(batch.Sources) {
		t.Fatal("restart changed preview choices or lifetime", err)
	}
	if got, err := s.SaveDefinition(ctx, "admin", "stage_create_001", "", in); err != nil || !reflect.DeepEqual(got, receipt) {
		t.Fatal("restart lost idempotent receipt", got, err)
	}
	edit := AnimeSaveInput{ExpectedRevision: after.Revision, SettingsRevision: settings.Revision, Config: AnimeSaveConfig{
		Entry:  AnimeEntryRef{ID: after.EntryID},
		Naming: &model.AnimeNamingInput{TMDBID: after.Naming.Target.TMDBID, Season: 2, Fingerprint: after.Naming.Target.Fingerprint, Sources: after.Naming.Sources},
	}}
	for _, source := range after.Sources {
		v := animeSourceConfig(source)
		if source.Role == "main" {
			v.Rules.MinimumEpisode = bindingPtr(31)
		}
		edit.Config.Sources = append(edit.Config.Sources, v)
	}
	if _, err := s.SaveDefinition(ctx, "admin", "stage_edit_001", after.ID, edit); err != nil {
		t.Fatal("offline edit of verified sources failed", err)
	}
	if _, err := s.SaveDefinition(ctx, "admin", "stage_stale_001", after.ID, edit); !errors.Is(err, storage.ErrAnimeConflict) {
		t.Fatal("stale revision accepted after restart", err)
	}
	view, err := s.Subscription(ctx, after.ID)
	if err != nil || view.Revision != 2 || !view.Readiness.Ready || view.ReleaseStats.SelectedCount != 0 || view.JobStats.TotalCount != 0 || view.FirstEnabledAt == nil {
		t.Fatalf("configuration projected as executable work: %+v %v", view, err)
	}
	for _, source := range view.Sources {
		rawEpisode, minimum := 30, 31
		switch source.Provider {
		case "anibt":
			rawEpisode, minimum = 2, 2
		case "garden":
			rawEpisode, minimum = 1, 1
		}
		projection := view.Naming.Project(source.ID, model.AnimeReleaseParse{Kind: "episode", Episode: &rawEpisode}, *source.GroupName)
		if projection.Status != "mapped" || *projection.Season != 2 || *projection.Episode != 1 || *source.Rules.MinimumEpisode != minimum || source.NextCheckAt == nil || source.EffectiveState != "enabled" {
			t.Fatal("source numbering, minimum or execution boundary changed", source, projection)
		}
	}
	list, err := s.Subscriptions(ctx, storage.AnimeSubscriptionFilter{BangumiID: "123"})
	if err != nil || list.Total != 1 || list.Items[0].ID != after.ID {
		t.Fatal("local list unavailable offline", list, err)
	}
	if offlineMikan.reads != 0 || subjects.calls != 0 || offlineAnibt.calls != 0 || offlineGarden.calls != 0 || naming.calls != 0 || m.searches != 0 {
		t.Fatal("local recovery or receipt replay contacted upstream")
	}
}
