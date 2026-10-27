package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type playbackSearchCounter struct {
	*fastCacheFake
	queries int
}

func (f *playbackSearchCounter) FindPlaybackFileBySHA1(ctx context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	f.queries++
	return f.fastCacheFake.FindPlaybackFileBySHA1(ctx, hash, cookie)
}
func TestPlaybackSearchBackoffDoesNotTurnErrorIntoMiss(t *testing.T) {
	s, base, selected := setupFastCache(t)
	f := &playbackSearchCounter{fastCacheFake: base}
	s.client = f
	f.searchErr = errors.New("network")
	for i := 0; i < 4; i++ {
		if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "UA"); err == nil {
			t.Fatal("error hidden")
		}
	}
	if f.queries != 1 || f.uploads != 0 {
		t.Fatal("error queried repeatedly or became upload")
	}
	for k, v := range s.searchBackoff {
		v.until = time.Now().Add(-time.Second)
		s.searchBackoff[k] = v
	}
	f.searchErr = nil
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "UA"); err != nil {
		t.Fatal(err)
	}
	if f.queries != 2 || f.uploads != 1 {
		t.Fatal("retry did not recover")
	}
}
func TestPlaybackPendingMissBackoffAndSizeMismatch(t *testing.T) {
	s, base, selected := setupFastCache(t)
	f := &playbackSearchCounter{fastCacheFake: base}
	s.client = f
	f.uploadErr = p115.ErrInstantUncertain
	_, _, _ = s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "UA")
	queries := f.queries
	for i := 0; i < 3; i++ {
		_, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "UA")
		if err == nil {
			t.Fatal("pending hidden")
		}
	}
	if f.queries != queries || f.uploads != 1 {
		t.Fatal("pending retry repeated search or upload")
	}
	if cacheMatches(p115.DrivePlaybackFile{SHA1: strings.Repeat("A", 40), Size: 999}, &p115.DownloadInfo{SHA1: strings.Repeat("A", 40), Size: 100}) {
		t.Fatal("size mismatch accepted")
	}
}
