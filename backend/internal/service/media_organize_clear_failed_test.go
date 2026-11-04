package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestMediaHistoryClearFailedAcrossPagesPreservesSuccessAndRunning(t *testing.T) {
	s, db, dir := newMediaSQLiteTest(t)
	mixed := mediaRecordFixture("mixed", "partial", 26)
	other := mediaRecordFixture("other", "partial", 5)
	protected := mediaRecordFixture("protected", "partial", 1)
	running := mediaRecordFixture("running", "running", 1)
	for _, item := range []struct {
		run   *MediaOrganizeRun
		count int
	}{{&mixed, 23}, {&other, 4}, {&protected, 1}} {
		for i := range item.count {
			item.run.Entries[i].Status, item.run.Entries[i].Result.Status = "error", "error"
		}
	}
	running.TaskFileID = running.Entries[0].File.ID
	running.Entries[0].Status, running.Entries[0].Result = "ready", nil
	mixed.Entries[0].Failure = &MediaOrganizeFailure{State: "moving"}
	mixed.Rule.Source = t.TempDir()
	mediaTestFile(t, mixed.Rule.Source, mixed.Entries[0].File.ID, "保留失败来源")
	target := t.TempDir()
	mediaTestFile(t, target, "kept.strm", "保留目标内容")
	mixed.Entries[0].Target = filepath.Join(target, "kept.strm")
	mixed.Entries = append(mixed.Entries, MediaOrganizeEntry{
		File:    OrganizeFile{ShareFile: p115.ShareFile{ID: "failed-subtitle"}, MediaType: "subtitle"},
		VideoID: mixed.Entries[23].File.ID, Group: "group", Status: "error",
	})
	saveMediaFixtures(t, s, mixed, other, protected, running)
	s.active = running.ID
	_, release := s.beginMediaEntries(&protected, []int{0})
	defer release()
	if deleted, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Status: "failed"}); err != nil || deleted != 27 {
		t.Fatal("全部失败应跨页删除并跳过正在操作的记录", deleted, err)
	}
	if history := mediaHistoryTest(t, s, "", "all", 1); history.Total != 6 || history.Counts["done"] != 4 || history.Counts["failed"] != 1 || history.Counts["running"] != 1 {
		t.Fatal("成功视频及运行中的记录必须保留", history)
	}
	if deleted, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Status: "failed"}); err != nil || deleted != 0 {
		t.Fatal("重复清理不能扩大到其他状态或受保护记录", deleted, err)
	}
	for filename, expected := range map[string]string{
		filepath.Join(mixed.Rule.Source, mixed.Entries[0].File.ID): "保留失败来源",
		mixed.Entries[0].Target: "保留目标内容",
	} {
		if body, err := os.ReadFile(filename); err != nil || string(body) != expected {
			t.Fatal("清理记录不得修改实际文件", filename, err)
		}
	}
	reopened, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if history := mediaHistoryTest(t, reopened, "", "all", 1); history.Total != 6 || history.Counts["done"] != 4 {
		t.Fatal("清理后的失败记录不能在重启后恢复", history)
	}
}
