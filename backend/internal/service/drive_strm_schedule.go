package service

import (
	"context"
	"slices"
	"time"

	"strm115/backend/internal/model"
)

// SaveSchedule 只更新策略，不触碰任务状态、检查点、停止时间或已固定的扫描快照。
func (s *DriveSTRMService) SaveSchedule(ctx context.Context, id string, schedule model.DriveSTRMSchedule) (model.DriveSTRM, error) {
	if err := validateDriveSTRMSchedule(schedule); err != nil {
		return model.DriveSTRM{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Store.SaveDriveSTRMSchedule(ctx, id, schedule); err != nil {
		return model.DriveSTRM{}, err
	}
	return s.Store.GetDriveSTRM(ctx, id)
}

func validateDriveSTRMSchedule(schedule model.DriveSTRMSchedule) error {
	if !slices.Contains([]int{6, 12, 24, 72, 168}, schedule.ReconcileIntervalHours) ||
		!slices.Contains([]int{24, 72, 168, 336, 720}, schedule.FullScanIntervalHours) {
		return generationInputError("请选择有效的目录自动任务周期")
	}
	return nil
}

// 只有完成整个目录任务才推进周期检查点，生活事件不能推迟目录自身的补漏周期。
func driveSTRMDeadlines(value model.DriveSTRM, now time.Time) (tree, full time.Time) {
	if !value.Enabled {
		return tree, full
	}
	deadline := func(last time.Time, hours int, enabled bool) time.Time {
		if !enabled || hours == 0 {
			return time.Time{}
		}
		// Stopping an attempt leaves successful checkpoints intact, while
		// allowing a new periodic attempt only after its configured interval.
		if value.Status == "stopped" && value.UpdatedAt.After(last) {
			last = value.UpdatedAt
		}
		if last.IsZero() {
			return now
		}
		return last.Add(time.Duration(hours) * time.Hour)
	}
	return deadline(value.LastReconcileAt, value.Schedule.ReconcileIntervalHours, value.Schedule.ReconcileEnabled), deadline(value.LastFullScanAt, value.Schedule.FullScanIntervalHours, value.Schedule.FullScanEnabled)
}

func driveSTRMDue(deadline, now time.Time) bool {
	return !deadline.IsZero() && !deadline.After(now)
}

// NextScanAt in storage is the retry boundary only. API lists project the
// earliest current deadline into their response copies without persisting it.
func driveSTRMNextScan(value model.DriveSTRM, now time.Time) time.Time {
	if value.Status == "running" {
		return time.Time{}
	}
	if value.FailureCount > 0 {
		if value.Enabled {
			return value.NextScanAt
		}
		return time.Time{}
	}
	if value.Queued {
		return now
	}
	tree, full := driveSTRMDeadlines(value, now)
	if tree.IsZero() || !full.IsZero() && full.Before(tree) {
		return full
	}
	return tree
}

func driveSTRMRetryDelay(failures int) time.Duration {
	switch {
	case failures <= 1:
		return time.Minute
	case failures == 2:
		return 5 * time.Minute
	case failures == 3:
		return 30 * time.Minute
	default:
		return 24 * time.Hour
	}
}
