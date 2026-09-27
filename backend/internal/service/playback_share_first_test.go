package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type shareFirstFake struct {
	*cacheDriveFake
	t                        *testing.T
	friendErr, sourceErr     error
	friendInfo               *p115.DownloadInfo
	friendReads, sourceReads int
}

func (f *shareFirstFake) DownloadInfoWithUA(ctx context.Context, share *p115.ShareInfo, file, cookie, ua string) (*p115.DownloadInfo, error) {
	if cookie == "UID=22_A; SEID=private" {
		f.friendReads++
		if ua != "player-UA" || share.Code != "private-share" || file != "1" {
			f.t.Fatal("friend request mismatch")
		}
		return f.friendInfo, f.friendErr
	}
	f.sourceReads++
	if f.sourceErr != nil {
		return nil, f.sourceErr
	}
	return f.cacheDriveFake.DownloadInfoWithUA(ctx, share, file, cookie, ua)
}

func TestFriendShareFirstBranches(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		friendErr, sourceErr             error
		link                             string
		missingSource, fallback, success bool
	}{
		{name: "direct no metadata needed", link: "https://cdn.115.com/direct?private-link", success: true},
		{name: "direct without source cookie", link: "https://cdn.115.com/direct?private-link", missingSource: true, success: true},
		{name: "restricted fallback", friendErr: p115.ErrShareNotReadable, fallback: true, success: true},
		{name: "source also denied", friendErr: p115.ErrShareNotReadable, sourceErr: p115.ErrShareNotReadable, fallback: true},
		{name: "missing source", friendErr: p115.ErrShareNotReadable, missingSource: true},
		{name: "expired friend", friendErr: p115.ErrShareLoginRequired},
		{name: "rate limited", friendErr: p115.ErrShareThrottled},
		{name: "bad password", friendErr: p115.ErrSharePassword},
		{name: "timeout", friendErr: context.DeadlineExceeded},
		{name: "cancelled", friendErr: context.Canceled},
		{name: "http forbidden", friendErr: &p115.HTTPError{Status: 403}},
		{name: "unknown failure", friendErr: errors.New("Cookie=private-cookie https://cdn.115.com/private-link")},
		{name: "cookie required", link: "https://cdn.115.com/direct?f=3"},
		{name: "invalid domain", link: "https://invalid.example/video"},
		{name: "nil response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, drive, selection := setupPlaybackCache(t)
			fake := &shareFirstFake{cacheDriveFake: drive, t: t, friendErr: tc.friendErr, sourceErr: tc.sourceErr}
			if tc.link != "" {
				fake.friendInfo = &p115.DownloadInfo{URL: tc.link}
			}
			s.client = fake
			source := "source-private"
			if tc.missingSource {
				source = ""
			}
			link, err := s.Resolve(context.Background(), selection, &p115.ShareInfo{Code: "private-share", Password: "private-password"}, "1", source, "player-UA")
			if (err == nil) != tc.success {
				t.Fatalf("unexpected result %v", err)
			}
			if fake.friendReads != 1 || (fake.sourceReads == 1) != tc.fallback {
				t.Fatal("incorrect account order")
			}
			if tc.fallback && tc.success {
				if drive.uploads != 1 || drive.links != 1 || link != "https://cdn.115.com/fresh?private" {
					t.Fatal("fallback did not use friend cache")
				}
				if _, err := resolveTestCache(s, selection); err != nil || drive.uploads != 1 || drive.links != 2 || fake.friendReads != 2 {
					t.Fatal("cache reuse failed", err)
				}
			} else if drive.creates != 0 || drive.uploads != 0 || drive.links != 0 || len(s.Status().Entries) != 0 {
				t.Fatal("unnecessary cloud mutation")
			}
			if tc.success && !tc.fallback && link != tc.link {
				t.Fatal("not friend direct URL")
			}
			text := ""
			if err != nil {
				text = err.Error()
			}
			for _, entry := range s.logs.Snapshot() {
				text += entry.Message
			}
			for _, secret := range []string{"private-cookie", "private-link", "private-share", "private-password", "source-private"} {
				if strings.Contains(text, secret) {
					t.Fatal("secret leaked")
				}
			}
		})
	}
}

func (f *shareFirstFake) DownloadInstantInfoWithUA(ctx context.Context, share *p115.ShareInfo, id, cookie, ua string) (*p115.DownloadInfo, error) {
	return f.DownloadInfoWithUA(ctx, share, id, cookie, ua)
}
