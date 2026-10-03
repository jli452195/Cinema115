package model

// Pool records never contain credentials, tokens or CDN URLs.
type PlaybackPoolSettings struct {
	NotificationBotID string `json:"notificationBotId"`
	MaxSessions       int    `json:"maxSessions"`
	MaxAttempts       int    `json:"maxAttempts"`
	BudgetSeconds     int    `json:"budgetSeconds"`
	LeaseMinutes      int    `json:"leaseMinutes"`
	CooldownSeconds   int    `json:"cooldownSeconds"`
	UIDIntervalMS     int    `json:"uidIntervalMs"`
	GlobalIntervalMS  int    `json:"globalIntervalMs"`
}

type PlaybackPoolLease struct {
	EventFallback  string `json:"eventFallback,omitempty"`
	UserGeneration uint64 `json:"userGeneration,omitempty"`
	ObservationKey string `json:"observationKey,omitempty"`
	Key            string `json:"key"`
	SessionKey     string `json:"sessionKey,omitempty"`
	Stopped        bool   `json:"stopped,omitempty"`
	UserID         string `json:"userId"`
	AccountID      string `json:"accountId"`
	UID            string `json:"uid"`
	Revision       uint64 `json:"revision"`
	DirectoryID    string `json:"directoryId"`
	Generation     uint64 `json:"generation"`
	Issued         bool   `json:"issued"`
	ExpiresAt      int64  `json:"expiresAt"`
}

type PlaybackPoolHealth struct {
	Sequence  uint64 `json:"sequence"`
	Failures  int    `json:"failures"`
	NextProbe int64  `json:"nextProbe"`
	UID       string `json:"uid"`
	Revision  uint64 `json:"revision"`
	State     string `json:"state"`
	Until     int64  `json:"until"`
	CheckedAt int64  `json:"checkedAt"`
}

type PlaybackPoolDocument struct {
	Events        []PlaybackPoolEvent           `json:"events,omitempty"`
	EventSequence uint64                        `json:"eventSequence"`
	GlobalUntil   int64                         `json:"globalUntil,omitempty"`
	Settings      PlaybackPoolSettings          `json:"settings"`
	Leases        map[string]PlaybackPoolLease  `json:"leases"`
	Health        map[string]PlaybackPoolHealth `json:"health"`
	Modes         map[string]string             `json:"modes"`
	Generation    uint64                        `json:"generation"`
}

// Bounded health history contains only configuration handles and safe enums.
type PlaybackPoolEvent struct {
	ID        uint64 `json:"id"`
	AccountID string `json:"accountId"`
	State     string `json:"state"`
	At        int64  `json:"at"`
	BotID     string `json:"botId,omitempty"`
	Delivered bool   `json:"delivered,omitempty"`
}
