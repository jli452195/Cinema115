package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestManualRetryBypassesScheduleAndAutomaticLimits(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "未来时间", true: "自动关闭且次数耗尽"}[disabled], func(t *testing.T) {
			calls := 0
			s, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
				calls++
				return nil, p115.ErrShareUnderReview
			}))
			value := s.Settings.Value()
			value.AccountID = "account-a"
			if err := s.Settings.Save(value); err != nil {
				t.Fatal(err)
			}
			s.AccountCookie = func(id string) string {
				if id != "account-a" {
					t.Fatalf("错误账号 %s", id)
				}
				return s.Cookie()
			}
			if _, err := s.Generate(context.Background(), input); !errors.Is(err, p115.ErrShareUnderReview) {
				t.Fatal(err)
			}
			task := taskHistory(t, db)[0]
			if disabled {
				value.ReviewRetryEnabled = false
				if err := s.Settings.Save(value); err != nil {
					t.Fatal(err)
				}
				task.Status = "review"
				task.NextRetryAt = nil
				task.Retries = task.MaxRetries
				if err := db.SaveShareTask(context.Background(), task); err != nil {
					t.Fatal(err)
				}
			}
			results, err := s.RetryTasks(context.Background(), []string{task.ID, task.ID})
			if err != nil || len(results) != 1 || !results[0].Accepted {
				t.Fatal(results, err)
			}
			again, err := s.RetryTasks(context.Background(), []string{task.ID})
			if err != nil || again[0].Accepted {
				t.Fatal("重复提交未拦截", again, err)
			}
			if n, err := db.ClearShareTasks(context.Background(), []string{task.ID}); err != nil || n != 0 {
				t.Fatal("队列任务被清理", n, err)
			}
			restarted := *s
			if err := restarted.retryDue(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			current := taskHistory(t, db)[0]
			if calls != 2 || current.ManualRetries != 1 || current.Retries != task.Retries {
				t.Fatal("重试次数错误", current, calls)
			}
			if disabled && current.NextRetryAt != nil {
				t.Fatal("禁用后仍然自动调度")
			}
			if !disabled && current.NextRetryAt == nil {
				t.Fatal("未恢复自动调度")
			}
		})
	}
}

func TestManualRetrySafetyAndAtomicClaim(t *testing.T) {
	calls := 0
	s, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		calls++
		return nil, p115.ErrShareUnderReview
	}))
	value := s.Settings.Value()
	value.AccountID = "a"
	if err := s.Settings.Save(value); err != nil {
		t.Fatal(err)
	}
	cookie := s.Cookie()
	s.AccountCookie = func(string) string { return cookie }
	_, _ = s.Generate(context.Background(), input)
	task := taskHistory(t, db)[0]
	cookie = "changed"
	if s.RetryReason(task) == "" {
		t.Fatal("凭据变化未拦截")
	}
	cookie = s.Cookie()
	unsafe := task
	unsafe.Status = "interrupted"
	if s.RetryReason(unsafe) == "" {
		t.Fatal("中断任务未拦截")
	}
	var req shareTaskRequest
	if err := json.Unmarshal(task.Request, &req); err != nil {
		t.Fatal(err)
	}
	req.Input.Mode = "reshare"
	req.RetrySafe = false
	unsafe.Status = "failed"
	unsafe.Request, _ = json.Marshal(req)
	if s.RetryReason(unsafe) == "" {
		t.Fatal("不确定转存未拦截")
	}
	var wg sync.WaitGroup
	accepted := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := s.RetryTasks(context.Background(), []string{task.ID})
			if err != nil {
				t.Error(err)
				return
			}
			accepted <- results[0].Accepted
		}()
	}
	wg.Wait()
	close(accepted)
	count := 0
	for ok := range accepted {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatal("重复抢占", count)
	}
	// 旧的自动调度快照不得覆盖手动队列。
	stale := task
	stale.Status = "running"
	if ok, err := db.CompareShareTask(context.Background(), task, stale); err != nil || ok {
		t.Fatal("自动调度覆盖队列", ok, err)
	}
	cookie = "changed-after-enqueue"
	if err := s.retryDue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || taskHistory(t, db)[0].Status != "cancelled" {
		t.Fatal("执行前没有复核凭据")
	}
}

func TestManualRetryReusesCreatedShare(t *testing.T) {
	calls := 0
	s, db, input := setupShareTasks(t, generateFunc(func(ctx context.Context, in GenerateInput) (*GenerationResult, error) {
		calls++
		if calls == 1 {
			return &GenerationResult{ReshareURL: "https://115cdn.com/s/own?password=new1"}, p115.ErrShareUnderReview
		}
		if in.Mode != "direct" || in.URL != "https://115cdn.com/s/own?password=new1" {
			t.Fatalf("重试重复转存: %+v", in)
		}
		return &GenerationResult{Written: 1}, nil
	}))
	value := s.Settings.Value()
	value.AccountID = "a"
	if err := s.Settings.Save(value); err != nil {
		t.Fatal(err)
	}
	s.AccountCookie = func(string) string { return s.Cookie() }
	input.Mode = "reshare"
	_, _ = s.Generate(context.Background(), input)
	task := taskHistory(t, db)[0]
	results, err := s.RetryTasks(context.Background(), []string{task.ID})
	if err != nil || !results[0].Accepted {
		t.Fatal(results, err)
	}
	if err := s.retryDue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	current := taskHistory(t, db)[0]
	if current.Status != "success" || current.ManualRetries != 1 || calls != 2 {
		t.Fatal(current.Status, calls)
	}
}
