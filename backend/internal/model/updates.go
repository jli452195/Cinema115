package model

type MediaUpdateEpisode struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

// MediaUpdateGroup is the database-level aggregation for one movie or one TV season on one day.
type MediaUpdateGroup struct {
	Kind          string
	TMDBID        int
	FallbackTitle string
	Season        int
	Date          string
	UpdatedAt     int64
	Episodes      []MediaUpdateEpisode
}
