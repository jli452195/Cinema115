package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMediaOrganizerLocalSameTarget(t *testing.T) {
	for _, scenario := range []string{"first succeeds", "first source changed", "target already exists", "target appears after scan"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, rule := newMediaOrganizeTest(t)
			const first = "a/Work.S01E01.strm"
			const second = "b/Work.S01E01.strm"
			const other = "Work.S01E02.strm"
			bodies := map[string]string{
				first: "http://example.invalid/first\r\n", second: "http://example.invalid/second\r\n",
				"a/Work.S01E01.ass": "first subtitle", "b/Work.S01E01.ass": "second subtitle", other: "other episode",
			}
			for name, body := range bodies {
				mediaTestFile(t, rule.Source, name, body)
			}
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &run, "scan")
			planned := make(map[string]MediaOrganizeEntry, len(run.Entries))
			for _, entry := range run.Entries {
				planned[entry.File.ID] = entry
			}
			if planned[first].Target == "" || planned[first].Target != planned[second].Target || planned[other].Target == planned[first].Target {
				t.Fatal("同集应生成同一目标，正常集保持独立", run.Entries)
			}
			if scenario == "first source changed" {
				mediaTestFile(t, rule.Source, first, "changed source after scan")
			} else if scenario == "target already exists" || scenario == "target appears after scan" {
				mediaTestFile(t, rule.Target, planned[first].Relative, "existing target")
				if scenario == "target already exists" {
					files, err := s.files(context.Background(), run.Rule)
					if err != nil {
						t.Fatal(err)
					}
					err = s.render(context.Background(), files, &run)
					files.close()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			mediaTestProcess(t, s, &run, "execute")
			for _, entry := range run.Entries {
				original := planned[entry.File.ID]
				if !reflect.DeepEqual(entry.File, original.File) || entry.Group != original.Group || entry.Relative != original.Relative || entry.Target != original.Target {
					t.Fatal("冲突处理不能改变识别、命名或附件归属", original, entry)
				}
				expected, expectedCode := "done", ""
				if entry.File.MediaType == "subtitle" {
					// 同一目标对应不同来源字幕，提前保留双方待核对。
					expected, expectedCode = "conflict", mediaSubtitleCollisionCode
				} else if entry.File.ID != other {
					isFirst := strings.HasPrefix(entry.File.SourcePath, "a/")
					failed := scenario == "first succeeds" && !isFirst || scenario == "first source changed" && isFirst || scenario == "target already exists" || scenario == "target appears after scan"
					if failed {
						expected, expectedCode = "conflict", "TARGET_EXISTS"
						if scenario == "first source changed" {
							expected = "error"
						}
					}
				}
				if entry.Status != expected {
					t.Fatalf("%s: expected %s, got %+v", entry.File.ID, expected, entry)
				}
				if expected == "conflict" && entry.ResultCode != expectedCode {
					t.Fatal("执行时冲突与预检使用相同结果码", entry)
				}
				source := filepath.Join(rule.Source, filepath.FromSlash(entry.File.SourcePath))
				if expected == "done" {
					data, err := os.ReadFile(entry.Target)
					if err != nil || string(data) != bodies[entry.File.ID] {
						t.Fatal("成功项应完整保留原内容", entry, string(data), err)
					}
					if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("成功项应移走来源", source, err)
					}
				} else if _, err := os.Stat(source); err != nil {
					t.Fatal("冲突或依赖失败必须保留来源", source, err)
				}
			}
			if scenario == "target already exists" || scenario == "target appears after scan" {
				data, err := os.ReadFile(planned[first].Target)
				if err != nil || string(data) != "existing target" {
					t.Fatal("不得覆盖既有目标", string(data), err)
				}
			}
		})
	}
}

func TestMediaOrganizerSameTargetBatchRetryFromFailure(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	rule.Failure = t.TempDir()
	rule, err := s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"Work.S01E01.1080p(1).strm", "Work.S01E01.1080p.strm", "Work.S01E02.strm"}
	for _, name := range names {
		mediaTestFile(t, rule.Source, name, name)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "scan")
	// 模拟旧版本将同目标输入全部拦截并隔离后的历史，验证原记录可直接批量重试。
	for i := range run.Entries {
		run.Entries[i].Status = "conflict"
		run.Entries[i].ResultCode = "BATCH_TARGET_CONFLICT"
		run.Entries[i].Message = "旧版本批次目标冲突"
	}
	mediaTestProcess(t, s, &run, "execute")
	for _, entry := range run.Entries {
		if entry.Failure == nil || entry.Failure.State != "stored" {
			t.Fatal("测试历史应从失败目录恢复", entry)
		}
	}
	untouched, err := s.Task(run.ID, names[2])
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, err := json.Marshal(untouched)
	if err != nil {
		t.Fatal(err)
	}
	selection := MediaOrganizeBatchRetry{Items: []MediaOrganizeDeleteItem{{RunID: run.ID, FileID: names[0]}, {RunID: run.ID, FileID: names[1]}}}
	result, err := s.RetryBatchContext(context.Background(), selection)
	if err != nil || result.Queued != 2 {
		t.Fatal("同目标历史应能一次选择两项重试", result, err)
	}
	queued, action, found := s.takeQueuedRunLocked()
	if !found || action != "retry" {
		t.Fatal("应按原批次进入重试", action, found)
	}
	if err := s.processQueuedRun(context.Background(), queued, action); err != nil {
		t.Fatal(err)
	}
	progress := s.retryBatchProgressLocked()
	if progress == nil || progress.Status != "completed" || progress.Success != 1 || progress.Failed != 1 || progress.Pending != 0 {
		t.Fatal("同目标重试应一成功一冲突", progress)
	}
	for _, name := range names[:2] {
		task, err := s.Task(run.ID, name)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == "done" {
			data, err := os.ReadFile(task.Target)
			if err != nil || string(data) != name {
				t.Fatal("应从失败目录原样恢复成功项", string(data), err)
			}
		} else {
			entry := task.Entries[0]
			if entry.Status != "conflict" || entry.ResultCode != "TARGET_EXISTS" || entry.Failure == nil || entry.Failure.State != "stored" {
				t.Fatal("另一项应明确冲突并留在失败目录", entry)
			}
			data, err := os.ReadFile(filepath.Join(rule.Failure, name))
			if err != nil || string(data) != name {
				t.Fatal("冲突原件不能被删掉", string(data), err)
			}
		}
	}
	after, err := s.Task(run.ID, names[2])
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil || string(beforeJSON) != string(afterJSON) {
		t.Fatal("不得改变未选中的普通识别记录", untouched, after, err)
	}
}
