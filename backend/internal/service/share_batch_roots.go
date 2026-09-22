package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"strm115/backend/internal/client/p115"
)

// 根条目范围仅在服务内部传递，审核重试单独持久化这组新分享 ID。
type generationRootIDsKey struct{}

// 按名称、类型、大小和目录树匹配来源，使用新分享实际返回的 ID 生成播放入口。
func (w *ShareWorkflow) matchSharedRoots(ctx context.Context, item preparedShare, roots []p115.ShareFile, used map[string]bool, snapshot generationSnapshot) ([]string, error) {
	ids := make([]string, 0, len(item.roots))
	for _, original := range item.roots {
		matched := ""
		for _, candidate := range roots {
			// 115 的分享快照在“原分享”和“新分享”接口中可能返回不同的
			// size（有时新分享暂时返回 0）。名称和目录类型才是稳定身份；
			// 仅当两边都提供明确大小且确实冲突时才排除候选。
			if used[candidate.ID] || strings.TrimSpace(candidate.Name) != strings.TrimSpace(original.Name) || candidate.Directory != original.Directory ||
				!original.Directory && original.Size > 0 && candidate.Size > 0 && candidate.Size != original.Size {
				continue
			}
			if original.Directory {
				children, _, err := w.snapshotFromCaptured(ctx, candidate.ID, snapshot)
				if err != nil {
					return nil, err
				}
				expected := make([]string, 0)
				for _, entry := range item.manifest {
					// 根名称可能带有 115 返回的首尾换行/空格；目录树比较
					// 只需要比较根目录下的相对路径，不应依赖根名称字节完全一致。
					if index := strings.Index(entry[1:], "/"); index >= 0 {
						expected = append(expected, entry[index+1:])
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
