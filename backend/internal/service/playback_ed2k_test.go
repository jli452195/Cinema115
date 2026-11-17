package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/strm"
)

func ed2kPlaybackTestClaim() strm.ED2K {
	return strm.ED2K{Hash: strings.Repeat("a", 32), SHA1: strings.Repeat("A", 40), Size: 100, Name: "声明名称.mkv"}
}

func TestED2KSourceAccountSnapshotSurvivesDerivedContext(t *testing.T) {
	account := P115Account{ID: "primary", UID: "123", Revision: 2, Cookie: "UID=123_A; SEID=original"}
	want := account
	ctx := WithED2KSourceAccount(context.Background(), account)
	account.Cookie, account.Revision = "UID=123_A; SEID=changed", 3
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	got, ok := ed2kSourceAccount(ctx)
	if !ok || got.ID != want.ID || got.UID != want.UID || got.Revision != want.Revision || got.Cookie != want.Cookie {
		t.Fatal("派生读取上下文丢失或替换了固定主来源凭据")
	}
	if _, ok := ed2kSourceAccount(context.Background()); ok {
		t.Fatal("未声明主来源时生成了账号快照")
	}
}

func TestED2KPlaybackReusesTargetWithoutSourceOrPrimaryCredentials(t *testing.T) {
	s, base, selected := setupFastCache(t)
	f := &fusedPlaybackFake{fastCacheFake: base}
	s.client = f
	claim := ed2kPlaybackTestClaim()
	base.files = []p115.DrivePlaybackFile{{ID: "123", CID: "77", PickCode: "pick123", SHA1: claim.SHA1, Size: claim.Size, Name: "不同名称.mkv"}}
	load := func(context.Context) (*p115.DownloadInfo, error) {
		t.Fatal("目标命中时访问了主缓存恢复入口")
		return nil, nil
	}
	if _, report, err := s.ResolveED2KWithReport(context.Background(), selected, claim, "player", load); err != nil || report.UsedSource || !report.ReusedCopy {
		t.Fatal("未复用目标文件", report, err)
	}
	if base.uploads != 0 || base.reads != 0 || f.candidates != 1 || f.verified != 1 {
		t.Fatal("目标命中额外查询或写入", base.uploads, base.reads, f.candidates, f.verified)
	}
	claim.Hash, claim.Name = strings.Repeat("b", 32), "另一个别名.mkv"
	if _, _, err := s.ResolveED2KWithReport(context.Background(), selected, claim, "player", load); err != nil {
		t.Fatal(err)
	}
	if f.candidates != 1 || base.uploads != 0 {
		t.Fatal("同内容别名重新搜索或上传")
	}
	materials := s.Status().Materials
	if len(materials) != 1 || len(materials[0].Origins) != 0 {
		t.Fatal("外部声明被写成可信 ED2K 来源关联")
	}
}

func TestED2KPlaybackTargetErrorsNeverRestoreOrWrite(t *testing.T) {
	for _, mode := range []string{"search-error", "wrong-size"} {
		t.Run(mode, func(t *testing.T) {
			s, base, selected := setupFastCache(t)
			f := &fusedPlaybackFake{fastCacheFake: base}
			s.client = f
			if mode == "search-error" {
				base.searchErr = errors.New("查询暂不可用")
			} else {
				base.files = []p115.DrivePlaybackFile{{ID: "123", CID: "77", PickCode: "pick123", SHA1: strings.Repeat("A", 40), Size: 99, Name: "冲突.mkv"}}
			}
			_, _, err := s.ResolveED2KWithReport(context.Background(), selected, ed2kPlaybackTestClaim(), "player", func(context.Context) (*p115.DownloadInfo, error) {
				t.Fatal("查询失败或冲突触发了主缓存恢复")
				return nil, nil
			})
			if err == nil || base.uploads != 0 || len(s.Status().Materials) != 0 {
				t.Fatal("查询失败或冲突被当成不存在", err)
			}
		})
	}
}

func TestED2KPlaybackLoadsSourceOnlyForMissingChallenge(t *testing.T) {
	for _, mode := range []string{"no-challenge", "challenge", "pending", "wrong-source"} {
		t.Run(mode, func(t *testing.T) {
			s, base, selected := setupFastCache(t)
			f := &materialFake{fastCacheFake: base, challenge: mode != "no-challenge"}
			s.client = f
			loads := 0
			_, report, err := s.ResolveED2KWithReport(context.Background(), selected, ed2kPlaybackTestClaim(), "player", func(context.Context) (*p115.DownloadInfo, error) {
				loads++
				if mode == "pending" {
					return nil, &PlaybackPendingError{RetryAt: time.Now().Add(time.Second), Reason: "ED2K 正在恢复"}
				}
				hash := strings.Repeat("A", 40)
				if mode == "wrong-source" {
					hash = strings.Repeat("B", 40)
				}
				return &p115.DownloadInfo{FileID: "999", SHA1: hash, Size: 100, Name: "主缓存.mkv", URL: "https://cdn.115.com/source"}, nil
			})
			failed := mode == "pending" || mode == "wrong-source"
			if (err != nil) != failed {
				t.Fatal(mode, err)
			}
			wantLoads := 1
			if mode == "no-challenge" {
				wantLoads = 0
			}
			if loads != wantLoads || base.reads != 0 {
				t.Fatal("来源读取时机错误", loads, base.reads)
			}
			if failed {
				if len(s.Status().Materials) != 0 {
					t.Fatal("失败资料被标记可信")
				}
				if mode == "pending" {
					var pending *PlaybackPendingError
					if !errors.As(err, &pending) || len(s.Status().Entries) != 0 {
						t.Fatal("来源等待丢失类型或被存成未知写入", err)
					}
				}
				return
			}
			if report.UsedSource != (mode == "challenge") {
				t.Fatal("来源使用标记错误")
			}
			if mode == "challenge" {
				other := *selected
				other.binding.AccountID, other.uid, other.cookie = "another", "33", "UID=33_A"
				if _, _, err := s.ResolveED2KWithReport(context.Background(), &other, ed2kPlaybackTestClaim(), "player", nil); err != nil || loads != 1 {
					t.Fatal("其他目标未复用已接受片段", err)
				}
			}
		})
	}
}

func TestED2KRestoreUsesKnownSharedCopyAndCommitsOnlyAcceptedRange(t *testing.T) {
	s, base, selected := setupFastCache(t)
	f := &materialFake{fastCacheFake: base}
	s.client = f
	claim := ed2kPlaybackTestClaim()
	if _, _, err := s.ResolveED2KWithReport(context.Background(), selected, claim, "player", nil); err != nil {
		t.Fatal(err)
	}
	info, commit, err := s.PrepareED2KRestore(context.Background(), claim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := info.ResolveRangeSHA1(context.Background(), 1, 3); err != nil {
		t.Fatal("无法从共享小号已知副本读取片段", err)
	}
	if f.donorReads != 1 || f.rangeReads != 1 || f.donorCookie != selected.cookie || len(s.Status().Materials[0].Ranges) != 0 {
		t.Fatal("读取越界或未接受的片段提前持久化")
	}
	if err := commit(); err != nil || len(s.Status().Materials[0].Ranges) != 1 {
		t.Fatal("已接受的片段未保存", err)
	}
	if err := s.ForgetMaterial(context.Background(), playbackMaterialID(claim.SHA1, claim.Size)); err != nil {
		t.Fatal(err)
	}
	// 管理员恢复主缓存不能把个人盘副本扩展为公共恢复来源。
	s.accounts.mu.Lock()
	for i := range s.accounts.value.Accounts {
		if s.accounts.value.Accounts[i].ID == selected.binding.AccountID {
			s.accounts.value.Accounts[i].Ownership = "member"
			s.accounts.value.Accounts[i].OwnerUserID = "personal-owner"
		}
	}
	s.accounts.mu.Unlock()
	info, _, err = s.PrepareED2KRestore(context.Background(), claim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := info.ResolveRangeSHA1(context.Background(), 1, 3); err == nil || f.donorReads != 1 {
		t.Fatal("主缓存恢复读取了个人盘")
	}
}
