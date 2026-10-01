package model

// P115Summary contains display metadata only, never credentials or playback state.
type P115Summary struct {
	VIP            string `json:"vip"` // vip, normal, or empty (unknown)
	RemainingBytes int64  `json:"remainingBytes"`
	SpaceKnown     bool   `json:"spaceKnown"`
	CheckedAt      string `json:"checkedAt"`
}
