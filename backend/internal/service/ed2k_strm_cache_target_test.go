package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestED2KSTRMSelectedCacheSettingsUseVerifiedPrimaryDirectory(t *testing.T) {
	for _, mode := range []string{"selected", "root", "wrong-account", "invalid-cid", "wrong-directory", "invalid-server-path", "read-error", "refresh-during-read", "replace-during-read", "automatic"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newED2KSTRMFixture(t)
			a, err := f.accounts.Primary("")
			if err != nil {
				t.Fatal(err)
			}
			in := model.ED2KSTRMSettings{BaseURL: "https://new.example/", CacheAccountID: a.ID, CacheCID: "42", CachePath: "/客户端声明"}
			wantPath := "/服务端目录"
			wantErr := error(nil)
			wantCalls := []string{"directory"}
			switch mode {
			case "root":
				in.CacheCID, wantPath = "0", "/"
			case "wrong-account":
				in.CacheAccountID, wantErr, wantCalls = "other-primary", ErrGenerationInput, nil
			case "invalid-cid":
				in.CacheCID, wantErr, wantCalls = "not-a-cid", ErrGenerationInput, nil
			case "wrong-directory", "invalid-server-path", "refresh-during-read", "replace-during-read":
				wantErr = ErrGenerationInput
			case "read-error":
				wantErr = context.DeadlineExceeded
			case "automatic":
				in.CacheCID, wantPath, wantCalls = "", "", nil
			}
			f.client.directory = func(cid, cookie string) (p115.DriveDirectory, error) {
				if cid != in.CacheCID || cookie != a.Cookie {
					t.Fatal("缓存目录核验没有固定所选目录和原主账号凭据")
				}
				dir := p115.DriveDirectory{CID: cid, ParentCID: "9", Name: "服务端目录", Path: wantPath}
				switch mode {
				case "wrong-directory":
					dir.CID = "84"
				case "invalid-server-path":
					dir.Path = ""
				case "read-error":
					return dir, context.DeadlineExceeded
				case "refresh-during-read", "replace-during-read":
					updated := "UID=123_B; CID=refreshed"
					if mode == "replace-during-read" {
						updated = "UID=999_A; CID=replaced"
					}
					if err := f.accounts.Update(a.ID, a.Revision, a.Name, &updated); err != nil {
						t.Fatal(err)
					}
				}
				return dir, nil
			}
			got, err := f.service.SaveSettings(ctx, in)
			if !errors.Is(err, wantErr) {
				t.Fatalf("保存缓存目录错误=%v want=%v", err, wantErr)
			}
			stored, readErr := f.store.GetED2KSTRMSettings(ctx)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if wantErr != nil {
				if stored != (model.ED2KSTRMSettings{BaseURL: "https://cinema.example"}) {
					t.Fatalf("失败的目录核验覆盖了原设置：%+v", stored)
				}
			} else if stored != got || got.CachePath != wantPath || got.CacheCID != in.CacheCID || got.CacheCID == "" && got.CacheAccountID != "" {
				t.Fatalf("目录设置未使用服务端结果或自动模式未清除归属：%+v", got)
			}
			if !slices.Equal(f.client.calls, wantCalls) || f.cloud.calls != 0 {
				t.Fatalf("目录设置触发多余请求：%v cloud=%d", f.client.calls, f.cloud.calls)
			}
		})
	}
}

func TestED2KSTRMImportPinsCacheDirectoryUntilExplicitRestore(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	settings := model.ED2KSTRMSettings{BaseURL: "https://cinema.example", CacheAccountID: f.accounts.PrimaryID(), CacheCID: "42", CachePath: "/原缓存"}
	if err := f.store.SaveED2KSTRMSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	first := f.imported(t)
	settings.CacheCID, settings.CachePath = "84", "/新缓存"
	if err := f.store.SaveED2KSTRMSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	out, err := f.service.ImportTo(ctx, f.target.ID, []string{
		ed2kSTRMTestLink("旧资源别名.mkv", first.Hash, first.Size),
		ed2kSTRMTestLink("新资源.mkv", "fedcba9876543210fedcba9876543210", first.Size),
	}, "after-directory-change")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range out.Resources {
		wantCID, wantPath := "84", "/新缓存"
		if v.Hash == first.Hash {
			wantCID, wantPath = "42", "/原缓存"
		}
		if v.CacheTargetCID != wantCID || v.CacheTargetPath != wantPath || v.CacheParentCID != "" {
			t.Fatalf("导入改写已入队目录或新任务未固定目录：%+v", v)
		}
	}
	f.restart(t)
	for _, v := range f.service.List() {
		wantCID := "84"
		if v.Hash == first.Hash {
			wantCID = "42"
		}
		if v.CacheTargetCID != wantCID {
			t.Fatalf("重启丢失缓存目录快照：%+v", v)
		}
	}
	if len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatal("导入或重启访问了网盘")
	}
}

func TestED2KSTRMRestoreOnlyUpdatesSafeCacheDirectorySnapshots(t *testing.T) {
	for _, mode := range []string{"restore", "automatic", "check", "directory-pending", "submit_pending", "upload_pending", "waiting", "stale-account"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newED2KSTRMFixture(t)
			v := f.knownResource(t)
			v.CacheTargetCID, v.CacheTargetPath, v.CacheParentCID, v.CacheCID = "42", "/原缓存", "42", "11"
			v.Status = "failed"
			switch mode {
			case "directory-pending":
				v.DirectoryPending = true
			case "submit_pending", "upload_pending", "waiting":
				v.Stage = mode
			}
			if err := f.service.save(ctx, v); err != nil {
				t.Fatal(err)
			}
			settings := model.ED2KSTRMSettings{BaseURL: "https://cinema.example", CacheAccountID: v.AccountID, CacheCID: "84", CachePath: "/新缓存"}
			if mode == "automatic" {
				settings.CacheCID, settings.CacheAccountID, settings.CachePath = "", "", ""
			} else if mode == "stale-account" {
				settings.CacheAccountID = "previous-primary"
			}
			if err := f.store.SaveED2KSTRMSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			got, err := f.service.Request(ctx, v.Hash, v.Size, mode == "check")
			if mode == "stale-account" {
				if !errors.Is(err, ErrGenerationInput) || f.service.List()[0].CacheTargetCID != "42" {
					t.Fatalf("失效账号目录仍被接受：%+v err=%v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantCID, wantPath, wantParent, wantStage := "42", "/原缓存", "42", v.Stage
			switch mode {
			case "restore", "automatic":
				wantCID, wantPath, wantParent, wantStage = settings.CacheCID, settings.CachePath, "", "locate"
			case "check":
				wantStage = "check"
			case "directory-pending":
				wantStage = "locate"
			}
			if got.CacheTargetCID != wantCID || got.CacheTargetPath != wantPath || got.CacheParentCID != wantParent || got.Stage != wantStage || got.CacheCID != "11" {
				t.Fatalf("恢复改写了不允许变动的快照：%+v", got)
			}
			stored, err := f.store.GetED2KSTRMResource(ctx, v.Hash, v.Size)
			if err != nil || stored.CacheTargetCID != wantCID || stored.CacheParentCID != wantParent {
				t.Fatalf("恢复目录快照未持久化：%+v err=%v", stored, err)
			}
			if len(f.client.calls) != 0 || f.cloud.calls != 0 {
				t.Fatal("恢复入队触发网盘请求")
			}
		})
	}
}

func TestED2KSTRMRestoreReusesExistingFileBeforeOpeningNewDirectory(t *testing.T) {
	for _, exists := range []bool{true, false} {
		name := "missing"
		if exists {
			name = "present"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newED2KSTRMFixture(t)
			v := f.knownResource(t)
			v.CacheTargetCID, v.CacheTargetPath, v.CacheParentCID = "42", "/原缓存", "42"
			v.CacheCID, v.FileID, v.PickCode = "11", "file", "pickcode"
			if err := f.service.save(ctx, v); err != nil {
				t.Fatal(err)
			}
			settings := model.ED2KSTRMSettings{BaseURL: "https://cinema.example", CacheAccountID: v.AccountID, CacheCID: "84", CachePath: "/新缓存"}
			if err := f.store.SaveED2KSTRMSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			f.client.info = func(pickcode, _ string) (*p115.DownloadInfo, error) {
				if pickcode != v.PickCode {
					t.Fatal("未先检查原文件")
				}
				if !exists {
					return nil, p115.ErrDriveFileMissing
				}
				return &p115.DownloadInfo{FileID: v.FileID, SHA1: v.SHA1, Size: v.Size, Name: v.Name}, nil
			}
			f.client.find = func(string, string) (*p115.DrivePlaybackFile, error) { return nil, nil }
			f.client.dirs = map[string]p115.DriveDirectory{
				"84": {CID: "84", ParentCID: "9", Name: "新缓存"},
				"11": {CID: "11", ParentCID: "42", Name: "ed2k-" + v.Hash + "-1234"},
			}
			f.client.create = func(parent, child, _ string) (string, error) {
				stored, err := f.store.GetED2KSTRMResource(ctx, v.Hash, v.Size)
				if err != nil || parent != "84" || child != "ed2k-"+v.Hash+"-1234" || stored.CacheParentCID != "84" || stored.FileID != "" || !stored.DirectoryPending {
					t.Fatalf("新恢复目录或写入前快照错误：parent=%s child=%s stored=%+v err=%v", parent, child, stored, err)
				}
				return "", context.DeadlineExceeded
			}
			queued, err := f.service.Request(ctx, v.Hash, v.Size, false)
			if err != nil {
				t.Fatal(err)
			}
			err = f.service.advance(ctx, &queued)
			if exists {
				if err != nil || queued.Status != "ready" || queued.CacheCID != "11" || queued.FileID != v.FileID || !slices.Equal(f.client.calls, []string{"info"}) {
					t.Fatalf("可用旧文件未直接复用：%+v calls=%v err=%v", queued, f.client.calls, err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) || !slices.Equal(f.client.calls, []string{"info", "find", "directory", "directory", "create"}) {
				t.Fatalf("缓存未确认缺失就准备新目录：calls=%v err=%v", f.client.calls, err)
			}
			if f.cloud.calls != 0 {
				t.Fatal("文件复用或建目录阶段触发云任务查询")
			}
		})
	}
}

func TestED2KSTRMSelectedCacheDirectoryNeverFallsBackWhenMissing(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	v := f.imported(t)
	v.CacheTargetCID, v.CacheTargetPath = "42", "/被删除的目录"
	if err := f.store.SaveED2KSTRMCache(ctx, model.ED2KSTRMCache{AccountID: v.AccountID, UID: v.UID, CID: "10", Name: "Cinema115-ED2K-existing"}); err != nil {
		t.Fatal(err)
	}
	f.client.directory = func(cid, _ string) (p115.DriveDirectory, error) {
		if cid != "42" {
			t.Fatalf("所选缓存目录缺失后改投其他目录：%s", cid)
		}
		return p115.DriveDirectory{}, p115.ErrDriveFileMissing
	}
	f.client.missing = func(cid, _ string) (bool, error) { return cid == "42", nil }
	if err := f.service.advance(ctx, &v); !errors.Is(err, ErrGenerationInput) || v.Status != "blocked" {
		t.Fatalf("已删除的所选目录未停止恢复：%+v err=%v", v, err)
	}
	if !slices.Equal(f.client.calls, []string{"directory", "directory-missing"}) || f.cloud.calls != 0 {
		t.Fatalf("所选目录删除后产生额外请求：%v cloud=%d", f.client.calls, f.cloud.calls)
	}
}

func TestED2KSTRMSelectedCacheRootIncludingZeroCreatesOnlyResourceDirectory(t *testing.T) {
	for _, cid := range []string{"42", "0"} {
		t.Run(cid, func(t *testing.T) {
			ctx := context.Background()
			f := newED2KSTRMFixture(t)
			v := f.imported(t)
			v.CacheTargetCID, v.CacheTargetPath = cid, "/所选缓存"
			child := "ed2k-" + v.Hash + "-1234"
			f.client.dirs = map[string]p115.DriveDirectory{
				cid:  {CID: cid, ParentCID: "9", Name: "所选缓存"},
				"11": {CID: "11", ParentCID: cid, Name: child},
			}
			f.client.create = func(parent, name, _ string) (string, error) {
				stored, err := f.store.GetED2KSTRMResource(ctx, v.Hash, v.Size)
				if err != nil || parent != cid || name != child || stored.CacheParentCID != cid || !stored.DirectoryPending {
					t.Fatalf("未在所选目录固定创建意图：parent=%s name=%s stored=%+v err=%v", parent, name, stored, err)
				}
				return "11", nil
			}
			a, err := f.accounts.Primary(v.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.ensureDirectory(ctx, &v, a); err != nil {
				t.Fatal(err)
			}
			if v.CacheCID != "11" || v.DirectoryPending || v.CacheParentCID != cid || !slices.Equal(f.client.calls, []string{"directory", "create", "directory"}) {
				t.Fatalf("所选目录创建结果错误：%+v calls=%v", v, f.client.calls)
			}
			if _, err := f.store.GetED2KSTRMCache(ctx, a.ID); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("所选目录模式误建自动缓存根目录：%v", err)
			}
		})
	}
}

func TestED2KSTRMUncertainDirectoryRestartKeepsOriginalParentAndName(t *testing.T) {
	for _, selected := range []bool{true, false} {
		name := "automatic"
		if selected {
			name = "selected"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newED2KSTRMFixture(t)
			v := f.imported(t)
			if selected {
				v.CacheTargetCID, v.CacheTargetPath = "42", "/原缓存"
			} else if err := f.store.SaveED2KSTRMCache(ctx, model.ED2KSTRMCache{AccountID: v.AccountID, UID: v.UID, CID: "42", Name: "Cinema115-ED2K-original"}); err != nil {
				t.Fatal(err)
			}
			child := "ed2k-" + v.Hash + "-1234"
			f.client.dirs = map[string]p115.DriveDirectory{"42": {CID: "42", ParentCID: "0", Name: "Cinema115-ED2K-original"}}
			f.client.create = func(parent, name, _ string) (string, error) {
				stored, err := f.store.GetED2KSTRMResource(ctx, v.Hash, v.Size)
				if err != nil || parent != "42" || name != child || stored.CacheParentCID != "42" || !stored.DirectoryPending {
					t.Fatalf("建目录请求前未持久保存原父目录：%+v err=%v", stored, err)
				}
				return "", context.DeadlineExceeded
			}
			a, err := f.accounts.Primary(v.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.ensureDirectory(ctx, &v, a); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if err := f.store.SaveED2KSTRMSettings(ctx, model.ED2KSTRMSettings{BaseURL: "https://cinema.example", CacheAccountID: a.ID, CacheCID: "84", CachePath: "/新缓存"}); err != nil {
				t.Fatal(err)
			}
			if err := f.store.SaveED2KSTRMCache(ctx, model.ED2KSTRMCache{AccountID: a.ID, UID: a.UID, CID: "90", Name: "Cinema115-ED2K-new"}); err != nil {
				t.Fatal(err)
			}
			f.restart(t)
			f.client.calls = nil
			found := false
			f.client.listing = func(parent, _ string) (p115.DriveDirectory, []p115.ShareFile, error) {
				if parent != "42" {
					t.Fatalf("未知写入恢复改投其他父目录：%s", parent)
				}
				files := []p115.ShareFile{}
				if found {
					files = append(files, p115.ShareFile{ID: "11", Name: child, Directory: true})
				}
				return f.client.dirs[parent], files, nil
			}
			restored := f.service.List()[0]
			if restored.CacheParentCID != "42" || !restored.DirectoryPending {
				t.Fatalf("重启丢失未知目录写入快照：%+v", restored)
			}
			if err := f.service.ensureDirectory(ctx, &restored, a); !errors.Is(err, errED2KDirectoryPending) {
				t.Fatalf("未知写入未保持只读核对：%v", err)
			}
			found = true
			if err := f.service.ensureDirectory(ctx, &restored, a); err != nil {
				t.Fatal(err)
			}
			if restored.CacheCID != "11" || restored.DirectoryPending || restored.CacheParentCID != "42" || !slices.Equal(f.client.calls, []string{"directory", "listing", "directory", "listing"}) {
				t.Fatalf("未知目录未复用原父目录的确定结果：%+v calls=%v", restored, f.client.calls)
			}
		})
	}
}

func TestED2KSTRMUncertainAutomaticRootRestoreDoesNotSwitchToSelectedDirectory(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	v := f.imported(t)
	originalName := ""
	f.client.create = func(parent, name, _ string) (string, error) {
		cache, err := f.store.GetED2KSTRMCache(ctx, v.AccountID)
		if err != nil || parent != "0" || cache.Name != name || !cache.Pending {
			t.Fatalf("自动根目录创建前未固定原目录名：%+v parent=%s err=%v", cache, parent, err)
		}
		originalName = name
		return "", context.DeadlineExceeded
	}
	if err := f.service.advance(ctx, &v); !errors.Is(err, context.DeadlineExceeded) || originalName == "" {
		t.Fatalf("自动根目录未进入待确认状态：%+v err=%v", v, err)
	}
	if err := f.service.save(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveED2KSTRMSettings(ctx, model.ED2KSTRMSettings{BaseURL: "https://cinema.example", CacheAccountID: v.AccountID, CacheCID: "84", CachePath: "/新缓存"}); err != nil {
		t.Fatal(err)
	}
	queued, err := f.service.Request(ctx, v.Hash, v.Size, false)
	if err != nil || queued.CacheTargetCID != "" || queued.CacheTargetPath != "" || queued.CacheParentCID != "" {
		t.Fatalf("原自动根目录结果未知时恢复切换到了新目录：%+v err=%v", queued, err)
	}
	f.restart(t)
	f.client.calls = nil
	f.client.listing = func(parent, _ string) (p115.DriveDirectory, []p115.ShareFile, error) {
		if parent != "0" {
			t.Fatalf("未知自动根目录改查其他父目录：%s", parent)
		}
		return p115.DriveDirectory{CID: "0"}, nil, nil
	}
	restored := f.service.List()[0]
	if err := f.service.advance(ctx, &restored); !errors.Is(err, errED2KDirectoryPending) {
		t.Fatalf("未知自动根目录未只读核对：%v", err)
	}
	cache, err := f.store.GetED2KSTRMCache(ctx, v.AccountID)
	if err != nil || cache.Name != originalName || !cache.Pending || restored.CacheTargetCID != "" || !slices.Equal(f.client.calls, []string{"listing"}) || f.cloud.calls != 0 {
		t.Fatalf("未知自动根目录被重建或改投：cache=%+v calls=%v err=%v", cache, f.client.calls, err)
	}
}
