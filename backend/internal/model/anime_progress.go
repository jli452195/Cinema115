package model

// Source episode numbers, scoped to one RSS and its saved rules. These are
// local workflow receipts, never a claim about files' current cloud location.
type AnimeEpisodeRange struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

type AnimeSourceProgress struct {
	State            string              `json:"state"` // unknown, ongoing, missing, complete
	FirstEpisode     int                 `json:"firstEpisode"`
	PublishedThrough int                 `json:"publishedThrough"`
	OrganizedCount   int                 `json:"organizedCount"`
	MissingCount     int                 `json:"missingCount"`
	Missing          []AnimeEpisodeRange `json:"missing,omitempty"`
}
