package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"strm115/backend/internal/client/nanshare"
)

func TestNanShareSearchSubmissionAndConfigurationChanges(t *testing.T) {
	account, p115Account, output := "re0-selected", "115-selected", "/chosen-output"
	posts := 0
	malformed := false
	var cancelDuringSubmit context.CancelFunc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer test-session" {
			t.Error("wrong session credential")
		}
		switch r.URL.Path {
		case "/api/session":
			_, _ = w.Write([]byte(`{"ok":true,"data":{"username":"user"}}`))
		case "/api/discover/resource/options":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "hdhive_account": account})
		case "/api/hdhive/config":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"enabled": true, "use_hdhive_account": account}})
		case "/api/strm/share/config":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"enabled": true, "enable_normal_share_mode": true, "enable_reshare_mode": true, "use_pan115_config": p115Account, "reshare_pan115_open_config": "open-selected", "strm_folders": []map[string]string{{"name": "chosen", "path": output}}, "cd2_token": "must-not-leak"}})
		case "/api/discover/resource/hdhive":
			var input struct {
				Account string `json:"account_name"`
				ID      int    `json:"tmdb_id"`
				Kind    string `json:"media_type"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Account != "re0-selected" || input.ID != 42 || input.Kind != "tv" {
				t.Error("wrong explicit search identity")
			}
			_, _ = w.Write([]byte(`{"ok":true,"resources":[{"source":"hdhive","key":"a","slug":"first","pan_type":"115","remark":"resource","unlock_points":10,"links":["secret-url"],"cookie":"secret-cookie"},{"source":"hdhive","key":"b","slug":"second","pan_type":"115"},{"source":"hdhive","key":"c","slug":"third","pan_type":"quark"}]}`))
		case "/api/hdhive/resource/save":
			posts++
			var input struct {
				Slug    string `json:"slug"`
				Index   int    `json:"path_index"`
				Reshare bool   `json:"enable_reshare"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Index != 0 || !input.Reshare || (input.Slug != "first" && input.Slug != "second") {
				t.Error("wrong submission payload")
			}
			if cancelDuringSubmit != nil {
				cancelDuringSubmit()
			}
			if malformed {
				_, _ = w.Write([]byte(`<html>lost receipt</html>`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"success":true}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "session.enc"), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewNanShareService(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.Configure(ctx, NanShareConfigInput{BaseURL: server.URL, Token: "test-session"}); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Search(ctx, "tv", 42, false)
	if err != nil || len(result.Items) != 2 {
		t.Fatalf("search: %+v %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	for _, secret := range []string{"secret-url", "secret-cookie", "test-session"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("sensitive upstream data leaked")
		}
	}
	options, err := svc.ShareOptions(ctx, result.ID)
	if err != nil || options.AccountID != "115-selected" || len(options.Modes) != 2 {
		t.Fatalf("options: %+v %v", options, err)
	}
	index := 0
	input := NanShareSubmitInput{RequestID: "00000000000000000000000000000001", SearchID: result.ID, Slug: "first", AccountID: options.AccountID, Mode: "reshare", FolderIndex: &index, Version: options.Version}
	badID := input
	badID.RequestID = ""
	if _, err := svc.Submit(ctx, badID); err == nil {
		t.Fatal("missing request ID accepted")
	}
	bad := input
	bad.AccountID = ""
	if _, err := svc.Submit(ctx, bad); err == nil {
		t.Fatal("empty account accepted")
	}
	bad = input
	bad.Slug = "third"
	if _, err := svc.Submit(ctx, bad); err == nil {
		t.Fatal("non-115/unlisted resource accepted")
	}
	bad = input
	bad.FolderIndex = nil
	if _, err := svc.Submit(ctx, bad); err == nil {
		t.Fatal("implicit first folder accepted")
	}
	output = "/changed-output"
	if _, err := svc.Submit(ctx, input); !errors.Is(err, nanshare.ErrChanged) {
		t.Fatalf("changed destination not rejected: %v", err)
	}
	output = "/chosen-output"
	p115Account = ""
	if _, err := svc.Submit(ctx, input); err == nil {
		t.Fatal("deleted remote account fell back")
	}
	p115Account = "115-selected"
	account = "different-re0"
	if _, err := svc.Submit(ctx, input); !errors.Is(err, nanshare.ErrChanged) {
		t.Fatalf("changed RE0 account not rejected: %v", err)
	}
	account = "re0-selected"
	if posts != 0 {
		t.Fatal("preflight failures submitted remote jobs")
	}
	closedCtx, cancel := context.WithCancel(ctx)
	cancelDuringSubmit = cancel
	receipt, err := svc.Submit(closedCtx, input)
	if err != nil || receipt.State != "accepted" || posts != 1 {
		t.Fatalf("receipt lost on browser cancellation: %+v %v", receipt, err)
	}
	cancelDuringSubmit = nil
	receipt, err = svc.Submit(ctx, input)
	if err != nil || receipt.State != "accepted" || posts != 1 {
		t.Fatal("duplicate submission")
	}
	refreshed, err := svc.Search(ctx, "tv", 42, true)
	if err != nil || refreshed.Submissions["first"].State != "accepted" {
		t.Fatal("refresh lost receipt")
	}
	if refreshed.ID != result.ID {
		t.Fatal("refresh invalidated idempotency session")
	}
	if _, err := svc.Submit(ctx, input); err != nil || posts != 1 {
		t.Fatal("same request replayed after refresh")
	}
	bad = input
	bad.Slug = "second"
	if _, err := svc.Submit(ctx, bad); err == nil {
		t.Fatal("request ID reused for another resource")
	}
	input.RequestID = "00000000000000000000000000000002"
	receipt, err = svc.Submit(ctx, input)
	if err != nil || receipt.State != "accepted" || posts != 2 {
		t.Fatalf("intentional resubmission blocked: %+v %v", receipt, err)
	}
	_, _ = svc.Submit(ctx, input)
	if posts != 2 {
		t.Fatal("resubmission request not idempotent")
	}
	input.SearchID = refreshed.ID
	input.Slug = "second"
	input.RequestID = "00000000000000000000000000000003"
	malformed = true
	receipt, err = svc.Submit(ctx, input)
	if err != nil || receipt.State != "unknown" || posts != 3 {
		t.Fatalf("unknown outcome: %+v %v", receipt, err)
	}
	_, _ = svc.Submit(ctx, input)
	if posts != 3 {
		t.Fatal("uncertain remote request retried automatically")
	}
	input.RequestID = "00000000000000000000000000000004"
	malformed = false
	receipt, err = svc.Submit(ctx, input)
	if err != nil || receipt.State != "accepted" || posts != 4 {
		t.Fatal("intentional retry after unknown result blocked")
	}
	if err := svc.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ShareOptions(ctx, refreshed.ID); !errors.Is(err, nanshare.ErrChanged) {
		t.Fatal("old search reused after clearing login")
	}
}
