package model

const FollowPageSize = 24

// Follow 只含影视简介及当前用户的申请，不包含其他订阅者信息。
type Follow struct {
	Kind         string          `json:"kind"`
	TMDBID       int             `json:"tmdbId"`
	Title        string          `json:"title"`
	PosterURL    string          `json:"posterUrl"`
	Date         string          `json:"date"`
	Status       string          `json:"status"`
	SubscribedAt string          `json:"subscribedAt"`
	LibraryState string          `json:"libraryState"`
	Note         string          `json:"note"`
	Progress     *FollowProgress `json:"progress,omitempty"`
}

// 只保存集数汇总，不重复保存 Emby 集索引或缺集区间。
type FollowProgress struct {
	State     string `json:"state"`
	Aired     int    `json:"aired"`
	Owned     int    `json:"owned"`
	CheckedAt int64  `json:"checkedAt"`
	Error     string `json:"error,omitempty"`
}

type FollowCheck struct {
	Kind    string
	TMDBID  int
	Version int64
}

type WebsiteNotification struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	TMDBID    int    `json:"tmdbId"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	CreatedAt int64  `json:"createdAt"`
	ReadAt    int64  `json:"readAt"`
}

type AdminFollow struct {
	Follow
	SubscriberCount int    `json:"subscriberCount"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	Revision        int64  `json:"revision"`
}

type FollowState struct {
	Subscribed   bool    `json:"subscribed"`
	Request      *Follow `json:"request"`
	LibraryState string  `json:"libraryState"`
}
