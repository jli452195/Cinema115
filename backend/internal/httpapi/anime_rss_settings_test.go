package httpapi

import (
	"reflect"
	"testing"
)

func TestAnimeRSSSettingsAdminValidationAndReplay(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	const path = "/rss-settings"
	in := map[string]any{"revision": 1, "pollMinutes": 30, "autoArchive": false, "inactiveArchiveDays": 90}
	for _, method := range []string{"GET", "PATCH"} {
		f.call(t, method, path, "", "rss-anonymous", in, 401)
		f.call(t, method, path, f.user, "rss-reader", in, 403)
	}
	before := f.call(t, "GET", "/settings", f.admin, "", nil, 200)
	initial := f.call(t, "GET", path, f.admin, "", nil, 200)
	if initial["revision"] != float64(1) || initial["pollMinutes"] != float64(15) || initial["autoArchive"] != true || initial["inactiveArchiveDays"] != float64(60) {
		t.Fatal(initial)
	}
	f.call(t, "PATCH", path, f.admin, "rss-unknown", `{"revision":1,"pollMinutes":30,"extra":true}`, 400)
	f.call(t, "PATCH", path, f.admin, "rss-trailing", `{"revision":1,"pollMinutes":30} {}`, 400)
	f.call(t, "PATCH", path, f.admin, "", in, 400)
	for _, invalid := range []string{
		`{"revision":0,"pollMinutes":30,"inactiveArchiveDays":60}`,
		`{"revision":1,"pollMinutes":9,"inactiveArchiveDays":60}`,
		`{"revision":1,"pollMinutes":1441,"inactiveArchiveDays":60}`,
		`{"revision":1,"pollMinutes":30,"inactiveArchiveDays":29}`,
		`{"revision":1,"pollMinutes":30,"inactiveArchiveDays":366}`,
	} {
		f.call(t, "PATCH", path, f.admin, "rss-invalid", invalid, 400)
	}
	saved := f.call(t, "PATCH", path, f.admin, "rss-save", in, 200)
	if saved["revision"] != float64(2) || saved["pollMinutes"] != float64(30) || saved["autoArchive"] != false || saved["inactiveArchiveDays"] != float64(90) {
		t.Fatal(saved)
	}
	replay := f.call(t, "PATCH", path, f.admin, "rss-save", in, 200)
	if !reflect.DeepEqual(saved, replay) {
		t.Fatal("replay changed receipt", saved, replay)
	}
	f.call(t, "PATCH", path, f.admin, "rss-stale", in, 409)
	in["pollMinutes"] = 40
	f.call(t, "PATCH", path, f.admin, "rss-save", in, 409)
	after := f.call(t, "GET", "/settings", f.admin, "", nil, 200)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("RSS policy altered storage readiness", before, after)
	}
	if f.mikan.reads != 0 || f.bgm.calls != 0 {
		t.Fatal("local settings requested an upstream")
	}
}
