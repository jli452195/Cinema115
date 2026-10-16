package model

// EmbyMissingSource identifies one concrete source and its exact Series TMDB binding.
// EpisodeCount counts indexed formal episode records, not missing episodes or files.
type EmbyMissingSource struct {
	SeriesID     string `json:"seriesId"`
	TMDBID       string `json:"tmdbId"`
	Title        string `json:"title"`
	LibraryID    string `json:"libraryId"`
	EpisodeCount int    `json:"episodeCount"`
}

type EmbyMissingExclusion struct {
	EmbyMissingSource
	ID         string `json:"id"`
	ExcludedAt string `json:"excludedAt"`
}
