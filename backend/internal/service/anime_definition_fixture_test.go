package service

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"strm115/backend/internal/anime/preview"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
)

func newAnimeDefinitionService(t *testing.T) (*AnimeAdminService, *bindingMikanFake, AnimeSaveInput) {
	t.Helper()
	f := newAnimeDownloadFixture(t)
	s := f.service
	p, feed, _, c, _ := previewService(t)
	entry, err := s.Store.SaveAnimeEntry(context.Background(), model.AnimeEntry{BangumiID: bindingPtr("123"), EditionKey: "original", Title: "test anime", Metadata: model.AnimeMetadata{SchemaVersion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	p.Repository, p.Bindings.Entries = s.Store, s.Store
	s.Previews, s.Bindings, s.Naming = p, p.Bindings, &AnimeNamingService{Metadata: &animeNamingFake{}}
	a, err := f.accounts.Primary("")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.SaveSettings(context.Background(), "admin", "start_settings", AnimeSettingsInput{ExpectedRevision: f.settings.Revision, DownloadCID: "100", OrganizeCID: bindingPtr("200"), ExpectedPrimaryAccountID: a.ID, ExpectedPrimaryAccountRevision: int64(a.Revision)})
	if err != nil {
		t.Fatal(err)
	}
	c.Settings = settings
	c.Chapter = preview.Chapter{ID: entry.ID, BangumiID: entry.BangumiID, EditionKey: entry.EditionKey, Revision: entry.Revision}
	c.Sources[0].RunWhenEnabled = true
	if _, err := s.Bindings.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), AnimeBindingInput{Entry: AnimeEntryRef{ID: entry.ID}, Source: mikan.Source{ItemID: *c.Sources[0].ItemID, GroupID: *c.Sources[0].GroupID}, Binding: c.Sources[0].Binding}); err != nil {
		t.Fatal(err)
	}
	season := 2
	target, err := s.Naming.Target(context.Background(), AnimeNamingTargetInput{TMDBID: "117061", Season: &season}, "")
	if err != nil {
		t.Fatal(err)
	}
	in := AnimeSaveInput{SettingsRevision: settings.Revision,
		Config: AnimeSaveConfig{Entry: AnimeEntryRef{ID: entry.ID}, Sources: c.Sources, Naming: &model.AnimeNamingInput{TMDBID: target.TMDBID, Season: season, Fingerprint: target.Fingerprint, Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{First: 25, SourceBase: 25, TargetSeason: 2, TargetBase: 1}}}}}}}
	// Starting uses the directory snapshot; it must not list/read 115 again.
	s.Directories = animeDownloadDirectoryFunc(func(context.Context, string, string) (p115.DriveDirectory, error) {
		t.Fatal("start read 115")
		return p115.DriveDirectory{}, nil
	})
	return s, feed, in
}

func TestAnimeDefinitionServiceNoRepeatedRSSAndImmutableReplay(t *testing.T) {
	s, feed, in := newAnimeDefinitionService(t)
	ctx := context.Background()
	reads := feed.reads
	before, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.SaveDefinition(ctx, "admin", "service_start_01", "", in)
	if err != nil || out.Status != "enabled" || feed.reads != reads {
		t.Fatal(out, err, feed.reads, reads)
	}
	select {
	case <-s.animeDiscoveryWakeSignal():
	default:
		t.Fatal("committed start did not wake pending resource verification")
	}
	after, err := json.Marshal(in)
	if err != nil || string(before) != string(after) {
		t.Fatal("start mutated caller input", err)
	}
	s.Naming.Metadata.(*animeNamingFake).offline = true
	again, err := s.SaveDefinition(ctx, "admin", "service_start_01", "", in)
	if err != nil || !reflect.DeepEqual(out, again) || feed.reads != reads {
		t.Fatal(again, err)
	}
	select {
	case <-s.animeDiscoveryWakeSignal():
		t.Fatal("idempotent start replay created another notification")
	default:
	}
}

func TestAnimeDefinitionServiceConcurrentSameKey(t *testing.T) {
	s, _, in := newAnimeDefinitionService(t)
	s.Naming.Metadata = &animeDefinitionConcurrentNaming{AnimeNamingMetadata: s.Naming.Metadata}
	var wg sync.WaitGroup
	results := make([]model.AnimeConfigReceipt, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.SaveDefinition(context.Background(), "admin", "concurrent_start_01", "", in)
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || !reflect.DeepEqual(results[0], results[1]) {
		t.Fatal(results, errs)
	}
}

// The metadata fake counts calls; serialize those counters in concurrent tests.
type animeDefinitionConcurrentNaming struct {
	AnimeNamingMetadata
	mu sync.Mutex
}

func (f *animeDefinitionConcurrentNaming) AnimeDetails(ctx context.Context, id int) (*tmdb.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.AnimeNamingMetadata.AnimeDetails(ctx, id)
}

func (f *animeDefinitionConcurrentNaming) AnimeSeason(ctx context.Context, id, season int) (*tmdb.SeasonDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.AnimeNamingMetadata.AnimeSeason(ctx, id, season)
}
