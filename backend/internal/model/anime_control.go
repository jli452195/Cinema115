package model

type AnimeUnchangedSource struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Control receipts describe local changes, never cancellation of cloud I/O.
type AnimeControlReceipt struct {
	ID                     string                 `json:"id"`
	Revision               int64                  `json:"revision"`
	Status                 string                 `json:"status"`
	CancelledJobs          int                    `json:"cancelledJobs"`
	PendingJobs            int                    `json:"pendingJobs"`
	InFlightOperationCount int                    `json:"inFlightOperationCount"`
	ResumedSourceIDs       []string               `json:"resumedSourceIds"`
	UnchangedSources       []AnimeUnchangedSource `json:"unchangedSources"`
}

type AnimeDownloadJobPage struct {
	Items    []AnimeDownloadJob `json:"items"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"pageSize"`
}
