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
	if sourceCookie == "" || selected.binding.ParentCID == "" {
		return "", fmt.Errorf("请配置源账号凭据及朋友账号的秒传保存目录")
	}
	next("等待缓存操作")
	if err := s.enter(ctx); err != nil {
		return "", err
	}
	defer func() { <-s.gate }()
	sourceKey, _ := ctx.Value(playbackSourceKey{}).(string)
	if key := s.sourceMappings[sourceKey]; sourceKey != "" && key != "" {
		for _, e := range s.Status().Entries {
			if e.Key != key || e.AccountID != selected.binding.AccountID || e.UID != selected.uid || e.ParentCID != selected.binding.ParentCID || e.Status != "ready" {
				continue
			}
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
			break
		}
		delete(s.sourceMappings, sourceKey)
	}
	next("源文件解析")
	info, err := loadSource()
	if err != nil {
		return "", fmt.Errorf("源文件解析失败：%s", p115.SharePlaybackFailureReason(err))
	}
	if err := p115.ValidateInstantSource(info); err != nil {
		return "", err
	}
	report.UsedSource = true
	key := DirectLinkKey(selected.binding.AccountID, selected.uid, selected.binding.ParentCID, strings.ToUpper(info.SHA1))
	next("目标账号 SHA1 查找")
	f, err := fast.FindPlaybackFileBySHA1(ctx, info.SHA1, selected.cookie)
	if err != nil {
		return "", fmt.Errorf("朋友账号 SHA1 查找失败，未执行秒传：%s", p115.SharePlaybackFailureReason(err))
	}
	e := PlaybackCacheEntry{Key: key, AccountID: selected.binding.AccountID, UID: selected.uid, ParentCID: selected.binding.ParentCID, DirectoryID: selected.binding.ParentCID, File: p115.DrivePlaybackFile{Name: info.Name, Size: info.Size, SHA1: info.SHA1}, Status: "uploading", CreatedAt: time.Now().UTC(), LastUsed: time.Now().UTC()}
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
	// A search miss cannot prove an earlier interrupted write never completed.
	for _, old := range s.Status().Entries {
		if old.AccountID == selected.binding.AccountID && old.UID == selected.uid && strings.EqualFold(old.File.SHA1, info.SHA1) && old.Status != "ready" {
			return "", fmt.Errorf("历史秒传结果未确认且尚未查到同 SHA1 文件，请核查后移除本地记录")
		}
	}
	next("账号上传预检")
	ticket, err := fast.PreparePlaybackAccount(ctx, selected.binding.AccountID, selected.cookie)
	if err != nil {
		return "", fmt.Errorf("朋友账号秒传预检失败：%s", p115.SharePlaybackFailureReason(err))
	}
	if err := s.put(e); err != nil {
		return "", err
	}
	next("秒传预检")
	s.logs.Add(ctx, "INFO", "朋友账号未查到同 SHA1 文件，秒传到配置目录="+selected.binding.ParentCID)
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
		e.Status, e.Message = "uncertain", "秒传结果未确认；下次先按 SHA1 查找，不自动重复写入"
		if saveErr := s.put(e); saveErr != nil {
			return "", saveErr
		}
		return "", fmt.Errorf("朋友账号秒传未确认，请检查记录")
	}
	next("目标 SHA1 校验与取链")
	file, link, err := fast.CompletePlaybackUpload(ctx, info, selected.binding.ParentCID, result, selected.cookie, ua)
	if err != nil || !cacheMatches(file, info) || file.ID == "" || file.PickCode == "" || !p115.AllowedDirectPlaybackURL(link) {
		e.Status, e.Message = "uncertain", "秒传已提交，目标 SHA1 或取链未确认；下次先查找再复用"
		if saveErr := s.put(e); saveErr != nil {
			return "", saveErr
		}
		return "", fmt.Errorf("秒传目标 SHA1 校验或取链失败，请检查记录")
	}
	e.File, e.DirectoryID, e.Status, e.Message = file, file.CID, "ready", "秒传完成，SHA1 核对通过；云端文件由用户手动管理"
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
