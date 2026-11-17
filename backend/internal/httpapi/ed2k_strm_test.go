package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestED2KSTRMImportRejectsOversizedBodyBeforeRegistering(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "ed2k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accounts, _ := proxyTestAccounts(t)
	s, err := service.NewED2KSTRMService(ctx, db, accounts, nil, nil, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterED2KSTRM(r.Group("/api/admin"), s, func() string { return "https://cinema.example" }, nil)
	body := `{"links":["ed2k://|file|` + strings.Repeat("x", 512<<10) + `.mkv|123|0123456789abcdef0123456789abcdef|/"],"targetId":"tv","requestId":"oversized"}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/p115/ed2k-strm/imports", strings.NewReader(body)))
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "本次未接收") {
		t.Fatalf("请求体过大没有明确接收前拒绝：status=%d body=%s", w.Code, w.Body.String())
	}
	stored, err := db.ListED2KSTRMTasks(ctx)
	if err != nil || len(stored) != 0 || len(s.List()) != 0 {
		t.Fatalf("请求体过大仍产生了登记：tasks=%d resources=%d err=%v", len(stored), len(s.List()), err)
	}
}

func TestED2KSTRMManagementSeparatesTargetsTasksAndResources(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "ed2k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accounts, accountID := proxyTestAccounts(t)
	resource := model.ED2KSTRMResource{Hash: strings.Repeat("a", 32), SHA1: strings.Repeat("B", 40), Size: 1234, Name: "Film.mkv", AccountID: accountID, UID: "123", Status: "ready", CacheState: "ready"}
	if err := db.SaveED2KSTRMResource(ctx, resource); err != nil {
		t.Fatal(err)
	}
	task := model.ED2KSTRMTask{ID: "failed-output", Hash: resource.Hash, Size: resource.Size, Name: resource.Name, OutputDir: "first", BaseURL: "https://cinema.example", Status: "failed", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.SaveED2KSTRMTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	s, err := service.NewED2KSTRMService(ctx, db, accounts, nil, nil, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	refreshes := 0
	RegisterED2KSTRM(r.Group("/api/admin"), s, func() string { return "https://cinema.example" }, func() { refreshes++ })
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/admin/p115/ed2k-strm"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	w := call(http.MethodPost, "/targets", `{"name":"目录一","outputDir":"first","assignedBotId":"untrusted"}`)
	var target model.ED2KSTRMTarget
	if err := json.Unmarshal(w.Body.Bytes(), &target); err != nil || w.Code != 200 || target.ID == "" || target.AssignedBotID != "" {
		t.Fatalf("create target status=%d body=%s err=%v", w.Code, w.Body.String(), err)
	}
	w = call(http.MethodPut, "/targets/"+target.ID, `{"name":"目录二","outputDir":"second"}`)
	if w.Code != 200 || refreshes != 2 {
		t.Fatalf("update target status=%d refreshes=%d", w.Code, refreshes)
	}
	w = call(http.MethodPost, "/tasks/failed-output/retry", "")
	if w.Code != 202 || w.Body.String() != `{"accepted":true}` {
		t.Fatalf("retry must return usable JSON: %d %s", w.Code, w.Body.String())
	}
	w = call(http.MethodGet, "", "")
	var state map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || w.Code != 200 {
		t.Fatal(w.Code, err)
	}
	if string(state["targets"]) == "null" || string(state["tasks"]) == "null" || strings.Contains(string(state["resources"]), "strmPath") || strings.Contains(string(state["resources"]), "outputDir") {
		t.Fatal("resource API leaked output ownership", w.Body.String())
	}
	w = call(http.MethodDelete, "/targets/"+target.ID, "")
	if w.Code != 204 || refreshes != 3 || len(s.List()) != 1 || len(s.Tasks()) != 1 {
		t.Fatal("deleting target affected accepted task or content", w.Code, refreshes)
	}
}

func TestED2KSTRMManagementCacheReadinessDoesNotUseNetwork(t *testing.T) {
	for _, mode := range []string{"selected", "root", "default", "default-uncreated", "stale-account", "missing-primary"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "ed2k.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			accounts, accountID := proxyTestAccounts(t)
			settings := model.ED2KSTRMSettings{BaseURL: "https://cinema.example", CacheAccountID: accountID, CacheCID: "42", CachePath: "/媒体缓存"}
			wantReady := true
			switch mode {
			case "root":
				settings.CacheCID, settings.CachePath = "0", "/"
			case "default", "default-uncreated":
				settings.CacheCID, settings.CachePath, settings.CacheAccountID = "", "", ""
				if mode == "default" {
					if err := db.SaveED2KSTRMCache(ctx, model.ED2KSTRMCache{AccountID: accountID, UID: "123", CID: "10", Name: "Cinema115-ED2K-existing"}); err != nil {
						t.Fatal(err)
					}
				}
			case "stale-account":
				settings.CacheAccountID, wantReady = "previous-primary", false
			case "missing-primary":
				a, err := accounts.Primary(accountID)
				if err != nil {
					t.Fatal(err)
				}
				if err := accounts.Delete(a.ID, a.Revision); err != nil {
					t.Fatal(err)
				}
				wantReady = false
			}
			if err := db.SaveED2KSTRMSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			// 不注入网盘客户端，页面状态读取必须仅依赖本地资料。
			s, err := service.NewED2KSTRMService(ctx, db, accounts, nil, nil, nil, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			r := gin.New()
			RegisterED2KSTRM(r.Group("/api/admin"), s, func() string { return "https://cinema.example" }, nil)
			for range 3 {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/p115/ed2k-strm", nil))
				var state struct {
					Settings       model.ED2KSTRMSettings `json:"settings"`
					CacheReady     bool                   `json:"cacheReady"`
					CacheSetupHint string                 `json:"cacheSetupHint"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || w.Code != http.StatusOK {
					t.Fatalf("读取页面状态失败：status=%d err=%v", w.Code, err)
				}
				if state.Settings != settings || state.CacheReady != wantReady || wantReady != (state.CacheSetupHint == "") {
					t.Fatalf("本地目录状态错误：%+v", state)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
					t.Fatal(err)
				}
				if fields["cacheCID"] != nil || fields["cachePath"] != nil || strings.Contains(w.Body.String(), "Cinema115-ED2K-existing") {
					t.Fatal("页面状态不应返回历史目录或重复的有效目录字段", w.Body.String())
				}
				if strings.Contains(w.Body.String(), "UID=") || strings.Contains(w.Body.String(), "SEID=") {
					t.Fatal("目录状态返回凭据")
				}
			}
			resources, err := db.ListED2KSTRMResources(ctx)
			if err != nil || len(resources) != 0 || len(s.Tasks()) != 0 {
				t.Fatalf("读取页面状态产生业务任务：resources=%v err=%v", resources, err)
			}
		})
	}
}
