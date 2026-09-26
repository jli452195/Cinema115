package service

import (
	"context"
	"fmt"
	"testing"
)

func TestEmbyProxyLogsSharedBufferAndModuleFilter(t *testing.T) {
	shared := &GenerationLogs{}
	logs := NewEmbyProxyLogs(shared)
	ctx := logs.Trace(context.Background())
	for i := 0; i < GenerationLogCapacity+20; i++ {
		logs.Add(ctx, "INFO", fmt.Sprint(i))
	}
	entries := logs.Snapshot()
	if len(entries) != GenerationLogCapacity || entries[0].Message != "2019" || entries[len(entries)-1].Message != "20" {
		t.Fatal("日志缓冲区未按容量淘汰最旧条目")
	}
	entries[0].Message = "changed"
	if logs.Snapshot()[0].Message == "changed" {
		t.Fatal("日志快照影响内部数据")
	}
	shared.Append("Emby", "index", "", "INFO", "unrelated")
	if logs.Snapshot()[0].Message != "2019" {
		t.Fatal("模块筛选混入索引日志")
	}
	all := shared.Snapshot("ALL")
	if len(all) != GenerationLogCapacity || all[0].Module != "Emby" || all[1].Module != "Emby 302" || all[0].ID == all[1].ID {
		t.Fatal("302 日志未进入统一缓冲区或 ID 冲突")
	}
}

func TestEmbyProxyStoppingDistinguishesRequestCancellation(t *testing.T) {
	lifecycle, stop := context.WithCancel(context.Background())
	defer stop()
	base := context.WithValue(lifecycle, embyProxyLifecycleKey{}, lifecycle)
	request, cancel := context.WithCancel(base)
	cancel()
	if EmbyProxyStopping(request) {
		t.Fatal("单个请求取消被误认为服务停止")
	}
	stop()
	if !EmbyProxyStopping(request) {
		t.Fatal("未识别监听服务停止")
	}
}
