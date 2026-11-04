package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type driveSTRMBatchTestClient struct {
	*driveSTRMTestClient
	batchMu           sync.Mutex
	prepared          [][]p115.ShareFile
	cookies           []string
	reads             []string
	active            int
	peak              int
	prepareErr        error
	prepareFileErrors map[string]error
	prepareHook       func()
	readHook          func(context.Context, string) error
}

type driveSTRMAttachmentSettingsCancellationStore struct {
	DriveSTRMStore
	mu        sync.Mutex
	blocked   bool
	entered   chan struct{}
	completed chan string
}

func (s *driveSTRMAttachmentSettingsCancellationStore) GetDriveSTRMSettings(ctx context.Context) (model.DriveSTRMSettings, error) {
	s.mu.Lock()
	block := !s.blocked
	s.blocked = true
	s.mu.Unlock()
	if block {
		close(s.entered)
		<-ctx.Done()
		return model.DriveSTRMSettings{}, ctx.Err()
	}
	return s.DriveSTRMStore.GetDriveSTRMSettings(ctx)
}

func (s *driveSTRMAttachmentSettingsCancellationStore) CompleteDriveSTRMAttachment(ctx context.Context, job model.DriveSTRMAttachment, record model.DriveSTRMFile) error {
	if err := s.DriveSTRMStore.CompleteDriveSTRMAttachment(ctx, job, record); err != nil {
		return err
	}
	s.completed <- job.Record.MappingID
	return nil
}

func (f *driveSTRMBatchTestClient) PrepareDriveMetadata(ctx context.Context, files []p115.ShareFile, cookie, mode string) (map[string]p115.DriveMetadataLink, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.batchMu.Lock()
	f.prepared = append(f.prepared, append([]p115.ShareFile(nil), files...))
	f.cookies = append(f.cookies, cookie)
	hook, err := f.prepareHook, f.prepareErr
	f.batchMu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return nil, err
	}
	links := make(map[string]p115.DriveMetadataLink, len(files))
	for _, file := range files {
		links[file.ID] = p115.DriveMetadataLink{File: file, URL: "https://cdn.115cdn.com/file", Preview: p115.DriveMetadataUsesPreview(file, mode), Err: f.prepareFileErrors[file.ID]}
	}
	return links, nil
}

func (f *driveSTRMBatchTestClient) ReadDriveMetadataLink(ctx context.Context, link p115.DriveMetadataLink) ([]byte, error) {
	f.batchMu.Lock()
	f.reads = append(f.reads, link.File.ID)
	f.active++
	f.peak = max(f.peak, f.active)
	hook := f.readHook
	f.batchMu.Unlock()
	defer func() { f.batchMu.Lock(); f.active--; f.batchMu.Unlock() }()
	if hook != nil {
		if err := hook(ctx, link.File.ID); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.metadata[link.File.ID]...), f.metadataErr[link.File.ID]
}

func driveSTRMBatchFixture(t *testing.T, count, concurrency int) (*DriveSTRMService, *storage.Store, *driveSTRMBatchTestClient, model.DriveSTRM, string) {
	t.Helper()
	s, db, base, mapping, root := driveSTRMTestSetup(t)
	settings, err := s.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.MetadataConcurrency = concurrency
	if _, err := s.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	client := &driveSTRMBatchTestClient{driveSTRMTestClient: base}
	s.Client = client
	for i := 0; i < count; i++ {
		id := fmt.Sprint(1000 + i)
		file := p115.ShareFile{ID: id, Name: fmt.Sprintf("subtitle-%02d.ass", i), Size: 3, PickCode: fmt.Sprintf("ecjq9ichcb40lz%03d", i), SHA1: strings.Repeat("A", 40)}
		base.files["100"] = append(base.files["100"], file)
		base.metadata[id] = []byte("sub")
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	return s, db, client, mapping, root
}

func TestDriveSTRMAttachmentBatchCombinesLinksBoundsConcurrencyAndPinsCookie(t *testing.T) {
	s, db, client, mapping, _ := driveSTRMBatchFixture(t, 8, 4)
	client.prepareHook = func() {
		account, _ := s.Accounts.Get(mapping.AccountID)
		cookie := "UID=42_B"
		if err := s.Accounts.Update(account.ID, account.Revision, account.Name, &cookie); err != nil {
			t.Error(err)
		}
	}
	started, release := make(chan string, 8), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	client.readHook = func(ctx context.Context, id string) error {
		started <- id
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	jobs, workCtx, _, found, err := s.nextAttachmentBatch(ctx)
	if err != nil || !found || len(jobs) != 8 {
		t.Fatal("没有合并同来源待办", len(jobs), found, err)
	}
	type result struct {
		completed int
		err       error
	}
	finished := make(chan result, 1)
	go func() {
		done, err := s.downloadAttachmentBatch(ctx, workCtx, jobs, client)
		finished <- result{done, err}
	}()
	for range 4 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("下载没有达到受控并发", ctx.Err())
		}
	}
	client.batchMu.Lock()
	if len(client.prepared) != 1 || len(client.prepared[0]) != 8 || len(client.reads) != 4 || client.cookies[0] != "UID=42_A" || client.peak != 4 {
		t.Error("取链次数、凭据或下载并发不符合约定", len(client.prepared), len(client.reads), client.peak)
	}
	client.batchMu.Unlock()
	releaseOnce.Do(func() { close(release) })
	got := <-finished
	if got.err != nil || got.completed != 8 {
		t.Fatal(got)
	}
	counts, err := db.DriveSTRMAttachmentCounts(context.Background())
	if err != nil || counts[mapping.ID][0] != 0 {
		t.Fatal("成功下载没有安全确认", counts, err)
	}
}

func TestDriveSTRMAttachmentBatch403StopsOtherMappingsAndProbesOne(t *testing.T) {
	s, db, client, mapping, _ := driveSTRMBatchFixture(t, 4, 1)
	ctx := context.Background()
	other := mapping
	other.ID, other.SourceCID, other.OutputDir, other.Queued, other.Status = "other-mapping", "300", "other-strm", false, "idle"
	if err := db.SaveDriveSTRM(ctx, other); err != nil {
		t.Fatal(err)
	}
	client.metadataErr["1000"] = &p115.HTTPError{Status: 403, Endpoint: "cdn.115cdn.com/file"}
	jobs, workCtx, _, found, err := s.nextAttachmentBatch(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	otherJob := jobs[0]
	otherJob.Record.MappingID, otherJob.Record.FileID, otherJob.Record.Path = other.ID, "9000", "other.ass"
	otherJob.SourceCID, otherJob.OutputDir = other.SourceCID, other.OutputDir
	if err := db.QueueDriveSTRMAttachments(ctx, []model.DriveSTRMAttachment{otherJob}); err != nil {
		t.Fatal(err)
	}
	client.metadata["9000"] = []byte("sub")
	done, err := s.downloadAttachmentBatch(ctx, workCtx, jobs, client)
	if err != nil || done != 0 || len(client.reads) != 1 {
		t.Fatal("403 后仍继续下载其他文件", done, len(client.reads), err)
	}
	state := s.AttachmentState()
	if !state.Probing || time.Until(state.CoolingUntil) < 55*time.Second || !strings.Contains(state.LastError, "403") {
		t.Fatal("403 没有触发共同冷却", state)
	}
	if _, _, _, found, err := s.nextAttachmentBatch(ctx); err != nil || found {
		t.Fatal("其他目录绕过共同冷却", found, err)
	}
	counts, err := db.DriveSTRMAttachmentCounts(ctx)
	if err != nil || counts[mapping.ID] != [2]int{4, 1} {
		t.Fatal("取消项不应被当作下载失败重试", counts, err)
	}
	s.metadataControlMu.Lock()
	s.metadataControl.state.CoolingUntil = time.Now().Add(-time.Second)
	s.metadataControlMu.Unlock()
	probe, probeCtx, _, found, err := s.nextAttachmentBatch(ctx)
	if err != nil || !found || len(probe) != 1 {
		t.Fatal("冷却后没有限制为单件探测", len(probe), found, err)
	}
	if done, err := s.downloadAttachmentBatch(ctx, probeCtx, probe, client); err != nil || done != 1 {
		t.Fatal("单件探测失败", done, err)
	}
	if state := s.AttachmentState(); state.Probing || state.LastError != "" {
		t.Fatal("探测成功后没有解除冷却状态", state)
	}
}

func TestDriveSTRMAttachmentBatchCancellationProtectsOutputs(t *testing.T) {
	s, db, client, mapping, root := driveSTRMBatchFixture(t, 4, 2)
	ctx := context.Background()
	started := make(chan struct{}, 4)
	client.readHook = func(ctx context.Context, _ string) error { started <- struct{}{}; <-ctx.Done(); return ctx.Err() }
	jobs, workCtx, _, found, err := s.nextAttachmentBatch(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	finished := make(chan error, 1)
	go func() { _, err := s.downloadAttachmentBatch(ctx, workCtx, jobs, client); finished <- err }()
	<-started
	if err := s.Stop(ctx, mapping.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("停止没有回收整批下载")
	}
	if _, err := os.Stat(filepath.Join(root, "strm", "subtitle-00.ass")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("停止后写入了旧附件", err)
	}
	counts, err := db.DriveSTRMAttachmentCounts(ctx)
	if err != nil || counts[mapping.ID][0] != 0 {
		t.Fatal("停止后恢复了已退休的待办", counts, err)
	}
}

func TestDriveSTRMAttachmentThumbnailCacheRejectsCredentialRefresh(t *testing.T) {
	s, _, _, mapping, _ := driveSTRMBatchFixture(t, 1, 1)
	jobs, _, _, found, err := s.nextAttachmentBatch(context.Background())
	if err != nil || !found {
		t.Fatal(found, err)
	}
	s.releaseAttachmentBatch()
	job := jobs[0]
	job.Thumbnail = "https://thumb.115.com/image?private-signature"
	s.mu.Lock()
	s.cacheAttachmentThumbnailsLocked([]model.DriveSTRMAttachment{job}, "UID=42_A")
	if s.attachmentThumbnailLocked(job) == "" {
		t.Error("同一凭据没有复用列表预览地址")
	}
	s.mu.Unlock()
	account, _ := s.Accounts.Get(mapping.AccountID)
	cookie := "UID=42_B"
	if err := s.Accounts.Update(account.ID, account.Revision, account.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attachmentThumbnailLocked(job) != "" {
		t.Fatal("新凭据复用了旧预览签名")
	}
	s.cacheAttachmentThumbnailsLocked([]model.DriveSTRMAttachment{job}, "UID=42_A")
	if s.attachmentThumbnailLocked(job) != "" {
		t.Fatal("旧扫描结果被绑定到新凭据")
	}
}

func TestDriveSTRMAttachmentBatchKeepsValidLinksWhenOneFileIsUnavailable(t *testing.T) {
	s, db, client, mapping, root := driveSTRMBatchFixture(t, 4, 2)
	client.prepareFileErrors = map[string]error{"1000": errors.New("附件已被删除")}
	ctx := context.Background()
	jobs, workCtx, _, found, err := s.nextAttachmentBatch(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	completed, err := s.downloadAttachmentBatch(ctx, workCtx, jobs, client)
	if err != nil || completed != 3 || len(client.prepared) != 1 || len(client.reads) != 3 {
		t.Fatal("单件失败影响了有效链接或触发额外取链", completed, len(client.prepared), len(client.reads), err)
	}
	counts, err := db.DriveSTRMAttachmentCounts(ctx)
	if err != nil || counts[mapping.ID] != [2]int{1, 1} {
		t.Fatal("失败项没有独立保留", counts, err)
	}
	for i := range 4 {
		_, err := os.Stat(filepath.Join(root, "strm", fmt.Sprintf("subtitle-%02d.ass", i)))
		if i == 0 && !errors.Is(err, os.ErrNotExist) || i > 0 && err != nil {
			t.Fatal("单件失败后其他附件输出异常", i, err)
		}
	}
}

func TestDriveSTRMAttachmentBatchBusinessThrottleStopsQueue(t *testing.T) {
	s, _, client, _, _ := driveSTRMBatchFixture(t, 2, 2)
	client.prepareErr = fmt.Errorf("附件 API：%w", p115.ErrShareThrottled)
	ctx := context.Background()
	jobs, workCtx, _, found, err := s.nextAttachmentBatch(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	completed, err := s.downloadAttachmentBatch(ctx, workCtx, jobs, client)
	if err != nil || completed != 0 || len(client.reads) != 0 || len(client.prepared) != 1 {
		t.Fatal(completed, len(client.reads), len(client.prepared), err)
	}
	if _, _, _, found, err := s.nextAttachmentBatch(ctx); err != nil || found || !s.AttachmentState().Probing {
		t.Fatal("业务风控未暂停整条附件队列", found, s.AttachmentState(), err)
	}
}

func TestDriveSTRMAttachmentBatch403CancelsBeforeWaitingForMappingLock(t *testing.T) {
	s, _, client, _, _ := driveSTRMBatchFixture(t, 6, 2)
	secondStarted, siblingCanceled, mappingLocked := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var unlock sync.Once
	defer func() {
		select {
		case <-mappingLocked:
			unlock.Do(func() { s.mu.Unlock() })
		default:
		}
	}()
	client.readHook = func(ctx context.Context, id string) error {
		if id == "1000" {
			select {
			case <-secondStarted:
			case <-ctx.Done():
				return ctx.Err()
			}
			s.mu.Lock()
			close(mappingLocked)
			return &p115.HTTPError{Status: 403}
		}
		if id == "1001" {
			close(secondStarted)
			<-ctx.Done()
			close(siblingCanceled)
			return ctx.Err()
		}
		return errors.New("403 之后不应启动新的下载")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	jobs, workCtx, _, found, err := s.nextAttachmentBatch(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	finished := make(chan error, 1)
	go func() { _, err := s.downloadAttachmentBatch(ctx, workCtx, jobs, client); finished <- err }()
	select {
	case <-siblingCanceled:
	case <-ctx.Done():
		t.Fatal("403 冷却等待映射锁时没有立即取消其他下载")
	}
	client.batchMu.Lock()
	reads := len(client.reads)
	client.batchMu.Unlock()
	if reads != 2 {
		t.Error("等待映射锁期间继续发起了下载", reads)
	}
	unlock.Do(func() { s.mu.Unlock() })
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("释放映射锁后批次没有结束")
	}
}

func TestDriveSTRMAttachmentBatchSettingsCancellationKeepsOtherMappingsRunning(t *testing.T) {
	for _, action := range []string{"stop", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, db, client, mapping, _ := driveSTRMBatchFixture(t, 1, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			other := mapping
			other.ID, other.SourceCID, other.OutputDir, other.Queued, other.Status = "other-mapping", "300", "other-strm", false, "idle"
			if err := db.SaveDriveSTRM(ctx, other); err != nil {
				t.Fatal(err)
			}
			jobs, err := db.ListDriveSTRMAttachments(ctx, 1)
			if err != nil || len(jobs) != 1 {
				t.Fatal(len(jobs), err)
			}
			otherJob := jobs[0]
			otherJob.Record.MappingID, otherJob.Record.FileID, otherJob.Record.Path = other.ID, "9000", "other.ass"
			otherJob.SourceCID, otherJob.OutputDir = other.SourceCID, other.OutputDir
			if err := db.QueueDriveSTRMAttachments(ctx, []model.DriveSTRMAttachment{otherJob}); err != nil {
				t.Fatal(err)
			}
			client.metadata["9000"] = []byte("sub")
			store := &driveSTRMAttachmentSettingsCancellationStore{DriveSTRMStore: db, entered: make(chan struct{}), completed: make(chan string, 1)}
			s.Store = store
			finished := make(chan error, 1)
			go func() { finished <- s.runAttachments(ctx) }()
			defer func() {
				cancel()
				select {
				case err := <-finished:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(3 * time.Second):
					t.Error("附件工作器没有退出")
				}
			}()
			select {
			case <-store.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("未阻塞在读取附件安全设置")
			}
			if action == "stop" {
				err = s.Stop(ctx, mapping.ID)
			} else {
				err = s.Delete(ctx, mapping.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case id := <-store.completed:
				if id != other.ID {
					t.Fatal("完成了错误目录", id)
				}
			case err := <-finished:
				finished <- err
				t.Fatal("取消单个目录导致附件工作器退出", err)
			case <-time.After(3 * time.Second):
				t.Fatal("取消后其他目录没有继续处理")
			}
		})
	}
}
