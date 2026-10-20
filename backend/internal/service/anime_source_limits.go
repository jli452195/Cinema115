package service

import (
	"strm115/backend/internal/client/anibt"
	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/mikan"
)

// Initial discovery reads the site's largest supported history window in one
// request. Provider limits also apply when only a few releases are available.
func animeSourceHistoryLimit(provider string) int {
	switch provider {
	case "mikan":
		return mikan.AnimeMaxRows
	case "anibt":
		return anibt.HistoryLimit
	case "garden":
		return garden.HistoryLimit
	default:
		return 0
	}
}
