package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestDriveSTRMCustomSchedulesAreIndependentOfOtherDirectoriesAndSharedSettings(t *testing.T) {
	s, db, client, mapping, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	value, _ := db.GetDriveSTRM(ctx, mapping.ID)
	value.LastReconcileAt = time.Now().Add(-7 * time.Hour)
	value.LastFullScanAt = time.Now().Add(-25 * time.Hour)
	if err := db.SaveDriveSTRM(ctx, value); err != nil {
		t.Fatal(err)
	}
	other := value
	other.ID, other.OutputDir = "other", "other-output"
	other.Schedule = model.DriveSTRMSchedule{ReconcileEnabled: true, ReconcileIntervalHours: 72, FullScanIntervalHours: 720}
	if err := db.SaveDriveSTRM(ctx, other); err != nil {
		t.Fatal(err)
	}
	custom := model.DriveSTRMSchedule{ReconcileEnabled: true, ReconcileIntervalHours: 6, FullScanIntervalHours: 720}
	calls := len(client.calls)
	if _, err := s.SaveSchedule(ctx, mapping.ID, custom); err != nil {
		t.Fatal(err)
	}
	settings, _ := s.Settings(ctx)
	settings.EventIntervalSeconds = 300
	if _, err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	listed, err := s.List(ctx)
	want := value.LastReconcileAt.Add(6 * time.Hour)
	byID := make(map[string]model.DriveSTRM, len(listed))
	for _, item := range listed {
		byID[item.ID] = item
	}
	if err != nil || len(listed) != 2 || !byID[mapping.ID].NextScanAt.Equal(want) || !byID[other.ID].NextScanAt.Equal(other.LastReconcileAt.Add(72*time.Hour)) {
		t.Fatal("自定义目录未按自身周期计算下次执行", listed, err)
	}
	stored, _ := db.GetDriveSTRM(ctx, mapping.ID)
	if stored.Queued || !stored.NextScanAt.Equal(value.NextScanAt) || len(client.calls) != calls {
		t.Fatal("保存策略触发排队、持久化展示时间或请求115", stored, len(client.calls))
	}
	attempt, _, found, err := s.next(ctx)
	if err != nil || !found || attempt.ID != mapping.ID || attempt.Mode != "tree" || attempt.Schedule != custom || attempt.Settings.EventIntervalSeconds != settings.EventIntervalSeconds {
		t.Fatal("实际调度未使用自定义周期或覆盖共享事件", attempt, found, err)
	}
	if err := s.finish(ctx, mapping.ID, model.DriveSTRMStats{}, nil); err != nil {
		t.Fatal(err)
	}
	archived := model.DriveSTRMSchedule{ReconcileIntervalHours: 24, FullScanIntervalHours: 720}
	if _, err := s.SaveSchedule(ctx, mapping.ID, archived); err != nil {
		t.Fatal(err)
	}
	value, _ = db.GetDriveSTRM(ctx, mapping.ID)
	value.LastReconcileAt = time.Now().Add(-7 * time.Hour)
	if err := db.SaveDriveSTRM(ctx, value); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := s.next(ctx); err != nil || found {
		t.Fatal("关闭目录周期后仍安排了新扫描", found, err)
	}
	listed, err = s.List(ctx)
	for _, item := range listed {
		byID[item.ID] = item
	}
	if err != nil || byID[mapping.ID].Schedule != archived || !byID[mapping.ID].NextScanAt.IsZero() || byID[other.ID].Schedule != other.Schedule {
		t.Fatal("目录关闭周期影响了其他目录", listed, err)
	}
}

func TestDriveSTRMCustomScheduleSavePreservesRunningTaskAndLifecycle(t *testing.T) {
	s, db, client, mapping, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	attempt, scanCtx, found, err := s.next(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	before, _ := db.GetDriveSTRM(ctx, mapping.ID)
	beforeSettings := attempt.Settings
	custom := model.DriveSTRMSchedule{ReconcileIntervalHours: 168, FullScanIntervalHours: 336}
	<-s.wake
	calls := len(client.calls)
	if _, err := s.SaveSchedule(ctx, mapping.ID, custom); err != nil {
		t.Fatal(err)
	}
	after, _ := db.GetDriveSTRM(ctx, mapping.ID)
	before.Schedule = custom
	if !reflect.DeepEqual(after, before) || len(s.wake) != 0 || len(client.calls) != calls || scanCtx.Err() != nil || attempt.Settings != beforeSettings || attempt.Schedule != model.DefaultDriveSTRMSchedule() {
		t.Fatal("修改策略干扰了在途任务或其设置快照", after, before)
	}
	if err := s.progress(ctx, mapping.ID, model.DriveSTRMStats{Videos: 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.finish(ctx, mapping.ID, model.DriveSTRMStats{Videos: 3}, nil); err != nil {
		t.Fatal(err)
	}
	value, _ := db.GetDriveSTRM(ctx, mapping.ID)
	value.Name = "renamed"
	if _, err := s.Save(ctx, value); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMonitoring(ctx, mapping.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMonitoring(ctx, mapping.ID, true); err != nil {
		t.Fatal(err)
	}
	value, _ = db.GetDriveSTRM(ctx, mapping.ID)
	value.OutputDir = "other-output"
	if _, err := s.Save(ctx, value); err != nil {
		t.Fatal(err)
	}
	value, _ = db.GetDriveSTRM(ctx, mapping.ID)
	if !reflect.DeepEqual(value.Schedule, custom) {
		t.Fatal("进度、完成、目录编辑或监控切换丢失策略", value.Schedule)
	}
}

func TestDriveSTRMCustomScheduleValidationAndDisabledSelection(t *testing.T) {
	s, db, _, mapping, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	valid := model.DriveSTRMSchedule{ReconcileIntervalHours: 24, FullScanIntervalHours: 720}
	if _, err := s.SaveSchedule(ctx, mapping.ID, valid); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []model.DriveSTRMSchedule{
		{ReconcileIntervalHours: 0, FullScanIntervalHours: 720},
		{ReconcileIntervalHours: -1, FullScanIntervalHours: 720},
		{ReconcileIntervalHours: 24, FullScanIntervalHours: 12},
	} {
		if _, err := s.SaveSchedule(ctx, mapping.ID, bad); !errors.Is(err, ErrGenerationInput) {
			t.Fatal("无效策略被接受", bad, err)
		}
	}
	if _, err := s.SaveSchedule(ctx, "missing", valid); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("不存在的目录未返回明确错误", err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal("关闭自动周期阻止了已接受任务", err)
	}
	stored, _ := db.GetDriveSTRM(ctx, mapping.ID)
	if stored.Schedule != valid {
		t.Fatal("无效输入替换了已保存策略", stored.Schedule)
	}
	if _, _, found, err := s.next(ctx); err != nil || found {
		t.Fatal("禁用自动周期却触发新任务", found, err)
	}
}

type driveSTRMConfigurationFailure struct{ DriveSTRMStore }

func (driveSTRMConfigurationFailure) SaveDriveSTRMConfiguration(context.Context, model.DriveSTRM, bool) error {
	return errors.New("模拟配置写入失败")
}

func TestDriveSTRMConfigurationAndScheduleSaveTogetherWithoutExtraScan(t *testing.T) {
	s, db, client, mapping, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	before, _ := db.GetDriveSTRM(ctx, mapping.ID)
	custom := model.DriveSTRMSchedule{ReconcileEnabled: true, ReconcileIntervalHours: 168, FullScanIntervalHours: 336}
	input := before
	input.Name = "周期与目录一次保存"
	calls := len(client.calls)
	saved, err := s.SaveWithSchedule(ctx, input, &custom)
	if err != nil || saved.Name != input.Name || saved.Schedule != custom || saved.Queued || saved.Status != before.Status || len(client.calls) != calls {
		t.Fatal("同根改名与策略触发扫描或未共同保存", saved, len(client.calls), err)
	}
	stored, _ := db.GetDriveSTRM(ctx, mapping.ID)
	if stored.Schedule != custom || stored.Name != input.Name || !stored.LastReconcileAt.Equal(before.LastReconcileAt) || !stored.LastFullScanAt.Equal(before.LastFullScanAt) {
		t.Fatal("目录与周期未保持同一已保存状态", stored)
	}
	input.Schedule = model.DefaultDriveSTRMSchedule()
	input.Name = "省略周期只改名"
	if _, err := s.Save(ctx, input); err != nil {
		t.Fatal(err)
	}
	stored, _ = db.GetDriveSTRM(ctx, mapping.ID)
	if stored.Schedule != custom {
		t.Fatal("旧客户端省略周期覆盖了新策略", stored.Schedule)
	}
	input.SourceCID, input.OutputDir = "300", "new-output"
	updated := model.DriveSTRMSchedule{ReconcileIntervalHours: 72, FullScanEnabled: true, FullScanIntervalHours: 168}
	if _, err := s.SaveWithSchedule(ctx, input, &updated); err != nil {
		t.Fatal(err)
	}
	stored, _ = db.GetDriveSTRM(ctx, mapping.ID)
	if stored.Schedule != updated || stored.SourceCID != "300" || !stored.Queued || !stored.FullScan {
		t.Fatal("更改来源未同事务保存周期和全量安排", stored)
	}
}

func TestDriveSTRMConfigurationScheduleFailuresLeavePreviousState(t *testing.T) {
	for _, action := range []string{"invalid-period", "invalid-source", "store-failure", "running"} {
		t.Run(action, func(t *testing.T) {
			s, db, client, mapping, _ := driveSTRMTestSetup(t)
			ctx := context.Background()
			if _, err := driveSTRMTestAttempt(t, s); err != nil {
				t.Fatal(err)
			}
			before, _ := db.GetDriveSTRM(ctx, mapping.ID)
			input := before
			input.Name = "未提交修改"
			custom := model.DriveSTRMSchedule{ReconcileIntervalHours: 168, FullScanIntervalHours: 336}
			calls := len(client.calls)
			switch action {
			case "invalid-period":
				custom.ReconcileIntervalHours = 1
			case "invalid-source":
				input.SourceCID = "999"
			case "store-failure":
				s.Store = driveSTRMConfigurationFailure{s.Store}
			case "running":
				s.active = mapping.ID
			}
			if _, err := s.SaveWithSchedule(ctx, input, &custom); err == nil {
				t.Fatal("预期失败却保存了目录与周期")
			}
			after, _ := db.GetDriveSTRM(ctx, mapping.ID)
			if !reflect.DeepEqual(after, before) {
				t.Fatal("失败保存部分修改了策略、目录或扫描状态", before, after)
			}
			if action != "invalid-source" && len(client.calls) != calls {
				t.Fatal("无效参数或本地失败请求了115", len(client.calls), calls)
			}
		})
	}
}

func TestDriveSTRMCreateAcceptsScheduleInOneConfigurationTransaction(t *testing.T) {
	s, db, _, existing, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	input := model.DriveSTRM{Name: "独立目录", AccountID: existing.AccountID, SourceCID: "300", OutputDir: "other-output", Enabled: true}
	custom := model.DriveSTRMSchedule{ReconcileIntervalHours: 168, FullScanIntervalHours: 720}
	saved, err := s.SaveWithSchedule(ctx, input, &custom)
	if err != nil || saved.Schedule != custom || !saved.Queued || !saved.FullScan {
		t.Fatal("新增目录未一次保存策略与初始任务", saved, err)
	}
	stored, err := db.GetDriveSTRM(ctx, saved.ID)
	if err != nil || stored.Schedule != custom {
		t.Fatal("新增目录策略未落库", stored, err)
	}
}
