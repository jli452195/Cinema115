package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"testing"

	"strm115/backend/internal/client/p115"
)

const media115RegressionCookie = "UID=115_fixture; SEID=fixed_fixture"

// 此假实现仅保留内存目录，所有读取和写入均不访问真实网盘。
type media115RegressionDrive struct {
	OrganizeDrive
	t             *testing.T
	directories   map[string]p115.DriveDirectory
	children      map[string][]p115.ShareFile
	batches       [][]string
	postReadCalls int
	treeCalls     int
	probeCalls    int
	selected      map[string]bool
	writeErr      error
	readErr       error
	readIdentity  string
	scanErrorID   string
	scanErr       error
	treeErr       error
	beforeTree    func()
	beforeProbe   func()
}

func newMedia115RegressionDrive(t *testing.T, emptyDirectories int) *media115RegressionDrive {
	t.Helper()
	d := &media115RegressionDrive{
		t: t,
		directories: map[string]p115.DriveDirectory{
			"10": {CID: "10", ParentCID: "0", Name: "inbox", Path: "/inbox"},
			"20": {CID: "20", ParentCID: "0", Name: "library", Path: "/library"},
		},
		children: map[string][]p115.ShareFile{"10": {}, "20": {}},
	}
	for i := 0; i < emptyDirectories; i++ {
		d.add("10", p115.ShareFile{ID: fmt.Sprint(100 + i), Name: fmt.Sprintf("empty-%02d", i), Directory: true})
	}
	return d
}

func (d *media115RegressionDrive) add(parent string, file p115.ShareFile) {
	d.children[parent] = append(d.children[parent], file)
	if file.Directory {
		d.directories[file.ID] = p115.DriveDirectory{CID: file.ID, ParentCID: parent, Name: file.Name, Path: path.Join(d.directories[parent].Path, file.Name)}
		d.children[file.ID] = []p115.ShareFile{}
	}
}

func (d *media115RegressionDrive) check(ctx context.Context, cookie string) error {
	d.t.Helper()
	if cookie != media115RegressionCookie {
		d.t.Fatal("同轮清理必须使用固定的测试凭据")
	}
	return ctx.Err()
}

func (d *media115RegressionDrive) DriveFiles(ctx context.Context, cid string, offset int, cookie string) (*p115.SharePage, error) {
	if err := d.check(ctx, cookie); err != nil {
		return nil, err
	}
	if cid == d.scanErrorID {
		return nil, d.scanErr
	}
	items := d.children[cid]
	if offset < 0 || offset > len(items) {
		return nil, fmt.Errorf("测试目录分页越界")
	}
	return &p115.SharePage{Offset: offset, Count: len(items), Files: slices.Clone(items[offset:])}, nil
}

func (d *media115RegressionDrive) ReadDirectory(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
	return d.directories[cid], d.check(ctx, cookie)
}

func (d *media115RegressionDrive) ProbeEmptyDirectory(ctx context.Context, cid, cookie string) (p115.DriveDirectory, bool, error) {
	d.probeCalls++
	if d.beforeProbe != nil {
		d.beforeProbe()
		d.beforeProbe = nil
	}
	return d.directories[cid], len(d.children[cid]) == 0, d.check(ctx, cookie)
}

func (d *media115RegressionDrive) ReadDirectoryFiles(ctx context.Context, cid, cookie string) (p115.DriveDirectory, []p115.ShareFile, error) {
	if err := d.check(ctx, cookie); err != nil {
		return p115.DriveDirectory{}, nil, err
	}
	directory := d.directories[cid]
	if len(d.batches) > 0 {
		d.postReadCalls++
		if d.readErr != nil {
			return p115.DriveDirectory{}, nil, d.readErr
		}
		switch d.readIdentity {
		case "wrong-id":
			directory.CID = "999"
		case "wrong-path":
			directory.Path = "/outside"
		}
	}
	return directory, slices.Clone(d.children[cid]), nil
}

func (d *media115RegressionDrive) WalkDriveFiles(ctx context.Context, cid, cookie string, visit func([]p115.DriveTreeEntry) error) error {
	if err := d.check(ctx, cookie); err != nil {
		return err
	}
	d.treeCalls++
	if d.beforeTree != nil {
		d.beforeTree()
		d.beforeTree = nil
	}
	entries := make([]p115.DriveTreeEntry, 0)
	var walk func(string, string)
	walk = func(parent, prefix string) {
		for _, file := range d.children[parent] {
			relative := path.Join(prefix, file.Name)
			entries = append(entries, p115.DriveTreeEntry{ShareFile: file, ParentCID: parent, RelativePath: relative})
			if file.Directory {
				walk(file.ID, relative)
			}
		}
	}
	walk(cid, "")
	if err := visit(entries); err != nil {
		return err
	}
	return d.treeErr
}

func (d *media115RegressionDrive) DeleteEntriesBatch(ctx context.Context, parent string, ids []string, cookie string) error {
	if err := d.check(ctx, cookie); err != nil {
		return err
	}
	d.batches = append(d.batches, slices.Clone(ids))
	for _, id := range ids {
		if id == "0" || id == "10" || id == "20" || d.directories[id].ParentCID != parent {
			d.t.Fatal("批量删除超出来源子目录或混入其他父目录", parent, id)
		}
		if d.selected != nil && !d.selected[id] {
			continue
		}
		d.children[parent] = slices.DeleteFunc(d.children[parent], func(file p115.ShareFile) bool { return file.ID == id })
	}
	return d.writeErr
}

func media115RegressionScan(t *testing.T, d *media115RegressionDrive) (*MediaOrganizer, *mediaOrganizeFiles, *MediaOrganizeRun) {
	t.Helper()
	s, err := NewMediaOrganizer(&failingAccountStore{}, nil, nil, nil, nil, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	rule := MediaOrganizeRule{ID: "115-empty-regression", Storage: "115", SourceID: "10", Source: "/inbox", TargetID: "20", Target: "/library"}
	run := &MediaOrganizeRun{ID: "115-empty-regression-run", Rule: rule, Status: "running"}
	s.doc.Runs = append(s.doc.Runs, *run)
	f := &mediaOrganizeFiles{rule: rule, drive: d, cookie: media115RegressionCookie, lists: make(map[string][]p115.ShareFile)}
	entries, err := f.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	run.Entries, f.scanned = entries, entries
	d.treeCalls, d.probeCalls = 0, 0
	return s, f, run
}

func TestMedia115EmptyRegressionTwentySiblingsShareBatchAndReadback(t *testing.T) {
	d := newMedia115RegressionDrive(t, 20)
	s, f, run := media115RegressionScan(t, d)
	if err := s.cleanup115EmptyDirectories(context.Background(), f, run); err != nil {
		t.Fatal(err)
	}
	if len(d.batches) != 1 || len(d.batches[0]) != 20 || d.postReadCalls != 1 || d.treeCalls != 1 || d.probeCalls != 0 {
		t.Fatal("20 个同级空目录应共用一次目录树、一次批删与一次父目录读回", d.batches, d.postReadCalls, d.treeCalls, d.probeCalls)
	}
	if len(d.children["10"]) != 0 || len(run.Entries) != 20 {
		t.Fatal("所有同级目录应完成清理并分别记录", d.children["10"], run.Entries)
	}
	for _, entry := range run.Entries {
		if !entry.File.Directory || entry.Status != "done" || entry.Result == nil || entry.Result.Status != "done" {
			t.Fatal("已确认消失的目录应逐项完成", entry)
		}
	}
	if err := s.cleanup115EmptyDirectories(context.Background(), f, run); err != nil || len(d.batches) != 1 {
		t.Fatal("收尾不得重新提交已确认的批次", len(d.batches), err)
	}
}

func TestMedia115EmptyRegressionUntrustedReadbackNeverConfirmsOrReplays(t *testing.T) {
	for _, mode := range []string{"read-failure", "wrong-id", "wrong-path"} {
		t.Run(mode, func(t *testing.T) {
			d := newMedia115RegressionDrive(t, 2)
			s, f, run := media115RegressionScan(t, d)
			failure := errors.New("模拟严格读回失败")
			if mode == "read-failure" {
				d.readErr = failure
			} else {
				d.readIdentity = mode
			}
			err := s.cleanup115EmptyDirectories(context.Background(), f, run)
			if err == nil || mode == "read-failure" && !errors.Is(err, failure) {
				t.Fatal("目录身份或完整列表无法核验时应保留错误", err)
			}
			if len(d.children["10"]) != 0 || len(d.batches) != 1 || d.postReadCalls != 1 || len(run.Entries) != 2 {
				t.Fatal("模拟写入应已发生一次，但读回不足以证明成功", d.batches, run.Entries)
			}
			for _, entry := range run.Entries {
				if entry.Status != "error" || entry.Result == nil || entry.Result.Status != "error" {
					t.Fatal("不可信空列表不得确认删除", entry)
				}
			}
			if err := s.cleanup115EmptyDirectories(context.Background(), f, run); err == nil || len(d.batches) != 1 || len(run.Entries) != 2 {
				t.Fatal("未知写入结果应等待下次扫描，不能同轮重放", len(d.batches), len(run.Entries), err)
			}
		})
	}
}

func TestMedia115EmptyRegressionPartialWriteConfirmsOnlyMissingIDs(t *testing.T) {
	d := newMedia115RegressionDrive(t, 2)
	s, f, run := media115RegressionScan(t, d)
	d.selected = map[string]bool{"100": true}
	d.writeErr = context.DeadlineExceeded
	err := s.cleanup115EmptyDirectories(context.Background(), f, run)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("部分写入应保留未确认项的超时错误", err)
	}
	statuses := make(map[string]string)
	for _, entry := range run.Entries {
		statuses[entry.File.ID] = entry.Status
	}
	if len(run.Entries) != 2 || statuses["100"] != "done" || statuses["101"] != "error" || len(d.children["10"]) != 1 || d.children["10"][0].ID != "101" || d.postReadCalls != 1 {
		t.Fatal("同一批次必须依据完整读回逐 ID 确认结果", statuses, d.children["10"], d.postReadCalls)
	}
	if err := s.cleanup115EmptyDirectories(context.Background(), f, run); !errors.Is(err, context.DeadlineExceeded) || len(d.batches) != 1 {
		t.Fatal("部分成功不能重放整批或同轮再次删除未确认项", d.batches, err)
	}
}

func TestMedia115EmptyRegressionTimeoutCanBeConfirmedByStrictReadback(t *testing.T) {
	d := newMedia115RegressionDrive(t, 2)
	s, f, run := media115RegressionScan(t, d)
	d.writeErr = context.DeadlineExceeded
	if err := s.cleanup115EmptyDirectories(context.Background(), f, run); err != nil {
		t.Fatal("写入响应超时但严格读回已确认全部消失时应完成", err)
	}
	for _, entry := range run.Entries {
		if entry.Status != "done" {
			t.Fatal("严格读回应恢复已实际成功的删除结果", entry)
		}
	}
	if len(d.batches) != 1 || d.postReadCalls != 1 {
		t.Fatal("确认超时结果不应重试写入", d.batches, d.postReadCalls)
	}
}

func TestMedia115EmptyRegressionIncompleteSnapshotsNeverDelete(t *testing.T) {
	for _, mode := range []string{"scan-incomplete", "tree-interrupted", "tree-unsupported-after-partial"} {
		t.Run(mode, func(t *testing.T) {
			d := newMedia115RegressionDrive(t, 4)
			s, f, run := media115RegressionScan(t, d)
			switch mode {
			case "scan-incomplete":
				d.scanErrorID, d.scanErr = "101", errors.New("初始扫描子目录失败")
				f = &mediaOrganizeFiles{rule: run.Rule, drive: d, cookie: media115RegressionCookie, lists: make(map[string][]p115.ShareFile)}
				if _, err := f.scan(context.Background()); !errors.Is(err, d.scanErr) {
					t.Fatal("应实际中断初始扫描并保留原读取错误", err)
				}
			case "tree-interrupted":
				d.treeErr = errors.New("目录树读取中断")
			case "tree-unsupported-after-partial":
				d.treeErr = p115.ErrDriveTreeUnsupported
			}
			err := s.cleanup115EmptyDirectories(context.Background(), f, run)
			if mode != "scan-incomplete" && err == nil {
				t.Fatal("返回部分快照后失败必须向上报告")
			}
			if len(d.batches) != 0 || len(d.children["10"]) != 4 || len(run.Entries) != 0 {
				t.Fatal("初始扫描或复核目录树不完整时不得删除", d.batches, run.Entries)
			}
		})
	}
}

func TestMedia115EmptyRegressionRetainedAndLateFilesPreventEmptyInference(t *testing.T) {
	for _, mode := range []string{"existing-subtitle", "retained-file", "late-subtitle-probe", "late-subtitle-tree"} {
		t.Run(mode, func(t *testing.T) {
			n := 1
			if mode == "late-subtitle-tree" {
				n = 4
			}
			d := newMedia115RegressionDrive(t, n)
			addSubtitle := func() { d.add("100", p115.ShareFile{ID: "900", Name: "orphan.srt", Size: 12}) }
			switch mode {
			case "existing-subtitle":
				addSubtitle()
			case "retained-file":
				d.add("100", p115.ShareFile{ID: "900", Name: "disc.iso", Size: 12})
			}
			s, f, run := media115RegressionScan(t, d)
			switch mode {
			case "late-subtitle-probe":
				d.beforeProbe = addSubtitle
			case "late-subtitle-tree":
				d.beforeTree = addSubtitle
			}
			if err := s.cleanup115EmptyDirectories(context.Background(), f, run); err != nil {
				t.Fatal(err)
			}
			for _, ids := range d.batches {
				if slices.Contains(ids, "100") {
					t.Fatal("已有或扫描后到达的文件不得被空目录推断删除", mode, ids)
				}
			}
			if len(d.children["100"]) != 1 || d.children["100"][0].ID != "900" || !slices.ContainsFunc(d.children["10"], func(file p115.ShareFile) bool { return file.ID == "100" }) {
				t.Fatal("字幕及保留文件所在目录必须保留", d.children)
			}
			if mode == "late-subtitle-tree" && (len(d.batches) != 1 || len(d.batches[0]) != 3) {
				t.Fatal("非空候选不能阻止其他同级空目录清理", d.batches)
			}
		})
	}
}
