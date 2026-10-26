package model

// The existing drive-operation row owns this plan and its checkpoints. These
// are execution history, never a registry of the files' current locations.
type AnimeOrganizeFile struct {
	ID           string `json:"id"`
	SourceCID    string `json:"sourceCid"`
	OriginalName string `json:"originalName"`
	TargetPath   string `json:"targetPath"`
	VideoID      string `json:"videoId,omitempty"`
}

type AnimeOrganizeRequest struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Target        AnimeDownloadTarget `json:"target"`
	Files         []AnimeOrganizeFile `json:"files"`
}

type AnimeOrganizeFileProgress struct {
	ID         string `json:"id"`
	Renamed    bool   `json:"renamed"`
	Moved      bool   `json:"moved"`
	ExistingID string `json:"existingId,omitempty"` // keep the observed destination; discard this source copy
	Conflict   bool   `json:"conflict,omitempty"`   // unresolved directory/source-name conflict
}

func (f AnimeOrganizeFileProgress) Handled() bool { return f.Moved && f.Renamed || f.ExistingID != "" }

type AnimeOrganizeDirectory struct {
	Path string `json:"path"` // relative to the fixed organize root
	CID  string `json:"cid"`
}

type AnimeOrganizeStep struct {
	Kind string   `json:"kind"` // mkdir, rename, move, cleanup
	CID  string   `json:"cid"`  // fixed parent/source/destination
	Path string   `json:"path,omitempty"`
	IDs  []string `json:"ids,omitempty"`
}

type AnimeOrganizeProgress struct {
	State          string                      `json:"state"` // working, unknown, needs_review, done
	Code           string                      `json:"code,omitempty"`
	RetryAt        int64                       `json:"retryAt,omitempty"`
	Directories    []AnimeOrganizeDirectory    `json:"directories"`
	Files          []AnimeOrganizeFileProgress `json:"files"`
	Step           *AnimeOrganizeStep          `json:"step,omitempty"`
	Cleanup        string                      `json:"cleanup,omitempty"`        // recycled, uncertain, skipped
	ReceiptIssue   string                      `json:"receiptIssue,omitempty"`   // fixed diagnostic enum, never raw upstream content
	RecoveryChecks int                         `json:"recoveryChecks,omitempty"` // bounded read-only checks of this uncertain step
}

func (p AnimeOrganizeProgress) AllHandled() bool {
	if len(p.Files) == 0 {
		return false
	}
	for _, f := range p.Files {
		if !f.Handled() || f.Conflict {
			return false
		}
	}
	return true
}

type AnimeOrganizeOperation struct {
	ID         string                `json:"id"`
	JobID      string                `json:"jobId"`
	Generation int64                 `json:"generation"`
	Request    AnimeOrganizeRequest  `json:"request"`
	Progress   AnimeOrganizeProgress `json:"progress"`
	UpdatedAt  int64                 `json:"updatedAt"`
}
