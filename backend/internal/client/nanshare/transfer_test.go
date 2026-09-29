package nanshare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransferReceiptsAndPayload(t *testing.T) {
	for _, tc := range []struct {
		name, raw, code string
		success         bool
	}{
		{"success", `{"ok":true,"success":true,"code":"ok","count":1,"results":[{"success":true,"message":"✅ 115转存成功","url":"secret-url"}]}`, "ok", true},
		{"duplicate", `{"ok":false,"success":false,"count":0,"results":[{"success":false,"message":"⚠️ 转存失败：你已经转存过该文件 secret-cookie","url":"secret-url"}]}`, "already_saved", false},
		{"points", `{"ok":false,"success":false,"message":"无法确认资源积分，已按积分限制策略跳过 secret-token"}`, "points_unknown", false},
		{"other", `{"ok":false,"success":false,"message":"Cookie=secret-cookie"}`, "failed", false},
		{"partial", `{"ok":false,"success":false,"count":1,"results":[{"success":true},{"success":false,"message":"failed"}]}`, "partial", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var input struct {
					Source  string   `json:"source"`
					PanType string   `json:"pan_type"`
					Folder  string   `json:"folder_id"`
					Links   []string `json:"links"`
					Slug    string   `json:"slug"`
				}
				if r.URL.Path != "/api/discover/resource/transfer" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer private-session" || json.NewDecoder(r.Body).Decode(&input) != nil || input.Source != "hdhive" || input.PanType != "115" || input.Folder != "3486868885414610176" || input.Slug != "slug" || input.Links == nil || len(input.Links) != 0 {
					t.Error("unexpected transfer request")
				}
				_, _ = w.Write([]byte(tc.raw))
			}))
			defer server.Close()
			c, _ := New(server.URL, "private-session")
			got, err := c.TransferResource(context.Background(), "hdhive", "115", "3486868885414610176", []string{}, "slug")
			if err != nil || got.Code != tc.code || got.Success != tc.success || calls != 1 {
				t.Fatal(got, err, calls)
			}
			if strings.Contains(got.Message, "secret") {
				t.Fatal("upstream secret leaked")
			}
		})
	}
}

func TestTransferOptionsPreserveLargeDirectoryIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"success":true,"data":{"use_pan115_config":"saved-account","transfer_folders":[{"cid":3486868885414610176,"name":"numeric"},{"cid":"3486868885414610177","name":"string"},{"cid":"invalid"}]}}`))
	}))
	defer server.Close()
	c, _ := New(server.URL, "session")
	options, err := c.TransferOptions(context.Background())
	if err != nil || len(options.Folders) != 2 || options.Folders[0].ID != "3486868885414610176" || options.Folders[1].ID != "3486868885414610177" {
		t.Fatal(options, err)
	}
}

func TestTransferUncertainReceiptsAndHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		raw      string
		wantErr  error
		wantCode string
	}{
		{"empty success", 200, `{"ok":true,"success":true}`, ErrFormat, ""},
		{"conflicting flags", 200, `{"ok":false,"success":true,"count":1}`, ErrFormat, ""},
		{"missing receipts", 200, `{"ok":true,"success":true,"count":2,"results":[{"success":true}]}`, ErrFormat, ""},
		{"points rejection", 400, `{"ok":false,"success":false,"message":"无法确认资源积分"}`, nil, "points_unknown"},
		{"unauthorized", 401, `{}`, ErrAuth, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.raw))
			}))
			defer server.Close()
			client, _ := New(server.URL, "session")
			got, err := client.TransferResource(context.Background(), "hdhive", "115", "123", []string{}, "slug")
			if !errors.Is(err, tc.wantErr) || got.Code != tc.wantCode || calls != 1 {
				t.Fatalf("code=%q err=%v requests=%d", got.Code, err, calls)
			}
		})
	}
}
