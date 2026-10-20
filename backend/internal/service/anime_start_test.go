package service

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func newAnimeStartService(t *testing.T) (*AnimeAdminService, *bindingMikanFake, AnimeStartInput) {
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
	c.Chapter = animepreview.Chapter{ID: entry.ID, BangumiID: entry.BangumiID, EditionKey: entry.EditionKey, Revision: entry.Revision}
	c.Sources[0].RunWhenEnabled = true
	preview, err := p.Generate(context.Background(), "admin", AnimePreviewInput{Config: c, SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	season := 2
	target, err := s.Naming.Target(context.Background(), AnimeNamingTargetInput{TMDBID: "117061", Season: &season}, "")
	if err != nil {
		t.Fatal(err)
	}
	in := AnimeStartInput{SettingsRevision: settings.Revision, Chapter: c.Chapter,
		Config:  AnimeSaveConfig{Entry: AnimeEntryRef{ID: entry.ID}, Sources: c.Sources, Naming: &model.AnimeNamingInput{TMDBID: target.TMDBID, Season: season, Fingerprint: target.Fingerprint, Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{First: 25, SourceBase: 25, TargetSeason: 2, TargetBase: 1}}}}}},
		Sources: []storage.AnimeActivationSource{{AnimePreviewRef: servicePreviewRef(*preview.Batch, 0), SelectedIDs: []string{preview.Batch.Sources[0].Items[0].Release.ID}, DismissedIDs: []string{preview.Batch.Sources[0].Items[1].Release.ID}}}}
	// Starting uses the directory snapshot; it must not list/read 115 again.
	s.Directories = animeDownloadDirectoryFunc(func(context.Context, string, string) (p115.DriveDirectory, error) {
		t.Fatal("start read 115")
		return p115.DriveDirectory{}, nil
	})
	return s, feed, in
}

func TestAnimeStartServiceNoRepeatedRSSAndImmutableReplay(t *testing.T) {
	s, feed, in := newAnimeStartService(t)
	ctx := context.Background()
	reads := feed.reads
	before, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.StartSubscription(ctx, "admin", "service_start_01", "", in)
	if err != nil || out.Status != "enabled" || feed.reads != reads {
		t.Fatal(out, err, feed.reads, reads)
	}
	after, err := json.Marshal(in)
	if err != nil || string(before) != string(after) {
		t.Fatal("start mutated caller input", err)
	}
	s.Naming.Metadata.(*animeNamingFake).offline = true
	again, err := s.StartSubscription(ctx, "admin", "service_start_01", "", in)
	if err != nil || !reflect.DeepEqual(out, again) || feed.reads != reads {
		t.Fatal(again, err)
	}
}

func TestAnimeStartServiceConcurrentSameKey(t *testing.T) {
	s, _, in := newAnimeStartService(t)
	s.Naming.Metadata = &animeStartConcurrentNaming{AnimeNamingMetadata: s.Naming.Metadata}
	var wg sync.WaitGroup
	results := make([]model.AnimeConfigReceipt, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.StartSubscription(context.Background(), "admin", "concurrent_start_01", "", in)
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || !reflect.DeepEqual(results[0], results[1]) {
		t.Fatal(results, errs)
	}
}

// The metadata fake counts calls; serialize those counters in concurrent tests.
type animeStartConcurrentNaming struct {
	AnimeNamingMetadata
	mu sync.Mutex
}

func (f *animeStartConcurrentNaming) AnimeDetails(ctx context.Context, id int) (*tmdb.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.AnimeNamingMetadata.AnimeDetails(ctx, id)
}

func (f *animeStartConcurrentNaming) AnimeSeason(ctx context.Context, id, season int) (*tmdb.SeasonDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.AnimeNamingMetadata.AnimeSeason(ctx, id, season)
}
