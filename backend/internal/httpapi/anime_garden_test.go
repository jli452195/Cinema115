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

type animeHTTPGardenSearch struct{ calls int }

func (f *animeHTTPGardenSearch) Fetch(_ context.Context, _ string, _ int64) ([]byte, error) {
	f.calls++
	return []byte(`{"status":"OK","resources":[],"pagination":{"page":1,"pageSize":100,"complete":true},"filter":{"search":["old"]}}`), nil
}
func TestGardenResourceSearchAdminBoundary(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	a := &animeHTTPGardenSearch{}
	f.s.Bindings.Garden = service.NewGardenService(a)
	f.call(t, "POST", "/resources/garden/search", "", "", `{"query":"old"}`, 401)
	f.call(t, "POST", "/resources/garden/search", f.user, "", `{"query":"old"}`, 403)
	f.call(t, "POST", "/resources/garden/search", f.admin, "", `{"query":"old","page":101}`, 400)
	f.call(t, "POST", "/resources/garden/search", f.admin, "", `{"query":"old","provider":"unknown"}`, 400)
	if a.calls != 0 {
		t.Fatal("unauthorized or invalid request reached upstream")
	}
	if len(f.s.Logs.Snapshot("ALL", "搜索")) != 4 {
		t.Fatal("只有已通过鉴权的两次无效搜索应产生日志")
	}
	out := f.call(t, "POST", "/resources/garden/search", f.admin, "", `{"query":"old"}`, 200)
	if out["query"] != "old" || out["hasMore"] != false || a.calls != 1 || f.mikan.reads != 0 || f.bgm.calls != 0 {
		t.Fatal(out, a.calls)
	}
	f.call(t, "POST", "/resources/garden/search", f.admin, "", `{"query":"old"}`, 200)
	if a.calls != 1 {
		t.Fatal("search cache not reused")
	}
	entries := f.s.Logs.Snapshot("ALL", "搜索")
	if len(entries) != 8 || entries[0].Source != "Garden" || entries[0].Level != "DEBUG" || !strings.Contains(entries[0].Message, "命中缓存") || entries[0].TaskID == entries[2].TaskID {
		t.Fatalf("Garden 缓存日志或操作隔离错误: %+v", entries)
	}
}
