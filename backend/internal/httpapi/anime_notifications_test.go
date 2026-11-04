package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/bot"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestAnimeTelegramNotificationSettingsPreviewAndConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub, err := bot.NewHub(t.TempDir(), "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "anime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub.Anime = &service.AnimeAdminService{Store: db}
	router := gin.New()
	RegisterTelegramHub(router.Group("/api/admin"), hub)
	path := "/api/admin/telegram/anime-notifications"
	raw := telegramHubRequest(t, router, http.MethodGet, path, "", http.StatusOK)
	var initial struct {
		Settings        model.AnimeNotificationSettings `json:"settings"`
		DefaultTemplate string                          `json:"defaultTemplate"`
		Preview         struct {
			HTML string `json:"html"`
		} `json:"preview"`
	}
	if err := json.Unmarshal(raw, &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Settings.Revision != 1 || initial.DefaultTemplate == "" || !strings.Contains(initial.Preview.HTML, "整理完成") {
		t.Fatal(string(raw))
	}
	initial.Settings.Template = `<b>{title}</b> · {event}`
	body, _ := json.Marshal(initial.Settings)
	telegramHubRequest(t, router, http.MethodPut, path, string(body), http.StatusOK)
	telegramHubRequest(t, router, http.MethodPut, path, string(body), http.StatusConflict)
	preview := telegramHubRequest(t, router, http.MethodPost, path+"/preview", `{"template":"<b>{title}</b> {event}","event":"downloaded"}`, http.StatusOK)
	if !strings.Contains(string(preview), "下载完成") || strings.Contains(string(preview), "private-token") {
		t.Fatal(string(preview))
	}
	telegramHubRequest(t, router, http.MethodPost, path+"/preview", `{"template":"<script>{title}</script>","event":"failed"}`, http.StatusBadRequest)
	telegramHubRequest(t, router, http.MethodPost, path+"/preview", `{"template":"{title}","event":"unknown"}`, http.StatusBadRequest)
	withPhoto := telegramHubRequest(t, router, http.MethodPost, path+"/preview", `{"template":"{title}","event":"organized","imageMode":"custom","imageUrl":"https://images.example.com/anime.jpg"}`, http.StatusOK)
	if !strings.Contains(string(withPhoto), `"photoUrl":"https://images.example.com/anime.jpg"`) {
		t.Fatal(string(withPhoto))
	}
	telegramHubRequest(t, router, http.MethodPost, path+"/preview", `{"template":"{title}","event":"organized","imageMode":"custom","imageUrl":"http://127.0.0.1/private.jpg"}`, http.StatusBadRequest)
	withoutPhoto := telegramHubRequest(t, router, http.MethodPost, path+"/preview", `{"template":"{title}","event":"organized","imageMode":"none","imageUrl":"invalid hidden draft"}`, http.StatusOK)
	if !strings.Contains(string(withoutPhoto), `"photoUrl":""`) {
		t.Fatal(string(withoutPhoto))
	}
	if pending, err := db.DueTelegramNotifications(context.Background(), "123", 42, time.Now()); err != nil || len(pending) != 0 {
		t.Fatal("预览不应发送消息", pending, err)
	}
}
