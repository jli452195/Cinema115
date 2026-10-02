package bot

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/storage"
)

func TestPoolHealthNotificationQueueRequiresEnabledBotAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	h, err := NewHub(t.TempDir(), "test-secret", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "notifications.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h.Notifications = db
	id, err := h.Add("pool notifications")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.NotifyPoolHealth(ctx, id, "pool-health:1", "safe status"); err == nil {
		t.Fatal("disabled bot accepted")
	}
	profile, _ := h.Profiles().Get(id)
	profile.Token, profile.AllowedChatIDs, profile.NotificationChatID, profile.Enabled = "123:test-token", "42", "42", true
	if err := h.Profiles().Update(profile, profile.Revision); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := h.NotifyPoolHealth(ctx, id, "pool-health:1", "safe status"); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := db.DueTelegramNotifications(ctx, "123", 42, time.Now().Add(time.Second))
	if err != nil || len(pending) != 1 || pending[0].Text != "safe status" || pending[0].PreviewURL != "" {
		t.Fatal(pending, err)
	}
	if kind, _ := notificationLabels("pool-health:1"); kind != "播放池通知" {
		t.Fatal(kind)
	}
}
