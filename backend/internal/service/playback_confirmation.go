package service

import (
	"context"
	"fmt"
	"time"

	"strm115/backend/internal/client/p115"
)

// PlaybackPendingError is a temporary result-confirmation state, not another
// failed upload. The deadline is persisted and is never extended by a read.
type PlaybackPendingError struct {
	RetryAt time.Time
	Reason  string
}

func (e *PlaybackPendingError) RetrySeconds() int {
	return max(1, int((time.Until(e.RetryAt)+time.Second-1)/time.Second))
}
func (e *PlaybackPendingError) Error() string {
	message := fmt.Sprintf("等待上次秒传结果确认，SHA1 暂未命中；请在 %d 秒后重试，无需移除本地记录", e.RetrySeconds())
	if e.Reason != "" {
		message += "；原因为：" + e.Reason
	}
	return message
}

// Called only for the submitting request, with the existing mutation gate held.
// At most two read-only searches within 3 seconds; never re-submit an upload.
func (s *PlaybackCache) confirmUncertainUpload(ctx context.Context, fast fastPlaybackClient, e PlaybackCacheEntry, info *p115.DownloadInfo, selected *PlaybackCacheSelection, ua string, report *PlaybackResolution) (string, bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	s.logs.Add(ctx, "INFO", "秒传响应未能确认，开始只读核查目标 SHA1（最多 2 次、查询窗口 3 秒）；不会重复提交")
	for _, delay := range []time.Duration{250 * time.Millisecond, 750 * time.Millisecond} {
		timer := time.NewTimer(delay)
		select {
		case <-probeCtx.Done():
			timer.Stop()
			return "", false, probeCtx.Err()
		case <-timer.C:
		}
		file, err := fast.FindPlaybackFileBySHA1(probeCtx, info.SHA1, selected.cookie)
		if err != nil {
			if ctx.Err() == nil {
				s.backoffPlaybackSearch(e.Key, e.TargetScope, time.Now().Add(5*time.Second), err)
			}
			return "", false, err
		}
		if file == nil {
			s.backoffPlaybackSearch(e.Key, e.TargetScope, minTime(time.Now().Add(3*time.Second), e.RetryNotBefore), nil)
			continue
		}
		if !cacheMatches(*file, info) || file.ID == "" || file.PickCode == "" {
			return "", false, fmt.Errorf("确认结果 SHA1 或文件标识无效")
		}
		e.File, e.DirectoryID, e.Status = *file, file.CID, "ready"
		e.RetryNotBefore = time.Time{}
		e.Message = "响应异常后按 SHA1 确认文件存在，已恢复；未重复提交秒传"
		link, err := s.reuseFastEntry(ctx, e, selected, ua, report)
		if err != nil {
			return "", false, err
		}
		s.rememberSource(e.SourceKey, e.Key)
		s.logs.Add(ctx, "INFO", "秒传响应异常后 SHA1 确认命中，已恢复取链；未重复提交秒传")
		return link, true, nil
	}
	return "", false, nil
}
