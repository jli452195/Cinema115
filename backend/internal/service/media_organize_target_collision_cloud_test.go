package service

import (
	"context"
	"errors"
	"path"
	"reflect"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestMediaCD2SameTargetWithoutDuplicateDeletion(t *testing.T) {
	for _, scenario := range []string{"normal", "source_changed", "rename_failed", "move_failed", "target_exists"} {
		t.Run(scenario, func(t *testing.T) {
			s, fs, store, rule := newMediaCD2BatchOrganizer(t)
			entries := []MediaOrganizeEntry{
				mediaCD2BatchEntry(fs, rule, "a/Work.S01E01.mkv", ""),
				mediaCD2BatchEntry(fs, rule, "b/Work.S01E01(1).mkv", ""),
			}
			for i := range entries {
				entries[i].File.Season, entries[i].File.Episode = 1, 1
				entries[i].File.EpisodeExplicit = true
				// 即使 SHA1 相同，未开启删除重复文件也必须保留冲突来源。
				file := fs.files[entries[i].File.ID]
				file.Hashes[2], entries[i].CD2Hashes[2] = "same-sha1", "same-sha1"
				fs.files[file.Path] = file
			}
			run := mediaCD2BatchRun(t, s, rule, entries...)
			files, err := s.files(context.Background(), rule)
			if err != nil {
				t.Fatal(err)
			}
			defer files.close()
			if err := s.render(context.Background(), files, &run); err != nil {
				t.Fatal(err)
			}
			if run.Entries[0].Status != "ready" || run.Entries[1].Status != "ready" || run.Entries[0].Target != run.Entries[1].Target {
				t.Fatal("同目标输入应进入执行阶段，不应在命名预检中全部拦截", run.Entries)
			}
			target := run.Entries[0].Target
			var existingID string
			switch scenario {
			case "source_changed":
				file := fs.files[entries[0].File.ID]
				file.ID = "replacement"
				fs.files[file.Path] = file
			case "rename_failed":
				fs.renameFailures[entries[0].File.ID] = errors.New("模拟第一份改名失败")
			case "move_failed":
				renamed := path.Join(path.Dir(entries[0].File.ID), path.Base(target))
				fs.moveFailures[renamed] = errors.New("模拟第一份移动失败")
			case "target_exists":
				for directory := path.Dir(target); directory != rule.Target; directory = path.Dir(directory) {
					fs.add(directory, true, 0)
				}
				existingID = fs.add(target, false, 512).ID
			}
			if err := s.process(context.Background(), &run, "execute"); err != nil {
				t.Fatal(err)
			}
			saved := mediaCD2BatchPersistedRun(t, store, run.ID)
			want := []string{"done", "conflict"}
			winner := 0
			if scenario == "source_changed" || scenario == "rename_failed" || scenario == "move_failed" {
				want, winner = []string{"error", "done"}, 1
			} else if scenario == "target_exists" {
				want, winner = []string{"conflict", "conflict"}, -1
			}
			for i, entry := range saved.Entries {
				if entry.Status != want[i] {
					t.Fatalf("第 %d 份结果错误：want %s, got %+v", i, want[i], entry)
				}
				if entry.Status == "conflict" && entry.ResultCode != "TARGET_EXISTS" {
					t.Fatal("目标占用应返回明确的结果码", entry)
				}
				if i != winner {
					source := entries[i].File.ID
					if scenario == "move_failed" && i == 0 {
						source = path.Join(path.Dir(source), path.Base(target))
					}
					if _, exists := fs.files[source]; !exists {
						t.Fatal("未成功的来源必须保留", entry)
					}
				}
			}
			if len(fs.deleted) != 0 {
				t.Fatal("未开启删除重复文件时不得删除任何来源", fs.deleted)
			}
			if winner >= 0 && fs.files[target].ID != entries[winner].CD2FileID || winner < 0 && fs.files[target].ID != existingID {
				t.Fatal("目标必须属于首个成功文件或原有文件", fs.files[target])
			}
		})
	}
}

func TestMedia115SameTargetWithoutDuplicateDeletion(t *testing.T) {
	for _, scenario := range []string{"normal", "source_changed", "rename_failed", "target_exists"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, files, run := newMedia115BatchTest(t, 2)
			defer files.close()
			run.Entries[1].Relative, run.Entries[1].Target = run.Entries[0].Relative, run.Entries[0].Target
			run.Entries[1].File.SHA1 = run.Entries[0].File.SHA1
			drive.dirs["10"][1].SHA1 = run.Entries[0].File.SHA1
			original := append([]p115.ShareFile(nil), drive.dirs["10"]...)
			switch scenario {
			case "source_changed":
				drive.dirs["10"][0].SHA1 = "replacement"
			case "rename_failed":
				drive.renameErr = errors.New("模拟第一份改名失败")
				drive.afterRename = func() {
					drive.renameErr = nil
					drive.afterRename = nil
				}
			case "target_exists":
				drive.dirs["20"] = []p115.ShareFile{{ID: "existing", Name: path.Base(run.Entries[0].Relative), Size: 512, SHA1: "existing"}}
			}
			existing := append([]p115.ShareFile(nil), drive.dirs["20"]...)
			runMedia115BatchTest(t, s, drive, files, run)
			want, winner := []string{"done", "conflict"}, 0
			if scenario == "source_changed" || scenario == "rename_failed" {
				want, winner = []string{"error", "done"}, 1
			} else if scenario == "target_exists" {
				want, winner = []string{"conflict", "conflict"}, -1
			}
			for i, entry := range run.Entries {
				if entry.Status != want[i] {
					t.Fatalf("第 %d 份结果错误：want %s, got %+v", i, want[i], entry)
				}
				if entry.Status == "conflict" && entry.ResultCode != "TARGET_EXISTS" {
					t.Fatal("目标占用应返回明确的结果码", entry)
				}
				if i != winner {
					found := false
					for _, file := range drive.dirs["10"] {
						found = found || file.ID == original[i].ID
					}
					if !found {
						t.Fatal("未成功来源必须保留", original[i], drive.dirs["10"])
					}
				}
			}
			if len(drive.deleted) != 0 {
				t.Fatal("未开启删除重复文件时不得删除任何来源", drive.deleted)
			}
			if winner < 0 {
				if !reflect.DeepEqual(existing, drive.dirs["20"]) {
					t.Fatal("已有目标被修改", drive.dirs["20"])
				}
			} else if len(drive.dirs["20"]) != 1 || drive.dirs["20"][0].ID != original[winner].ID {
				t.Fatal("同名文件不能同时写入或覆盖目标", drive.dirs["20"])
			}
		})
	}
}
