package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"strm115/backend/internal/client/p115"
	"sync"
	"testing"
	"time"
)

const cacheTestUser = "8be403a10974486c94a987b8036c8484"

type cacheDriveFake struct {
	files                            []p115.DrivePlaybackFile
	creates, uploads, links, deletes int
	cookie, ua                       string
	block                            chan struct{}
	started                          chan struct{}
	uploadErr                        error
	deleteErr                        error
}

func (f *cacheDriveFake) ValidateCacheParent(context.Context, string, string) error { return nil }

func (f *cacheDriveFake) DownloadInfoWithUA(ctx context.Context, _ *p115.ShareInfo, _ string, cookie, ua string) (*p115.DownloadInfo, error) {
	if cookie == "UID=22_A; SEID=private" {
		return nil, p115.ErrShareNotReadable
	}
	if cookie != "source-private" || ua != p115.PlaybackUserAgent {
		return nil, errors.New("错误的源凭据或 UA")
	}
	return &p115.DownloadInfo{Name: "v.mkv", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/private"}, nil
}
func (f *cacheDriveFake) CreateDirectory(_ context.Context, parent, name, cookie string) (string, error) {
	f.creates++
	f.cookie = cookie
	if parent != "0" || !strings.HasPrefix(name, "Cinema115-cache-") {
		return "", errors.New("错误目录")
	}
	return "900", nil
}
func (f *cacheDriveFake) CacheDirectoryFiles(_ context.Context, cid, cookie string) ([]p115.DrivePlaybackFile, error) {
	if cid != "900" {
		return nil, errors.New("错误目录")
	}
	return append([]p115.DrivePlaybackFile{}, f.files...), nil
}
func (f *cacheDriveFake) InstantUpload(ctx context.Context, info *p115.DownloadInfo, cid, cookie string, _ func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	f.uploads++
	if f.started != nil {
		close(f.started)
		f.started = nil
	}
	if f.block != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.block:
		}
	}
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	f.files = []p115.DrivePlaybackFile{{ID: "901", CID: cid, Name: info.Name, Size: info.Size, SHA1: info.SHA1, PickCode: "pick1234"}}
	return &p115.InstantUploadResult{PickCode: "pick1234"}, nil
}
func (f *cacheDriveFake) DriveDownloadURLWithUA(_ context.Context, id, pc, cookie, ua string) (string, error) {
	f.links++
	f.cookie = cookie
	f.ua = ua
	return "https://cdn.115.com/fresh?private", nil
}
func (f *cacheDriveFake) DeleteEntries(_ context.Context, ids []string, cookie string) error {
	f.deletes++
	if len(ids) != 1 || ids[0] != "901" {
		return errors.New("删除范围错误")
	}
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.files = nil
	return nil
}
func setupPlaybackCache(t *testing.T) (*PlaybackCache, *cacheDriveFake, *PlaybackCacheSelection) {
	t.Helper()
	accounts, err := newP115Accounts(&failingAccountStore{}, "")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := accounts.Add("朋友")
	cookie := "UID=22_A; SEID=private"
	if err := accounts.Update(id, 1, "朋友", &cookie); err != nil {
		t.Fatal(err)
	}
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "cache.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	fake := &cacheDriveFake{}
	s, err := NewPlaybackCache(context.Background(), store, accounts, fake, NewEmbyProxyLogs(&GenerationLogs{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	config := PlaybackCacheConfig{Enabled: true, EmbyURL: "http://emby:8096", CleanupEnabled: true, RetentionHours: 72, Bindings: []PlaybackBinding{{UserID: cacheTestUser, UserName: "friend", AccountID: id, ParentCID: "0"}}}
	if err := s.Apply(config); err != nil {
		t.Fatal(err)
	}
	selection, err := s.Snapshot().Select(config.EmbyURL, cacheTestUser)
	if err != nil {
		t.Fatal(err)
	}
	return s, fake, selection
}
func resolveTestCache(s *PlaybackCache, selection *PlaybackCacheSelection) (string, error) {
	return s.Resolve(context.Background(), selection, &p115.ShareInfo{Code: "private-share", Password: "private-password"}, "1", "source-private", "player-UA")
}
func TestPlaybackCacheConcurrentReusePinnedAccountAndFreshUA(t *testing.T) {
	s, f, selection := setupPlaybackCache(t)
	f.block = make(chan struct{})
	started := make(chan struct{})
	f.started = started
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, err := resolveTestCache(s, selection); errs <- err }()
	<-started
	replacement := "UID=33_A; SEID=replacement"
	if err := s.accounts.Update(selection.binding.AccountID, 2, "朋友", &replacement); err != nil {
		t.Fatal(err)
	}
	go func() { defer wg.Done(); _, err := resolveTestCache(s, selection); errs <- err }()
	close(f.block)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.creates != 1 || f.uploads != 1 || f.links != 2 || f.ua != "player-UA" || f.cookie != "UID=22_A; SEID=private" {
		t.Fatalf("未合并秒传或固定凭据 %+v", f)
	}
	if len(s.Status().Entries) != 1 || s.Status().Entries[0].Status != "ready" {
		t.Fatal("缓存未持久登记")
	}
	raw := s.store.Value()
	for _, secret := range []string{"private", "Cookie", "https://"} {
		if strings.Contains(raw, secret) {
			t.Fatal("缓存记录泄漏凭据")
		}
	}
}
func TestPlaybackCacheSelectionNeverFallsBack(t *testing.T) {
	s, _, selection := setupPlaybackCache(t)
	if got, err := s.Snapshot().Select("http://emby:8096", "unbound"); got != nil || err != nil {
		t.Fatal("未绑定用户不应创建朋友缓存")
	}
	if _, err := s.Snapshot().Select("http://other:8096", cacheTestUser); err == nil {
		t.Fatal("跨 Emby 复用了绑定")
	}
	if err := s.accounts.Delete(selection.binding.AccountID, 2); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Snapshot().Select("http://emby:8096", cacheTestUser); err == nil || got != nil {
		t.Fatal("删除账号后回退")
	}
}
func TestPlaybackCacheUncertainDoesNotRepeatWrite(t *testing.T) {
	s, f, selection := setupPlaybackCache(t)
	f.uploadErr = p115.ErrInstantUncertain
	for i := 0; i < 2; i++ {
		if _, err := resolveTestCache(s, selection); err == nil {
			t.Fatal("未确认上传被当成成功")
		}
	}
	if f.uploads != 1 || f.creates != 1 || s.Status().Entries[0].Status != "uncertain" {
		t.Fatal("重复上传未确认结果")
	}
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.deletes != 0 {
		t.Fatal("清理了未确认文件")
	}
}
func TestPlaybackCacheCleanupScopeAndRetention(t *testing.T) {
	for _, mode := range []string{"expired", "recent", "extra", "changed", "rebound", "deleted_account", "failed_delete", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			s, f, selection := setupPlaybackCache(t)
			if _, err := resolveTestCache(s, selection); err != nil {
				t.Fatal(err)
			}
			e := s.Status().Entries[0]
			e.LastUsed = time.Now().Add(-73 * time.Hour)
			if mode == "recent" {
				e.LastUsed = time.Now()
			}
			if err := s.put(e); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "extra":
				f.files = append(f.files, p115.DrivePlaybackFile{ID: "other"})
			case "changed":
				f.files[0].SHA1 = strings.Repeat("B", 40)
			case "rebound":
				cookie := "UID=33_A"
				_ = s.accounts.Update(e.AccountID, 2, "朋友", &cookie)
			case "deleted_account":
				_ = s.accounts.Delete(e.AccountID, 2)
			case "failed_delete":
				f.deleteErr = errors.New("network")
			case "disabled":
				c := s.Status().Config
				c.CleanupEnabled = false
				if err := s.Apply(c); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Cleanup(context.Background()); err != nil {
				t.Fatal(err)
			}
			expected := 0
			if mode == "expired" || mode == "failed_delete" {
				expected = 1
			}
			if f.deletes != expected {
				t.Fatalf("意外删除: %d", f.deletes)
			}
			if mode == "expired" && len(s.Status().Entries) != 0 {
				t.Fatal("清理后记录未移除")
			}
			if mode == "failed_delete" {
				if err := s.Cleanup(context.Background()); err != nil {
					t.Fatal(err)
				}
				if f.deletes != 1 {
					t.Fatal("重放删除请求")
				}
			}
		})
	}
}
func TestPlaybackCacheForgetOnlyLocalAndMissingFileFails(t *testing.T) {
	s, f, selection := setupPlaybackCache(t)
	if _, err := resolveTestCache(s, selection); err != nil {
		t.Fatal(err)
	}
	key := s.Status().Entries[0].Key
	f.files = nil
	if _, err := resolveTestCache(s, selection); err == nil {
		t.Fatal("缓存消失仍返回直链")
	}
	if f.uploads != 1 {
		t.Fatal("重复写入")
	}
	if err := s.Forget(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if f.deletes != 0 || len(s.Status().Entries) != 0 {
		t.Fatal("移除记录不应删除云文件")
	}
}
