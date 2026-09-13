package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

const testEmbyServerID = "30ffc4da1ff94898a54620e1b6aa5b90"

type embyFixture struct {
	mu             sync.Mutex
	items          []emby.Item
	failPage       bool
	missingLibrary bool
	detailCalls    int
	pageCalls      int
	server         *httptest.Server
	service        *EmbyService
	db             *storage.Store
	path           string
}

func number(n int) *int { return &n }
func newEmbyFixture(t *testing.T) *embyFixture {
	t.Helper()
	f := &embyFixture{}
	f.items = []emby.Item{
		{ID: "movie-a", Name: "电影", Type: "Movie", ParentID: "movies", ProviderIDs: map[string]string{"Tmdb": "100"}},
		{ID: "movie-b", Name: "电影第二版本", Type: "Movie", ParentID: "movies", ProviderIDs: map[string]string{"Tmdb": "100"}},
		{ID: "series-a", Name: "剧集", Type: "Series", ParentID: "tv", ProviderIDs: map[string]string{"Tmdb": "100"}},
		{ID: "series-b", Name: "剧集第二版本", Type: "Series", ParentID: "tv", ProviderIDs: map[string]string{"Tmdb": "100"}},
		{ID: "ep-1", Type: "Episode", ParentID: "season-a", SeriesID: "series-a", ParentIndexNumber: number(1), IndexNumber: number(1), IndexNumberEnd: number(2), ProviderIDs: map[string]string{"Tmdb": "999"}},
		{ID: "ep-2", Type: "Episode", ParentID: "season-b", SeriesID: "series-b", ParentIndexNumber: number(1), IndexNumber: number(2)},
		{ID: "virtual", Type: "Movie", ParentID: "movies", LocationType: "Virtual", ProviderIDs: map[string]string{"Tmdb": "101"}},
	}
	for i := 0; i < 201; i++ {
		f.items = append(f.items, emby.Item{ID: fmt.Sprintf("filler-%d", i), Type: "Movie", ParentID: "movies", ProviderIDs: map[string]string{"Tmdb": fmt.Sprint(i + 1000)}})
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("X-Emby-Token") != "test-api-key" || r.URL.Query().Get("api_key") != "" {
			t.Error("请求未使用密钥请求头")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.URL.Path {
		case "/emby/System/Info":
			json.NewEncoder(w).Encode(emby.Server{ID: testEmbyServerID, Name: "测试 Emby", Version: "4.9.5.0"})
		case "/emby/Library/VirtualFolders":
			libs := []emby.Library{{ID: "tv", Name: "电视剧"}, {ID: "other", Name: "其他"}}
			if !f.missingLibrary {
				libs = append(libs, emby.Library{ID: "movies", Name: "电影"})
			}
			json.NewEncoder(w).Encode(libs)
		case "/emby/Items":
			if id := r.URL.Query().Get("Ids"); id != "" {
				f.detailCalls++
				page := emby.Page{Items: []emby.Item{}}
				for _, item := range f.items {
					if item.ID == id {
						// 模拟聚合媒体库：物理父目录不同于虚拟媒体库，但归属过滤仍可准确判断。
						library := "movies"
						if item.Type == "Episode" || item.Type == "Series" || item.Type == "Season" {
							library = "tv"
						}
						if parent := r.URL.Query().Get("ParentId"); parent != "" && parent != library {
							continue
						}
						page.Items = append(page.Items, item)
					}
				}
				json.NewEncoder(w).Encode(page)
				return
			}
			f.pageCalls++
			if r.URL.Query().Get("Limit") != "200" || r.URL.Query().Get("SortBy") != "Id" || strings.Contains(r.URL.Query().Get("Fields"), "MediaSources") {
				t.Error("全量同步未使用轻量分页")
			}
			parent := r.URL.Query().Get("ParentId")
			items := make([]emby.Item, 0)
			for _, item := range f.items {
				if item.ParentID == parent || (parent == "tv" && item.Type == "Episode") || item.SeriesID == parent {
					items = append(items, item)
				}
			}
			start := 0
			fmt.Sscan(r.URL.Query().Get("StartIndex"), &start)
			if f.failPage && start > 0 {
				w.WriteHeader(503)
				return
			}
			end := start + 200
			if end > len(items) {
				end = len(items)
			}
			if start > len(items) {
				start = len(items)
			}
			json.NewEncoder(w).Encode(emby.Page{Items: items[start:end], TotalRecordCount: len(items)})
		default:
			if strings.HasSuffix(r.URL.Path, "/Ancestors") {
				id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/emby/Items/"), "/Ancestors")
				lib := "movies"
				for _, item := range f.items {
					if item.ID == id && (item.Type == "Series" || item.Type == "Episode" || item.Type == "Season") {
						lib = "tv"
					}
				}
				json.NewEncoder(w).Encode([]emby.Item{{ID: lib, Type: "CollectionFolder"}})
				return
			}
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.server.Close)
	f.path = filepath.Join(t.TempDir(), "cinema115.db")
	var err error
	f.db, err = storage.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Close() })
	secrets, err := NewSessionStore(filepath.Join(t.TempDir(), "emby.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = NewEmbyService(secrets, f.db)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.Save(context.Background(), EmbyConfig{URL: f.server.URL, APIKey: "test-api-key", LibraryIDs: []string{"movies", "tv"}}); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *embyFixture) sync(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := f.service.StartSync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.service.syncLibrary(ctx, f.service.snapshot()); err != nil {
		t.Fatal(err)
	}
}
func (f *embyFixture) match(t *testing.T, kind, id string) model.EmbyMatch {
	t.Helper()
	items, err := f.service.Matches(context.Background(), []model.EmbyMatch{{Kind: kind, TMDB: id}}, true)
	if err != nil {
		t.Fatal(err)
	}
	return items[0]
}
func (f *embyFixture) event(t *testing.T, item, kind string) model.EmbyEvent {
	t.Helper()
	name := "library.new"
	if kind == "delete" {
		name = "library.deleted"
	}
	body := fmt.Sprintf(`{"Event":%q,"Server":{"Id":%q},"Item":{"Id":%q}}`, name, testEmbyServerID, item)
	code, err := f.service.Webhook(context.Background(), f.service.snapshot().WebhookSecret, []byte(body))
	if err != nil || code != 202 {
		t.Fatalf("通知失败 %d %v", code, err)
	}
	_, _, id, err := f.db.EmbyEventStats(context.Background(), embyScope(f.service.snapshot()))
	if err != nil {
		t.Fatal(err)
	}
	return model.EmbyEvent{ID: id, ItemID: item, Kind: kind}
}
func (f *embyFixture) apply(t *testing.T, event model.EmbyEvent) {
	t.Helper()
	state, err := f.service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.applyEvent(context.Background(), f.service.snapshot(), state.Active, event); err != nil {
		t.Fatal(err)
	}
}

func TestEmbyFullSyncExactMatchAndFailureKeepsSnapshot(t *testing.T) {
	f := newEmbyFixture(t)
	if got := f.match(t, "movie", "100"); got.State != "unknown" {
		t.Fatal(got)
	}
	f.sync(t)
	if f.detailCalls != 0 || f.pageCalls != 3 {
		t.Fatalf("全量请求数量错误 details=%d pages=%d", f.detailCalls, f.pageCalls)
	}
	if got := f.match(t, "movie", "100"); got.State != "present" || len(got.Items) != 2 {
		t.Fatal(got)
	}
	if got := f.match(t, "tv", "100"); got.EpisodeCount != 2 || len(got.Items) != 4 {
		t.Fatal(got)
	}
	if got := f.match(t, "tv", "999"); got.State != "absent" {
		t.Fatal("不应使用分集 TMDB ID 匹配剧集", got)
	}
	if got := f.match(t, "movie", "101"); got.State != "absent" {
		t.Fatal("虚拟媒体不应计入", got)
	}
	f.failPage = true
	f.sync(t)
	state, err := f.service.Status(context.Background())
	if err != nil || state.Status != "failed" || state.LastError == "" {
		t.Fatal(state, err)
	}
	if got := f.match(t, "movie", "100"); got.State != "stale" || len(got.Items) != 2 {
		t.Fatal("失败不应清空旧索引", got)
	}
	f.failPage = false
	f.missingLibrary = true
	f.sync(t)
	if got := f.match(t, "movie", "100"); len(got.Items) != 2 || got.State != "stale" {
		t.Fatal("媒体库消失不应覆盖快照", got)
	}
}
func TestEmbyWebhookTestAuthReplayDeletionAndRetry(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	ctx := context.Background()
	secret := f.service.snapshot().WebhookSecret
	payload := []byte(`{"Title":"Test Notification","Description":"Test Notification Description","Date":"2026-09-13T14:01:49.8851352Z","Event":"system.webhooktest","Severity":"Info","User":{"Name":"jli452195","Id":"60df1479bd184f63948fcbbb04bfd1b7"},"Server":{"Name":"可乐emby","Id":"30ffc4da1ff94898a54620e1b6aa5b90","Version":"4.9.5.0"}}`)
	if code, err := f.service.Webhook(ctx, "wrong", payload); code != 401 || err == nil {
		t.Fatal("未拒绝错误密钥")
	}
	if code, err := f.service.Webhook(ctx, secret, []byte(`{"Event":"system.webhooktest","Server":{"Id":"other"}}`)); code != 403 || err == nil {
		t.Fatal("未拒绝其他服务器")
	}
	if code, err := f.service.Webhook(ctx, secret, payload); code != 200 || err != nil {
		t.Fatal(code, err)
	}
	state, err := f.service.Status(ctx)
	if err != nil || state.Pending != 0 || state.WebhookEvent != "system.webhooktest" || state.WebhookAt == "" {
		t.Fatal(state, err)
	}
	beforeCount := state.Count
	for _, name := range []string{"system.notificationtest", "system.webhooktest"} {
		body := strings.Replace(string(payload), "system.webhooktest", name, 1)
		if code, err := f.service.Webhook(ctx, secret, []byte(body)); code != 200 || err != nil {
			t.Fatal(code, err)
		}
		state, err = f.service.Status(ctx)
		if err != nil || state.Pending != 0 || state.Count != beforeCount || state.WebhookEvent != name || state.WebhookError != "" {
			t.Fatal("测试事件误改媒体数据或被当成未知事件", state, err)
		}
	}
	// 同步期间收到删除通知，快照提交之后重放，不能复活已删除的条目。
	event := f.event(t, "movie-a", "delete")
	f.sync(t)
	f.items = f.items[1:]
	f.apply(t, event)
	if got := f.match(t, "movie", "100"); len(got.Items) != 1 || got.State != "present" {
		t.Fatal("删除一个版本影响了其他版本", got)
	}
	// 相同条目的新通知覆盖旧通知；旧处理结果不能删除新通知或写入旧状态。
	old := f.event(t, "movie-b", "delete")
	latest := f.event(t, "movie-b", "upsert")
	f.apply(t, old)
	state, _ = f.service.Status(ctx)
	if state.Pending != 1 {
		t.Fatal("旧处理结果吞掉了新通知")
	}
	f.apply(t, latest)
	if got := f.match(t, "movie", "100"); len(got.Items) != 1 {
		t.Fatal(got)
	}
	f.items = append(f.items, emby.Item{ID: "new-movie", Type: "Movie", ParentID: "movies"})
	waiting := f.event(t, "new-movie", "upsert")
	f.apply(t, waiting)
	state, _ = f.service.Status(ctx)
	if state.Pending != 1 || state.EventError == "" {
		t.Fatal("刮削未完成时没有保留重试", state)
	}
	if state.MetadataPending != 1 {
		t.Fatal("刮削补查未独立统计", state)
	}
	if got := f.match(t, "movie", "100"); got.State != "present" {
		t.Fatal("不相关条目缺少刮削不应影响已匹配电影", got)
	}
	if got := f.match(t, "movie", "456"); got.State != "stale" {
		t.Fatal("仍有无法识别的条目时不能直接判断未入库", got)
	}
	f.items[len(f.items)-1].ProviderIDs = map[string]string{"Tmdb": "456"}
	f.apply(t, waiting)
	if got := f.match(t, "movie", "456"); got.State != "present" {
		t.Fatal(got)
	}
}
func TestEmbyVersionsFilterEpisodesBeforePagination(t *testing.T) {
	f := newEmbyFixture(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		f.items = append(f.items, emby.Item{ID: fmt.Sprintf("version-%02d", i), Type: "Episode", ParentID: "season-a", SeriesID: "series-a", ParentIndexNumber: number(1), IndexNumber: number(7)})
	}
	f.items = append(f.items,
		emby.Item{ID: "late-episode", Type: "Episode", ParentID: "season-a", SeriesID: "series-a", ParentIndexNumber: number(1), IndexNumber: number(99)},
		emby.Item{ID: "special", Type: "Episode", ParentID: "season-special", SeriesID: "series-a", ParentIndexNumber: number(0), IndexNumber: number(0)},
	)
	f.sync(t)
	page, err := f.service.Versions(ctx, "tv", "100", EmbyVersionQuery{Season: number(1), Episode: number(99)})
	if err != nil || len(page.Versions) != 1 || page.Versions[0].ItemID != "late-episode" {
		t.Fatal("超过前二十条的分集仍须正确返回", page, err)
	}
	page, err = f.service.Versions(ctx, "tv", "100", EmbyVersionQuery{Season: number(1), Episode: number(2)})
	if err != nil || len(page.Versions) != 2 {
		t.Fatal("多集合并文件和其他版本未正确匹配", page, err)
	}
	page, err = f.service.Versions(ctx, "tv", "100", EmbyVersionQuery{Season: number(0), Episode: number(0)})
	if err != nil || len(page.Versions) != 1 || page.Versions[0].ItemID != "special" {
		t.Fatal("特别篇零号分集匹配失败", page, err)
	}
	page, err = f.service.Versions(ctx, "tv", "100", EmbyVersionQuery{Season: number(1), Episode: number(7)})
	if err != nil || page.Total != 25 || len(page.Versions) != 20 || !page.HasMore || page.NextOffset != 20 {
		t.Fatal("版本分页首批错误", page, err)
	}
	last, err := f.service.Versions(ctx, "tv", "100", EmbyVersionQuery{Season: number(1), Episode: number(7), Offset: page.NextOffset})
	if err != nil || len(last.Versions) != 5 || last.HasMore {
		t.Fatal("后续版本丢失", last, err)
	}
	if _, err := f.service.Versions(ctx, "tv", "100", EmbyVersionQuery{}); err == nil {
		t.Fatal("剧集查询必须指定季集号")
	}
	// 上游重新识别后，旧索引不能把已变更集号的文件显示给另一集。
	for i := range f.items {
		if f.items[i].ID == "late-episode" {
			f.items[i].IndexNumber = number(100)
		}
	}
	page, err = f.service.Versions(ctx, "tv", "100", EmbyVersionQuery{Season: number(1), Episode: number(99)})
	if err != nil || len(page.Versions) != 0 {
		t.Fatal("错误分集混入版本列表", page, err)
	}
	page, err = f.service.Versions(ctx, "movie", "100", EmbyVersionQuery{})
	if err != nil || len(page.Versions) != 2 {
		t.Fatal("电影版本不应要求季集号", page, err)
	}
}

func TestEmbyWebhookVirtualLibraryMembership(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	// 新分集的物理父目录不等于媒体库 ID，必须向 Emby 查询实际归属。
	f.items = append(f.items, emby.Item{ID: "ep-new", Type: "Episode", Name: "新增第十一集", ParentID: "physical-season", SeriesID: "series-a", ParentIndexNumber: number(1), IndexNumber: number(11)})
	event := f.event(t, "ep-new", "upsert")
	f.apply(t, event)
	got := f.match(t, "tv", "100")
	found := false
	for _, item := range got.Items {
		if item.ID == "ep-new" {
			found = true
			if item.LibraryID != "tv" || item.Season != 1 || item.Episode != 11 {
				t.Fatal("条目归属或分集信息错误", item)
			}
		}
	}
	if !found || got.State != "present" {
		t.Fatal("聚合媒体库的新分集未正确入库", got)
	}
}

func TestEmbyConfigEncryptionScopeAndRestart(t *testing.T) {
	f := newEmbyFixture(t)
	f.sync(t)
	ctx := context.Background()
	oldConfig := f.service.snapshot()
	data, err := os.ReadFile(f.service.secrets.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "test-api-key") {
		t.Fatal("密钥明文落盘")
	}
	view, _ := json.Marshal(f.service.Config())
	if strings.Contains(string(view), "test-api-key") || strings.Contains(string(view), `"apiKey"`) {
		t.Fatal("接口回显 API Key")
	}
	if err := f.service.Save(ctx, EmbyConfig{URL: f.server.URL, LibraryIDs: []string{"movies"}}); err != nil {
		t.Fatal(err)
	}
	if got := f.match(t, "movie", "100"); got.State != "unknown" {
		t.Fatal("配置变更后不应查询旧范围", got)
	}
	if err := f.service.Save(ctx, EmbyConfig{URL: f.server.URL, LibraryIDs: oldConfig.LibraryIDs}); err != nil {
		t.Fatal(err)
	}
	if got := f.match(t, "movie", "100"); got.State != "stale" {
		t.Fatal("切回历史范围需要重新同步", got)
	}
	f.sync(t)
	f.event(t, "new-id", "upsert")
	if err := f.db.EmbyProgress(ctx, embyScope(f.service.snapshot()), "running", 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db, err = storage.Open(ctx, f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.service.db = f.db
	if err := f.db.EmbyRecover(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := f.service.Status(ctx)
	if err != nil || state.Pending != 1 || state.Status != "failed" || state.Count == 0 {
		t.Fatal("重启未保留队列或快照", state, err)
	}
	secrets, err := NewSessionStore(f.service.secrets.path, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewEmbyService(secrets, f.db)
	if err != nil || restored.snapshot().APIKey != "test-api-key" {
		t.Fatal("配置未恢复", err)
	}
}
func TestEmbyWebhookBaseURLPersistenceAndIsolation(t *testing.T) {
	f := newEmbyFixture(t)
	ctx := context.Background()
	f.sync(t)
	before := f.service.snapshot()
	stateBefore, err := f.service.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Emby 离线也应能修改 Cinema115 自身的地址。
	f.server.Close()
	if err := f.service.SaveWebhookBaseURL(ctx, " http://192.168.0.24:3002/ "); err != nil {
		t.Fatal(err)
	}
	after := f.service.snapshot()
	if after.WebhookBaseURL != "http://192.168.0.24:3002" || after.APIKey != before.APIKey || after.WebhookSecret != before.WebhookSecret || embyScope(after) != embyScope(before) {
		t.Fatal("保存回调地址修改了连接凭证或同步范围")
	}
	stateAfter, err := f.service.Status(ctx)
	if err != nil || stateAfter.Active != stateBefore.Active || stateAfter.Status != stateBefore.Status {
		t.Fatal("修改地址不应重置索引", err)
	}
	secrets, err := NewSessionStore(f.service.secrets.path, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewEmbyService(secrets, f.db)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Config().WebhookBaseURL != after.WebhookBaseURL {
		t.Fatal("重启后回调地址没有恢复")
	}
	for _, invalid := range []string{"", "javascript:alert(1)", "http://user:password@server", "http://server/api/webhook/emby?token=secret", "http://server/path", "http://server#fragment"} {
		if err := restored.SaveWebhookBaseURL(ctx, invalid); err == nil {
			t.Fatalf("未拒绝错误地址 %q", invalid)
		}
	}
	if restored.Config().WebhookBaseURL != after.WebhookBaseURL {
		t.Fatal("错误输入覆盖了已保存的地址")
	}
}

func TestEmbyMainConfigPreservesWebhookBaseURL(t *testing.T) {
	f := newEmbyFixture(t)
	ctx := context.Background()
	if err := f.service.SaveWebhookBaseURL(ctx, "http://192.168.0.24:3002"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Save(ctx, EmbyConfig{URL: f.server.URL, LibraryIDs: []string{"movies", "tv"}}); err != nil {
		t.Fatal(err)
	}
	if f.service.Config().WebhookBaseURL != "http://192.168.0.24:3002" {
		t.Fatal("旧配置请求清空了回调地址")
	}
	if err := f.service.Save(ctx, EmbyConfig{URL: f.server.URL, LibraryIDs: []string{"movies", "tv"}, WebhookBaseURL: "http://nas:3002/"}); err != nil {
		t.Fatal(err)
	}
	if f.service.Config().WebhookBaseURL != "http://nas:3002" {
		t.Fatal("主配置没有保存回调地址")
	}
}

func TestEmbyWorkerStopsWithContext(t *testing.T) {
	f := newEmbyFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	if err := f.service.StartSync(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { done <- f.service.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("后台任务未响应退出")
		}
	})
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("后台任务未消费全量同步请求")
		case <-poll.C:
			state, err := f.service.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if state.SyncedAt != "" {
				if state.Count == 0 || state.Status != "idle" {
					t.Fatal(state)
				}
				return
			}
		}
	}
}
