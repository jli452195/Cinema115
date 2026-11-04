package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

func TestMediaScanLimit115ReadsVideoAfterThreeHundredDirectories(t *testing.T) {
	s, drive, files, run, _ := newMedia115PipelineTest(t)
	s.Logs = &GenerationLogs{}
	last := ""
	for i := range 301 {
		id, name := fmt.Sprint(500+i), fmt.Sprintf("dir-%03d", i)
		drive.dirs["10"] = append(drive.dirs["10"], p115.ShareFile{ID: id, Name: name, Directory: true})
		drive.paths[id], drive.dirs[id] = path.Join(run.Rule.Source, name), []p115.ShareFile{}
		last = id
	}
	drive.dirs[last] = []p115.ShareFile{{ID: "9001", Name: "Work.S01E01.mp4", Size: 123}}
	if err := s.process115Auto(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "completed" || len(run.Entries) != 302 || run.Entries[0].File.ID != "9001" || run.Entries[0].Status != "done" || run.Entries[0].File.Season != 1 || run.Entries[0].File.Episode != 1 || len(run.Groups) != 1 || run.Groups[0].TMDBID != 42 || media115PipelineHas(drive, last, "9001") {
		t.Fatal("超过旧目录上限后仍必须识别并移动末尾视频", run, drive.moves)
	}
	if len(s.Logs.Read("ALL", "整理", 0, false).Entries) != 0 {
		t.Fatal("成功扫描不应输出日志")
	}
}

func TestMediaScanLimitFallbackEntriesAfterThreeHundredDirectories(t *testing.T) {
	for _, kind := range []string{"115", "cd2", "local"} {
		t.Run(kind, func(t *testing.T) {
			var files *mediaOrganizeFiles
			var err error
			switch kind {
			case "115":
				s, drive, rule := newMediaCleanupCloud(t)
				last := ""
				for i := range 301 {
					id, name := fmt.Sprint(500+i), fmt.Sprintf("dir-%03d", i)
					drive.dirs["10"] = append(drive.dirs["10"], p115.ShareFile{ID: id, Name: name, Directory: true})
					drive.dirs[id] = []p115.ShareFile{}
					last = id
				}
				drive.dirs[last] = []p115.ShareFile{{ID: "9001", Name: "Work.S01E01.mp4", Size: 123}}
				files, err = s.files(context.Background(), rule)
			case "cd2":
				s, _, _, connection, rule := newMediaCD2Organizer(t)
				for i := range 301 {
					connection.filesystem.add(path.Join(rule.Source, fmt.Sprintf("dir-%03d", i)), true, 0)
				}
				connection.filesystem.add(path.Join(rule.Source, "dir-300/Work.S01E01.mp4"), false, 123)
				files, err = s.files(context.Background(), rule)
			case "local":
				s, _, _, rule := newMediaOrganizeTest(t)
				for i := range 301 {
					if err := os.Mkdir(filepath.Join(rule.Source, fmt.Sprintf("dir-%03d", i)), 0755); err != nil {
						t.Fatal(err)
					}
				}
				mediaTestFile(t, rule.Source, "dir-300/Work.S01E01.mp4", "原视频")
				files, err = s.files(context.Background(), rule)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer files.close()
			entries, err := files.scan(context.Background())
			if err != nil || len(entries) != 1 || entries[0].File.Name != "Work.S01E01.mp4" || entries[0].File.Season != 1 || entries[0].File.Episode != 1 || len(files.directories) != 301 {
				t.Fatal("后备扫描必须使用同样的目录边界", err, entries, len(files.directories))
			}
		})
	}
}

func TestMediaScanLimitCD2StreamsVideoAfterThreeHundredDirectories(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	for i := range 301 {
		connection.filesystem.add(path.Join(rule.Source, fmt.Sprintf("dir-%03d", i)), true, 0)
	}
	source := connection.filesystem.add(path.Join(rule.Source, "dir-300/Work.S01E01.mp4"), false, 123)
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	if run.Status != "completed" || len(mediaTasks(run)) != 1 || mediaTasks(run)[0].Status != "done" || run.Groups[0].TMDBID != 42 {
		t.Fatal("CD2 流式扫描未整理末尾视频", run)
	}
	if _, exists := connection.filesystem.files[source.Path]; exists {
		t.Fatal("CD2 末尾视频仍在来源目录", run)
	}
}

func TestMediaScanLimitBoundsHaveDistinctErrors(t *testing.T) {
	visited := make(map[string]bool, mediaOrganizeScanDirectoryLimit)
	for i := range mediaOrganizeScanDirectoryLimit {
		if err := mediaVisitScanDirectory(visited, fmt.Sprint(i), fmt.Sprintf("/source/dir-%d", i), mediaOrganizeScanDepthLimit); err != nil {
			t.Fatal("边界以内不应拒绝目录", i, err)
		}
	}
	for _, item := range []struct {
		id, location, want string
		depth              int
	}{
		{"0", "/source/repeated", "重复目录或目录循环", 1},
		{"next", "/source/deep", "层级超过 12 层", 13},
		{"next", "/source/overflow", "最多扫描 5000 个目录", 1},
	} {
		if err := mediaVisitScanDirectory(visited, item.id, item.location, item.depth); err == nil || !strings.Contains(err.Error(), item.want) || !strings.Contains(err.Error(), item.location) {
			t.Fatal("不同失败原因应带准确路径", item, err)
		}
	}
}

type mediaScanFailureCommitStore struct {
	mediaOrganizeStorage
	fail bool
}

func (s *mediaScanFailureCommitStore) SaveMediaOrganize(ctx context.Context, settings string, updates []storage.MediaOrganizeRunRow, removed []string) error {
	for _, row := range updates {
		var record struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(row.Record), &record); err != nil {
			return err
		}
		if s.fail && record.Status == "failed" {
			return errors.New("模拟扫描最终结果保存失败")
		}
	}
	return s.mediaOrganizeStorage.SaveMediaOrganize(ctx, settings, updates, removed)
}

func TestMediaScanLimitNoVideoFailureLogsOnlyAfterCommit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			s, drive, _, _, _ := newMedia115PipelineTest(t)
			s.Logs = &GenerationLogs{}
			drive.dirs["10"] = []p115.ShareFile{{ID: "10", Name: "cycle", Directory: true}}
			repository := &mediaScanFailureCommitStore{mediaOrganizeStorage: s.db, fail: fail}
			s.db = repository
			run, action, ok := s.takeQueuedRunLocked()
			if !ok {
				t.Fatal("缺少排队扫描")
			}
			err := s.processQueuedRun(context.Background(), run, action)
			logs := s.Logs.Read("ALL", "整理", 0, false).Entries
			if fail {
				if err == nil || len(logs) != 0 {
					t.Fatal("提交失败不能发布扫描失败日志", err, logs)
				}
				return
			}
			stored, snapshotErr := s.Snapshot(run.ID)
			if err != nil || snapshotErr != nil || stored.Status != "failed" || len(stored.Entries) != 0 || len(logs) != 1 || logs[0].Level != "ERROR" || logs[0].Recognition == nil || logs[0].Recognition.ResultCode != "BATCH_FAILED" || logs[0].Recognition.FileID != "" || !strings.Contains(logs[0].Message, "重复目录或目录循环") {
				t.Fatal("无视频扫描失败应只输出一次必要终态错误", err, snapshotErr, stored, logs)
			}
			if err := s.checkpoint(&stored); err != nil || len(s.Logs.Read("ALL", "整理", 0, false).Entries) != 1 {
				t.Fatal("重复保存不应重复记录同次失败", err)
			}
			if _, err := s.Start(run.Rule.ID); err != nil || len(s.Logs.Read("ALL", "整理", 0, false).Entries) != 1 {
				t.Fatal("下一轮扫描不能抹去已经发布的失败日志", err)
			}
		})
	}
}
