package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNanShareTransferSelectionAndIdempotence(t *testing.T) {
	for _, mode := range []string{"success", "unknown", "duplicate", "points"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			remoteAccount := "remote-primary"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/discover/resource/options":
					_, _ = w.Write([]byte(`{"ok":true,"hdhive_account":"re0"}`))
				case "/api/discover/resource/hdhive":
					_, _ = w.Write([]byte(`{"ok":true,"resources":[{"source":"hdhive","pan_type":"115","slug":"one","key":"one"}]}`))
				case "/api/transfer/115/config":
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"use_pan115_config": remoteAccount, "transfer_folders": []map[string]string{{"cid": "3486868885414610176", "name": "saved"}}}})
				case "/api/discover/resource/transfer":
					posts++
					switch mode {
					case "unknown":
						_, _ = w.Write([]byte("invalid reply"))
					case "duplicate":
						_, _ = w.Write([]byte(`{"ok":false,"success":false,"results":[{"success":false,"message":"你已经转存过该文件"}]}`))
					case "points":
						_, _ = w.Write([]byte(`{"ok":false,"success":false,"message":"无法确认资源积分"}`))
					default:
						_, _ = w.Write([]byte(`{"ok":true,"success":true,"count":1,"results":[{"success":true}]}`))
					}
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			s := &NanShareService{doc: nanShareDocument{BaseURL: server.URL, Token: "session", ConfigID: "config"}}
			ctx := context.Background()
			search, err := s.Search(ctx, "tv", 42, false)
			if err != nil {
				t.Fatal(err)
			}
			input := NanShareTransferInput{RequestID: "00000000000000000000000000000001", SearchID: search.ID, Slug: "one", AccountID: "remote-primary", FolderID: "3486868885414610176"}
			bad := input
			bad.FolderID = "999"
			if _, err := s.Transfer(ctx, bad); err == nil || posts != 0 {
				t.Fatal("unconfigured directory accepted")
			}
			bad = input
			bad.Slug = "forged"
			if _, err := s.Transfer(ctx, bad); err == nil || posts != 0 {
				t.Fatal("forged resource accepted")
			}
			remoteAccount = "changed"
			if _, err := s.Transfer(ctx, input); err == nil || posts != 0 {
				t.Fatal("changed account accepted")
			}
			remoteAccount = "remote-primary"
			first, err := s.Transfer(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			second, err := s.Transfer(ctx, input)
			if err != nil || first != second || posts != 1 {
				t.Fatal("write replayed", first, second, err, posts)
			}
			want := map[string]string{"success": "accepted", "unknown": "unknown", "duplicate": "already_saved", "points": "points_unknown"}[mode]
			if first.State != want {
				t.Fatal(first)
			}
			refreshed, err := s.Search(ctx, "tv", 42, true)
			if err != nil || refreshed.Transfers["one"] != first || len(refreshed.Submissions) != 0 {
				t.Fatal("receipt lost or mixed with STRM", err)
			}
		})
	}
}
