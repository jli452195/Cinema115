package service

import (
	"context"
	"errors"
	"time"

	"strm115/backend/internal/client/p115"
)

func downloadItemsDone(job OrganizeJob) bool {
	if len(job.Items) == 0 {
		return false
	}
	for _, item := range job.Items {
		if item.Status != "done" {
			return false
		}
	}
	return true
}

func pendingDownloadCleanup(job OrganizeJob) bool {
	return job.Download != nil && len(job.Download.CleanupDirs) > 0 && downloadItemsDone(job)
}

// Roots come only from directories created for this job or its sealed members.
// In particular, a share collection's SourceCID is the common parent, not a root.
func (s *DownloadOrganize) ownsCleanupRoot(job OrganizeJob, root string) bool {
	d := job.Download
	if !organizeID(root) || root == "0" || root == d.ParentCID || root == job.Config.TargetCID {
		return false
	}
	for _, item := range job.Items {
		if root == item.TargetCID {
			return false
		}
	}
	if root == d.DirectoryID {
		return true
	}
	if d.Share == nil || d.Share.LeaderID != job.ID || d.BatchID == "" {
		return false
	}
	s.Organize.mu.Lock()
	defer s.Organize.mu.Unlock()
	for _, member := range s.Organize.doc.Jobs {
		m := member.Download
		if m != nil && m.Share != nil && m.Share.LeaderID == job.ID && m.BatchID == d.BatchID && m.ParentCID == d.ParentCID && m.DirectoryID == root && member.Config.AccountID == job.Config.AccountID {
			return true
		}
	}
	return false
}

// 同一父目录的任务根目录按批回收，避免逐目录请求耗尽整批超时。
// 批量回执逐项核对；失败仍保留原任务的待清理目录，不重放整批写入。
func (s *DownloadOrganize) cleanupDownload(ctx context.Context, p *downloadPending) (string, error) {
	o, job := s.Organize, &p.job
	if !pendingDownloadCleanup(*job) {
		return "", nil
	}
	recycle, ok := o.drive.(organizeRecycleDrive)
	if !ok {
		return "目录清理服务不可用，可重试清理", nil
	}
	batchRecycle, _ := o.drive.(organizeRecycleBatchDrive)
	for len(job.Download.CleanupDirs) > 0 {
		size := 1
		useBatch := batchRecycle != nil && len(job.Download.CleanupDirs) > 1
		if useBatch {
			size = min(len(job.Download.CleanupDirs), p115.DriveMutationBatchLimit)
		}
		roots := job.Download.CleanupDirs[:size]
		account, err := o.account(job.Config.AccountID)
		if err != nil || account.ID != p.account.ID {
			return "原任务账号配置已变化，下载目录未清理", nil
		}
		for _, root := range roots {
			if !s.ownsCleanupRoot(*job, root) {
				return "下载目录归属未确认，未执行清理", nil
			}
		}
		work, cancel := context.WithTimeout(ctx, 30*time.Second)
		if useBatch {
			err = batchRecycle.DeleteEntriesBatch(work, job.Download.ParentCID, roots, p.account.Cookie)
		} else {
			err = recycle.DeleteEntries(work, roots, p.account.Cookie)
		}
		cancel()
		remaining := make([]string, 0, len(job.Download.CleanupDirs))
		if useBatch || err != nil {
			// 写入超时后仍使用独立预算读取，不能沿用已耗尽的写入上下文。
			verify, cancelVerify := context.WithTimeout(ctx, 30*time.Second)
			children, readErr := o.list(verify, job.Download.ParentCID, p.account.Cookie)
			cancelVerify()
			if readErr != nil {
				return "下载目录清理结果未确认，可重试清理：" + organizePublicError(errors.Join(err, readErr), p.account.Cookie), nil
			}
			for _, root := range roots {
				if slicesContainFile(children, root) {
					remaining = append(remaining, root)
				}
			}
		}
		pending := len(remaining)
		job.Download.CleanupDirs = append(remaining, job.Download.CleanupDirs[size:]...)
		if saveErr := o.checkpoint(job); saveErr != nil {
			return "", saveErr
		}
		if pending > 0 {
			message := "下载目录清理未完成，可重试清理"
			if err != nil {
				message += "：" + organizePublicError(err, p.account.Cookie)
			}
			return message, nil
		}
	}
	return "", nil
}

// Resume uses the same tail after all selected media moves are durable. Explicit
// subtitle uploads still finish first; unrelated staging contents are recycled.
func (s *DownloadOrganize) finishDownload(ctx context.Context, p *downloadPending) (bool, error) {
	o, job, cookie := s.Organize, &p.job, p.account.Cookie
	job.Status = "completed"
	if err := s.uploadSubtitles(ctx, job, cookie); err != nil {
		return false, err
	}
	if job.Status != "completed" {
		return true, nil
	}
	job.Message = "所选文件已整理，正在清理本次下载目录"
	if err := o.checkpoint(job); err != nil {
		return false, err
	}
	issue, err := s.cleanupDownload(ctx, p)
	if err != nil {
		return false, err
	}
	job.Message = "所选文件已整理，下载目录已清理"
	if job.Download.KeepExtras {
		job.Message = "所选文件已整理"
	}
	if share := job.Download.Share; share != nil {
		if !share.HandedOff {
			if _, err := o.account(job.Config.AccountID); err != nil {
				return s.stop(p, "interrupted", "文件已整理，原任务主账号不可用；请恢复登录或核对账号后继续原任务")
			}
			if s.Shares == nil {
				return s.stop(p, "interrupted", "文件已整理，分享队列尚未初始化；可继续原任务交接分享")
			}
			if err := s.Shares.EnqueueDownloadShare(ctx, *job, cookie); err != nil {
				return s.stop(p, "interrupted", "文件已整理，交接分享队列失败；可继续原任务："+organizePublicError(err, cookie))
			}
			share.HandedOff = true
		}
		job.Message = "文件已整理，正在生成分享 STRM；可在下载整理记录中查看整批进度"
	}
	if issue != "" {
		job.Message = "所选文件已整理；" + issue
	}
	if job.Download.KeepExtras {
		job.Message += "；附加视频保留在原下载目录"
	}
	return true, o.checkpoint(job)
}
