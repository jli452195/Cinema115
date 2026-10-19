package httpapi

import (
	"context"
	"os"
	"strings"
	"strm115/backend/internal/client/anibt"
	"strm115/backend/internal/service"
	"testing"
)

type animeHTTPAnibt struct {
	calls int
	err   error
}

func (f *animeHTTPAnibt) Fetch(_ context.Context, path string, _ int64) ([]byte, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	name := "calendar.json"
	if strings.Contains(path, "/groups?") {
		name = "groups.json"
	}
	return os.ReadFile("../client/anibt/testdata/" + name)
}
func TestAnibtAdminRoutingAndErrors(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	a := &animeHTTPAnibt{}
	f.s.Bindings.Anibt = service.NewAnibtService(a)
	f.call(t, "POST", "/catalog/anibt", "", "", `{"mode":"search","keyword":"test"}`, 401)
	f.call(t, "GET", "/sources/anibt/630160", f.user, "", nil, 403)
	f.call(t, "POST", "/catalog/anibt", f.admin, "", `{"mode":"search","keyword":"`+strings.Repeat("x", 121)+`"}`, 400)
	if a.calls != 0 {
		t.Fatal("invalid/unauthorized upstream access")
	}
	out := f.call(t, "POST", "/catalog/anibt", f.admin, "", `{"mode":"calendar","season":"2026-10"}`, 200)
	if out["provider"] != "anibt" || f.mikan.reads != 0 || f.bgm.calls != 0 {
		t.Fatal(out)
	}
	f.call(t, "GET", "/sources/anibt/630160", f.admin, "", nil, 200)
	f.call(t, "GET", "/sources/anibt/0", f.admin, "", nil, 400)
	a.err = anibt.ErrMoved
	out = f.call(t, "GET", "/sources/anibt/630161", f.admin, "", nil, 409)
	if out["code"] != "SOURCE_MOVED" {
		t.Fatal(out)
	}
	a.err = anibt.ErrUnavailable
	f.call(t, "POST", "/catalog/anibt", f.admin, "", `{"mode":"search","keyword":"new"}`, 502)
}
