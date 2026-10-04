package model

// ChildCreation is a durable creation intent. Passwords and credentials never enter it.
type ChildCreation struct {
	ID               string         `json:"id"`
	OwnerID          string         `json:"ownerId"`
	Name             string         `json:"name"`
	ReservedName     string         `json:"reservedName"`
	ServerID         string         `json:"serverId"`
	EmbyURL          string         `json:"embyUrl"`
	EmbyID           string         `json:"embyId"`
	Started          bool           `json:"started"`
	Completed        bool           `json:"completed"`
	ActivationPolicy map[string]any `json:"activationPolicy,omitempty"`
}
