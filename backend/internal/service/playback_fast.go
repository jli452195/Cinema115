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
	release, err := s.acquirePlayback(ctx, "")
	if err != nil {
		return err
	}
	defer release()
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

// Serialize writes by target UID while unrelated accounts make bounded progress.
// Unknown writes are recoverable by a positive SHA1 hit.
func (s *PlaybackCache) resolveFastInstant(ctx context.Context, fast fastPlaybackClient, selected *PlaybackCacheSelection, sourceCookie, ua string, report *PlaybackResolution, loadSource func() (*p115.DownloadInfo, error)) (string, error) {
	debug := ProxyDebugEnabled(ctx)
	if debug {
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
			if t.TLSResumed {
				message += "｜TLS会话=恢复"
			}
			if t.Bytes > 0 {
				message += fmt.Sprintf("｜字节数=%d", t.Bytes)
			}
			if t.SearchSummary {
				message += fmt.Sprintf("｜请求=%d｜分页=%d｜重试=%d｜排队及限速=%d ms｜HTTP合计=%d ms", t.Requests, t.Pages, t.Retries, t.Wait.Milliseconds(), t.RequestHTTP.Milliseconds())
			}
			s.logs.Add(ctx, "INFO", message)
		})
	}
	started, stageStarted := time.Now(), time.Now()
	stage := "配置检查"
	stages := make([]string, 0, 12)
	next := func(name string) {
		if !debug {
			return
		}
		stages = append(stages, fmt.Sprintf("%s=%d ms", stage, time.Since(stageStarted).Milliseconds()))
		stage, stageStarted = name, time.Now()
	}
	defer func() {
		if !debug {
			return
		}
		next("结束")
		s.logs.Add(ctx, "INFO", fmt.Sprintf("秒传链路耗时：%s｜合计=%d ms", strings.Join(stages, "｜"), time.Since(started).Milliseconds()))
	}()
	if selected.binding.ParentCID == "" {
		return "", fmt.Errorf("请配置绑定账号的秒传保存目录")
	}
	next("等待缓存操作")
	release, err := s.acquirePlayback(ctx, selected.uid)
	if err != nil {
		return "", err
	}
	defer release()
	sourceKey, _ := ctx.Value(playbackSourceKey{}).(string)
	targetScope := DirectLinkKey(selected.binding.AccountID, selected.uid, selected.cookie, fmt.Sprint(selected.accountRevision))
	recovery := newPlaybackRecovery()
	defer recovery.close()
	defer func() {
		// Back off unresolved rejected files, not confirmed deleted copies or
		// successfully replaced source mappings. Never persist an error as a miss.
		for id := range recovery.failed {
			if !recovery.missing[id] {
				s.backoffPlaybackSearch(DirectLinkKey("target-link", selected.uid, id, ua), targetScope, time.Now().Add(5*time.Second), p115.ErrDriveRejected)
			}
		}
	}()
	// Rebuild a verified mapping after restart without querying the source.
	if sourceKey != "" && s.mappedSource(sourceKey) == "" {
		for _, entry := range s.entriesBySource(sourceKey) {
			if entry.SourceKey == sourceKey && entry.Status == "ready" {
				s.rememberSource(sourceKey, entry.Key)
				break
			}
		}
	}
	if key := s.mappedSource(sourceKey); sourceKey != "" && key != "" {
		if e, ok := s.entryByKey(key); ok {
			if e.Key != key || e.UID != selected.uid || e.Status != "ready" {
				s.forgetSource(sourceKey)
			} else {
				next("朋友账号取链")
				link, err := recovery.reuse(ctx, s, e, selected, ua, report)
				if err == nil {
					s.logs.Add(ctx, "INFO", "pickcode 文件映射命中，直接使用朋友账号取链")
					return link, nil
				}
				if !errors.Is(err, p115.ErrDriveFileMissing) && !errors.Is(err, errPlaybackNeedsSearch) {
					return "", err
				}
			}
		}
		s.forgetSource(sourceKey)
	}
	prefetch := s.startPlaybackSearch(ctx, fast, selected, targetScope)
	defer prefetch.stop()
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
	key := DirectLinkKey("pending-copy", selected.uid, materialID)
	copies := s.entriesByTarget(selected.uid, info.SHA1)
	_, canRevalidate := s.client.(playbackRangeClient)
	_, canVerifyLink := s.client.(interface {
		VerifiedPlaybackURL(context.Context, p115.DrivePlaybackFile, string, string) (string, error)
	})
	for _, entry := range copies {
		if len(recovery.tried) > 0 {
			break
		}
		if entry.UID == selected.uid && (entry.TargetScope == targetScope || canRevalidate || canVerifyLink) && entry.Status == "ready" && cacheMatches(entry.File, info) {
			next("朋友账号文件映射取链")
			entry.SourceKey = sourceKey
			entry.MaterialID = materialID
			link, err := recovery.reuse(ctx, s, entry, selected, ua, report)
			if err == nil {
				s.rememberSource(sourceKey, report.FileCacheKey)
				return link, nil
			}
			if !errors.Is(err, p115.ErrDriveFileMissing) && !errors.Is(err, errPlaybackNeedsSearch) {
				return "", err
			}
			break
		}
	}
	// Only fresh material without ranges/copies arms this hook. Speculate on
	// the read-only source URL while searching the destination; never upload
	// until the search and all destination preflight checks have succeeded.
	if err := s.waitPlaybackSearch(ctx, key, targetScope); err != nil {
		return "", err
	}
	quickKey := DirectLinkKey("quick-search", selected.uid, strings.ToUpper(info.SHA1))
	if err := s.waitPlaybackSearch(ctx, quickKey, targetScope); err != nil {
		return "", err
	}
	stopPrefetch := info.StartRangeSourcePrefetch(ctx)
	defer stopPrefetch()
	next("目标账号 SHA1 查找")
	var f *p115.DrivePlaybackFile
	if len(recovery.failed) > 0 || len(recovery.missing) > 0 {
		prefetch.stop()
		f, err = recovery.search(ctx, fast, selected, info.SHA1)
	} else {
		f, err = s.searchPlaybackTarget(ctx, fast, selected, info, copies, prefetch)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil && f != nil && errors.Is(err, p115.ErrDriveRejected) && cacheMatches(*f, info) {
		candidate := PlaybackCacheEntry{UID: selected.uid, File: *f}
		err = recovery.confirmMissing(ctx, s, candidate, selected, err)
		if errors.Is(err, p115.ErrDriveFileMissing) {
			prefetch.stop()
			f, err = recovery.search(ctx, fast, selected, info.SHA1)
		}
	}
	if err != nil {
		s.backoffPlaybackSearch(key, targetScope, time.Now().Add(5*time.Second), err)
		s.backoffPlaybackSearch(quickKey, targetScope, time.Now().Add(5*time.Second), err)
		return "", &playbackTargetError{stage: "朋友账号 SHA1 查找失败，未执行秒传", err: err}
	}
	e := PlaybackCacheEntry{Key: key, AccountID: selected.binding.AccountID, UID: selected.uid, ParentCID: selected.binding.ParentCID, DirectoryID: selected.binding.ParentCID, File: p115.DrivePlaybackFile{Name: info.Name, Size: info.Size, SHA1: info.SHA1}, Status: "uploading", CreatedAt: time.Now().UTC(), LastUsed: time.Now().UTC()}
	e.SourceKey, e.TargetScope = sourceKey, targetScope
	e.MaterialID = materialID
	for f != nil {
		if !cacheMatches(*f, info) || f.ID == "" || f.PickCode == "" {
			return "", fmt.Errorf("目标搜索结果 SHA1 或文件标识无效")
		}
		e.File, e.DirectoryID, e.Status, e.Message = *f, f.CID, "ready", "目标账号已有同 SHA1 文件，直接复用"
		next("朋友账号取链")
		link, err := recovery.reuse(ctx, s, e, selected, ua, report)
		if err != nil {
			if !errors.Is(err, p115.ErrDriveFileMissing) && !errors.Is(err, errPlaybackNeedsSearch) {
				return "", err
			}
			prefetch.stop()
			next("失效副本恢复查询")
			f, err = recovery.search(ctx, fast, selected, info.SHA1)
			if err != nil {
				return "", &playbackTargetError{stage: "失效副本恢复查询失败，未执行秒传", err: err}
			}
			continue
		}
		report.Mode = "复用同 SHA1 文件"
		s.rememberSource(sourceKey, report.FileCacheKey)
		s.logs.Add(ctx, "INFO", "朋友账号 SHA1 命中，直接取链；无需秒传")
		return link, nil
	}
	// Search candidates must not turn the new write intent into a ready record.
	e.File = p115.DrivePlaybackFile{Name: info.Name, Size: info.Size, SHA1: info.SHA1}
	e.DirectoryID, e.Status, e.Message = selected.binding.ParentCID, "uploading", ""
	// Old uncertain records are diagnostic only. Newly interrupted writes get a
	// bounded cooldown; a positive SHA1 hit above always bypasses it.
	var previous *PlaybackCacheEntry
	var retryAt time.Time
	count := 0
	for _, old := range copies {
		if old.UID == selected.uid && strings.EqualFold(old.File.SHA1, info.SHA1) && old.Status != "ready" {
			count++
			if previous == nil || old.CreatedAt.After(previous.CreatedAt) {
				copy := old
				previous = &copy
			}
			if cacheMatches(old.File, info) && old.RetryNotBefore.After(retryAt) {
				retryAt = old.RetryNotBefore
			}
		}
	}
	if previous != nil {
		s.logs.Add(ctx, "INFO", fmt.Sprintf("SHA1 搜索完成，未命中；SHA1=%s｜历史记录数=%d｜最近记录=%s｜状态=%s｜原因=%s", strings.ToUpper(info.SHA1), count, previous.Key, previous.Status, previous.Message))
	}
	if remaining := time.Until(retryAt); remaining > 0 {
		s.backoffPlaybackSearch(key, targetScope, minTime(time.Now().Add(3*time.Second), retryAt), nil)
		return "", &PlaybackPendingError{RetryAt: retryAt}
	}
	next("账号上传预检")
	if len(recovery.failed) > 0 || len(recovery.missing) > 0 {
		if directory, ok := fast.(interface {
			RefreshPlaybackDirectory(context.Context, string, string, string) error
		}); ok {
			if err := directory.RefreshPlaybackDirectory(recovery.budget(ctx), selected.binding.AccountID, selected.binding.ParentCID, selected.cookie); err != nil {
				return "", &playbackTargetError{stage: "恢复秒传前保存目录核验失败，请检查原目录配置", err: err}
			}
		}
		s.logs.Add(ctx, "INFO", "目标 UID 完整搜索未找到可用替代文件，向原目录重新秒传；本次最多重建一次")
	}
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
	var file p115.DrivePlaybackFile
	var link string
	if preparingPlaybackFile(ctx) {
		if preparer, ok := fast.(interface {
			CompletePlaybackFile(context.Context, *p115.DownloadInfo, string, *p115.InstantUploadResult, string) (p115.DrivePlaybackFile, error)
		}); ok {
			file, err = preparer.CompletePlaybackFile(ctx, info, selected.binding.ParentCID, result, selected.cookie)
		} else {
			err = fmt.Errorf("播放客户端不支持独立文件核验")
		}
	} else {
		file, link, err = fast.CompletePlaybackUpload(ctx, info, selected.binding.ParentCID, result, selected.cookie, ua)
	}
	if err != nil || !cacheMatches(file, info) || file.ID == "" || file.PickCode == "" || !preparingPlaybackFile(ctx) && !p115.AllowedDirectPlaybackURL(link) {
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
	e, err = s.storeReadyCopy(e)
	if err != nil {
		return "", err
	}
	report.Mode, report.FileCacheKey, report.FileCacheID = "新建秒传文件", e.Key, file.ID
	s.rememberSource(sourceKey, report.FileCacheKey)
	return link, nil
}

func (s *PlaybackCache) rememberSource(source, key string) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if source == "" {
		return
	}
	if s.sourceMappings == nil || len(s.sourceMappings) >= 2000 {
		s.sourceMappings = make(map[string]string)
	}
	s.sourceMappings[source] = key
}
func (s *PlaybackCache) reuseFastEntry(ctx context.Context, e PlaybackCacheEntry, selected *PlaybackCacheSelection, ua string, report *PlaybackResolution) (string, error) {
	verified, canVerify := s.client.(interface {
		VerifiedPlaybackURL(context.Context, p115.DrivePlaybackFile, string, string) (string, error)
	})
	if e.UID != selected.uid || p115.CookieUserID(selected.cookie) != selected.uid {
		return "", fmt.Errorf("目标凭据与副本 UID 不一致")
	}
	if preparingPlaybackFile(ctx) || !canVerify {
		if err := s.verifyReusableCopy(ctx, e, selected); err != nil {
			return "", err
		}
	}
	e.AccountID, e.ParentCID = selected.binding.AccountID, selected.binding.ParentCID
	e.TargetScope = DirectLinkKey(selected.binding.AccountID, selected.uid, selected.cookie, fmt.Sprint(selected.accountRevision))
	var link string
	if !preparingPlaybackFile(ctx) {
		var err error
		cooldownKey := DirectLinkKey("target-link", selected.uid, e.File.ID, ua)
		if err := s.waitPlaybackSearch(ctx, cooldownKey, e.TargetScope); err != nil {
			return "", err
		}
		if canVerify {
			link, err = verified.VerifiedPlaybackURL(ctx, e.File, selected.cookie, ua)
		} else {
			link, err = s.client.DriveDownloadURLWithUA(ctx, e.File.ID, e.File.PickCode, selected.cookie, ua)
		}
		if err == nil && !p115.AllowedDirectPlaybackURL(link) {
			err = p115.ErrDriveLinkRestricted
		}
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			failure := &playbackTargetError{stage: "目标文件取链失败", err: err}
			if !errors.Is(err, p115.ErrDriveRejected) && !errors.Is(err, p115.ErrDriveFileMissing) {
				s.backoffPlaybackSearch(cooldownKey, e.TargetScope, time.Now().Add(5*time.Second), failure)
			}
			return "", failure
		}
	}
	e.LastUsed = time.Now().UTC()
	e, err := s.storeReadyCopy(e)
	if err != nil {
		return "", err
	}
	s.stats.targetHits.Add(1)
	report.Mode, report.FileCacheKey, report.FileCacheID = "复用同 SHA1 文件", e.Key, e.File.ID
	return link, nil
}
