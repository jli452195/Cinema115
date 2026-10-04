package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"strm115/backend/internal/client/emby"
	"testing"
)

// Controlled canary: real Emby HTTP client, real SQLite and provisioning service;
// only the external Emby server is isolated and fault-injected.
func TestProvisioningHTTPCanary(t *testing.T) {
	s, f, owner := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, owner)
	if err := s.SetChildLimit(ctx, owner, 1); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Emby-Token") != "canary-key" {
			http.Error(w, "unauthorized", 401)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/emby")
		var value any
		var err error
		switch {
		case path == "/Library/VirtualFolders":
			value = []map[string]any{{"ItemId": "library", "Guid": "guid-movies", "Name": "Movies"}}
		case path == "/Users" && r.Method == http.MethodGet:
			value, err = f.Users(r.Context())
		case path == "/Users/New":
			var input struct{ Name string }
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				http.Error(w, "bad request", 400)
				return
			}
			value, err = f.CreateManagedUser(r.Context(), input.Name)
			// Simulate an upstream gateway losing a successfully committed response.
			http.Error(w, "lost reply", 502)
			return
		case strings.HasPrefix(path, "/Users/"):
			parts := strings.Split(strings.TrimPrefix(path, "/Users/"), "/")
			id := parts[0]
			if r.Method == http.MethodGet {
				value, err = f.ManagedUser(r.Context(), id)
				break
			}
			var input map[string]any
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				http.Error(w, "bad request", 400)
				return
			}
			if len(parts) == 1 {
				err = f.RenameManagedUser(r.Context(), id, input["Name"].(string))
			} else if parts[1] == "Policy" {
				err = f.SetManagedPolicy(r.Context(), id, input)
			} else if parts[1] == "Password" {
				err = f.SetManagedPassword(r.Context(), id, input["NewPw"].(string))
			} else {
				http.NotFound(w, r)
				return
			}
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "upstream failure", 503)
			return
		}
		if value == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()
	s.NewEmby = func(string, string) memberEmbyClient { return emby.New(server.URL, "canary-key") }
	a, invite := provisioningInvite(t, s, "assigned")
	f.failEnable = true
	if _, err := a.Register(ctx, "http-reader", "password123", invite.Code); err == nil {
		t.Fatal("activation failure reported success")
	}
	f.failEnable = false
	u, err := a.Register(ctx, "http-reader", "password123", invite.Code)
	if err != nil || s.record(u.ID).Source != "assigned" {
		t.Fatal(u, err)
	}
	input := ChildCreateInput{RequestID: "http-canary-child", Name: "http-child", Password: "password123"}
	f.failEnable = true
	if err := s.CreateChild(ctx, owner, input); err == nil {
		t.Fatal("child activation failure reported success")
	}
	f.failEnable = false
	if err := s.CreateChild(ctx, owner, input); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateChild(ctx, owner, input); err != nil {
		t.Fatal(err)
	}
	view, err := s.Children(ctx, owner)
	if err != nil || view.Used != 1 || !view.Items[0].Completed || f.created != 2 {
		t.Fatal(view, err, f.created)
	}
	child := s.record(view.Items[0].UserID)
	if _, err := s.portalUser(ctx, child.UserID); err == nil {
		t.Fatal("child website access accepted")
	}
	for _, id := range []string{u.EmbyID, child.EmbyID} {
		policy := f.users[id].Policy
		if policy["IsDisabled"] != false || policy["EnableMediaPlayback"] != true || policy["EnablePlaybackRemuxing"] != false || policy["EnableVideoPlaybackTranscoding"] != false || policy["EnableAudioPlaybackTranscoding"] != false {
			t.Fatal("incorrect final policy", id, policy)
		}
	}
}
