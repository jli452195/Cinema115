package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/dmhy"
)

type dmhyServiceTransport struct {
	mu    sync.Mutex
	paths []string
	fetch func(context.Context, string) ([]byte, error)
}

func (f *dmhyServiceTransport) Fetch(ctx context.Context, endpoint string, _ int64) ([]byte, error) {
	f.mu.Lock()
	f.paths = append(f.paths, endpoint)
	f.mu.Unlock()
	return f.fetch(ctx, endpoint)
}

func (f *dmhyServiceTransport) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.paths)
}

func dmhyServicePage(rows string) []byte {
	return []byte(`<html><body><table id="topic_list"><thead><tr><th>張貼日期</th><th>分類</th><th>標題</th><th>磁鏈</th><th>大小</th><th>種子</th><th>下載</th><th>完成</th><th>發佈人</th></tr></thead><tbody>` + rows + `</tbody></table></body></html>`)
}

func dmhyServiceRow(id, name, category, magnet string) string {
	return fmt.Sprintf(`<tr><td><span>2026/11/16 01:00</span></td><td><a href="/topics/list/sort_id/%s">分類</a></td><td class="title"><a href="/topics/list/team_id/45">字幕组</a><a href="/topics/view/%s_Show.html">%s</a></td><td><a href="%s">磁力</a></td><td>1.2GB</td><td>12</td><td>3</td><td>30</td><td><a href="/topics/list/user_id/88">发布者</a></td></tr>`, category, id, html.EscapeString(name), html.EscapeString(magnet))
}

func newDmhyTestService(f *dmhyServiceTransport) *DmhyService {
	s := NewDmhyService(f)
	s.spacing, s.torrents.spacing = 0, 0
	return s
}

func TestDmhySearchCacheFallbackAndAllCategories(t *testing.T) {
	fail := false
	f := &dmhyServiceTransport{fetch: func(_ context.Context, endpoint string) ([]byte, error) {
		if fail {
			return nil, dmhy.ErrNetwork
		}
		u, _ := url.Parse(endpoint)
		if u.Query().Get("keyword") == "empty" {
			return []byte(`<form><input name="keyword"></form><p>沒有可顯示資源</p>`), nil
		}
		return dmhyServicePage(dmhyServiceRow("123", "Show.S01E01.1080p", "2", "magnet:?xt=urn:btih:"+gardenTestHash) + dmhyServiceRow("124", "Show music", "43", "") + dmhyServiceRow("125", "Show comic", "3", "") + dmhyServiceRow("126", "Show unknown", "999", "")), nil
	}}
	s := newDmhyTestService(f)
	in := DmhySearchInput{Query: " empty ", Fallback: "original"}
	first, err := s.Search(context.Background(), in)
	if err != nil || first.Query != "original" || len(first.Items) != 4 || len(first.Attempts) != 2 || f.calls() != 2 {
		t.Fatal(first, err, f.calls())
	}
	if first.Items[0].CategoryScope != "film" || first.Items[1].CategoryScope != "supplement" || first.Items[2].CategoryScope != "other" || first.Items[3].CategoryScope != "supplement" || first.Items[1].Magnet != "" {
		t.Fatal("分类或无磁力条目丢失", first.Items)
	}
	if first.Specs["123"].Resolution != "1080p" || first.FetchedAt.IsZero() || first.ExpiresAt.Sub(first.FetchedAt) != 2*time.Minute {
		t.Fatal("规格或缓存寿命不正确", first)
	}
	again, err := s.Search(context.Background(), in)
	if err != nil || !again.Cached || f.calls() != 2 {
		t.Fatal("搜索未复用缓存", again, err, f.calls())
	}
	fail, in.Refresh = true, true
	if _, err := s.Search(context.Background(), in); !errors.Is(err, dmhy.ErrNetwork) {
		t.Fatal(err)
	}
	in.Refresh = false
	if _, err := s.Search(context.Background(), in); err != nil || f.calls() != 3 {
		t.Fatal("刷新失败覆盖了已有缓存", err, f.calls())
	}
	fail = false
	in.Page = 2
	page, err := s.Search(context.Background(), in)
	if err != nil || page.Query != "empty" || len(page.Attempts) != 1 || f.calls() != 4 {
		t.Fatal("第二页错误触发原名兜底", page, err, f.calls())
	}
	in.Page, in.Group = 1, "45"
	group, err := s.Search(context.Background(), in)
	if err != nil || group.Query != "empty" || len(group.Attempts) != 1 || f.calls() != 5 {
		t.Fatal("字幕组搜索错误触发原名兜底", group, err, f.calls())
	}
	for _, invalid := range []DmhySearchInput{{Query: ""}, {Query: "test", Group: "45&evil=1"}, {Query: "test", Page: -1}, {Query: "test", Fallback: strings.Repeat("x", 201)}} {
		if _, err := s.Search(context.Background(), invalid); !errors.Is(err, dmhy.ErrInput) || f.calls() != 5 {
			t.Fatal("无效参数访问了上游", invalid, err, f.calls())
		}
	}
}

func TestDmhySearchNoMagnetDoesNotFallbackAndRequestsCoalesce(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f := &dmhyServiceTransport{fetch: func(ctx context.Context, _ string) ([]byte, error) {
		once.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return dmhyServicePage(dmhyServiceRow("123", "无磁力漫画", "3", "")), nil
		}
	}}
	s := newDmhyTestService(f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Search(ctx, DmhySearchInput{Query: "test"}); !errors.Is(err, context.Canceled) || f.calls() != 0 {
		t.Fatal("取消未阻止请求", err)
	}
	var wg sync.WaitGroup
	wg.Add(6)
	for i := 0; i < 6; i++ {
		go func() {
			defer wg.Done()
			result, err := s.Search(context.Background(), DmhySearchInput{Query: "test", Fallback: "original"})
			if err != nil || result.Query != "test" || len(result.Items) != 1 || result.Items[0].Magnet != "" {
				t.Error("没有磁力的原帖不应触发改词", result, err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if f.calls() != 1 {
		t.Fatal("相同请求未合并", f.calls())
	}
}

func TestDmhySearchCancellationAndPagination(t *testing.T) {
	started := make(chan struct{})
	f := &dmhyServiceTransport{fetch: func(ctx context.Context, _ string) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	s := newDmhyTestService(f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := s.Search(ctx, DmhySearchInput{Query: "test"})
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("请求没有开始")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("请求中途取消未传递", err)
		}
	case <-time.After(time.Second):
		t.Fatal("请求取消后没有退出")
	}
	f.fetch = func(_ context.Context, endpoint string) ([]byte, error) {
		u, err := url.Parse(endpoint)
		if err != nil || u.Query().Get("keyword") != "test" || u.Query().Get("order") != "date-desc" || u.Path == "/topics/list/page/2" && u.Query().Get("team_id") != "45" {
			t.Error("分页或发布组参数未正确传递", endpoint, err)
		}
		body := dmhyServicePage(dmhyServiceRow("123", "Show", "31", ""))
		return append(body, []byte(`<a href="/topics/list/page/3?keyword=test&amp;team_id=45">下一頁</a>`)...), nil
	}
	if result, err := s.Search(context.Background(), DmhySearchInput{Query: "test"}); err != nil || len(result.Items) != 1 || f.calls() != 2 {
		t.Fatal("取消污染了同一关键词的后续请求", result, err, f.calls())
	}
	result, err := s.Search(context.Background(), DmhySearchInput{Query: "test", Group: "45", Page: 2})
	if err != nil || !result.HasMore || result.Page != 2 || result.Group != "45" || len(result.Items) != 1 || f.calls() != 3 {
		t.Fatal("取消污染后续请求或分页结果丢失", result, err, f.calls())
	}
}
