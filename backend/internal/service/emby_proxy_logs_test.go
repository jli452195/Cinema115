package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestEmbyProxyRequestSummaryAndDebug(t *testing.T) {
	for _, debug := range []bool{false, true} {
		for _, tc := range []struct {
			name, level, result, want string
		}{
			{"success", "INFO", "播放取链结果：返回 HTTP 302", "播放取链结果：返回 HTTP 302"},
			{"recovered warning", "WARNING", "下载取链结果：返回 HTTP 302", "下载取链结果：返回 HTTP 302"},
			{"failure", "ERROR", "HTTP 504：Emby 用户权限查询超时", "HTTP 504：Emby 用户权限查询超时"},
			{"cleanup failure", "ERROR", "播放取链结果：返回 HTTP 302", "HTTP 302｜共享池全局冷却持久化失败"},
			{"failed cleanup", "ERROR", "HTTP 504：Emby 用户权限查询超时", "HTTP 504：Emby 用户权限查询超时｜共享池全局冷却持久化失败"},
			{"fallback", "INFO", "播放解析结束：转交 Emby，耗时 3 ms", "播放解析结束：转交 Emby"},
		} {
			t.Run(fmt.Sprintf("%t/%s", debug, tc.name), func(t *testing.T) {
				logs := NewEmbyProxyLogs(&GenerationLogs{})
				ctx, finish := logs.BeginRequest(logs.Trace(context.Background()), debug)
				logs.Add(ctx, "INFO", "开始解析")
				if tc.name == "recovered warning" {
					logs.Add(ctx, "WARNING", "原 UID 暂不可用")
				}
				logs.Add(ctx, tc.level, tc.result)
				logs.AddDetails(ctx, "DEBUG", "后置 HTTP 诊断", ProxyLogDetails{URL: "https://cdn.115.com/video?signature=memory-only", Status: 302})
				if tc.name == "failure" {
					logs.AddDetails(ctx, "ERROR", "通用 HTTP 错误详情", ProxyLogDetails{Status: 504})
				}
				if tc.name == "cleanup failure" || tc.name == "failed cleanup" {
					logs.Add(ctx, "ERROR", "共享池全局冷却持久化失败")
				}
				finish()
				rows := logs.Snapshot()
				if len(rows) != 1 || rows[0].TaskID == "" || rows[0].Level != tc.level || !strings.Contains(rows[0].Message, tc.want) || !strings.Contains(rows[0].Message, "耗时") {
					t.Fatalf("request summary lost: %+v", rows)
				}
				if (rows[0].Details != nil) != debug {
					t.Fatalf("debug toggle ignored: %+v", rows[0])
				}
				if debug && (!strings.Contains(rows[0].Details.Steps, "开始解析") || !strings.Contains(rows[0].Details.Steps, "后置 HTTP 诊断") || !strings.Contains(rows[0].Details.Steps, "[+")) {
					t.Fatal("existing debug steps lost")
				}
			})
		}
	}
}

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
