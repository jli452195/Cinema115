package service

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/model"
)

type gardenFake struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *gardenFake) Fetch(ctx context.Context, path string, _ int64) ([]byte, error) {
	f.mu.Lock()
	f.calls++
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	name := "calendar.json"
	if strings.HasPrefix(path, "/bgm/subject/") {
		name = "subject.json"
	}
	if strings.HasPrefix(path, "/resources?") {
		u, _ := url.Parse(path)
		name = "resources.json"
		if u.Query().Get("fansub") != "" {
			name = "filtered.json"
		}
		raw, err := os.ReadFile("../client/garden/testdata/" + name)
		if err != nil {
			return nil, err
		}
		var v garden.Resources
		if err = json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		v.Pagination.Size, _ = strconv.Atoi(u.Query().Get("pageSize"))
		return json.Marshal(v)
	}
	return os.ReadFile("../client/garden/testdata/" + name)
}
func TestGardenProviderPreviewAndSave(t *testing.T) {
	s, m, _, in := animeAdminFixture(t)
	in.SettingsRevision = animeSaveReadySettings(t, s).Revision
	f := &gardenFake{}
	a := NewGardenService(f)
	a.spacing = 0
	// Garden records real observation times. Use the real preview clock too,
	// so a slower run cannot put observations after the fixture's frozen time.
	s.Previews.now = nil
	s.Bindings.Garden = a
	source := &in.Config.Sources[0]
	source.Provider = "garden"
	source.ItemID = bindingPtr("623249")
	source.GroupID = bindingPtr("dmhy_f776")
	source.Binding.Scope = &model.AnimeScope{Kind: "feed"}
	source.Binding.ChapterCorrespondence = nil
	source.Binding.Review = &model.AnimeSourceReview{Method: "source_review", ItemID: "623249", GroupID: "dmhy_f776"}
	source.Rules.MinimumEpisode = bindingPtr(1)
	ctx := context.Background()
	out, e := s.SaveDefinition(ctx, "admin", "garden_create_001", "", in)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := s.Store.GetAnimeSubscription(ctx, out.ID, false)
	if e != nil || saved.Sources[0].Provider != "garden" || *saved.Sources[0].GroupName != "天月動漫發佈組" || m.reads != 0 {
		t.Fatalf("provider persistence: %+v %v", saved, e)
	}
	c := in.Config.Sources[0]
	b := animeMikanTestBudget(t, nil)
	p, e := s.Bindings.ReadSource(ctx, b, AnimeSourceInput{Kind: "history", Source: animefeed.NewSource(c.Provider, *c.ItemID, *c.GroupID), Take: 50})
	if e != nil || p.Completeness != "visible_end" || len(p.Releases) != 2 || p.Source.Site() != "garden" {
		t.Fatalf("preview: %+v %v", p, e)
	}
	if _, e = s.Bindings.ReadSource(ctx, animeMikanTestBudget(t, nil), AnimeSourceInput{Kind: "history", Source: animefeed.NewSource("garden", "623249", "dmhy_f999"), Take: 50}); e == nil {
		t.Fatal("cross group accepted")
	}
	stats := b.Stats()
	if stats.Requests != 1 || stats.TotalRawCount != 2 {
		t.Fatal(stats)
	}
	if _, e = s.Bindings.ReadSource(ctx, animeMikanTestBudget(t, nil), AnimeSourceInput{Kind: "history", Source: p.Source, Take: 1001}); e == nil {
		t.Fatal("over source window")
	}
	entry, e := s.Store.GetAnimeEntry(ctx, saved.EntryID)
	if e != nil {
		t.Fatal(e)
	}
	settings, e := s.Store.GetAnimeSettings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	config := animepreview.Config{Chapter: animepreview.Chapter{ID: entry.ID, BangumiID: entry.BangumiID, EditionKey: entry.EditionKey, Revision: entry.Revision}, Settings: settings, Sources: in.Config.Sources}
	preview, e := s.Previews.Generate(ctx, "admin", AnimePreviewInput{Config: config, SourceRef: "main", Take: 50})
	if e != nil || preview.Batch == nil || len(preview.Batch.Sources) != 1 || len(preview.Batch.Sources[0].Items) != 2 {
		t.Fatalf("stored Garden preview: %+v %v", preview, e)
	}
	if preview.Batch.Sources[0].Items[0].Release.Provider != "garden" {
		t.Fatal("preview provider lost")
	}
	old := saved.Sources[0]
	replacement := in.Config.Sources[0]
	replacement.Provider, replacement.Ref = "mikan", "replacement"
	replacement.ItemID, replacement.GroupID = bindingPtr("2259"), bindingPtr("45")
	replacement.Binding.Review = &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}
	in.ExpectedRevision = saved.Revision
	in.Config.Sources = []model.AnimeSourceConfig{replacement}
	in.Config.Naming.Sources[0].SourceRef = "replacement"
	in.Config.RemovedSourceIDs = []model.AnimeSourceRemoval{{ID: old.ID, ExpectedRevision: old.Revision}}
	if _, e = s.SaveDefinition(ctx, "admin", "replace_garden_001", out.ID, in); e != nil {
		t.Fatal("cross-provider replacement", e)
	}
	updated, e := s.Store.GetAnimeSubscription(ctx, out.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	removed, active := false, false
	for _, row := range updated.Sources {
		removed = removed || row.ID == old.ID && row.Status == "removed" && row.Provider == "garden"
		active = active || row.ID != old.ID && row.Status != "removed" && row.Provider == "mikan"
	}
	if !removed || !active || updated.EntryID != saved.EntryID {
		t.Fatal("replacement lost identity/history", updated)
	}
	if _, e = s.Store.GetAnimePreview(ctx, "admin", preview.Batch.ID); e != nil {
		t.Fatal("old preview lost", e)
	}
}
