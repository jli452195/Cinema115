package service

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type downloadDirectoryTestDrive struct {
	*organizeFakeDrive
	reads     int
	err       error
	alter     func(*p115.DriveDirectory)
	afterRead func()
}

func (d *downloadDirectoryTestDrive) ReadDirectory(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
	d.reads++
	if err := ctx.Err(); err != nil {
		return p115.DriveDirectory{}, err
	}
	if d.err != nil {
		return p115.DriveDirectory{}, d.err
	}
	for parent, children := range d.dirs {
		for _, child := range children {
			if child.ID != cid || !child.Directory {
				continue
			}
			out := p115.DriveDirectory{CID: cid, Name: child.Name, ParentCID: parent, Path: path.Join("/来源", child.Name)}
			if d.alter != nil {
				d.alter(&out)
			}
			if d.afterRead != nil {
				d.afterRead()
			}
			return out, nil
		}
	}
	return p115.DriveDirectory{}, errors.New("模拟目录不存在")
}

type downloadDirectoryTestAdder struct {
	*cloudAdderStub
	calls int
	err   error
	check func()
}

func (a *downloadDirectoryTestAdder) AddURL(ctx context.Context, cookie, rawURL, cid string) (json.RawMessage, error) {
	a.calls++
	if a.check != nil {
		a.check()
	}
	if a.err != nil {
		return nil, a.err
	}
	return a.cloudAdderStub.AddURL(ctx, cookie, rawURL, cid)
}

func newDownloadDirectoryTest(t *testing.T) (*DownloadOrganize, *downloadDirectoryTestDrive, *downloadDirectoryTestAdder, *gardenTrackerFake, *downloadPending) {
	t.Helper()
	s, base, adder, tracker, _, in := gardenFixture(t)
	drive := &downloadDirectoryTestDrive{organizeFakeDrive: base}
	client := &downloadDirectoryTestAdder{cloudAdderStub: adder}
	s.Organize.drive, s.Downloads.Client = drive, client
	tracker.downloading = true
	plan, err := s.Preview(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Start(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, drive, client, tracker, s.live[job.ID]
}

func restartDownloadDirectoryTest(t *testing.T, s *DownloadOrganize) *DownloadOrganize {
	t.Helper()
	o, err := NewOrganizeEngine(s.Organize.store, s.Organize.accounts, s.Organize.drive, s.Organize.metadata, s.Organize.templates)
	if err != nil {
		t.Fatal(err)
	}
	return NewDownloadOrganize(o, s.Resources, s.Downloads, s.Settings, s.Reader)
}

func TestDownloadDirectoryConfirmationDoesNotListChangingParent(t *testing.T) {
	s, drive, adder, _, pending := newDownloadDirectoryTest(t)
	parentReads := drive.listCalls[pending.job.Download.ParentCID]
	drive.beforeRead = func(cid string, _ int) error {
		if cid == pending.job.Download.ParentCID {
			return errOrganizePagination
		}
		return nil
	}
	if done, err := s.advance(context.Background(), pending); err != nil || done || pending.job.Status != "downloading" {
		t.Fatal("共享父目录变化阻止了独立下载目录确认", done, err, pending.job.Status)
	}
	if drive.reads != 1 || drive.listCalls[pending.job.Download.ParentCID] != parentReads || drive.creates != 1 || adder.calls != 1 || adder.cid != pending.job.Download.DirectoryID || pending.job.Download.SubmitPending {
		t.Fatal("目录确认增加请求、重建目录或丢失提交阶段", drive.reads, drive.listCalls, drive.creates, adder.calls, pending.job.Download)
	}
}

func TestDownloadDirectoryConfirmationRejectsWrongIdentity(t *testing.T) {
	for _, field := range []string{"cid", "name", "parent"} {
		t.Run(field, func(t *testing.T) {
			s, drive, adder, tracker, pending := newDownloadDirectoryTest(t)
			drive.alter = func(directory *p115.DriveDirectory) {
				switch field {
				case "cid":
					directory.CID = "999"
				case "name":
					directory.Name = "其他目录"
				case "parent":
					directory.ParentCID = "999"
				}
			}
			if done, err := s.advance(context.Background(), pending); err != nil || !done || pending.job.Status != "blocked" {
				t.Fatal("错误目录身份未阻止提交", done, err, pending.job.Status)
			}
			if adder.calls != 0 || tracker.calls != 0 || drive.creates != 1 {
				t.Fatal("目录身份错误仍提交或重复创建", adder.calls, tracker.calls, drive.creates)
			}
		})
	}
}

func TestDownloadDirectoryConfirmationFailureResumesOriginalDirectory(t *testing.T) {
	s, drive, adder, _, pending := newDownloadDirectoryTest(t)
	drive.err = context.DeadlineExceeded
	if done, err := s.advance(context.Background(), pending); err != nil || !done || pending.job.Status != "interrupted" || !pending.job.Download.SubmitPending {
		t.Fatal("确认失败没有保留未提交阶段", done, err, pending.job.Status)
	}
	dir := pending.job.Download.DirectoryID
	drive.err = nil
	s = restartDownloadDirectoryTest(t, s)
	if _, err := s.Resume(pending.job.ID); err != nil {
		t.Fatal(err)
	}
	pending = s.live[pending.job.ID]
	if done, err := s.advance(context.Background(), pending); err != nil || done || pending.job.Status != "downloading" {
		t.Fatal("原目录确认后未继续提交", done, err, pending.job.Status)
	}
	if drive.creates != 1 || adder.calls != 1 || adder.cid != dir || pending.job.Download.DirectoryID != dir {
		t.Fatal("恢复重建目录或改变离线目标", drive.creates, adder.calls, pending.job.Download)
	}
}

func TestDownloadDirectorySubmitIntentMustPersistBeforeRequest(t *testing.T) {
	s, drive, adder, _, pending := newDownloadDirectoryTest(t)
	store := s.Organize.store.(*failingAccountStore)
	drive.afterRead = func() { store.err = errors.New("模拟提交意图保存失败") }
	if _, err := s.advance(context.Background(), pending); err == nil || adder.calls != 0 {
		t.Fatal("提交意图保存失败仍调用115", err, adder.calls)
	}
	store.err, drive.afterRead = nil, nil
	s = restartDownloadDirectoryTest(t, s)
	if !s.History()[0].Download.SubmitPending {
		t.Fatal("未发出的提交丢失原目录恢复状态")
	}
	if _, err := s.Resume(pending.job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.advance(context.Background(), s.live[pending.job.ID]); err != nil || adder.calls != 1 || drive.creates != 1 {
		t.Fatal("未发出的提交无法按原目录继续", err, adder.calls, drive.creates)
	}
}

func TestDownloadDirectoryUncertainSubmissionNeverReplays(t *testing.T) {
	s, drive, adder, tracker, pending := newDownloadDirectoryTest(t)
	adder.err = context.DeadlineExceeded
	adder.check = func() {
		var persisted organizeDocument
		if err := json.Unmarshal([]byte(s.Organize.store.Value()), &persisted); err != nil || len(persisted.Jobs) != 1 || persisted.Jobs[0].Download.SubmitPending {
			t.Fatal("发出请求前没有持久化提交意图", err)
		}
	}
	if done, err := s.advance(context.Background(), pending); err != nil || !done || pending.job.Status != "uncertain" || adder.calls != 1 {
		t.Fatal("超时提交未保留未知结果", done, err, pending.job.Status, adder.calls)
	}
	// 模拟请求已发出、但尚未来得及持久化回执便重启的崩溃窗口。
	pending.job.Status = "submitting"
	if err := s.Organize.checkpoint(&pending.job); err != nil {
		t.Fatal(err)
	}
	s = restartDownloadDirectoryTest(t, s)
	tracker.missing = true
	if _, err := s.Resume(pending.job.ID); err != nil {
		t.Fatal(err)
	}
	if done, err := s.advance(context.Background(), s.live[pending.job.ID]); err != nil || !done || adder.calls != 1 || drive.creates != 1 || tracker.calls != 1 {
		t.Fatal("未知提交在重启后被重发或重建目录", done, err, adder.calls, drive.creates, tracker.calls)
	}
}

func TestDownloadDirectoryBlockedTitleRechecksWithoutResubmission(t *testing.T) {
	s, drive, adder, tracker, pending := newDownloadDirectoryTest(t)
	item := &pending.job.Items[0]
	item.File.ID, item.File.ParentID, item.File.SourcePath, item.File.TitleOnly = "title", ".", "/title", true
	item.Name = strings.TrimSuffix(item.Name, path.Ext(item.Name))
	item.TargetPath = path.Join(pending.job.Config.TargetPath, item.Directory, item.Name)
	if _, err := s.advance(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	tracker.downloading, tracker.resultID = false, "801"
	drive.dirs[pending.job.Download.DirectoryID] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
	drive.dirs["801"] = []p115.ShareFile{{ID: "901", Name: "Show [01].mkv", Size: 1200}, {ID: "902", Name: "Show [02].mkv", Size: 1300}}
	if done, err := s.advance(context.Background(), pending); err != nil || !done || pending.job.Status != "blocked" {
		t.Fatal("多个正片未进入待核对状态", done, err, pending.job.Status)
	}
	drive.dirs["801"] = []p115.ShareFile{{ID: "901", Name: "Show [01].mkv", Size: 1200}, {ID: "903", Name: "Show [01].chs.ass", Size: 30}}
	s = restartDownloadDirectoryTest(t, s)
	if _, err := s.Resume(pending.job.ID); err != nil {
		t.Fatal(err)
	}
	pending = s.live[pending.job.ID]
	if done, err := s.advance(context.Background(), pending); err != nil || !done || pending.job.Status != "completed" || adder.calls != 1 || len(pending.job.Items) != 2 {
		t.Fatal("调整后的原下载内容无法恢复或重新下载", done, err, pending.job.Status, adder.calls)
	}
}
