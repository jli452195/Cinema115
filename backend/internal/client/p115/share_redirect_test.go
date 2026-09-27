package p115

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestDownloadURLUsesPerRequestUserAgent(t *testing.T) {
	var mu sync.Mutex
	seen := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/os_windows/2.0/share/downurl" || r.Method != "GET" || r.URL.Query().Get("dl") != "0" || r.Header.Get("Cookie") != "fixed-cookie" {
			t.Error("下载查询请求不正确")
		}
		mu.Lock()
		seen[r.UserAgent()]++
		mu.Unlock()
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	client := NewShareClient()
	client.AppURL = server.URL
	var wg sync.WaitGroup
	for _, ua := range []string{"player-a", "player-b"} {
		wg.Add(1)
		go func(ua string) {
			defer wg.Done()
			_, err := client.DownloadURLWithUA(context.Background(), &ShareInfo{Code: "abc"}, "123", "fixed-cookie", ua)
			if err == nil {
				t.Error("预期失败")
			}
		}(ua)
	}
	wg.Wait()
	_, _ = client.DownloadURL(context.Background(), &ShareInfo{Code: "abc"}, "123", "fixed-cookie")
	if seen["player-a"] != 1 || seen["player-b"] != 1 || seen[PlaybackUserAgent] != 1 {
		t.Fatal("并发 UA 串用或改变原媒体代理 UA", seen)
	}
}
