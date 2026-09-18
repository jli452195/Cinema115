package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"strm115/backend/internal/client/p115"
)

type cloudAdderStub struct {
	cookie  string
	url     string
	cid     string
	task    p115.CloudTask
	found   bool
	deleted string
}

func (s *cloudAdderStub) LookupTask(_ context.Context, cookie string, _ p115.CloudResource) (p115.CloudTask, bool, error) {
	s.cookie = cookie
	return s.task, s.found, nil
}

func (s *cloudAdderStub) DeleteTaskKeepFiles(_ context.Context, cookie, hash string) error {
	s.cookie, s.deleted = cookie, hash
	return nil
}

func (s *cloudAdderStub) AddURL(_ context.Context, cookie, rawURL, targetCID string) (json.RawMessage, error) {
	s.cookie, s.url, s.cid = cookie, rawURL, targetCID
	return json.RawMessage(`{"state":1,"info_hash":"abc","name":"测试任务"}`), nil
}

func TestCloudDownloadServiceUsesOnlyExplicitAccount(t *testing.T) {
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "accounts.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := newP115Accounts(store, "")
	if err != nil {
		t.Fatal(err)
	}
	firstID, _ := accounts.Add("账号一")
	first, _ := accounts.Get(firstID)
	firstCookie := "UID=111_A; CID=first"
	if err := accounts.Update(firstID, first.Revision, first.Name, &firstCookie); err != nil {
		t.Fatal(err)
	}
	secondID, _ := accounts.Add("账号二")
	second, _ := accounts.Get(secondID)
	secondCookie := "UID=222_B; CID=second"
	if err := accounts.Update(secondID, second.Revision, second.Name, &secondCookie); err != nil {
		t.Fatal(err)
	}
	client := &cloudAdderStub{}
	downloads := &CloudDownloadService{Accounts: accounts, Client: client}
	link := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	receipt, err := downloads.Submit(context.Background(), CloudDownloadConfig{AccountID: secondID, TargetCID: "88", TargetPath: "/电影"}, link)
	if err != nil {
		t.Fatal(err)
	}
	if client.cookie != secondCookie || client.cid != "88" || client.url != link || receipt.AccountName != "账号二" || receipt.TargetPath != "/电影" {
		t.Fatalf("云下载没有固定使用明确账号和目录：%+v %#v", receipt, client)
	}
	if _, err := downloads.Submit(context.Background(), CloudDownloadConfig{TargetCID: "0", TargetPath: "/"}, link); err == nil || client.cookie != secondCookie {
		t.Fatal("空账号错误回退，或在校验失败后调用了上游")
	}
}

func TestCloudDownloadRedoDeletesOnlyVerifiedTerminalTaskAndKeepsFiles(t *testing.T) {
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "accounts.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := newP115Accounts(store, "")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := accounts.Add("账号")
	account, _ := accounts.Get(id)
	cookie := "UID=333_C"
	if err := accounts.Update(id, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	link := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	client := &cloudAdderStub{found: true, task: p115.CloudTask{InfoHash: "0123456789ABCDEF0123456789ABCDEF01234567", Status: 2, StatusKnown: true}}
	downloads := &CloudDownloadService{Accounts: accounts, Client: client}
	receipt, err := downloads.Redo(context.Background(), CloudDownloadConfig{AccountID: id, TargetCID: "9", TargetPath: "/重下"}, link)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.RemovedOldTask || client.deleted != client.task.InfoHash || client.url != link || client.cid != "9" {
		t.Fatalf("没有精确删除旧任务并重新提交：receipt=%+v client=%+v", receipt, client)
	}
}

func TestCloudDownloadRedoNeverDeletesRunningOrUnknownTask(t *testing.T) {
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "accounts.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := newP115Accounts(store, "UID=444_D")
	if err != nil {
		t.Fatal(err)
	}
	link := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name string
		task p115.CloudTask
		want error
	}{
		{"running", p115.CloudTask{InfoHash: "0123456789abcdef0123456789abcdef01234567", Status: 1, StatusKnown: true}, ErrCloudTaskRunning},
		{"unknown", p115.CloudTask{InfoHash: "0123456789abcdef0123456789abcdef01234567"}, ErrCloudTaskUnclear},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &cloudAdderStub{found: true, task: tc.task}
			downloads := &CloudDownloadService{Accounts: accounts, Client: client}
			_, err := downloads.Redo(context.Background(), CloudDownloadConfig{AccountID: "default", TargetCID: "0", TargetPath: "/"}, link)
			if !errors.Is(err, tc.want) || client.deleted != "" || client.url != "" {
				t.Fatalf("未阻止不安全删除：err=%v client=%+v", err, client)
			}
		})
	}
}
