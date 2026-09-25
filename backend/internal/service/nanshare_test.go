package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNanShareLoginPersistenceAndFeatureChecks(t *testing.T) {
	account := "explicit-re0"
	expired := false
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			t.Error("missing request header")
		}
		if r.URL.Path == "/api/session/login" {
			var input map[string]string
			if json.NewDecoder(r.Body).Decode(&input) != nil || input["username"] != "user" || input["password"] != "test-password" {
				t.Error("wrong login payload")
			}
			_, _ = w.Write([]byte(`{"ok":true,"success":true,"data":{"token":"test-secret-token","username":"user"}}`))
			return
		}
		if expired || r.Header.Get("Authorization") != "Bearer test-secret-token" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"test-secret-token"}`))
			return
		}
		switch r.URL.Path {
		case "/api/session":
			_, _ = w.Write([]byte(`{"success":true,"data":{"username":"user"}}`))
		case "/api/discover/resource/options":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "hdhive_account": account})
		case "/api/strm/share/config":
			_, _ = w.Write([]byte(`{"success":true,"data":{"enabled":true,"enable_normal_share_mode":false,"enable_reshare_mode":true,"strm_folders":[{"path":"/output"}],"cd2_token":"remote-secret"}}`))
		case "/api/discover/resource/hdhive":
			searchCalls++
			var input struct {
				ID      int    `json:"tmdb_id"`
				Kind    string `json:"media_type"`
				Account string `json:"account_name"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.ID != 106449 || input.Kind != "tv" || input.Account != "explicit-re0" {
				t.Error("wrong search payload")
			}
			_, _ = w.Write([]byte(`{"ok":true,"resources":[{"key":"a","source":"hdhive","pan_type":"115"},{"key":"b","source":"hdhive","pan_type":"quark"}]}`))
		default:
			t.Errorf("unexpected remote mutation: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "session.enc")
	store, err := NewSessionStore(file, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewNanShareService(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	view, err := svc.Configure(ctx, NanShareConfigInput{BaseURL: server.URL, Username: "user", Password: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	if !view.Session.OK || !view.RE0.OK || !view.Share.OK {
		t.Fatalf("feature checks failed: %+v", view)
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), "secret") || strings.Contains(store.Value(), "test-password") {
		t.Fatal("credentials leaked")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "test-secret-token") {
		t.Fatal("token persisted unencrypted")
	}
	reopened, err := NewSessionStore(file, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewNanShareService(reopened)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.View().Configured || restored.View().Session.OK {
		t.Fatal("restored session must require verification")
	}
	result, err := restored.SearchTest(ctx, "tv", 106449)
	if err != nil || result.Total != 2 || result.P115 != 1 {
		t.Fatalf("search failed: %+v %v", result, err)
	}
	account = ""
	view, err = restored.Verify(ctx)
	if err != nil || view.RE0.OK || !view.Session.OK {
		t.Fatalf("missing account falsely ready: %+v %v", view, err)
	}
	if _, err := restored.SearchTest(ctx, "tv", 106449); err == nil || searchCalls != 1 {
		t.Fatal("empty account fell back")
	}
	// Feature configuration is independent from successful login.
	view, err = restored.Configure(ctx, NanShareConfigInput{BaseURL: server.URL, Token: "test-secret-token"})
	if err != nil || !view.Configured || view.RE0.OK {
		t.Fatalf("login rejected due to missing RE0 account: %v", err)
	}
	before := restored.View()
	if _, err := restored.Configure(ctx, NanShareConfigInput{BaseURL: server.URL, Token: "invalid"}); err == nil {
		t.Fatal("invalid token accepted")
	}
	if restored.View() != before {
		t.Fatal("failed login replaced saved configuration")
	}
	expired = true
	view, err = restored.Verify(ctx)
	if err != nil || view.Session.OK || view.RE0.OK || view.Share.OK {
		t.Fatal("expired session still marked verified")
	}
	if err := restored.Clear(); err != nil {
		t.Fatal(err)
	}
	if restored.View().Configured || reopened.Value() != "" {
		t.Fatal("clear retained session")
	}
}

// Explicit opt-in; credentials are supplied by the operator, never stored in test fixtures.
func TestNanShareLive(t *testing.T) {
	base, token := os.Getenv("NANSHARE_TEST_URL"), os.Getenv("NANSHARE_TEST_TOKEN")
	if base == "" || token == "" {
		t.Skip("live NanShare credentials not provided")
	}
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "session.enc"), "temporary-live-test-key")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewNanShareService(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	view, err := svc.Configure(ctx, NanShareConfigInput{BaseURL: base, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if !view.Session.OK || !view.RE0.OK || !view.Share.OK {
		t.Fatalf("remote checks: session=%t RE0=%t share=%t", view.Session.OK, view.RE0.OK, view.Share.OK)
	}
	result, err := svc.SearchTest(ctx, "tv", 106449)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("NanShare session/config checks passed; TMDB 106449: total=%d, 115=%d", result.Total, result.P115)
	search, err := svc.Search(ctx, "tv", 106449, false)
	if err != nil {
		t.Fatal(err)
	}
	options, err := svc.ShareOptions(ctx, search.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Detail search: 115=%d; STRM options: modes=%d folders=%d accountBound=%t (no task submitted)", len(search.Items), len(options.Modes), len(options.Folders), options.AccountID != "")
}
