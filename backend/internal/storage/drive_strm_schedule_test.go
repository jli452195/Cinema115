package storage

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestDriveSTRMScheduleUpdatesOnlyPolicyAndPersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "drive.db")
	s, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	now := time.Now().UTC().Truncate(time.Millisecond)
	value := model.DriveSTRM{ID: "mapping", AccountID: "main", SourceCID: "100", Status: "running", Queued: true, FullScan: true, PendingDirectories: []string{"200"}, FailureCount: 2, NextScanAt: now.Add(time.Minute), UpdatedAt: now, Stats: model.DriveSTRMStats{Written: 45}}
	if err := s.SaveDriveSTRM(ctx, value); err != nil {
		t.Fatal(err)
	}
	custom := model.DriveSTRMSchedule{ReconcileEnabled: true, ReconcileIntervalHours: 6, FullScanIntervalHours: 720}
	if err := s.SaveDriveSTRMSchedule(ctx, value.ID, custom); err != nil {
		t.Fatal(err)
	}
	value.Schedule = custom
	got, err := s.GetDriveSTRM(ctx, value.ID)
	if err != nil || !reflect.DeepEqual(got, value) {
		t.Fatal("保存策略覆盖了运行状态或检查点", got, value, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.GetDriveSTRM(ctx, value.ID)
	if err != nil || !reflect.DeepEqual(got, value) {
		t.Fatal("重启后策略丢失", got, err)
	}
	archived := model.DriveSTRMSchedule{ReconcileIntervalHours: 168, FullScanIntervalHours: 720}
	if err := s.SaveDriveSTRMSchedule(ctx, value.ID, archived); err != nil {
		t.Fatal(err)
	}
	value.Schedule = archived
	got, err = s.GetDriveSTRM(ctx, value.ID)
	if err != nil || !reflect.DeepEqual(got, value) {
		t.Fatal("更改目录策略覆盖了其他状态", got, err)
	}
	if err := s.SaveDriveSTRMSchedule(ctx, "missing", archived); !errors.Is(err, ErrNotFound) {
		t.Fatal("为不存在目录静默保存了策略", err)
	}
}

func TestDriveSTRMMissingScheduleUsesFixedInitialPolicy(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "drive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	value := model.DriveSTRM{ID: "mapping", AccountID: "main", SourceCID: "100", UpdatedAt: time.Now().UTC()}
	if err := s.SaveDriveSTRM(ctx, value); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE drive_strm_mappings SET record=json_remove(record,'$.schedule') WHERE id=?`, value.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDriveSTRM(ctx, value.ID)
	if err != nil || got.Schedule != model.DefaultDriveSTRMSchedule() {
		t.Fatal("缺少目录策略时未使用固定初始值", got.Schedule, err)
	}
	listed, err := s.ListDriveSTRM(ctx)
	if err != nil || len(listed) != 1 || listed[0].Schedule != model.DefaultDriveSTRMSchedule() {
		t.Fatal("单项与列表读取的初始策略不一致", listed, err)
	}
}
