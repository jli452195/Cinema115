package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"strm115/backend/internal/client/p115"
)

// 根条目范围仅在服务内部传递，审核重试单独持久化这组新分享 ID。
type generationRootIDsKey struct{}

// 按名称、类型、大小和目录树匹配来源，使用新分享实际返回的 ID 生成播放入口。
func (w *ShareWorkflow) matchSharedRoots(ctx context.Context, own *p115.ShareInfo, cookie string, item preparedShare, roots []p115.ShareFile, used map[string]bool) ([]string, error) {
	ids := make([]string, 0, len(item.roots))
	for _, original := range item.roots {
		matched := ""
		for _, candidate := range roots {
			if used[candidate.ID] || candidate.Name != original.Name || candidate.Directory != original.Directory || !original.Directory && candidate.Size != original.Size {
				continue
			}
			if original.Directory {
				children, _, err := w.snapshot(ctx, own, candidate.ID, cookie)
				if err != nil {
					return nil, err
				}
				prefix := "/" + strconv.Quote(original.Name)
				expected := make([]string, 0)
				for _, entry := range item.manifest {
					if strings.HasPrefix(entry, prefix+"/") {
						expected = append(expected, strings.TrimPrefix(entry, prefix))
					}
				}
				sort.Strings(expected)
				if compareManifest(expected, children) != nil {
					continue
				}
			}
			matched = candidate.ID
			break
		}
		if matched == "" {
			return nil, fmt.Errorf("新分享无法对应原始条目：%s", original.Name)
		}
		used[matched] = true
		ids = append(ids, matched)
	}
	return ids, nil
}
