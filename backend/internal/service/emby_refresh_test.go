package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/emby"
)

func TestEmbyRefreshMappingsAndPersistence(t *testing.T) {
	mappings, err := parseEmbyRefreshMappings(" /云盘/已整理 => /media\n/云盘 => /other\nC:\\strm => /strm\n//nas/strm => /network")
	if err != nil {
		t.Fatal(err)
	}
	for input, want := range map[string]string{
		"/云盘/已整理/剧集/a.strm": "/media/剧集/a.strm",
		"/云盘/已整理2/a.mkv":    "/other/已整理2/a.mkv",
		"/else/a.mkv":       "/else/a.mkv",
		`C:\strm\a.strm`:    "/strm/a.strm",
		"//nas/strm/a.strm": "/network/a.strm",
	} {
		got, err := embyMappedRefreshPath(input, mappings)
		if err != nil || got != want {
			t.Fatalf("%q: got %q, want %q: %v", input, got, want, err)
		}
	}
	rootMappings, err := parseEmbyRefreshMappings(`C:\ => /media`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := embyMappedRefreshPath(`C:\a.mkv`, rootMappings); err != nil || got != "/media/a.mkv" {
		t.Fatal("Windows 根目录映射失败", got, err)
	}
	for _, invalid := range []string{"/a", "/a => relative", "/a => /b => /c", strings.Repeat("a", 16385)} {
		if _, err := parseEmbyRefreshMappings(invalid); err == nil {
			t.Fatalf("接受了无效路径替换 %q", invalid[:min(len(invalid), 40)])
		}
	}
	f := newEmbyFixture(t)
	c := f.service.snapshot()
	before := embyScope(c)
	if err := f.service.SaveMediaRefreshConfig(context.Background(), true, "/source => /media"); err != nil {
		t.Fatal(err)
	}
	// 连接页面提交的旧配置不得覆盖独立刷新的设置。
	if err := f.service.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	f.server.Close()
	// 保存刷新设置只写本地配置，不依赖 Emby 在线。
	if err := f.service.SaveMediaRefreshConfig(context.Background(), true, "/source => /updated"); err != nil {
		t.Fatal(err)
	}
	restored, err := NewEmbyService(f.service.secrets, f.db)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Config(); !got.RefreshAfterOrganize || got.RefreshPathMappings != "/source => /updated" || embyScope(restored.snapshot()) != before {
		t.Fatal("通知设置未恢复或改变了索引范围", got)
	}
	if err := f.service.SaveMediaRefreshConfig(context.Background(), true, "/source => relative"); err == nil || f.service.Config().RefreshPathMappings != "/source => /updated" {
		t.Fatal("无效配置覆盖了已保存的路径替换", err)
	}
}

func TestEmbyRefreshCoalescesRetriesAndStops(t *testing.T) {
	var calls atomic.Int32
	var reads atomic.Int32
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "secret" || strings.Contains(r.URL.RawQuery, "secret") {
			t.Error("刷新鉴权不正确")
		}
		if r.Method == http.MethodGet && r.URL.Path == "/emby/Library/VirtualFolders" {
			reads.Add(1)
			_, _ = w.Write([]byte(`[{"ItemId":"anime","Guid":"policy-guid","Name":"动漫","Locations":["/media"]}]`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/emby/Items/anime/Refresh" {
			t.Error("没有只刷新对应媒体库", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		for name, want := range map[string]string{"Recursive": "true", "MetadataRefreshMode": "Default", "ImageRefreshMode": "Default", "ReplaceAllMetadata": "false", "ReplaceAllImages": "false"} {
			if r.URL.Query().Get(name) != want {
				t.Error("普通递归刷新参数不正确", name, r.URL.Query())
			}
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		received <- "anime"
	}))
	defer server.Close()
	s := &EmbyService{config: EmbyConfig{URL: server.URL, APIKey: "secret", RefreshAfterOrganize: true, RefreshPathMappings: "/source => /media"}, refreshWake: make(chan struct{}, 1)}
	// 超过旧的 200 路径分块大小，同一库仍然只能刷新一次；不配置本地索引范围。
	paths := make([]string, 0, 250)
	for i := range 250 {
		paths = append(paths, "/source/"+time.Unix(int64(i), 0).Format("150405")+".strm")
	}
	for range 10 {
		s.QueueMediaRefresh(paths)
	}
	if s.refreshCount != 250 || calls.Load() != 0 || reads.Load() != 0 {
		t.Fatal("入队没有去重或进行了同步请求", s.refreshCount, calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.runMediaRefresh(ctx, 10*time.Millisecond) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case id := <-received:
		if id != "anime" || calls.Load() != 2 || reads.Load() != 1 {
			t.Fatal("媒体库刷新未合并或重试不正确", id, calls.Load(), reads.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到通知")
	}
	// 空闲不轮询，且关闭后不再入队。
	s.mu.Lock()
	s.config.RefreshAfterOrganize = false
	s.mu.Unlock()
	s.QueueMediaRefresh([]string{"/source/disabled.mkv"})
	if s.refreshCount != 0 {
		t.Fatal("关闭开关后仍然排队")
	}
	cancel()
}

func TestEmbyRefreshQueueBoundAndConnectionChange(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	s := &EmbyService{config: EmbyConfig{URL: server.URL, APIKey: "old", RefreshAfterOrganize: true}, refreshWake: make(chan struct{}, 1)}
	paths := make([]string, 0, embyRefreshPendingLimit+1)
	for i := range embyRefreshPendingLimit + 1 {
		paths = append(paths, "/media/"+time.Unix(int64(i), 0).Format("150405")+".mkv")
	}
	s.QueueMediaRefresh(paths)
	if s.refreshCount != embyRefreshPendingLimit || s.refreshDropped != 1 {
		t.Fatal("队列未限制内存增长", s.refreshCount, s.refreshDropped)
	}
	s.config.APIKey = "new"
	s.sendMediaRefresh(context.Background(), embyRefreshTarget{URL: server.URL, APIKey: "old"}, paths[:1], time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("配置变化后仍发送旧通知")
	}
}

func TestEmbyRefreshMatchesLibraryDirectories(t *testing.T) {
	libraries := []emby.Library{
		{ID: "parent", Locations: []string{"/media"}},
		{ID: "anime", Name: "动漫", Locations: []string{"/media/anime/", "/other/anime", "/media/anime"}},
		{ID: "shared", Locations: []string{"/media/anime"}},
		{ID: "movie", Locations: []string{"/media/movies"}},
		{ID: "windows", Locations: []string{`C:\media`}},
		{ID: "network", Locations: []string{`\\nas\media`}},
		{ID: "invalid", Locations: []string{"relative", ""}},
		{Locations: []string{"/unmatched"}},
	}
	targets, unmatched := embyRefreshLibraryTargets([]string{
		"/media/anime/show/a.strm", "/media/anime/show/b.strm", "/other/anime/show/c.strm",
		"/media/movies/movie/a.mkv", "/media/anime2/a.strm", "C:/media/movie/a.mkv", "//nas/media/movie/a.mkv",
		"/media2/a.strm", "/unmatched/a.strm",
	}, libraries)
	want := map[string]int{"anime": 3, "shared": 2, "movie": 1, "parent": 1, "windows": 1, "network": 1}
	if len(targets) != len(want) {
		t.Fatal("匹配到了错误的媒体库", targets)
	}
	for id, count := range want {
		if targets[id].Count != count {
			t.Fatal("最长目录匹配、多路径去重或目录边界不正确", id, targets)
		}
	}
	if !slices.Equal(unmatched, []string{"/media2/a.strm", "/unmatched/a.strm"}) {
		t.Fatal("未匹配路径丢失", unmatched)
	}
	root, missing := embyRefreshLibraryTargets([]string{"/a.strm", "C:/a.strm"}, []emby.Library{{ID: "root", Locations: []string{"/"}}, {ID: "drive", Locations: []string{`C:\`}}})
	if len(missing) != 0 || root["root"].Count != 1 || root["drive"].Count != 1 {
		t.Fatal("根目录匹配不正确", root, missing)
	}
}

func TestEmbyRefreshPartialFailureDoesNotRepeatSuccessfulLibrary(t *testing.T) {
	calls := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		switch r.URL.Path {
		case "/emby/Library/VirtualFolders":
			_, _ = w.Write([]byte(`[{"ItemId":"a","Name":"动漫","Locations":["/anime"]},{"ItemId":"b","Locations":["/movies"]},{"ItemId":"c","Locations":["/tv"]}]`))
		case "/emby/Items/a/Refresh", "/emby/Items/c/Refresh":
			w.WriteHeader(204)
		case "/emby/Items/b/Refresh":
			w.WriteHeader(403)
		default:
			t.Error("不应调用逐文件通知或全库扫描", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	logs := &GenerationLogs{}
	s := &EmbyService{Logs: logs, config: EmbyConfig{URL: server.URL, APIKey: "private-key", RefreshAfterOrganize: true, LibraryIDs: []string{"unrelated-sync-library"}}}
	s.sendMediaRefresh(context.Background(), embyRefreshTarget{server.URL, "private-key"}, []string{"/anime/show/a.strm", "/anime/show/b.strm", "/movies/a.mkv", "/tv/a.strm", "/outside/a.strm"}, time.Millisecond)
	if len(calls) != 4 {
		t.Fatal("未刷新同步范围外的全部命中库", calls)
	}
	for _, count := range calls {
		if count != 1 {
			t.Fatal("非临时错误不应重试，也不应重复成功的库", calls)
		}
	}
	entries := logs.Read("ALL", "Emby", 0, false, "").Entries
	var submitted, failed, unmatched bool
	for _, entry := range entries {
		if strings.Contains(entry.Message, "private-key") {
			t.Fatal("日志泄露密钥")
		}
		submitted = submitted || strings.Contains(entry.Message, "已向 Emby 提交媒体库") && strings.Contains(entry.Message, "合并 2 条")
		failed = failed || strings.Contains(entry.Message, "刷新请求失败")
		unmatched = unmatched || strings.Contains(entry.Message, "/outside/a.strm") && strings.Contains(entry.Message, "未匹配")
	}
	if !submitted || !failed || !unmatched {
		t.Fatal("缺少提交、失败或路径不匹配的诊断日志", entries)
	}
}

func TestEmbyRefreshDisablingDuringLookupStopsPosts(t *testing.T) {
	var s *EmbyService
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(204)
			return
		}
		s.mu.Lock()
		s.config.RefreshAfterOrganize = false
		s.mu.Unlock()
		_, _ = w.Write([]byte(`[{"ItemId":"anime","Locations":["/anime"]}]`))
	}))
	defer server.Close()
	s = &EmbyService{config: EmbyConfig{URL: server.URL, APIKey: "key", RefreshAfterOrganize: true}}
	s.sendMediaRefresh(context.Background(), embyRefreshTarget{server.URL, "key"}, []string{"/anime/a.strm"}, time.Millisecond)
	if posts.Load() != 0 {
		t.Fatal("读取媒体库时关闭开关后仍然发送刷新")
	}
}

type mediaRefreshRecorder struct{ paths []string }

func (r *mediaRefreshRecorder) QueueMediaRefresh(paths []string) { r.paths = append(r.paths, paths...) }

func TestMediaOrganizerRefreshAfterPersistedVideoAndAttachments(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	recorder := &mediaRefreshRecorder{}
	s.Refresh = recorder
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "http://original/video")
	mediaTestFile(t, rule.Source, "Work.S01E01.zh-CN.ass", "subtitle")
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "scan")
	if len(recorder.paths) != 0 {
		t.Fatal("仅扫描就触发了刷新")
	}
	mediaTestProcess(t, s, &run, "execute")
	if len(recorder.paths) != 1 || filepath.Ext(recorder.paths[0]) != ".strm" {
		t.Fatal("没有只通知成功的视频", recorder.paths)
	}
	for _, entry := range run.Entries {
		if entry.Status == "done" {
			if _, err := os.Stat(filepath.FromSlash(entry.Target)); err != nil {
				t.Fatal("通知时整理文件尚不存在", err)
			}
		}
	}
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	s.flushMediaRefresh(&run)
	if len(recorder.paths) != 1 {
		t.Fatal("重复保存进度导致重复通知", recorder.paths)
	}
}

func BenchmarkEmbyRefreshQueue(b *testing.B) {
	s := &EmbyService{config: EmbyConfig{APIKey: "secret", RefreshAfterOrganize: true, RefreshPathMappings: "/source => /media"}, refreshWake: make(chan struct{}, 1)}
	paths := []string{"/source/show/a.strm", "/source/show/b.strm"}
	s.QueueMediaRefresh(paths)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s.QueueMediaRefresh(paths)
	}
}
