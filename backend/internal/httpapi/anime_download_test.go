package httpapi

import (
	"context"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
)

type animeDownloadHTTPDirectory struct{}

func (animeDownloadHTTPDirectory) ReadDirectory(_ context.Context, cid, cookie string) (p115.DriveDirectory, error) {
	return p115.DriveDirectory{CID: cid, Path: "/动漫"}, nil
}

func TestAnimeDownloadHTTPDefinitionAuthReceiptAndLifecycle(t *testing.T) {
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
	f.s.Offline = cloud
	f.s.StatusGateway = cloud
	f.s.FileGateway = p115.NewShareClient()
	f.s.DownloadCoordinator = &service.CloudDownloadCoordinator{}
	settings, err := f.s.SaveSettings(ctx, "admin", "http_settings", service.AnimeSettingsInput{ExpectedRevision: 1, DownloadCID: "100", OrganizeCID: animeHTTPPtr("200"), ExpectedPrimaryAccountID: id})
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
	f.s.Naming = &service.AnimeNamingService{Metadata: animeStartHTTPNaming{}}
	season := 1
	target, err := f.s.Naming.Target(ctx, service.AnimeNamingTargetInput{TMDBID: "123", Season: &season}, "")
	if err != nil {
		t.Fatal(err)
	}
	input := service.AnimeSaveInput{Config: service.AnimeSaveConfig{Entry: service.AnimeEntryRef{ID: e.ID}, Sources: []model.AnimeSourceConfig{source}, Naming: &model.AnimeNamingInput{TMDBID: target.TMDBID, Season: season, Fingerprint: target.Fingerprint, Sources: []model.AnimeSourceMapping{{SourceRef: source.Ref, Segments: []model.AnimeMappingSegment{{First: 1, SourceBase: 1, TargetSeason: 1, TargetBase: 1}}}}}}, SettingsRevision: settings.Revision}
	for _, path := range []string{"/subscriptions"} {
		f.call(t, "POST", path, "", "", `broken`, 401)
		f.call(t, "POST", path, f.user, "", `broken`, 403)
		f.call(t, "POST", path, f.admin, "invalid_body_01", `{"unexpected":1}`, 400)
	}
	reads := f.mikan.reads
	first := f.call(t, "POST", "/subscriptions", f.admin, "http_start_01", input, 201)
	if first["status"] != "enabled" || first["revision"] != float64(1) {
		t.Fatal(first)
	}
	reads = f.mikan.reads // Verified source metadata is allowed; RSS is not read.
	again := f.call(t, "POST", "/subscriptions", f.admin, "http_start_01", input, 201)
	if again["id"] != first["id"] || again["revision"] != first["revision"] || f.mikan.reads != reads {
		t.Fatal("start or replay re-read RSS", again)
	}
	base := "/subscriptions/" + first["id"].(string)
	for _, path := range []string{base + "/enable-preview", base + "/enable", "/subscriptions/start", base + "/start"} {
		f.call(t, "POST", path, f.admin, "closed_enable_01", input, 404)
	}
	for _, action := range []string{"pause", "resume"} {
		v := f.call(t, "GET", base, f.admin, "", nil, 200)
		f.call(t, "POST", base+"/"+action, f.admin, "http_"+action, map[string]any{"expectedRevision": v["revision"]}, 200)
	}
	view := f.call(t, "GET", base, f.admin, "", nil, 200)
	if view["releaseStats"].(map[string]any)["pendingVerificationCount"] != float64(0) {
		t.Fatal(view)
	}
	f.call(t, "GET", base+"/jobs", f.admin, "", nil, 200)
	stateInput := map[string]any{"sourceId": view["sources"].([]any)[0].(map[string]any)["id"], "releaseIds": []string{}}
	f.call(t, "POST", base+"/release-states", "", "", stateInput, 401)
	f.call(t, "POST", base+"/release-states", f.user, "", stateInput, 403)
	f.call(t, "POST", base+"/release-states", f.admin, "", stateInput, 200)
	redoInput := map[string]any{"sourceId": stateInput["sourceId"], "expectedRevision": view["revision"], "settingsRevision": settings.Revision, "items": []map[string]any{{"id": "ffffffffffffffffffffffffffffffff", "revision": 1}}}
	f.call(t, "POST", base+"/redownload", f.user, "http_redownload_01", redoInput, 403)
	redo := f.call(t, "POST", base+"/redownload", f.admin, "http_redownload_01", redoInput, 200)
	if redo["results"].([]any)[0].(map[string]any)["error"] == "" {
		t.Fatal("unknown resource was scheduled", redo)
	}
	f.call(t, "POST", base+"/redownload", f.admin, "http_redownload_01", redoInput, 200)
	if f.mikan.reads != reads {
		t.Fatal("local lifecycle or GET contacted upstream")
	}
	capabilities := f.call(t, "GET", "/capabilities", f.admin, "", nil, 200)["capabilities"].(map[string]any)
	if capabilities["download"] != true || capabilities["organize"] != false {
		t.Fatal(capabilities)
	}
}

type animeStartHTTPNaming struct{}

func (animeStartHTTPNaming) Browse(context.Context, tmdb.Options) (*tmdb.Page, error) {
	return &tmdb.Page{}, nil
}
func (animeStartHTTPNaming) AnimeDetails(context.Context, int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{ID: 123, Title: "test", Date: "2026-10-01"}, Seasons: []tmdb.SeasonSummary{{Number: 1, EpisodeCount: 100}}}, nil
}
func (animeStartHTTPNaming) AnimeSeason(context.Context, int, int) (*tmdb.SeasonDetail, error) {
	return &tmdb.SeasonDetail{Number: 1, Episodes: []tmdb.Episode{{ID: 101, Number: 1, SourceSeason: 1, SourceNumber: 1}}}, nil
}
func (animeStartHTTPNaming) AnimeGroups(context.Context, int) ([]tmdb.EpisodeGroupSummary, error) {
	return []tmdb.EpisodeGroupSummary{}, nil
}
func (animeStartHTTPNaming) AnimeGroup(context.Context, int, string) (*tmdb.EpisodeGroup, error) {
	return nil, tmdb.ErrNotFound
}
