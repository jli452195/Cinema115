package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestFollowNotificationQueueRestartRetryAndTarget(t *testing.T) {
	sends := 0
	m, dir := configTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			identityHandler(t, w, r)
			return
		}
		sends++
		var body struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.ChatID != -10077 || !strings.Contains(body.Text, "新的追更申请") || !strings.Contains(body.Text, "/follows/manage?kind=tv&q=100") || strings.Contains(body.Text, "valid-token") {
			t.Error("追更通知内容或目标错误")
		}
		if sends == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":123}}`))
	})
	ctx := context.Background()
	f := model.Follow{Kind: "tv", TMDBID: 100, Title: "测试剧集"}
	if n, err := m.PrepareFollowNotification(f, ""); err != nil || n != nil || m.FollowNotificationsConfigured() {
		t.Fatal("未配置时应仅保存申请", err)
	}
	if err := m.Save(ctx, BotConfig{Token: "123:valid-token", AllowedChatIDs: "42,43", NotificationChatID: "-10077", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	n, err := m.PrepareFollowNotification(f, "http://192.168.0.24:5173")
	if err != nil || n == nil {
		t.Fatal(err)
	}
	if sends != 0 {
		t.Fatal("格式化通知执行了网络发送")
	}
	db, err := storage.Open(ctx, filepath.Join(dir, "follow.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SubscribeWithNotification(ctx, "alice", f, n); err != nil {
		t.Fatal(err)
	}
	if err := db.SubscribeWithNotification(ctx, "bob", f, n); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(ctx, filepath.Join(dir, "follow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m.Notifications = db
	now := time.Now()
	if err := m.deliverNotification(ctx, now); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatal("重启后没有发送通知")
	}
	if err := m.deliverNotification(ctx, now); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatal("忽略了重试等待")
	}
	m.config.NotificationChatID = "99"
	if err := m.deliverNotification(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatal("旧通知转发到新目标")
	}
	m.config.NotificationChatID = "-10077"
	if err := m.deliverNotification(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sends != 2 {
		t.Fatal("通知没有恢复重试")
	}
	if err := db.SubscribeWithNotification(ctx, "charlie", f, n); err != nil {
		t.Fatal(err)
	}
	if err := m.deliverNotification(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sends != 2 {
		t.Fatal("新增订阅者触发重复通知")
	}
	if kind, message := notificationLabels("follow:tv:100:1"); kind != "追更通知" || message != "追更申请通知已发送" {
		t.Fatal("追更发送日志分类错误")
	}
}
