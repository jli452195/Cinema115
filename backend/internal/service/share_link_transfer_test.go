package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type shareLinkTransferFake struct {
	ownerID     string
	files       []p115.ShareFile
	pageSize    int
	targetCID   string
	targetFiles []p115.ShareFile
	listOffsets []int
	received    [][]string
	receiveCIDs []string
	failBatch   int
	fail        error
	cookies     []string
}

func (f *shareLinkTransferFake) ListForTransfer(_ context.Context, _ *p115.ShareInfo, cid string, offset int, cookie string) (*p115.SharePage, error) {
	f.cookies = append(f.cookies, cookie)
	if cid != "0" {
		return nil, fmt.Errorf("unexpected source directory %q", cid)
	}
	f.listOffsets = append(f.listOffsets, offset)
	end := min(offset+f.pageSize, len(f.files))
	if f.pageSize <= 0 || end < offset {
		end = len(f.files)
	}
	return &p115.SharePage{OwnerID: f.ownerID, Count: len(f.files), Offset: offset, Files: append([]p115.ShareFile(nil), f.files[offset:end]...)}, nil
}

func (f *shareLinkTransferFake) ReceiveShare(_ context.Context, _ *p115.ShareInfo, ids []string, cid, cookie string) error {
	f.cookies = append(f.cookies, cookie)
	f.received = append(f.received, append([]string(nil), ids...))
	f.receiveCIDs = append(f.receiveCIDs, cid)
	if f.failBatch == len(f.received) {
		return f.fail
	}
	if cid != f.targetCID {
		return fmt.Errorf("received into %q, want %q", cid, f.targetCID)
	}
	return nil
}

func (f *shareLinkTransferFake) DriveFiles(_ context.Context, cid string, _ int, cookie string) (*p115.SharePage, error) {
	f.cookies = append(f.cookies, cookie)
	if cid != f.targetCID {
		return nil, fmt.Errorf("checked directory %q, want %q", cid, f.targetCID)
	}
	return &p115.SharePage{Count: len(f.targetFiles), Files: append([]p115.ShareFile(nil), f.targetFiles...)}, nil
}

func shareLinkTransferFixture(t *testing.T, files []p115.ShareFile) (*ShareLinkTransferService, *shareLinkTransferFake, ShareTransferTarget) {
	t.Helper()
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "accounts.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := newP115Accounts(store, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := accounts.AddPrimary("main")
	if err != nil {
		t.Fatal(err)
	}
	account, err := accounts.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_A; CID=main"
	if err := accounts.Update(id, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	targetStore, err := NewSessionStore(filepath.Join(t.TempDir(), "targets.enc"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := NewShareTransferSettings(targetStore)
	if err != nil {
		t.Fatal(err)
	}
	settings.PrimaryID = accounts.PrimaryID
	settings.ValidateAccount = accounts.ValidatePrimary
	target, err := settings.SaveTarget(ShareTransferTarget{Name: "动画", AccountID: id, TargetCID: "88", TargetPath: "/动画"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &shareLinkTransferFake{ownerID: "999", files: files, pageSize: 80, targetCID: target.TargetCID, targetFiles: []p115.ShareFile{{ID: "500", Name: "existing.mkv"}}}
	return &ShareLinkTransferService{Accounts: accounts, Settings: settings, Client: fake}, fake, target
}

func numberedShareFiles(count int) []p115.ShareFile {
	files := make([]p115.ShareFile, count)
	for i := range files {
		files[i] = p115.ShareFile{ID: fmt.Sprint(i + 1), Name: fmt.Sprintf("episode-%03d.mkv", i+1), Size: 100}
	}
	return files
}

func TestShareLinkTransferUsesConfiguredDirectoryAndExistingContents(t *testing.T) {
	service, client, target := shareLinkTransferFixture(t, numberedShareFiles(2))
	receipt, err := service.Transfer(context.Background(), target, "https://115.com/s/share-code?password=abcd", "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TargetPath != target.TargetPath || receipt.ItemCount != 2 || receipt.AcceptedItems != 2 || receipt.BatchCount != 1 {
		t.Fatalf("unexpected transfer receipt: %+v", receipt)
	}
	if len(client.received) != 1 || len(client.received[0]) != 2 || client.receiveCIDs[0] != target.TargetCID {
		t.Fatalf("share items were not received directly into the configured directory: %+v", client)
	}
	if len(client.targetFiles) != 1 || client.targetFiles[0].ID != "500" {
		t.Fatal("the pre-existing target directory contents were altered")
	}
}

func TestShareLinkTransferSplitsLargeSourceIntoBatchesOfAtMost100(t *testing.T) {
	service, client, target := shareLinkTransferFixture(t, numberedShareFiles(205))
	receipt, err := service.Transfer(context.Background(), target, "https://115.com/s/share-code?password=abcd", "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ItemCount != 205 || receipt.AcceptedItems != 205 || receipt.BatchCount != 3 || len(client.received) != 3 {
		t.Fatalf("unexpected batched receipt: %+v batches=%d", receipt, len(client.received))
	}
	for i, batch := range client.received {
		if len(batch) > 100 || client.receiveCIDs[i] != target.TargetCID {
			t.Fatalf("batch %d size=%d destination=%q", i+1, len(batch), client.receiveCIDs[i])
		}
	}
	if client.received[0][0] != "1" || client.received[2][4] != "205" || len(client.listOffsets) != 3 {
		t.Fatalf("source pages or batch ordering changed: offsets=%v", client.listOffsets)
	}
}

func TestShareLinkTransferReportsUncertainBatchWithoutClaimingCompletion(t *testing.T) {
	service, client, target := shareLinkTransferFixture(t, numberedShareFiles(101))
	client.failBatch = 2
	client.fail = errors.New("receive failed")
	receipt, err := service.Transfer(context.Background(), target, "https://115.com/s/share-code?password=abcd", "")
	if err == nil || receipt.AcceptedItems != 100 || receipt.BatchCount != 1 || len(client.received) != 2 {
		t.Fatalf("partial result was not preserved: receipt=%+v err=%v", receipt, err)
	}
	if !containsError(err, "100/101") || !containsError(err, "结果可能不确定") {
		t.Fatalf("partial result did not explain retry risk: %v", err)
	}
}

func TestShareLinkTransferRejectsStaleTargetBeforeCalling115(t *testing.T) {
	service, client, target := shareLinkTransferFixture(t, numberedShareFiles(1))
	target.TargetCID = "99"
	if _, err := service.Transfer(context.Background(), target, "https://115.com/s/share-code?password=abcd", ""); !errors.Is(err, ErrShareTransferTargetChanged) {
		t.Fatalf("stale destination was accepted: %v", err)
	}
	if len(client.listOffsets) != 0 || len(client.received) != 0 {
		t.Fatalf("stale destination made upstream calls: %+v", client)
	}
}

func TestShareLinkTransferPreparedTaskKeepsCredentialsAndDestination(t *testing.T) {
	service, client, target := shareLinkTransferFixture(t, numberedShareFiles(1))
	transfer, err := service.PrepareTransfer(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transfer(context.Background(), "https://115.com/s/first-code?password=abcd", ""); err != nil {
		t.Fatal(err)
	}
	account, err := service.Accounts.Get(target.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	replacement := "UID=123_B; CID=refreshed"
	if err := service.Accounts.Update(account.ID, account.Revision, account.Name, &replacement); err != nil {
		t.Fatal(err)
	}
	updated := target
	updated.TargetCID, updated.TargetPath = "99", "/新的目录"
	if _, err := service.Settings.SaveTarget(updated); err != nil {
		t.Fatal(err)
	}
	receipt, err := transfer(context.Background(), "https://115.com/s/second-code?password=abcd", "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TargetPath != target.TargetPath || len(client.received) != 2 {
		t.Fatalf("prepared task changed destination: receipt=%+v received=%v", receipt, client.receiveCIDs)
	}
	for _, cookie := range client.cookies {
		if cookie != "UID=123_A; CID=main" {
			t.Fatal("prepared task changed credentials")
		}
	}
}

func TestShareLinkTransferAcceptsConfiguredRootDirectory(t *testing.T) {
	service, client, target := shareLinkTransferFixture(t, numberedShareFiles(1))
	target.TargetCID, target.TargetPath = "0", "/"
	var err error
	target, err = service.Settings.SaveTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	client.targetCID = "0"
	receipt, err := service.Transfer(context.Background(), target, "https://115.com/s/share-code?password=abcd", "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TargetPath != "/" || len(client.receiveCIDs) != 1 || client.receiveCIDs[0] != "0" {
		t.Fatalf("root destination was not preserved: receipt=%+v received=%v", receipt, client.receiveCIDs)
	}
}

func containsError(err error, text string) bool {
	return err != nil && strings.Contains(err.Error(), text)
}
