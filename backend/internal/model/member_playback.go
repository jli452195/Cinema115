package model

// Contains lifecycle metadata only; no passwords, tokens, cookies or links.
type MemberPlayback struct {
	Adopted             bool   `json:"adopted"`
	AllowPersonalSwitch bool   `json:"allowPersonalSwitch"`
	Provisioned         bool   `json:"provisioned"`
	UserID              string `json:"userId"`
	EmbyURL             string `json:"embyUrl"`
	ServerID            string `json:"serverId"`
	EmbyID              string `json:"embyId"`
	EmbyName            string `json:"embyName"`
	RegistrationName    string `json:"registrationName"`
	PendingName         string `json:"pendingName"`
	AccountRevision     uint64 `json:"accountRevision"`
	AccountMessage      string `json:"accountMessage"`
	AccountID           string `json:"accountId"`
	Source              string `json:"source"` // personal, primary, assigned
	AssignedAccountID   string `json:"assignedAccountId"`
	LoginEnabled        bool   `json:"loginEnabled"`
	Stage               string `json:"stage"`
	Message             string `json:"message"`
	Suspended           bool   `json:"suspended"`
	AdminBlocked        bool   `json:"adminBlocked"`
	PolicyEnabled       bool   `json:"policyEnabled"`
	AppliedRevision     uint64 `json:"appliedRevision"`
	SyncError           string `json:"syncError"`
	DirectoryID         string `json:"directoryId"`
	DirectoryStarted    bool   `json:"directoryStarted"`
}

type MemberPlaybackSettings struct {
	Enabled       bool     `json:"enabled"`
	LibraryIDs    []string `json:"libraryIds"`
	AllowDownload bool     `json:"allowDownload"`
	Revision      uint64   `json:"revision"`
	PublicURL     string   `json:"publicUrl"`
	EmbyURL       string   `json:"embyUrl"`
	ServerID      string   `json:"serverId"`
}
