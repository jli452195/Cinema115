package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

func TestMediaPersistenceStoresCompactPrimaryTaskSummaries(t *testing.T) {
	run := mediaRecordFixture("compact", "partial", 2)
	run.Entries[0].Relative = run.Entries[0].File.Name
	run.Entries[1].Status, run.Entries[1].Message = "conflict", "目标文件冲突"
	run.Entries[1].Result = &MediaOrganizeResult{Status: "error", Message: "目标文件冲突"}
	run.Entries = append(run.Entries,
		MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "success-sub"}, MediaType: "subtitle"}, VideoID: run.Entries[0].File.ID, Status: "error"},
		MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "failed-sub"}, MediaType: "subtitle"}, VideoID: run.Entries[1].File.ID, Status: "error"},
	)
	row, err := mediaRunRow(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Tasks) != 2 || row.Tasks[0].FileID != run.Entries[0].File.ID || row.Tasks[0].Group != "done" || row.Tasks[1].FileID != run.Entries[1].File.ID || row.Tasks[1].Group != "failed" || row.Activity != "" || row.Cleanup != "" {
		t.Fatal("成功与失败主文件都应保存摘要，附件不单独计入", row)
	}
	var stored MediaOrganizeRun
	if err := json.Unmarshal([]byte(row.Record), &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Entries) != 3 || !stored.Entries[0].Reference || stored.Entries[0].Result == nil || stored.Entries[0].Result.Status != "done" || !stored.Entries[0].Result.At.Equal(run.Entries[0].Result.At) || stored.Entries[0].Relative != run.Entries[0].Relative || stored.Entries[1].Reference {
		t.Fatal("成功主文件应保留精简结果和只读映射，失败主文件保留上下文", stored.Entries)
	}
	if slices.ContainsFunc(stored.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == "success-sub" }) || !slices.ContainsFunc(stored.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == "failed-sub" }) {
		t.Fatal("成功附件不应持久化，失败主文件仍需其重试附件", stored.Entries)
	}
	if run.Entries[0].Reference || run.Entries[0].Result == nil || len(run.Entries) != 4 {
		t.Fatal("压缩存储副本不能改变当前轮次的运行显示", run.Entries)
	}
}

func TestMediaPersistenceKeepsActiveMainRecoveryContext(t *testing.T) {
	s, db, dir := newMediaSQLiteTest(t)
	run := mediaRecordFixture("active-main", "running", 1)
	run.Entries[0].Status, run.Entries[0].Result = "ready", nil
	run.Entries[0].P115Move = &Media115MoveIntent{Phase: "moving", TargetCID: "target"}
	saveMediaFixtures(t, s, run)
	durable, err := db.QueryMediaOrganize(context.Background(), "", "all", 1)
	if err != nil || durable.Total != 0 {
		t.Fatal("活动主文件不应写入运行中任务摘要", durable, err)
	}
	reopened, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, s.templates, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := reopened.Snapshot(run.ID)
	if err != nil || recovered.Status != "interrupted" || len(recovered.Entries) != 1 || recovered.Entries[0].Reference || recovered.Entries[0].P115Move == nil {
		t.Fatal("重启必须保留未完成主文件的实际上下文，不能标为成功", recovered, err)
	}
	durable, err = db.QueryMediaOrganize(context.Background(), "", "failed", 1)
	if err != nil || durable.Total != 1 {
		t.Fatal("重启后未完成主文件应作为失败供用户重试", durable, err)
	}
}

func TestMediaPersistenceKeepsActiveRoundFrozenContext(t *testing.T) {
	for _, status := range []string{"queued", "scanning", "running"} {
		t.Run(status, func(t *testing.T) {
			run := mediaRecordFixture("active-round", status, 1)
			run.Trigger, run.RecognitionVersion = "manual", "pinned-policy"
			run.Classification = ClassificationSnapshot{YAML: "固定分类规则", Revision: "pinned-revision"}
			for _, entries := range [][]MediaOrganizeEntry{nil, run.Entries} {
				run.Entries = entries
				stored := mediaPersistentRun(run)
				if stored.Status != status || stored.Trigger != run.Trigger || stored.RecognitionVersion != run.RecognitionVersion || stored.Classification.YAML != run.Classification.YAML || stored.Classification.Revision != run.Classification.Revision {
					t.Fatal("扫描或清理尚未结束时，即使没有未完成主文件也必须保留本轮冻结与队列上下文", stored)
				}
			}
		})
	}
}

func TestMediaPersistenceSuccessRetainsHistoryAndMapping(t *testing.T) {
	s, db, dir := newMediaSQLiteTest(t)
	run := mediaRecordFixture("successful", "completed", 1)
	run.Entries[0].Relative = run.Entries[0].File.Name
	run.Entries[0].Result.Action = "duplicate"
	row, err := mediaRunRow(run)
	if err != nil || len(row.Tasks) != 1 || !strings.Contains(row.Record, `"status":"done"`) || !strings.Contains(row.Record, `"action":"duplicate"`) {
		t.Fatal("去重成功应保存完成结果与必要动作", row.Record, err)
	}
	saveMediaFixtures(t, s, run)
	item := mediaTasks(run)[0].MediaOrganizeHistoryItem
	summary, err := mediaHistoryTaskRow(item)
	if err != nil {
		t.Fatal(err)
	}
	live, err := db.QueryMediaOrganizeLive(context.Background(), "", "done", 1, []storage.MediaOrganizeLiveTaskRow{{RunID: run.ID, MediaOrganizeTaskRow: summary}})
	if err != nil || live.Total != 1 {
		t.Fatal("当前内存轮次仍应可以显示成功结果", live, err)
	}
	reopened, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, s.templates, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := db.QueryMediaOrganize(context.Background(), "", "all", 1)
	if err != nil || durable.Total != 1 || durable.Counts["done"] != 1 || len(reopened.doc.Runs) != 0 {
		t.Fatal("重启后成功历史及其完成时间应保留", durable, reopened.doc.Runs, err)
	}
	index, err := reopened.Snapshot(run.ID)
	if err != nil || len(index.Entries) != 1 || !index.Entries[0].Reference || index.Entries[0].Relative != run.Entries[0].Relative || index.Entries[0].Target != run.Entries[0].Target || index.Entries[0].Result == nil || index.Entries[0].Result.Action != "duplicate" {
		t.Fatal("晚到附件所需的已核实目标映射和去重语义不能丢失", index, err)
	}
}

func TestMediaPersistenceSuccessRemovesPriorFailureSummary(t *testing.T) {
	s, db, _ := newMediaSQLiteTest(t)
	run := mediaRecordFixture("retry-success", "partial", 1)
	run.Entries[0].Status = "error"
	saveMediaFixtures(t, s, run)
	failed, err := db.QueryMediaOrganize(context.Background(), "", "failed", 1)
	if err != nil || failed.Total != 1 {
		t.Fatal("应保留最初失败摘要", failed, err)
	}
	run.Entries[0].Status, run.Entries[0].Relative = "done", run.Entries[0].File.Name
	run.Status = "completed"
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	durable, err := db.QueryMediaOrganize(context.Background(), "", "all", 1)
	if err != nil || durable.Total != 1 || durable.Counts["done"] != 1 || durable.Counts["failed"] != 0 {
		t.Fatal("重试成功必须事务更新为成功摘要", durable, err)
	}
}

func mediaPersistenceResolvedFixture() (MediaOrganizeRun, MediaOrganizeRun) {
	previous := mediaRecordFixture("previous-failure", "partial", 1)
	previous.Rule.Storage, previous.Rule.AccountID, previous.Rule.SourceID, previous.Rule.TargetID = "115", "account", "10", "20"
	previous.Rule.Target = "/target"
	previous.Entries[0].File.Size, previous.Entries[0].File.SHA1 = 123, strings.Repeat("A", 40)
	previous.Entries[0].Status, previous.Entries[0].Message = "error", "原整理失败"
	current := previous
	current.ID, current.Status = "current-success", "completed"
	current.Entries = slices.Clone(previous.Entries)
	current.Entries[0].Status, current.Entries[0].Message = "done", ""
	return previous, current
}

func TestMediaPersistenceForgetsResolvedFailureAndCompanions(t *testing.T) {
	previous, current := mediaPersistenceResolvedFixture()
	previous.Entries = append(previous.Entries,
		MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "old-subtitle"}, MediaType: "subtitle"}, VideoID: previous.Entries[0].File.ID, Status: "error"},
		MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "other-video"}, MediaType: "video"}, Group: "group", Status: "error"},
	)
	queued, active := previous, previous
	queued.ID, active.ID, active.Status = "queued-action", "active-run", "running"
	doc := mediaOrganizeDocument{Runs: []MediaOrganizeRun{previous, queued, active, current}}
	changed := mediaForgetResolvedFailures(&doc, current, func(id string) bool { return id == queued.ID })
	if !slices.Equal(changed, []string{previous.ID}) || len(doc.Runs) != 4 || len(doc.Runs[0].Entries) != 1 || doc.Runs[0].Entries[0].File.ID != "other-video" {
		t.Fatal("同来源再次成功应只移除旧失败主文件及其附件", changed, doc.Runs)
	}
	if len(doc.Runs[1].Entries) != 3 || len(doc.Runs[2].Entries) != 3 {
		t.Fatal("活动或仍有排队动作的批次不能自动移除", doc.Runs)
	}
	previous, current = mediaPersistenceResolvedFixture()
	doc.Runs = []MediaOrganizeRun{previous, current}
	changed = mediaForgetResolvedFailures(&doc, current, nil)
	if !slices.Equal(changed, []string{previous.ID}) || len(doc.Runs) != 1 || doc.Runs[0].ID != current.ID {
		t.Fatal("最后一个旧失败消除后应删除空批次，并返回待持久化的原批次编号", changed, doc.Runs)
	}
}

func TestMediaPersistenceResolvedFailureRequiresFrozenScopeAndIdentity(t *testing.T) {
	for _, scenario := range []string{"rule", "account", "source-id", "source", "target", "id", "size", "hash", "reference"} {
		t.Run(scenario, func(t *testing.T) {
			previous, current := mediaPersistenceResolvedFixture()
			switch scenario {
			case "rule":
				current.Rule.ID += "-other"
			case "account":
				current.Rule.AccountID += "-other"
			case "source-id":
				current.Rule.SourceID += "-other"
			case "source":
				current.Rule.Source += "/other"
			case "target":
				current.Rule.Target += "/other"
			case "id":
				current.Entries[0].File.ID += "-other"
			case "size":
				current.Entries[0].File.Size++
			case "hash":
				current.Entries[0].File.SHA1 = strings.Repeat("B", 40)
			case "reference":
				current.Entries[0].Reference = true
			}
			doc := mediaOrganizeDocument{Runs: []MediaOrganizeRun{previous, current}}
			if changed := mediaForgetResolvedFailures(&doc, current, nil); len(changed) != 0 || len(doc.Runs) != 2 {
				t.Fatal("不同冻结绑定、文件身份或被动引用不能清除旧失败", changed, doc.Runs)
			}
		})
	}
}

func TestMediaPersistenceResolvedLocalAndCD2Identity(t *testing.T) {
	for _, storageType := range []string{"local", "cd2"} {
		for _, scenario := range []string{"same", "modified", "native-id", "revision"} {
			t.Run(storageType+"/"+scenario, func(t *testing.T) {
				previous, current := mediaPersistenceResolvedFixture()
				previous.Rule.Storage, current.Rule.Storage = storageType, storageType
				previous.Entries[0].Modified, current.Entries[0].Modified = 1234, 1234
				previous.Entries[0].CD2FileID, current.Entries[0].CD2FileID = "native-source-id", "native-source-id"
				switch scenario {
				case "modified":
					current.Entries[0].Modified++
				case "native-id":
					if storageType == "cd2" {
						current.Entries[0].CD2FileID += "-other"
					} else {
						current.Entries[0].File.SourcePath += "-other"
					}
				case "revision":
					current.Rule.ConnectionRevision++
				}
				doc := mediaOrganizeDocument{Runs: []MediaOrganizeRun{previous, current}}
				changed := mediaForgetResolvedFailures(&doc, current, nil)
				if (len(changed) == 1) != (scenario == "same") {
					t.Fatal("路径型标识必须结合来源修改时间及原生身份，CD2 还须核对连接修订", changed, doc.Runs)
				}
			})
		}
	}
}

func TestMediaPersistenceResolvedCD2DuplicateAllowsKeeperSizeOnlyWithFrozenSHA1(t *testing.T) {
	for _, scenario := range []string{"duplicate", "ordinary", "not-done", "missing-hash", "short-hash", "invalid-hash", "different-hash", "different-id", "different-native-id", "different-binding", "115", "local"} {
		t.Run(scenario, func(t *testing.T) {
			previous, current := mediaPersistenceResolvedFixture()
			previous.Rule.Storage, current.Rule.Storage = "cd2", "cd2"
			previous.Entries[0].CD2FileID, current.Entries[0].CD2FileID = "original-native-id", "original-native-id"
			previous.Entries[0].CD2Hashes = map[uint32]string{2: strings.Repeat("A", 40)}
			current.Entries[0].CD2Hashes = map[uint32]string{2: strings.Repeat("a", 40)}
			current.Entries[0].File.Size++
			result := *current.Entries[0].Result
			result.Action = "duplicate"
			current.Entries[0].Result = &result
			switch scenario {
			case "ordinary":
				current.Entries[0].Result.Action = ""
			case "not-done":
				current.Entries[0].Status = "error"
			case "missing-hash":
				current.Entries[0].CD2Hashes = nil
			case "short-hash":
				previous.Entries[0].CD2Hashes[2], current.Entries[0].CD2Hashes[2] = "same", "same"
			case "invalid-hash":
				previous.Entries[0].CD2Hashes[2], current.Entries[0].CD2Hashes[2] = strings.Repeat("G", 40), strings.Repeat("G", 40)
			case "different-hash":
				current.Entries[0].CD2Hashes[2] = strings.Repeat("B", 40)
			case "different-id":
				current.Entries[0].File.ID += "-other"
			case "different-native-id":
				current.Entries[0].CD2FileID += "-other"
			case "different-binding":
				current.Rule.ConnectionRevision++
			case "115", "local":
				previous.Rule.Storage, current.Rule.Storage = scenario, scenario
				previous.Entries[0].Modified, current.Entries[0].Modified = 123, 123
			}
			doc := mediaOrganizeDocument{Runs: []MediaOrganizeRun{previous, current}}
			changed := mediaForgetResolvedFailures(&doc, current, nil)
			if scenario == "duplicate" {
				if !slices.Equal(changed, []string{previous.ID}) || len(doc.Runs) != 1 || doc.Runs[0].ID != current.ID {
					t.Fatal("同冻结来源成功采用 keeper 大小后应清除旧失败", changed, doc.Runs)
				}
			} else if len(changed) != 0 || len(doc.Runs) != 2 {
				t.Fatal("大小差异例外不能放宽普通成功、内容或来源身份核验", scenario, changed, doc.Runs)
			}
		})
	}
}
