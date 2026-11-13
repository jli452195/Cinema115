package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DownloadPipeline 仅汇总已有整理记录和分享日志，不创建新的执行任务。
type DownloadPipeline struct {
	ID         string                  `json:"id"`
	Title      string                  `json:"title"`
	CreatedAt  time.Time               `json:"createdAt"`
	JobIDs     []string                `json:"jobIds"`
	ShareSTRM  bool                    `json:"shareSTRM"`
	Status     string                  `json:"status"`
	Finished   bool                    `json:"finished"`
	Message    string                  `json:"message"`
	Total      int                     `json:"total"`
	Ready      int                     `json:"ready"`
	Organized  int                     `json:"organized"`
	Failed     int                     `json:"failed"`
	VideoCount int                     `json:"videoCount"`
	Written    int                     `json:"written"`
	Shares     []DownloadPipelineShare `json:"shares"`
}

type DownloadPipelineShare struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Written     int        `json:"written"`
	Total       int        `json:"total"`
	Error       string     `json:"error"`
	Note        string     `json:"note"`
	ShareURL    string     `json:"shareUrl"`
	CanRetry    bool       `json:"canRetry"`
	RetryReason string     `json:"retryReason,omitempty"`
	NextRetryAt *time.Time `json:"nextRetryAt"`
}

type downloadShareProgressReader interface {
	DownloadShareProgress(context.Context, string) (DownloadPipelineShare, error)
}

func (s *ShareTasks) DownloadShareProgress(ctx context.Context, id string) (DownloadPipelineShare, error) {
	if s.Store == nil {
		return DownloadPipelineShare{}, errors.New("分享日志尚未初始化")
	}
	task, err := s.Store.GetShareTask(ctx, id)
	if err != nil {
		return DownloadPipelineShare{}, err
	}
	reason := s.RetryReason(task)
	return DownloadPipelineShare{ID: task.ID, Status: task.Status, Written: task.Written, Total: task.Total, Error: task.Error, Note: task.Note, ShareURL: task.ShareURL, CanRetry: reason == "", RetryReason: reason, NextRetryAt: task.NextRetryAt}, nil
}

func downloadPipelineID(job OrganizeJob) string {
	if job.Download != nil && job.Download.BatchID != "" {
		return job.Download.BatchID
	}
	return job.ID
}

// 调用方持有任务文档锁；裁剪历史前仅查本地日志，保留仍在生成或等待审核的整批记录。
func (s *DownloadOrganize) activeSharePipelines(jobs []OrganizeJob) (map[string]bool, error) {
	protected := make(map[string]bool)
	reader, _ := s.Shares.(downloadShareProgressReader)
	seen := make(map[string]bool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, job := range jobs {
		if job.Download == nil || job.Download.Share == nil || !job.Download.Share.HandedOff {
			continue
		}
		id := job.Download.Share.LeaderID
		if id == "" {
			id = job.ID
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if reader == nil {
			protected[downloadPipelineID(job)] = true
			continue
		}
		share, err := reader.DownloadShareProgress(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("确认分享任务历史保留范围: %w", err)
		}
		switch share.Status {
		case "success", "failed", "cancelled", "interrupted":
		default:
			protected[downloadPipelineID(job)] = true
		}
	}
	return protected, nil
}

// PipelineHistory 使用同一份持久记录快照，防止列表和聚合分别读取时出现矛盾。
func (s *DownloadOrganize) PipelineHistory(ctx context.Context) ([]OrganizeJob, []DownloadPipeline, error) {
	jobs := s.History()
	pipelines, err := s.downloadPipelines(ctx, jobs, nil)
	return jobs, pipelines, err
}

func (s *DownloadOrganize) Progress(ctx context.Context, ids []string) ([]DownloadPipeline, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, fmt.Errorf("%w：请选择 1 至 100 个批次或任务", ErrOrganizeInput)
	}
	jobs := s.History()
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		found := false
		for _, job := range jobs {
			if id != "" && (job.ID == id || downloadPipelineID(job) == id) {
				selected[downloadPipelineID(job)], found = true, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w：任务记录不存在或已移出历史，请核对任务记录", ErrOrganizeConflict)
		}
	}
	return s.downloadPipelines(ctx, jobs, selected)
}

func (s *DownloadOrganize) downloadPipelines(ctx context.Context, jobs []OrganizeJob, selected map[string]bool) ([]DownloadPipeline, error) {
	groups := make(map[string][]OrganizeJob, len(jobs))
	order := make([]string, 0, len(jobs))
	for _, job := range jobs {
		id := downloadPipelineID(job)
		if selected != nil && !selected[id] {
			continue
		}
		if _, ok := groups[id]; !ok {
			order = append(order, id)
		}
		groups[id] = append(groups[id], job)
	}
	out := make([]DownloadPipeline, 0, len(order))
	reader, _ := s.Shares.(downloadShareProgressReader)
	for _, id := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pipeline, err := aggregateDownloadPipeline(ctx, id, groups[id], reader)
		if err != nil {
			return nil, err
		}
		out = append(out, pipeline)
	}
	return out, nil
}

func aggregateDownloadPipeline(ctx context.Context, id string, jobs []OrganizeJob, reader downloadShareProgressReader) (DownloadPipeline, error) {
	p := DownloadPipeline{ID: id, Title: jobs[0].Title, CreatedAt: jobs[0].CreatedAt, Total: len(jobs), JobIDs: make([]string, 0, len(jobs)), Shares: []DownloadPipelineShare{}}
	shares := make(map[string]bool, len(jobs))
	videos := make(map[string]bool)
	queued, downloading, organizing, generating, review, success, interrupted := 0, 0, 0, 0, 0, 0, false
	failedShares, expected := 0, len(jobs)
	issues := make([]string, 0, len(jobs))
	for _, job := range jobs {
		p.JobIDs = append(p.JobIDs, job.ID)
		if job.CreatedAt.Before(p.CreatedAt) {
			p.CreatedAt = job.CreatedAt
		}
		d := job.Download
		if d.BatchSize > expected {
			expected = d.BatchSize
		}
		ready := d.DirectoryID != "" && (job.Status == "running" || job.Status == "completed" || !job.Stats.StartedAt.IsZero() || d.Share != nil && d.Share.LeaderID != "")
		if ready {
			p.Ready++
		}
		if downloadItemsDone(job) {
			p.Organized++
		}
		switch job.Status {
		case "queued":
			if d.Share != nil && d.Share.LeaderID != "" {
				organizing++
			} else {
				queued++
			}
		case "submitting", "downloading":
			downloading++
		case "running":
			organizing++
		case "completed":
		default:
			p.Failed++
			if job.Message != "" {
				issues = append(issues, job.Message)
			}
			interrupted = interrupted || job.Status == "interrupted" || job.Status == "uncertain"
		}
		videoGroup := job.ID
		if d.Share != nil {
			p.ShareSTRM = true
			if d.Share.LeaderID != "" {
				videoGroup = d.Share.LeaderID
			}
			if job.Status == "completed" || d.Share.HandedOff {
				shares[videoGroup] = shares[videoGroup] || d.Share.HandedOff
			}
		}
		for _, item := range job.Items {
			if item.File.MediaType == "video" {
				videos[videoGroup+":"+item.File.ID] = true
			}
		}
	}
	p.VideoCount = len(videos)
	// 沿原资源顺序读取每个唯一分享批次，合并主任务的项目不会重复累计。
	for _, job := range jobs {
		shareID := job.ID
		if job.Download.Share != nil && job.Download.Share.LeaderID != "" {
			shareID = job.Download.Share.LeaderID
		}
		handedOff, exists := shares[shareID]
		if !exists {
			continue
		}
		delete(shares, shareID)
		share := DownloadPipelineShare{ID: shareID, Status: "queued", RetryReason: "尚未交接分享队列"}
		var err error
		if reader != nil {
			share, err = reader.DownloadShareProgress(ctx, shareID)
		} else {
			err = sql.ErrNoRows
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return p, fmt.Errorf("读取关联分享日志: %w", err)
		}
		if errors.Is(err, sql.ErrNoRows) {
			share = DownloadPipelineShare{ID: shareID, Status: "queued", RetryReason: "尚未交接分享队列"}
			if handedOff {
				share.Status, share.Error = "interrupted", "关联分享日志不存在，请核对原任务"
			}
		}
		p.Shares = append(p.Shares, share)
		p.Written += share.Written
		if share.Error != "" {
			issues = append(issues, share.Error)
		}
		switch share.Status {
		case "success":
			success++
		case "review", "waiting_review":
			review++
		case "failed", "cancelled", "interrupted":
			failedShares++
			interrupted = interrupted || share.Status == "interrupted"
		default:
			generating++
		}
	}
	p.Message = fmt.Sprintf("资源 %d/%d 已就绪 · %d/%d 已整理", p.Ready, expected, p.Organized, expected)
	if p.ShareSTRM {
		p.Message += fmt.Sprintf(" · STRM 已写入 %d 个", p.Written)
	}
	if review > 0 {
		p.Message += fmt.Sprintf(" · %d 批分享待审核", review)
	}
	if p.Failed+failedShares > 0 {
		p.Message += fmt.Sprintf(" · %d 项失败或中断", p.Failed+failedShares)
	}
	if len(issues) > 0 {
		reason := []rune(strings.Join(strings.Fields(issues[0]), " "))
		if len(reason) > 120 {
			reason = append(reason[:120], '…')
		}
		p.Message += " · " + string(reason)
		if len(issues) > 1 {
			p.Message += fmt.Sprintf("（另有 %d 条提示）", len(issues)-1)
		}
	}
	p.Total = expected
	switch {
	case expected != len(jobs):
		p.Status, p.Finished = "interrupted", true
		p.Message = "批次资源记录不完整，请核对任务历史"
	case organizing > 0:
		p.Status = "organizing"
	case downloading > 0:
		p.Status = "downloading"
	case queued > 0:
		p.Status = "queued"
	case generating > 0:
		p.Status = "generating"
	case review > 0:
		p.Status = "review"
	default:
		p.Finished = true
		switch {
		case p.Failed+failedShares == 0:
			p.Status = "success"
		case success > 0 || p.Organized > 0 && p.Failed > 0:
			p.Status = "partial"
		case interrupted:
			p.Status = "interrupted"
		default:
			p.Status = "failed"
		}
	}
	return p, nil
}
