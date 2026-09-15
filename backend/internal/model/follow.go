package model

// Follow 只含影视简介及当前用户的申请，不包含其他订阅者信息。
type Follow struct {
	Kind         string `json:"kind"`
	TMDBID       int    `json:"tmdbId"`
	Title        string `json:"title"`
	PosterURL    string `json:"posterUrl"`
	Date         string `json:"date"`
	Status       string `json:"status"`
	SubscribedAt string `json:"subscribedAt"`
	LibraryState string `json:"libraryState"`
	Note         string `json:"note"`
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
