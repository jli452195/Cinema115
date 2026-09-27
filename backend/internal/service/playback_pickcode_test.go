package service

import (
	"context"
	"errors"
	"strm115/backend/internal/client/p115"
	"testing"
)

type pickOnlyFake struct {
	*cacheDriveFake
	reads int
	fail  bool
}

func (f *pickOnlyFake) DownloadInfoWithUA(context.Context, *p115.ShareInfo, string, string, string) (*p115.DownloadInfo, error) {
	return nil, errors.New("share endpoint must not be used")
}
func (f *pickOnlyFake) PickCodeInstantInfo(ctx context.Context, pc, cookie string) (*p115.DownloadInfo, error) {
	f.reads++
	if f.fail {
		return nil, errors.New("source unavailable")
	}
	return f.cacheDriveFake.PickCodeInstantInfo(ctx, pc, cookie)
}
func TestPickCodeSkipsShareAndReusesInstantCache(t *testing.T) {
	s, drive, selection := setupPlaybackCache(t)
	f := &pickOnlyFake{cacheDriveFake: drive}
	s.client = f
	for i := 0; i < 2; i++ {
		link, report, err := s.ResolvePickCodeWithReport(context.Background(), selection, "abc123", "source-private", "player-UA")
		if err != nil || link != "https://cdn.115.com/fresh?private" || (i == 0 && !report.UsedSource) || report.FileCacheID != "901" {
			t.Fatal("wrong destination", err)
		}
	}
	if f.reads != 1 || drive.creates != 0 || drive.uploads != 1 || drive.links != 2 || drive.cookie != selection.cookie {
		t.Fatal("cache or account mismatch")
	}
	f.fail = true
	s.sourceMappings = nil
	for _, entry := range s.Status().Entries {
		entry.SourceKey = ""
		if err := s.put(entry); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selection, "abc123", "source-private", "player-UA"); err == nil {
		t.Fatal("source failure accepted")
	}
	if drive.links != 2 || drive.uploads != 1 {
		t.Fatal("unexpected fallback")
	}
}
func TestPickCodeMissingConfigCannotWrite(t *testing.T) {
	for _, kind := range []string{"source", "directory", "target"} {
		t.Run(kind, func(t *testing.T) {
			s, drive, selection := setupPlaybackCache(t)
			source := "source-private"
			switch kind {
			case "source":
				source = ""
			case "directory":
				selection.binding.ParentCID = ""
			case "target":
				selection.cookie = ""
			}
			if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selection, "abc123", source, "player-UA"); err == nil {
				t.Fatal("missing config accepted")
			}
			if drive.creates != 0 || drive.uploads != 0 || drive.links != 0 {
				t.Fatal("wrote without config")
			}
		})
	}
}
