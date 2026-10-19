package service

import (
	"context"
	"errors"
	"testing"

	"strm115/backend/internal/client/mikan"
)

type catalogReader struct {
	calls int
	fail  bool
}

func (f *catalogReader) Search(context.Context, string, string) (mikan.Result, error) {
	return mikan.Result{}, errors.New("must use scoped catalog")
}
func (f *catalogReader) Catalog(_ context.Context, season, keyword string) (mikan.Catalog, error) {
	f.calls++
	if f.fail {
		return mikan.Catalog{}, mikan.ErrNetwork
	}
	item := mikan.CatalogItem{Bangumi: mikan.Bangumi{ID: "4063", Title: "蜜柑原名"}}
	return mikan.Catalog{Season: season, Days: []mikan.CatalogDay{{Day: 1, Items: []mikan.CatalogItem{item}}}, Items: []mikan.CatalogItem{item}}, nil
}
func TestAnimeCatalogCachesSeparatelyAndKeepsSuccessOnFailure(t *testing.T) {
	reader := &catalogReader{}
	s := NewMikanService(reader, nil)
	ctx := context.Background()
	first, err := s.AnimeCatalog(ctx, "2026-10", "", false)
	if err != nil || first.Provider != "mikan" {
		t.Fatal(first, err)
	}
	first.Days[0].Items[0].Title = "mutated"
	next, err := s.AnimeCatalog(ctx, "2026-10", "", false)
	if err != nil || next.Days[0].Items[0].Title != "蜜柑原名" || reader.calls != 1 {
		t.Fatal(next, err, reader.calls)
	}
	if _, err = s.AnimeCatalog(ctx, "", "test", false); err != nil || reader.calls != 2 {
		t.Fatal(err, reader.calls)
	}
	reader.fail = true
	if _, err = s.AnimeCatalog(ctx, "2026-10", "", true); !errors.Is(err, mikan.ErrNetwork) {
		t.Fatal(err)
	}
	next, err = s.AnimeCatalog(ctx, "2026-10", "", false)
	if err != nil || next.Days[0].Items[0].Title != "蜜柑原名" || reader.calls != 3 {
		t.Fatal(next, err, reader.calls)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.AnimeCatalog(cancelled, "2026-10", "", false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
