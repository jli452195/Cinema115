package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"strm115/backend/internal/model"
)

func TestED2KSTRMImportRegistersEntireLargeSelectionAndReplaysOriginalSnapshot(t *testing.T) {
	const count = 545
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	account, err := f.accounts.Primary("")
	if err != nil {
		t.Fatal(err)
	}
	links := make([]string, count)
	for i := range links {
		hash := strconv.FormatInt(int64(i+1), 16)
		hash = strings.Repeat("0", 32-len(hash)) + hash
		links[i] = ed2kSTRMTestLink("Series.S01E"+strconv.Itoa(i+1)+".mkv", hash, ed2kSTRMTestSize)
	}
	// 最后一条无效时整批不得登记，不能只校验此前允许的前几十条。
	invalid := append([]string(nil), links...)
	invalid[len(invalid)-1] = "invalid"
	if _, err := f.service.ImportTo(ctx, f.target.ID, invalid, "invalid-large-selection"); !errors.Is(err, ErrGenerationInput) {
		t.Fatalf("尾部无效链接未被拒绝：%v", err)
	}
	storedTasks, err := f.store.ListED2KSTRMTasks(ctx)
	if err != nil || len(storedTasks) != 0 || len(f.service.List()) != 0 {
		t.Fatalf("无效整批产生了部分登记：tasks=%d resources=%d err=%v", len(storedTasks), len(f.service.List()), err)
	}
	result, err := f.service.ImportTo(ctx, f.target.ID, links, "large-selection")
	if err != nil || len(result.Resources) != count || len(result.Tasks) != count {
		t.Fatalf("完整选择未被接收：resources=%d tasks=%d err=%v", len(result.Resources), len(result.Tasks), err)
	}
	storedResources, err := f.store.ListED2KSTRMResources(ctx)
	if err != nil || len(storedResources) != count {
		t.Fatalf("资源未完整持久化：resources=%d err=%v", len(storedResources), err)
	}
	storedTasks, err = f.store.ListED2KSTRMTasks(ctx)
	if err != nil || len(storedTasks) != count {
		t.Fatalf("输出任务未完整持久化：tasks=%d err=%v", len(storedTasks), err)
	}
	for _, resource := range result.Resources {
		if resource.AccountID != account.ID || resource.UID != account.UID || resource.AccountRevision != account.Revision {
			t.Fatalf("整批资源未固定账号快照：%+v", resource)
		}
	}
	ids := make(map[string]bool, count)
	for _, task := range result.Tasks {
		ids[task.ID] = true
		if task.TargetID != f.target.ID || task.OutputDir != f.target.OutputDir || task.BaseURL != "https://cinema.example" {
			t.Fatalf("整批输出目标未固定：%+v", task)
		}
	}
	if _, err := f.service.SaveSettings(ctx, model.ED2KSTRMSettings{BaseURL: "https://changed.example"}); err != nil {
		t.Fatal(err)
	}
	changed := f.target
	changed.OutputDir = "changed-output"
	if _, err := f.service.SaveTarget(ctx, changed); err != nil {
		t.Fatal(err)
	}
	replayed, err := f.service.ImportTo(ctx, f.target.ID, links, "large-selection")
	if err != nil || len(replayed.Tasks) != count || len(f.service.Tasks()) != count {
		t.Fatalf("原请求重放创建了重复或遗漏任务：tasks=%d err=%v", len(replayed.Tasks), err)
	}
	for _, task := range replayed.Tasks {
		if !ids[task.ID] || task.OutputDir != f.target.OutputDir || task.BaseURL != "https://cinema.example" {
			t.Fatalf("原请求重放未沿用已登记的输出快照：%+v", task)
		}
	}
	if len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatalf("批量登记不应访问外网：calls=%v cloud=%d", f.client.calls, f.cloud.calls)
	}
}
