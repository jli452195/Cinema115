package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/strm"
)

// BatchOutcome 与输入一一对应，审核链接和正常链接分别保存结果。
type BatchOutcome struct {
	Result *GenerationResult
	Err    error
	// 保留旧批次的目录范围；新批次使用根条目 ID 限定来源，避免重复生成。
	RetryCID     string
	RetryRootIDs []string
}

type BatchGenerator interface {
	GenerateBatch(context.Context, []GenerateInput) ([]BatchOutcome, error)
}

type preparedShare struct {
	index    int
	input    GenerateInput
	share    *p115.ShareInfo
	roots    []p115.ShareFile
	files    []p115.ShareFile
	manifest []string
	name     string
}

func (w *ShareWorkflow) receiveRoots(ctx context.Context, share *p115.ShareInfo, roots []p115.ShareFile, cid, cookie string) error {
	ids := fileIDs(roots)
	for start := 0; start < len(ids); start += 100 {
		if err := checkGenerationAccount(ctx); err != nil {
			return err
		}
		if err := w.Client.ReceiveShare(ctx, share, ids[start:min(start+100, len(ids))], cid, cookie); err != nil {
			return err
		}
	}
	return nil
}

// GenerateBatch 先逐条只读筛选，再把可转存的来源合并为一个长期分享。
func (w *ShareWorkflow) GenerateBatch(ctx context.Context, inputs []GenerateInput) ([]BatchOutcome, error) {
	ctx = withReshareMetrics(ctx)
	outcomes := make([]BatchOutcome, len(inputs))
	settings := w.Settings.ShareValue()
	if saved, ok := ctx.Value(automationContextKey{}).(AutomationSettings); ok {
		settings = saved
	}
	cookie := generationCookie(ctx, w.Cookie)
	if cookie == "" {
		return outcomes, generationInputError("请在分享基础设置中选择已登录的 115 账号配置")
	}
	ctx, cancel := context.WithTimeout(ctx, ShareGenerationTimeout)
	defer cancel()
	prepared := make([]preparedShare, 0, len(inputs))
	for i, input := range inputs {
		if err := ctx.Err(); err != nil {
			return outcomes, err
		}
		if err := ValidateGenerationOptions(w.Root, input); err != nil {
			outcomes[i].Err = err
			continue
		}
		share, err := p115.ParseShareURL(input.URL)
		if err != nil {
			outcomes[i].Err = generationInputError(err.Error())
			continue
		}
		if input.Password != "" {
			share.Password = strings.TrimSpace(input.Password)
		}
		if err := strm.ValidatePassword(share.Password); err != nil {
			outcomes[i].Err = generationInputError(err.Error())
			continue
		}
		if input.Mode == "direct" || input.Mode == "" && !settings.ReshareEnabled {
			outcomes[i].Result, outcomes[i].Err = w.generateOriginal(ctx, input, cookie, nil)
			continue
		}
		if input.Mode != "reshare" && input.Mode != "" {
			outcomes[i].Err = generationInputError("分享模式无效")
			continue
		}
		input, err = resolveSharePath(ctx, input, measuredPages(ctx, "source:"+share.Code, func(ctx context.Context, cid string, offset int) (*p115.SharePage, error) {
			return w.Client.ListForTransfer(ctx, share, cid, offset, cookie)
		}))
		if err != nil {
			if errors.Is(err, p115.ErrShareUnderReview) {
				err = errors.Join(err, errSourceShareReview)
			}
			outcomes[i].Err = err
			continue
		}
		first, own, err := w.inspectSource(ctx, share, input.CID, cookie)
		if err != nil {
			if errors.Is(err, p115.ErrShareUnderReview) {
				logGeneration(ctx, "INFO", fmt.Sprintf("批次第 %d 条原分享审核中，已移出合并转存，按审核重试设置处理", i+1))
			}
			outcomes[i].Err = err
			continue
		}
		if own {
			logGeneration(ctx, "INFO", fmt.Sprintf("批次第 %d 条分享属于所选账号，直接生成 STRM，不参与合并转存", i+1))
			outcomes[i].Result, outcomes[i].Err = w.generateOriginal(ctx, input, cookie, first)
			continue
		}
		manifest, roots, files, err := w.scanDirectory(context.WithValue(ctx, reshareScanPhaseKey{}, "source:"+share.Code), input.CID, fmt.Sprintf("正在检查批次原分享 %d/%d", i+1, len(inputs)), w.sourcePages(share, input.CID, cookie, first))
		if err != nil {
			if errors.Is(err, p115.ErrShareUnderReview) {
				err = errors.Join(err, errSourceShareReview)
				logGeneration(ctx, "INFO", fmt.Sprintf("批次第 %d 条原分享审核中，已移出合并转存，按审核重试设置处理", i+1))
			}
			outcomes[i].Err = fmt.Errorf("读取原分享: %w", err)
			continue
		}
		if len(roots) == 0 || len(roots) > 1000 {
			outcomes[i].Err = generationInputError("转分享需要 1 至 1000 个顶层条目，请缩小分享范围")
			continue
		}
		prepared = append(prepared, preparedShare{index: i, input: input, share: share, roots: roots, files: files, manifest: manifest, name: fmt.Sprintf("来源-%03d-%s", i+1, share.Code)})
	}
	if len(prepared) == 0 {
		return outcomes, nil
	}
	if len(prepared) > 1000 {
		return outcomes, generationInputError("单个合并分享最多包含 1000 个来源")
	}
	password, err := newSharePassword(settings.FixedSharePassword)
	if err != nil {
		return outcomes, err
	}
	manifest := make([]string, 0)
	sourceFiles := make([]p115.ShareFile, 0)
	for _, item := range prepared {
		prefix := "/" + strconv.Quote(item.name)
		manifest = append(manifest, prefix+":true:0")
		for _, entry := range item.manifest {
			manifest = append(manifest, prefix+entry)
		}
		sourceFiles = append(sourceFiles, item.files...)
	}
	if len(manifest) > 20000 {
		return outcomes, generationInputError("合并分享超过 20000 个条目，请分批发送")
	}
	sort.Strings(manifest)
	logGeneration(ctx, "INFO", fmt.Sprintf("合并 %d 个正常来源，统一转存并创建一个长期分享", len(prepared)))
	jobInput := prepared[0].input
	// 转存记录保留全部原链接，任务列表仍按来源分别保存。
	links := make([]string, 0, len(prepared))
	for _, item := range prepared {
		links = append(links, item.input.URL)
	}
	jobInput.URL = strings.Join(links, "\n")
	generationFailed := false
	combined, batchErr := w.resharePrepared(ctx, settings, jobInput, cookie, password, manifest, sourceFiles,
		func(ctx context.Context, directories []p115.ShareFile, verified map[string][]p115.ShareFile) ([]p115.ShareFile, []string, error) {
			files := make([]p115.ShareFile, 0)
			expected := make([]string, 0)
			for _, directory := range directories {
				roots, ok := verified[directory.ID]
				if !ok {
					return nil, nil, fmt.Errorf("已核验目录缺少来源 %s，停止分享", directory.ID)
				}
				files = append(files, roots...)
			}
			reportGeneration(ctx, "sharing", "复用已核验的来源目录条目，不再扫描子目录")
			if len(files) == 0 || len(files) > 1000 {
				return nil, nil, generationInputError("合并分享需要 1 至 1000 个顶层条目")
			}
			for _, item := range prepared {
				expected = append(expected, item.manifest...)
			}
			sort.Strings(expected)
			return files, expected, nil
		},
		func(ctx context.Context, parent string) error {
			seen := map[string]bool{parent: true, settings.ParentCID: true, "0": true, "": true}
			for _, item := range prepared {
				if err := checkGenerationAccount(ctx); err != nil {
					return err
				}
				cid, err := w.Client.CreateDirectory(ctx, parent, item.name, cookie)
				if err != nil {
					return fmt.Errorf("创建来源目录 %s: %w", item.name, err)
				}
				if seen[cid] {
					return fmt.Errorf("115 返回了不安全或重复的来源目录 ID")
				}
				seen[cid] = true
				if err := w.receiveRoots(ctx, item.share, item.roots, cid, cookie); err != nil {
					// 已发送转存请求后，即使上游此刻报审核也不能自动重放云端操作。
					return fmt.Errorf("来源 %d 转存失败: %w", item.index+1, err)
				}
			}
			return nil
		}, func(ctx context.Context, own *p115.ShareInfo, snapshot generationSnapshot) (*GenerationResult, error) {
			roots := snapshot.Roots
			used := make(map[string]bool, len(roots))
			total := &GenerationResult{}
			var failed error
			for _, item := range prepared {
				ids, err := w.matchSharedRoots(ctx, item, roots, used, snapshot)
				if err != nil {
					return total, err
				}
				input := item.input
				input.URL, input.Password, input.CID, input.Mode = own.URL, own.Password, "0", "direct"
				result, err := w.Generator.Generate(context.WithValue(ctx, generationRootIDsKey{}, ids), input)
				if result == nil && err == nil {
					err = errors.New("生成器未返回结果")
				}
				outcomes[item.index] = BatchOutcome{Result: result, Err: err, RetryRootIDs: ids}
				addBatchResult(total, result)
				failed = errors.Join(failed, err)
			}
			generationFailed = failed != nil
			return total, failed
		})
	for _, item := range prepared {
		out := &outcomes[item.index]
		if combined != nil {
			if out.Result == nil {
				out.Result = &GenerationResult{}
			}
			out.Result.ReshareURL, out.Result.TempDirectoryID, out.Result.Cleaned = combined.ReshareURL, combined.TempDirectoryID, combined.Cleaned
		}
		if batchErr != nil {
			if generationFailed && out.Err == nil && len(out.RetryRootIDs) > 0 {
				// 本来源已生成，其他来源失败只影响共同目录的清理，不重试成功来源。
				continue
			}
			// 合并目录只有整体成功才清理；成功生成的来源也展示批次清理失败原因。
			out.Err = errors.Join(out.Err, batchErr)
		}
	}
	return outcomes, nil
}

func addBatchResult(total, result *GenerationResult) {
	if result == nil {
		return
	}
	total.Written += result.Written
	total.Failed += result.Failed
	total.Skipped += result.Skipped
	total.MetadataWritten += result.MetadataWritten
	total.MetadataFailed += result.MetadataFailed
	total.ConflictRenamed += result.ConflictRenamed
	total.Files = append(total.Files, result.Files...)
	total.MetadataFiles = append(total.MetadataFiles, result.MetadataFiles...)
}
