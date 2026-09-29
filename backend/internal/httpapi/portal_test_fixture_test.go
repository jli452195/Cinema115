package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
	"testing"
)

// Real HTTP Emby authentication adapter, shared by tests of member-only APIs.
func portalTestAuth(t *testing.T, db *storage.Store, name, password, secret string) *service.PortalAuth {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/emby/Users/")
		username := "reader"
		if r.URL.Path == "/emby/Users/AuthenticateByName" {
			var in struct{ Username, Pw string }
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Pw != "test-password" && in.Pw != "reader-password" {
				w.WriteHeader(401)
				return
			}
			username = in.Username
			sum := sha256.Sum256([]byte(username))
			id = hex.EncodeToString(sum[:16])
			json.NewEncoder(w).Encode(map[string]any{"ServerId": "server", "User": map[string]any{"Id": id, "Name": username, "Policy": map[string]any{"IsDisabled": false, "IsAdministrator": false}}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"Id": id, "Name": username, "Policy": map[string]any{"IsDisabled": false, "IsAdministrator": false}})
	}))
	t.Cleanup(server.Close)
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "emby.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(service.EmbyConfig{URL: server.URL, APIKey: "key", ServerID: "server"})
	if err := store.Save(string(raw)); err != nil {
		t.Fatal(err)
	}
	embyService, err := service.NewEmbyService(store, db)
	if err != nil {
		t.Fatal(err)
	}
	members, err := service.NewMemberPlayback(context.Background(), db, nil, embyService, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	auth := service.NewPortalAuth(db, name, password, secret)
	auth.Members = members
	return auth
}

func portalTestUser(t *testing.T, auth *service.PortalAuth, name, password string) model.PortalUser {
	t.Helper()
	_, u, err := auth.Login(context.Background(), name, password)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
