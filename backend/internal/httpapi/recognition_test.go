package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/recognition"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestRecognitionHTTPPermissionsPreviewAndSave(t *testing.T) {
	prior := recognition.Current()
	t.Cleanup(func() { _ = recognition.Activate(prior) })
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "recognition.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	auth := portalTestAuth(t, db, "admin", "admin-password", "recognition-secret")
	admin, _, err := auth.LoginAdmin(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := auth.Login(ctx, "reader", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := service.OpenRecognitionSettings(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterRecognition(r.Group("/api/admin", PortalAuthenticated(auth), PortalAdminOnly()), settings)
	call := func(method, endpoint, token string, body any, status int) *httptest.ResponseRecorder {
		t.Helper()
		raw := []byte{}
		if body != nil {
			var err error
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, "/api/admin/recognition"+endpoint, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d %s", method, endpoint, w.Code, w.Body.String())
		}
		return w
	}
	for _, endpoint := range []struct{ method, path string }{{"GET", ""}, {"PUT", ""}, {"POST", "/reload"}, {"POST", "/preview"}} {
		call(endpoint.method, endpoint.path, "", nil, 401)
		call(endpoint.method, endpoint.path, user, nil, 403)
	}
	var initial struct {
		Record service.RecognitionDocument `json:"record"`
	}
	initialBody := call("GET", "", admin, nil, 200).Body.Bytes()
	if strings.Contains(string(initialBody), `"defaults"`) {
		t.Fatal("接口不应提供生产默认词表", string(initialBody))
	}
	if err := json.Unmarshal(initialBody, &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Record.Revision == "" || initial.Record.Groups != "" || initial.Record.Platforms != "" || initial.Record.LoadError != "" {
		t.Fatal(initial)
	}
	draft := map[string]any{"groups": "喵萌奶茶屋\nLoliHouse\nEPSiLON\n桜都字幕组 ~= (?:桜|樱)都字幕[组組]", "platforms": "IQ ~= iQIYI|爱奇艺", "items": []string{"[喵萌奶茶屋&LoliHouse] Show [iQIYI WEB-DL].mkv", "[樱都字幕組] Show.mkv", "EPSiLON -dfaef", "[EPSiLON 1080p] 示例电影.mkv"}}
	var preview struct {
		Items []struct {
			Name string
			recognition.Result
		}
	}
	if err := json.Unmarshal(call("POST", "/preview", admin, draft, 200).Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 4 || len(preview.Items[0].ReleaseGroups) != 2 || preview.Items[0].Platforms[0] != "IQ" || preview.Items[1].ReleaseGroups[0] != "桜都字幕组" {
		t.Fatal(preview)
	}
	for _, index := range []int{2, 3} {
		if len(preview.Items[index].ReleaseGroups) != 1 || preview.Items[index].ReleaseGroups[0] != "EPSiLON" {
			t.Fatal("预览应支持裸前缀及混合规格标签", preview.Items[index])
		}
	}
	if recognition.Current().Revision != initial.Record.Revision {
		t.Fatal("草稿预览激活了未保存词表")
	}
	bad := call("POST", "/preview", admin, map[string]any{"groups": "# 注释\n[", "platforms": "", "items": []string{"Title"}}, 400)
	if !strings.Contains(bad.Body.String(), `"line":2`) || !strings.Contains(bad.Body.String(), `"kind":"groups"`) {
		t.Fatal(bad.Body.String())
	}
	call("POST", "/preview", admin, map[string]any{"groups": "", "platforms": "", "items": []string{}}, 400)
	call("PUT", "", admin, map[string]any{"groups": ""}, 400)
	var saved service.RecognitionDocument
	body := map[string]string{"groups": draft["groups"].(string), "platforms": draft["platforms"].(string)}
	if err := json.Unmarshal(call("PUT", "", admin, body, 200).Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Revision == initial.Record.Revision || saved.ActiveRevision != saved.Revision {
		t.Fatal(saved)
	}
	call("PUT", "", admin, body, 200)
	body["groups"] = "Other"
	call("PUT", "", admin, body, 200)
	call("POST", "/reload", admin, nil, 200)
}
