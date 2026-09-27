package httpapi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/service"
)

func TestProxyDebugToggleRoutesAndRedaction(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://cdn.115.com/private-location?token=secret-location")
		w.WriteHeader(307)
		_, _ = io.WriteString(w, "unchanged")
	}))
	defer upstream.Close()
	for _, enabled := range []bool{false, true} {
		logs := service.NewEmbyProxyLogs(&service.GenerationLogs{})
		h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, Debug: enabled}, nil, NewShareAPI(func() string { return "" }), logs)
		for _, tc := range []struct {
			path, route string
			status      int
		}{
			{"/api/stream/share/secret-signature?api_key=secret-token&receive_code=secret-password", "普通透传", 307},
			{"/emby/Items/secret-id/Images/Primary", "图片透传", 307},
			{"/Videos/movie/master.m3u8", "拒绝转码", 403},
			{"/cinema115/play", "302 解析入口", 401},
		} {
			before := len(logs.Snapshot())
			r := httptest.NewRequest("GET", tc.path, nil)
			r.Header.Set("User-Agent", "Yamby/1 secret-ua")
			r.Header.Set("Cookie", "secret-cookie")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatal("response changed", w.Code)
			}
			if tc.status == 307 && (w.Body.String() != "unchanged" || !strings.Contains(w.Header().Get("Location"), "secret-location")) {
				t.Fatal("passthrough changed")
			}
			debug := make([]service.GenerationLog, 0, 2)
			entries := logs.Snapshot()
			for _, entry := range entries[:len(entries)-before] {
				if entry.Details != nil && entry.Details.RequestHeaders != "" {
					debug = append(debug, entry)
				}
				raw, _ := json.Marshal(entry)
				for _, secret := range []string{"secret-signature", "secret-token", "secret-password", "secret-id", "secret-cookie", "secret-location"} {
					if strings.Contains(string(raw), secret) {
						t.Fatal("debug leaked secret", secret)
					}
				}
			}
			if enabled && tc.status != 307 {
				if len(debug) != 1 {
					t.Fatal("missing request/response logs", len(debug))
				}
				all := debug[0].Message + debug[0].Details.Steps
				if !strings.Contains(all, tc.route) || !strings.Contains(all, "yamby") || !strings.Contains(all, fmt.Sprintf("HTTP %d", tc.status)) {
					t.Fatal("missing debug details", all)
				}
				if !strings.Contains(debug[0].Details.RequestHeaders, "Yamby/1 secret-ua") {
					t.Fatal("raw UA missing")
				}
			} else if len(debug) != 0 {
				t.Fatal("debug switch ignored")
			}
		}
	}
}

func TestProxyDebugPreservesStreamingAndUpgrade(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/socket" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: first\n\n")
			_ = http.NewResponseController(w).Flush()
			_, _ = io.WriteString(w, "data: second\n\n")
			return
		}
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		if err := rw.Flush(); err != nil {
			return
		}
		data := make([]byte, 4)
		if _, err := io.ReadFull(rw, data); err == nil {
			_, _ = conn.Write(data)
		}
	}))
	defer upstream.Close()
	h := NewEmbyProxyHandler(service.EmbyProxyConfig{URL: upstream.URL, Debug: true}, nil, NewShareAPI(func() string { return "" }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/events", nil))
	if !rec.Flushed || rec.Code != 200 || rec.Body.String() != "data: first\n\ndata: second\n\n" {
		t.Fatal("streaming changed")
	}
	proxy := httptest.NewServer(h)
	defer proxy.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(proxy.URL, "http://"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(conn, "GET /socket HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 101 {
		t.Fatal("upgrade failed", resp.StatusCode)
	}
	_, _ = io.WriteString(conn, "ping")
	echo := make([]byte, 4)
	if _, err := io.ReadFull(reader, echo); err != nil || string(echo) != "ping" {
		t.Fatal("upgrade data failed", err)
	}
}
