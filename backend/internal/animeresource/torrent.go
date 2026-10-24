package animeresource

import (
	"errors"
	"fmt"

	"strm115/backend/internal/utils/torrentmeta"
)

// TorrentHash shares the manifest parser and hashes the original info bytes.
func TorrentHash(body []byte) (string, error) {
	meta, err := torrentmeta.Parse(body)
	if errors.Is(err, torrentmeta.ErrUnsupported) {
		return "", fmt.Errorf("parse torrent: %w", ErrUnsupported)
	}
	if err != nil {
		return "", fmt.Errorf("parse torrent: %w", ErrInvalid)
	}
	return meta.InfoHash, nil
}
