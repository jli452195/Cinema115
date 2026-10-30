package service

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"strm115/backend/internal/strm"
)

func (s *DriveSTRMService) Settings(ctx context.Context) (model.DriveSTRMSettings, error) {
	value, err := s.Store.GetDriveSTRMSettings(ctx)
	if errors.Is(err, storage.ErrNotFound) {
		return value, generationInputError("请先保存基础设置")
	}
	return value, err
}

// SaveSettings changes future attempts only; it does not queue or wake scans.
func (s *DriveSTRMService) SaveSettings(ctx context.Context, value model.DriveSTRMSettings) (model.DriveSTRMSettings, error) {
	base, err := ValidateBaseURL(value.BaseURL)
	if err != nil {
		return value, err
	}
	if _, err := strm.DriveURL(base, strm.Drive{PickCode: "abcdef", FileID: "1", SHA1: strings.Repeat("A", 40), Size: 1, Name: "validate.mkv"}); err != nil {
		return value, generationInputError(err.Error())
	}
	videos, err := driveSTRMExtensions(value.VideoExtensions)
	if err != nil {
		return value, err
	}
	if len(videos) == 0 {
		return value, generationInputError("至少需要一个视频后缀")
	}
	metadata, err := parseMetadataExtensions(value.MetadataExtensions, videos)
	if err != nil {
		return value, err
	}
	if value.MinSizeMB < 0 || value.MinSizeMB > 1<<30 {
		return value, generationInputError("最小文件大小无效")
	}
	if !slices.Contains([]int{0, 20, 60, 300, 900, 1800}, value.EventIntervalSeconds) ||
		!slices.Contains([]int{0, 6, 12, 24, 72, 168}, value.ReconcileIntervalHours) ||
		!slices.Contains([]int{0, 24, 72, 168, 336, 720}, value.FullScanIntervalHours) {
		return value, generationInputError("请选择有效的自动监控周期")
	}
	if value.MetadataFilesPerSecond < 1 || value.MetadataFilesPerSecond > 10 {
		return value, generationInputError("附件下载频率应为每秒 1 到 10 个文件")
	}
	normalize := func(extensions map[string]bool) string {
		values := make([]string, 0, len(extensions))
		for ext := range extensions {
			values = append(values, strings.TrimPrefix(ext, "."))
		}
		sort.Strings(values)
		return strings.Join(values, ",")
	}
	value.BaseURL = base
	value.VideoExtensions, value.MetadataExtensions = normalize(videos), normalize(metadata)
	return value, s.Store.SaveDriveSTRMSettings(ctx, value)
}
