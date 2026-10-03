package model

type UserPlaybackLease struct {
	EventFallback  string `json:"eventFallback,omitempty"`
	Key            string `json:"key"`
	UserID         string `json:"userId"`
	ServerID       string `json:"serverId"`
	SessionDigest  string `json:"sessionDigest"`
	EventKey       string `json:"eventKey"`
	ObservationKey string `json:"observationKey"`
	Item           string `json:"item"`
	Generation     uint64 `json:"generation"`
	Issued         bool   `json:"issued"`
	UpdatedAt      int64  `json:"updatedAt"`
	ExpiresAt      int64  `json:"expiresAt"`
}

type UserPlaybackDocument struct {
	Leases     map[string]UserPlaybackLease `json:"leases"`
	Stopped    map[string]int64             `json:"stopped"`
	Generation uint64                       `json:"generation"`
}
