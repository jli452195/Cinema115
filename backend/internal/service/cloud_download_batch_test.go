package service

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestCloudBatchSnapshotsAndContinuesKnownFailure(t *testing.T) {
	accounts, config := cloudWorkflowAccounts(t)
	var cookies, directories []string
	client := &cloudWorkflowStub{
		lookup: func(_ context.Context, cookie string, _ p115.CloudResource) (p115.CloudTask, bool, error) {
			cookies = append(cookies, cookie)
			return p115.CloudTask{}, false, nil
		},
		add: func(_ context.Context, cookie, raw, cid string) (json.RawMessage, error) {
			cookies = append(cookies, cookie)
			directories = append(directories, cid)
			if len(directories) == 1 {
				account, err := accounts.Get(config.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				replacement := "UID=changed"
				if err := accounts.Update(account.ID, account.Revision, account.Name, &replacement); err != nil {
					t.Fatal(err)
				}
				return nil, errors.New("明确拒绝此资源")
			}
			return json.RawMessage(`{}`), nil
		},
	}
	s := &CloudDownloadService{Accounts: accounts, Client: client}
	batch, err := s.PrepareBatch(config, []string{"magnet:?xt=urn:btih:" + strings.Repeat("a", 40), "magnet:?xt=urn:btih:" + strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	config.TargetCID = "changed"
	var events []CloudDownloadBatchEvent
	if err := batch.Run(context.Background(), func(event CloudDownloadBatchEvent) error { events = append(events, event); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 || events[2].Accepted || !events[4].Accepted || events[5].Stopped {
		t.Fatalf("events=%+v", events)
	}
	for _, cookie := range cookies {
		if cookie != "UID=test; CID=original" {
			t.Fatal("batch changed credentials")
		}
	}
	for _, cid := range directories {
		if cid != "9" {
			t.Fatal("batch changed directory")
		}
	}
}

func TestCloudBatchValidationDeduplicationAndStop(t *testing.T) {
	accounts, config := cloudWorkflowAccounts(t)
	adds := 0
	client := &cloudWorkflowStub{
		lookup: func(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error) {
			return p115.CloudTask{}, false, nil
		},
		add: func(context.Context, string, string, string) (json.RawMessage, error) {
			adds++
			return nil, context.DeadlineExceeded
		},
	}
	s := &CloudDownloadService{Accounts: accounts, Client: client}
	const hash = "0123456789abcdef0123456789abcdef01234567"
	decoded, _ := hex.DecodeString(hash)
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(decoded)
	link := "magnet:?xt=urn:btih:" + hash
	for _, rows := range [][]string{nil, {link, "invalid"}, make([]string, 101)} {
		if _, err := s.PrepareBatch(config, rows); err == nil {
			t.Fatal("invalid batch accepted")
		}
	}
	if adds != 0 {
		t.Fatal("validation wrote upstream")
	}
	batch, err := s.PrepareBatch(config, []string{link, "magnet:?xt=urn:btih:" + b32 + "&dn=duplicate", "magnet:?xt=urn:btih:" + strings.Repeat("b", 40)})
	if err == nil || adds != 0 {
		t.Fatal("duplicate batch was not rejected before writing")
	}
	batch, err = s.PrepareBatch(config, []string{link, "magnet:?xt=urn:btih:" + strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	var events []CloudDownloadBatchEvent
	if err := batch.Run(context.Background(), func(event CloudDownloadBatchEvent) error { events = append(events, event); return nil }); err != nil {
		t.Fatal(err)
	}
	if adds != 1 || events[0].Total != 2 || !events[2].OutcomeUncertain || !events[3].Stopped {
		t.Fatalf("events=%+v adds=%d", events, adds)
	}
	adds = 0
	broken := errors.New("stream broken")
	if err := batch.Run(context.Background(), func(event CloudDownloadBatchEvent) error {
		if event.Type == "start" {
			return broken
		}
		return nil
	}); !errors.Is(err, broken) || adds != 0 {
		t.Fatal("broken stream still submitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := batch.Run(ctx, func(CloudDownloadBatchEvent) error { return nil }); !errors.Is(err, context.Canceled) || adds != 0 {
		t.Fatal("canceled batch still submitted")
	}
}
