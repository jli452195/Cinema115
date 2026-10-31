package model

import "time"

// TelegramRelayRule belongs to one authenticated Telegram user. Peer access
// hashes and account credentials remain in the encrypted Telegram account store.
type TelegramRelayRule struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	OwnerID        int64     `json:"ownerId"`
	SourceID       string    `json:"sourceId"`
	SourceTitle    string    `json:"sourceTitle"`
	TargetID       string    `json:"targetId"`
	TargetTitle    string    `json:"targetTitle"`
	Enabled        bool      `json:"enabled"`
	LinkTypes      []string  `json:"linkTypes"`
	IncludePattern string    `json:"includePattern,omitempty"`
	ExcludePattern string    `json:"excludePattern,omitempty"`
	StartedAt      time.Time `json:"startedAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	// SourcePts seeds a new channel checkpoint in the same save transaction.
	// It is transport input, never a user option or persisted rule field.
	SourcePts int `json:"-"`
}

// TelegramRelayJob stores one message card. Its stable random ID is replayed
// after ambiguous network failures; it must not be regenerated on retries.
type TelegramRelayJob struct {
	ID          int64     `json:"id"`
	RuleID      string    `json:"ruleId"`
	MessageID   int       `json:"messageId"`
	Fingerprint string    `json:"-"`
	Payload     string    `json:"-"`
	RandomID    int64     `json:"-"`
	Attempts    int       `json:"attempts"`
	Due         time.Time `json:"due"`
	CreatedAt   time.Time `json:"createdAt"`
}
