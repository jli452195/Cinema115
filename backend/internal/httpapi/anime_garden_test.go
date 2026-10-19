package httpapi

import (
	"context"
	"os"
	"strings"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/service"
	"testing"
)

type animeHTTPGarden struct {
	calls int
	err   error
}

func (f *animeHTTPGarden) Fetch(_ context.Context, path string, _ int64) ([]byte, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}

	name := "calendar.json"
	if strings.HasPrefix(path, "/bgm/subject/") {
		name = "subject.json"
	}
	if strings.HasPrefix(path, "/resources?") {
		name = "resources.json"
	}
	return os.ReadFile("../client/garden/testdata/" + name)
}
func TestGardenAdminRoutingAndErrors(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	a := &animeHTTPGarden{}
	f.s.Bindings.Garden = service.NewGardenService(a)
	f.call(t, "POST", "/catalog/garden", "", "", `{"mode":"search","keyword":"test"}`, 401)
	f.call(t, "GET", "/sources/garden/623249", f.user, "", nil, 403)
	f.call(t, "POST", "/catalog/garden", f.admin, "", `{"mode":"search","keyword":"`+strings.Repeat("x", 129)+`"}`, 400)
	if a.calls != 0 {
		t.Fatal("invalid/unauthorized upstream access")
	}
	out := f.call(t, "POST", "/catalog/garden", f.admin, "", `{"mode":"calendar","season":"2026-10"}`, 200)
	if out["provider"] != "garden" || f.mikan.reads != 0 || f.bgm.calls != 0 {
		t.Fatal(out)
	}
	f.call(t, "GET", "/sources/garden/623249", f.admin, "", nil, 200)
	f.call(t, "GET", "/sources/garden/0", f.admin, "", nil, 400)
	a.err = garden.ErrInvalid
	out = f.call(t, "GET", "/sources/garden/623250", f.admin, "", nil, 502)
	if out["code"] != "UPSTREAM_INVALID" {
		t.Fatal(out)
	}
	a.err = garden.ErrUnavailable
	f.call(t, "POST", "/catalog/garden", f.admin, "", `{"mode":"search","keyword":"new"}`, 502)
}
