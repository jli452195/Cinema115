package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"strm115/backend/internal/client/p115"
)

type shareMetadataReader interface {
	ReadMetadata(context.Context, *p115.ShareInfo, string, string) ([]byte, error)
}

func parseMetadataExtensions(raw string, videos map[string]bool) (map[string]bool, error) {
	if len(raw) > 1024 {
		return nil, generationInputError("元数据后缀配置过长")
	}
	extensions := make(map[string]bool)
	for _, part := range strings.Split(strings.ReplaceAll(raw, "，", ","), ",") {
		ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(part), "."))
		if ext == "" {
			continue
		}
		for _, r := range ext {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				return nil, generationInputError("元数据后缀只能包含英文字母和数字，以逗号分隔")
			}
		}
		if ext == "strm" || videos["."+ext] {
			return nil, generationInputError("元数据后缀不能包含 strm 或与视频后缀重复")
		}
		extensions["."+ext] = true
	}
	return extensions, nil
}

// copyMetadata 将配套文件按原后缀保存。单个文件失败不妨碍其余文件继续尝试。
func (g *STRMGenerator) copyMetadata(ctx context.Context, share *p115.ShareInfo, cookie, directory string, result *GenerationResult, reserved map[string]bool) error {
	if len(result.MetadataFiles) == 0 {
		return nil
	}
	reader, ok := g.Client.(shareMetadataReader)
	if !ok {
		result.MetadataFailed = len(result.MetadataFiles)
		return fmt.Errorf("当前分享客户端不支持读取元数据")
	}
	failures := make([]error, 0, min(len(result.MetadataFiles), 10))
	recordFailure := func(err error) {
		result.MetadataFailed++
		if len(failures) < 10 {
			failures = append(failures, err)
		}
	}
	for i := range result.MetadataFiles {
		file := &result.MetadataFiles[i]
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := reader.ReadMetadata(ctx, share, file.ID, cookie)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			recordFailure(fmt.Errorf("读取元数据 %s: %w", file.Path, err))
			continue
		}
		if file.Size > 0 && int64(len(data)) != file.Size {
			recordFailure(fmt.Errorf("元数据 %s 下载不完整", file.Path))
			continue
		}
		delete(reserved, strings.ToLower(file.Path))
		originalPath := file.Path
		actualPath, renamed, err := writeGeneratedFile(ctx, directory, file.Path, string(data), reserved)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			recordFailure(fmt.Errorf("保存元数据 %s: %w", file.Path, err))
			continue
		}
		file.Path = actualPath
		reserved[strings.ToLower(actualPath)] = true
		if renamed {
			result.ConflictRenamed++
			logGeneration(ctx, "WARNING", fmt.Sprintf("同名目标内容不同，已保留旧文件并改名：%s -> %s", originalPath, actualPath))
		}
		result.MetadataWritten++
		logGeneration(ctx, "DEBUG", "元数据保存成功："+filepath.ToSlash(filepath.Join(directory, filepath.FromSlash(actualPath))))
	}
	logGeneration(ctx, "INFO", fmt.Sprintf("元数据保存完成：%d/%d", result.MetadataWritten, len(result.MetadataFiles)))
	if omitted := result.MetadataFailed - len(failures); omitted > 0 {
		failures = append(failures, fmt.Errorf("另有 %d 个元数据失败，详见失败数量", omitted))
	}
	return errors.Join(failures...)
}
