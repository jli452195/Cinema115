package model

// The existing drive-operation row owns this plan and its checkpoints. These
// are execution history, never a registry of the files' current locations.
type AnimeOrganizeFile struct {
	ID           string `json:"id"`
	SourceCID    string `json:"sourceCid"`
	OriginalName string `json:"originalName"`
	TargetPath   string `json:"targetPath"`
}

type AnimeOrganizeRequest struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Target        AnimeDownloadTarget `json:"target"`
	Files         []AnimeOrganizeFile `json:"files"`
}

type AnimeOrganizeFileProgress struct {
	ID      string `json:"id"`
	Renamed bool   `json:"renamed"`
	Moved   bool   `json:"moved"`
}

type AnimeOrganizeDirectory struct {
	Path string `json:"path"` // relative to the fixed organize root
	CID  string `json:"cid"`
}

type AnimeOrganizeStep struct {
	Kind string   `json:"kind"` // mkdir, rename, move
	CID  string   `json:"cid"`  // fixed parent/source/destination
	Path string   `json:"path,omitempty"`
	IDs  []string `json:"ids,omitempty"`
}

type AnimeOrganizeProgress struct {
	State       string                      `json:"state"` // working, unknown, needs_review, done
	Code        string                      `json:"code,omitempty"`
	RetryAt     int64                       `json:"retryAt,omitempty"`
	Directories []AnimeOrganizeDirectory    `json:"directories"`
	Files       []AnimeOrganizeFileProgress `json:"files"`
	Step        *AnimeOrganizeStep          `json:"step,omitempty"`
}

type AnimeOrganizeOperation struct {
	ID         string                `json:"id"`
	JobID      string                `json:"jobId"`
	Generation int64                 `json:"generation"`
	Request    AnimeOrganizeRequest  `json:"request"`
	Progress   AnimeOrganizeProgress `json:"progress"`
	UpdatedAt  int64                 `json:"updatedAt"`
}
