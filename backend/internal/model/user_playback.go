package model

type UserPlaybackLease struct {
	AuthSession    string `json:"authSession,omitempty"`
	FirstIssuedAt  int64  `json:"firstIssuedAt,omitempty"`
	CandidateUntil int64  `json:"candidateUntil,omitempty"`
	Device         string `json:"device,omitempty"`
	PlaybackSource string `json:"playbackSource,omitempty"`
	LastPlaybackAt int64  `json:"lastPlaybackAt,omitempty"`
	IssuedAt       int64  `json:"issuedAt,omitempty"`
	HandoffAt      int64  `json:"handoffAt,omitempty"`
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
	DevicePolicy   PlaybackDevicePolicy         `json:"devicePolicy"`
	PolicyRevision uint64                       `json:"policyRevision"`
	Leases         map[string]UserPlaybackLease `json:"leases"`
	Stopped        map[string]int64             `json:"stopped"`
	Generation     uint64                       `json:"generation"`
}

// Modes are extensible strategies, not a boolean playback permission.
type PlaybackDevicePolicy struct {
	Mode           string `json:"mode"`
	SilenceSeconds int    `json:"silenceSeconds"`
}
