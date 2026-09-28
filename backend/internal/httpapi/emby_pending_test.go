package httpapi

import (
	"context"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type pendingProxyFake struct{ *proxyCacheFake }

func (f *pendingProxyFake) UploadPlayback(context.Context, *p115.DownloadInfo, string, string, string, *p115.PlaybackUploadTicket, func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	f.uploads++
	return nil, p115.ErrInstantUncertain
}

func TestPendingPlaybackReturnsRetryAfterWithoutReplay(t *testing.T) {
	var denied atomic.Bool
	h := prewarmFixture(t, false, false, &denied)
	target, err := h.accounts.Add("friend")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=456_B; SEID=friend-private"
	if err := h.accounts.Update(target, 1, "friend", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := h.accounts.UpdatePlaybackDirectory(target, 2, "0"); err != nil {
		t.Fatal(err)
	}
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "cache.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	f := &pendingProxyFake{&proxyCacheFake{t: t}}
	h.cache, err = service.NewPlaybackCache(context.Background(), store, h.accounts, f, h.logs)
	if err != nil {
		t.Fatal(err)
	}
	defer h.cache.Close()
	if err := h.cache.ApplyBindings(h.config.URL, []service.PlaybackBinding{{UserID: prewarmTestUser, AccountID: target}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		w := prewarmCall(h, "/cinema115/play?ItemId=movie&MediaSourceId=src&api_key=token", "player-UA")
		seconds, err := strconv.Atoi(w.Header().Get("Retry-After"))
		if w.Code != 503 || err != nil || seconds < 1 || seconds > 30 || f.uploads != 1 {
			t.Fatal("incorrect pending response", w.Code, w.Body.String())
		}
	}
	for _, row := range h.logs.Snapshot() {
		if row.Level == "ERROR" {
			t.Fatal("pending state logged as new failure")
		}
	}
}
