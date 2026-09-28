package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
)

type PlaybackCacheDirectory struct {
	Key       string `json:"key"`
	AccountID string `json:"accountId"`
	UID       string `json:"uid"`
	ParentCID string `json:"parentCid"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
}
type playbackSourceKey struct{}

const playbackRetryCooldown = 30 * time.Second

type fastPlaybackClient interface {
	PreparePlaybackAccount(context.Context, string, string) (*p115.PlaybackUploadTicket, error)
	UploadPlayback(context.Context, *p115.DownloadInfo, string, string, string, *p115.PlaybackUploadTicket, func(p115.InstantProgress)) (*p115.InstantUploadResult, error)
	CompletePlaybackUpload(context.Context, *p115.DownloadInfo, string, *p115.InstantUploadResult, string, string) (p115.DrivePlaybackFile, string, error)
	FindPlaybackFileBySHA1(context.Context, string, string) (*p115.DrivePlaybackFile, error)
}

// Forgetting a directory never removes its cloud contents or per-file records.
func (s *PlaybackCache) ForgetDirectory(ctx context.Context, key string) error {
	if len(key) != 64 || strings.Trim(key, "0123456789abcdef") != "" {
		return fmt.Errorf("目录记录编号无效")
	}
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	doc.Directories = make([]PlaybackCacheDirectory, 0, len(s.doc.Directories))
	for _, d := range s.doc.Directories {
		if d.Key != key {
			doc.Directories = append(doc.Directories, d)
		}
	}
	return s.saveLocked(doc)
}

// All mutations and lookups share a gate so concurrent requests cannot upload
// the same content twice. Unknown writes are recoverable by a positive SHA1 hit.
func (s *PlaybackCache) resolveFastInstant(ctx context.Context, fast fastPlaybackClient, selected *PlaybackCacheSelection, sourceCookie, ua string, report *PlaybackResolution, loadSource func() (*p115.DownloadInfo, error)) (string, error) {
	ctx = p115.WithPlaybackTiming(ctx, func(t p115.PlaybackTiming) {
		message := fmt.Sprintf("秒传分段计时：%s=%.2f ms", t.Stage, float64(t.Duration)/float64(time.Millisecond))
		if t.Canceled {
			message += "｜状态=已取消"
		} else if t.Failed {
			message += "｜状态=失败"
		}
		if t.HTTP {
			connection := "未取得连接"
			if t.Connected {
				if t.Reused {
					connection = "复用"
				} else {
					connection = "新建"
				}
			}
			message += fmt.Sprintf("｜DNS=%.2f ms｜TCP=%.2f ms｜TLS=%.2f ms｜请求开始至首字节=%.2f ms｜连接=%s", float64(t.DNS)/float64(time.Millisecond), float64(t.TCP)/float64(time.Millisecond), float64(t.TLS)/float64(time.Millisecond), float64(t.FirstByte)/float64(time.Millisecond), connection)
		}
		if t.Bytes > 0 {
			message += fmt.Sprintf("｜字节数=%d", t.Bytes)
		}
		s.logs.Add(ctx, "INFO", message)
	})
	started, stageStarted := time.Now(), time.Now()
	stage := "配置检查"
	stages := make([]string, 0, 12)
	next := func(name string) {
		stages = append(stages, fmt.Sprintf("%s=%d ms", stage, time.Since(stageStarted).Milliseconds()))
		stage, stageStarted = name, time.Now()
	}
	defer func() {
		next("结束")
		s.logs.Add(ctx, "INFO", fmt.Sprintf("秒传链路耗时：%s｜合计=%d ms", strings.Join(stages, "｜"), time.Since(started).Milliseconds()))
	}()
	if selected.binding.ParentCID == "" {
		return "", fmt.Errorf("请配置绑定账号的秒传保存目录")
	}
	next("等待缓存操作")
	if err := s.enter(ctx); err != nil {
		return "", err
	}
	defer func() { <-s.gate }()
	sourceKey, _ := ctx.Value(playbackSourceKey{}).(string)
	targetScope := DirectLinkKey(selected.binding.AccountID, selected.uid, selected.cookie, fmt.Sprint(selected.accountRevision))
	failedMapping := ""
	// Rebuild a verified mapping after restart without querying the source.
	if sourceKey != "" && s.sourceMappings[sourceKey] == "" {
		for _, entry := range s.entriesBySource(sourceKey) {
			if entry.SourceKey == sourceKey && entry.Status == "ready" {
				s.rememberSource(sourceKey, entry.Key)
				break
			}
		}
	}
	if key := s.sourceMappings[sourceKey]; sourceKey != "" && key != "" {
		if e, ok := s.entryByKey(key); ok {
			if e.Key != key || e.AccountID != selected.binding.AccountID || e.UID != selected.uid || e.TargetScope != targetScope || e.ParentCID != selected.binding.ParentCID || e.Status != "ready" {
				delete(s.sourceMappings, sourceKey)
			} else {
				next("朋友账号取链")
				link, err := s.reuseFastEntry(ctx, e, selected, ua, report)
				if err == nil {
					s.logs.Add(ctx, "INFO", "pickcode 文件映射命中，直接使用朋友账号取链")
					return link, nil
				}
				// A failed URL request does not prove deletion; search before any new write.
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				failedMapping = e.Key
			}
		}
		delete(s.sourceMappings, sourceKey)
	}
	next("文件秒传资料")
	info, materialID, commitRanges, err := s.preparePlaybackMaterial(ctx, selected, report, func() (*p115.DownloadInfo, error) {
		if sourceCookie == "" {
			return nil, fmt.Errorf("请配置主账号凭据")
		}
		return loadSource()
	})
	if err != nil {
		return "", err
	}
	if err := p115.ValidateInstantSource(info); err != nil {
		return "", err
	}
	key := DirectLinkKey(selected.binding.AccountID, selected.uid, selected.binding.ParentCID, strings.ToUpper(info.SHA1))
	copies := s.entriesByHash(info.SHA1)
	for _, entry := range copies {
		if entry.Key != failedMapping && entry.AccountID == selected.binding.AccountID && entry.UID == selected.uid && entry.TargetScope == targetScope && entry.Status == "ready" && cacheMatches(entry.File, info) {
			next("朋友账号文件映射取链")
			entry.SourceKey = sourceKey
			entry.MaterialID = materialID
			link, err := s.reuseFastEntry(ctx, entry, selected, ua, report)
			if err == nil {
				s.rememberSource(sourceKey, entry.Key)
				return link, nil
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			break
		}
	}
	next("目标账号 SHA1 查找")
	f, err := fast.FindPlaybackFileBySHA1(ctx, info.SHA1, selected.cookie)
	if err != nil {
		return "", fmt.Errorf("朋友账号 SHA1 查找失败，未执行秒传：%s", p115.SharePlaybackFailureReason(err))
	}
	e := PlaybackCacheEntry{Key: key, AccountID: selected.binding.AccountID, UID: selected.uid, ParentCID: selected.binding.ParentCID, DirectoryID: selected.binding.ParentCID, File: p115.DrivePlaybackFile{Name: info.Name, Size: info.Size, SHA1: info.SHA1}, Status: "uploading", CreatedAt: time.Now().UTC(), LastUsed: time.Now().UTC()}
	e.SourceKey, e.TargetScope = sourceKey, targetScope
	e.MaterialID = materialID
	if f != nil {
		if !cacheMatches(*f, info) || f.ID == "" || f.PickCode == "" {
			return "", fmt.Errorf("目标搜索结果 SHA1 或文件标识无效")
		}
		e.File, e.DirectoryID, e.Status, e.Message = *f, f.CID, "ready", "目标账号已有同 SHA1 文件，直接复用"
		next("朋友账号取链")
		link, err := s.reuseFastEntry(ctx, e, selected, ua, report)
		if err != nil {
			return "", err
		}
		report.Mode = "复用同 SHA1 文件"
		s.rememberSource(sourceKey, key)
		s.logs.Add(ctx, "INFO", "朋友账号 SHA1 命中，直接取链；无需秒传")
		return link, nil
	}
	// Old uncertain records are diagnostic only. Newly interrupted writes get a
	// bounded cooldown; a positive SHA1 hit above always bypasses it.
	var previous *PlaybackCacheEntry
	var retryAt time.Time
	count := 0
	for _, old := range copies {
		if old.AccountID == selected.binding.AccountID && old.UID == selected.uid && strings.EqualFold(old.File.SHA1, info.SHA1) && old.Status != "ready" {
			count++
			if previous == nil || old.CreatedAt.After(previous.CreatedAt) {
				copy := old
				previous = &copy
			}
			if old.RetryNotBefore.After(retryAt) {
				retryAt = old.RetryNotBefore
			}
		}
	}
	if previous != nil {
		s.logs.Add(ctx, "INFO", fmt.Sprintf("SHA1 搜索完成，未命中；SHA1=%s｜历史记录数=%d｜最近记录=%s｜状态=%s｜原因=%s", strings.ToUpper(info.SHA1), count, previous.Key, previous.Status, previous.Message))
	}
	if remaining := time.Until(retryAt); remaining > 0 {
		return "", &PlaybackPendingError{RetryAt: retryAt}
	}
	next("账号上传预检")
	ticket, err := fast.PreparePlaybackAccount(ctx, selected.binding.AccountID, selected.cookie)
	if err != nil {
		return "", fmt.Errorf("朋友账号秒传预检失败：%s", p115.SharePlaybackFailureReason(err))
	}
	// Persist a finite deadline before submitting, including for process crashes.
	e.RetryNotBefore = time.Now().UTC().Add(playbackRetryCooldown)
	if err := s.put(e); err != nil {
		return "", err
	}
	next("秒传预检")
	s.logs.Add(ctx, "INFO", "朋友账号未查到同 SHA1 文件，允许秒传；SHA1="+strings.ToUpper(info.SHA1)+"｜保存目录="+selected.binding.ParentCID)
	result, err := fast.UploadPlayback(ctx, info, selected.binding.AccountID, selected.binding.ParentCID, selected.cookie, ticket, func(p p115.InstantProgress) {
		switch p.Stage {
		case "init":
			next("秒传提交")
		case "range":
			next("二次校验片段读取")
			s.logs.Add(ctx, "INFO", fmt.Sprintf("秒传二次校验，读取片段字节数=%d", p.Bytes))
		case "range_done":
			next("校验结果准备")
		}
	})
	if err != nil || result == nil {
		var preflight *p115.PlaybackPreflightError
		if errors.As(err, &preflight) {
			if saveErr := s.ForgetLocked(key); saveErr != nil {
				return "", saveErr
			}
			return "", fmt.Errorf("朋友账号 %s：%w", selected.binding.AccountID, preflight)
		}
		reason := p115.InstantUploadFailureReason(err)
		e.Status, e.Message = "uncertain", reason+"；下次先按 SHA1 查找，30 秒冷却结束且未命中时允许重新秒传"
		e.RetryNotBefore = time.Now().UTC().Add(playbackRetryCooldown)
		if saveErr := s.put(e); saveErr != nil {
			return "", saveErr
		}
		var httpFailure *p115.HTTPError
		if (errors.Is(err, p115.ErrInstantUncertain) || err == nil) && ctx.Err() == nil && !errors.As(err, &httpFailure) {
			next("异常响应 SHA1 确认")
			link, found, confirmErr := s.confirmUncertainUpload(ctx, fast, e, info, selected, ua, report)
			if found {
				return link, nil
			}
			if confirmErr != nil {
				reason += "；只读确认未完成：" + p115.SharePlaybackFailureReason(confirmErr)
			}
			return "", &PlaybackPendingError{RetryAt: e.RetryNotBefore, Reason: reason}
		}
		return "", fmt.Errorf("朋友账号秒传未完成：%s", reason)
	}
	// Only an explicit successful upload response proves the challenge accepted.
	if err := commitRanges(); err != nil {
		return "", fmt.Errorf("秒传已成功，但校验资料保存失败: %w", err)
	}
	next("目标 SHA1 校验与取链")
	s.logs.Add(ctx, "INFO", fmt.Sprintf("115 已返回秒传成功，开始按回执取链；fileID=%s｜pickcode=%s", result.FileID, result.PickCode))
	file, link, err := fast.CompletePlaybackUpload(ctx, info, selected.binding.ParentCID, result, selected.cookie, ua)
	if err != nil || !cacheMatches(file, info) || file.ID == "" || file.PickCode == "" || !p115.AllowedDirectPlaybackURL(link) {
		reason := "目标 SHA1、文件标识或直链未通过核验"
		var detailed *p115.PlaybackCompletionError
		if errors.As(err, &detailed) {
			reason = detailed.Reason
		} else if err != nil {
			reason = p115.SharePlaybackFailureReason(err)
		}
		e.Status, e.Message = "uncertain", "秒传已成功，"+reason+"；下次先查找，30 秒冷却结束且未命中时允许重新秒传"
		e.RetryNotBefore = time.Now().UTC().Add(playbackRetryCooldown)
		if saveErr := s.put(e); saveErr != nil {
			return "", saveErr
		}
		return "", fmt.Errorf("秒传已成功，%s（fileID=%s，pickcode=%s）", reason, result.FileID, result.PickCode)
	}
	e.File, e.DirectoryID, e.Status, e.Message = file, file.CID, "ready", "秒传完成，SHA1 核对通过；云端文件由用户手动管理"
	e.RetryNotBefore = time.Time{}
	next("保存文件记录")
	if err := s.put(e); err != nil {
		return "", err
	}
	report.Mode, report.FileCacheKey, report.FileCacheID = "新建秒传文件", key, file.ID
	s.rememberSource(sourceKey, key)
	return link, nil
}

func (s *PlaybackCache) rememberSource(source, key string) {
	if source == "" {
		return
	}
	if s.sourceMappings == nil || len(s.sourceMappings) >= 2000 {
		s.sourceMappings = make(map[string]string)
	}
	s.sourceMappings[source] = key
}
func (s *PlaybackCache) reuseFastEntry(ctx context.Context, e PlaybackCacheEntry, selected *PlaybackCacheSelection, ua string, report *PlaybackResolution) (string, error) {
	link, err := s.client.DriveDownloadURLWithUA(ctx, e.File.ID, e.File.PickCode, selected.cookie, ua)
	if err != nil || !p115.AllowedDirectPlaybackURL(link) {
		return "", fmt.Errorf("朋友文件取链失败，请检查文件及账号")
	}
	e.LastUsed = time.Now().UTC()
	if err := s.put(e); err != nil {
		return "", err
	}
	report.Mode, report.FileCacheKey, report.FileCacheID = "复用同 SHA1 文件", e.Key, e.File.ID
	return link, nil
}
