package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

func TestMediaHistorySnapshotExcludesRuntimePermissions(t *testing.T) {
	run := mediaRecordFixture("permissions", "partial", 1)
	run.Entries[0].Status, run.Entries[0].Result.Status = "error", "error"
	row, err := mediaRunRow(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Tasks) != 1 {
		t.Fatal("失败主文件应生成一条精简摘要", row.Tasks)
	}
	for _, field := range []string{`"canDelete"`, `"canRetry"`} {
		if strings.Contains(row.Tasks[0].Record, field) {
			t.Fatal("实时权限不应持久化", field, row.Tasks[0].Record)
		}
	}
	for _, field := range []string{`"readyCount"`, `"failureCount"`, `"recognition"`} {
		if strings.Contains(row.Record, field) {
			t.Fatal("批次仍保存冗余字段", field)
		}
	}
}

func TestMediaOrganizerOpenRebuildsDerivedTaskStates(t *testing.T) {
	s, db, dir := newMediaSQLiteTest(t)
	run := mediaRecordFixture("rebuild", "partial", 1)
	run.Entries = append(run.Entries, MediaOrganizeEntry{
		File:    OrganizeFile{ShareFile: p115.ShareFile{ID: "subtitle"}, MediaType: "subtitle"},
		VideoID: run.Entries[0].File.ID, Status: "error", Message: "字幕处理失败",
	})
	row, err := mediaRunRow(run)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟仍按附件聚合的旧摘要，批次原始结果不变。
	item := mediaTasks(run)[0].MediaOrganizeHistoryItem
	item.Status = "partial"
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	row.Tasks = []storage.MediaOrganizeTaskRow{{FileID: item.FileID, Group: "attention", At: mediaSortTime(item.At), Record: string(raw)}}
	legacy, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	row.Record = string(legacy)
	if err := db.SaveMediaOrganize(context.Background(), `{"rules":[]}`, []storage.MediaOrganizeRunRow{row}, nil); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, s.templates, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	history := mediaHistoryTest(t, restored, "", "all", 1)
	if history.Total != 1 || history.Counts["done"] != 1 || history.Counts["failed"] != 0 {
		t.Fatal("启动应按主文件恢复成功历史，附件失败不能改变视频结果", history)
	}
	index, err := restored.Snapshot(run.ID)
	if err != nil || len(index.Entries) != 1 || !index.Entries[0].Reference {
		t.Fatal("成功记录应压缩为不含失败附件的只读主文件映射", index, err)
	}
}

func TestMediaLogsOnlyPublishFinalFailures(t *testing.T) {
	at := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	previous := MediaOrganizeRun{ID: "run", Status: "running", UpdatedAt: at}
	current := previous
	current.UpdatedAt = at.Add(time.Second)
	current.Entries = []MediaOrganizeEntry{{
		File:   OrganizeFile{ShareFile: p115.ShareFile{ID: "video", Name: "Work.mkv"}, MediaType: "video"},
		Status: "error", Message: "写入结果待核实", P115Move: &Media115MoveIntent{Phase: "moving"},
	}}
	mediaCheckpointResults(&current, previous)
	if len(current.recognitionEvents) != 0 {
		t.Fatal("写入准备不应生成失败日志", current.recognitionEvents)
	}
	previous = current
	current.Status = "partial"
	mediaCheckpointResults(&current, previous)
	if len(current.recognitionEvents) != 1 || current.recognitionEvents[0].details.ResultCode != "MOVE_UNCONFIRMED" {
		t.Fatal("结束后仍未确认的写入必须保留失败结果", current.recognitionEvents)
	}
	current.recognitionEvents = nil
	previous = current
	mediaCheckpointResults(&current, previous)
	if len(current.recognitionEvents) != 0 {
		t.Fatal("重复检查点不应重复输出最终失败", current.recognitionEvents)
	}

	previous = current
	current.Status = "running"
	current.Entries = append([]MediaOrganizeEntry(nil), current.Entries...)
	current.Entries[0].Status, current.Entries[0].Message, current.Entries[0].P115Move = "done", "", nil
	mediaCheckpointResults(&current, previous)
	previous = current
	current.Status = "completed"
	mediaCheckpointResults(&current, previous)
	if len(current.recognitionEvents) != 0 {
		t.Fatal("成功恢复不应产生过程或成功日志", current.recognitionEvents)
	}
}

func TestMediaFinalFailureLogWaitsForCommittedResult(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	s.Logs = &GenerationLogs{}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Entries = []MediaOrganizeEntry{{
		File:   OrganizeFile{ShareFile: p115.ShareFile{ID: "video", Name: "Unknown.mkv"}, MediaType: "video"},
		Status: "needs_match", Message: "未找到对应作品",
	}}
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	run.Status = "partial"
	store.err = errors.New("模拟最终结果保存失败")
	if err := s.checkpoint(&run); err == nil {
		t.Fatal("应报告保存失败")
	}
	if len(s.Logs.Read("ALL", "整理", 0, false).Entries) != 0 || len(run.recognitionEvents) != 0 {
		t.Fatal("保存失败不应发布或残留最终失败事件")
	}
	store.err = nil
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	logs := s.Logs.Read("ALL", "整理", 0, false).Entries
	if len(logs) != 1 || logs[0].Recognition.ResultCode != "MATCH_NOT_FOUND" || logs[0].TaskID != run.ID+":video" {
		t.Fatal("保存恢复后应只发布一次带文件定位的最终失败", logs)
	}
	if err := s.checkpoint(&run); err != nil || len(s.Logs.Read("ALL", "整理", 0, false).Entries) != 1 {
		t.Fatal("重复保存发布了重复失败", err)
	}
}
