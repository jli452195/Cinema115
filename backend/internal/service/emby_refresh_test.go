package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	received := make(chan []string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/emby/Library/Media/Updated" || r.Header.Get("X-Emby-Token") != "secret" || r.URL.RawQuery != "" {
			t.Error("通知方法、路径或鉴权不正确")
		}
		var body struct {
			Updates []struct{ Path, UpdateType string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		paths := make([]string, 0, len(body.Updates))
		for _, update := range body.Updates {
			if update.UpdateType != "Created" {
				t.Error("新文件通知类型不正确", update)
			}
			paths = append(paths, update.Path)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		received <- paths
	}))
	defer server.Close()
	s := &EmbyService{config: EmbyConfig{URL: server.URL, APIKey: "secret", RefreshAfterOrganize: true, RefreshPathMappings: "/source => /media"}, refreshWake: make(chan struct{}, 1)}
	for range 1000 {
		s.QueueMediaRefresh([]string{"/source/a.mkv", "/source/b.strm"})
	}
	if s.refreshCount != 2 || calls.Load() != 0 {
		t.Fatal("入队没有去重或进行了同步请求", s.refreshCount, calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.runMediaRefresh(ctx, 10*time.Millisecond) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case paths := <-received:
		slices.Sort(paths)
		if !slices.Equal(paths, []string{"/media/a.mkv", "/media/b.strm"}) || calls.Load() != 2 {
			t.Fatal("通知未合并或重试不正确", paths, calls.Load())
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
