package model

// Deletion metadata contains no credentials, invitation codes or file data.
type MemberDeletionTarget struct {
	Key           string `json:"key"`
	UserID        string `json:"userId"`
	ServerID      string `json:"serverId"`
	EmbyURL       string `json:"embyUrl"`
	EmbyID        string `json:"embyId"`
	Name          string `json:"name"`
	OwnerID       string `json:"ownerId,omitempty"`
	InviteID      string `json:"inviteId,omitempty"`
	ChildID       string `json:"childId,omitempty"`
	ReservedName  string `json:"reservedName,omitempty"`
	Started       bool   `json:"started,omitempty"`
	RemoteDeleted bool   `json:"remoteDeleted"`
	Completed     bool   `json:"completed"`
}

type MemberDeletion struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Fingerprint string                 `json:"fingerprint"`
	Targets     []MemberDeletionTarget `json:"targets"`
	Stage       string                 `json:"stage"`
	Message     string                 `json:"message"`
	Actor       string                 `json:"actor"`
	CreatedAt   int64                  `json:"createdAt"`
	UpdatedAt   int64                  `json:"updatedAt"`
}
