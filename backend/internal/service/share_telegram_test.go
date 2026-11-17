package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func configureTelegramShare(t *testing.T, tasks *ShareTasks, input GenerateInput) AutomationSettings {
	t.Helper()
	tasks.Settings.ValidateAccount = func(id string) error {
		if id != "primary" {
			return ErrAccountMissing
		}
		return nil
	}
	tasks.Settings.ValidateBot = func(string, bool) error { return nil }
	settings := tasks.Settings.Value()
	settings.AccountID, settings.BotID, settings.TelegramEnabled = "primary", "share-bot", true
	settings.Generation = input
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	snapshot, err := tasks.Settings.TelegramShareSettings("share-bot")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestTelegramShareReadinessIsLocalAndReturnsNormalizedSnapshot(t *testing.T) {
	calls := 0
	tasks, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		calls++
		return &GenerationResult{}, nil
	}))
	want := configureTelegramShare(t, tasks, input)
	// 模拟此前保存的请求字段，不允许它们进入可复用的选择快照。
	tasks.Settings.mu.Lock()
	tasks.Settings.value.Generation.URL = "https://115.com/s/old"
	tasks.Settings.value.Generation.Password = "old1"
	tasks.Settings.value.Generation.CID = "88"
	tasks.Settings.value.Generation.Mode = "reshare"
	tasks.Settings.mu.Unlock()
	got, err := tasks.Settings.TelegramShareSettings(want.BotID)
	if err != nil || got != want {
		t.Fatalf("unexpected readiness snapshot: %+v, %v", got, err)
	}
	tasks.Settings.ValidateAccount = nil
	got, err = tasks.Settings.TelegramShareSettings(want.BotID)
	if !errors.Is(err, ErrGenerationInput) || got != want {
		t.Fatal("uninitialized account validation must reject readiness and retain the snapshot", err)
	}
	if calls != 0 || len(taskHistory(t, db)) != 0 {
		t.Fatal("local readiness created a task or invoked generation")
	}
}

func TestPrepareTelegramGenerationRejectsChangedSelection(t *testing.T) {
	for _, field := range []string{"output", "baseURL", "strategy", "bot", "disabled", "account", "invalid-output"} {
		t.Run(field, func(t *testing.T) {
			tasks, db, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
				t.Fatal("preparation invoked generation")
				return nil, nil
			}))
			snapshot := configureTelegramShare(t, tasks, input)
			tasks.Settings.mu.Lock()
			switch field {
			case "output":
				tasks.Settings.value.Generation.OutputDir = "other-output"
			case "baseURL":
				tasks.Settings.value.Generation.BaseURL = "https://other.example"
			case "strategy":
				tasks.Settings.value.ReshareEnabled = !snapshot.ReshareEnabled
			case "bot":
				tasks.Settings.value.BotID = "other-bot"
			case "disabled":
				tasks.Settings.value.TelegramEnabled = false
			case "account":
				tasks.Settings.value.AccountID = ""
			case "invalid-output":
				tasks.Settings.value.Generation.OutputDir = ""
			}
			tasks.Settings.mu.Unlock()
			if decorate, err := tasks.PrepareTelegramGeneration(snapshot); err == nil || decorate != nil {
				t.Fatal("changed selection was accepted")
			}
			if len(taskHistory(t, db)) != 0 {
				t.Fatal("failed local preparation created a task")
			}
		})
	}
}

func TestPreparedTelegramBatchPinsSettingsCookieAndCallerContext(t *testing.T) {
	cookie := "UID=42_A; SEID=prepared-private-cookie"
	original := cookie
	var snapshot AutomationSettings
	calls := 0
	tasks, db, input := setupShareTasks(t, generateFunc(func(ctx context.Context, _ GenerateInput) (*GenerationResult, error) {
		calls++
		if generationCookie(ctx, nil) != original || ctx.Value(automationContextKey{}).(AutomationSettings) != snapshot {
			t.Error("batch changed its fixed credentials or settings")
		}
		return &GenerationResult{Written: 1}, nil
	}))
	snapshot = configureTelegramShare(t, tasks, input)
	tasks.AccountCookie = func(id string) string {
		if id != snapshot.AccountID {
			t.Error("batch switched account")
		}
		return cookie
	}
	decorate, err := tasks.PrepareTelegramGeneration(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(taskHistory(t, db)) != 0 {
		t.Fatal("preparation created task records")
	}
	type traceKey struct{}
	caller, cancel := context.WithCancel(context.WithValue(context.Background(), traceKey{}, "trace"))
	ctx := decorate(caller)
	if ctx.Value(traceKey{}) != "trace" {
		t.Fatal("decorator lost caller context values")
	}
	cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("decorator lost caller cancellation")
	}
	cookie = "UID=42_B; SEID=refreshed-private-cookie"
	value := tasks.Settings.Value()
	value.Generation.OutputDir, value.ReshareEnabled = "future-output", !snapshot.ReshareEnabled
	if err := tasks.Settings.Save(value); err != nil {
		t.Fatal(err)
	}
	outcomes, err := tasks.GenerateBatch(decorate(context.Background()), []GenerateInput{input, input})
	if err != nil || len(outcomes) != 2 || calls != 2 {
		t.Fatalf("prepared batch did not finish twice: %v, %d", err, calls)
	}
	for _, task := range taskHistory(t, db) {
		var req shareTaskRequest
		if err := json.Unmarshal(task.Request, &req); err != nil {
			t.Fatal(err)
		}
		if req.Settings != snapshot || strings.Contains(string(task.Request), "SEID=") {
			t.Fatal("task did not retain settings safely")
		}
	}
}

func TestPreparedTelegramGenerationStopsWhenOriginalAccountIsRemoved(t *testing.T) {
	calls := 0
	tasks, _, input := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		calls++
		return &GenerationResult{}, nil
	}))
	snapshot := configureTelegramShare(t, tasks, input)
	cookie := tasks.Cookie()
	tasks.AccountCookie = func(string) string { return cookie }
	decorate, err := tasks.PrepareTelegramGeneration(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	cookie = ""
	if _, err := tasks.Generate(decorate(context.Background()), input); !errors.Is(err, ErrGenerationInput) || calls != 0 {
		t.Fatal("removed account reached generation", err, calls)
	}
}

func TestPreparedTelegramShareRunsKeepCookieAcrossQueue(t *testing.T) {
	cookie := "UID=42_A; SEID=fixed-before-queue"
	original := cookie
	calls := 0
	tasks, _, input := setupShareTasks(t, generateFunc(func(ctx context.Context, _ GenerateInput) (*GenerationResult, error) {
		calls++
		if generationCookie(ctx, nil) != original {
			t.Error("queue replaced prepared Bot credentials")
		}
		return &GenerationResult{Written: 1}, nil
	}))
	snapshot := configureTelegramShare(t, tasks, input)
	tasks.AccountCookie = func(string) string { return cookie }
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	runs := NewShareRuns(root, tasks)
	decorate, err := runs.PrepareTelegramGeneration(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := runs.Submit(decorate(context.Background()), strings.Repeat("c", 32), []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	cookie = "UID=42_B; SEID=login-after-queue"
	runShareWorker(t, runs, cancel)
	completed := awaitRun(t, runs, queued.ID)
	if completed.Status != "success" || calls != 1 {
		t.Fatal("prepared Bot queue did not finish once", completed.Status, calls)
	}
}
