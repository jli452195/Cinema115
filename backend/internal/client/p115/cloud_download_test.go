package p115

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestValidateCloudURL(t *testing.T) {
	tests := []struct {
		name string
		link string
		ok   bool
	}{
		{"magnet", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", true},
		{"ed2k", "ed2k://|file|movie.mkv|123|0123456789abcdef0123456789abcdef|/", true},
		{"http", "https://example.com/movie.mkv", false},
		{"empty", "", false},
		{"ed2k bad hash", "ed2k://|file|movie.mkv|123|bad|/", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateCloudURL(tt.link)
			if (err == nil) != tt.ok {
				t.Fatalf("ValidateCloudURL(%q) error=%v, want ok=%v", tt.link, err, tt.ok)
			}
		})
	}
}

func TestCloudClientAddURLUsesSSPEncryption(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lixianssp/" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Cookie"); got != "UID=test" {
			t.Fatalf("cookie=%q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		encoded := r.Form.Get("data")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(decoded) == 0 || len(decoded)%128 != 0 {
			t.Fatalf("data is not a valid 115 RSA payload: len=%d err=%v", len(decoded), err)
		}
		response := `{"state":1,"data":{"info_hash":"abc"}}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	defer server.Close()

	client := NewCloudClient()
	client.HTTP = server.Client()
	client.BaseURL = server.URL
	got, err := client.AddURL(context.Background(), "UID=test", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", "123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "info_hash") {
		t.Fatalf("response=%s", got)
	}
}

func TestCloudClientListTasksParsesWebResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/web/" || r.URL.Query().Get("ac") != "task_lists" {
			t.Fatalf("unexpected request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"state":true,"tasks":[{"info_hash":"abc","name":"movie","status":"1","percent":"50%","size":"100","done_size":"50"}],"count":1}`)
	}))
	defer server.Close()
	client := NewCloudClient()
	client.HTTP = server.Client()
	client.BaseURL = server.URL
	page, err := client.ListTasks(context.Background(), "UID=test", 1, 30, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 1 || page.Tasks[0].InfoHash != "abc" || page.Tasks[0].Status != 1 || page.Tasks[0].Progress != 50 || page.Tasks[0].DoneBytes != 50 {
		t.Fatalf("page=%+v", page)
	}
}

func TestCloudClientRejectsInvalidTargetCID(t *testing.T) {
	client := NewCloudClient()
	_, err := client.AddURL(context.Background(), "", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", "not-a-cid")
	if err == nil || !strings.Contains(err.Error(), "目标目录") {
		t.Fatalf("error=%v", err)
	}
}

func TestCloudClientListTasksQueryEscaping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"state":1,"list":[]}`)
	}))
	defer server.Close()
	client := NewCloudClient()
	client.HTTP = server.Client()
	client.BaseURL = server.URL
	if _, err := client.ListTasks(context.Background(), "", 0, 0, "12"); err != nil {
		t.Fatal(err)
	}
}
