package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

type pipelineShareReader struct {
	tasks map[string]DownloadPipelineShare
	reads int
}

func (r *pipelineShareReader) SnapshotDownloadShare(string) (AutomationSettings, error) {
	return AutomationSettings{}, nil
}
func (r *pipelineShareReader) EnqueueDownloadShare(context.Context, OrganizeJob, string) error {
	return nil
}
func (r *pipelineShareReader) DownloadShareProgress(_ context.Context, id string) (DownloadPipelineShare, error) {
	r.reads++
	if task, ok := r.tasks[id]; ok {
		return task, nil
	}
	return DownloadPipelineShare{}, sql.ErrNoRows
}

func pipelineJob(id, status string) OrganizeJob {
	job := OrganizeJob{ID: id, Title: "测试剧集", CreatedAt: time.Now(), Status: status, Download: &ResourceDownload{DirectoryID: "10", Share: &DownloadShare{HandedOff: status == "completed"}}}
	item := OrganizeItem{Status: "pending"}
	item.File.ID, item.File.MediaType = id+"-video", "video"
	if status == "completed" {
		item.Status = "done"
	}
	job.Items = []OrganizeItem{item}
	return job
}

func TestDownloadPipelineTracksShareLifecycle(t *testing.T) {
	for _, tc := range []struct {
		share, status string
		finished      bool
	}{
		{"queued", "generating", false}, {"running", "generating", false}, {"retry_queued", "generating", false},
		{"waiting_review", "review", false}, {"review", "review", false}, {"success", "success", true},
		{"failed", "failed", true}, {"interrupted", "interrupted", true},
	} {
		t.Run(tc.share, func(t *testing.T) {
			job := pipelineJob("one", "completed")
			reader := &pipelineShareReader{tasks: map[string]DownloadPipelineShare{"one": {ID: "one", Status: tc.share, Written: 2, Total: 3}}}
			p, err := aggregateDownloadPipeline(context.Background(), "one", []OrganizeJob{job}, reader)
			if err != nil || p.Status != tc.status || p.Finished != tc.finished || p.Written != 2 || p.Organized != 1 || reader.reads != 1 {
				t.Fatalf("分享阶段被提前结束: %+v, %v", p, err)
			}
		})
	}
}

func TestDownloadPipelineIncludesLateResourcesAndDeduplicatesLeader(t *testing.T) {
	one, two, late := pipelineJob("one", "completed"), pipelineJob("two", "completed"), pipelineJob("late", "downloading")
	for _, j := range []*OrganizeJob{&one, &two, &late} {
		j.Download.BatchID, j.Download.BatchSize = "batch", 3
	}
	one.Download.Share.LeaderID, two.Download.Share.LeaderID = "one", "one"
	one.Items = append(one.Items, two.Items...)
	reader := &pipelineShareReader{tasks: map[string]DownloadPipelineShare{"one": {ID: "one", Status: "success", Written: 2}}}
	s := &DownloadOrganize{Organize: &OrganizeEngine{doc: organizeDocument{Jobs: []OrganizeJob{one, two, late}}}, Shares: reader}
	progress, err := s.Progress(context.Background(), []string{"batch", "two"})
	if err != nil || len(progress) != 1 {
		t.Fatal(progress, err)
	}
	p := progress[0]
	if p.Status != "downloading" || p.Finished || p.Written != 2 || len(p.Shares) != 1 || p.VideoCount != 3 || p.Total != 3 || p.Organized != 2 || p.Ready != 2 || reader.reads != 1 {
		t.Fatalf("重复统计主任务或遗漏晚到资源: %+v", p)
	}
	late = pipelineJob("late", "completed")
	late.Download.BatchID, late.Download.BatchSize = "batch", 3
	s.Organize.doc.Jobs[2] = late
	reader.tasks["late"] = DownloadPipelineShare{ID: "late", Status: "success", Written: 1}
	progress, err = s.Progress(context.Background(), []string{"batch"})
	if err != nil || progress[0].Status != "success" || !progress[0].Finished || progress[0].Written != 3 || len(progress[0].Shares) != 2 {
		t.Fatal(progress, err)
	}
}

func TestDownloadPipelineDoesNotSucceedForMissingRecords(t *testing.T) {
	job := pipelineJob("one", "completed")
	s := &DownloadOrganize{Organize: &OrganizeEngine{doc: organizeDocument{Jobs: []OrganizeJob{job}}}}
	p, err := s.Progress(context.Background(), []string{"one"})
	if err != nil || p[0].Status != "interrupted" || !p[0].Finished {
		t.Fatal(p, err)
	}
	job.Download.BatchID, job.Download.BatchSize = "batch", 2
	job.Download.Share = nil
	s.Organize.doc.Jobs[0] = job
	p, err = s.Progress(context.Background(), []string{"batch"})
	if err != nil || p[0].Status != "interrupted" || p[0].Total != 2 {
		t.Fatal(p, err)
	}
	if _, err = s.Progress(context.Background(), []string{"missing"}); !errors.Is(err, ErrOrganizeConflict) {
		t.Fatal(err)
	}
	if _, err = s.Progress(context.Background(), []string{""}); !errors.Is(err, ErrOrganizeConflict) {
		t.Fatal(err)
	}
}

func TestDownloadPipelineFailedCollectionHasNoPhantomShare(t *testing.T) {
	job := pipelineJob("one", "blocked")
	job.Download.Share.LeaderID = job.ID
	p, err := aggregateDownloadPipeline(context.Background(), "one", []OrganizeJob{job}, nil)
	if err != nil || !p.Finished || p.Status != "failed" || len(p.Shares) != 0 {
		t.Fatal(p, err)
	}
}

func TestDownloadPipelineShareUsesExistingRetryRulesAndHidesRequest(t *testing.T) {
	s, db, _ := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) { return nil, nil }))
	s.AccountCookie = func(string) string { return "private-cookie" }
	req, err := json.Marshal(shareTaskRequest{Download: &downloadShareRequest{}, Input: GenerateInput{Mode: "direct"}, Settings: AutomationSettings{AccountID: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	task := model.ShareTask{ID: "one", Status: "review", Request: req, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err = db.SaveShareTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	p, err := s.DownloadShareProgress(context.Background(), "one")
	if err != nil || !p.CanRetry || p.RetryReason != s.RetryReason(task) {
		t.Fatal(p, err)
	}
	encoded, err := json.Marshal(p)
	if err != nil || strings.Contains(string(encoded), "private-cookie") || strings.Contains(string(encoded), "settings") || strings.Contains(string(encoded), "request") {
		t.Fatal(string(encoded), err)
	}
}

func TestDownloadPipelineRetentionKeepsWholeBatch(t *testing.T) {
	jobs := make([]OrganizeJob, 0, 60)
	for i := 0; i < 60; i++ {
		job := pipelineJob(string(rune('A'+i)), "completed")
		job.Download.BatchID, job.Download.BatchSize = "batch", 60
		jobs = append(jobs, job)
	}
	for i := 0; i < 29; i++ {
		jobs = prependOrganizeJob(pipelineJob("new"+string(rune('A'+i)), "completed"), jobs)
	}
	if len(jobs) != 89 {
		t.Fatalf("同批成员被部分裁剪: %d", len(jobs))
	}
	jobs = prependOrganizeJob(pipelineJob("newest", "completed"), jobs)
	if len(jobs) != 30 {
		t.Fatalf("完整旧批次未统一移出历史: %d", len(jobs))
	}
}

func TestDownloadPipelineRetentionProtectsActiveShare(t *testing.T) {
	for _, status := range []string{"queued", "running", "retry_queued", "review", "waiting_review", "success"} {
		t.Run(status, func(t *testing.T) {
			old := pipelineJob("old", "completed")
			reader := &pipelineShareReader{tasks: map[string]DownloadPipelineShare{"old": {ID: "old", Status: status}}}
			s := &DownloadOrganize{Shares: reader}
			jobs := []OrganizeJob{old}
			for i := 0; i < 29; i++ {
				newJob := pipelineJob("new"+string(rune('A'+i)), "completed")
				newJob.Download.Share = nil
				jobs = append([]OrganizeJob{newJob}, jobs...)
			}
			protected, err := s.activeSharePipelines(jobs)
			if err != nil {
				t.Fatal(err)
			}
			jobs = prependOrganizeJob(pipelineJob("newest", "queued"), jobs, protected)
			want := 31
			if status == "success" {
				want = 30
			}
			if len(jobs) != want || reader.reads != 1 {
				t.Fatalf("活跃分享的整理记录被裁剪: %s %d", status, len(jobs))
			}
		})
	}
}

func TestDownloadPipelineMessageIncludesFailureAndConcurrentReview(t *testing.T) {
	one, two := pipelineJob("one", "completed"), pipelineJob("two", "downloading")
	reader := &pipelineShareReader{tasks: map[string]DownloadPipelineShare{"one": {ID: "one", Status: "waiting_review", Error: "分享仍在审核中"}}}
	p, err := aggregateDownloadPipeline(context.Background(), "batch", []OrganizeJob{one, two}, reader)
	if err != nil || p.Status != "downloading" || !strings.Contains(p.Message, "分享待审核") || !strings.Contains(p.Message, "分享仍在审核中") {
		t.Fatal(p, err)
	}
	two.Status, two.Message = "blocked", "文件身份不一致"
	p, err = aggregateDownloadPipeline(context.Background(), "batch", []OrganizeJob{one, two}, reader)
	if err != nil || !strings.Contains(p.Message, "文件身份不一致") || !strings.Contains(p.Message, "1 项失败或中断") {
		t.Fatal(p, err)
	}
}
