package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestMediaSuccessHistoryPersistsPrimaryResultAcrossRestart(t *testing.T) {
	s, db, dir := newMediaSQLiteTest(t)
	run := mediaRecordFixture("durable-success", "partial", 1)
	entry := &run.Entries[0]
	entry.Relative = "作品/Season 01/Work.S01E01.strm"
	entry.Target = "/target/" + entry.Relative
	entry.File.PickCode = "临时取链字段"
	entry.UpdatedAt = entry.Result.At.Add(time.Hour)
	entry.Result.Action = "duplicate"
	at := entry.Result.At
	run.Entries = append(run.Entries, MediaOrganizeEntry{
		File:    OrganizeFile{ShareFile: p115.ShareFile{ID: "failed-subtitle", Name: "Work.S01E01.ass"}, MediaType: "subtitle"},
		VideoID: entry.File.ID, Status: "error", Message: "字幕失败", UpdatedAt: at.Add(2 * time.Hour),
	})
	saveMediaFixtures(t, s, run)
	for range 2 {
		reopened, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, s.templates, nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		s = reopened
		history := mediaHistoryTest(t, s, "作品", "done", 1)
		if history.Total != 1 || history.Counts["failed"] != 0 || !history.Items[0].At.Equal(at) || history.Items[0].Action != "duplicate" || history.Items[0].AttachmentCount != 0 {
			t.Fatal("重启应保留主文件原完成结果，附件失败和检查点时间不能覆盖", history)
		}
		task, err := s.Task(run.ID, run.Entries[0].File.ID)
		if err != nil || task.Status != "done" || task.CanRetry || !task.CanDelete || len(task.Entries) != 1 || !task.At.Equal(at) || task.Target != run.Entries[0].Target || task.Source != "/source/Work.S01E01.strm" {
			t.Fatal("成功详情和删除权限应可用，持久化不带全量附件", task, err)
		}
		if !task.Entries[0].Reference || task.Entries[0].File.PickCode != "" || !task.Entries[0].UpdatedAt.IsZero() || task.Group.Identity.Name != "" {
			t.Fatal("成功只保留精简结果及只读位置映射", task)
		}
	}
	rows, err := db.LoadMediaOrganize(context.Background())
	if err != nil || len(rows.Runs) != 1 || strings.Contains(rows.Runs[0], "failed-subtitle") || strings.Contains(rows.Runs[0], "临时取链字段") {
		t.Fatal("成功历史不应保存附件失败或取链临时信息", rows, err)
	}
	var compact struct {
		Entries []struct {
			UpdatedAt json.RawMessage            `json:"updatedAt"`
			Result    map[string]json.RawMessage `json:"result"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(rows.Runs[0]), &compact); err != nil || len(compact.Entries) != 1 || len(compact.Entries[0].UpdatedAt) != 0 || compact.Entries[0].Result["target"] != nil || compact.Entries[0].Result["status"] == nil || compact.Entries[0].Result["at"] == nil {
		t.Fatal("成功只保存一次完成时间和目标，同时保留明确成功证据", compact, err)
	}
}

func TestMediaSuccessHistoryKeepsExplicitUnknownCompletionTime(t *testing.T) {
	s, db, dir := newMediaSQLiteTest(t)
	run := mediaRecordFixture("recovered-unknown", "completed", 1)
	run.CreatedAt = run.UpdatedAt.Add(-time.Hour)
	run.Entries[0].Reference = true
	run.Entries[0].UpdatedAt = run.UpdatedAt
	run.Entries[0].Result.At = time.Time{}
	saveMediaFixtures(t, s, run)
	for range 2 {
		reopened, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, s.templates, nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		s = reopened
		history := mediaHistoryTest(t, s, "", "done", 1)
		if history.Total != 1 || !history.Items[0].At.IsZero() {
			t.Fatal("经证据恢复的成功可保留未知时间，不能用批次时间补写", history)
		}
		task, err := s.Task(run.ID, run.Entries[0].File.ID)
		if err != nil || !task.At.IsZero() || task.Status != "done" || task.Entries[0].Result == nil || !task.Entries[0].Result.At.IsZero() || !task.Entries[0].UpdatedAt.IsZero() {
			t.Fatal("详情和再次存储必须保持完成时间未知", task, err)
		}
	}
}

func TestMediaSuccessHistoryDoesNotInferFromReferenceOrCheckpointTime(t *testing.T) {
	for _, action := range []string{"", "duplicate"} {
		t.Run(action, func(t *testing.T) {
			run := mediaRecordFixture("only-reference", "completed", 1)
			run.Entries[0].Reference = true
			run.Entries[0].UpdatedAt = run.UpdatedAt
			run.Entries[0].Result = nil
			if action != "" {
				run.Entries[0].Result = &MediaOrganizeResult{Action: action}
			}
			run.Entries = append(run.Entries, MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "late-subtitle"}, MediaType: "subtitle"}, VideoID: run.Entries[0].File.ID, Status: "error"})
			row, err := mediaRunRow(run)
			if err != nil || len(row.Tasks) != 0 || len(mediaTasks(run)) != 0 {
				t.Fatal("引用存在、字幕失败或检查点非零时间均不能自动恢复成功历史", row.Tasks, err)
			}
			var stored MediaOrganizeRun
			if err := json.Unmarshal([]byte(row.Record), &stored); err != nil || len(stored.Entries) != 0 || len(stored.Groups) != 0 {
				t.Fatal("无完成证据的借用引用不应重复持久化", stored, err)
			}
		})
	}
}

func TestMediaSuccessHistoryReferencesDoNotDuplicateOrResurrectDeletedSuccess(t *testing.T) {
	s, db, dir := newMediaSQLiteTest(t)
	original := mediaRecordFixture("original-success", "completed", 1)
	original.Entries[0].Relative = original.Entries[0].File.Name
	original.Entries[0].Result.Action = "duplicate"
	at := original.Entries[0].Result.At
	saveMediaFixtures(t, s, original)
	aliases := make([]MediaOrganizeRun, 0, 3)
	for _, id := range []string{"reference-one", "reference-two", "reference-three"} {
		var alias MediaOrganizeRun
		cloneMediaRun(original, &alias)
		alias.ID, alias.Status = id, "running"
		alias.CreatedAt, alias.UpdatedAt = at.Add(time.Hour), at.Add(2*time.Hour)
		alias.Entries[0].Reference = true
		alias.Entries[0].UpdatedAt = alias.UpdatedAt
		alias.Entries[0].Result = &MediaOrganizeResult{Action: "duplicate"}
		alias.Entries = append(alias.Entries, MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: id + "-subtitle"}, MediaType: "subtitle"}, VideoID: original.Entries[0].File.ID, Status: "done"})
		aliases = append(aliases, alias)
		saveMediaFixtures(t, s, alias)
		if err := s.checkpoint(&aliases[len(aliases)-1]); err != nil {
			t.Fatal(err)
		}
	}
	history := mediaHistoryTest(t, s, "", "all", 1)
	if history.Total != 1 || !history.Items[0].At.Equal(at) || history.Items[0].RunID != original.ID {
		t.Fatal("多轮晚到附件引用不能重复生成或刷新主文件成功历史", history)
	}
	if deleted, err := s.DeleteHistory(context.Background(), MediaOrganizeDelete{Status: "done"}); err != nil || deleted != 1 {
		t.Fatal("清理全部成功只应删除拥有成功事实的记录", deleted, err)
	}
	for i := range aliases {
		aliases[i].Status = "completed"
		if err := s.checkpoint(&aliases[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Task(original.ID, original.Entries[0].File.ID); !errors.Is(err, ErrMediaOrganizeTaskNotFound) {
		t.Fatal("手动清理后原成功详情应不存在", err)
	}
	reopened, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, s.templates, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	history = mediaHistoryTest(t, reopened, "", "all", 1)
	if history.Total != 0 || !reflect.DeepEqual(history.Counts, map[string]int{"all": 0, "done": 0, "running": 0, "failed": 0}) {
		t.Fatal("引用检查点或重启不能复活手动删除的成功历史", history)
	}
}
