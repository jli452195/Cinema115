package httpapi

import (
	"context"
	"testing"

	"strm115/backend/internal/model"
)

func localAnimeControlSubscription(t *testing.T, f animeHTTPFixture) string {
	t.Helper()
	entry, err := f.s.Store.SaveAnimeEntry(context.Background(), model.AnimeEntry{EditionKey: "original", Title: "本地动漫", Metadata: model.AnimeMetadata{SchemaVersion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := f.s.Store.SaveAnimeSubscription(context.Background(), model.AnimeSubscriptionConfig{EntryID: entry.ID, Status: "draft", Form: model.AnimeForm{SchemaVersion: 1}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return receipt.ID
}

func TestAnimeControlHTTPAuthAndStrictInput(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	id := localAnimeControlSubscription(t, f)
	for _, action := range []string{"pause", "archive", "remove", "unarchive"} {
		path := "/subscriptions/" + id + "/" + action
		f.call(t, "POST", path, "", "", `broken`, 401)
		f.call(t, "POST", path, f.user, "", `broken`, 403)
		for _, body := range []string{`{}`, `{"expectedRevision":0}`, `{"expectedRevision":1,"force":true}`, `{"expectedRevision":1} {}`, `{"expectedRevision":"1"}`} {
			f.call(t, "POST", path, f.admin, "strict_key_01", body, 400)
		}
		f.call(t, "POST", path, f.admin, "", `{"expectedRevision":1}`, 400)
	}
	for _, path := range []string{"/subscriptions/" + id + "/jobs", "/jobs/missing_job"} {
		f.call(t, "GET", path, "", "", nil, 401)
		f.call(t, "GET", path, f.user, "", nil, 403)
	}
	if f.bgm.calls != 0 || f.mikan.reads != 0 {
		t.Fatal("local control contacted upstream")
	}
}

func TestAnimeControlHTTPLocalLifecycleReceiptAndHistory(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	id := localAnimeControlSubscription(t, f)
	base := "/subscriptions/" + id
	archived := f.call(t, "POST", base+"/archive", f.admin, "archive_key_01", `{"expectedRevision":1}`, 200)
	if archived["status"] != "archived" || archived["revision"] != float64(2) || archived["cancelledJobs"] != float64(0) {
		t.Fatal(archived)
	}
	f.call(t, "POST", base+"/unarchive", f.admin, "unarchive_key_01", `{"expectedRevision":2}`, 200)
	// Replay must return its original receipt even though the entity advanced.
	again := f.call(t, "POST", base+"/archive", f.admin, "archive_key_01", `{"expectedRevision":1}`, 200)
	if again["revision"] != archived["revision"] || again["status"] != "archived" {
		t.Fatal(again)
	}
	if v := f.call(t, "POST", base+"/archive", f.admin, "archive_key_01", `{"expectedRevision":3}`, 409); v["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatal(v)
	}
	if v := f.call(t, "POST", base+"/remove", f.admin, "remove_stale", `{"expectedRevision":1}`, 409); v["code"] != "REVISION_CONFLICT" {
		t.Fatal(v)
	}
	f.call(t, "POST", base+"/pause", f.admin, "paused_noop", `{"expectedRevision":3}`, 200)
	f.call(t, "POST", base+"/remove", f.admin, "remove_key_01", `{"expectedRevision":3}`, 200)
	f.call(t, "GET", base, f.admin, "", nil, 404)
	f.call(t, "GET", base+"/jobs", f.admin, "", nil, 404)
	f.call(t, "POST", base+"/remove", f.admin, "remove_key_01", `{"expectedRevision":3}`, 200)
	f.call(t, "POST", base+"/archive", f.admin, "deleted_restore", `{"expectedRevision":4}`, 404)
	if v := f.call(t, "GET", "/subscriptions", f.admin, "", nil, 200); v["total"] != float64(0) {
		t.Fatal("deleted item listed", v)
	}
	f.call(t, "GET", "/subscriptions?status=removed", f.admin, "", nil, 400)

	if f.bgm.calls != 0 || f.mikan.reads != 0 {
		t.Fatal("local lifecycle contacted upstream")
	}
}

func TestAnimeControlHTTPExecutionStaysGatedAndActionsAgree(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	id := localAnimeControlSubscription(t, f)
	base := "/subscriptions/" + id
	if v := f.call(t, "POST", base+"/pause", f.admin, "pause_draft_01", `{"expectedRevision":1}`, 409); v["code"] != "STATE_CONFLICT" {
		t.Fatal(v)
	}
	for _, action := range []string{"resume", "check", "organize/apply"} {
		if v := f.call(t, "POST", base+"/"+action, f.admin, "enable_gated", `{"expectedRevision":1}`, 409); v["code"] != "CAPABILITY_UNAVAILABLE" {
			t.Fatal(v)
		}
	}
	v := f.call(t, "GET", base, f.admin, "", nil, 200)
	allowed := map[string]bool{}
	for _, raw := range v["permittedActions"].([]any) {
		action := raw.(map[string]any)
		allowed[action["name"].(string)] = action["allowed"].(bool)
	}
	if !allowed["archive"] || !allowed["remove"] || allowed["pause"] || allowed["resume"] || allowed["enable"] {
		t.Fatal(allowed)
	}
	f.call(t, "GET", base+"/jobs?page=0", f.admin, "", nil, 400)
	f.call(t, "GET", base+"/jobs?pageSize=101", f.admin, "", nil, 400)
	caps := f.call(t, "GET", "/capabilities", f.admin, "", nil, 200)["capabilities"].(map[string]any)
	if caps["download"] != false || caps["organize"] != false {
		t.Fatal(caps)
	}
	if f.bgm.calls != 0 || f.mikan.reads != 0 {
		t.Fatal("read-only API contacted upstream")
	}
}
