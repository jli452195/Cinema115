package service

import (
	"context"
	"time"
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
		if m != nil && m.Share != nil && m.Share.LeaderID == job.ID && m.BatchID == d.BatchID && m.ParentCID == d.ParentCID && m.DirectoryID == root && member.Config.AccountID == job.Config.AccountID && member.Revision == job.Revision {
			return true
		}
	}
	return false
}

// Successful cleanup costs one recycle request per saved staging root. Only an
// ambiguous failure needs a parent listing; no source tree is scanned again.
// Ordinary drive errors stay on this job; only persistence errors stop workers.
func (s *DownloadOrganize) cleanupDownload(ctx context.Context, p *downloadPending) (string, error) {
	o, job := s.Organize, &p.job
	if !pendingDownloadCleanup(*job) {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	recycle, ok := o.drive.(organizeRecycleDrive)
	if !ok {
		return "目录清理服务不可用，可重试清理", nil
	}
	for len(job.Download.CleanupDirs) > 0 {
		root := job.Download.CleanupDirs[0]
		account, err := o.account(job.Config.AccountID)
		if err != nil || account.Revision != job.Revision || account.ID != p.account.ID || account.Revision != p.account.Revision {
			return "原任务账号配置已变化，下载目录未清理", nil
		}
		if !s.ownsCleanupRoot(*job, root) {
			return "下载目录归属未确认，未执行清理", nil
		}
		if err := recycle.DeleteEntries(ctx, []string{root}, p.account.Cookie); err != nil {
			children, readErr := o.list(ctx, job.Download.ParentCID, p.account.Cookie)
			if readErr != nil || slicesContainFile(children, root) {
				return "下载目录清理失败，可重试清理", nil
			}
		}
		job.Download.CleanupDirs = job.Download.CleanupDirs[1:]
		if err := o.checkpoint(job); err != nil {
			return "", err
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
	if share := job.Download.Share; share != nil {
		if !share.HandedOff {
			if s.Shares == nil {
				return s.stop(p, "interrupted", "文件已整理，分享队列尚未初始化；可继续原任务交接分享")
			}
			if err := s.Shares.EnqueueDownloadShare(ctx, *job, cookie); err != nil {
				return s.stop(p, "interrupted", "文件已整理，交接分享队列失败；可继续原任务："+organizePublicError(err, cookie))
			}
			share.HandedOff = true
		}
		job.Message = "文件已整理并交给 115 分享 STRM；生成进度和重试请查看分享日志"
	}
	if issue != "" {
		job.Message = "所选文件已整理；" + issue
	}
	return true, o.checkpoint(job)
}
