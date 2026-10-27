package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type downloadShareFake struct {
	*cleanupFakeDrive
	roots                    []p115.ShareFile
	shares                   map[string][]p115.ShareFile
	sends, configures, reads int
	sendErr, configureErr    error
	pending, review          bool
}

func (f *downloadShareFake) DeleteDirectory(ctx context.Context, cid, cookie string) error {
	return f.DeleteEntries(ctx, []string{cid}, cookie)
}

func (f *downloadShareFake) SendShare(_ context.Context, ids []string, _ string) (*p115.ShareInfo, error) {
	f.sends++
	f.roots = nil
	for _, id := range ids {
		for _, files := range f.dirs {
			for _, file := range files {
				if file.ID == id {
					f.roots = append(f.roots, file)
				}
			}
		}
	}
	code := "ownedabc"
	if f.sends > 1 {
		code += strconv.Itoa(f.sends)
	}
	if f.shares == nil {
		f.shares = make(map[string][]p115.ShareFile)
	}
	f.shares[code] = append([]p115.ShareFile(nil), f.roots...)
	return &p115.ShareInfo{Code: code, Password: "abcd"}, f.sendErr
}

func (f *downloadShareFake) ConfigureShare(_ context.Context, share *p115.ShareInfo, password, _ string) error {
	f.configures++
	if password != "" {
		share.Password = password
	}
	return f.configureErr
}

func (f *downloadShareFake) List(ctx context.Context, share *p115.ShareInfo, cid string, offset int, cookie string) (*p115.SharePage, error) {
	f.reads++
	if cid == "0" {
		roots := f.shares[share.Code]
		return &p115.SharePage{Count: len(roots), Files: roots, UnderReview: f.review}, nil
	}
	page, err := f.DriveFiles(ctx, cid, offset, cookie)
	if err == nil {
		for i := range page.Files {
			page.Files[i].SHA1 = strings.Repeat("A", 40)
		}
		if f.pending {
			page.Files = nil
			page.Count = 0
		}
	}
	return page, err
}

func downloadShareFixture(t *testing.T) (*DownloadOrganize, *ShareTasks, *storage.Store, *downloadShareFake, DownloadOrganizeInput) {
	t.Helper()
	s, drive, _, tracker, _, in := gardenFixture(t)
	client := &downloadShareFake{cleanupFakeDrive: &cleanupFakeDrive{organizeFakeDrive: drive}}
	tasks, db, _ := setupShareTasks(t, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		return &GenerationResult{Written: 1, Files: make([]GeneratedSTRM, 1)}, nil
	}))
	tasks.OwnClient, tasks.AccountCookie = client, s.Organize.accounts.CookieFor
	tasks.Settings.PrimaryID, tasks.Settings.ValidateAccount = s.Organize.accounts.PrimaryID, s.Organize.accounts.ValidatePrimary
	settings := tasks.Settings.ShareValue()
	settings.Generation.OutputDir, settings.Generation.MinSizeMB, settings.FixedSharePassword = "strm", 0, "xy12"
	settings.ReshareEnabled = true // New action must still generate directly.
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	s.Shares = tasks
	in.ShareSTRM = true
	in.TargetCID, in.TargetPath = "", ""
	tracker.resultID = "801"
	tracker.beforeRead = func() {
		for _, p := range s.live {
			drive.dirs[p.job.Download.DirectoryID] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
			drive.dirs["801"] = []p115.ShareFile{{ID: "901", Name: "[Group] Show [01].mkv", Size: 1200}, {ID: "902", Name: "unselected.mkv", Size: 1300}}
		}
	}
	return s, tasks, db, client, in
}

func completeDownloadShare(t *testing.T, s *DownloadOrganize, in DownloadOrganizeInput) OrganizeJob {
	t.Helper()
	plan, err := s.Preview(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(plan.ID); err != nil {
		t.Fatal(err)
	}
	p := s.live[plan.ID]
	if done, err := s.advance(context.Background(), p); !done || err != nil || p.job.Status != "completed" {
		t.Fatal(done, err, p.job)
	}
	return p.job
}

func TestDownloadShareHandoffAndCanonicalOutput(t *testing.T) {
	s, tasks, db, client, in := downloadShareFixture(t)
	tasks.Generator = &STRMGenerator{Client: client, Root: tasks.Settings.root}
	job := completeDownloadShare(t, s, in)
	if job.Config.TargetCID != "0" || job.Config.TargetPath != "/" || s.Target().TargetCID != "" {
		t.Fatal("share destination coupled to organize config", job.Config, s.Target())
	}
	if !job.Download.Share.HandedOff || !strings.HasPrefix(job.Items[0].Directory, "Cinema115-") {
		t.Fatal(job)
	}
	if len(client.dirs["801"]) != 1 || client.dirs["801"][0].ID != "902" {
		t.Fatal("unselected file moved", client.dirs["801"])
	}
	// Settings changing after acceptance must not alter the saved generation.
	settings := tasks.Settings.ShareValue()
	settings.Generation.OutputDir = "changed"
	settings.FixedSharePassword = "zz99"
	if err := tasks.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	if err := tasks.retryDue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	record, err := db.GetShareTask(context.Background(), job.ID)
	if err != nil || record.Status != "success" || record.Source != "磁力整理" || record.Written != 1 {
		t.Fatal(record, err)
	}
	if client.sends != 1 || client.configures != 1 || client.reads != 3 || len(client.roots) != 1 || strings.HasPrefix(client.roots[0].Name, "Cinema115-") {
		t.Fatal("unexpected sharing or duplicate scan", client)
	}
	output := filepath.Join(tasks.Settings.root, "strm", filepath.FromSlash(strings.TrimPrefix(job.Items[0].Directory, job.Download.Share.Directory+"/")), strings.TrimSuffix(job.Items[0].Name, ".mkv")+".strm")
	data, err := os.ReadFile(output)
	if err != nil || !strings.Contains(string(data), "ownedabc") || !strings.Contains(record.ShareURL, "xy12") {
		t.Fatal("canonical output/settings", output, string(data), err, record.ShareURL)
	}
	if strings.Contains(string(record.Request), "secret-cookie") {
		t.Fatal("persisted credentials")
	}
	var req shareTaskRequest
	if err := json.Unmarshal(record.Request, &req); err != nil {
		t.Fatal(err)
	}
	if req.Download.Generated == nil || !req.Download.Generated.Cleaned || len(client.deleted) != 1 || client.deleted[0] != req.Download.DirectoryID {
		t.Fatal("missing exact-directory cleanup", req.Download, client.deleted)
	}
	for _, root := range client.dirs["0"] {
		if root.ID == req.Download.DirectoryID {
			t.Fatal("completed wrapper still in drive", root)
		}
	}
	if err := tasks.EnqueueDownloadShare(context.Background(), job, tasks.AccountCookie(in.AccountID)); err != nil {
		t.Fatal(err)
	}
	if err := tasks.retryDue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if client.sends != 1 || len(client.deleted) != 1 || len(taskHistory(t, db)) != 1 {
		t.Fatal("duplicate handoff")
	}
	// The destination may have unrelated trees: only the parent listing is read.
	if client.listCalls["30"] != 0 {
		t.Fatal("scanned unrelated destination")
	}
}

func TestDownloadShareReviewAndGenerationRetryReuseLink(t *testing.T) {
	for _, mode := range []string{"pending", "configure", "generate"} {
		t.Run(mode, func(t *testing.T) {
			s, tasks, db, client, in := downloadShareFixture(t)
			job := completeDownloadShare(t, s, in)
			client.pending = mode == "pending"
			if mode == "configure" {
				client.configureErr = errors.New("unavailable")
			}
			if mode == "generate" {
				tasks.Generator = generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
					return nil, errors.New("local write failed")
				})
			}
			if err := tasks.retryDue(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			record, _ := db.GetShareTask(context.Background(), job.ID)
			if record.Status != "waiting_review" && record.Status != "failed" {
				t.Fatal(record)
			}
			if len(client.deleted) != 0 {
				t.Fatal("recycled incomplete generation", client.deleted)
			}
			if reason := tasks.RetryReason(record); reason != "" {
				t.Fatal(reason)
			}
			client.review, client.pending, client.configureErr = false, false, nil
			tasks.Generator = generateFunc(func(_ context.Context, input GenerateInput) (*GenerationResult, error) {
				if input.Mode != "direct" || !strings.Contains(input.URL, "ownedabc") {
					t.Fatal(input)
				}
				return &GenerationResult{Written: 1, Files: make([]GeneratedSTRM, 1)}, nil
			})
			if _, err := tasks.RetryTasks(context.Background(), []string{job.ID}); err != nil {
				t.Fatal(err)
			}
			if err := tasks.retryDue(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			record, _ = db.GetShareTask(context.Background(), job.ID)
			if record.Status != "success" || client.sends != 1 || client.renames != 1 || client.moves != 1 {
				t.Fatal(record, client)
			}
			if mode != "configure" && client.configures != 1 {
				t.Fatal("reconfigured an already confirmed share")
			}
		})
	}
}

func TestDownloadShareUncertainCreationNeverRepeats(t *testing.T) {
	s, tasks, db, client, in := downloadShareFixture(t)
	job := completeDownloadShare(t, s, in)
	client.sendErr = errors.New("response lost")
	if err := tasks.retryDue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	record, _ := db.GetShareTask(context.Background(), job.ID)
	if record.Status != "failed" || !strings.Contains(tasks.RetryReason(record), "不会重复创建") {
		t.Fatal(record, tasks.RetryReason(record))
	}
	result, err := tasks.RetryTasks(context.Background(), []string{job.ID})
	if err != nil || result[0].Accepted || client.sends != 1 {
		t.Fatal(result, err)
	}
	if err := tasks.retryDue(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if client.sends != 1 {
		t.Fatal("repeated uncertain share")
	}
}

func TestDownloadShareRejectsChangedTreeAndCredentials(t *testing.T) {
	for _, mode := range []string{"extra-file", "changed-cookie"} {
		t.Run(mode, func(t *testing.T) {
			s, tasks, db, client, in := downloadShareFixture(t)
			job := completeDownloadShare(t, s, in)
			if mode == "changed-cookie" {
				tasks.AccountCookie = func(string) string { return "UID=456_changed" }
			} else {
				cid := job.Items[0].TargetCID
				client.dirs[cid] = append(client.dirs[cid], p115.ShareFile{ID: "999", Name: "unrelated.mkv", Size: 1})
			}
			if err := tasks.retryDue(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			record, _ := db.GetShareTask(context.Background(), job.ID)
			if client.sends != 0 || (record.Status != "failed" && record.Status != "cancelled") {
				t.Fatal(record)
			}
		})
	}
}

func TestDownloadShareIncompleteHandoffCanResume(t *testing.T) {
	s, tasks, _, client, in := downloadShareFixture(t)
	job := completeDownloadShare(t, s, in)
	job.Download.Share.HandedOff = false // crash after completion, before the receipt
	if err := s.Organize.checkpoint(&job); err != nil {
		t.Fatal(err)
	}
	delete(s.live, job.ID)
	if !s.History()[0].Resumable {
		t.Fatal("lost handoff cannot recover")
	}
	if _, err := s.Resume(job.ID); err != nil {
		t.Fatal(err)
	}
	p := s.live[job.ID]
	if done, err := s.advance(context.Background(), p); !done || err != nil {
		t.Fatal(done, err)
	}
	if !p.job.Download.Share.HandedOff || client.renames != 1 || client.moves != 1 {
		t.Fatal("repeated organize on handoff recovery")
	}
	if err := tasks.retryDue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if client.sends != 1 {
		t.Fatal(client.sends)
	}
}

func TestDownloadShareConfigOnlyAffectsNewAction(t *testing.T) {
	s, tasks, _, client, in := downloadShareFixture(t)
	tasks.Settings.value.Generation.BaseURL = ""
	if _, err := s.Preview(context.Background(), in); !errors.Is(err, ErrOrganizeInput) {
		t.Fatal(err)
	}
	in.ShareSTRM = false
	in.TargetCID, in.TargetPath = "20", "/目标"
	plan, err := s.Preview(context.Background(), in)
	if err != nil || plan.Download.Share != nil || strings.Contains(plan.Items[0].Directory, "Cinema115-") || client.sends != 0 {
		t.Fatal(plan, err)
	}
	copy := cloneOrganizeJob(plan)
	copy.Download.Share = &DownloadShare{Directory: "one"}
	other := cloneOrganizeJob(copy)
	other.Download.Share.Directory = "two"
	if copy.Download.Share.Directory != "one" {
		t.Fatal("shared mutable snapshot")
	}
	data, _ := json.Marshal(copy)
	if strings.Contains(string(data), "secret-cookie") {
		t.Fatal("persisted credentials")
	}
}

type downloadShareFailStore struct {
	ShareTaskStore
	stage string
}

func (s downloadShareFailStore) SaveShareTask(ctx context.Context, task model.ShareTask) error {
	var req shareTaskRequest
	if err := json.Unmarshal(task.Request, &req); err != nil {
		return err
	}
	if req.Download != nil {
		beforeSend := req.Download.Attempted && req.Input.URL == ""
		afterSend := req.Input.URL != "" && !req.Download.Configured
		beforeCleanup := req.Download.Generated != nil && !req.Download.Generated.Cleaned
		afterCleanup := req.Download.Generated != nil && req.Download.Generated.Cleaned
		if s.stage == "before-send" && beforeSend || s.stage == "after-send" && afterSend || s.stage == "before-cleanup" && beforeCleanup || s.stage == "after-cleanup" && afterCleanup {
			return errors.New("checkpoint unavailable")
		}
	}
	return s.ShareTaskStore.SaveShareTask(ctx, task)
}

func TestDownloadShareDurableBoundariesAndRestart(t *testing.T) {
	for _, stage := range []string{"before-send", "after-send"} {
		t.Run(stage, func(t *testing.T) {
			s, tasks, db, client, in := downloadShareFixture(t)
			job := completeDownloadShare(t, s, in)
			tasks.Store = downloadShareFailStore{ShareTaskStore: db, stage: stage}
			if err := tasks.retryDue(context.Background(), time.Now()); err == nil {
				t.Fatal("checkpoint failure was swallowed")
			}
			if err := db.InterruptShareTasks(context.Background()); err != nil {
				t.Fatal(err)
			}
			record, _ := db.GetShareTask(context.Background(), job.ID)
			if record.Status != "interrupted" || client.configures != 0 {
				t.Fatal(record)
			}
			reason := tasks.RetryReason(record)
			if stage == "before-send" {
				if reason != "" || client.sends != 0 {
					t.Fatal(reason, client.sends)
				}
			} else if !strings.Contains(reason, "不会重复创建") || client.sends != 1 {
				t.Fatal(reason, client.sends)
			}
		})
	}
}

func TestDownloadShareBatchPreservesSubtitlesAndRequestReuse(t *testing.T) {
	s, drive, in, reads := downloadBatchFixture(t, 3)
	_, tasks, _, _, _ := downloadShareFixture(t)
	tasks.Settings.PrimaryID, tasks.Settings.ValidateAccount = s.Organize.accounts.PrimaryID, s.Organize.accounts.ValidatePrimary
	s.Shares = tasks
	in.ShareSTRM = true
	preview, err := s.PreviewBatch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	root := preview.Jobs[0].Download.Share.Directory
	for _, job := range preview.Jobs {
		if job.Download.Share.Directory != root || len(job.Items) != 2 || job.Items[1].File.MediaType != "subtitle" {
			t.Fatal(job)
		}
		for _, item := range job.Items {
			if !strings.HasPrefix(item.Directory, root+"/") || !strings.Contains(item.TargetPath, root) || item.Conflict != "" {
				t.Fatal(item)
			}
		}
	}
	if reads.Load() != 3 || drive.listCalls["0"] != 1 || drive.listCalls["20"] != 0 {
		t.Fatal("batch repeated manifest/parent reads", reads.Load(), drive.listCalls)
	}
	accepted, err := s.StartBatch(preview.ID)
	if err != nil || len(accepted.Jobs) != 3 {
		t.Fatal(accepted, err)
	}
	if s.Target().TargetCID != "" {
		t.Fatal("share batch overwrote ordinary organize destination", s.Target())
	}
	acceptedAgain, err := s.StartBatch(preview.ID)
	if err != nil || acceptedAgain.ID != accepted.ID || len(s.live) != 3 {
		t.Fatal("non-idempotent batch", acceptedAgain, err)
	}
}

func TestDownloadShareAllowsRootCloudDownload(t *testing.T) {
	s, tasks, _, _, in := downloadShareFixture(t)
	s.Settings.value.TargetCID, s.Settings.value.TargetPath = "0", "/"
	job := completeDownloadShare(t, s, in)
	if job.Download.ParentCID != "0" || !job.Download.Share.HandedOff {
		t.Fatal(job)
	}
	if err := tasks.retryDue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadShareReadableOwnerSnapshotDoesNotWaitForReview(t *testing.T) {
	s, tasks, db, client, in := downloadShareFixture(t)
	tasks.Generator = &STRMGenerator{Client: client, Root: tasks.Settings.root}
	job := completeDownloadShare(t, s, in)
	client.review = true // The public review flag does not block owner generation.
	if err := tasks.retryDue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	record, err := db.GetShareTask(context.Background(), job.ID)
	if err != nil || record.Status != "success" || record.Written != 1 || record.NextRetryAt != nil {
		t.Fatal(record, err)
	}
	if client.sends != 1 || client.configures != 1 || client.reads != 3 {
		t.Fatal("unexpected review waits or repeated requests", client)
	}
}

func TestDownloadSharePartialGenerationRetainsDirectory(t *testing.T) {
	for _, mode := range []string{"nil", "empty", "strm-failed", "metadata-failed"} {
		t.Run(mode, func(t *testing.T) {
			s, tasks, db, client, in := downloadShareFixture(t)
			job := completeDownloadShare(t, s, in)
			tasks.Generator = generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
				result := &GenerationResult{Written: 1, Files: make([]GeneratedSTRM, 1)}
				switch mode {
				case "nil":
					return nil, nil
				case "empty":
					result.Written = 0
				case "strm-failed":
					result.Failed = 1
				case "metadata-failed":
					result.MetadataFailed = 1
				}
				return result, nil
			})
			if err := tasks.retryDue(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			record, err := db.GetShareTask(context.Background(), job.ID)
			if err != nil || record.Status != "failed" || len(client.deleted) != 0 || tasks.RetryReason(record) != "" {
				t.Fatal(record, client.deleted, err)
			}
		})
	}
}

func TestDownloadShareCleanupRecovery(t *testing.T) {
	for _, stage := range []string{"delete-failed", "response-lost", "before-cleanup", "after-cleanup"} {
		t.Run(stage, func(t *testing.T) {
			s, tasks, db, client, in := downloadShareFixture(t)
			job := completeDownloadShare(t, s, in)
			generated := 0
			tasks.Generator = generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
				generated++
				return &GenerationResult{Written: 1, Files: make([]GeneratedSTRM, 1), MetadataWritten: 2, MetadataFiles: make([]GeneratedMetadata, 2), Directory: "strm", ConflictRenamed: 1}, nil
			})
			if stage == "delete-failed" || stage == "response-lost" {
				client.deleteErr = errors.New("delete response unavailable")
				client.applyOnError = stage == "response-lost"
			} else {
				tasks.Store = downloadShareFailStore{ShareTaskStore: db, stage: stage}
			}
			err := tasks.retryDue(context.Background(), time.Now())
			if (stage == "before-cleanup" || stage == "after-cleanup") != (err != nil) {
				t.Fatal("checkpoint failure must stop worker", err)
			}
			record, _ := db.GetShareTask(context.Background(), job.ID)
			if stage == "before-cleanup" {
				if len(client.deleted) != 0 {
					t.Fatal("deleted before generation receipt was durable")
				}
			} else if record.Written != 1 || record.Total != 1 || len(client.deleted) != 1 {
				t.Fatal("lost generation counts or unexpected cleanup", record, client.deleted)
			}
			if err := db.InterruptShareTasks(context.Background()); err != nil {
				t.Fatal(err)
			}
			client.deleteErr = nil
			restarted := &ShareTasks{Store: db, Settings: tasks.Settings, Generator: tasks.Generator, OwnClient: client, AccountCookie: tasks.AccountCookie}
			if results, err := restarted.RetryTasks(context.Background(), []string{job.ID}); err != nil || !results[0].Accepted {
				t.Fatal(results, err)
			}
			reads := client.reads
			lists := 0
			for _, n := range client.listCalls {
				lists += n
			}
			if err := restarted.retryDue(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			record, _ = db.GetShareTask(context.Background(), job.ID)
			var req shareTaskRequest
			if err := json.Unmarshal(record.Request, &req); err != nil {
				t.Fatal(err)
			}
			if record.Status != "success" || record.Written != 1 || record.Total != 1 || req.Download.Generated == nil || !req.Download.Generated.Cleaned || req.Download.Generated.MetadataCount != 2 || req.Download.Generated.ConflictRenamed != 1 {
				t.Fatal(record, req.Download)
			}
			wantGenerated := 1
			if stage == "before-cleanup" {
				wantGenerated = 2 // No durable receipt: generation must be confirmed again.
			} else {
				for _, n := range client.listCalls {
					lists -= n
				}
				if client.reads != reads || lists != 0 {
					t.Fatal("cleanup retry scanned or read the share again")
				}
			}
			if generated != wantGenerated || client.sends != 1 || client.configures != 1 || client.renames != 1 || client.moves != 1 {
				t.Fatal("repeated cloud writes or generation", generated, client)
			}
			for _, cid := range client.deleted {
				if cid != req.Download.DirectoryID || cid == "0" || cid == "801" {
					t.Fatal("recycled unrelated directory", client.deleted)
				}
			}
		})
	}
}

func TestDownloadShareCleanupRetryRejectsChangedCredentials(t *testing.T) {
	s, tasks, db, client, in := downloadShareFixture(t)
	job := completeDownloadShare(t, s, in)
	client.deleteErr = errors.New("unavailable")
	if err := tasks.retryDue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	client.deleteErr = nil
	tasks.AccountCookie = func(string) string { return "UID=456_changed" }
	record, _ := db.GetShareTask(context.Background(), job.ID)
	if reason := tasks.RetryReason(record); !strings.Contains(reason, "凭据已变化") {
		t.Fatal(reason)
	}
	if results, err := tasks.RetryTasks(context.Background(), []string{job.ID}); err != nil || results[0].Accepted || len(client.deleted) != 1 {
		t.Fatal(results, err, client.deleted)
	}
}
