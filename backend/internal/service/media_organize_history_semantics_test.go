package service

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestMediaTaskOutcomeAndTimeBelongToPrimary(t *testing.T) {
	run := mediaRecordFixture("primary", "partial", 1)
	at := run.Entries[0].Result.At
	run.Entries = append(run.Entries, MediaOrganizeEntry{
		File:    OrganizeFile{ShareFile: p115.ShareFile{ID: "subtitle", Name: "Work.ass"}, MediaType: "subtitle"},
		VideoID: run.Entries[0].File.ID, Status: "error", Message: "附件失败", UpdatedAt: at.Add(time.Hour),
		Result: &MediaOrganizeResult{Status: "error", Message: "附件失败", At: at.Add(2 * time.Hour)},
	})
	task := mediaTasks(run)[0]
	if task.Status != "done" || task.Message != "" || !task.At.Equal(at) {
		t.Fatal("附件错误不能改变主文件的成功、消息或完成时间", task)
	}
	run.Status, run.TaskFileID = "queued", task.FileID
	task = mediaTasks(run)[0]
	if task.Status != "done" || !task.At.Equal(at) {
		t.Fatal("排队重试附件时主文件仍成功且完成时间不变", task)
	}
	run.Entries[0].Status, run.Entries[0].Message = "error", "主文件失败"
	task = mediaTasks(run)[0]
	if task.Status != "running" || task.Message != "已排队，等待整理" {
		t.Fatal("主文件排队应显示整理中及排队说明", task)
	}
	run.Status = "error"
	task = mediaTasks(run)[0]
	if task.Status != "failed" || task.Message != "主文件失败" || !task.At.Equal(at) {
		t.Fatal("主文件失败必须保留自身原因和时间", task)
	}
	run.Entries[0].Message, run.Entries[0].Result.Message = "", "主文件保存的回执原因"
	run.Groups[0].Message = "组的回退消息"
	if task = mediaTasks(run)[0]; task.Message != "主文件保存的回执原因" {
		t.Fatal("主文件回执原因优先于组或附件消息", task)
	}
}

func TestMediaHistoryAllowsDroppingIdleRecoveryRecords(t *testing.T) {
	for _, tc := range []struct {
		name   string
		orphan bool
		apply  func(*MediaOrganizeEntry)
	}{
		{"移动回执", false, func(entry *MediaOrganizeEntry) { entry.P115Move = &Media115MoveIntent{} }},
		{"失败目录回执", false, func(entry *MediaOrganizeEntry) { entry.Failure = &MediaOrganizeFailure{State: "moving"} }},
		{"回收回执", false, func(entry *MediaOrganizeEntry) { entry.AttachmentKeep = &MediaAttachmentKeepIntent{Phase: "recycling"} }},
		{"已提交来源快照", false, func(entry *MediaOrganizeEntry) {
			entry.AttachmentKeep = &MediaAttachmentKeepIntent{Phase: "selected", Source: &MediaAttachmentFileSnapshot{ID: "source"}}
		}},
		{"失去主文件归属", true, func(entry *MediaOrganizeEntry) { entry.P115Move = &Media115MoveIntent{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newMediaSQLiteTest(t)
			run := mediaRecordFixture("protected", "partial", 2)
			owner := run.Entries[0].File.ID
			entry := MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "pending"}, MediaType: "attachment"}, VideoID: owner, Status: "error"}
			if tc.orphan {
				entry.VideoID = "missing-video"
			}
			tc.apply(&entry)
			run.Entries = append(run.Entries, entry)
			saveMediaFixtures(t, s, run)
			for _, task := range mediaTasks(run) {
				got, err := s.Task(run.ID, task.FileID)
				if err != nil || got.Status != "done" || !got.CanDelete {
					t.Fatal("空闲回执不能锁住历史", got, err)
				}
			}
			items := []MediaOrganizeDeleteItem{{RunID: run.ID, FileID: run.Entries[1].File.ID}, {RunID: run.ID, FileID: owner}}
			if deleted, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Items: items}); deleted != 2 || err != nil {
				t.Fatal("删除历史只放弃记录，不需要调用网盘", deleted, err)
			}
			if len(s.doc.Runs) != 0 {
				t.Fatal("无主文件的旧恢复状态不应单独保留", s.doc.Runs)
			}
		})
	}
}
