package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
	"sync"
	"testing"
)

func TestAccountPlaybackDirectoryPersistsAndIsolates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accounts, err := OpenP115AccountsSQLite(ctx, db, dir, "secret")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := accounts.Add("friend")
	other, _ := accounts.Add("other")
	cookie := "UID=22_A"
	if err = accounts.Update(id, 1, "friend", &cookie); err != nil {
		t.Fatal(err)
	}
	if err = accounts.UpdatePlaybackDirectory(id, 2, "123"); err != nil {
		t.Fatal(err)
	}
	if err = accounts.UpdatePlaybackDirectory(id, 2, "456"); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("stale write accepted")
	}
	if err = accounts.UpdatePlaybackDirectory("", 3, "0"); !errors.Is(err, ErrAccountMissing) {
		t.Fatal("implicit account selected")
	}
	if err = accounts.UpdatePlaybackDirectory(id, 3, "+1"); err == nil {
		t.Fatal("invalid directory accepted")
	}
	reopened, err := OpenP115AccountsSQLite(ctx, db, dir, "secret")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := reopened.Get(id)
	b, _ := reopened.Get(other)
	if a.PlaybackParentCID != "123" || b.PlaybackParentCID != "" {
		t.Fatal("directory persistence or isolation failed")
	}
	replacement := "UID=33_A"
	if err = reopened.Update(id, a.Revision, "friend", &replacement); err != nil {
		t.Fatal(err)
	}
	a, _ = reopened.Get(id)
	if a.PlaybackParentCID != "" {
		t.Fatal("directory leaked across UID change")
	}
	if err = reopened.UpdatePlaybackDirectory(id, a.Revision, "0"); err != nil {
		t.Fatal(err)
	}
	a, _ = reopened.Get(id)
	empty := ""
	if err = reopened.Update(id, a.Revision, "friend", &empty); err != nil {
		t.Fatal(err)
	}
	a, _ = reopened.Get(id)
	if a.PlaybackParentCID != "" {
		t.Fatal("directory survived credential removal")
	}
}

func TestPlaybackCardsUpdateIndependentlyAndRejectStaleWrites(t *testing.T) {
	s, _, selection := setupPlaybackCache(t)
	account := selection.binding.AccountID
	ids := []string{"11111111111111111111111111111111", "22222222222222222222222222222222"}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := s.UpdateBinding("http://emby:8096", PlaybackBinding{UserID: id, AccountID: account}, "", false); err != nil {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()
	if len(s.Status().Config.Bindings) != 3 || s.Status().Config.RetentionHours != 72 {
		t.Fatal("card save lost other settings")
	}
	if err := s.UpdateBinding("http://emby:8096", PlaybackBinding{UserID: ids[0]}, "stale", true); !errors.Is(err, ErrPlaybackBindingChanged) {
		t.Fatal("stale save accepted")
	}
	if err := s.UpdateBinding("http://emby:8096", PlaybackBinding{UserID: ids[0]}, "", false); err == nil {
		t.Fatal("empty account implicitly selected")
	}
	if err := s.accounts.Delete(account, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot().Select("http://emby:8096", ids[0]); err == nil {
		t.Fatal("deleted account fell back")
	}
	for _, id := range append(ids, cacheTestUser) {
		if err := s.UpdateBinding("http://emby:8096", PlaybackBinding{UserID: id}, account, true); err != nil {
			t.Fatal(err)
		}
	}
	if selected, err := s.Snapshot().Select("http://new-emby:8096", cacheTestUser); err != nil || selected != nil {
		t.Fatal("empty bindings should allow source playback", err)
	}
}

func TestPlaybackIgnoresRetiredEnabledFlag(t *testing.T) {
	s, _, _ := setupPlaybackCache(t)
	raw, err := json.Marshal(s.Status().Config)
	if err != nil {
		t.Fatal(err)
	}
	var config PlaybackCacheConfig
	if err := json.Unmarshal(append([]byte(`{"enabled":false,`), raw[1:]...), &config); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(config); err != nil {
		t.Fatal(err)
	}
	if selected, err := s.Snapshot().Select("http://emby:8096", cacheTestUser); err != nil || selected == nil {
		t.Fatal("retired switch disabled binding", err)
	}
	if err := s.ApplyBindings("http://emby:8096", nil); err != nil {
		t.Fatal("empty bindings rejected", err)
	}
}

func TestPlaybackDirectoryPinnedAndOptionalForShareDirect(t *testing.T) {
	s, drive, selection := setupPlaybackCache(t)
	if err := s.accounts.UpdatePlaybackDirectory(selection.binding.AccountID, 3, ""); err != nil {
		t.Fatal(err)
	}
	latest, err := s.Snapshot().Select("http://emby:8096", cacheTestUser)
	if err != nil {
		t.Fatal(err)
	}
	if selection.binding.ParentCID != "0" || latest.binding.ParentCID != "" || selection.DirectLinkScope() == latest.DirectLinkScope() {
		t.Fatal("directory not pinned or cache scope unchanged")
	}
	fake := &shareFirstFake{cacheDriveFake: drive, t: t, friendInfo: &p115.DownloadInfo{URL: "https://cdn.115.com/direct"}}
	s.client = fake
	if _, err := resolveTestCache(s, latest); err != nil {
		t.Fatal("direct playback required directory", err)
	}
	fake.friendErr = p115.ErrShareNotReadable
	if _, err := resolveTestCache(s, latest); err == nil {
		t.Fatal("instant upload accepted missing directory")
	}
	if fake.sourceReads != 0 || drive.creates != 0 || drive.uploads != 0 {
		t.Fatal("cloud mutation without directory")
	}
}

func TestPlaybackPagesSaveIndependently(t *testing.T) {
	s, _, selection := setupPlaybackCache(t)
	if err := s.ApplyCleanup(false, 96); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyBindings("http://emby:8096", []PlaybackBinding{{UserID: cacheTestUser, AccountID: selection.binding.AccountID}}); err != nil {
		t.Fatal(err)
	}
	c := s.Status().Config
	if c.CleanupEnabled || c.RetentionHours != 96 {
		t.Fatal("binding save reset cleanup")
	}
	if err := s.ApplyCleanup(true, 120); err == nil {
		t.Fatal("retired cleanup enabled")
	}
	c = s.Status().Config
	if len(c.Bindings) != 1 {
		t.Fatal("cleanup save reset binding")
	}
}
