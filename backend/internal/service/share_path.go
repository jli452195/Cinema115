package service

import (
	"context"
	"fmt"
	"strings"

	"strm115/backend/internal/client/p115"
)

func normalizeSharePath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || value == "/" {
		return "/", nil
	}
	if len(value) > 4096 || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\r\n\x00") {
		return "", generationInputError("分享路径须以 / 开头，使用 / 分隔目录")
	}
	parts := strings.Split(strings.TrimSuffix(value, "/"), "/")[1:]
	if len(parts) > 20 {
		return "", generationInputError("分享路径最多支持 20 层目录")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", generationInputError("分享路径不能包含空目录、. 或 ..")
		}
	}
	return "/" + strings.Join(parts, "/"), nil
}

// 路径相对于分享根目录；预览明确选定的 CID 优先。根路径不增加请求。
func resolveSharePath(ctx context.Context, input GenerateInput, list func(context.Context, string, int) (*p115.SharePage, error)) (GenerateInput, error) {
	path, err := normalizeSharePath(input.SharePath)
	if err != nil {
		return input, err
	}
	input.SharePath = ""
	if path == "/" || (input.CID != "" && input.CID != "0") {
		return input, nil
	}
	cid, pages := "0", 0
	for _, name := range strings.Split(path[1:], "/") {
		found := ""
		for offset := 0; ; {
			if err := ctx.Err(); err != nil {
				return input, err
			}
			pages++
			if pages > 500 || offset > 20000 {
				return input, generationInputError("分享路径目录过大，请缩小范围")
			}
			page, err := list(ctx, cid, offset)
			if err != nil {
				return input, fmt.Errorf("读取分享路径: %w", err)
			}
			if page == nil || page.Count < 0 || (len(page.Files) == 0 && offset < page.Count) {
				return input, fmt.Errorf("分享路径目录分页不完整")
			}
			for _, file := range page.Files {
				if file.Directory && file.Name == name {
					if found != "" {
						return input, generationInputError("分享路径存在同名目录，请在预览中选择具体目录")
					}
					found = file.ID
				}
			}
			offset += len(page.Files)
			if offset >= page.Count {
				break
			}
		}
		if found == "" {
			return input, generationInputError("分享路径不存在：" + path)
		}
		cid = found
	}
	input.CID = cid
	return input, nil
}
