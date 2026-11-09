package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"strm115/backend/internal/strm"
)

type embyDeepDeleteDrive interface {
	DeleteEntriesBatch(context.Context, string, []string, string) error
	ReadDirectoryFiles(context.Context, string, string) (p115.DriveDirectory, []p115.ShareFile, error)
}

// SaveDeepDeleteConfig 仅保存本地开关，不连接 Emby 或 115。
func (s *EmbyService) SaveDeepDeleteConfig(ctx context.Context, enabled bool) (resultErr error) {
	ctx, finish := s.startOperation(ctx, "保存 Emby 删除联动设置")
	defer func() { finish(resultErr) }()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c := s.config
	if enabled && (c.APIKey == "" || c.ServerID == "" || c.WebhookSecret == "") {
		return fmt.Errorf("请先保存 Emby 连接配置")
	}
	c.DeepDeleteEnabled = enabled
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("编码删除联动设置: %w", err)
	}
	if err := s.secrets.Save(string(data)); err != nil {
		return fmt.Errorf("保存删除联动设置: %w", err)
	}
	s.config = c
	return nil
}

// 只解析神医约定的目标列表，绝不将 Item Path 或普通删除事件解释为源文件。
func embyDeepDeleteLinks(description string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(description, "\r\n", "\n"), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "Mount Paths:" {
			if start >= 0 {
				return nil, fmt.Errorf("深度删除通知包含重复的 Mount Paths")
			}
			start = i + 1
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("深度删除通知缺少 Mount Paths 目标列表")
	}
	links := make([]string, 0, len(lines)-start)
	seen := make(map[string]bool, len(lines)-start)
	for _, line := range lines[start:] {
		link := strings.TrimSpace(line)
		if link != "" && !seen[link] {
			seen[link] = true
			links = append(links, link)
		}
	}
	if len(links) == 0 || len(links) > 1000 {
		return nil, fmt.Errorf("深度删除目标数量应为 1 到 1000")
	}
	return links, nil
}

func embyDeepDeleteFile(link string) (strm.Drive, error) {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return strm.Drive{}, fmt.Errorf("不是本项目目录 STRM 链接")
	}
	return strm.ParseDrivePath(u.EscapedPath())
}

// 本地记录同时证明输出内容、源文件签名和所属账号，正常路径无需查询 115。
func (s *EmbyService) matchDeepDelete(ctx context.Context, c EmbyConfig, link string) (model.EmbyDeepDeleteJob, bool, error) {
	file, err := embyDeepDeleteFile(link)
	if err != nil {
		return model.EmbyDeepDeleteJob{}, false, nil
	}
	files, err := s.db.FindDriveSTRMDeleteCandidates(ctx, file.FileID)
	if err != nil {
		return model.EmbyDeepDeleteJob{}, false, err
	}
	digest := driveSTRMDigest([]byte(link + "\n"))
	signature := strings.ToUpper(file.SHA1) + ":" + strconv.FormatInt(file.Size, 10)
	var job model.EmbyDeepDeleteJob
	for _, record := range files {
		if record.Directory || record.Digest != digest || record.Signature != signature || !strings.EqualFold(path.Ext(record.Path), ".strm") || record.ParentCID == "" {
			continue
		}
		mapping, err := s.db.GetDriveSTRM(ctx, record.MappingID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			return job, false, err
		}
		account, err := s.DeepDeleteAccounts.Get(mapping.AccountID)
		if err != nil || !account.Primary || account.Cookie == "" || account.UID == "" || account.UID != p115.CookieUserID(account.Cookie) {
			continue
		}
		if job.Key != "" && (job.AccountID != account.ID || job.ParentCID != record.ParentCID) {
			return model.EmbyDeepDeleteJob{}, false, nil
		}
		key := sha256.Sum256([]byte(strings.Join([]string{c.URL, c.ServerID, account.ID, account.UID, file.FileID, signature}, "\x00")))
		job = model.EmbyDeepDeleteJob{Key: hex.EncodeToString(key[:]), ServerURL: c.URL, ServerID: c.ServerID, AccountID: account.ID, UID: account.UID, Revision: account.Revision, FileID: file.FileID, ParentCID: record.ParentCID, MappingID: record.MappingID, Digest: digest, Signature: signature}
	}
	return job, job.Key != "", nil
}

// 调用方已持有配置锁并完成 Webhook 密钥及服务身份核验；此处只进行本地读写。
func (s *EmbyService) queueDeepDeleteLocked(ctx context.Context, c EmbyConfig, description string) (int, error) {
	if !c.DeepDeleteEnabled {
		logEmby(ctx, "DEBUG", "删除联动未启用，已忽略 deep.delete 通知")
		return 200, nil
	}
	if s.DeepDeleteAccounts == nil || s.DeepDeleteDrive == nil {
		return 503, fmt.Errorf("删除联动服务尚未就绪")
	}
	links, err := embyDeepDeleteLinks(description)
	if err != nil {
		return 400, err
	}
	jobs := make([]model.EmbyDeepDeleteJob, 0, len(links))
	for _, link := range links {
		job, matched, err := s.matchDeepDelete(ctx, c, link)
		if err != nil {
			return 503, err
		}
		if matched {
			jobs = append(jobs, job)
		}
	}
	queued, err := s.db.QueueEmbyDeepDeletes(ctx, jobs)
	if err != nil {
		return 503, err
	}
	logEmby(ctx, "INFO", fmt.Sprintf("收到神医深度删除通知：目标 %d 个，新入队 %d 个，重复 %d 个，未匹配跳过 %d 个；接收通知未请求 115", len(links), queued, len(jobs)-queued, len(links)-len(jobs)))
	select {
	case s.deleteWake <- struct{}{}:
	default:
	}
	return 202, nil
}

func (s *EmbyService) RunDeepDelete(ctx context.Context) error {
	recovered, err := s.db.RecoverEmbyDeepDeletes(ctx)
	if err != nil {
		return err
	}
	if recovered > 0 {
		logEmby(s.logContext(ctx, s.snapshot(), s.newLogTask(), "删除联动"), "WARNING", fmt.Sprintf("%d 个中断的删除请求结果待确认，未自动重发", recovered))
	}
	for {
		if err := s.processDeepDeletes(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.deleteWake:
		}
		// 多个通知共用短合并窗口，空闲时没有轮询和网络请求。
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (s *EmbyService) processDeepDeletes(ctx context.Context) error {
	for {
		jobs, err := s.db.PendingEmbyDeepDeletes(ctx, 200)
		if err != nil || len(jobs) == 0 {
			return err
		}
		groups := make(map[string][]model.EmbyDeepDeleteJob, len(jobs))
		order := make([]string, 0, len(jobs))
		for _, job := range jobs {
			key := job.ServerURL + "\x00" + job.ServerID + "\x00" + job.AccountID + "\x00" + strconv.FormatUint(job.Revision, 10) + "\x00" + job.ParentCID
			if _, exists := groups[key]; !exists {
				order = append(order, key)
			}
			groups[key] = append(groups[key], job)
		}
		for _, key := range order {
			batch := groups[key]
			for len(batch) > 0 {
				count := min(len(batch), p115.DriveMutationBatchLimit)
				if err := s.deleteDeepBatch(ctx, batch[:count]); err != nil {
					return err
				}
				batch = batch[count:]
			}
		}
	}
}

func (s *EmbyService) deleteDeepBatch(ctx context.Context, jobs []model.EmbyDeepDeleteJob) error {
	first := jobs[0]
	c := s.snapshot()
	ctx = s.logContext(ctx, c, "EMBY-DELETE-"+first.Key[:12], "删除联动")
	keys := make([]string, 0, len(jobs))
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		keys = append(keys, job.Key)
		ids = append(ids, job.FileID)
	}
	skip := func() error {
		logEmby(ctx, "WARNING", fmt.Sprintf("跳过 %d 个删除目标：开关、服务器、账号凭据或本地文件记录已变化", len(jobs)))
		return s.db.SetEmbyDeepDeleteState(ctx, keys, "skipped")
	}
	if !c.DeepDeleteEnabled || c.URL != first.ServerURL || c.ServerID != first.ServerID || s.DeepDeleteAccounts == nil || s.DeepDeleteDrive == nil {
		return skip()
	}
	account, err := s.DeepDeleteAccounts.Get(first.AccountID)
	if err != nil || !account.Primary || account.Cookie == "" || account.Revision != first.Revision || account.UID != first.UID || p115.CookieUserID(account.Cookie) != first.UID {
		return skip()
	}
	ctx = s.logContext(ctx, c, "EMBY-DELETE-"+first.Key[:12], "删除联动", account.Cookie)
	// 排队后目录映射可能被移除或重扫；发送前只在本地复核，绝不改用其他目标。
	valid := make([]model.EmbyDeepDeleteJob, 0, len(jobs))
	for _, job := range jobs {
		mapping, err := s.db.GetDriveSTRM(ctx, job.MappingID)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		files, err := s.db.ListDriveSTRMFilesByIDs(ctx, job.MappingID, []string{job.FileID})
		if err != nil {
			return err
		}
		if mapping.AccountID != job.AccountID || len(files) != 1 || files[0].Directory || files[0].ParentCID != job.ParentCID || files[0].Digest != job.Digest || files[0].Signature != job.Signature {
			if err := s.db.SetEmbyDeepDeleteState(ctx, []string{job.Key}, "skipped"); err != nil {
				return err
			}
			logEmby(ctx, "WARNING", "本地记录已变化，跳过视频文件 ID："+job.FileID)
			continue
		}
		valid = append(valid, job)
	}
	keys, ids = keys[:0], ids[:0]
	for _, job := range valid {
		keys, ids = append(keys, job.Key), append(ids, job.FileID)
	}
	if len(keys) == 0 {
		return nil
	}
	// 先落盘发送意图；崩溃或保存回执失败后，不会把结果未知的删除重新发送。
	if err := s.db.SetEmbyDeepDeleteState(ctx, keys, "sending"); err != nil {
		return err
	}
	work, cancel := context.WithTimeout(p115.WithBackgroundBudget(ctx), 30*time.Second)
	defer cancel()
	err = s.DeepDeleteDrive.DeleteEntriesBatch(work, first.ParentCID, ids, account.Cookie)
	state := "accepted"
	if err == nil {
		logEmby(ctx, "INFO", fmt.Sprintf("115 已接受 %d 个视频的回收站删除请求，共 1 次批量请求，未额外逐项回查；文件 ID：%s", len(ids), strings.Join(ids, ",")))
	} else {
		state = "uncertain"
		logEmby(ctx, "WARNING", fmt.Sprintf("%d 个视频的删除请求结果未确认，未重发删除；将只读核对原父目录：%s", len(ids), embyLogError(err)))
		// 异常时整组共用一次目录读取，允许分页，不逐文件查询、不自动重放删除。
		if ctx.Err() == nil {
			check, checkCancel := context.WithTimeout(p115.WithBackgroundBudget(ctx), 10*time.Second)
			_, files, readErr := s.DeepDeleteDrive.ReadDirectoryFiles(check, first.ParentCID, account.Cookie)
			checkCancel()
			if readErr == nil {
				present := make(map[string]bool, len(files))
				for _, file := range files {
					present[file.ID] = true
				}
				remaining := 0
				for _, id := range ids {
					if present[id] {
						remaining++
					}
				}
				logEmby(ctx, "WARNING", fmt.Sprintf("删除结果复核：%d 个仍在原目录，%d 个已不在原目录；不将目录缺失推断为删除成功，待人工确认", remaining, len(ids)-remaining))
			} else {
				logEmby(ctx, "WARNING", "删除结果只读复核失败，保留待确认状态")
			}
		}
	}
	// 请求取消时仍尽量保存结果；sending 也会在下次启动恢复为 uncertain。
	finish, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	return s.db.SetEmbyDeepDeleteState(finish, keys, state)
}
