package httpapi

import (
	"context"
	"testing"

	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/storage"
)

func (f *animeHTTPMikan) Catalog(_ context.Context, season, keyword string) (mikan.Catalog, error) {
	f.reads++
	return mikan.Catalog{Season: season, Days: []mikan.CatalogDay{{Day: 1, Items: []mikan.CatalogItem{{Bangumi: mikan.Bangumi{ID: "4063", Title: "蜜柑条目"}}}}}, Items: []mikan.CatalogItem{}}, f.err
}

func TestAnimeCatalogStatusIsLocalAndAdminOnly(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	f.call(t, "POST", "/catalog-status", "", "", `{"provider":"mikan","itemIds":["99"]}`, 401)
	f.call(t, "POST", "/catalog-status", f.user, "", `{"provider":"mikan","itemIds":["99"]}`, 403)
	for _, body := range []string{`{"provider":"bad","itemIds":["99"]}`, `{"provider":"mikan","itemIds":[]}`, `{"provider":"mikan","itemIds":["invalid"]}`, `{"provider":"mikan","itemIds":["99"],"bangumiId":"123"}`} {
		f.call(t, "POST", "/catalog-status", f.admin, "", body, 400)
	}
	if err := f.s.Store.CacheAnimeCatalogLinks(context.Background(), "mikan", []storage.AnimeCatalogLink{{ItemID: "99", BangumiID: animeHTTPPtr("123"), ObservedAt: 100}}); err != nil {
		t.Fatal(err)
	}
	out := f.call(t, "POST", "/catalog-status", f.admin, "", `{"provider":"mikan","itemIds":["99","100"]}`, 200)
	items := out["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["bangumiId"] != "123" || f.mikan.reads != 0 || f.bgm.calls != 0 {
		t.Fatal(out)
	}
	f.call(t, "GET", "/subscriptions?bangumiId=invalid", f.admin, "", nil, 400)
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
