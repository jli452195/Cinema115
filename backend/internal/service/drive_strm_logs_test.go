package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func requireDriveSTRMLog(t *testing.T, logs *GenerationLogs, id, level, message string) GenerationLog {
	t.Helper()
	for _, entry := range logs.Snapshot(level, "STRM") {
		if entry.TaskID == id && entry.Source == "目录生成" && strings.Contains(entry.Message, message) {
			return entry
		}
	}
	t.Fatalf("missing %s log %q for %s: %+v", level, message, id, logs.Snapshot("ALL"))
	return GenerationLog{}
}

func TestDriveSTRMLogsSummarizeGenerationWithoutPerFileSpam(t *testing.T) {
	s, _, client, mapping, _ := driveSTRMTestSetup(t)
	s.Logs = &GenerationLogs{}
	for i := 1; i <= 40; i++ {
		client.files["100"] = append(client.files["100"], driveSTRMTestVideo(fmt.Sprint(i), fmt.Sprintf("Episode %02d.mkv", i)))
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	requireDriveSTRMLog(t, s.Logs, mapping.ID, "INFO", "开始全量生成")
	requireDriveSTRMLog(t, s.Logs, mapping.ID, "INFO", "STRM 写入 40，未变化 0，失败 0")
	if entries := s.Logs.Snapshot("ALL"); len(entries) != 2 {
		t.Fatalf("scan logged individual files: %+v", entries)
	}
	if err := s.Queue(context.Background(), mapping.ID, true); err != nil {
		t.Fatal(err)
	}
	before := len(client.calls)
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	requireDriveSTRMLog(t, s.Logs, mapping.ID, "INFO", "STRM 写入 0，未变化 40，失败 0")
	// One root check and twenty two-file pages; logging performs no cloud work.
	if len(client.calls)-before != 21 || len(s.Logs.Snapshot("ALL")) != 4 {
		t.Fatalf("unexpected logging or request overhead: calls=%v logs=%+v", client.calls[before:], s.Logs.Snapshot("ALL"))
	}
}

func TestDriveSTRMLogsDistinguishFailureStopAndShutdown(t *testing.T) {
	for _, action := range []string{"failure", "stop", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			s, db, client, mapping, _ := driveSTRMTestSetup(t)
			s.Logs = &GenerationLogs{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv")}
			switch action {
			case "failure":
				client.pageErr = errors.New("读取失败 https://private.example/?token=private-token Cookie: UID=42_A; SEID=private-secret")
			case "stop":
				client.onList = func(context.Context) {
					if err := s.Stop(context.Background(), mapping.ID); err != nil {
						t.Error(err)
					}
				}
			case "shutdown":
				client.onList = func(context.Context) { cancel() }
			}
			value, scanCtx, found, err := s.next(ctx)
			if err != nil || !found {
				t.Fatalf("start found=%t err=%v", found, err)
			}
			stats, scanErr := s.scan(scanCtx, value)
			if err := s.finish(ctx, value.ID, stats, scanErr); err != nil {
				t.Fatal(err)
			}
			current, err := db.GetDriveSTRM(context.Background(), mapping.ID)
			if err != nil {
				t.Fatal(err)
			}
			if action == "failure" {
				entry := requireDriveSTRMLog(t, s.Logs, mapping.ID, "ERROR", "1m0s 后重试")
				if current.FailureCount != 1 || current.NextScanAt.IsZero() {
					t.Fatalf("logged retry was not saved: %+v", current)
				}
				for _, secret := range []string{"private.example", "private-token", "42_A", "private-secret"} {
					if strings.Contains(entry.Message, secret) {
						t.Fatalf("log leaked %s: %s", secret, entry.Message)
					}
				}
			} else {
				message := "本次任务已停止"
				if action == "shutdown" {
					message = "重启后恢复"
					if !current.Queued {
						t.Fatal("shutdown recovery was not queued")
					}
				}
				requireDriveSTRMLog(t, s.Logs, mapping.ID, "WARNING", message)
				if len(s.Logs.Snapshot("ERROR", "STRM")) != 0 {
					t.Fatal("normal cancellation was reported as an error")
				}
			}
		})
	}
}

func TestDriveSTRMLogsThrottleProgressAndRedactAttachmentRetries(t *testing.T) {
	s, db, client, mapping, _ := driveSTRMTestSetup(t)
	s.Logs = &GenerationLogs{}
	r := driveSTRMScan{service: s, value: mapping, lastProgress: time.Now().Add(-time.Minute), lastLog: time.Now().Add(-time.Minute)}
	if err := r.progress(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.lastProgress = time.Now().Add(-time.Second)
	if err := r.progress(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.Logs.Snapshot("ALL")) != 1 {
		t.Fatal("frequent progress updates flooded the log")
	}
	client.files["100"] = []p115.ShareFile{driveSTRMTestPoster()}
	client.metadataErr["10"] = errors.New("下载失败 https://private.example/file Cookie: UID=42_A; token=private-token")
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	driveSTRMTestDrainAttachments(t, s)
	entry := requireDriveSTRMLog(t, s.Logs, mapping.ID, "WARNING", "poster.jpg")
	if !strings.Contains(entry.Message, "单独重试，不重扫目录") || strings.Contains(entry.Message, "42_A") || strings.Contains(entry.Message, "private-") || strings.Contains(entry.Message, "private.example") {
		t.Fatalf("invalid attachment failure log: %s", entry.Message)
	}
	jobs, err := db.ListDriveSTRMAttachments(context.Background(), 1)
	if err != nil || len(jobs) != 1 || jobs[0].Attempts != 1 || jobs[0].NextAttemptAt.IsZero() {
		t.Fatalf("logged attachment retry not saved: %+v err=%v", jobs, err)
	}
}

func TestDriveSTRMLogsAggregateBackgroundAttachments(t *testing.T) {
	s, _, client, _, _ := driveSTRMTestSetup(t)
	s.Logs = &GenerationLogs{}
	poster := driveSTRMTestPoster()
	other := poster
	other.ID, other.Name = "11", "fanart.jpg"
	client.files["100"] = []p115.ShareFile{poster, other}
	client.metadata["10"], client.metadata["11"] = []byte("image"), []byte("image")
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.runAttachments(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, entry := range s.Logs.Snapshot("INFO", "STRM") {
			if strings.Contains(entry.Message, "完成附件任务 2") {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("background completion summary was not emitted")
		case <-ticker.C:
		}
	}
}
