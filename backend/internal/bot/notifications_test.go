package bot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

func TestNotificationPersistsRetriesAndDoesNotBroadcast(t *testing.T) {
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
			Reply  any    `json:"reply_parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.ChatID != -10077 || body.Reply != nil || !strings.Contains(body.Text, "成功 2 个") || !strings.Contains(body.Text, "目录校验成功") || !strings.Contains(body.Text, "回收站") {
			t.Error("通知目标或消息内容错误")
		}
		if sends == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":123}}`))
	})
	ctx := context.Background()
	if err := m.Save(ctx, BotConfig{Token: "123:valid-token", AllowedChatIDs: "42,43", NotificationChatID: " -10077 ", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, filepath.Join(dir, "notification.db"))
	if err != nil {
		t.Fatal(err)
	}
	m.Notifications = db
	input := service.GenerateInput{URL: "https://115.com/s/source", Password: "abcd", Mode: "reshare"}
	result := &service.GenerationResult{Written: 2, MetadataWritten: 1, ReshareURL: "https://115.com/s/own?password=fwgl", Cleaned: true}
	if err := m.NotifyShareSuccess(ctx, "task", input, result, nil); err != nil {
		t.Fatal(err)
	}
	if sends != 0 {
		t.Fatal("通知入队执行了网络请求")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(ctx, filepath.Join(dir, "notification.db"))
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
		t.Fatal("重启后未读取待发送通知")
	}
	if err := m.deliverNotification(ctx, now); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatal("没有遵守失败重试间隔")
	}
	// 更换目标不能把旧任务转发给新聊天，也不能发送到原白名单的所有聊天。
	m.config.NotificationChatID = "99"
	if err := m.deliverNotification(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatal("旧通知被改发到新目标")
	}
	m.config.NotificationChatID = "-10077"
	m.config.Token = "456:another-token"
	if err := m.deliverNotification(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatal("旧通知改用了其他 Bot")
	}
	m.config.Token = "123:valid-token"
	if err := m.deliverNotification(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sends != 2 {
		t.Fatal("失败后未恢复发送")
	}
	if err := m.NotifyShareSuccess(ctx, "task", input, result, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.deliverNotification(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sends != 2 {
		t.Fatal("同一任务重复通知")
	}
}

func TestNotificationChatConfigValidationAndPersistence(t *testing.T) {
	m, dir := configTestManager(t, func(w http.ResponseWriter, r *http.Request) { identityHandler(t, w, r) })
	ctx := context.Background()
	if err := m.Save(ctx, BotConfig{Token: "123:valid-token", NotificationChatID: "-10077"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"0", "42,43", "@someone", "999999999999999999999"} {
		if err := m.Save(ctx, BotConfig{NotificationChatID: bad}); !errors.Is(err, ErrConfig) {
			t.Fatal("未拒绝无效目标", bad, err)
		}
		if m.View().NotificationChatID != "-10077" {
			t.Fatal("无效保存改变了原目标")
		}
	}
	reloaded, err := NewManager(dir, "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.View().NotificationChatID != "-10077" {
		t.Fatal("通知目标没有持久化")
	}
	if err := m.Save(ctx, BotConfig{}); err != nil {
		t.Fatal(err)
	}
	if m.View().NotificationChatID != "" {
		t.Fatal("无法关闭独立通知")
	}
	if err := m.NotifyShareSuccess(ctx, "disabled", service.GenerateInput{}, nil, nil); err != nil {
		t.Fatal("未配置时不应入队", err)
	}
}

func TestBatchNotificationQueuesOneMergedSummary(t *testing.T) {
	m, dir := configTestManager(t, func(w http.ResponseWriter, r *http.Request) { identityHandler(t, w, r) })
	ctx := context.Background()
	if err := m.Save(ctx, BotConfig{Token: "123:valid-token", NotificationChatID: "42"}); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, filepath.Join(dir, "batch-notification.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m.Notifications = db
	inputs := []service.GenerateInput{{URL: "https://115.com/s/source-a"}, {URL: "https://115.com/s/source-b"}}
	outcomes := []service.BatchOutcome{
		{Result: &service.GenerationResult{Written: 1, ReshareURL: "https://115.com/s/merged", Cleaned: true}},
		{Result: &service.GenerationResult{Written: 2, ReshareURL: "https://115.com/s/merged", Cleaned: true}},
	}
	if err := m.NotifyShareBatchSuccess(ctx, "batch:task", inputs, outcomes); err != nil {
		t.Fatal(err)
	}
	pending, err := db.DueTelegramNotifications(ctx, "123", 42, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("合并批次通知数量错误：%d", len(pending))
	}
	text := pending[0].Text
	for _, want := range []string{"成功 2 个，失败 0 个", inputs[0].URL, inputs[1].URL, "生成 STRM：成功 3 个"} {
		if !strings.Contains(text, want) {
			t.Fatalf("合并通知缺少 %q：%s", want, text)
		}
	}
	if strings.Count(text, "https://115.com/s/merged") != 1 {
		t.Fatal("共同新分享链接重复显示", text)
	}
}
