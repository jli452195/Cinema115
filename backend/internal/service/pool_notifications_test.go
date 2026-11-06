package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type poolNotifierFunc func(context.Context, string, string, string) error

func (f poolNotifierFunc) NotifyPoolHealth(ctx context.Context, bot, key, message string) error {
	return f(ctx, bot, key, message)
}

func poolNotificationFixture(t *testing.T) (*MemberPlaybackService, P115Account, *poolNotificationFake, *time.Time) {
	t.Helper()
	s, _, _ := memberFixture(t)
	a, err := s.accounts.Get(memberTestPool(t, s))
	if err != nil {
		t.Fatal(err)
	}
	settings := s.Pool.Settings()
	settings.NotificationBotID = "bot"
	if err := s.Pool.Configure(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.Pool.now = func() time.Time { return now }
	fake := &poolNotificationFake{}
	s.PoolNotifier = fake
	return s, a, fake, &now
}

func TestPoolNotificationsSilenceTransientFailures(t *testing.T) {
	for _, fault := range []error{context.DeadlineExceeded, &p115.HTTPError{Status: 503}, p115.ErrShareThrottled} {
		t.Run(fault.Error(), func(t *testing.T) {
			s, a, fake, now := poolNotificationFixture(t)
			ctx := context.Background()
			if err := s.Pool.report(ctx, a, fault); err != nil {
				t.Fatal(err)
			}
			s.flushPoolNotifications(ctx)
			*now = now.Add(30 * time.Second)
			if err := s.Pool.report(ctx, a, nil); err != nil {
				t.Fatal(err)
			}
			s.flushPoolNotifications(ctx)
			if fake.calls != 0 {
				t.Fatal("短暂异常及未告警的恢复不应入队", fake)
			}
		})
	}
}

func TestPoolNotificationsConfirmPersistentFailureAndPairRecovery(t *testing.T) {
	s, a, fake, now := poolNotificationFixture(t)
	ctx := context.Background()
	for _, elapsed := range []time.Duration{0, 20 * time.Second} {
		*now = now.Add(elapsed)
		if err := s.Pool.report(ctx, a, context.DeadlineExceeded); err != nil {
			t.Fatal(err)
		}
		s.flushPoolNotifications(ctx)
	}
	if fake.calls != 0 {
		t.Fatal("连续失败尚未满一分钟就通知", fake)
	}
	*now = now.Add(40 * time.Second)
	s.flushPoolNotifications(ctx)
	if fake.calls != 1 || !strings.Contains(fake.messages[0], a.Name+"（"+a.ID+"）") || !strings.Contains(fake.messages[0], "网络请求失败或超时") || !strings.Contains(fake.messages[0], "异常持续约 60 秒") {
		t.Fatal("持续异常缺少账号、原因或持续时间", fake)
	}
	alertID := s.Pool.Events()[0].ID
	if err := s.Pool.report(ctx, a, p115.ErrPlaybackDirectory); err != nil {
		t.Fatal(err)
	}
	s.flushPoolNotifications(ctx)
	if fake.calls != 1 {
		t.Fatal("同一轮异常重复通知", fake)
	}
	*now = now.Add(30 * time.Second)
	if err := s.Pool.report(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	s.flushPoolNotifications(ctx)
	if fake.calls != 2 || !strings.Contains(fake.messages[1], "已恢复") || !strings.Contains(fake.messages[1], "异常持续约 90 秒") || s.Pool.Events()[2].RecoveryOf != alertID {
		t.Fatal("恢复未对应原异常", fake)
	}
	if err := s.Pool.report(ctx, a, p115.ErrShareLoginRequired); err != nil {
		t.Fatal(err)
	}
	s.flushPoolNotifications(ctx)
	if fake.calls != 3 {
		t.Fatal("新一轮异常被旧的十分钟去重吞掉", fake)
	}
}

func TestPoolNotificationThresholds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fault  error
		delay  time.Duration
		repeat bool
		want   int
	}{
		{"single-timeout", context.DeadlineExceeded, time.Minute, false, 0},
		{"login", p115.ErrShareLoginRequired, 0, false, 1},
		{"directory", p115.ErrPlaybackDirectory, 0, false, 1},
		{"short-throttle", p115.ErrShareThrottled, 59 * time.Second, false, 0},
		{"expired-throttle", p115.ErrShareThrottled, time.Minute, false, 0},
		{"persistent-throttle", p115.ErrShareThrottled, time.Minute, true, 1},
		{"global-throttle", &p115.HTTPError{Status: 429, RetryAfter: 2 * time.Minute}, time.Minute, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, a, fake, now := poolNotificationFixture(t)
			ctx := context.Background()
			if err := s.Pool.report(ctx, a, tc.fault); err != nil {
				t.Fatal(err)
			}
			*now = now.Add(tc.delay)
			if tc.repeat {
				if err := s.Pool.report(ctx, a, tc.fault); err != nil {
					t.Fatal(err)
				}
			}
			s.flushPoolNotifications(ctx)
			if fake.calls != tc.want {
				t.Fatal("通知门槛不正确", fake.calls, tc.want)
			}
		})
	}
}

func TestPoolNotificationsMergeUnqueuedEventsAndSurviveRestart(t *testing.T) {
	s, a, fake, now := poolNotificationFixture(t)
	ctx := context.Background()
	if err := s.Pool.report(ctx, a, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	if err := s.Pool.report(ctx, a, p115.ErrShareLoginRequired); err != nil {
		t.Fatal(err)
	}
	s.flushPoolNotifications(ctx)
	if fake.calls != 1 || !strings.Contains(fake.messages[0], "登录凭据失效") {
		t.Fatal("未发送的旧异常没有被最新原因替代", fake)
	}
	restarted, err := NewDynamicPool(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = s.Pool.now
	s.Pool = restarted
	if err := s.Pool.report(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	s.flushPoolNotifications(ctx)
	if fake.calls != 2 || s.Pool.Events()[2].RecoveryOf != s.Pool.Events()[1].ID {
		t.Fatal("重启后丢失恢复配对", fake)
	}
	s.flushPoolNotifications(ctx)
	if fake.calls != 2 {
		t.Fatal("重复入队恢复", fake)
	}
}

func TestPoolNotificationEnqueueCannotLoseConcurrentRecovery(t *testing.T) {
	s, a, _, _ := poolNotificationFixture(t)
	ctx := context.Background()
	if err := s.Pool.report(ctx, a, p115.ErrShareLoginRequired); err != nil {
		t.Fatal(err)
	}
	recovery := make(chan error, 1)
	s.PoolNotifier = poolNotifierFunc(func(context.Context, string, string, string) error {
		go func() { recovery <- s.Pool.report(ctx, a, nil) }()
		select {
		case err := <-recovery:
			t.Error("异常尚未确认入队，恢复就已提交", err)
			recovery <- err
		case <-time.After(30 * time.Millisecond):
		}
		return nil
	})
	s.flushPoolNotifications(ctx)
	select {
	case err := <-recovery:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("恢复提交没有完成")
	}
	events := s.Pool.Events()
	if len(events) != 2 || events[1].RecoveryOf != events[0].ID {
		t.Fatal("并发恢复丢失异常引用", events)
	}
}
