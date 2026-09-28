package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestPlaybackPrefetchOnlyForFreshMaterialWithoutRangesOrCopies(t *testing.T) {
	for _, mode := range []string{"fresh", "saved-material", "saved-range", "known-copy"} {
		t.Run(mode, func(t *testing.T) {
			s, _, selected := setupPlaybackCache(t)
			hash := strings.Repeat("A", 40)
			origin := DirectLinkKey("origin")
			material := PlaybackMaterial{ID: playbackMaterialID(hash, 100), Name: "movie.mkv", Size: 100, SHA1: hash, VerifiedAt: time.Now()}
			if mode == "saved-material" {
				material.Origins = []string{origin}
			}
			if mode == "saved-range" {
				material.Ranges = []PlaybackRange{{Start: 1, End: 3, SHA1: hash, VerifiedAt: time.Now()}}
			}
			if mode != "fresh" {
				if err := s.putMaterial(material); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "known-copy" {
				if err := s.put(PlaybackCacheEntry{Key: DirectLinkKey("copy"), AccountID: "donor", Status: "ready", File: p115.DrivePlaybackFile{SHA1: hash, Size: 100, ID: "123", PickCode: "abc123"}}); err != nil {
					t.Fatal(err)
				}
			}
			reads := 0
			ctx := context.WithValue(context.Background(), playbackMaterialOrigin{}, origin)
			info, _, _, err := s.preparePlaybackMaterial(ctx, selected, &PlaybackResolution{}, func() (*p115.DownloadInfo, error) {
				reads++
				return &p115.DownloadInfo{Name: "movie.mkv", SHA1: hash, Size: 100, URL: "https://cdn.115.com/private"}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if (info.PrefetchRangeSource != nil) != (mode == "fresh") {
				t.Fatal("wrong speculation policy", mode)
			}
			if mode == "saved-material" && reads != 0 {
				t.Fatal("read primary despite cached material")
			}
		})
	}
}
