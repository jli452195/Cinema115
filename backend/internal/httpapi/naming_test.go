package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type mediaNamingHTTPMetadata struct{}

func (mediaNamingHTTPMetadata) Details(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, Title: "作品", Date: "2026-01-01"}}, nil
}

func TestNamingTemplateHTTP(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "naming.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := portalTestAuth(t, db, "admin", "admin-password", "naming-secret")
	r := gin.New()
	RegisterPortalAuth(r, auth)
	RegisterNamingTemplates(r.Group("/api/admin", PortalAuthenticated(auth), PortalAdminOnly()), db)
	RegisterMediaNaming(r.Group("/api/admin", PortalAuthenticated(auth), PortalAdminOnly()), service.MediaNaming{Templates: db, Metadata: mediaNamingHTTPMetadata{}})
	call := func(method, endpoint, token, body string, want int) map[string]json.RawMessage {
		t.Helper()
		req := httptest.NewRequest(method, endpoint, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, endpoint, w.Code, w.Body.String())
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	var token, user string
	if err := json.Unmarshal(call("POST", "/api/auth/admin/login", "", `{"username":"admin","password":"admin-password"}`, 200)["token"], &token); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(call("POST", "/api/auth/login", "", `{"username":"reader","password":"test-password"}`, 200)["token"], &user); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, endpoint, body string }{
		{"GET", "/api/admin/naming", ""}, {"PUT", "/api/admin/naming/movie", `{}`}, {"POST", "/api/admin/naming/preview", `{}`}, {"POST", "/api/admin/naming/media-preview", `{}`},
	} {
		call(route.method, route.endpoint, "", route.body, 401)
		call(route.method, route.endpoint, user, route.body, 403)
	}
	initial := call("GET", "/api/admin/naming", token, "", 200)
	if initial["defaults"] == nil || initial["catalog"] == nil {
		t.Fatal("missing editor data")
	}
	template, _ := naming.Default(naming.TV)
	encoded, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"revision":0,"template":` + string(encoded) + `}`
	call("PUT", "/api/admin/naming/tv", token, body, 200)
	call("PUT", "/api/admin/naming/tv", token, body, 200)
	template.Filename[0].Prefix = "changed"
	changed, _ := json.Marshal(template)
	call("PUT", "/api/admin/naming/tv", token, `{"revision":0,"template":`+string(changed)+`}`, 409)
	call("PUT", "/api/admin/naming/movie", token, body, 400)
	call("PUT", "/api/admin/naming/tv", token, `{"template":`+string(encoded)+`}`, 400)
	call("PUT", "/api/admin/naming/tv", token, body+`{}`, 400)
	call("PUT", "/api/admin/naming/tv", token, `{"revision":1,"extra":true,"template":`+string(encoded)+`}`, 400)
	preview := `{"template":` + string(encoded) + `,"items":[{"values":{"title":"作品","resolution":"1080p"},"season":1,"episode":13,"endEpisode":14,"originalName":"file.MKV"},{"values":{"title":"作品"}}]}`
	result := call("POST", "/api/admin/naming/preview", token, preview, 200)
	var items []struct {
		Result naming.Result `json:"result"`
		Error  string        `json:"error"`
	}
	if err := json.Unmarshal(result["items"], &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Result.RelativePath != "作品/Season 01/作品.S01E13-E14.1080p.mkv" || items[1].Error == "" {
		t.Fatalf("preview: %+v", items)
	}
	call("POST", "/api/admin/naming/preview", token, `{"template":`+string(encoded)+`,"items":[]}`, 400)
	call("POST", "/api/admin/naming/preview", token, `{"template":{"kind":"tv","directories":[],"filename":[{"field":"resolution"}]},"items":[{}]}`, 400)
	call("POST", "/api/admin/naming/preview", token, `{"template":`+string(encoded)+`,"items":[`+strings.Repeat(`{},`, 50)+`{}]}`, 400)
	call("POST", "/api/admin/naming/preview", token, `{"template":`+string(encoded)+`,"items":[{}],"extra":true}`, 400)
	call("POST", "/api/admin/naming/preview", token, strings.Repeat(" ", 256<<10)+`{}`, 400)
	mediaPreview := call("POST", "/api/admin/naming/media-preview", token, `{"kind":"tv","tmdbId":42,"files":[{"id":"one","name":"Show.S01E13-E14.1080p.MKV","season":1,"episode":13},{"id":"invalid","name":"Unknown.mkv"}]}`, 200)
	if err := json.Unmarshal(mediaPreview["items"], &items); err != nil || len(items) != 2 || items[0].Result.Name != "作品.S01E13.1080p.mkv" || items[1].Error == "" {
		t.Fatal("media preview", string(mediaPreview["items"]), err)
	}
	call("POST", "/api/admin/naming/media-preview", token, `{"kind":"tv","tmdbId":42,"files":[],"extra":true}`, 400)
	call("POST", "/api/admin/naming/media-preview", token, `{"kind":"tv","tmdbId":42,"files":[`+strings.Repeat(`{},`, 1000)+`{}]}`, 400)
	call("POST", "/api/admin/naming/media-preview", token, strings.Repeat(" ", 1<<20)+`{}`, 400)
	current, err := db.NamingTemplate(context.Background(), naming.TV)
	if err != nil || current.Revision != 1 || current.Template.Filename[0].Prefix != "" {
		t.Fatalf("preview mutated saved settings: %+v, %v", current, err)
	}
}
