package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type confirmationFake struct {
	*fastCacheFake
	queries, visibleAt int
	queryErr           error
	wrongHash          bool
	accountCookie      string
}

func (f *confirmationFake) FindPlaybackFileBySHA1(ctx context.Context, hash, cookie string) (*p115.DrivePlaybackFile, error) {
	f.queries++
	if cookie != f.accountCookie {
		return nil, errors.New("wrong account")
	}
	if f.queries == 1 {
		return nil, nil
	}
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.visibleAt == 0 || f.queries < f.visibleAt {
		return nil, nil
	}
	file := p115.DrivePlaybackFile{ID: "123", PickCode: "pick123", CID: "0", SHA1: hash, Size: 100}
	if f.wrongHash {
		file.SHA1 = strings.Repeat("B", 40)
	}
	f.files = []p115.DrivePlaybackFile{file}
	return &file, nil
}

func TestUncertainUploadConfirmationDoesNotReplayWrite(t *testing.T) {
	for _, tc := range []struct {
		name               string
		visibleAt, queries int
		wrong              bool
		queryErr           error
		success            bool
	}{
		{"first-confirmation", 2, 2, false, nil, true},
		{"second-confirmation", 3, 3, false, nil, true},
		{"still-missing", 0, 3, false, nil, false},
		{"search-error", 0, 2, false, errors.New("network-secret"), false},
		{"wrong-hash", 2, 2, true, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, base, selected := setupFastCache(t)
			base.uploadErr = &p115.InstantUploadError{Stage: "第 1 次秒传提交", Reason: "LZ4 解压失败", Cause: p115.ErrInstantUncertain}
			f := &confirmationFake{fastCacheFake: base, visibleAt: tc.visibleAt, queryErr: tc.queryErr, wrongHash: tc.wrong, accountCookie: selected.cookie}
			s.client = f
			link, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player")
			if (err == nil) != tc.success || f.uploads != 1 || f.queries != tc.queries {
				t.Fatal("incorrect recovery", err, f.uploads, f.queries)
			}
			e := s.Status().Entries[0]
			if tc.success {
				if link == "" || e.Status != "ready" || !e.RetryNotBefore.IsZero() {
					t.Fatal("not recovered")
				}
			} else {
				var pending *PlaybackPendingError
				if !errors.As(err, &pending) || e.Status != "uncertain" || e.RetryNotBefore.IsZero() || strings.Contains(err.Error(), "secret") {
					t.Fatal("lost pending state", err)
				}
			}
		})
	}
}

func TestConfirmationRespectsCancellation(t *testing.T) {
	s, f, selected := setupFastCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, found, err := s.confirmUncertainUpload(ctx, f, PlaybackCacheEntry{}, &p115.DownloadInfo{}, selected, "player", &PlaybackResolution{})
	if found || !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatal("cancellation ignored", err)
	}
}
