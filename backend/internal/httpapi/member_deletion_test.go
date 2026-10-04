package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

// Controlled canary exercises authenticated admin routes, real Emby transport,
// persistent deletion progress and SQLite cleanup. No live server is contacted.
func TestMemberDeletionHTTPCanary(t *testing.T) {
	ctx := context.Background()
	id := strings.Repeat("a", 32)
	var mu sync.Mutex
	user := emby.ManagedUser{ID: id, Name: "canary-reader", Policy: map[string]any{"IsAdministrator": false, "IsDisabled": false, "EnableMediaPlayback": true, "EnableContentDownloading": true}}
	exists, failDelete, deletes := true, true, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Emby-Token") != "canary-key" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch {
		case r.URL.Path == "/emby/Users":
			users := []emby.ManagedUser{}
			if exists {
				users = append(users, user)
			}
			json.NewEncoder(w).Encode(users)
		case r.URL.Path == "/emby/Users/"+id && r.Method == http.MethodDelete:
			deletes++
			if failDelete {
				http.Error(w, "offline", 503)
				return
			}
			exists = false
			http.Error(w, "committed response lost", 502)
		case r.URL.Path == "/emby/Users/"+id && r.Method == http.MethodGet:
			if !exists {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(user)
		case r.URL.Path == "/emby/Users/"+id+"/Policy" && r.Method == http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&user.Policy); err != nil {
				http.Error(w, "invalid", 400)
				return
			}
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "emby.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	cfg := service.EmbyConfig{URL: upstream.URL, APIKey: "canary-key", ServerID: "canary-server"}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(string(raw)); err != nil {
		t.Fatal(err)
	}
	server, err := service.NewEmbyService(store, db)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := service.OpenP115AccountsSQLite(ctx, db, t.TempDir(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	members, err := service.NewMemberPlayback(ctx, db, accounts, server, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := members.ObserveUser(ctx, cfg, user)
	if err != nil {
		t.Fatal(err)
	}
	auth := service.NewPortalAuth(db, "admin", "password123", "secret")
	auth.Members = members
	token, _, err := auth.LoginAdmin(ctx, "admin", "password123")
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterMemberPlayback(r.Group("/api/me", PortalAuthenticated(auth)), r.Group("/api/admin", PortalAuthenticated(auth), PortalAdminOnly()), members)
	call := func(path string, body any, credential string, want int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/admin/member-playback/deletions"+path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var data map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	call("/preview", map[string]any{"key": profile.ID}, "invalid", 401)
	preview := call("/preview", map[string]any{"key": profile.ID}, token, 200)
	in := map[string]any{"key": profile.ID, "fingerprint": preview["fingerprint"], "confirmName": "wrong"}
	call("", in, token, 400)
	in["confirmName"] = "canary-reader"
	first := call("", in, token, 202)["job"].(map[string]any)
	if first["stage"] != "pending" {
		t.Fatal(first)
	}
	if _, err := db.PortalUser(ctx, profile.ID); err != nil {
		t.Fatal("local identity removed while remote delete failed")
	}
	mu.Lock()
	failDelete = false
	mu.Unlock()
	last := call("", in, token, 200)["job"].(map[string]any)
	if last["stage"] != "completed" {
		t.Fatal(last)
	}
	call("", in, token, 200)
	mu.Lock()
	count := deletes
	mu.Unlock()
	if count != 2 {
		t.Fatal("unexpected upstream delete count", count)
	}
	if _, err := db.PortalUser(ctx, profile.ID); err == nil {
		t.Fatal("profile survived deletion")
	}
	if err := db.Check(ctx); err != nil {
		t.Fatal(err)
	}
	// Even an accidentally unguarded parent group cannot expose delete routes.
	for _, role := range []string{"", "user"} {
		isolated := gin.New()
		isolated.Use(func(c *gin.Context) { c.Set("portal_role", role) })
		registerMemberDeletion(isolated.Group("/delete"), nil, func(*gin.Context, error) { t.Fatal("service reached without admin role") })
		w := httptest.NewRecorder()
		isolated.ServeHTTP(w, httptest.NewRequest("GET", "/delete/deletions", nil))
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}
