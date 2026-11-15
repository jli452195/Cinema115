package service

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"strm115/backend/internal/client/tguser"
)

type relaySelectionMissing struct{ *relayTransportFake }

func (relaySelectionMissing) RunRelay(context.Context, tguser.RelayStateStore, func(context.Context, int64, *tg.Message) error, func(int64, string)) error {
	return &tguser.OperationError{Code: "selection_required", Message: "转发频道资料缺失，请编辑规则重新选择来源和目标"}
}

func TestTelegramRelaySelectionStatusRecoversAfterReconnect(t *testing.T) {
	relay, _, transport := relayFixture(t)
	saveRelayRule(t, relay, relayRuleInput("channel:100", "user:200"))
	relay.transport = relaySelectionMissing{transport}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- relay.watch(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		view, err := relay.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status.Error != "" {
			if view.Status.Error != "转发频道资料缺失，请编辑规则重新选择来源和目标" || view.Status.Connected {
				t.Fatalf("incorrect missing-selection status: %+v", view.Status)
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("watcher did not report missing selection")
		case <-tick.C:
		}
	}
	if err := relay.receive(ctx, 42, nil); err != nil {
		t.Fatal(err)
	}
	view, err := relay.List(ctx)
	if err != nil || view.Status.Error != "" {
		t.Fatalf("reconnection kept obsolete error: %+v, %v", view.Status, err)
	}
}

func TestTelegramRelayReselectingSamePeersRefreshesSavedMetadata(t *testing.T) {
	relay, _, transport := relayFixture(t)
	input := relayRuleInput("channel:100", "user:200")
	rule := saveRelayRule(t, relay, input)
	input.ID = rule.ID
	input.SourceSelectionToken, input.TargetSelectionToken = "source-picked", "target-picked"
	updated := saveRelayRule(t, relay, input)
	if transport.prepareCalls != 2 || updated.ID != rule.ID || updated.OwnerID != rule.OwnerID || !updated.StartedAt.Equal(rule.StartedAt) {
		t.Fatalf("reselecting existing peers did not refresh metadata on the original rule: %+v", updated)
	}
}

type relayGapTransport struct {
	*relayTransportFake
	warning string
	ready   chan struct{}
}

func (f relayGapTransport) RunRelay(ctx context.Context, _ tguser.RelayStateStore, receive func(context.Context, int64, *tg.Message) error, report func(int64, string)) error {
	f.mu.Lock()
	f.connected = true
	owner := f.owner
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.connected = false; f.mu.Unlock() }()
	// 启动恢复可能先报告缺口，再收到就绪事件和普通消息。
	report(owner, f.warning)
	for _, message := range []*tg.Message{nil, relayMessage(1, "普通频道消息"), nil} {
		if err := receive(ctx, owner, message); err != nil {
			return err
		}
		report(owner, f.warning)
	}
	select {
	case f.ready <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestTelegramRelayGapWarningKeepsListenerConnected(t *testing.T) {
	relay, _, transport := relayFixture(t)
	relay.logs = &GenerationLogs{}
	saveRelayRule(t, relay, relayRuleInput("channel:100", "user:200"))
	// 清除保存规则留下的唤醒，只验证本次监听自身不会因缺口重启。
	<-relay.wakeWatch
	const warning = "频道积压超出可恢复窗口，已处理可获取的新消息；更早的消息可能缺失"
	ready := make(chan struct{}, 1)
	relay.transport = relayGapTransport{transport, warning, ready}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- relay.watch(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("缺口恢复后监听未保持运行")
	}
	view, err := relay.List(ctx)
	if err != nil || !view.Status.Connected || view.Status.Error != "" || view.Status.Warning != warning {
		t.Fatalf("缺口被误判成断线或正常事件清除了提示：%+v, %v", view.Status, err)
	}
	entries := relay.logs.Snapshot("WARNING", "Telegram")
	if len(entries) != 1 || entries[0].Message != warning {
		t.Fatalf("相同缺口反复写入警告日志：%+v", entries)
	}
}

func TestTelegramRelayGapWarningAndErrorHaveIndependentLifetimes(t *testing.T) {
	relay, _, _ := relayFixture(t)
	relay.logs = &GenerationLogs{}
	ctx := context.Background()
	const warning = "历史频道消息可能缺失"
	relay.lastError = "等待重新连接"
	relay.reportWarning(42, warning)
	view, err := relay.List(ctx)
	if err != nil || view.Status.Warning != warning || view.Status.Error != "等待重新连接" {
		t.Fatalf("历史提示覆盖了连接错误：%+v, %v", view.Status, err)
	}
	if err := relay.receive(ctx, 42, nil); err != nil {
		t.Fatal(err)
	}
	view, err = relay.List(ctx)
	if err != nil || view.Status.Warning != warning || view.Status.Error != "" {
		t.Fatalf("重连未独立清除连接错误：%+v, %v", view.Status, err)
	}
	if err := relay.receive(ctx, 99, nil); err != nil {
		t.Fatal(err)
	}
	view, err = relay.List(ctx)
	if err != nil || view.Status.Warning != "" {
		t.Fatalf("换号后遗留旧账号缺口提示：%+v, %v", view.Status, err)
	}
	// 新账号的缺口也可能早于就绪事件报告，不得被随后就绪清掉。
	relay.reportWarning(99, warning)
	if err := relay.receive(ctx, 99, nil); err != nil {
		t.Fatal(err)
	}
	relay.reportWarning(99, warning)
	view, err = relay.List(ctx)
	if err != nil || view.Status.Warning != warning {
		t.Fatalf("新账号就绪事件清除了本账号提示：%+v, %v", view.Status, err)
	}
	if entries := relay.logs.Snapshot("WARNING", "Telegram"); len(entries) != 2 {
		t.Fatalf("缺口日志没有按账号独立去重：%+v", entries)
	}
}

func TestTelegramRelayVerifiedAccountClearsOnlyPreviousOwnerWarning(t *testing.T) {
	relay, _, _ := relayFixture(t)
	relay.logs = &GenerationLogs{}
	ctx := context.Background()
	const warning = "历史频道消息可能缺失"
	relay.reportWarning(42, warning)
	// 同账号重新验证不清历史提示，也不额外写日志。
	relay.reportWarning(42, "")
	view, err := relay.List(ctx)
	if err != nil || view.Status.Warning != warning {
		t.Fatalf("同账号验证清除了缺口提示：%+v, %v", view.Status, err)
	}
	// 新账号已验证但频道资料尚未确认时，也不能继续显示旧账号提示。
	relay.reportWarning(99, "")
	view, err = relay.List(ctx)
	if err != nil || view.Status.Warning != "" || relay.ownerID != 99 {
		t.Fatalf("新账号验证后仍保留旧账号缺口提示：%+v, %v", view.Status, err)
	}
	entries := relay.logs.Snapshot("WARNING", "Telegram")
	if len(entries) != 1 || entries[0].Message != warning {
		t.Fatalf("身份通知生成了额外缺口日志：%+v", entries)
	}
}
