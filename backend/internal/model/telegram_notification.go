package model

// TelegramNotification 保存已经格式化的通知，不能包含 Bot Token 和 115 Cookie。
type TelegramNotification struct {
	ID              int64
	TaskID          string
	BotID           string
	ChatID          int64
	Text            string
	ParseMode       string
	PhotoURL        string
	PhotoSent       bool
	PreviewURL      string
	Attempts        int
	AnimeJobID      string
	AnimeGeneration int64
	AnimeKind       string
	AnimePhase      string
	AnimeBlockCode  string
	AnimeBotID      string
}
