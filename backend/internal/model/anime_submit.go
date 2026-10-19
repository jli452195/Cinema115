package model

// AnimeSubmitRequest contains only immutable, credential-free identities.
type AnimeSubmitRequest struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Target        AnimeDownloadTarget `json:"target"`
	InfoHash      string              `json:"infoHash"`
	DirectoryCID  string              `json:"directoryCid"`
}

type AnimeSubmitObservation struct {
	SchemaVersion int    `json:"schemaVersion"`
	Kind          string `json:"kind"` // accepted receipt; or matched, missing, conflict, unreadable, rejected lookup
	TargetCID     string `json:"targetCid,omitempty"`
	ResultID      string `json:"resultId,omitempty"`
}

type AnimeSubmitOperation struct {
	ID           string                  `json:"id"`
	JobID        string                  `json:"jobId"`
	OperationKey string                  `json:"operationKey"`
	State        string                  `json:"state"`
	Generation   int64                   `json:"generation"`
	Request      AnimeSubmitRequest      `json:"request"`
	Result       *AnimeSubmitObservation `json:"result,omitempty"`
	ErrorCode    *string                 `json:"errorCode,omitempty"`
	AuthorizedAt *int64                  `json:"authorizedAt,omitempty"`
	UpdatedAt    int64                   `json:"updatedAt"`
}

type AnimeEnsureDownloadResult struct {
	Job       AnimeDownloadJob     `json:"job"`
	Operation AnimeSubmitOperation `json:"operation"`
}
