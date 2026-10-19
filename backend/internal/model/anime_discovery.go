package model

// AnimeExecutionBaseline is written only by confirmed activation, never by a
// form save or an RSS poll. T0 and the initial identity set live in baseline
// columns/tables; this snapshot pins the target and interpretation versions.
type AnimeExecutionBaseline struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Target        AnimeDownloadTarget `json:"target"`
	RuleVersion   int64               `json:"ruleVersion"`
	ParserVersion int64               `json:"parserVersion"`
	TimeVersion   int64               `json:"timeVersion"`
}

// AnimeRSSWindow describes the last successful bounded feed read. Failure
// cannot erase this coverage evidence or turn a feed window into all history.
type AnimeRSSWindow struct {
	SchemaVersion int   `json:"schemaVersion"`
	ObservedAt    int64 `json:"observedAt"`
	RawCount      int   `json:"rawCount"`
	ReleaseCount  int   `json:"releaseCount"`
}
