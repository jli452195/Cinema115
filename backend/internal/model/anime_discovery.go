package model

// AnimeExecutionBaseline freezes the source execution target and parser versions.
// It does not distinguish historical publications from later RSS items.
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
