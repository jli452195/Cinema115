package httpapi

import (
	"context"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

type animeDownloadHTTPDirectory struct{}

func (animeDownloadHTTPDirectory) ReadDirectory(_ context.Context, cid, cookie string) (p115.DriveDirectory, error) {
	return p115.DriveDirectory{CID: cid, Path: "/动漫"}, nil
}

func TestAnimeDownloadHTTPEnablePreviewStrictAuthAndReceipt(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	ctx := context.Background()
	accounts, err := service.OpenP115AccountsSQLite(ctx, f.s.Store, t.TempDir(), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	id, err := accounts.AddPrimary("主网盘")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_A; SEID=mock"
	if err := accounts.Update(id, 1, "主网盘", &cookie); err != nil {
		t.Fatal(err)
	}
	f.s.Accounts = accounts
	f.s.Directories = animeDownloadHTTPDirectory{}
	cloud := p115.NewCloudClient()
	f.s.SubmitGateway = cloud
	f.s.StatusGateway = cloud
	f.s.FileGateway = p115.NewShareClient()
	f.s.DownloadCoordinator = &service.CloudDownloadCoordinator{}
	settings, err := f.s.SaveSettings(ctx, "admin", "http_settings", service.AnimeSettingsInput{ExpectedRevision: 1, DownloadCID: "100", ExpectedPrimaryAccountID: id, ExpectedPrimaryAccountRevision: 2})
	if err != nil {
		t.Fatal(err)
	}
	e, err := f.s.Store.SaveAnimeEntry(ctx, model.AnimeEntry{EditionKey: "original", Title: "测试动画", Metadata: model.AnimeMetadata{SchemaVersion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	source := animeHTTPSource()
	source.GroupName = animeHTTPPtr("字幕组甲")
	source.RunWhenEnabled = true
	r, err := f.s.Store.SaveAnimeSubscription(ctx, model.AnimeSubscriptionConfig{EntryID: e.ID, Status: "paused", Form: model.AnimeForm{SchemaVersion: 1}, Sources: []model.AnimeSourceConfig{source}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := f.s.Store.GetAnimeSubscription(ctx, r.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	base := "/subscriptions/" + sub.ID
	for _, path := range []string{base + "/enable-preview", base + "/enable", base + "/resume", "/jobs/missing/reconcile", "/jobs/missing/retry"} {
		f.call(t, "POST", path, "", "", `broken`, 401)
		f.call(t, "POST", path, f.user, "", `broken`, 403)
		f.call(t, "POST", path, f.admin, "invalid_body_01", `{"unexpected":1}`, 400)
	}
	preview := f.call(t, "POST", base+"/enable-preview", f.admin, "", service.AnimeEnablePreviewInput{ExpectedRevision: 1, SettingsRevision: settings.Revision, SourceID: sub.Sources[0].ID}, 200)
	page := preview["page"].(map[string]any)
	summary := page["summary"].(map[string]any)
	selected := page["selectedIds"].([]any)
	source.ID, source.Ref, source.ExpectedRevision = sub.Sources[0].ID, sub.Sources[0].ID, sub.Sources[0].Revision
	_, err = f.s.Store.SaveAnimeSubscription(ctx, model.AnimeSubscriptionConfig{ID: sub.ID, EntryID: e.ID, ExpectedRevision: 1, Status: "paused", Form: model.AnimeForm{SchemaVersion: 1, FormSelections: []model.AnimeFormSelection{{SourceRef: source.ID, SelectedReleaseIDs: []string{selected[0].(string)}, DismissedReleaseIDs: []string{selected[1].(string)}, UnresolvedReleaseIDs: []string{"missing_old"}}}}, Sources: []model.AnimeSourceConfig{source}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	preview = f.call(t, "POST", base+"/enable-preview", f.admin, "", service.AnimeEnablePreviewInput{ExpectedRevision: 2, SettingsRevision: settings.Revision, SourceID: source.ID}, 200)
	page = preview["page"].(map[string]any)
	summary = page["summary"].(map[string]any)
	if preview["selectionNotice"] == nil || len(page["selectedIds"].([]any)) != 1 {
		t.Fatal("saved selections replaced by defaults", preview)
	}
	body := map[string]any{"expectedRevision": 2, "settingsRevision": settings.Revision, "sources": []any{map[string]any{"batchId": preview["batchId"], "sourceRef": sub.Sources[0].ID, "sourcePreviewId": summary["sourcePreviewId"], "loadRevision": summary["loadRevision"], "selectedReleaseIds": page["selectedIds"], "dismissedReleaseIds": []string{}}}}
	first := f.call(t, "POST", base+"/enable", f.admin, "http_enable_01", body, 200)
	if first["status"] != "enabled" || first["revision"] != float64(3) {
		t.Fatal(first)
	}
	reads := f.mikan.reads
	again := f.call(t, "POST", base+"/enable", f.admin, "http_enable_01", body, 200)
	if again["revision"] != first["revision"] || f.mikan.reads != reads {
		t.Fatal("replay reread upstream", again)
	}
	for _, action := range []string{"pause", "resume"} {
		v := f.call(t, "GET", base, f.admin, "", nil, 200)
		f.call(t, "POST", base+"/"+action, f.admin, "http_"+action, map[string]any{"expectedRevision": v["revision"]}, 200)
	}
	view := f.call(t, "GET", base, f.admin, "", nil, 200)
	if view["releaseStats"].(map[string]any)["pendingVerificationCount"] != float64(1) {
		t.Fatal(view)
	}
	f.call(t, "GET", base+"/jobs", f.admin, "", nil, 200)
	if f.mikan.reads != reads {
		t.Fatal("local lifecycle or GET contacted upstream")
	}
	capabilities := f.call(t, "GET", "/capabilities", f.admin, "", nil, 200)["capabilities"].(map[string]any)
	if capabilities["download"] != true || capabilities["organize"] != false {
		t.Fatal(capabilities)
	}
}
