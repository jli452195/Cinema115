package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type notificationFunc func(context.Context, string, string, GenerateInput, *GenerationResult, error) error

func (f notificationFunc) NotifyShareSuccess(ctx context.Context, botID, id string, in GenerateInput, r *GenerationResult, generationErr error) error {
	return f(ctx, botID, id, in, r, generationErr)
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
		{"已有成功文件后失败", &GenerationResult{Written: 1}, errors.New("清理失败"), false, 1},
		{"审核中", nil, p115.ErrShareUnderReview, false, 0},
		{"无新增", &GenerationResult{}, nil, false, 0},
		{"部分失败", &GenerationResult{Written: 1, Failed: 1}, nil, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) { return tc.result, tc.err }))
			settings := s.Settings.Value()
			settings.TelegramEnabled, settings.BotID = true, "test-bot"
			s.Settings.value = settings
			notified := 0
			s.Notifier = notificationFunc(func(ctx context.Context, botID, id string, in GenerateInput, r *GenerationResult, generationErr error) error {
				notified++
				history := taskHistory(t, db)
				wantStatus := "success"
				if tc.err != nil {
					wantStatus = "failed"
				}
				if botID != "test-bot" || len(history) != 1 || history[0].ID != id || history[0].Status != wantStatus || in.URL != input.URL || r != tc.result || !errors.Is(generationErr, tc.err) || ctx.Err() != nil {
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
	settings := s.Settings.Value()
	settings.TelegramEnabled, settings.BotID = true, "test-bot"
	s.Settings.value = settings
	s.Notifier = notificationFunc(func(context.Context, string, string, GenerateInput, *GenerationResult, error) error {
		notified++
		return nil
	})
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
