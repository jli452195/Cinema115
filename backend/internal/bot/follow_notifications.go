package bot

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"strm115/backend/internal/model"
)

func (m *Manager) FollowNotificationsConfigured() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Token != "" && m.config.NotificationChatID != ""
}

// 只创建通知内容与目标快照，由订阅事务入队后交给已有通知工作器发送。
func (m *Manager) PrepareFollowNotification(f model.Follow, baseURL string) (*model.TelegramNotification, error) {
	m.mu.RLock()
	cfg := m.config
	m.mu.RUnlock()
	if cfg.Token == "" || cfg.NotificationChatID == "" {
		return nil, nil
	}
	chatID, err := strconv.ParseInt(cfg.NotificationChatID, 10, 64)
	if err != nil || chatID == 0 {
		return nil, fmt.Errorf("追更通知聊天 ID 无效")
	}
	label := "剧集"
	if f.Kind == "movie" {
		label = "电影"
	}
	text := "📌 新的追更申请\n" + shortReply(f.Title, 180) + "\n类型：" + label + "\nTMDB：" + strconv.Itoa(f.TMDBID) + "\n请查看申请并搜索资源；入库结果以 Emby 为准。"
	if baseURL != "" {
		query := url.Values{"kind": {f.Kind}, "q": {strconv.Itoa(f.TMDBID)}}
		text += "\n\n查看追更：" + strings.TrimRight(baseURL, "/") + "/follows/manage?" + query.Encode()
	} else {
		text += "\n\n请登录 Cinema115，在右上角菜单打开「追更管理」。"
	}
	return &model.TelegramNotification{BotID: strings.SplitN(cfg.Token, ":", 2)[0], ChatID: chatID, Text: text}, nil
}

func notificationLabels(taskID string) (string, string) {
	if strings.HasPrefix(taskID, "pool-health:") {
		return "播放池通知", "播放池健康通知已发送"
	}
	if strings.HasPrefix(taskID, "follow:") {
		return "追更通知", "追更申请通知已发送"
	}
	return "生成通知", "STRM 生成成功通知已发送"
}
