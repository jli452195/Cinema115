package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type instantClientFake struct {
	read   func(string)
	upload func(string, string)
	err    error
	calls  int
}

func (f *instantClientFake) DownloadInstantInfoWithUA(_ context.Context, _ *p115.ShareInfo, _ string, cookie, _ string) (*p115.DownloadInfo, error) {
	if f.read != nil {
		f.read(cookie)
	}
	return &p115.DownloadInfo{Name: "video.mkv", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/media?private-link"}, nil
}
func (f *instantClientFake) InstantUpload(_ context.Context, _ *p115.DownloadInfo, cid, cookie string, progress func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	f.calls++
	if f.upload != nil {
		f.upload(cid, cookie)
	}
	progress(p115.InstantProgress{Stage: "range", Bytes: 3})
	progress(p115.InstantProgress{Stage: "range_done", Bytes: 3})
	if f.err != nil {
		return nil, f.err
	}
	return &p115.InstantUploadResult{FileID: "123", PickCode: "pick1234", Challenged: true}, nil
}

func setupInstantService(t *testing.T) (*InstantUploads, *instantClientFake, InstantUploadConfig, *SessionStore) {
	t.Helper()
	accounts, err := newP115Accounts(&failingAccountStore{}, "")
	if err != nil {
		t.Fatal(err)
	}
	source, _ := accounts.AddPrimary("源账号")
	target, _ := accounts.Add("目标账号")
	srcCookie, dstCookie := "UID=11_A; SEID=source-private", "UID=22_A; SEID=target-private"
	if err := accounts.Update(source, 1, "源账号", &srcCookie); err != nil {
		t.Fatal(err)
	}
	if err := accounts.Update(target, 1, "目标账号", &dstCookie); err != nil {
		t.Fatal(err)
	}
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "instant.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	fake := &instantClientFake{}
	s, err := NewInstantUploads(store, accounts, fake, NewEmbyProxyLogs(&GenerationLogs{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.UpdatePlaybackDirectory(target, 2, "0"); err != nil {
		t.Fatal(err)
	}
	s.Playback = &PlaybackCache{doc: PlaybackCacheStatus{Config: PlaybackCacheConfig{Bindings: []PlaybackBinding{{UserID: "user-000000000001", AccountID: target}}}}}
	config := InstantUploadConfig{UserID: "user-000000000001", SourceAccountID: source, AccountID: target, TargetCID: "0", TargetPath: "0"}
	if err := s.Apply(config); err != nil {
		t.Fatal(err)
	}
	return s, fake, config, store
}

func TestInstantTaskPinsAccountsConfigAndDeduplicatesRequest(t *testing.T) {
	s, fake, config, store := setupInstantService(t)
	fake.read = func(cookie string) {
		if !strings.Contains(cookie, "source-private") {
			t.Fatal("源账号错误")
		}
		_, err := s.Run(context.Background(), config, "concurrent-request-002", &p115.ShareInfo{Code: "private-share"}, "1")
		if !errors.Is(err, ErrInstantBusy) {
			t.Fatal("并发重复写入未被阻止")
		}
		newCookie := "UID=22_A; SEID=replacement-private"
		if err := s.accounts.Update(config.AccountID, 3, "目标账号", &newCookie); err != nil {
			t.Fatal(err)
		}
		changed := config
		changed.TargetCID = "9"
		if err := s.accounts.UpdatePlaybackDirectory(config.AccountID, 4, "9"); err != nil {
			t.Fatal(err)
		}
		if err := s.Apply(changed); err != nil {
			t.Fatal(err)
		}
	}
	fake.upload = func(cid, cookie string) {
		if cid != "0" || !strings.Contains(cookie, "target-private") {
			t.Fatal("任务中途切换了目录或账号凭据")
		}
	}
	result, err := s.Run(context.Background(), config, "test-request-00001", &p115.ShareInfo{Code: "private-share", Password: "private-password"}, "1")
	if err != nil || result.Status != "success" || result.FileID != "123" {
		t.Fatalf("%+v %v", result, err)
	}
	again, err := s.Run(context.Background(), config, "test-request-00001", &p115.ShareInfo{Code: "private-share"}, "1")
	if err != nil || again.ID != result.ID || fake.calls != 1 {
		t.Fatal("同一请求发生重复上传")
	}
	if s.Status().Config.TargetCID != "9" {
		t.Fatal("任务完成覆盖了更新配置")
	}
	raw, _ := json.Marshal(s.Status())
	logs, _ := json.Marshal(s.logs.Snapshot())
	for _, value := range []string{string(raw), string(logs), store.Value()} {
		for _, secret := range []string{"private", "SEID=", "UID=", "https://"} {
			if strings.Contains(value, secret) {
				t.Fatalf("结果、存储或日志泄漏凭据: %s", secret)
			}
		}
	}
	reloaded, err := NewInstantUploads(store, s.accounts, fake, s.logs)
	if err != nil || reloaded.Status().Records[0].Status != "success" {
		t.Fatal("结果未持久保存")
	}
}

func TestInstantRequiresPrimaryBoundSecondaryAndDirectory(t *testing.T) {
	for _, field := range []string{"source", "target", "directory", "deleted", "cleared"} {
		t.Run(field, func(t *testing.T) {
			s, fake, config, _ := setupInstantService(t)
			bad := config
			switch field {
			case "source":
				if err := s.accounts.Delete(config.SourceAccountID, 2); err != nil {
					t.Fatal(err)
				}
			case "target":
				bad.UserID = ""
			case "directory":
				if err := s.accounts.UpdatePlaybackDirectory(config.AccountID, 3, ""); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if err := s.accounts.Delete(config.AccountID, 3); err != nil {
					t.Fatal(err)
				}
			case "cleared":
				empty := ""
				if err := s.accounts.Update(config.AccountID, 3, "目标账号", &empty); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Apply(bad); err == nil {
				t.Fatal("允许缺失账号或目录")
			}
			if field == "deleted" || field == "cleared" {
				if _, err := s.Run(context.Background(), config, "test-request-00001", &p115.ShareInfo{Code: "s"}, "1"); !errors.Is(err, ErrInstantConfig) {
					t.Fatal("启动时未重新检查账号")
				}
			}
			if fake.calls != 0 {
				t.Fatal("错误回退其他账号")
			}
		})
	}
}

func TestInstantUnsupportedUncertainAndRestart(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status string
	}{{p115.ErrInstantUnsupported, "unsupported"}, {p115.ErrInstantUncertain, "uncertain"}} {
		t.Run(tc.status, func(t *testing.T) {
			s, fake, config, store := setupInstantService(t)
			fake.err = tc.err
			result, err := s.Run(context.Background(), config, "test-request-00001", &p115.ShareInfo{Code: "s"}, "1")
			if err != nil || result.Status != tc.status || result.FileID != "" {
				t.Fatalf("%+v %v", result, err)
			}
			_, err = s.Run(context.Background(), config, "test-request-00001", &p115.ShareInfo{Code: "s"}, "1")
			if err != nil || fake.calls != 1 {
				t.Fatal("重复执行未确认任务")
			}
			s.doc.Records[0].Status = "running"
			raw, _ := json.Marshal(s.doc)
			if err := store.Save(string(raw)); err != nil {
				t.Fatal(err)
			}
			reloaded, err := NewInstantUploads(store, s.accounts, fake, s.logs)
			if err != nil || reloaded.Status().Records[0].Status != "uncertain" || reloaded.Status().Running {
				t.Fatal("重启后错误恢复为运行或成功")
			}
		})
	}
}
