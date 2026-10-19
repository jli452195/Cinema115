package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/anibt"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/model"
	"sync"
	"testing"
	"time"
)

type anibtFake struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *anibtFake) Fetch(ctx context.Context, path string, _ int64) ([]byte, error) {
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
	if strings.Contains(path, "/groups?") {
		name = "groups.json"
	}
	if strings.HasPrefix(path, "/rss/") {
		name = "rss.xml"
	}
	return os.ReadFile("../client/anibt/testdata/" + name)
}
func TestAnibtProviderPreviewAndSave(t *testing.T) {
	s, m, _, in := animeAdminFixture(t)
	f := &anibtFake{}
	a := NewAnibtService(f)
	a.spacing = 0
	s.Bindings.Anibt = a
	source := &in.Config.Sources[0]
	source.Provider = "anibt"
	source.ItemID = bindingPtr("630160")
	source.GroupID = bindingPtr("lolihouse")
	source.Binding.Scope = &model.AnimeScope{Kind: "feed"}
	source.Binding.ChapterCorrespondence = nil
	source.Binding.Review = &model.AnimeSourceReview{Method: "source_review", ItemID: "630160", GroupID: "lolihouse"}
	source.Rules.MinimumEpisode = bindingPtr(1)
	ctx := context.Background()
	out, e := s.SaveConfiguration(ctx, "admin", "anibt_create_001", "", in)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := s.Store.GetAnimeSubscription(ctx, out.ID, false)
	if e != nil || saved.Sources[0].Provider != "anibt" || *saved.Sources[0].GroupName != "LoliHouse" || m.reads != 0 {
		t.Fatalf("provider persistence: %+v %v", saved, e)
	}
	c := in.Config.Sources[0]
	b := animeMikanTestBudget(t, nil)
	p, e := s.Bindings.ReadSource(ctx, b, AnimeSourceInput{Kind: "history", Source: animefeed.NewSource(c.Provider, *c.ItemID, *c.GroupID), Take: 50})
	if e != nil || p.Completeness != "visible_end" || len(p.Releases) != 3 || p.Source.Site() != "anibt" {
		t.Fatalf("preview: %+v %v", p, e)
	}
	if _, e = s.Bindings.ReadSource(ctx, animeMikanTestBudget(t, nil), AnimeSourceInput{Kind: "history", Source: animefeed.NewSource("anibt", "630160", "other"), Take: 50}); e == nil {
		t.Fatal("cross group accepted")
	}
	stats := b.Stats()
	if stats.Requests != 1 || stats.TotalRawCount != 3 {
		t.Fatal(stats)
	}
	if _, e = s.Bindings.ReadSource(ctx, animeMikanTestBudget(t, nil), AnimeSourceInput{Kind: "history", Source: p.Source, Take: 101}); e == nil {
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
	if e != nil || preview.Batch == nil || len(preview.Batch.Sources) != 1 || len(preview.Batch.Sources[0].Items) != 3 {
		t.Fatalf("stored AniBT preview: %+v %v", preview, e)
	}
	if preview.Batch.Sources[0].Items[0].Release.Provider != "anibt" {
		t.Fatal("preview provider lost")
	}
	old := saved.Sources[0]
	replacement := in.Config.Sources[0]
	replacement.Provider, replacement.Ref = "mikan", "replacement"
	replacement.ItemID, replacement.GroupID = bindingPtr("2259"), bindingPtr("45")
	replacement.Binding.Review = &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}
	in.ExpectedRevision = saved.Revision
	in.Config.Sources = []model.AnimeSourceConfig{replacement}
	in.Config.RemovedSourceIDs = []model.AnimeSourceRemoval{{ID: old.ID, ExpectedRevision: old.Revision}}
	if _, e = s.SaveConfiguration(ctx, "admin", "replace_anibt_001", out.ID, in); e != nil {
		t.Fatal("cross-provider replacement", e)
	}
	updated, e := s.Store.GetAnimeSubscription(ctx, out.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	removed, active := false, false
	for _, row := range updated.Sources {
		removed = removed || row.ID == old.ID && row.Status == "removed" && row.Provider == "anibt"
		active = active || row.ID != old.ID && row.Status != "removed" && row.Provider == "mikan"
	}
	if !removed || !active || updated.EntryID != saved.EntryID {
		t.Fatal("replacement lost identity/history", updated)
	}
	if _, e = s.Store.GetAnimePreview(ctx, "admin", preview.Batch.ID); e != nil {
		t.Fatal("old preview lost", e)
	}
}
func TestAnibtCacheIsolationFailureAndCooldown(t *testing.T) {
	f := &anibtFake{}
	s := NewAnibtService(f)
	s.spacing = 0
	ctx := context.Background()
	a, e := s.Catalog(ctx, "2026-10", "", false)
	if e != nil {
		t.Fatal(e)
	}
	title := a.Items[0].Title
	a.Items[0].Title = "changed"
	*a.Items[0].Episodes = 999
	b, e := s.Catalog(ctx, "2026-10", "", false)
	if e != nil || b.Items[0].Title != title || f.calls != 1 || *b.Items[0].Episodes == 999 {
		t.Fatal("cache aliases", e)
	}
	f.err = anibt.ErrInvalid
	if _, e = s.Catalog(ctx, "2026-10", "", true); !errors.Is(e, anibt.ErrInvalid) {
		t.Fatal(e)
	}
	if _, e = s.Catalog(ctx, "2026-10", "", false); e != nil || f.calls != 2 {
		t.Fatal("failed refresh erased success", e)
	}
	f.err = &anibt.HTTPError{Status: 429, RetryAt: time.Now().Add(time.Minute)}
	_, _ = s.Catalog(ctx, "", "search", true)
	calls := f.calls
	_, e = s.Catalog(ctx, "", "other", false)
	var h *anibt.HTTPError
	if !errors.As(e, &h) || h.Status != 429 || f.calls != calls {
		t.Fatal("cooldown not shared", e)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = s.Catalog(cancelCtx, "2026-10", "", false); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
