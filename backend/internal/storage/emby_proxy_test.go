package storage

import (
	"context"
	"strings"
	"testing"

	"strm115/backend/internal/model"
)

func TestSaveEmbyProxyConfigRollsBackWhenMemberSettingsFail(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if raw, err := s.EmbyProxyConfig(ctx); err != nil || raw != "" {
		t.Fatalf("空配置 = %q，错误 = %v", raw, err)
	}
	const previousProxy = `{"port":8097}`
	const previousURL = "https://play.example"
	previous := model.MemberPlaybackSettings{PublicURL: previousURL}
	if err := s.SaveEmbyProxyConfig(ctx, previousProxy, &previous); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_proxy_member_settings BEFORE UPDATE ON member_playback_settings
BEGIN SELECT RAISE(ABORT,'injected member settings failure'); END`); err != nil {
		t.Fatal(err)
	}
	next := previous
	next.AllowDownload = true
	if err := s.SaveEmbyProxyConfig(ctx, `{"port":8098}`, &next); err == nil || !strings.Contains(err.Error(), "injected member settings failure") {
		t.Fatalf("预期成员播放设置写入失败，得到 %v", err)
	}
	if raw, err := s.EmbyProxyConfig(ctx); err != nil || raw != previousProxy {
		t.Fatalf("回滚后的反代配置 = %q，错误 = %v", raw, err)
	}
	if settings, err := s.MemberPlaybackSettings(ctx); err != nil || settings.AllowDownload || settings.PublicURL != previousURL {
		t.Fatalf("回滚后的成员播放设置 = %+v，错误 = %v", settings, err)
	}
}
