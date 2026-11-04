package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"strm115/backend/internal/client/p115"
)

type mediaCleanupPartialBatchDrive struct {
	*mediaCleanupDrive
	applied int
	readErr error
	written bool
}

func (d *mediaCleanupPartialBatchDrive) DeleteEntriesBatch(ctx context.Context, parent string, ids []string, cookie string) error {
	d.batches = append(d.batches, slices.Clone(ids))
	if err := d.cleanupFakeDrive.DeleteEntries(ctx, ids[:min(d.applied, len(ids))], cookie); err != nil {
		return err
	}
	d.written = true
	return context.DeadlineExceeded
}

func (d *mediaCleanupPartialBatchDrive) ReadDirectory(ctx context.Context, id, cookie string) (p115.DriveDirectory, error) {
	if d.written && d.readErr != nil {
		return p115.DriveDirectory{}, d.readErr
	}
	return d.mediaCleanupDrive.ReadDirectory(ctx, id, cookie)
}

func TestMediaOrganizerCloudCleanupConfirmsEachBatchMember(t *testing.T) {
	for _, scenario := range []string{"部分删除", "全部删除", "读取失败"} {
		t.Run(scenario, func(t *testing.T) {
			s, base, rule := newMediaCleanupCloud(t)
			drive := &mediaCleanupPartialBatchDrive{mediaCleanupDrive: base, applied: 1}
			if scenario != "部分删除" {
				drive.applied = 2
			}
			if scenario == "读取失败" {
				drive.readErr = errors.New("来源读取失败")
			}
			drive.dirs["10"] = []p115.ShareFile{{ID: "500", Name: "a.txt", Size: 3}, {ID: "501", Name: "b.zip", Size: 3}}
			s.drive = drive
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			files, err := s.files(context.Background(), rule)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := files.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			err = s.cleanup(context.Background(), files, &run)
			if scenario == "全部删除" {
				if err != nil {
					t.Fatal("写入超时但所有编号均已从来源消失，应确认成功", err)
				}
			} else if err == nil {
				t.Fatal("剩余来源或读取异常必须保留未完成结果")
			}
			if len(drive.batches) != 1 || len(drive.batches[0]) != 2 || len(run.Entries) != 2 {
				t.Fatal("同父文件没有一次提交", drive.batches, run.Entries)
			}
			for _, entry := range run.Entries {
				wantDone := scenario != "读取失败" && (scenario == "全部删除" || entry.File.ID == "500")
				if (entry.Status == "done") != wantDone {
					t.Fatal("没有按文件编号独立确认删除结果", scenario, entry)
				}
			}
		})
	}
}

func TestMediaOrganizerCloudCleanupRequiresObservedRemoval(t *testing.T) {
	s, drive, rule := newMediaCleanupCloud(t)
	drive.dirs["10"] = []p115.ShareFile{{ID: "500", Name: "a.txt", Size: 3}}
	drive.noDelete = true
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanup(context.Background(), files, &run); err == nil || run.Entries[0].Status == "done" {
		t.Fatal("提供方返回成功但来源仍存在，不能记录为已清理", err, run.Entries)
	}
}
