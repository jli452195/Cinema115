package model

type WebsiteInvite struct {
	ID        string `json:"id"`
	CodeHash  string `json:"-"`
	CreatedBy string `json:"createdBy"`
	Note      string `json:"note"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	RevokedAt int64  `json:"revokedAt"`
	UsedAt    int64  `json:"usedAt"`
	UsedBy    string `json:"usedBy"`
	Username  string `json:"username"`
}
