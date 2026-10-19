package httpapi

import (
	"context"
	"testing"

	"strm115/backend/internal/client/mikan"
)

func (f *animeHTTPMikan) Catalog(_ context.Context, season, keyword string) (mikan.Catalog, error) {
	f.reads++
	return mikan.Catalog{Season: season, Days: []mikan.CatalogDay{{Day: 1, Items: []mikan.CatalogItem{{Bangumi: mikan.Bangumi{ID: "4063", Title: "蜜柑条目"}}}}}, Items: []mikan.CatalogItem{}}, f.err
}

func TestAnimeCatalogProviderIsolationAndReadOnlyBoundary(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	f.call(t, "GET", "/providers", f.user, "", nil, 403)
	f.call(t, "GET", "/providers", f.admin, "", nil, 200)
	for _, provider := range []string{"anibt", "garden"} {
		out := f.call(t, "POST", "/catalog/"+provider, f.admin, "", `{"mode":"search","keyword":"test"}`, 503)
		if out["code"] != "PROVIDER_NOT_READY" {
			t.Fatal(out)
		}
	}
	f.call(t, "POST", "/catalog/other", f.admin, "", `{}`, 404)
	for _, input := range []string{`{"mode":"search","keyword":""}`, `{"mode":"calendar","season":"2026-02"}`, `{"mode":"calendar","season":"2026-10","keyword":"test"}`, `{"mode":"search","keyword":"test","url":"https://evil.test"}`} {
		f.call(t, "POST", "/catalog/mikan", f.admin, "", input, 400)
	}
	if f.mikan.reads != 0 || f.bgm.calls != 0 {
		t.Fatal("invalid/unsupported requests must not access any upstream")
	}
	out := f.call(t, "POST", "/catalog/mikan", f.admin, "", `{"mode":"calendar","season":"2026-10"}`, 200)
	if out["provider"] != "mikan" || f.mikan.reads != 1 || f.bgm.calls != 0 {
		t.Fatal(out)
	}
	f.call(t, "POST", "/catalog/mikan", f.admin, "", `{"mode":"calendar","season":"2026-10"}`, 200)
	if f.mikan.reads != 1 {
		t.Fatal("calendar must be shared from cache")
	}
}
