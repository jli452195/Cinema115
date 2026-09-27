package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"strm115/backend/internal/service"
	"testing"
	"time"
)

func TestProxyDiagnosticHeadersRedactCredentials(t *testing.T) {
	headers := http.Header{"User-Agent": {"Yamby/1"}, "Range": {"bytes=10-"}, "Cookie": {"uid-secret"}, "Authorization": {"Bearer auth-secret"}, "X-Emby-Token": {"emby-secret"}, "X-Api-Key": {"key-secret"}, "X-Custom": {"unknown-secret"}, "Referer": {"http://emby/secret-path?api_key=query-secret"}, "Set-Cookie": {"session-secret"}}
	text := proxyDiagnosticHeaders(headers, "nas:8098")
	for _, secret := range []string{"uid-secret", "auth-secret", "emby-secret", "key-secret", "unknown-secret", "query-secret", "session-secret", "secret-path"} {
		if strings.Contains(text, secret) {
			t.Fatal("credential exposed", secret)
		}
	}
	for _, value := range []string{"Yamby/1", "bytes=10-", "nas:8098"} {
		if !strings.Contains(text, value) {
			t.Fatal("diagnostic missing", value)
		}
	}
}

func TestProxyQuietTrafficAggregatesButErrorsRemain(t *testing.T) {
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	defer server.Close()
	logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: server.URL, Debug: true}, nil, NewShareAPI(func() string { return "" }), logs).(*embyProxyHandler)
	for i := 0; i < 100; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/Items/1/Images/Primary", nil))
	}
	if len(logs.Snapshot()) != 0 {
		t.Fatal("image log flood")
	}
	h.debugQuietSince = time.Now().Add(-31 * time.Second)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/Sessions/Playing/Progress", nil))
	entries := logs.Snapshot()
	if len(entries) != 1 || !strings.Contains(entries[0].Message, "101") {
		t.Fatal("missing aggregate")
	}
	status = 502
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/Items/1/Images/Primary", nil))
	if len(logs.Snapshot()) != 2 || logs.Snapshot()[0].Details.Status != 502 {
		t.Fatal("error suppressed")
	}
}

func TestProxyGroupedFullLinkNotTruncated(t *testing.T) {
	logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
	ctx, finish := logs.BeginRequest(logs.Trace(context.Background()), true)
	link := "https://cdn.115.com/file?signature=" + strings.Repeat("a", 1800)
	for i := 0; i < 20; i++ {
		logs.Add(ctx, "INFO", "解析步骤")
	}
	logs.Add(ctx, "INFO", "播放取链结果：朋友分享直取")
	logs.AddDetails(ctx, "DEBUG", "反代调试：HTTP 302", service.ProxyLogDetails{URL: link, Status: 302})
	finish()
	entries := logs.Snapshot()
	if len(entries) != 1 || entries[0].Details.URL != link || !strings.Contains(entries[0].Details.Steps, "解析步骤") {
		t.Fatal("group or full URL lost")
	}
	entries[0].Details.URL = "changed"
	if logs.Snapshot()[0].Details.URL != link {
		t.Fatal("snapshot shared mutation")
	}
	raw, _ := json.Marshal(logs.Snapshot())
	if !strings.Contains(string(raw), link) {
		t.Fatal("URL missing in JSON")
	}
}
