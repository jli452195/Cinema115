package p115

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
		{"magnet base32", "magnet:?xt=urn:btih:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", true},
		{"ed2k", "ed2k://|file|movie.mkv|123|0123456789abcdef0123456789abcdef|/", true},
		{"http", "https://example.com/movie.mkv", false},
		{"empty", "", false},
		{"ed2k bad hash", "ed2k://|file|movie.mkv|123|bad|/", false},
		{"ed2k non hex", "ed2k://|file|movie.mkv|123|gggggggggggggggggggggggggggggggg|/", false},
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

func TestParseCloudResourceNormalizesStableHashes(t *testing.T) {
	magnet, err := ParseCloudResource("magnet:?xt=urn:btih:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&dn=test")
	if err != nil || magnet.Protocol != "magnet" || magnet.Hash != strings.Repeat("0", 40) {
		t.Fatalf("base32 magnet=%+v err=%v", magnet, err)
	}
	ed2k, err := ParseCloudResource("ed2k://|file|movie.mkv|123|0123456789ABCDEF0123456789ABCDEF|/")
	if err != nil || ed2k.Protocol != "ed2k" || ed2k.Hash != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("ed2k=%+v err=%v", ed2k, err)
	}
	if !ed2k.MatchesTaskHash("0123456789ABCDEF0123456789ABCDEF") || ed2k.MatchesTaskHash(strings.Repeat("f", 32)) {
		t.Fatal("ED2K 任务 Hash 没有精确匹配")
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

func TestCloudClientAddURLRejectsUpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"state":false,"error":"任务已存在"}`)
	}))
	defer server.Close()
	client := NewCloudClient()
	client.HTTP, client.BaseURL = server.Client(), server.URL
	_, err := client.AddURL(context.Background(), "UID=test", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", "0")
	if err == nil || !strings.Contains(err.Error(), "任务已存在") {
		t.Fatalf("upstream error=%v", err)
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
	if len(page.Tasks) != 1 || page.Tasks[0].InfoHash != "abc" || page.Tasks[0].Status != 1 || !page.Tasks[0].StatusKnown || page.Tasks[0].Progress != 50 || page.Tasks[0].DoneBytes != 50 {
		t.Fatalf("page=%+v", page)
	}
}

func TestCloudClientLookupAndDeleteExactTaskWithoutDeletingFiles(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef01234567"
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/web/" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("ac") != "get_user_task" || r.URL.Query().Get("info_hash") != hash {
				t.Fatalf("unsafe lookup query=%v", r.URL.Query())
			}
			_, _ = io.WriteString(w, `{"state":1,"info_hash":"0123456789ABCDEF0123456789ABCDEF01234567","status":1,"display_status":"finished","display_percent":100}`)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("ac") != "task_del" || r.Form.Get("hash[0]") != strings.ToUpper(hash) || r.Form.Get("flag") != "" {
			t.Fatalf("unsafe delete form=%v", r.Form)
		}
		deleted = true
		_, _ = io.WriteString(w, `{"state":1}`)
	}))
	defer server.Close()
	client := NewCloudClient()
	client.HTTP, client.BaseURL = server.Client(), server.URL
	resource, _ := ParseCloudResource("magnet:?xt=urn:btih:" + hash)
	task, found, err := client.LookupTask(context.Background(), "UID=test", resource)
	if err != nil || !found || task.InfoHash != strings.ToUpper(hash) {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
	if err := client.DeleteTaskKeepFiles(context.Background(), "UID=test", task.InfoHash); err != nil || !deleted {
		t.Fatalf("delete=%v deleted=%v", err, deleted)
	}
}

func TestCloudClientLookupTaskTreats10021AsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/web/" || r.URL.Query().Get("ac") != "get_user_task" {
			t.Fatalf("request=%s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"state":false,"errcode":10021}`)
	}))
	defer server.Close()
	client := NewCloudClient()
	client.HTTP, client.BaseURL = server.Client(), server.URL
	resource, _ := ParseCloudResource("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	_, found, err := client.LookupTask(context.Background(), "UID=test", resource)
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestCloudTaskRedoReadyHandlesSingleTaskFinishedShape(t *testing.T) {
	task, err := ParseCloudTask(json.RawMessage(`{"state":true,"info_hash":"0123456789abcdef0123456789abcdef01234567","status":1,"display_status":"finished","display_percent":100,"status_text":"下载成功"}`))
	if err != nil {
		t.Fatal(err)
	}
	ready, known := task.RedoReady()
	if !ready || !known || task.Progress != 100 || task.DisplayStatus != "finished" {
		t.Fatalf("task=%+v ready=%v known=%v", task, ready, known)
	}
	running := CloudTask{Status: 1, StatusKnown: true, DisplayStatus: "downloading"}
	if ready, known := running.RedoReady(); ready || !known {
		t.Fatalf("running ready=%v known=%v", ready, known)
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
