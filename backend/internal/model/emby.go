package model

// EmbyItem 只保存匹配所需字段，不保存媒体路径或播放凭证。
type EmbyItem struct {
	ID          string `json:"id"`
	LibraryID   string `json:"libraryId"`
	Type        string `json:"type"`
	TMDB        string `json:"tmdbId"`
	SeriesID    string `json:"seriesId"`
	ParentID    string `json:"parentId"`
	Name        string `json:"name"`
	DateCreated string `json:"dateCreated"`
	Season      int    `json:"season"`
	Episode     int    `json:"episode"`
	EndEpisode  int    `json:"endEpisode"`
}

type EmbyState struct {
	Scope             string `json:"-"`
	Active            string `json:"-"`
	Status            string `json:"status"`
	Count             int    `json:"count"`
	Scanned           int    `json:"scanned"`
	SyncedAt          string `json:"syncedAt"`
	LastError         string `json:"lastError"`
	WebhookAt         string `json:"webhookAt"`
	WebhookEvent      string `json:"webhookEvent"`
	WebhookError      string `json:"webhookError"`
	StagingGeneration string `json:"-"`
	StagingComplete   bool   `json:"-"`
	StagingCutoff     int64  `json:"-"`
	UpdatesGeneration string `json:"-"`
	UpdatesReady      bool   `json:"updatesReady"`
	UpdatesError      string `json:"updatesError"`
}

type EmbyEvent struct {
	ID       int64
	ItemID   string
	Kind     string
	Attempts int
}

type EmbyMatch struct {
	Kind         string     `json:"kind"`
	TMDB         string     `json:"tmdbId"`
	State        string     `json:"state"`
	Items        []EmbyItem `json:"items"`
	EpisodeCount int        `json:"episodeCount"`
}
