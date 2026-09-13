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

// copyMetadata 将配套文件按原后缀保存，失败时阻止转存流程继续清理网盘源文件。
func (g *STRMGenerator) copyMetadata(ctx context.Context, share *p115.ShareInfo, cookie, directory string, result *GenerationResult) error {
	if len(result.MetadataFiles) == 0 {
		return nil
	}
	reader, ok := g.Client.(shareMetadataReader)
	if !ok {
		return fmt.Errorf("当前分享客户端不支持读取元数据")
	}
	recordFailure := func(err error) error {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			result.MetadataFailed++
		}
		return err
	}
	for _, file := range result.MetadataFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := reader.ReadMetadata(ctx, share, file.ID, cookie)
		if err != nil {
			return recordFailure(fmt.Errorf("读取元数据 %s: %w", file.Path, err))
		}
		if file.Size > 0 && int64(len(data)) != file.Size {
			return recordFailure(fmt.Errorf("元数据 %s 下载不完整，已停止生成", file.Path))
		}
		destination := filepath.Join(directory, filepath.FromSlash(file.Path))
		if err := ensureSTRMDirectory(filepath.Dir(destination)); err != nil {
			return recordFailure(fmt.Errorf("创建元数据目录: %w", err))
		}
		// 复用同目录临时文件与原子替换，内容不转码、不写入下载地址。
		if err := writeSTRM(ctx, destination, string(data)); err != nil {
			return recordFailure(fmt.Errorf("保存元数据 %s: %w", file.Path, err))
		}
		result.MetadataWritten++
		logGeneration(ctx, "DEBUG", "元数据保存成功："+filepath.ToSlash(destination))
	}
	logGeneration(ctx, "INFO", fmt.Sprintf("元数据保存完成：%d/%d", result.MetadataWritten, len(result.MetadataFiles)))
	return nil
}
