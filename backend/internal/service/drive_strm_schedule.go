package service

import (
	"time"

	"strm115/backend/internal/model"
)

// Only completed whole-directory work advances periodic deadlines. Event
// scans cannot postpone reconciliation or a full scan, and zero disables one
// period without discarding accepted queues or a failed attempt's retry.
func driveSTRMDeadlines(value model.DriveSTRM, settings model.DriveSTRMSettings, now time.Time) (tree, full time.Time) {
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
	return deadline(value.LastReconcileAt, settings.ReconcileIntervalHours, settings.ReconcileEnabled), deadline(value.LastFullScanAt, settings.FullScanIntervalHours, settings.FullScanEnabled)
}

func driveSTRMDue(deadline, now time.Time) bool {
	return !deadline.IsZero() && !deadline.After(now)
}

// NextScanAt in storage is the retry boundary only. API lists project the
// earliest current deadline into their response copies without persisting it.
func driveSTRMNextScan(value model.DriveSTRM, settings model.DriveSTRMSettings, now time.Time) time.Time {
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
	tree, full := driveSTRMDeadlines(value, settings, now)
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
