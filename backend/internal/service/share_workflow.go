package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/strm"
)

type TransferClient interface {
	ShareLister
	ListForTransfer(context.Context, *p115.ShareInfo, string, int, string) (*p115.SharePage, error)
	CreateDirectory(context.Context, string, string, string) (string, error)
	ReceiveShare(context.Context, *p115.ShareInfo, []string, string, string) error
	DriveFiles(context.Context, string, int, string) (*p115.SharePage, error)
	SendShare(context.Context, []string, string) (*p115.ShareInfo, error)
	ConfigureShare(context.Context, *p115.ShareInfo, string, string) error
	DeleteDirectory(context.Context, string, string) error
}

type MediaGenerator interface {
	Generate(context.Context, GenerateInput) (*GenerationResult, error)
}

type ReshareJobWriter interface {
	SaveReshareJob(context.Context, model.ReshareJob) error
}

type ShareWorkflow struct {
	Client       TransferClient
	Generator    MediaGenerator
	Cookie       func() string
	Settings     *AutomationStore
	Root         string
	Jobs         ReshareJobWriter
	PollInterval time.Duration
}

func NewShareWorkflow(client TransferClient, generator MediaGenerator, cookie func() string, settings *AutomationStore, root string, jobs ReshareJobWriter) *ShareWorkflow {
	return &ShareWorkflow{Client: client, Generator: generator, Cookie: cookie, Settings: settings, Root: root, Jobs: jobs, PollInterval: 2 * time.Second}
}

func (w *ShareWorkflow) Generate(ctx context.Context, input GenerateInput) (result *GenerationResult, err error) {
	ctx = withReshareMetrics(ctx)
	settings := w.Settings.ShareValue()
	if saved, ok := ctx.Value(automationContextKey{}).(AutomationSettings); ok {
		settings = saved
	}
	switch input.Mode {
	case "direct":
		settings.ReshareEnabled = false
	case "reshare":
		settings.ReshareEnabled = true
	case "":
	default:
		return nil, generationInputError("分享模式无效")
	}
	if !settings.ReshareEnabled {
		reportGenerationWithLevel(ctx, "INFO", "generating", "转存再分享已关闭：正在读取原分享并直接生成 STRM")
		return w.Generator.Generate(ctx, input)
	}
	reportGenerationWithLevel(ctx, "INFO", "starting", "正在核对分享者 user ID 与本功能所选账号")
	if err := ValidateGenerationOptions(w.Root, input); err != nil {
		return nil, err
	}
	share, err := p115.ParseShareURL(input.URL)
	if err != nil {
		return nil, generationInputError(err.Error())
	}
	if input.Password != "" {
		share.Password = strings.TrimSpace(input.Password)
	}
	if err := strm.ValidatePassword(share.Password); err != nil {
		return nil, generationInputError(err.Error())
	}
	cookie := generationCookie(ctx, w.Cookie)
	if cookie == "" {
		return nil, generationInputError("请在分享基础设置中选择已登录的 115 账号配置")
	}
	input, err = resolveSharePath(ctx, input, measuredPages(ctx, "source:"+share.Code, func(ctx context.Context, cid string, offset int) (*p115.SharePage, error) {
		return w.Client.ListForTransfer(ctx, share, cid, offset, cookie)
	}))
	if err != nil {
		if errors.Is(err, p115.ErrShareUnderReview) {
			err = errors.Join(err, errSourceShareReview)
		}
		return nil, err
	}
	first, own, err := w.inspectSource(ctx, share, input.CID, cookie)
	if err != nil {
		return nil, err
	}
	if own {
		reportGenerationWithLevel(ctx, "INFO", "generating", "分享者与所选账号一致：直接使用原分享生成 STRM")
		return w.generateOriginal(ctx, input, cookie, first)
	}
	reportGenerationWithLevel(ctx, "INFO", "starting", "分享者与所选账号不同：转存后创建自己的长期分享并生成 STRM")
	sharePassword, err := newSharePassword(settings.FixedSharePassword)
	if err != nil {
		return nil, err
	}
	// 每批使用独立临时目录，批次并发由公共任务队列管理，接口请求仍保留限速。
	ctx, cancel := context.WithTimeout(ctx, ShareGenerationTimeout)
	defer cancel()
	reportGeneration(ctx, "scanning", "正在读取原分享的完整目录")
	source, roots, sourceFiles, err := w.scanDirectory(context.WithValue(ctx, reshareScanPhaseKey{}, "source:"+share.Code), input.CID, "正在核对原分享目录及转存审核状态", w.sourcePages(share, input.CID, cookie, first))
	if err != nil {
		if errors.Is(err, p115.ErrShareUnderReview) {
			err = errors.Join(err, errSourceShareReview)
		}
		return nil, fmt.Errorf("读取原分享: %w", err)
	}
	if len(roots) == 0 || len(roots) > 1000 {
		return nil, generationInputError("转分享需要 1 至 1000 个顶层条目，请缩小分享范围")
	}
	return w.resharePrepared(ctx, settings, input, cookie, sharePassword, source, sourceFiles, nil,
		func(ctx context.Context, cid string) error {
			return w.receiveRoots(ctx, share, roots, cid, cookie)
		}, func(ctx context.Context, own *p115.ShareInfo, _ generationSnapshot) (*GenerationResult, error) {
			input.URL, input.Password, input.CID = own.URL, own.Password, "0"
			return w.Generator.Generate(ctx, input)
		})
}

// 单链接和合并批次共用媒体数量确认、长期分享、生成及清理流程。
func (w *ShareWorkflow) resharePrepared(ctx context.Context, settings AutomationSettings, input GenerateInput, cookie, sharePassword string, source []string, sourceFiles []p115.ShareFile, selectShare func(context.Context, []p115.ShareFile, map[string][]p115.ShareFile) ([]p115.ShareFile, []string, error), receive func(context.Context, string) error, generate func(context.Context, *p115.ShareInfo, generationSnapshot) (*GenerationResult, error)) (result *GenerationResult, err error) {
	// Both single-link and batch transfers always stage under this account's
	// root, including calls carrying a previously captured settings snapshot.
	settings.ParentCID = "0"
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("创建任务编号: %w", err)
	}
	if w.Jobs == nil {
		return nil, fmt.Errorf("转存任务存储未初始化")
	}
	job := model.ReshareJob{ID: hex.EncodeToString(nonce[:]), Status: "creating", SourceURL: input.URL, SourceCID: input.CID}
	metrics := reshareMetricsFrom(ctx)
	if metrics != nil {
		job.Stats = metrics.stats
		job.Stats.SourceFiles = len(sourceFiles)
	}
	job.DirectoryName = "Cinema115-临时-" + job.ID
	logGeneration(ctx, "DEBUG", "转存任务记录编号："+job.ID)
	persist := func(status string) error {
		metrics.advance(status)
		reportGeneration(ctx, status, stageMessage(status))
		job.Status = status
		job.UpdatedAt = time.Now().UTC()
		return w.Jobs.SaveReshareJob(ctx, job)
	}
	if err := persist("creating"); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			metrics.advance("failed")
			if job.Status != "deleting" && job.Status != "deleted" && job.Status != "completed" {
				logGeneration(ctx, "WARNING", "转存流程未完成，保留临时目录："+job.DirectoryName)
			} else {
				logGeneration(ctx, "WARNING", "清理请求或完成检查点未确认，请根据转存任务记录核对网盘与回收站")
			}
			job.LastError = err.Error()
			job.Finished = true
			job.UpdatedAt = time.Now().UTC()
			// 取消请求后仍需保存最终检查点；独立的短超时仅用于本地持久化。
			saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer saveCancel()
			if saveErr := w.Jobs.SaveReshareJob(saveCtx, job); saveErr != nil {
				err = errors.Join(err, saveErr)
			}
			if result == nil {
				result = &GenerationResult{}
			}
			result.ReshareURL = job.ShareURL
			result.TempDirectoryID = job.DirectoryID
			err = fmt.Errorf("%w；任务 %s，临时目录 ID %s（请查看任务记录中的清理状态）", err, job.ID, job.DirectoryID)
		}
		if job.Stats != nil {
			requests, probes, retries := 0, 0, 0
			for _, d := range job.Stats.Directories {
				requests += d.Requests
				probes += d.Probes
				retries += d.Retries
			}
			logGeneration(ctx, "DEBUG", fmt.Sprintf("转存统计：任务 %s，核验轮数 %d，目录请求 %d，轻量探测 %d，局部重试 %d，等待 %.2f 秒；阶段耗时及目录明细已保存到任务记录", job.ID, job.Stats.ScanRounds, requests, probes, retries, float64(job.Stats.WaitMS)/1000))
			stages := make([]string, 0, len(job.Stats.StageMS))
			for stage := range job.Stats.StageMS {
				stages = append(stages, stage)
			}
			sort.Strings(stages)
			for _, stage := range stages {
				logGeneration(ctx, "DEBUG", fmt.Sprintf("转存阶段耗时：%s，%.2f 秒", stage, float64(job.Stats.StageMS[stage])/1000))
			}
			ids := make([]string, 0)
			for key, d := range job.Stats.Directories {
				if d.Phase == "transfer" {
					ids = append(ids, key)
				}
			}
			sort.Strings(ids)
			for _, key := range ids[:min(50, len(ids))] {
				d := job.Stats.Directories[key]
				logGeneration(ctx, "DEBUG", fmt.Sprintf("转存目录统计：CID %s，请求 %d，首页 %d，完整读取 %d，局部重试 %d，探测 %d", d.CID, d.Requests, d.FirstPages, d.FullReads, d.Retries, d.Probes))
			}
		}
	}()
	if err := checkGenerationAccount(ctx); err != nil {
		return nil, err
	}
	job.DirectoryID, err = w.Client.CreateDirectory(ctx, settings.ParentCID, job.DirectoryName, cookie)
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败，若请求已在 115 执行请按名称 %s 检查: %w", job.DirectoryName, err)
	}
	// 只有本次新建目录可进入清理流程，不接受配置中的父目录作为删除目标。
	if job.DirectoryID == "" || job.DirectoryID == "0" || job.DirectoryID == settings.ParentCID {
		return nil, fmt.Errorf("115 返回了不安全的临时目录 ID，已停止")
	}
	if err := persist("receiving"); err != nil {
		return nil, err
	}
	if err := receive(ctx, job.DirectoryID); err != nil {
		return nil, fmt.Errorf("转存失败，保留临时目录: %w", err)
	}
	expectedCounts := countTransferMedia(input, sourceFiles)
	metrics.advance("verifying_transfer")
	var transferred []p115.ShareFile
	var verifiedDirectories map[string][]p115.ShareFile
	waitBudget := transferWaitBudget(len(sourceFiles))
	reportGeneration(ctx, "scanning", fmt.Sprintf("开始核验转存结果：原分享 %d 个文件，本阶段最多 %.0f 秒；核验通过即继续", len(sourceFiles), waitBudget.Seconds()))
	err = w.untilBudget(ctx, waitBudget, func(waitCtx context.Context) error {
		if metrics != nil {
			metrics.stats.ScanRounds++
		}
		verifiedDirectories = make(map[string][]p115.ShareFile)
		var readErr error
		var receivedFiles []p115.ShareFile
		list := w.transferPages(measuredPages(waitCtx, "transfer", func(ctx context.Context, cid string, offset int) (*p115.SharePage, error) {
			return w.Client.DriveFiles(ctx, cid, offset, cookie)
		}), verifiedDirectories)
		_, transferred, receivedFiles, readErr = w.scanDirectory(waitCtx, job.DirectoryID, "正在统计网盘中的转存文件", list)
		if readErr != nil {
			return readErr
		}
		// 独立临时目录只接收本任务内容；按配置中的视频和元数据数量判断异步转存完成。
		return compareTransferCounts(expectedCounts, countTransferMedia(input, receivedFiles))
	})
	if err != nil {
		return nil, fmt.Errorf("等待转存失败，保留临时目录: %w", err)
	}
	if err := persist("sharing"); err != nil {
		return nil, err
	}
	// 合并批次仅分享来源目录中的原始条目，临时包装目录不进入公开分享。
	if selectShare != nil {
		transferred, source, err = selectShare(ctx, transferred, verifiedDirectories)
		if err != nil {
			return nil, err
		}
	}
	if err := checkGenerationAccount(ctx); err != nil {
		return nil, err
	}
	own, err := w.Client.SendShare(ctx, fileIDs(transferred), cookie)
	if err != nil {
		return nil, fmt.Errorf("创建分享失败，保留临时目录: %w", err)
	}
	job.ShareURL = own.URL
	if err := persist("setting_duration"); err != nil {
		return nil, err
	}
	if err := checkGenerationAccount(ctx); err != nil {
		return nil, err
	}
	configureErr := w.Client.ConfigureShare(ctx, own, sharePassword, cookie)
	// 更新成功后提取码会变化，即使后续核实失败也需记录新链接。
	job.ShareURL = own.URL
	if configureErr != nil {
		return nil, fmt.Errorf("新分享密码或长期有效设置失败，保留临时目录: %w", configureErr)
	}
	if err := persist("waiting_snapshot"); err != nil {
		return nil, err
	}
	var verified generationSnapshot
	verify := func(waitCtx context.Context) error {
		_, snapshot, err := w.captureSnapshot(waitCtx, own, "0", cookie)
		if err != nil {
			return err
		}
		if err := compareTransferCounts(expectedCounts, countTransferMedia(input, snapshot.Files)); err != nil {
			return err
		}
		verified = snapshot
		return nil
	}
	if err := w.untilBudget(ctx, waitBudget, verify); err != nil {
		return nil, fmt.Errorf("等待新分享快照失败，保留临时目录: %w", err)
	}
	if err := persist("generating"); err != nil {
		return nil, err
	}
	generationCtx := context.WithValue(ctx, generationCookieKey{}, cookie)
	generationCtx = context.WithValue(generationCtx, generationSnapshotKey{}, verified)
	result, err = generate(generationCtx, own, verified)
	if result != nil {
		result.ReshareURL = own.URL
		result.TempDirectoryID = job.DirectoryID
	}
	if err != nil {
		return result, fmt.Errorf("新分享已创建，但 STRM 生成失败，保留临时目录: %w", err)
	}
	if err := checkGenerationAccount(ctx); err != nil {
		return result, err
	}
	// Cookie 在任务内固定，避免用户切换账号后误删另一个账号的同名目录。
	if err := persist("deleting"); err != nil {
		return result, err
	}
	if err := w.Client.DeleteDirectory(ctx, job.DirectoryID, cookie); err != nil {
		return result, fmt.Errorf("STRM 已生成，但临时目录清理失败: %w", err)
	}
	result.Cleaned = true
	if err := persist("deleted"); err != nil {
		return result, err
	}
	job.Finished = true
	if err := persist("completed"); err != nil {
		return result, err
	}
	return result, nil
}

func (w *ShareWorkflow) until(ctx context.Context, check func(context.Context) error) error {
	return w.untilBudget(ctx, 90*time.Second, check)
}

func (w *ShareWorkflow) untilBudget(ctx context.Context, budget time.Duration, check func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var lastErr error
	// 保留最后一次有效校验原因，避免截止时的网络取消覆盖目录差异或重复拼接超时。
	stopped := func() error {
		cause := ctx.Err()
		message := "等待 115 处理已取消"
		if errors.Is(cause, context.DeadlineExceeded) {
			message = "等待 115 处理超时"
		}
		if lastErr != nil {
			return fmt.Errorf("%s: %w；最后一次校验: %w", message, cause, lastErr)
		}
		return fmt.Errorf("%s: %w", message, cause)
	}
	for {
		if err := ctx.Err(); err != nil {
			return stopped()
		}
		err := check(ctx)
		if err == nil {
			return nil
		}
		// 审核不是快照生成延迟，立即结束，不进入等待和后续清理。
		if errors.Is(err, p115.ErrShareUnderReview) {
			return err
		}
		if errors.Is(err, errTransferCountExceeded) {
			return err
		}
		if errors.Is(err, errTransferListingInvalid) {
			return err
		}
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			lastErr = err
		}
		if ctx.Err() != nil {
			return stopped()
		}
		reportGeneration(ctx, "waiting", fmt.Sprintf("等待 115 完成处理（本阶段最多 %.0f 秒）：%s", budget.Seconds(), err))
		if waitTransfer(ctx, w.PollInterval) != nil {
			return stopped()
		}
	}
}

func stageMessage(stage string) string {
	switch stage {
	case "creating":
		return "正在创建独立临时目录"
	case "receiving":
		return "正在转存分享文件"
	case "sharing":
		return "正在创建自己的分享链接"
	case "setting_duration":
		return "正在设置并确认分享长期有效及分享密码"
	case "waiting_snapshot":
		return "正在核对新分享中的视频及元数据数量"
	case "generating":
		return "正在复用已读取的新分享快照生成 STRM"
	case "deleting":
		return "正在将本次临时目录移入回收站"
	case "deleted":
		return "临时目录已移入回收站，正在保存完成状态"
	case "completed":
		return "生成和清理完成，新分享长期有效"
	default:
		return stage
	}
}

func compareManifest(expected, actual []string) error {
	if reflect.DeepEqual(expected, actual) {
		return nil
	}
	counts := make(map[string]int, len(actual))
	for _, item := range actual {
		counts[item]++
	}
	for _, item := range expected {
		if counts[item] == 0 {
			return fmt.Errorf("目录校验未通过：原分享 %d 项，新分享 %d 项；缺失或大小不同：%s", len(expected), len(actual), item)
		}
		counts[item]--
	}
	return fmt.Errorf("目录校验未通过：原分享 %d 项，新分享 %d 项，新分享包含额外条目", len(expected), len(actual))
}

type transferMediaCounts struct {
	Videos, Metadata int
}

var errTransferCountExceeded = errors.New("转存文件数量超过预期")

func countTransferMedia(input GenerateInput, files []p115.ShareFile) transferMediaCounts {
	videoExtensions := extensionSet(input.VideoExtensions, DefaultVideoExtensions)
	metadataExtensions := extensionSet(input.MetadataExtensions, "")
	minimumSize := input.MinSizeMB * 1024 * 1024
	var counts transferMediaCounts
	for _, file := range files {
		extension := strings.ToLower(path.Ext(file.Name))
		switch {
		case videoExtensions[extension] && file.Size >= minimumSize:
			counts.Videos++
		case metadataExtensions[extension]:
			counts.Metadata++
		}
	}
	return counts
}

func extensionSet(raw, fallback string) map[string]bool {
	if strings.TrimSpace(raw) == "" {
		raw = fallback
	}
	result := make(map[string]bool)
	for _, value := range strings.Split(strings.ReplaceAll(raw, "，", ","), ",") {
		value = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "."))
		if value != "" {
			result["."+value] = true
		}
	}
	return result
}

func compareTransferCounts(expected, actual transferMediaCounts) error {
	if actual.Videos > expected.Videos || actual.Metadata > expected.Metadata {
		return fmt.Errorf("%w：视频 %d/%d，元数据 %d/%d", errTransferCountExceeded, actual.Videos, expected.Videos, actual.Metadata, expected.Metadata)
	}
	if actual != expected {
		return fmt.Errorf("转存数量尚未完成：视频 %d/%d，元数据 %d/%d", actual.Videos, expected.Videos, actual.Metadata, expected.Metadata)
	}
	return nil
}

func fileIDs(files []p115.ShareFile) []string {
	ids := make([]string, 0, len(files))
	for _, file := range files {
		ids = append(ids, file.ID)
	}
	return ids
}

func (w *ShareWorkflow) captureSnapshot(ctx context.Context, share *p115.ShareInfo, cid, cookie string) ([]string, generationSnapshot, error) {
	return captureShareSnapshot(ctx, w.Client, share, cid, cookie)
}

func captureShareSnapshot(ctx context.Context, client ShareLister, share *p115.ShareInfo, cid, cookie string) ([]string, generationSnapshot, error) {
	if cid == "" {
		cid = "0"
	}
	snapshot := generationSnapshot{ShareCode: share.Code, RootCID: cid, Pages: make(map[generationSnapshotPageKey]*p115.SharePage)}
	manifest, roots, files, err := scanShareTree(context.WithValue(ctx, reshareScanPhaseKey{}, "snapshot"), cid, "正在读取分享目录并统计媒体数量", func(ctx context.Context, id string, offset int) (*p115.SharePage, error) {
		page, err := measuredPages(ctx, "snapshot", func(ctx context.Context, id string, offset int) (*p115.SharePage, error) {
			return client.List(ctx, share, id, offset, cookie)
		})(ctx, id, offset)
		if err != nil || page == nil {
			return page, err
		}
		copyPage := *page
		copyPage.Files = append([]p115.ShareFile(nil), page.Files...)
		snapshot.Pages[generationSnapshotPageKey{CID: id, Offset: offset}] = &copyPage
		return page, nil
	})
	if err != nil {
		return nil, generationSnapshot{}, err
	}
	snapshot.Roots = append([]p115.ShareFile(nil), roots...)
	snapshot.Files = append([]p115.ShareFile(nil), files...)
	return manifest, snapshot, nil
}

func (w *ShareWorkflow) snapshotFromCaptured(ctx context.Context, cid string, snapshot generationSnapshot) ([]string, []p115.ShareFile, error) {
	manifest, roots, _, err := w.scanDirectory(ctx, cid, "正在复用已读取的分享目录", func(_ context.Context, id string, offset int) (*p115.SharePage, error) {
		page, ok := snapshot.Pages[generationSnapshotPageKey{CID: id, Offset: offset}]
		if !ok {
			return nil, fmt.Errorf("已确认快照缺少目录页 %s/%d", id, offset)
		}
		return page, nil
	})
	return manifest, roots, err
}

func (w *ShareWorkflow) scanDirectory(ctx context.Context, cid, label string, list func(context.Context, string, int) (*p115.SharePage, error)) ([]string, []p115.ShareFile, []p115.ShareFile, error) {
	return scanShareTree(ctx, cid, label, list)
}

func scanShareTree(ctx context.Context, cid, label string, list func(context.Context, string, int) (*p115.SharePage, error)) ([]string, []p115.ShareFile, []p115.ShareFile, error) {
	if cid == "" {
		cid = "0"
	}
	entries := make([]string, 0)
	roots := make([]p115.ShareFile, 0)
	files := make([]p115.ShareFile, 0)
	visited := make(map[string]bool)
	pages := 0
	var walk func(string, string, int) error
	walk = func(id, prefix string, depth int) error {
		if depth > 20 || visited[id] {
			return fmt.Errorf("分享目录层级过深或循环")
		}
		visited[id] = true
		seen := make(map[string]bool)
		for offset := 0; ; {
			if err := ctx.Err(); err != nil {
				return err
			}
			pages++
			if pages > 500 {
				return fmt.Errorf("分享目录超过 500 页")
			}
			reportGeneration(ctx, "scanning", fmt.Sprintf("%s，已读取 %d 项", label, len(entries)))
			page, err := list(ctx, id, offset)
			if err != nil {
				return err
			}
			if page == nil || page.Count < 0 || len(page.Files) == 0 && offset < page.Count {
				return fmt.Errorf("分享分页不完整")
			}
			for _, f := range page.Files {
				if _, err := strconv.ParseUint(f.ID, 10, 64); err != nil || f.ID == "0" || seen[f.ID] {
					return fmt.Errorf("分享条目 ID 异常")
				}
				seen[f.ID] = true
				if depth == 0 {
					roots = append(roots, f)
				}
				name := prefix + "/" + strconv.Quote(f.Name)
				size := f.Size
				if f.Directory {
					size = 0
				}
				entries = append(entries, name+":"+strconv.FormatBool(f.Directory)+":"+strconv.FormatInt(size, 10))
				if len(entries) > 20000 {
					return fmt.Errorf("分享超过 20000 个条目")
				}
				if f.Directory {
					if err := walk(f.ID, name, depth+1); err != nil {
						return err
					}
				} else {
					files = append(files, f)
				}
			}
			offset += len(page.Files)
			if offset >= page.Count {
				break
			}
		}
		if phase, ok := ctx.Value(reshareScanPhaseKey{}).(string); ok {
			if d := directoryMetrics(ctx, phase, id); d != nil {
				d.FullReads++
			}
		}
		return nil
	}
	if err := walk(cid, "", 0); err != nil {
		return nil, nil, nil, err
	}
	sort.Strings(entries)
	return entries, roots, files, nil
}
