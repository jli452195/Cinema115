package service

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestMediaRetryLocationOlderFailureUsesConfirmedIsolation(t *testing.T) {
	s, metadata, _, rule := newMediaOrganizeTest(t)
	rule.Failure = t.TempDir()
	var err error
	rule, err = s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"season/Unknown.S01E01.strm", "season/Unknown.S01E01.ass", "season/Other.S01E02.strm"} {
		mediaTestFile(t, rule.Source, name, name)
	}
	metadata.items, metadata.err = nil, errors.New("模拟第一次元数据暂时不可用")
	first, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &first, "auto")
	metadata.err = nil
	second, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &second, "auto")
	for _, entry := range second.Entries {
		if entry.File.MediaType != "cleanup" && (entry.Failure == nil || entry.Failure.State != "stored") {
			t.Fatal("第二轮应有已确认的隔离位置", entry)
		}
	}
	const selected = "season/Unknown.S01E01.strm"
	if _, err := s.RetryContext(context.Background(), first.ID, MediaOrganizeCorrection{FileID: selected, Kind: "tv", TMDBID: 42}); err != nil {
		t.Fatal(err)
	}
	worker, action, found := s.takeQueuedRunLocked()
	if !found || worker.ID != first.ID {
		t.Fatal("应重试第一轮历史", worker, found)
	}
	if err := s.processQueuedRun(context.Background(), worker, action); err != nil {
		t.Fatal(err)
	}
	task, err := s.Task(first.ID, selected)
	if err != nil || task.Status != "done" {
		t.Fatal("旧失败应继承第二轮隔离证据后完成", task, err)
	}
	data, err := os.ReadFile(filepath.FromSlash(task.Target))
	if err != nil || string(data) != selected {
		t.Fatal("继承位置后应保持原文件内容", string(data), err)
	}
	if _, err := os.Stat(filepath.Join(rule.Failure, "season/Unknown.S01E01.ass")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("关联字幕应随选中视频从隔离位置恢复", err)
	}
	if _, err := os.Stat(filepath.Join(rule.Failure, "season/Other.S01E02.strm")); err != nil {
		t.Fatal("未选中的另一视频不能恢复或移动", err)
	}
	if _, err := s.Task(second.ID, selected); !errors.Is(err, ErrMediaOrganizeTaskNotFound) {
		t.Fatal("完成后仍按原来源身份清除第二轮旧失败", err)
	}
}

func TestMediaRetryLocationCloudIdentityGuards(t *testing.T) {
	t.Run("115 rejects source reappearance and accepts exact isolated identity", func(t *testing.T) {
		s, drive, rule := newMediaCleanupCloud(t)
		rule.Failure, rule.FailureID = "/failed", "40"
		files, err := s.files(context.Background(), rule)
		if err != nil {
			t.Fatal(err)
		}
		defer files.close()
		const name = "Work.S01E01.mkv"
		original := MediaOrganizeEntry{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "500", Name: name, Size: 123, SHA1: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, SourcePath: name, MediaType: "video"}}
		candidate := original
		candidate.Failure = &MediaOrganizeFailure{State: "stored", ParentID: "40"}
		drive.dirs["40"] = []p115.ShareFile{{ID: "500", Name: name, Size: 123, SHA1: original.File.SHA1}}
		if ok, err := files.verifyRetryLocation(context.Background(), original, candidate); err != nil || !ok {
			t.Fatal("应接受 115 失败目录中精确身份", ok, err)
		}
		drive.dirs["10"] = []p115.ShareFile{{ID: "500", Name: name, Size: 123, SHA1: original.File.SHA1}}
		if ok, err := files.verifyRetryLocation(context.Background(), original, candidate); err != nil || ok {
			t.Fatal("来源重新出现后不得继承旧隔离位置", ok, err)
		}
		drive.dirs["10"] = nil
		drive.dirs["40"][0].ID = "different-file-id"
		if ok, err := files.verifyRetryLocation(context.Background(), original, candidate); err != nil || ok {
			t.Fatal("不得继承文件 ID 不同的 115 文件", ok, err)
		}
	})

	t.Run("CD2 requires missing source and matching native identity", func(t *testing.T) {
		s, _, _, connection, rule := newMediaCD2Organizer(t)
		rule.Failure = "/其他网盘/failed"
		files, err := s.files(context.Background(), rule)
		if err != nil {
			t.Fatal(err)
		}
		defer files.close()
		const name = "Work.S01E01.mkv"
		source := path.Join(rule.Source, name)
		file := connection.filesystem.add(source, false, 123)
		original := mediaCD2Entry(file, rule.Source, name)
		candidate := original
		candidate.Failure = &MediaOrganizeFailure{State: "stored", CD2FileID: file.ID}
		isolated := file
		isolated.Path, isolated.Name = path.Join(rule.Failure, name), name
		connection.filesystem.files[isolated.Path] = isolated
		delete(connection.filesystem.files, source)
		if ok, err := files.verifyRetryLocation(context.Background(), original, candidate); err != nil || !ok {
			t.Fatal("应接受 CD2 失败目录中原生 ID 一致的文件", ok, err)
		}
		isolated.ID = "cross-cloud-native-id"
		connection.filesystem.files[isolated.Path] = isolated
		candidate.Failure.CD2FileID = isolated.ID
		candidate.CD2Current = &MediaCD2Location{Path: isolated.Path, FileID: isolated.ID, Modified: isolated.Modified.UnixNano()}
		if ok, err := files.verifyRetryLocation(context.Background(), original, candidate); err != nil || !ok {
			t.Fatal("应接受已确认的新 CD2 原生身份，原关联 ID 仍然保留", ok, err)
		}
		connection.filesystem.add(source, false, 123)
		if ok, err := files.verifyRetryLocation(context.Background(), original, candidate); err != nil || ok {
			t.Fatal("CD2 来源仍存在时不得继承失败目录", ok, err)
		}
		delete(connection.filesystem.files, source)
		isolated.ID = "different-native-id"
		connection.filesystem.files[isolated.Path] = isolated
		if ok, err := files.verifyRetryLocation(context.Background(), original, candidate); err != nil || ok {
			t.Fatal("不得继承原生 ID 不同的 CD2 文件", ok, err)
		}
	})
}

func TestMediaRetryLocationRejectsDifferentOrUncertainIdentity(t *testing.T) {
	for _, scenario := range []string{"same", "source-reappeared", "source-identity", "isolated-identity", "isolated-modified", "binding", "uncertain-write", "uncertain-attachment", "candidate-active", "missing-old-identity"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, rule := newMediaOrganizeTest(t)
			rule.Failure = t.TempDir()
			const name = "Work.S01E01.strm"
			mediaTestFile(t, rule.Source, name, "原始内容")
			source := filepath.Join(rule.Source, name)
			info, err := os.Lstat(source)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := mediaLocalFileIdentityKey(source, info)
			if err != nil {
				t.Fatal(err)
			}
			old := mediaRecordFixture("older", "partial", 1)
			old.Rule, old.TaskFileID = rule, name
			old.Entries[0].Status, old.Entries[0].Result = "error", nil
			old.Entries[0].File.Size, old.Entries[0].Modified, old.Entries[0].LocalIdentity = info.Size(), info.ModTime().UnixNano(), identity
			var isolated MediaOrganizeRun
			cloneMediaRun(old, &isolated)
			isolated.ID, isolated.TaskFileID = "isolated", ""
			isolated.Entries[0].Failure = &MediaOrganizeFailure{State: "stored", LocalIdentity: identity}
			destination := filepath.Join(rule.Failure, name)
			if err := os.Rename(source, destination); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "source-reappeared":
				mediaTestFile(t, rule.Source, name, "新来源")
			case "source-identity":
				old.Entries[0].LocalIdentity = "different-native-file"
			case "isolated-identity":
				isolated.Entries[0].Failure.LocalIdentity = "different-native-file"
			case "isolated-modified":
				if err := os.Chtimes(destination, info.ModTime(), info.ModTime().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			case "binding":
				isolated.Rule.Target = t.TempDir()
			case "uncertain-write":
				old.Entries[0].Failure = &MediaOrganizeFailure{State: "moving"}
			case "uncertain-attachment":
				old.Entries[0].AttachmentKeep = &MediaAttachmentKeepIntent{Phase: "moving"}
			case "candidate-active":
				isolated.Status = "running"
			case "missing-old-identity":
				old.Entries[0].LocalIdentity = ""
			}
			saveMediaFixtures(t, s, old, isolated)
			files, err := s.files(context.Background(), rule)
			if err != nil {
				t.Fatal(err)
			}
			defer files.close()
			if err := s.restoreRetryLocations(context.Background(), files, &old); err != nil {
				t.Fatal(err)
			}
			stored := old.Entries[0].Failure != nil && old.Entries[0].Failure.State == "stored"
			if stored != (scenario == "same") {
				t.Fatal("只能继承原身份一致且实时隔离目标可核验的证据", scenario, old.Entries[0])
			}
			if scenario == "uncertain-write" && old.Entries[0].Failure.State != "moving" {
				t.Fatal("不得用另一记录覆盖未确认写入意图")
			}
		})
	}
}
