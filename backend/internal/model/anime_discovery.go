package model

// AnimeRSSWindow describes the last successful bounded feed read. Failure
// cannot erase this coverage evidence or turn a feed window into all history.
type AnimeRSSWindow struct {
	SchemaVersion int   `json:"schemaVersion"`
	ObservedAt    int64 `json:"observedAt"`
	RawCount      int   `json:"rawCount"`
	ReleaseCount  int   `json:"releaseCount"`
}
