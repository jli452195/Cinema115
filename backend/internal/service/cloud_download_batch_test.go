package service

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

func cloudBatchTestLinks(count int) []string {
	links := make([]string, count)
	for i := range links {
		if i%2 == 0 {
			links[i] = fmt.Sprintf("magnet:?xt=urn:btih:%040x", i+1)
		} else {
			links[i] = fmt.Sprintf("ed2k://|file|episode-%d.mkv|128|%032x|/", i+1, i+1)
		}
	}
	return links
}

func TestCloudBatchSubmitsLargeSelectionInGroupsWithOneSnapshot(t *testing.T) {
	const count = 545
	accounts, config := cloudWorkflowAccounts(t)
	links := cloudBatchTestLinks(count)
	submitted := 0
	var sizes []int
	client := &cloudWorkflowStub{
		addURLs: func(_ context.Context, cookie string, urls []string, cid string) ([]p115.CloudURLAddResult, error) {
			if cookie != "UID=123_A; CID=original" || cid != "9" || !slices.Equal(urls, links[submitted:submitted+len(urls)]) {
				t.Fatalf("提交未保持原凭据、目录或文件顺序：index=%d cid=%s", submitted, cid)
			}
			if submitted == 0 {
				account, err := accounts.Get(config.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				replacement := "UID=123_B; CID=refreshed"
				if err := accounts.Update(account.ID, account.Revision, account.Name, &replacement); err != nil {
					t.Fatal(err)
				}
			}
			results := make([]p115.CloudURLAddResult, len(urls))
			for i, url := range urls {
				resource, err := p115.ParseCloudResource(url)
				if err != nil {
					t.Fatal(err)
				}
				// 回执顺序与输入相反，并混合明确拒绝和确认成功。
				result := p115.CloudURLAddResult{URL: url, InfoHash: strings.ToUpper(resource.Hash), Rejected: submitted+i == 3}
				if submitted+i == 5 {
					result.InfoHash = ""
					result.Confirmed = true
				}
				results[len(urls)-i-1] = result
			}
			submitted += len(urls)
			sizes = append(sizes, len(urls))
			return results, nil
		},
	}
	service := &CloudDownloadService{Accounts: accounts, Client: client, Coordinator: &CloudDownloadCoordinator{}}
	invalid := slices.Clone(links)
	invalid[len(invalid)-1] = "invalid"
	if _, err := service.PrepareBatch(config, invalid); err == nil || submitted != 0 {
		t.Fatalf("尾部无效链接未在写入前整批拒绝：submitted=%d err=%v", submitted, err)
	}
	batch, err := service.PrepareBatch(config, links)
	if err != nil {
		t.Fatal(err)
	}
	config.TargetCID = "changed"
	events := make([]CloudDownloadBatchEvent, 0, 2*count+2)
	if err := batch.Run(context.Background(), func(event CloudDownloadBatchEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sizes, []int{100, 100, 100, 100, 100, 45}) || len(events) != 2*count+2 || events[0].Type != "ready" || events[0].Total != count ||
		events[len(events)-1].Type != "done" || events[len(events)-1].Stopped {
		t.Fatalf("完整选择未按组完成：sizes=%v events=%d", sizes, len(events))
	}
	for group := 0; group < count; group += CloudDownloadBatchSize {
		size := min(CloudDownloadBatchSize, count-group)
		for i := range size {
			start, result := events[2*group+1+i], events[2*group+1+size+i]
			accepted := group+i != 3
			if start.Type != "start" || result.Type != "result" || start.Index != group+i || result.Index != group+i || start.Total != count ||
				result.Total != count || result.Accepted != accepted || result.OutcomeUncertain || (result.Error == "") != accepted {
				t.Fatalf("文件回执顺序或总数错误：start=%+v result=%+v", start, result)
			}
		}
	}
}

func TestCloudBatchValidatesBeforeWriting(t *testing.T) {
	accounts, config := cloudWorkflowAccounts(t)
	service := &CloudDownloadService{Accounts: accounts, Client: &cloudWorkflowStub{}, Coordinator: &CloudDownloadCoordinator{}}
	const hash = "0123456789abcdef0123456789abcdef01234567"
	decoded, _ := hex.DecodeString(hash)
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(decoded)
	link := "magnet:?xt=urn:btih:" + hash
	for _, rows := range [][]string{nil, {link, "invalid"}, {link, ""}, {link, "magnet:?xt=urn:btih:" + b32 + "&dn=duplicate"}} {
		if _, err := service.PrepareBatch(config, rows); err == nil {
			t.Fatal("无效或重复链接未在写入前拒绝")
		}
	}
	batch, err := service.PrepareBatch(config, cloudBatchTestLinks(2))
	if err != nil {
		t.Fatal(err)
	}
	broken := errors.New("stream broken")
	if err := batch.Run(context.Background(), func(event CloudDownloadBatchEvent) error {
		if event.Type == "start" && event.Index == 1 {
			return broken
		}
		return nil
	}); !errors.Is(err, broken) {
		t.Fatal("事件发送失败后未停止提交")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := batch.Run(ctx, func(CloudDownloadBatchEvent) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("取消后未停止提交")
	}
}

func TestCloudBatchStopsAfterIncompleteOrRiskControlledSubmission(t *testing.T) {
	const count = CloudDownloadBatchSize + 3
	links := cloudBatchTestLinks(count)
	for _, tc := range []struct {
		name         string
		receipts     []p115.CloudURLAddResult
		err          error
		firstOK      bool
		gotUncertain bool
	}{
		{name: "超时", err: context.DeadlineExceeded, gotUncertain: true},
		{name: "限速", err: &p115.HTTPError{Status: http.StatusTooManyRequests}},
		{name: "整组被拒绝", err: p115.ErrCloudSubmissionRejected},
		{name: "缺少回执", receipts: []p115.CloudURLAddResult{{InfoHash: fmt.Sprintf("%040x", 1)}}, firstOK: true, gotUncertain: true},
		{name: "回执矛盾", receipts: []p115.CloudURLAddResult{{URL: links[0], InfoHash: fmt.Sprintf("%040x", 1)}, {URL: links[0], Rejected: true}}, gotUncertain: true},
		{name: "回执身份不符", receipts: []p115.CloudURLAddResult{{URL: links[0], InfoHash: strings.Repeat("f", 40), Confirmed: true}}, gotUncertain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts, config := cloudWorkflowAccounts(t)
			calls := 0
			client := &cloudWorkflowStub{
				addURLs: func(_ context.Context, _ string, urls []string, _ string) ([]p115.CloudURLAddResult, error) {
					calls++
					if !slices.Equal(urls, links[:CloudDownloadBatchSize]) {
						t.Fatal("发送了后续分组")
					}
					return tc.receipts, tc.err
				},
			}
			service := &CloudDownloadService{Accounts: accounts, Client: client, Coordinator: &CloudDownloadCoordinator{}}
			batch, err := service.PrepareBatch(config, links)
			if err != nil {
				t.Fatal(err)
			}
			var events []CloudDownloadBatchEvent
			err = batch.Run(context.Background(), func(event CloudDownloadBatchEvent) error {
				events = append(events, event)
				return nil
			})
			if err != nil || calls != 1 || len(events) != 2*CloudDownloadBatchSize+2 || !events[len(events)-1].Stopped {
				t.Fatalf("未按分组停止：calls=%d events=%d err=%v", calls, len(events), err)
			}
			for i, result := range events[CloudDownloadBatchSize+1 : len(events)-1] {
				accepted := tc.firstOK && i == 0
				if result.Type != "result" || result.Index != i || result.Accepted != accepted || result.OutcomeUncertain != (!accepted && tc.gotUncertain) {
					t.Fatalf("提交结果不符：%+v", result)
				}
			}
		})
	}
}
