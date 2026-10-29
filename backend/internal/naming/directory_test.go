package naming_test

import (
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/naming"
)

func TestValidateDirectory(t *testing.T) {
	for _, directory := range []string{"电影", "剧集/日本动漫", "电影/导演剪辑版 (2026)", "movie.v2/Season 01", strings.Repeat("影", 80)} {
		if err := naming.ValidateDirectory(directory); err != nil {
			t.Fatalf("valid directory %q: %v", directory, err)
		}
	}
	for _, directory := range []string{"", "/media", "C:/media", `\\server\media`, `movie\anime`, ".", "..", ".actors", "movie/../anime", "movie//anime", "movie/", " movie", "movie.", "movie/CON", "COM¹.txt", "movie/a:b", "movie/a?b", "movie/\x00", "movie/\xff", strings.Repeat("影", 81)} {
		if err := naming.ValidateDirectory(directory); !errors.Is(err, naming.ErrInput) {
			t.Fatalf("invalid directory %q: %v", directory, err)
		}
	}
}
