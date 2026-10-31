package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

func driveSTRMTestDrainAttachments(t *testing.T, s *DriveSTRMService) {
	t.Helper()
	for i := 0; i < 100; i++ {
		job, workCtx, _, found, err := s.nextAttachment(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			return
		}
		cookie, downloadErr := s.downloadAttachment(workCtx, job)
		if err := s.finishAttachment(context.Background(), workCtx, job, cookie, downloadErr); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("attachment queue did not yield")
}

func driveSTRMTestPoster() p115.ShareFile {
	return p115.ShareFile{ID: "10", Name: "poster.jpg", Size: 5, PickCode: "ecjq9ichcb40lzlvx", SHA1: strings.Repeat("B", 40)}
}

func TestDriveSTRMAttachmentsRestartWithoutRescanAndRenameWithoutRequests(t *testing.T) {
	s, db, client, mapping, root := driveSTRMTestSetup(t)
	ctx := context.Background()
	client.files["100"] = []p115.ShareFile{driveSTRMTestPoster(), driveSTRMTestVideo("1", "movie.mkv")}
	client.metadata["10"] = []byte("image")
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	if client.metadataCalls != 0 {
		t.Fatal("scan downloaded attachments inline")
	}
	before := len(client.calls)
	mapping.Name = "renamed"
	if _, err := s.Save(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != before {
		t.Fatal("rename queried the cloud")
	}
	account, _ := s.Accounts.Get(mapping.AccountID)
	refreshed := "UID=42_B"
	if err := s.Accounts.Update(account.ID, account.Revision, "renamed account", &refreshed); err != nil {
		t.Fatal(err)
	}
	s = NewDriveSTRMService(db, s.Accounts, client, root)
	values, err := s.List(ctx)
	if err != nil || len(values) != 1 || values[0].MetadataPending != 1 || values[0].MetadataFailed != 0 {
		t.Fatalf("pending projection=%+v err=%v", values, err)
	}
	driveSTRMTestDrainAttachments(t, s)
	if len(client.calls) != before+1 || !strings.HasPrefix(client.calls[before], "metadata:") {
		t.Fatalf("restart triggered a rescan: %v", client.calls[before:])
	}
	if !strings.HasSuffix(client.calls[before], ":"+refreshed) {
		t.Fatal("queued attachment did not use refreshed primary credentials")
	}
	if _, _, found, err := s.next(ctx); err != nil || found {
		t.Fatalf("restart queued a scan: found=%v err=%v", found, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "strm", "poster.jpg"))
	if err != nil || string(data) != "image" {
		t.Fatalf("resumed download=%q err=%v", data, err)
	}
}

func TestDriveSTRMAttachmentLifecycleGuardsFinalWrite(t *testing.T) {
	for _, action := range []string{"pause", "stop", "delete", "root", "refresh", "clear", "replace", "delete-account"} {
		t.Run(action, func(t *testing.T) {
			s, db, client, mapping, root := driveSTRMTestSetup(t)
			ctx := context.Background()
			client.files["100"] = []p115.ShareFile{driveSTRMTestPoster()}
			client.metadata["10"] = []byte("image")
			if _, err := driveSTRMTestAttempt(t, s); err != nil {
				t.Fatal(err)
			}
			client.onMetadata = func() {
				var err error
				switch action {
				case "pause":
					_, err = s.SetMonitoring(ctx, mapping.ID, false)
				case "stop":
					err = s.Stop(ctx, mapping.ID)
				case "delete":
					err = s.Delete(ctx, mapping.ID)
				case "root":
					mapping.OutputDir = "other-strm"
					_, err = s.Save(ctx, mapping)
				case "refresh", "clear", "replace", "delete-account":
					account, _ := s.Accounts.Get(mapping.AccountID)
					cookie := "UID=42_B"
					switch action {
					case "clear":
						cookie = ""
					case "replace":
						cookie = "UID=99_A"
					}
					if action == "delete-account" {
						err = s.Accounts.Delete(account.ID, account.Revision)
					} else {
						err = s.Accounts.Update(account.ID, account.Revision, account.Name, &cookie)
					}
				}
				if err != nil {
					t.Error(err)
				}
			}
			driveSTRMTestDrainAttachments(t, s)
			_, err := os.Stat(filepath.Join(root, "strm", "poster.jpg"))
			if action == "pause" || action == "refresh" {
				if err != nil {
					t.Fatalf("%s canceled accepted work: %v", action, err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stale attachment was written: %v", err)
			}
			counts, err := db.DriveSTRMAttachmentCounts(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if action == "clear" || action == "replace" || action == "delete-account" {
				if counts[mapping.ID] != [2]int{1, 1} || client.metadataCalls != 1 {
					t.Fatalf("unavailable account continued: counts=%v requests=%d", counts, client.metadataCalls)
				}
			} else if counts[mapping.ID][0] != 0 {
				t.Fatalf("pending work survived %s: %v", action, counts)
			}
			if client.metadataCalls != 1 || !strings.HasSuffix(client.calls[len(client.calls)-1], ":UID=42_A") {
				t.Fatal("active attachment replaced its fixed credentials")
			}
		})
	}
}

func TestDriveSTRMSlowAttachmentDoesNotBlockOtherMappingSTRM(t *testing.T) {
	s, _, client, mapping, root := driveSTRMTestSetup(t)
	client.files["100"] = []p115.ShareFile{driveSTRMTestPoster()}
	client.metadata["10"] = []byte("image")
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	client.onDownload = func(ctx context.Context) { close(started); <-ctx.Done() }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("workers did not stop")
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("attachment worker did not start")
	}
	client.mu.Lock()
	client.files["300"] = []p115.ShareFile{driveSTRMTestVideo("2", "other.mkv")}
	client.mu.Unlock()
	if _, err := s.Save(ctx, model.DriveSTRM{Name: "other", AccountID: mapping.AccountID, SourceCID: "300", OutputDir: "other-strm"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "other-strm", "other.strm")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow attachment blocked another mapping's video scan")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDriveSTRMAttachmentThrottleCancellationAndNoCatchUpBurst(t *testing.T) {
	s := &DriveSTRMService{}
	settings := model.DefaultDriveSTRMSettings()
	settings.MetadataFilesPerSecond = 10
	if err := s.waitAttachment(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	first := s.metadataStarted
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := s.waitAttachment(ctx, settings); !errors.Is(err, context.DeadlineExceeded) || !s.metadataStarted.Equal(first) {
		t.Fatalf("canceled wait consumed allowance: %v", err)
	}
	// Simulate a slow first request. The next can begin immediately, but the
	// request after it must still observe a full interval.
	s.metadataStarted = time.Now().Add(-time.Second)
	if err := s.waitAttachment(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	second := s.metadataStarted
	if err := s.waitAttachment(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if s.metadataStarted.Sub(second) < 95*time.Millisecond {
		t.Fatal("slow request permitted a catch-up burst")
	}
}

func TestDriveSTRMQueuedScanDoesNotRestartAcceptedAttachment(t *testing.T) {
	s, _, client, mapping, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	client.files["100"] = []p115.ShareFile{driveSTRMTestPoster()}
	client.metadata["10"] = []byte("image")
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	job, workCtx, _, found, err := s.nextAttachment(ctx)
	if err != nil || !found {
		t.Fatalf("attachment not started: %v", err)
	}
	client.onMetadata = func() {
		if err := s.NotifyDirectories(ctx, mapping.AccountID, []string{"100"}); err != nil {
			t.Error(err)
		}
		if _, _, found, err := s.next(ctx); err != nil || found {
			t.Errorf("scan started during the same mapping's attachment: %v", err)
		}
		if workCtx.Err() != nil {
			t.Error("life event canceled an accepted attachment")
		}
	}
	cookie, downloadErr := s.downloadAttachment(workCtx, job)
	if downloadErr != nil {
		t.Fatal(downloadErr)
	}
	if err := s.finishAttachment(ctx, workCtx, job, cookie, downloadErr); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := s.next(ctx); err != nil || !found || client.metadataCalls != 1 {
		t.Fatalf("queued scan failed to resume or attachment repeated: found=%v requests=%d err=%v", found, client.metadataCalls, err)
	}
	if err := s.Stop(ctx, mapping.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.finish(ctx, mapping.ID, model.DriveSTRMStats{}, context.Canceled); err != nil {
		t.Fatal(err)
	}
}

func TestDriveSTRMInvalidAttachmentDoesNotRequest115OrFailScan(t *testing.T) {
	s, db, client, mapping, _ := driveSTRMTestSetup(t)
	file := driveSTRMTestPoster()
	file.Size = p115.MaxMetadataSize + 1
	client.files["100"] = []p115.ShareFile{file, driveSTRMTestVideo("1", "movie.mkv")}
	stats, err := driveSTRMTestAttempt(t, s)
	if err != nil || stats.Written != 1 || stats.Failed != 0 {
		t.Fatalf("invalid attachment failed the scan: %+v %v", stats, err)
	}
	before := len(client.calls)
	driveSTRMTestDrainAttachments(t, s)
	counts, _ := db.DriveSTRMAttachmentCounts(context.Background())
	if client.metadataCalls != 0 || len(client.calls) != before || counts[mapping.ID] != [2]int{1, 1} {
		t.Fatalf("invalid attachment requested 115: calls=%v counts=%v", client.calls[before:], counts)
	}
}

type driveSTRMQueueDeleteFailure struct{ DriveSTRMStore }

func (driveSTRMQueueDeleteFailure) DeleteDriveSTRMAttachments(context.Context, string) error {
	return errors.New("queue deletion failed")
}

func TestDriveSTRMStopCancelsScanEvenWhenQueueCleanupFails(t *testing.T) {
	s, db, _, mapping, _ := driveSTRMTestSetup(t)
	_, scanCtx, found, err := s.next(context.Background())
	if err != nil || !found {
		t.Fatalf("scan not started: %v", err)
	}
	s.Store = driveSTRMQueueDeleteFailure{s.Store}
	if err := s.Stop(context.Background(), mapping.ID); err == nil {
		t.Fatal("queue deletion failure was hidden")
	}
	if !errors.Is(scanCtx.Err(), context.Canceled) {
		t.Fatal("scan continued after the stopped state was committed")
	}
	saved, _ := db.GetDriveSTRM(context.Background(), mapping.ID)
	if saved.Status != "stopped" {
		t.Fatalf("stop was not persisted: %+v", saved)
	}
}
