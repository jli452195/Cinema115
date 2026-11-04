package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"testing"

	"strm115/backend/internal/client/clouddrive2"
)

type mediaCD2CleanupBatchFake struct {
	*mediaCD2Fake
	batches [][]string
	apply   int
	fail    error
}

func (f *mediaCD2CleanupBatchFake) Delete(ctx context.Context, paths []string) error {
	f.batches = append(f.batches, slices.Clone(paths))
	if f.fail == nil {
		return f.mediaCD2Fake.Delete(ctx, paths)
	}
	if f.apply > 0 {
		if err := f.mediaCD2Fake.Delete(ctx, paths[:min(f.apply, len(paths))]); err != nil {
			return err
		}
	}
	return f.fail
}

func TestMediaCD2CleanupBatchesFilesAndSharesDirectoryRead(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := &mediaCD2CleanupBatchFake{mediaCD2Fake: connection.filesystem}
	for i := range 205 {
		fs.add(path.Join(rule.Source, fmt.Sprintf("orphan-%03d.nfo", i)), false, 16)
	}
	files := &mediaOrganizeFiles{rule: rule, cd2: fs}
	entries, err := files.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	files.orphanDue = make(map[string]bool, len(entries))
	for _, entry := range entries {
		files.orphanDue[entry.File.ID] = true
	}
	files.unrelated = entries
	fs.listed = nil
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupCD2(context.Background(), files, &run); err != nil {
		t.Fatal(err)
	}
	if len(fs.batches) != 3 || len(fs.batches[0]) != 100 || len(fs.batches[1]) != 100 || len(fs.batches[2]) != 5 {
		t.Fatal("应按父目录每批最多100项删除", fs.batches)
	}
	if len(fs.listed) != 3 {
		t.Fatal("每批只需一次实时目录读取，不随附件数量重复", fs.listed)
	}
	if len(fs.deleted) != 205 || len(run.Entries) != 205 {
		t.Fatal("所有附件都应完成清理", len(fs.deleted), len(run.Entries))
	}
	for _, entry := range run.Entries {
		if entry.Status != "done" {
			t.Fatal("批次结果未完成", entry)
		}
	}
}

func TestMediaCD2CleanupRetainsChangedFilesAndNewVideoCompanions(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := &mediaCD2CleanupBatchFake{mediaCD2Fake: connection.filesystem}
	for _, name := range []string{"changed.zip", "orphan.ass", "Work.S01E01.ass", "poster.jpg"} {
		fs.add(path.Join(rule.Source, name), false, 16)
	}
	files := &mediaOrganizeFiles{rule: rule, cd2: fs}
	entries, err := files.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	files.orphanDue = make(map[string]bool, len(entries))
	for _, entry := range entries {
		files.orphanDue[entry.File.ID] = true
	}
	files.unrelated = append(files.unrelated, entries...)
	fs.add(path.Join(rule.Source, "changed.zip"), false, 16)
	fs.add(path.Join(rule.Source, "Work.S01E01.mkv.part"), false, 123)
	fs.add(path.Join(rule.Source, "Season 2"), true, 0)
	fs.add(path.Join(rule.Source, "Season 2/Other.S02E01.mkv"), false, 123)
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupCD2(context.Background(), files, &run); err != nil {
		t.Fatal(err)
	}
	if len(fs.deleted) != 1 || fs.deleted[0] != path.Join(rule.Source, "orphan.ass") {
		t.Fatal("变化身份、临时视频的字幕和子目录视频的公共图片应保留", fs.deleted)
	}
}

func TestMediaCD2CleanupReconcilesPartialDeletion(t *testing.T) {
	for _, applied := range []int{1, 3} {
		t.Run(fmt.Sprint(applied), func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			failure := errors.New("模拟批量删除响应超时")
			fs := &mediaCD2CleanupBatchFake{mediaCD2Fake: connection.filesystem, apply: applied, fail: failure}
			for _, name := range []string{"first.zip", "second.zip", "third.zip"} {
				fs.add(path.Join(rule.Source, name), false, 16)
			}
			files := &mediaOrganizeFiles{rule: rule, cd2: fs}
			if _, err := files.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			err = s.cleanupCD2(context.Background(), files, &run)
			if applied == 3 && err != nil || applied != 3 && !errors.Is(err, failure) {
				t.Fatal("应以读回的实际删除结果为准", err)
			}
			done := 0
			for _, entry := range run.Entries {
				if entry.Status == "done" {
					done++
				}
			}
			if done != applied || len(fs.batches) != 1 {
				t.Fatal("已确认消失的条目应成功，未删除条目保留错误，不能重放整批", done, fs.batches)
			}
		})
	}
}

func TestMediaCD2CleanupReadFailureDoesNotDelete(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := &mediaCD2CleanupBatchFake{mediaCD2Fake: connection.filesystem}
	fs.add(path.Join(rule.Source, "old.zip"), false, 16)
	files := &mediaOrganizeFiles{rule: rule, cd2: fs}
	if _, err := files.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("模拟目录读取失败")
	fs.onList = func(string) error { return failure }
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupCD2(context.Background(), files, &run); !errors.Is(err, failure) {
		t.Fatal("读取异常必须终止本批删除", err)
	}
	if len(fs.batches) != 0 {
		t.Fatal("读取失败不得被解释成目录或文件不存在", fs.batches)
	}
}

func TestMediaCD2CleanupRechecksIdentityAfterSharedScopeScan(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := &mediaCD2CleanupBatchFake{mediaCD2Fake: connection.filesystem}
	poster := fs.add(path.Join(rule.Source, "poster.jpg"), false, 20)
	child := path.Join(rule.Source, "Season 01")
	fs.add(child, true, 0)
	files := &mediaOrganizeFiles{rule: rule, cd2: fs, orphanDue: map[string]bool{poster.Path: true}}
	entry := mediaCD2Entry(poster, rule.Source, poster.Name)
	entry.File.MediaType = "attachment"
	files.unrelated = []MediaOrganizeEntry{entry}
	fs.onList = func(directory string) error {
		if directory == child {
			fs.add(poster.Path, false, poster.Size)
		}
		return nil
	}
	run, _ := s.Start(rule.ID)
	if err := s.cleanupCD2(context.Background(), files, &run); err != nil {
		t.Fatal(err)
	}
	if len(fs.deleted) != 0 || len(fs.batches) != 0 {
		t.Fatal("递归归属检查期间变化的附件必须保留", fs.deleted)
	}
}

var _ clouddrive2.Filesystem = (*mediaCD2CleanupBatchFake)(nil)
