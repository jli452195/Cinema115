package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestAnimeRSSLogsDescribeCommittedPollingWithoutExtraRequests(t *testing.T) {
	f, db, runner, source, now := discoveryServiceFixture(t)
	f.service.Logs = &GenerationLogs{}
	reads := 0
	var readErr error
	runner.Feeds = animeRSSFunc(func(_ context.Context, b *AnimeSourceBudget, in AnimeSourceInput) (AnimeSourceResult, error) {
		reads++
		if _, err := b.startRequest(); err != nil {
			t.Fatal(err)
		}
		return discoveryServicePage(in, now, 1), readErr
	})
	check := func() {
		t.Helper()
		if _, err := db.Exec(`UPDATE anime_subscription_sources SET next_check_at=? WHERE id=?`, now, source.ID); err != nil {
			t.Fatal(err)
		}
		if err := runner.PollOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	check()
	info := f.service.Logs.Snapshot("INFO", "订阅")
	if len(info) != 1 || info[0].TaskID != source.SubscriptionID || !strings.Contains(info[0].Source, "蜜柑") || !strings.Contains(info[0].Message, "选中 1 条待下载") || !strings.Contains(info[0].Message, "《") {
		t.Fatal("missing RSS identity or actual result", info)
	}
	check()
	if len(f.service.Logs.Snapshot("INFO")) != 1 || len(f.service.Logs.Snapshot("DEBUG")) != 1 {
		t.Fatal("unchanged RSS flooded INFO or paired stage rows", f.service.Logs.Snapshot("ALL"))
	}
	readErr = errors.New("https://private.invalid/feed?token=private-token Cookie: private-cookie")
	check()
	if len(f.service.Logs.Snapshot("WARNING")) != 1 {
		t.Fatal("first read failure missing")
	}
	check()
	if len(f.service.Logs.Snapshot("WARNING")) != 1 {
		t.Fatal("repeated unchanged failure flooded warnings")
	}
	readErr = nil
	check()
	info = f.service.Logs.Snapshot("INFO")
	if len(info) != 2 || !strings.Contains(info[0].Message, "读取已恢复") || reads != 5 {
		t.Fatal("missing recovery or extra feed requests", info, reads)
	}
	if err := runner.VerifyOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runner.VerifyOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	info = f.service.Logs.Snapshot("INFO")
	if len(info) != 3 || !strings.Contains(info[0].Message, "已加入下载队列") || reads != 5 {
		t.Fatal("queue log missing or duplicated", info, reads)
	}
	for _, entry := range f.service.Logs.Snapshot("ALL") {
		if strings.Contains(entry.Message, "private") || strings.Contains(entry.Message, "请求 ") || strings.Contains(entry.Message, "耗时") || strings.Contains(entry.Message, "执行结束") {
			t.Fatal("RSS log exposed upstream text or misleading I/O metrics", entry)
		}
	}
}

func TestAnimeSubscriptionLogsRetainTitlesAndSkipReplayedActions(t *testing.T) {
	s, feed, in := newAnimeDefinitionService(t)
	s.Logs = &GenerationLogs{}
	ctx := context.Background()
	before := feed.reads
	created, err := s.SaveDefinition(ctx, "admin", "logs-create", "", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDefinition(ctx, "admin", "logs-create", "", in); err != nil {
		t.Fatal(err)
	}
	if info := s.Logs.Snapshot("INFO"); len(info) != 1 || !strings.Contains(info[0].Message, "已开始订阅") || info[0].TaskID != created.ID {
		t.Fatal("save log missing or replay duplicated it", info)
	}
	for _, action := range []string{"pause", "resume", "archive", "unarchive", "remove"} {
		sub, err := s.Store.GetAnimeSubscription(ctx, created.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		input := AnimeControlInput{ExpectedRevision: sub.Revision}
		if _, err := s.ControlSubscription(ctx, "admin", "logs-"+action, created.ID, action, input); err != nil {
			t.Fatal(action, err)
		}
		count := len(s.Logs.Snapshot("ALL"))
		if _, err := s.ControlSubscription(ctx, "admin", "logs-"+action, created.ID, action, input); err != nil || len(s.Logs.Snapshot("ALL")) != count {
			t.Fatal("action replay changed logs", action, err)
		}
	}
	info := s.Logs.Snapshot("INFO")
	if len(info) != 6 || !strings.Contains(info[0].Message, "移除订阅已完成") || !strings.Contains(info[0].Message, "《test anime》") || feed.reads != before {
		t.Fatal("removal lost title or operations read upstream", info, feed.reads, before)
	}
}

func TestAnimeLogsScrubDynamicMetadataWithoutTiming(t *testing.T) {
	s := &AnimeAdminService{Logs: &GenerationLogs{}}
	poll := storage.AnimeSourcePoll{Source: model.AnimeSource{Provider: "anibt", GroupName: bindingPtr("字幕组\nCookie: private-cookie")}, Subscription: model.AnimeSubscription{ID: strings.Repeat("a", 32)}, Entry: model.AnimeEntry{Title: "动画 https://private.invalid/path?token=private-token"}}
	s.logAnimeRSSResult(poll, AnimeSourceResult{Cached: true}, storage.AnimeDiscoveryStats{})
	s.logAnimeRSSFailure(poll, "SOURCE_BINDING_CHANGED", 0, true)
	job := model.AnimeDownloadJob{ID: "local-job", Snapshot: model.AnimeDownloadSnapshot{Source: model.AnimeDownloadSource{Provider: "anibt", GroupName: *poll.Source.GroupName}, Decision: model.AnimeDecisionSnapshot{Title: poll.Entry.Title, Parse: model.AnimeReleaseParse{Kind: "episode", Episode: bindingPtr(0)}}}}
	s.logAnimeJob(job, "INFO", "整理完成")
	entries := s.Logs.Snapshot("ALL")
	if len(entries) != 3 || !strings.Contains(entries[0].Message, "第00集：整理完成") || !strings.Contains(entries[1].Message, "已停止自动轮询") || !strings.Contains(entries[2].Message, "暂无新资源") {
		t.Fatal("missing identity, action or retry context", entries)
	}
	for _, entry := range entries {
		text := entry.Source + entry.Message
		if strings.Contains(text, "private") || strings.Contains(text, "https://") || strings.ContainsAny(text, "\r\n") {
			t.Fatal("untrusted display metadata leaked", entry)
		}
	}
}

func TestAnimeJobLogsFollowCommittedStatusWithoutExtraReads(t *testing.T) {
	f, db, job, runner, now := animeStatusFixture(t)
	f.service.Logs = &GenerationLogs{}
	f.service.FileGateway = nil
	reads := 0
	display, progress := "running", float64(10)
	var readErr error
	f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
		reads++
		return serviceAnimeStatus(job, display, progress), readErr
	})
	check := func(want int) {
		t.Helper()
		if _, err := db.Exec(`UPDATE anime_download_jobs SET next_run_at=? WHERE id=?`, *now, job.ID); err != nil {
			t.Fatal(err)
		}
		if err := runner.PollOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if entries := f.service.Logs.Snapshot("ALL"); len(entries) != want {
			t.Fatal("missing committed result or repeated unchanged state", entries)
		}
	}
	check(1)
	progress = 40
	check(1)
	readErr = errors.New("Cookie: private-cookie https://private.invalid/?token=private-token")
	check(2)
	check(2)
	readErr, display, progress = nil, "finished", 100
	check(3)
	check(3)
	entries := f.service.Logs.Snapshot("ALL")
	if reads != 6 || entries[0].Level != "INFO" || !strings.Contains(entries[0].Message, "离线完成") || entries[1].Level != "WARNING" || !strings.Contains(entries[1].Message, "自动重试") || !strings.Contains(entries[2].Message, "正在下载") {
		t.Fatal("incorrect status results or extra requests", reads, entries)
	}
	for _, entry := range entries {
		if entry.TaskID != job.ID || !strings.Contains(entry.Message, "第01集") || strings.Contains(entry.Message, "private") {
			t.Fatal("missing resource identity or leaked upstream error", entry)
		}
	}
}

func TestAnimeJobLogsNeverClaimUncommittedOrConflictingCompletion(t *testing.T) {
	for _, mode := range []string{"rollback", "directory conflict"} {
		t.Run(mode, func(t *testing.T) {
			f, db, job, runner, _ := animeStatusFixture(t)
			f.service.Logs = &GenerationLogs{}
			f.service.FileGateway = nil
			f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
				out := serviceAnimeStatus(job, "finished", 100)
				if mode == "directory conflict" {
					out.TargetCID = "999"
				}
				return out, nil
			})
			if mode == "rollback" {
				if _, err := db.Exec(`CREATE TRIGGER reject_log_status BEFORE UPDATE OF raw_status_json ON anime_download_jobs BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			err := runner.PollOnce(context.Background())
			entries := f.service.Logs.Snapshot("ALL")
			if mode == "rollback" {
				if err == nil || len(entries) != 0 {
					t.Fatal("rolled-back result was logged", err, entries)
				}
			} else if err != nil || len(entries) != 1 || entries[0].Level != "WARNING" || !strings.Contains(entries[0].Message, "不匹配") {
				t.Fatal("conflicting remote completion was presented as success", err, entries)
			}
		})
	}
}

func TestAnimeSubmitLogsAcceptedOnceAndOnlyAfterReceipt(t *testing.T) {
	f, _, session, job := animeSubmitFixture(t)
	f.service.Logs = &GenerationLogs{}
	reads, writes := 0, 0
	f.service.SubmitGateway = &animeSubmitGatewayStub{
		inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
			reads++
			return p115.AnimeTaskObservation{}, nil
		},
		submit: func(context.Context, string, string, string) error {
			writes++
			if entries := f.service.Logs.Snapshot("ALL"); len(entries) != 0 {
				t.Fatal("acceptance logged before receipt", entries)
			}
			return nil
		},
	}
	for i := 0; i < 2; i++ {
		if _, err := f.service.EnsureDownload(context.Background(), session, job.ID); err != nil {
			t.Fatal(err)
		}
	}
	entries := f.service.Logs.Snapshot("ALL")
	if reads != 1 || writes != 1 || len(entries) != 1 || entries[0].Level != "INFO" || !strings.Contains(entries[0].Message, "离线任务已确认") {
		t.Fatal("acceptance duplicated, missing or added requests", reads, writes, entries)
	}
}

func TestAnimeOrganizationLogsActualResultAndCleanupUncertainty(t *testing.T) {
	for _, mode := range []string{"success", "uncertain move", "uncertain cleanup", "checkpoint failure"} {
		t.Run(mode, func(t *testing.T) {
			f, db, session, job, drive := animeOrganizeFixture(t)
			cleanup := animeEnableCleanup(f, drive)
			f.service.Logs = &GenerationLogs{}
			switch mode {
			case "uncertain move":
				drive.lost = "move"
			case "uncertain cleanup":
				cleanup.lostDelete = true
			case "checkpoint failure":
				cleanup.beforeDelete = func() {
					if _, err := db.Exec(`CREATE TRIGGER reject_log_cleanup BEFORE UPDATE ON anime_drive_operations WHEN json_extract(NEW.result_json,'$.cleanup')='recycled' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := f.service.OrganizeDownloadedFiles(context.Background(), session, job.ID)
			entries := f.service.Logs.Snapshot("ALL")
			if mode == "checkpoint failure" {
				if err == nil || len(entries) != 0 {
					t.Fatal("failed checkpoint logged completion", err, entries)
				}
				return
			}
			if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Message, "《黑暗召唤师在地下恋爱中》") || !strings.Contains(entries[0].Message, "第01集") {
				t.Fatal("missing clear organization identity", err, entries)
			}
			entry := entries[0]
			switch mode {
			case "success":
				if entry.Level != "INFO" || !strings.Contains(entry.Message, "整理完成") || drive.reads != 1 || cleanup.deletes != 1 {
					t.Fatal("success missing or extra drive requests", entry, drive, cleanup.deletes)
				}
				organizeTestCall(t, f, session, job.ID)
				if len(f.service.Logs.Snapshot("ALL")) != 1 {
					t.Fatal("completed organization was logged again")
				}
			case "uncertain move":
				if entry.Level != "WARNING" || strings.Contains(entry.Message, "整理完成") || cleanup.deletes != 0 {
					t.Fatal("unknown move claimed success", entry)
				}
			case "uncertain cleanup":
				if entry.Level != "WARNING" || !strings.Contains(entry.Message, "整理完成") || !strings.Contains(entry.Message, "清理结果尚未确认") {
					t.Fatal("cleanup uncertainty lost or hid completed media moves", entry)
				}
			}
		})
	}
}
