package emby

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLibraryItemIDAndPolicyGUIDAreDistinct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emby/Library/VirtualFolders" {
			t.Error("unexpected endpoint")
		}
		_, _ = w.Write([]byte(`[{"ItemId":"6218","Guid":"deb16d44daf74d93a84cb829ea482369","Name":"Movies"}]`))
	}))
	defer server.Close()
	libraries, err := New(server.URL, "key").Libraries(context.Background())
	if err != nil || len(libraries) != 1 || libraries[0].ID != "6218" || libraries[0].GUID != "deb16d44daf74d93a84cb829ea482369" {
		t.Fatal(libraries, err)
	}
}

func TestManagedUserWritesUseDocumentedCreationAndKeepSecretsOffURL(t *testing.T) {
	id := strings.Repeat("a", 32)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.Header.Get("X-Emby-Token") != "private-key" || r.URL.RawQuery != "" {
			t.Error("unsafe request")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/emby/Users/New":
			if body["Name"] != "c115_test" || body["Password"] != nil || body["CopyFromUserId"] != nil {
				t.Error("unexpected creation credentials")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": id, "Name": "c115_test"})
		case "/emby/Users/" + id + "/Password":
			if body["NewPw"] != "private-password" || body["ResetPassword"] != false {
				t.Error("password request incorrect")
			}
			w.WriteHeader(204)
		case "/emby/Users/" + id + "/Policy":
			w.WriteHeader(204)
		case "/emby/Users/" + id:
			if body["Id"] != id || body["Name"] != "Reader" || len(body) != 2 {
				t.Error("rename modifies fields beyond name")
			}
			w.WriteHeader(204)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer server.Close()
	client := New(server.URL, "private-key")
	ctx := context.Background()
	if _, err := client.CreateManagedUser(ctx, "c115_test"); err != nil {
		t.Fatal(err)
	}
	if err := client.SetManagedPassword(ctx, id, "private-password"); err != nil {
		t.Fatal(err)
	}
	if err := client.SetManagedPolicy(ctx, id, map[string]any{"IsDisabled": true}); err != nil {
		t.Fatal(err)
	}
	if err := client.RenameManagedUser(ctx, id, "Reader"); err != nil {
		t.Fatal(err)
	}
	if err := client.RenameManagedUser(ctx, "../admin", "Reader"); err == nil {
		t.Fatal("invalid rename target accepted")
	}
	if err := client.SetManagedPassword(ctx, "../admin", "private-password"); err == nil || calls != 4 {
		t.Fatal("invalid ID accepted")
	}
}
