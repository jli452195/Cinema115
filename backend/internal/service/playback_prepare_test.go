package service

import (
	"context"
	"errors"
	"strings"
	"strm115/backend/internal/client/p115"
	"testing"
	"time"
)

type filePrepareFake struct {
	*fastCacheFake
	fileChecks, queries int
}

func (f *filePrepareFake) CompletePlaybackFile(_ context.Context, _ *p115.DownloadInfo, _ string, result *p115.InstantUploadResult, _ string) (p115.DrivePlaybackFile, error) {
	f.fileChecks++
	for _, file := range f.files {
		if file.ID == result.FileID {
			return file, nil
		}
	}
	return p115.DrivePlaybackFile{}, errors.New("missing")
}
func (f *filePrepareFake) FindPlaybackFileBySHA1(ctx context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	f.queries++
	return f.fastCacheFake.FindPlaybackFileBySHA1(ctx, hash, cookie)
}
func TestPrepareFileWithoutLinkThenActualUA(t *testing.T) {
	s, base, selected := setupFastCache(t)
	f := &filePrepareFake{fastCacheFake: base}
	s.client = f
	ctx := PreparePlaybackFileContext(context.Background())
	for _, ua := range []string{"detail-UA", "other-UA"} {
		link, report, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "src", ua, "primary")
		if err != nil || link != "" || report.FileCacheID == "" {
			t.Fatal("preparation failed", err)
		}
	}
	if f.uploads != 1 || f.fileChecks != 1 || f.completes != 0 || f.ua != "" || f.queries != 1 {
		t.Fatal("preparation made redundant request", f)
	}
	link, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "actual-player", "primary")
	if err != nil || link == "" || f.ua != "actual-player" || f.uploads != 1 || f.queries != 1 {
		t.Fatal("actual playback did not reuse file", err)
	}
}
func TestPlaybackSearchBackoffDoesNotTurnErrorIntoMiss(t *testing.T) {
	s, base, selected := setupFastCache(t)
	f := &filePrepareFake{fastCacheFake: base}
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
	f := &filePrepareFake{fastCacheFake: base}
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
