package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type notificationFunc func(context.Context, string, GenerateInput, *GenerationResult) error

func (f notificationFunc) NotifyShareSuccess(ctx context.Context, id string, in GenerateInput, r *GenerationResult) error {
	return f(ctx, id, in, r)
}

func TestShareSuccessNotificationOnlyAfterPersistedSuccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  *GenerationResult
		err     error
		managed bool
		want    int
	}{
		{"网页成功", &GenerationResult{Written: 2}, nil, false, 1},
		{"Bot 已有回执", &GenerationResult{Written: 2}, nil, true, 0},
		{"失败", &GenerationResult{Written: 1}, errors.New("清理失败"), false, 0},
		{"审核中", nil, p115.ErrShareUnderReview, false, 0},
		{"无新增", &GenerationResult{}, nil, false, 0},
		{"部分失败", &GenerationResult{Written: 1, Failed: 1}, nil, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) { return tc.result, tc.err }))
			notified := 0
			s.Notifier = notificationFunc(func(ctx context.Context, id string, in GenerateInput, r *GenerationResult) error {
				notified++
				history := taskHistory(t, db)
				if len(history) != 1 || history[0].ID != id || history[0].Status != "success" || in.URL != input.URL || r != tc.result || ctx.Err() != nil {
					t.Error("通知早于成功落盘或丢失结果")
				}
				return errors.New("模拟通知队列写入失败")
			})
			ctx := context.Background()
			if tc.managed {
				ctx = WithShareReplyManaged(ctx)
			}
			_, err := s.Generate(ctx, input)
			if !errors.Is(err, tc.err) || notified != tc.want {
				t.Fatal("通知改变任务结果或发送次数错误", err, notified)
			}
		})
	}
}

func TestTelegramReviewRetrySendsIndependentSuccessNotification(t *testing.T) {
	calls, notified := 0, 0
	s, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		calls++
		if calls == 1 {
			return nil, p115.ErrShareUnderReview
		}
		return &GenerationResult{Written: 1}, nil
	}))
	s.Notifier = notificationFunc(func(context.Context, string, GenerateInput, *GenerationResult) error { notified++; return nil })
	ctx := WithShareReplyManaged(WithGenerationSource(context.Background(), "Telegram"))
	if _, err := s.Generate(ctx, input); !errors.Is(err, p115.ErrShareUnderReview) {
		t.Fatal(err)
	}
	if notified != 0 {
		t.Fatal("初次审核失败不应发送成功通知")
	}
	if err := s.retryDue(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if notified != 1 || calls != 2 || taskHistory(t, db)[0].Status != "success" {
		t.Fatal("后台重试成功未发送通知")
	}
}
