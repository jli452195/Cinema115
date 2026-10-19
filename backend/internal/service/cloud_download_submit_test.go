package service

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type cloudWorkflowStub struct {
	lookup func(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error)
	remove func(context.Context, string, string) error
	add    func(context.Context, string, string, string) (json.RawMessage, error)
}

func (s *cloudWorkflowStub) LookupTask(ctx context.Context, cookie string, resource p115.CloudResource) (p115.CloudTask, bool, error) {
	return s.lookup(ctx, cookie, resource)
}
func (s *cloudWorkflowStub) DeleteTaskKeepFiles(ctx context.Context, cookie, hash string) error {
	return s.remove(ctx, cookie, hash)
}
func (s *cloudWorkflowStub) AddURL(ctx context.Context, cookie, rawURL, cid string) (json.RawMessage, error) {
	return s.add(ctx, cookie, rawURL, cid)
}

func cloudWorkflowAccounts(t *testing.T) (*P115Accounts, CloudDownloadConfig) {
	t.Helper()
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "accounts.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := newP115Accounts(store, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := accounts.AddPrimary("明确绑定账号")
	if err != nil {
		t.Fatal(err)
	}
	account, err := accounts.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_A; CID=original"
	if err := accounts.Update(id, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	return accounts, CloudDownloadConfig{AccountID: id, TargetCID: "9", TargetPath: "/新目录"}
}

func TestCloudSubmitDefaultWorkflow(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	const link = "magnet:?xt=urn:btih:" + hash
	upstreamErr := errors.New("上游拒绝")
	for _, tc := range []struct {
		name                         string
		found                        bool
		task                         p115.CloudTask
		lookupErr, removeErr, addErr error
		wantErr                      error
		steps                        []string
		removed                      bool
	}{
		{name: "无旧任务直接提交", steps: []string{"lookup", "add"}},
		{name: "已完成删除重提", found: true, task: p115.CloudTask{InfoHash: hash, DisplayStatus: "finished"}, steps: []string{"lookup", "remove", "add"}, removed: true},
		{name: "已失败删除重提", found: true, task: p115.CloudTask{InfoHash: hash, DisplayStatus: "failed"}, steps: []string{"lookup", "remove", "add"}, removed: true},
		{name: "已停止删除重提", found: true, task: p115.CloudTask{InfoHash: hash, DisplayStatus: "stopped"}, steps: []string{"lookup", "remove", "add"}, removed: true},
		{name: "运行中不删除", found: true, task: p115.CloudTask{InfoHash: hash, DisplayStatus: "downloading"}, steps: []string{"lookup"}, wantErr: ErrCloudTaskRunning},
		{name: "等待中不删除", found: true, task: p115.CloudTask{InfoHash: hash, DisplayStatus: "waiting"}, steps: []string{"lookup"}, wantErr: ErrCloudTaskRunning},
		{name: "未知状态不删除", found: true, task: p115.CloudTask{InfoHash: hash}, steps: []string{"lookup"}, wantErr: ErrCloudTaskUnclear},
		{name: "Hash不符不删除", found: true, task: p115.CloudTask{InfoHash: strings.Repeat("a", 40), DisplayStatus: "finished"}, steps: []string{"lookup"}, wantErr: ErrCloudTaskUnclear},
		{name: "查询失败停止", lookupErr: upstreamErr, steps: []string{"lookup"}, wantErr: upstreamErr},
		{name: "删除失败停止", found: true, task: p115.CloudTask{InfoHash: hash, DisplayStatus: "finished"}, removeErr: upstreamErr, steps: []string{"lookup", "remove"}, wantErr: upstreamErr},
		{name: "删除后提交失败保留回执", found: true, task: p115.CloudTask{InfoHash: hash, DisplayStatus: "finished"}, addErr: upstreamErr, steps: []string{"lookup", "remove", "add"}, wantErr: upstreamErr, removed: true},
		{name: "删除后提交超时不重试", found: true, task: p115.CloudTask{InfoHash: hash, DisplayStatus: "finished"}, addErr: context.DeadlineExceeded, steps: []string{"lookup", "remove", "add"}, wantErr: context.DeadlineExceeded, removed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts, config := cloudWorkflowAccounts(t)
			var steps []string
			client := &cloudWorkflowStub{
				lookup: func(ctx context.Context, cookie string, r p115.CloudResource) (p115.CloudTask, bool, error) {
					steps = append(steps, "lookup")
					if r.Hash != hash {
						t.Error("查询 Hash 不符")
					}
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > CloudDownloadTimeout {
						t.Error("缺少总超时")
					}
					return tc.task, tc.found, tc.lookupErr
				},
				remove: func(_ context.Context, _, got string) error {
					steps = append(steps, "remove")
					if got != hash {
						t.Error("删除 Hash 不符")
					}
					return tc.removeErr
				},
				add: func(_ context.Context, _, raw, cid string) (json.RawMessage, error) {
					steps = append(steps, "add")
					if raw != link || cid != "9" {
						t.Error("提交未使用指定链接或新目录")
					}
					return json.RawMessage(`{}`), tc.addErr
				},
			}
			s := &CloudDownloadService{Accounts: accounts, Client: client, Coordinator: &CloudDownloadCoordinator{}}
			receipt, err := s.Submit(context.Background(), config, link)
			if !errors.Is(err, tc.wantErr) || receipt.RemovedOldTask != tc.removed || !reflect.DeepEqual(steps, tc.steps) {
				t.Fatalf("err=%v removed=%v steps=%v", err, receipt.RemovedOldTask, steps)
			}
			if len(s.Coordinator.uids) != 0 {
				t.Fatal("并发锁未释放")
			}
		})
	}
}

func TestCloudSubmitDeduplicatesConcurrentEntrances(t *testing.T) {
	accounts, config := cloudWorkflowAccounts(t)
	hash := "0123456789abcdef0123456789abcdef01234567"
	decoded, err := hex.DecodeString(hash)
	if err != nil {
		t.Fatal(err)
	}
	base32Hash := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(decoded)
	entered, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	client := &cloudWorkflowStub{
		lookup: func(ctx context.Context, _ string, _ p115.CloudResource) (p115.CloudTask, bool, error) {
			close(entered)
			select {
			case <-release:
				return p115.CloudTask{}, false, nil
			case <-ctx.Done():
				return p115.CloudTask{}, false, ctx.Err()
			}
		},
		add: func(context.Context, string, string, string) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	}
	s := &CloudDownloadService{Accounts: accounts, Client: client, Coordinator: &CloudDownloadCoordinator{}}
	go func() { _, err := s.Submit(ctx, config, "magnet:?xt=urn:btih:"+hash); finished <- err }()
	defer func() { cancel(); <-finished }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("查询未开始")
	}
	otherDirectory := config
	otherDirectory.TargetCID = "88"
	_, err = s.Redo(context.Background(), otherDirectory, "magnet:?xt=urn:btih:"+base32Hash+"&dn=another-name")
	if !errors.Is(err, ErrCloudTaskBusy) {
		t.Fatalf("另一入口未阻止重复处理: %v", err)
	}
	close(release)
}

func TestCloudSubmitSnapshotsCredentialsAndSupportsED2K(t *testing.T) {
	accounts, config := cloudWorkflowAccounts(t)
	hash := strings.Repeat("a", 32)
	var cookies []string
	client := &cloudWorkflowStub{
		lookup: func(_ context.Context, cookie string, resource p115.CloudResource) (p115.CloudTask, bool, error) {
			cookies = append(cookies, cookie)
			if resource.Protocol != "ed2k" || resource.Hash != hash {
				t.Error("ED2K 解析错误")
			}
			account, err := accounts.Get(config.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			replacement := "UID=changed"
			if err := accounts.Update(account.ID, account.Revision, account.Name, &replacement); err != nil {
				t.Fatal(err)
			}
			return p115.CloudTask{InfoHash: strings.ToUpper(hash), DisplayStatus: "finished"}, true, nil
		},
		remove: func(_ context.Context, cookie, got string) error {
			cookies = append(cookies, cookie)
			if got != hash {
				t.Error("删除编号错误")
			}
			return nil
		},
		add: func(_ context.Context, cookie, _, cid string) (json.RawMessage, error) {
			cookies = append(cookies, cookie)
			if cid != "9" {
				t.Error("目录改变")
			}
			return json.RawMessage(`{}`), nil
		},
	}
	s := &CloudDownloadService{Accounts: accounts, Client: client, Coordinator: &CloudDownloadCoordinator{}}
	receipt, err := s.Submit(context.Background(), config, "ed2k://|file|movie.mkv|123|"+hash+"|/")
	if err != nil || !receipt.RemovedOldTask || len(cookies) != 3 {
		t.Fatal("ED2K 重提失败", err)
	}
	for _, cookie := range cookies {
		if cookie != "UID=123_A; CID=original" {
			t.Fatal("同一次操作改变了凭据")
		}
	}
}

func TestCloudSubmitCoordinatesActualUIDAcrossConfigurationAndDevice(t *testing.T) {
	accounts, config := cloudWorkflowAccounts(t)
	otherAccounts, otherConfig := cloudWorkflowAccounts(t)
	other, err := otherAccounts.Primary(otherConfig.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_B; SEID=independent-credential"
	if err := otherAccounts.Update(other.ID, other.Revision, other.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	if config.AccountID == otherConfig.AccountID {
		t.Fatal("fixture configuration IDs must differ")
	}
	c := &CloudDownloadCoordinator{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	entered := make(chan struct{})
	client := &cloudWorkflowStub{lookup: func(ctx context.Context, cookie string, _ p115.CloudResource) (p115.CloudTask, bool, error) {
		if cookie != "UID=123_A; CID=original" {
			return p115.CloudTask{}, false, errors.New("credential changed")
		}
		close(entered)
		<-ctx.Done()
		return p115.CloudTask{}, false, ctx.Err()
	}}
	first := &CloudDownloadService{Accounts: accounts, Client: client, Coordinator: c}
	done := make(chan struct{})
	link := "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)
	go func() { defer close(done); _, _ = first.Submit(ctx, config, link) }()
	defer func() { cancel(); <-done }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first lookup not started")
	}
	var reads atomic.Int32
	secondClient := &cloudWorkflowStub{lookup: func(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error) {
		reads.Add(1)
		return p115.CloudTask{}, false, nil
	}}
	second := &CloudDownloadService{Accounts: otherAccounts, Client: secondClient, Coordinator: c}
	if _, err := second.Submit(ctx, otherConfig, link); !errors.Is(err, ErrCloudTaskBusy) || reads.Load() != 0 {
		t.Fatal("configuration ID/device suffix bypassed UID coordination", err)
	}
	cancel()
	<-done
	waitCloudResources(t, c, "123", 0)
}

func TestCloudSubmitRequiresSharedCoordinatorAndVerifiedUID(t *testing.T) {
	for _, mode := range []string{"missing coordinator", "invalid UID", "conflicting UID"} {
		t.Run(mode, func(t *testing.T) {
			accounts, config := cloudWorkflowAccounts(t)
			client := &cloudWorkflowStub{lookup: func(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error) {
				t.Fatal("invalid identity reached upstream")
				return p115.CloudTask{}, false, nil
			}}
			s := &CloudDownloadService{Accounts: accounts, Client: client, Coordinator: &CloudDownloadCoordinator{}}
			switch mode {
			case "missing coordinator":
				s.Coordinator = nil
			case "invalid UID":
				a, err := accounts.Primary(config.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				cookie := "UID=invalid; SEID=redacted"
				if err := accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
					t.Fatal(err)
				}
			case "conflicting UID":
				accounts.mu.Lock()
				accounts.value.Accounts[0].UID = "999"
				accounts.mu.Unlock()
			}
			_, err := s.Submit(context.Background(), config, "magnet:?xt=urn:btih:"+strings.Repeat("a", 40))
			if !errors.Is(err, ErrGenerationInput) || strings.Contains(err.Error(), "SEID=") {
				t.Fatal("invalid identity was not rejected safely", err)
			}
		})
	}
}
