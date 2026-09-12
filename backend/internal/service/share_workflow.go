package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

type TransferClient interface {
	ShareLister
	CreateDirectory(context.Context, string, string, string) (string, error)
	ReceiveShare(context.Context, *p115.ShareInfo, []string, string, string) error
	DriveFiles(context.Context, string, int, string) (*p115.SharePage, error)
	SendShare(context.Context, []string, string) (*p115.ShareInfo, error)
	SetSharePermanent(context.Context, *p115.ShareInfo, string) error
	DeleteDirectory(context.Context, string, string) error
}

type MediaGenerator interface {
	Generate(context.Context, GenerateInput) (*GenerationResult, error)
}

type ShareWorkflow struct {
	Client           TransferClient
	Generator        MediaGenerator
	Cookie           func() string
	Settings         *AutomationStore
	Root, JournalDir string
	PollInterval     time.Duration
	gate             chan struct{}
}

func NewShareWorkflow(client TransferClient, generator MediaGenerator, cookie func() string, settings *AutomationStore, root, dataDir string) *ShareWorkflow {
	return &ShareWorkflow{Client: client, Generator: generator, Cookie: cookie, Settings: settings, Root: root, JournalDir: filepath.Join(dataDir, "reshare-jobs"), PollInterval: 2 * time.Second, gate: make(chan struct{}, 1)}
}

type reshareJob struct {
	SourceURL     string    `json:"sourceURL,omitempty"`
	SourceCID     string    `json:"sourceCID,omitempty"`
	LastError     string    `json:"lastError,omitempty"`
	Finished      bool      `json:"finished"`
	ID            string    `json:"id"`
	DirectoryName string    `json:"directoryName"`
	DirectoryID   string    `json:"directoryID,omitempty"`
	ShareURL      string    `json:"shareURL,omitempty"`
	Status        string    `json:"status"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (w *ShareWorkflow) Generate(ctx context.Context, input GenerateInput) (result *GenerationResult, err error) {
	settings := w.Settings.Value()
	if !settings.ReshareEnabled {
		reportGeneration(ctx, "generating", "转存再分享已关闭：正在读取原分享并直接生成 STRM")
		return w.Generator.Generate(ctx, input)
	}
	reportGeneration(ctx, "starting", "转存再分享已开启：完整校验转存后创建长期分享，再生成 STRM 和清理临时目录")
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
	cookie := w.Cookie()
	if cookie == "" {
		return nil, generationInputError("请先登录 115")
	}
	// 整个转分享流程串行化，避免多个请求同时执行 115 的删除接口。
	reportGeneration(ctx, "queued", "正在等待其他转分享任务完成")
	select {
	case w.gate <- struct{}{}:
		defer func() { <-w.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	reportGeneration(ctx, "scanning", "正在读取原分享的完整目录")
	source, roots, err := w.snapshot(ctx, share, input.CID, cookie)
	if err != nil {
		return nil, fmt.Errorf("读取原分享: %w", err)
	}
	if len(roots) == 0 || len(roots) > 1000 {
		return nil, generationInputError("转分享需要 1 至 1000 个顶层条目，请缩小分享范围")
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("创建任务编号: %w", err)
	}
	job := reshareJob{ID: hex.EncodeToString(nonce[:]), Status: "creating", SourceURL: input.URL, SourceCID: input.CID}
	job.DirectoryName = "Cinema115-临时-" + job.ID
	logGeneration(ctx, "DEBUG", "转存任务记录编号："+job.ID)
	persist := func(status string) error {
		reportGeneration(ctx, status, stageMessage(status))
		job.Status = status
		job.UpdatedAt = time.Now().UTC()
		return writeAutomationJSON(filepath.Join(w.JournalDir, job.ID+".json"), job)
	}
	if err := persist("creating"); err != nil {
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
	defer func() {
		if err != nil {
			if job.Status != "deleting" && job.Status != "deleted" && job.Status != "completed" {
				logGeneration(ctx, "WARNING", "转存流程未完成，保留临时目录："+job.DirectoryName)
			} else {
				logGeneration(ctx, "WARNING", "清理或分享复查未完成，请根据转存任务记录核对网盘与回收站")
			}
			job.LastError = err.Error()
			job.Finished = true
			job.UpdatedAt = time.Now().UTC()
			if saveErr := writeAutomationJSON(filepath.Join(w.JournalDir, job.ID+".json"), job); saveErr != nil {
				err = errors.Join(err, saveErr)
			}
			if result == nil {
				result = &GenerationResult{}
			}
			result.ReshareURL = job.ShareURL
			result.TempDirectoryID = job.DirectoryID
			err = fmt.Errorf("%w；任务 %s，临时目录 ID %s（请查看任务记录中的清理状态）", err, job.ID, job.DirectoryID)
		}
	}()
	if err := persist("receiving"); err != nil {
		return nil, err
	}
	ids := fileIDs(roots)
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		if err := w.Client.ReceiveShare(ctx, share, ids[start:end], job.DirectoryID, cookie); err != nil {
			return nil, fmt.Errorf("转存失败，保留临时目录: %w", err)
		}
	}
	var transferred []p115.ShareFile
	err = w.until(ctx, func(waitCtx context.Context) error {
		var readErr error
		var received []string
		received, transferred, readErr = w.scanDirectory(waitCtx, job.DirectoryID, "正在校验网盘中的完整转存目录", func(ctx context.Context, cid string, offset int) (*p115.SharePage, error) {
			return w.Client.DriveFiles(ctx, cid, offset, cookie)
		})
		if readErr != nil {
			return readErr
		}
		// 115 的目录转存可能异步完成；必须核对全部子文件后才能创建固定快照。
		return compareManifest(source, received)
	})
	if err != nil {
		return nil, fmt.Errorf("等待转存失败，保留临时目录: %w", err)
	}
	if err := persist("sharing"); err != nil {
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
	if err := w.Client.SetSharePermanent(ctx, own, cookie); err != nil {
		return nil, fmt.Errorf("新分享长期有效设置失败，保留临时目录: %w", err)
	}
	if err := persist("waiting_snapshot"); err != nil {
		return nil, err
	}
	verify := func(waitCtx context.Context) error {
		actual, _, err := w.snapshot(waitCtx, own, "0", cookie)
		if err != nil {
			return err
		}
		return compareManifest(source, actual)
	}
	if err := w.until(ctx, verify); err != nil {
		return nil, fmt.Errorf("等待新分享快照失败，保留临时目录: %w", err)
	}
	if err := persist("generating"); err != nil {
		return nil, err
	}
	input.URL = own.URL
	input.Password = own.Password
	input.CID = "0"
	result, err = w.Generator.Generate(ctx, input)
	if result != nil {
		result.ReshareURL = own.URL
		result.TempDirectoryID = job.DirectoryID
	}
	if err != nil {
		return result, fmt.Errorf("新分享已创建，但 STRM 生成失败，保留临时目录: %w", err)
	}
	if err := ctx.Err(); err != nil {
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
	if err := w.until(ctx, verify); err != nil {
		return result, fmt.Errorf("临时目录已删除，但新分享复查失败；可从回收站恢复目录: %w", err)
	}
	job.Finished = true
	if err := persist("completed"); err != nil {
		return result, err
	}
	return result, nil
}

func (w *ShareWorkflow) until(ctx context.Context, check func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := check(ctx)
		if err == nil {
			return nil
		}
		reportGeneration(ctx, "waiting", "等待 115 完成处理（本阶段最多 90 秒）："+err.Error())
		timer := time.NewTimer(w.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(ctx.Err(), err)
		case <-timer.C:
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
		return "正在设置并确认分享长期有效"
	case "waiting_snapshot":
		return "正在核对新分享的完整快照"
	case "generating":
		return "正在使用新分享生成 STRM"
	case "deleting":
		return "正在将本次临时目录移入回收站"
	case "deleted":
		return "临时目录已删除，正在复查分享"
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

func fileIDs(files []p115.ShareFile) []string {
	ids := make([]string, 0, len(files))
	for _, file := range files {
		ids = append(ids, file.ID)
	}
	return ids
}

// snapshot 比较完整目录树中的路径、类型和文件大小，不能仅以分享链接存在为清理依据。
func (w *ShareWorkflow) snapshot(ctx context.Context, share *p115.ShareInfo, cid, cookie string) ([]string, []p115.ShareFile, error) {
	return w.scanDirectory(ctx, cid, "正在核对分享目录", func(ctx context.Context, id string, offset int) (*p115.SharePage, error) {
		return w.Client.List(ctx, share, id, offset, cookie)
	})
}

func (w *ShareWorkflow) scanDirectory(ctx context.Context, cid, label string, list func(context.Context, string, int) (*p115.SharePage, error)) ([]string, []p115.ShareFile, error) {
	if cid == "" {
		cid = "0"
	}
	entries := make([]string, 0)
	roots := make([]p115.ShareFile, 0)
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
				}
			}
			offset += len(page.Files)
			if offset >= page.Count {
				break
			}
		}
		return nil
	}
	if err := walk(cid, "", 0); err != nil {
		return nil, nil, err
	}
	sort.Strings(entries)
	return entries, roots, nil
}
