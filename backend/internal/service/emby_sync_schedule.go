package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

// EmbySyncSettings 只控制本地索引校准，不触发 Emby 扫描文件或刮削。
type EmbySyncSettings struct {
	Enabled      bool   `json:"enabled"`
	IntervalDays int    `json:"intervalDays"`
	Time         string `json:"time"`
}

var embySyncLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func embySyncSettings(c EmbyConfig) EmbySyncSettings {
	if c.SyncSettings == nil {
		return EmbySyncSettings{Enabled: true, IntervalDays: 7, Time: "03:00"}
	}
	return *c.SyncSettings
}

func validateEmbySyncSettings(settings EmbySyncSettings) error {
	if settings.IntervalDays < 1 || settings.IntervalDays > 30 {
		return fmt.Errorf("全量校准周期须为 1 至 30 天")
	}
	if _, err := time.Parse("15:04", settings.Time); err != nil || len(settings.Time) != 5 {
		return fmt.Errorf("校准时间须为 HH:MM 格式的北京时间")
	}
	return nil
}

// SaveSyncSettings 独立保存计划，不连接 Emby，也不覆盖连接或其他功能的配置。
func (s *EmbyService) SaveSyncSettings(ctx context.Context, settings EmbySyncSettings) (resultErr error) {
	ctx, finish := s.startOperation(ctx, "保存 Emby 自动校准设置")
	defer func() { finish(resultErr) }()
	if err := validateEmbySyncSettings(settings); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("保存自动校准设置取消: %w", err)
	}
	c := s.config
	previous := embySyncSettings(c)
	c.SyncSettings = &settings
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("编码自动校准设置: %w", err)
	}
	if err := s.secrets.Save(string(data)); err != nil {
		return fmt.Errorf("保存自动校准设置: %w", err)
	}
	s.config = c
	if previous.IntervalDays != settings.IntervalDays || previous.Time != settings.Time {
		// 短时退避保持原计划；重试耗尽后等待的下一轮按新设置重新安排。
		next := embySyncAfter(time.Now(), settings)
		if err := s.db.EmbyRescheduleSyncRetry(ctx, embyScope(c), next.Format(time.RFC3339)); err != nil {
			return fmt.Errorf("设置已保存，但更新下一轮校准时间失败: %w", err)
		}
	}
	return nil
}

// 下一个周期按北京时间的日历天计算，不依赖主机或容器的时区。
func embySyncAfter(base time.Time, settings EmbySyncSettings) time.Time {
	clock, err := time.Parse("15:04", settings.Time)
	if err != nil || settings.IntervalDays < 1 {
		return time.Time{}
	}
	date := base.In(embySyncLocation).AddDate(0, 0, settings.IntervalDays)
	return time.Date(date.Year(), date.Month(), date.Day(), clock.Hour(), clock.Minute(), 0, 0, embySyncLocation)
}

// 每轮最多尝试三次，失败后分别等待 30、60 分钟，再等待下一个校准周期。
func embySyncRetry(now time.Time, settings EmbySyncSettings, count int) (int, time.Time) {
	count++
	if count < 3 {
		return count, now.Add(time.Duration(count) * 30 * time.Minute)
	}
	return 3, embySyncAfter(now, settings)
}

func embyNextSyncAt(c EmbyConfig, state model.EmbyState, now time.Time) string {
	settings := embySyncSettings(c)
	if !settings.Enabled || c.APIKey == "" || len(c.LibraryIDs) == 0 || state.Status == "queued" || state.Status == "running" {
		return ""
	}
	if state.Status == "failed" {
		if state.SyncRetryAt != "" {
			return state.SyncRetryAt
		}
		_, retryAt := embySyncRetry(now, settings, state.SyncRetryCount)
		return retryAt.Format(time.RFC3339)
	}
	if state.Active == "" {
		return ""
	}
	last, err := time.Parse(time.RFC3339, state.SyncedAt)
	if err != nil {
		return ""
	}
	next := embySyncAfter(last, settings)
	if next.IsZero() {
		return ""
	}
	return next.Format(time.RFC3339)
}

// 复用已有后台循环；读锁阻止旧范围或旧设置在保存后继续自动入队。
func (s *EmbyService) queueDueSync(ctx context.Context, c EmbyConfig, state model.EmbyState, now time.Time) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if embyScope(c) != embyScope(s.config) || state.Status == "queued" || state.Status == "running" {
		return false, nil
	}
	c = s.config
	scope := embyScope(c)
	if state.Status == "idle" && state.Active == "" {
		// 首次配置始终初始化索引，定时开关只控制后续周期。
		if err := s.db.EmbyQueueSync(ctx, scope); err != nil {
			return false, err
		}
		return true, nil
	}
	settings := embySyncSettings(c)
	if !settings.Enabled {
		return false, nil
	}
	if state.Status == "failed" && state.SyncRetryAt == "" {
		count, retryAt := embySyncRetry(now, settings, state.SyncRetryCount)
		if err := s.db.EmbyRecordSyncRetry(ctx, scope, count, retryAt.Format(time.RFC3339)); err != nil {
			return false, err
		}
		logEmby(s.logContext(ctx, c, "EMBY-SCHEDULE", "自动校准"), "WARNING", "全量校准未完成，下次自动尝试："+retryAt.In(embySyncLocation).Format("2006-01-02 15:04")+"（北京时间）")
		return false, nil
	}
	next, err := time.Parse(time.RFC3339, embyNextSyncAt(c, state, now))
	if err != nil || now.Before(next) {
		return false, nil
	}
	resetRetry := state.Status != "failed" || state.SyncRetryCount >= 3
	if err := s.db.EmbyQueueScheduledSync(ctx, scope, resetRetry); err != nil {
		return false, err
	}
	logEmby(s.logContext(ctx, c, "EMBY-SCHEDULE", "自动校准"), "INFO", "全量校准已按计划加入后台队列")
	return true, nil
}
