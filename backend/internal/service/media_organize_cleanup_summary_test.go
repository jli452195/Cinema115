package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestMediaPersistenceBorrowedReferencesStayInCurrentRound(t *testing.T) {
	run := mediaRecordFixture("borrowed-round", "running", 1)
	borrowed := run.Entries[0]
	borrowed.Reference, borrowed.Result = true, nil
	run.Entries = nil
	run.Groups = nil
	for i := range 880 {
		entry := borrowed
		entry.File.ID, entry.Group = fmt.Sprintf("video-%d", i), fmt.Sprintf("group-%d", i)
		run.Entries = append(run.Entries, entry)
		run.Groups = append(run.Groups, MediaOrganizeGroup{ID: entry.Group, TMDBID: 123})
	}
	row, err := mediaRunRow(run)
	if err != nil {
		t.Fatal(err)
	}
	var stored MediaOrganizeRun
	if err := json.Unmarshal([]byte(row.Record), &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Entries) != 0 || len(stored.Groups) != 0 || len(row.Tasks) != 0 || len(row.Record) > 10000 {
		t.Fatal("晚到附件借用的引用及分组不能重复落库", len(stored.Entries), len(stored.Groups), len(row.Record))
	}
	if len(run.Entries) != 880 || len(run.Groups) != 880 || run.Entries[0].Result != nil {
		t.Fatal("精简存储不能改变本轮附件仍需使用的历史引用")
	}
}

func mediaCleanupSummaryEntry(id string, directory bool, status string) MediaOrganizeEntry {
	return MediaOrganizeEntry{
		File:   OrganizeFile{ShareFile: p115.ShareFile{ID: id, Name: id, Directory: directory}, MediaType: "cleanup"},
		Status: status, Result: &MediaOrganizeResult{Status: status},
	}
}

func TestMediaPersistenceCleanupSummaryPrecedesCompaction(t *testing.T) {
	run := mediaRecordFixture("cleanup-summary", "failed", 1)
	run.Entries = append(run.Entries,
		mediaCleanupSummaryEntry("orphan-file", false, "done"),
		mediaCleanupSummaryEntry("empty-directory", true, "done"),
		mediaCleanupSummaryEntry("failed-orphan", false, "error"),
	)
	row, err := mediaRunRow(run)
	if err != nil {
		t.Fatal(err)
	}
	var cleanup MediaOrganizeCleanup
	if err := json.Unmarshal([]byte(row.Cleanup), &cleanup); err != nil {
		t.Fatal(err)
	}
	if cleanup.Files != 1 || cleanup.Directories != 1 || cleanup.Failed != 1 {
		t.Fatal("精简附件条目前应保留清理结果计数", cleanup)
	}
	var stored MediaOrganizeRun
	if err := json.Unmarshal([]byte(row.Record), &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Entries) != 1 || stored.Entries[0].File.MediaType != "video" || len(row.Tasks) != 1 || row.Tasks[0].Group != "done" || row.Activity != "" {
		t.Fatal("清理失败不应覆盖视频成功，也不需要持久保存清理文件列表", stored, row.Tasks)
	}
}

func TestMediaHistoryCleanupCountsFollowCurrentCheckpoint(t *testing.T) {
	s, _, _ := newMediaSQLiteTest(t)
	run := mediaRecordFixture("cleanup-progress", "running", 1)
	run.Entries[0].Reference, run.Entries[0].Result = true, nil
	run.Entries = append(run.Entries,
		mediaCleanupSummaryEntry("first-file", false, "done"),
		mediaCleanupSummaryEntry("writing-file", false, "error"),
	)
	saveMediaFixtures(t, s, run)
	history, err := s.History(context.Background(), "", "all", 1)
	if err != nil {
		t.Fatal(err)
	}
	if history.Total != 0 || len(history.Cleanup) != 1 || history.Cleanup[0].Files != 1 || history.Cleanup[0].Failed != 0 {
		t.Fatal("运行中的已清数量应可见，写前占位不能暂时显示为失败", history)
	}
	run.Entries[2].Status, run.Entries[2].Result.Status = "done", "done"
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	history, err = s.History(context.Background(), "", "all", 1)
	if err != nil || len(history.Cleanup) != 1 || history.Cleanup[0].Files != 2 || history.Cleanup[0].Failed != 0 || history.Counts["done"] != 0 {
		t.Fatal("清理计数随检查点更新，附件不进入视频成功计数", history, err)
	}
}
