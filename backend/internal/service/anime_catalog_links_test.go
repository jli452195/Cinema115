package service

import (
	"context"
	"errors"
	"testing"

	"strm115/backend/internal/client/animefeed"
)

func TestAnimeCatalogProvidersPersistObservedLinksWithoutExtraReads(t *testing.T) {
	s, _, _, _ := animeAdminFixture(t)
	ctx := context.Background()
	bindings, m, _ := bindingTestService()
	bindings.Mikan.CatalogLinks = s.Store
	if _, err := bindings.Mikan.ReadAnimeSourceEntry(ctx, animeMikanTestBudget(t, nil), "2259", false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Store.AnimeCatalogStatuses(ctx, "mikan", []string{"2259"})
	if err != nil || got[0].BangumiID == nil || *got[0].BangumiID != "278826" || m.reads != 1 {
		t.Fatal(got, err)
	}
	if _, err := bindings.Mikan.ReadAnimeSourceEntry(ctx, animeMikanTestBudget(t, nil), "2259", false); err != nil || m.reads != 1 {
		t.Fatal("cache miss", err)
	}
	aFake := &anibtFake{}
	a := NewAnibtService(aFake)
	a.spacing = 0
	a.CatalogLinks = s.Store
	gFake := &gardenFake{}
	g := NewGardenService(gFake)
	g.spacing = 0
	g.CatalogLinks = s.Store
	if _, err := a.Catalog(ctx, "2026-10", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Catalog(ctx, "2026-10", "", false); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"anibt", "624923"}, {"garden", "634587"}} {
		got, err := s.Store.AnimeCatalogStatuses(ctx, pair[0], []string{pair[1]})
		if err != nil || got[0].BangumiID == nil || *got[0].BangumiID != pair[1] {
			t.Fatal(pair, got, err)
		}
	}
	if aFake.calls != 1 || gFake.calls != 1 {
		t.Fatal("mapping persistence made extra upstream reads", aFake.calls, gFake.calls)
	}
	aFake.err = animefeed.ErrInput
	if _, err := a.Catalog(ctx, "2026-10", "", true); !errors.Is(err, animefeed.ErrInput) {
		t.Fatal(err)
	}
	got, err = s.Store.AnimeCatalogStatuses(ctx, "anibt", []string{"624923"})
	if err != nil || got[0].BangumiID == nil {
		t.Fatal("upstream failure erased verified identity", got, err)
	}
}
