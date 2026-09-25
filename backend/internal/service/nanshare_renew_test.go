package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"strm115/backend/internal/client/nanshare"
)

func TestNanShareAutomaticLogin(t *testing.T) {
	var logins, submissions atomic.Int32
	var expired, rejectLogin, rejectReads, rejectSubmit atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/session/login" {
			n := logins.Add(1)
			var input map[string]string
			if json.NewDecoder(r.Body).Decode(&input) != nil || input["username"] != "user" || input["password"] != "private-password" {
				t.Error("wrong saved credentials")
			}
			if rejectLogin.Load() {
				w.WriteHeader(401)
				return
			}
			expired.Store(false)
			_, _ = fmt.Fprintf(w, `{"ok":true,"data":{"username":"user","token":"session-%d"}}`, n)
			return
		}
		if r.URL.Path == "/api/hdhive/resource/save" {
			submissions.Add(1)
			if rejectSubmit.Load() {
				w.WriteHeader(401)
				return
			}
		}
		if expired.Load() || r.Header.Get("Authorization") != fmt.Sprintf("Bearer session-%d", logins.Load()) {
			w.WriteHeader(401)
			return
		}
		if rejectReads.Load() && r.URL.Path == "/api/discover/resource/hdhive" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/session":
			_, _ = w.Write([]byte(`{"ok":true,"data":{"username":"user"}}`))
		case "/api/discover/resource/options":
			_, _ = w.Write([]byte(`{"ok":true,"hdhive_account":"chosen"}`))
		case "/api/strm/share/config":
			_, _ = w.Write([]byte(`{"ok":true,"data":{"enabled":true,"enable_normal_share_mode":true,"enable_reshare_mode":false,"strm_folders":[],"use_pan115_config":"selected"}}`))
		case "/api/discover/resource/hdhive":
			_, _ = w.Write([]byte(`{"ok":true,"resources":[{"key":"a","slug":"a","source":"hdhive","pan_type":"115"}]}`))
		case "/api/hdhive/resource/save":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "config.enc")
	store, err := NewSessionStore(file, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewNanShareService(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.Configure(ctx, NanShareConfigInput{BaseURL: server.URL, Username: "user", Password: "private-password"}); err != nil {
		t.Fatal(err)
	}
	search, err := svc.Search(ctx, "tv", 42, false)
	if err != nil {
		t.Fatal(err)
	}
	// All operations capture the same expired token before any renewal starts.
	clients := make([]*nanshare.Client, 8)
	for i := range clients {
		clients[i], _, err = svc.snapshot()
		if err != nil {
			t.Fatal(err)
		}
	}
	expired.Store(true)
	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Add(1)
		go func(c *nanshare.Client) {
			defer wg.Done()
			if _, err := c.RE0Account(ctx); err != nil {
				t.Error(err)
			}
		}(client)
	}
	wg.Wait()
	if logins.Load() != 2 {
		t.Fatalf("concurrent requests triggered %d logins", logins.Load())
	}
	refreshed, err := svc.Search(ctx, "tv", 42, true)
	if err != nil || refreshed.ID != search.ID {
		t.Fatal("renewal invalidated search session")
	}
	// Restart restores encrypted password and refreshed token.
	reopened, err := NewSessionStore(file, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewNanShareService(reopened)
	if err != nil || !restored.View().AutoLogin {
		t.Fatal("password not restored")
	}
	expired.Store(true)
	view, err := restored.Verify(ctx)
	if err != nil || !view.Session.OK || logins.Load() != 3 {
		t.Fatalf("renew after restart: %v %+v", err, view)
	}
	// A second auth failure must stop, not loop.
	rejectReads.Store(true)
	before := logins.Load()
	if _, err := restored.SearchTest(ctx, "tv", 42); err == nil {
		t.Fatal("rejected read succeeded")
	}
	if logins.Load() != before+1 {
		t.Fatal("renewal did not stop at one attempt")
	}
	rejectReads.Store(false)
	// Never automatically replay a mutating STRM request, including auth errors.
	rejectSubmit.Store(true)
	client, _, err := restored.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	before = logins.Load()
	if err := client.Submit(ctx, "a", "normal", 0); err == nil {
		t.Fatal("rejected submission succeeded")
	}
	if submissions.Load() != 1 || logins.Load() != before {
		t.Fatal("mutation retried automatically")
	}
	// Failed renewal keeps credentials; errors and views never expose them.
	rejectLogin.Store(true)
	expired.Store(true)
	view, err = restored.Verify(ctx)
	if err != nil || view.Session.OK {
		t.Fatal("failed renewal marked online")
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), "private-password") || strings.Contains(string(encoded), "session-") {
		t.Fatal("secret leaked")
	}
	// Clearing prevents an old captured client from restoring the deleted login.
	client, _, err = restored.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Clear(); err != nil {
		t.Fatal(err)
	}
	before = logins.Load()
	if _, err := client.Session(ctx); err == nil {
		t.Fatal("old client survived clear")
	}
	if logins.Load() != before || reopened.Value() != "" {
		t.Fatal("cleared credentials resurrected")
	}
}

func TestNanShareLegacySessionRequiresPassword(t *testing.T) {
	var logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session/login" {
			logins.Add(1)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "legacy.enc"), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(map[string]any{"version": 1, "baseUrl": server.URL, "username": "user", "token": "legacy-token"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(string(legacy)); err != nil {
		t.Fatal(err)
	}
	svc, err := NewNanShareService(store)
	if err != nil {
		t.Fatal(err)
	}
	if !svc.View().Configured || svc.View().AutoLogin {
		t.Fatal("legacy state incorrectly upgraded")
	}
	view, err := svc.Verify(context.Background())
	if err != nil || view.Session.OK || !strings.Contains(view.Session.Message, "账号密码") || logins.Load() != 0 {
		t.Fatal("legacy token triggered automatic login")
	}
	var input NanShareConfigInput
	if err := json.Unmarshal([]byte(`{"baseUrl":"`+server.URL+`","token":"manual-token"}`), &input); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Configure(context.Background(), input); err == nil || logins.Load() != 0 {
		t.Fatal("manual token import still accepted")
	}
	if store.Value() != string(legacy) {
		t.Fatal("failed import changed stored configuration")
	}
}
