package model

import "strconv"

// AnimeNotificationSettings 保存动漫订阅独立的通知设置；空模板使用内建排版。
type AnimeNotificationSettings struct {
	Revision         int64  `json:"revision"`
	BotID            string `json:"botId"`
	DownloadComplete bool   `json:"downloadComplete"`
	Organized        bool   `json:"organized"`
	Failed           bool   `json:"failed"`
	Template         string `json:"template"`
	ImageMode        string `json:"imageMode"`
	ImageURL         string `json:"imageUrl"`
}

// AnimeNotificationEvent 与任务状态在同一事务生成，不保存凭据或取链结果。
type AnimeNotificationEvent struct {
	ID         int64
	JobID      string
	Generation int64
	Kind       string
	Phase      string
	Title      string
	BangumiID  string
	Snapshot   AnimeDownloadSnapshot
	BlockCode  string
	CreatedAt  int64
	BotID      string
	Template   string
	ImageMode  string
	ImageURL   string
	CoverURL   string
}

// TelegramTaskID 使用任务业务身份去重，不依赖可能随表重建变化的事件编号。
func (e AnimeNotificationEvent) TelegramTaskID() string {
	return "anime:job:" + e.JobID + ":" + strconv.FormatInt(e.Generation, 10) + ":" + e.Kind + ":" + e.Phase + ":" + e.BlockCode
}
