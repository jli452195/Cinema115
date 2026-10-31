package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type transferFake struct {
	ownerID            string
	reviewStage        string
	missingChildren    int
	childScans         int
	fail               string
	calls              []string
	newSnapshots       int
	deleted            bool
	configuredPassword string
	listCalls          int
	driveFileName      string
	driveFileSize      int64
	snapshotFileName   string
	snapshotFileSize   int64
}

func (f *transferFake) ListForTransfer(ctx context.Context, share *p115.ShareInfo, cid string, offset int, cookie string) (*p115.SharePage, error) {
	page, err := f.List(ctx, share, cid, offset, cookie)
	if page != nil {
		page.OwnerID = f.ownerID
		if f.ownerID == "" {
			page.OwnerID = "99"
		}
		if f.ownerID == "missing" {
			page.OwnerID = ""
		}
	}
	return page, err
}

func (f *transferFake) List(_ context.Context, share *p115.ShareInfo, cid string, _ int, cookie string) (*p115.SharePage, error) {
	f.listCalls++
	if cookie != "UID=42_A; SEID=original-cookie" {
		return nil, errors.New("校验没有使用本次登录 Cookie")
	}
	if f.reviewStage == "deleted" && f.deleted {
		return nil, p115.ErrShareUnderReview
	}
	if f.reviewStage == "source" && share.Code == "source" || f.reviewStage == "own" && share.Code == "own" {
		return nil, p115.ErrShareUnderReview
	}
	if share.Code == "own" && cid == "0" {
		f.newSnapshots++
		if f.fail == "snapshot" || f.newSnapshots == 1 {
			return nil, errors.New("快照生成中")
		}
	}
	if cid == "0" || cid == "" {
		id := "10"
		if share.Code == "own" {
			id = "20"
		}
		return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: id, Name: "剧集", Directory: true, Size: 100}}}, nil
	}
	id := "11"
	name, size := "第一集.mkv", int64(100)
	if share.Code == "own" {
		id = "21"
		if f.snapshotFileName != "" {
			name = f.snapshotFileName
		}
		if f.snapshotFileSize != 0 {
			size = f.snapshotFileSize
		}
	}
	return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: id, Name: name, Size: size}}}, nil
}
func (f *transferFake) CreateDirectory(_ context.Context, parent, name, _ string) (string, error) {
	f.calls = append(f.calls, "create")
	if parent != "0" {
		return "", errors.New("分享转存未使用根目录")
	}
	return "999", nil
}
func (f *transferFake) ReceiveShare(_ context.Context, _ *p115.ShareInfo, ids []string, cid, _ string) error {
	f.calls = append(f.calls, "receive")
	if cid != "999" || strings.Join(ids, ",") != "10" {
		return errors.New("转存范围错误")
	}
	if f.fail == "receive" {
		return errors.New("转存失败")
	}
	return nil
}
func (f *transferFake) DriveFiles(_ context.Context, cid string, _ int, _ string) (*p115.SharePage, error) {
	if cid == "20" {
		f.childScans++
		if f.missingChildren > 0 {
			f.missingChildren--
			return &p115.SharePage{Count: 0, Files: []p115.ShareFile{}}, nil
		}
		name, size := f.driveFileName, f.driveFileSize
		if name == "" {
			name = "第一集.mkv"
		}
		if size == 0 {
			size = 100
		}
		return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: "21", Name: name, Size: size}}}, nil
	}
	return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: "20", Name: "剧集", Directory: true, Size: 999}}}, nil
}
func (f *transferFake) SendShare(_ context.Context, ids []string, _ string) (*p115.ShareInfo, error) {
	f.calls = append(f.calls, "share")
	if strings.Join(ids, ",") != "20" {
		return nil, errors.New("没有分享转存后的 ID")
	}
	if f.fail == "share" {
		return nil, errors.New("分享失败")
	}
	return &p115.ShareInfo{Code: "own", Password: "test", URL: "https://115cdn.com/s/own?password=test"}, nil
}
func (f *transferFake) DeleteDirectory(_ context.Context, cid, cookie string) error {
	f.calls = append(f.calls, "delete")
	if cid != "999" || cookie != "UID=42_A; SEID=original-cookie" {
		return errors.New("清理范围或账号错误")
	}
	if f.fail == "delete" {
		return errors.New("清理失败")
	}
	f.deleted = true
	return nil
}

func (f *transferFake) ConfigureShare(_ context.Context, share *p115.ShareInfo, password, _ string) error {
	f.calls = append(f.calls, "permanent")
	if share.Code != "own" {
		return errors.New("不能修改原分享的有效期")
	}
	if f.fail == "permanent" {
		return errors.New("长期有效设置失败")
	}
	f.configuredPassword = password
	share.Password = password
	share.URL = "https://115cdn.com/s/own?password=" + password
	return nil
}

type generateFunc func(context.Context, GenerateInput) (*GenerationResult, error)

func (f generateFunc) Generate(ctx context.Context, in GenerateInput) (*GenerationResult, error) {
	return f(ctx, in)
}

func setupWorkflow(t *testing.T, fake *transferFake) (*ShareWorkflow, GenerateInput) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewAutomationStore(filepath.Join(dir, "automation.json"), dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	settings := store.Value()
	settings.Generation.OutputDir = "strm"
	settings.ReshareEnabled = true
	settings.ParentCID = "123"
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	gen := generateFunc(func(_ context.Context, in GenerateInput) (*GenerationResult, error) {
		fake.calls = append(fake.calls, "generate")
		if fake.deleted || fake.newSnapshots < 2 {
			t.Error("校验完整快照之前生成或清理")
		}
		if !strings.Contains(in.URL, "/own?") || in.CID != "0" {
			t.Error("没有使用新分享生成 STRM")
		}
		if in.Password != fake.configuredPassword || !strings.HasSuffix(in.URL, "password="+fake.configuredPassword) {
			t.Error("生成未使用设置后的新密码")
		}
		if fake.fail == "generate" {
			return nil, ErrNoMedia
		}
		return &GenerationResult{Written: 1}, nil
	})
	db, err := storage.Open(context.Background(), filepath.Join(dir, "cinema115.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	w := NewShareWorkflow(fake, gen, func() string { return "UID=42_A; SEID=original-cookie" }, store, dir, db)
	w.PollInterval = time.Millisecond
	in := settings.Generation
	in.URL = "https://115cdn.com/s/source?password=1234"
	return w, in
}

func TestShareWorkflowWaitsForSnapshotAndCleansOnlyOwnedDirectory(t *testing.T) {
	fake := &transferFake{}
	w, in := setupWorkflow(t, fake)
	// An injected snapshot must not restore the removed parent selection.
	ctx := context.WithValue(context.Background(), automationContextKey{}, w.Settings.Value())
	result, err := w.Generate(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cleaned || result.ReshareURL == "" || result.TempDirectoryID != "999" {
		t.Fatalf("结果不完整: %+v", result)
	}
	if got := strings.Join(fake.calls, ","); got != "create,receive,share,permanent,generate,delete" {
		t.Fatal(got)
	}
	jobs := workflowJobs(t, w)
	if len(jobs) != 1 {
		t.Fatal("缺少任务记录")
	}
	job := jobs[0]
	if job.Status != "completed" || job.DirectoryID != "999" {
		t.Fatalf("任务状态错误: %+v", job)
	}
}

func TestShareWorkflowRefreshKeepsPinnedCookieAndSignOutStopsCleanup(t *testing.T) {
	for _, action := range []string{"refresh", "sign-out"} {
		t.Run(action, func(t *testing.T) {
			fake := &transferFake{}
			workflow, input := setupWorkflow(t, fake)
			settings := workflow.Settings.Value()
			settings.AccountID = "original-account"
			if err := workflow.Settings.Save(settings); err != nil {
				t.Fatal(err)
			}
			cookie := workflow.Cookie()
			generator := workflow.Generator
			workflow.Generator = generateFunc(func(ctx context.Context, input GenerateInput) (*GenerationResult, error) {
				result, err := generator.Generate(ctx, input)
				if action == "refresh" {
					cookie = "UID=42_B; SEID=refreshed"
				} else {
					cookie = ""
				}
				return result, err
			})
			tasks := &ShareTasks{Generator: workflow, Settings: workflow.Settings, Store: workflow.Jobs.(*storage.Store), AccountCookie: func(id string) string {
				if id != settings.AccountID {
					t.Fatal("execution switched account binding")
				}
				return cookie
			}}
			result, err := tasks.Generate(context.Background(), input)
			if action == "refresh" {
				if err != nil || result == nil || !result.Cleaned || !fake.deleted {
					t.Fatal("same-account refresh blocked cleanup", result, err)
				}
			} else if err == nil || fake.deleted || strings.Contains(strings.Join(fake.calls, ","), "delete") {
				t.Fatal("sign-out allowed another cloud write", result, err)
			}
		})
	}
}

func TestShareWorkflowWaitsForAllTransferredChildrenBeforeSharing(t *testing.T) {
	fake := &transferFake{missingChildren: 2}
	w, input := setupWorkflow(t, fake)
	var progress []GenerationProgress
	ctx := WithGenerationProgress(context.Background(), func(event GenerationProgress) { progress = append(progress, event) })
	if _, err := w.Generate(ctx, input); err != nil {
		t.Fatal(err)
	}
	if fake.childScans != 3 {
		t.Fatal("未等待目录中的子文件转存完毕", fake.childScans)
	}
	var waiting bool
	for _, event := range progress {
		if event.Stage == "waiting" && strings.Contains(event.Message, "转存数量尚未完成") {
			waiting = true
		}
	}
	if !waiting {
		t.Fatal("未报告转存数量进度")
	}
}

func TestShareWorkflowUsesMediaCountsInsteadOfNamesAndSizes(t *testing.T) {
	fake := &transferFake{driveFileName: "转存名称不同.mkv", driveFileSize: 999, snapshotFileName: "分享名称也不同.mkv", snapshotFileSize: 888}
	w, input := setupWorkflow(t, fake)
	result, err := w.Generate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cleaned || result.Written != 1 {
		t.Fatalf("数量一致时未完成生成：%+v", result)
	}
}

func TestTransferMediaCountsUseConfiguredFilters(t *testing.T) {
	input := GenerateInput{VideoExtensions: "mkv,mp4", MetadataExtensions: "srt", MinSizeMB: 100}
	files := []p115.ShareFile{
		{Name: "合格.mkv", Size: 100 * 1024 * 1024},
		{Name: "太小.mp4", Size: 99 * 1024 * 1024},
		{Name: "字幕.SRT", Size: 1024},
		{Name: "忽略.nfo", Size: 1024},
	}
	want := transferMediaCounts{Videos: 1, Metadata: 1}
	if got := countTransferMedia(input, files); got != want {
		t.Fatalf("媒体数量 = %+v，期望 %+v", got, want)
	}
	if err := compareTransferCounts(want, want); err != nil {
		t.Fatal(err)
	}
	if err := compareTransferCounts(want, transferMediaCounts{Videos: 2, Metadata: 1}); !errors.Is(err, errTransferCountExceeded) {
		t.Fatalf("数量超出未立即识别：%v", err)
	}
}

func TestShareWorkflowKeepsDirectoryOnFailure(t *testing.T) {
	for _, stage := range []string{"receive", "share", "permanent", "snapshot", "generate", "delete"} {
		t.Run(stage, func(t *testing.T) {
			fake := &transferFake{fail: stage}
			w, in := setupWorkflow(t, fake)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			result, err := w.Generate(ctx, in)
			if err == nil || fake.deleted {
				t.Fatal("失败时清理了临时目录")
			}
			if result == nil || result.TempDirectoryID != "999" {
				t.Fatal("失败时没有返回保留的目录 ID")
			}
			if stage != "delete" && strings.Contains(strings.Join(fake.calls, ","), "delete") {
				t.Fatal("失败后调用了删除接口")
			}
		})
	}
}

func TestShareWorkflowOffAndInvalidInputDoNotTransfer(t *testing.T) {
	fake := &transferFake{}
	w, in := setupWorkflow(t, fake)
	in.OutputDir = "../outside"
	if _, err := w.Generate(context.Background(), in); !errors.Is(err, ErrGenerationInput) {
		t.Fatal(err)
	}
	if len(fake.calls) != 0 {
		t.Fatal("非法参数触发云端变更")
	}
	settings := w.Settings.Value()
	settings.ReshareEnabled = false
	if err := w.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	w.Generator = generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		return &GenerationResult{Written: 2}, nil
	})
	result, err := w.Generate(context.Background(), in)
	if err != nil || result.Written != 2 || len(fake.calls) != 0 {
		t.Fatal("关闭开关没有走普通生成")
	}
}

func TestShareWorkflowCancellationBeforeCleanup(t *testing.T) {
	fake := &transferFake{}
	w, in := setupWorkflow(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Generator = generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		cancel()
		return &GenerationResult{}, nil
	})
	if _, err := w.Generate(ctx, in); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if fake.deleted {
		t.Fatal("取消后仍删除目录")
	}
	jobs := workflowJobs(t, w)
	if len(jobs) != 1 || !jobs[0].Finished || jobs[0].LastError == "" || jobs[0].DirectoryID != "999" {
		t.Fatal("取消后未持久化最终检查点")
	}
}

func workflowJobs(t *testing.T, w *ShareWorkflow) []model.ReshareJob {
	t.Helper()
	jobs, err := w.Jobs.(*storage.Store).ListReshareJobs(context.Background(), "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

type failingJobWriter struct {
	store     ReshareJobWriter
	failStage string
}

func (s failingJobWriter) SaveReshareJob(ctx context.Context, job model.ReshareJob) error {
	if job.Status == s.failStage {
		return errors.New("模拟磁盘写入失败")
	}
	return s.store.SaveReshareJob(ctx, job)
}

func TestCheckpointFailurePreventsCloudMutation(t *testing.T) {
	for _, stage := range []string{"creating", "deleting"} {
		t.Run(stage, func(t *testing.T) {
			fake := &transferFake{}
			w, in := setupWorkflow(t, fake)
			w.Jobs = failingJobWriter{store: w.Jobs, failStage: stage}
			if _, err := w.Generate(context.Background(), in); err == nil {
				t.Fatal("检查点失败却报告成功")
			}
			if fake.deleted || stage == "creating" && len(fake.calls) != 0 {
				t.Fatal("检查点保存失败后仍变更云端")
			}
		})
	}
}

func TestAutomationSettingsPersistWithoutShareCredentials(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "settings.json")
	store, err := NewAutomationStore(file, dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	value := store.Value()
	value.ReshareEnabled = true
	value.Generation.URL = "private-share"
	value.Generation.Password = "secret"
	value.Generation.OutputDir = "strm2"
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewAutomationStore(file, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Value(); !got.ReshareEnabled || got.Generation.OutputDir != "strm2" || got.Generation.URL != "" || got.Generation.Password != "" {
		t.Fatalf("持久化错误: %+v", got)
	}
}

func TestAutomationSettingsPersistExplicitRoot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "root-settings.json")
	store, err := NewAutomationStore(file, dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	value := store.Value()
	value.Generation.OutputDir = "."
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewAutomationStore(file, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Value().Generation.OutputDir != "." {
		t.Fatal("lost root selection")
	}
	value.Generation.OutputDir = ""
	if err := reopened.Save(value); err != nil {
		t.Fatal(err)
	}
	if reopened.Value().Generation.OutputDir != "" {
		t.Fatal("missing selection became root")
	}
}
