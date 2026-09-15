package model

// WebsiteUser 不向浏览器返回密码哈希或凭证版本。
type WebsiteUser struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	Disabled     bool   `json:"disabled"`
	CreatedAt    string `json:"createdAt"`
	PasswordHash string `json:"-"`
	TokenVersion int64  `json:"-"`
}
