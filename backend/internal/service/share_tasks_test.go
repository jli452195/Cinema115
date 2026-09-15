package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func setupShareTasks(t *testing.T, gen MediaGenerator) (*ShareTasks, *storage.Store, GenerateInput) {
	t.Helper()
	root := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	settings, err := NewAutomationStore(filepath.Join(root, "automation.json"), root, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Value()
	value.ReviewRetryEnabled = true
	value.ReviewRetryMax = 2
	if err := settings.Save(value); err != nil {
		t.Fatal(err)
	}
	return &ShareTasks{Store: db, Generator: gen, Settings: settings, Cookie: func() string { return "UID=42_A; SEID=private-cookie" }}, db, generationOptions(t)
}

func taskHistory(t *testing.T, db *storage.Store) []model.ShareTask {
	t.Helper()
	tasks, err := db.ListShareTasks(context.Background(), "", "", "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func TestReviewRetryLimitAndSuccess(t *testing.T) {
	for _, passAt := range []int{2, 99} {
		name := "审核通过后完成"
		if passAt == 99 {
			name = "达到上限停止"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			s, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
				calls++
				if calls < passAt {
					return nil, p115.ErrShareUnderReview
				}
				return &GenerationResult{Written: 2, Files: make([]GeneratedSTRM, 2)}, nil
			}))
			if _, err := s.Generate(context.Background(), input); !errors.Is(err, p115.ErrShareUnderReview) {
				t.Fatal(err)
			}
			task := taskHistory(t, db)[0]
			if task.Status != "waiting_review" || task.NextRetryAt == nil || task.Retries != 0 || strings.Contains(string(task.Request), "UID=42_A; SEID=private-cookie") {
				t.Fatal("等待状态或凭证存储错误")
			}
			// 模拟重新装配调度器，不依赖原请求或内存队列。
			restarted := *s
			for i := 0; i < 4; i++ {
				if err := restarted.retryDue(context.Background(), time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			task = taskHistory(t, db)[0]
			if passAt == 2 {
				if calls != 2 || task.Status != "success" || task.Retries != 1 || task.Written != 2 || task.NextRetryAt != nil {
					t.Fatal("审核通过未正确完成", task.Status, calls)
				}
			} else if calls != 3 || task.Status != "review" || task.Retries != 2 || task.NextRetryAt != nil || !strings.Contains(task.Error, "次数已用尽") {
				t.Fatal("超过最大重试次数", calls, task.Status)
			}
		})
	}
}

func TestSourceReviewSchedulesWithoutCloudMutation(t *testing.T) {
	fake := &transferFake{reviewStage: "source"}
	workflow, input := setupWorkflow(t, fake)
	s, db, _ := setupShareTasks(t, workflow)
	s.Cookie = workflow.Cookie
	s.Settings = workflow.Settings
	settings := s.Settings.Value()
	settings.ReviewRetryEnabled = true
	if err := s.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	input.Mode = "reshare"
	if _, err := s.Generate(context.Background(), input); !errors.Is(err, p115.ErrShareUnderReview) {
		t.Fatal(err)
	}
	if taskHistory(t, db)[0].Status != "waiting_review" || len(fake.calls) != 0 {
		t.Fatal("原分享审核未安排重试，或提前执行了云端写入")
	}
}

func TestReviewRetryReusesOwnShare(t *testing.T) {
	calls := 0
	s, db, input := setupShareTasks(t, generateFunc(func(_ context.Context, in GenerateInput) (*GenerationResult, error) {
		calls++
		if calls == 1 {
			return &GenerationResult{ReshareURL: "https://115cdn.com/s/own?password=new1", TempDirectoryID: "123"}, p115.ErrShareUnderReview
		}
		if in.Mode != "direct" || in.URL != "https://115cdn.com/s/own?password=new1" || in.Password != "" || in.CID != "0" {
			t.Fatal("重试没有复用新分享，可能重复转存")
		}
		return &GenerationResult{Written: 1, Files: make([]GeneratedSTRM, 1)}, nil
	}))
	input.Mode = "reshare"
	_, _ = s.Generate(context.Background(), input)
	if err := s.retryDue(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	task := taskHistory(t, db)[0]
	if calls != 2 || task.Status != "success" || task.ShareURL == "" || task.Note == "" || task.Mode != "reshare" {
		t.Fatal("新分享或目录保留提示丢失")
	}
}

func TestReviewRetryStopsForOtherFailuresAndSettings(t *testing.T) {
	for _, kind := range []string{"http405", "unsafe-transfer", "disabled", "cookie-change"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			s, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
				calls++
				if kind == "http405" {
					return nil, errors.New("HTTP 405")
				}
				return nil, p115.ErrShareUnderReview
			}))
			if kind == "unsafe-transfer" {
				input.Mode = "reshare"
			}
			_, _ = s.Generate(context.Background(), input)
			if kind == "disabled" {
				settings := s.Settings.Value()
				settings.ReviewRetryEnabled = false
				if err := s.Settings.Save(settings); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "cookie-change" {
				s.Cookie = func() string { return "different-cookie" }
			}
			if err := s.retryDue(context.Background(), time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			task := taskHistory(t, db)[0]
			if calls != 1 || task.NextRetryAt != nil || task.Status == "waiting_review" {
				t.Fatal("不应自动重新执行", kind)
			}
		})
	}
}

type failFinalTaskStore struct {
	ShareTaskStore
	writes int
}

func (s *failFinalTaskStore) SaveShareTask(ctx context.Context, t model.ShareTask) error {
	s.writes++
	if s.writes > 1 {
		return errors.New("磁盘已满")
	}
	return s.ShareTaskStore.SaveShareTask(ctx, t)
}

func TestShareTaskPersistenceFailureDoesNotScheduleRetry(t *testing.T) {
	s, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) { return nil, p115.ErrShareUnderReview }))
	s.Store = &failFinalTaskStore{ShareTaskStore: s.Store}
	_, err := s.Generate(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "磁盘已满") {
		t.Fatal("没有报告持久化失败")
	}
	if err := db.InterruptShareTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	task := taskHistory(t, db)[0]
	if task.Status != "interrupted" {
		t.Fatal("中断任务被自动重放")
	}
	due, err := db.DueShareTasks(context.Background(), time.Now().Add(time.Hour))
	if err != nil || len(due) != 0 {
		t.Fatal("未保存的重试不应运行")
	}
}
