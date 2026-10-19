package httpapi

import (
	"context"
	"strings"
	"testing"

	"strm115/backend/internal/model"
)

func TestAnimeOrganizationHTTPReadOnlyPreviewAndStrictAuth(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	ctx := context.Background()
	e, err := f.s.Store.SaveAnimeEntry(ctx, model.AnimeEntry{EditionKey: "original", Title: "作品", Metadata: model.AnimeMetadata{SchemaVersion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	naming := &model.AnimeNamingPlan{SchemaVersion: 1, Target: model.AnimeNamingTarget{TMDBID: "335790", Title: "作品", Year: "2026", Season: 1, Fingerprint: strings.Repeat("a", 64), Seasons: []model.AnimeNamingSeason{{Number: 1, Episodes: []model.AnimeNamingEpisode{{ID: "201", Number: 1}}}}}, Sources: []model.AnimeSourceMapping{{SourceRef: "main", Segments: []model.AnimeMappingSegment{{First: 25, SourceBase: 25, TargetSeason: 1, TargetBase: 1}}}}}
	source := animeHTTPSource()
	source.GroupName = animeHTTPPtr("字幕组甲")
	r, err := f.s.Store.SaveAnimeSubscription(ctx, model.AnimeSubscriptionConfig{EntryID: e.ID, Status: "draft", Naming: naming, Form: model.AnimeForm{SchemaVersion: 1}, Sources: []model.AnimeSourceConfig{source}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := "/subscriptions/" + r.ID + "/organization-preview"
	for _, endpoint := range []string{p, "/jobs/missing/organization-preview"} {
		f.call(t, "GET", endpoint, "", "", nil, 401)
		f.call(t, "GET", endpoint, f.user, "", nil, 403)
	}
	for _, token := range []string{"", f.user} {
		status := 401
		if token != "" {
			status = 403
		}
		f.call(t, "PATCH", "/settings/organize-directory", token, "organization_auth", `broken`, status)
	}
	f.call(t, "PATCH", "/settings/organize-directory", f.admin, "organization_strict", `{"expectedRevision":1,"cid":"900","enabled":false}`, 400)
	beforeReads := f.mikan.reads
	v := f.call(t, "GET", p, f.admin, "", nil, 200)
	items := v["items"].([]any)
	if v["mode"] != "standard" || v["example"] != true || v["cloudConflictChecked"] != false || len(items) != 1 || items[0].(map[string]any)["reason"] != "target_directory_required" {
		t.Fatal(v)
	}
	if f.mikan.reads != beforeReads || f.bgm.calls != 0 {
		t.Fatal("preview requested upstream")
	}
	cap := f.call(t, "GET", "/capabilities", f.admin, "", nil, 200)
	if cap["capabilities"].(map[string]any)["organize"] != false {
		t.Fatal("cloud writes prematurely enabled")
	}
	// Read-only preview does not require any configured 115 gateway.
	if f.s.Directories != nil || f.s.Accounts != nil {
		t.Fatal("unexpected fixture dependency")
	}
}
