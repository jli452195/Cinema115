package model

// PortalUser is an Emby identity projection, never a separate password account.
type PortalUser struct {
	ID           string `json:"id"`
	ServerID     string `json:"-"`
	EmbyID       string `json:"embyId,omitempty"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	Disabled     bool   `json:"disabled"`
	CreatedAt    string `json:"createdAt"`
	TokenVersion int64  `json:"-"`
}
