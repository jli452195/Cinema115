package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type followTMDBFixture struct {
	calls int
	fail  bool
}

func (f *followTMDBFixture) Details(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("上游暂时不可用")
	}
	if id == 404 {
		return nil, tmdb.ErrNotFound
	}
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, Title: "来自 TMDB 的影视 " + strconv.Itoa(id), PosterURL: "https://image.tmdb.org/t/p/w500/test.jpg", Date: "2026-01-01"}}, nil
}

type followLibraryFixture struct {
	available bool
	fail      bool
}

func (f *followLibraryFixture) Matches(ctx context.Context, queries []model.EmbyMatch, detail bool) ([]model.EmbyMatch, error) {
	if f.fail {
		return nil, errors.New("数据库读取失败")
	}
	for i := range queries {
		queries[i].State = "absent"
		if f.available {
			queries[i].State = "present"
		}
	}
	return queries, nil
}
func TestFollowRoutesIdentityPersistenceAndAvailability(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "follows.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	auth := portalTestAuth(t, db, "admin", "admin-password", "secret")
	for _, name := range []string{"alice", "bob"} {
		portalTestUser(t, auth, name, "test-password")
	}
	alice, _, err := auth.Login(ctx, "alice", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	bob, _, err := auth.Login(ctx, "bob", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	source := &followTMDBFixture{}
	library := &followLibraryFixture{}
	r := gin.New()
	member := r.Group("/api/me", PortalAuthenticated(auth))
	s := &service.FollowService{Store: db, TMDB: source, Library: library}
	RegisterFollows(member, s)
	call := func(method, path, token, body string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, "/api/me/follows"+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s status %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		var data map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	call("GET", "", "", "", 401)
	for _, path := range []string{"/person/100", "/tv/0", "/movie/001", "/tv/99999999999999"} {
		call("POST", path, alice, "", 400)
	}
	call("GET", "?kind=invalid", alice, "", 400)
	call("GET", "?offset=-1", alice, "", 400)
	state := call("GET", "/tv/100", alice, "", 200)
	if state["subscribed"] != false {
		t.Fatal(state)
	}
	created := call("POST", "/tv/100?userId=bob", alice, `{"userId":"bob","title":"伪造片名"}`, 200)
	if created["subscribed"] != true || created["request"].(map[string]any)["title"] != "来自 TMDB 的影视 100" {
		t.Fatal("信任了前端影视信息或用户编号")
	}
	call("POST", "/tv/100", alice, "", 200)
	if source.calls != 1 {
		t.Fatal("重复订阅不应重新查询 TMDB")
	}
	if list := call("GET", "", bob, "", 200)["items"].([]any); len(list) != 0 {
		t.Fatal("其他用户读到了 Alice 的申请")
	}
	call("POST", "/tv/100", bob, "", 200)
	call("POST", "/tv/100/cancel?userId=alice", bob, "", 200)
	if state := call("GET", "/tv/100", alice, "", 200); state["subscribed"] != true {
		t.Fatal("取消越权")
	}
	call("POST", "/tv/100/cancel", bob, "", 200)
	library.available = true
	call("POST", "/movie/100", alice, "", 409)
	call("POST", "/tv/101", alice, "", 200)
	library.available = false
	call("POST", "/movie/100", alice, "", 200)
	if list := call("GET", "?kind=movie", alice, "", 200)["items"].([]any); len(list) != 1 {
		t.Fatal("电影剧集编号混淆")
	}
	library.fail = true
	call("POST", "/movie/102", alice, "", 502)
	if state := call("GET", "/movie/100", alice, "", 200); state["subscribed"] != true || state["libraryState"] != "unknown" {
		t.Fatal("入库查询异常隐藏了订阅")
	}
	library.fail = false
	source.fail = true
	call("POST", "/tv/102", alice, "", 502)
	source.fail = false
	call("POST", "/tv/404", alice, "", 404)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	auth = portalTestAuth(t, db, "admin", "admin-password", "secret")
	u, err := db.PortalUser(ctx, portalTestUser(t, auth, "alice", "test-password").ID)
	if err != nil {
		t.Fatal(err)
	}
	restored := &service.FollowService{Store: db, TMDB: source, Library: library}
	list, _, err := restored.List(ctx, u.ID, "", "ongoing", 0)
	if err != nil || len(list) != 3 {
		t.Fatal("重启丢失订阅或失败申请留下记录", len(list), err)
	}
}
