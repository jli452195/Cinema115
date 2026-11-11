package service

import (
	"context"
	"path"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
)

func TestMediaOrganizeNumberingFailureResults(t *testing.T) {
	for _, tc := range []struct {
		name, code, message string
		season, episode     int
	}{
		{"Work.S01E02 S01E03.mkv", "NUMBERING_CONFLICT", "相互冲突", 1, 0},
		{"Work.S01E00.5.mkv", "NUMBERING_INVALID", "格式无效", 1, 0},
		{"Work.strm", "NUMBERING_MISSING", "缺少有效集号", 1, 0},
		{"Work.S01E01.mkv", "NUMBERING_INVALID", "超出有效范围", 1, 10001},
		{"Work.S01E02 S01E03.mkv", "", "", 0, 7},
		{"一间房 - S02E00 - 第 0 集.strm", "", "", 2, 0},
		{"天降之物 - S02E10 - S02E10.mkv", "", "", 2, 10},
		{"绯弹的亚里亚 - S00E01 - OVA#13「武侦来到温泉研修」.mkv", "", "", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, rule := newMediaOrganizeTest(t)
			files, err := s.files(context.Background(), rule)
			if err != nil {
				t.Fatal(err)
			}
			defer files.close()
			run := MediaOrganizeRun{ID: "numbering-result", Status: "running", Rule: rule,
				Groups:  []MediaOrganizeGroup{{ID: "g", Kind: "tv", TMDBID: 42}},
				Entries: []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "v", Name: tc.name}, MediaType: "video", Season: tc.season, Episode: tc.episode}, Group: "g"}},
			}
			if err := s.render(context.Background(), files, &run); err != nil {
				t.Fatal(err)
			}
			entry := run.Entries[0]
			if entry.ResultCode != tc.code || !strings.Contains(entry.Message, tc.message) {
				t.Fatal("编号错误应有具体结果码和原因", entry)
			}
			if tc.code == "" {
				if entry.Status != "ready" || entry.Target == "" || entry.File.Season != tc.season || entry.File.Episode != tc.episode {
					t.Fatal("有效坐标和人工修正不能被名称冲突覆盖", entry)
				}
				return
			}
			if entry.Status != "invalid" || entry.Target != "" {
				t.Fatal("无效编号不能形成写入计划", entry)
			}
			run.Status = "partial"
			tasks := mediaTasks(run)
			if len(tasks) != 1 || tasks[0].Status != "failed" || tasks[0].Message != entry.Message {
				t.Fatal("历史和详情应展示主文件的具体原因", tasks)
			}
			mediaCheckpointResults(&run, MediaOrganizeRun{Status: "running"})
			if len(run.recognitionEvents) != 1 || run.recognitionEvents[0].details.ResultCode != tc.code {
				t.Fatal("最终失败日志应复用同一结果码", run.recognitionEvents)
			}
		})
	}
}

func TestMediaOrganizeRetryDoesNotInferPendingWriteCoordinates(t *testing.T) {
	s, _, _, run := newMedia115ExecutionTest(t)
	run.Status = "partial"
	run.Groups = []MediaOrganizeGroup{{ID: "g", Kind: "tv", TMDBID: 42}}
	run.Entries = []MediaOrganizeEntry{{
		File:  OrganizeFile{ShareFile: p115.ShareFile{ID: "501", Name: "Work.S00E01 - OVA#13.mkv"}, MediaType: "video", Season: 1},
		Group: "g", Status: "error", Relative: "frozen.mkv", Target: "/library/frozen.mkv", P115Move: &Media115MoveIntent{},
	}}
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	delete(s.actions, run.ID)
	before, err := s.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: "501"}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Snapshot(run.ID)
	if err != nil || !reflect.DeepEqual(before.Entries[0].File, after.Entries[0].File) || before.Entries[0].Target != after.Entries[0].Target || !reflect.DeepEqual(before.Entries[0].P115Move, after.Entries[0].P115Move) {
		t.Fatal("未核实写入的直接重试必须保留冻结坐标和目标", before, after, err)
	}
}

func TestMediaOrganizeNumberingRetryRepairsLegacyFailure(t *testing.T) {
	for _, tc := range []struct {
		name            string
		season, episode int
	}{
		{"天降之物 - S02E10 - S02E10.mkv", 2, 10},
		{"一间房 - S02E00 - 第 0 集.strm", 2, 0},
		{"绯弹的亚里亚 - S00E01 - OVA#13「武侦来到温泉研修」.mkv", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, metadata, _, rule := newMediaOrganizeTest(t)
			title := strings.SplitN(tc.name, " - ", 2)[0]
			metadata.items = []tmdb.Item{{ID: 42, Kind: "tv", Title: title, OriginalTitle: title, Date: "2024-01-01"}}
			mediaTestFile(t, rule.Source, tc.name, "原始文件")
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &run, "scan")
			video := &run.Entries[0]
			video.File.Episode = 0
			video.File.EpisodeExplicit = false
			video.Status, video.Message = "invalid", "旧版季集识别失败"
			run.Status = "partial"
			if err := s.checkpoint(&run); err != nil {
				t.Fatal(err)
			}
			before, err := s.Snapshot(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.History(context.Background(), "", "failed", 1); err != nil {
				t.Fatal(err)
			}
			after, err := s.Snapshot(run.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("查看历史不能重新识别或改写旧失败", before, after, err)
			}
			if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: video.File.ID}); err != nil {
				t.Fatal(err)
			}
			retry, err := s.Snapshot(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &retry, "retry")
			entry := retry.Entries[0]
			if entry.Status != "done" || entry.File.Season != tc.season || entry.File.Episode != tc.episode {
				t.Fatal("明确重试应按当前解析补齐旧失败集号，无需手填", entry)
			}
		})
	}
}

func TestMediaOrganizeUnmatchedSpecialKeepsNumberingFailure(t *testing.T) {
	for _, tc := range []struct {
		name, code string
	}{
		{"Work.S01E02 S01E03.OVA.mkv", "NUMBERING_CONFLICT"},
		{"Work.S01E00.5.OVA.mkv", "NUMBERING_INVALID"},
		{"Work.OVA.mkv", "NUMBERING_MISSING"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, rule := newMediaOrganizeTest(t)
			s.metadata = nil
			mediaTestFile(t, rule.Source, path.Join("Work (2024)", "Season 1", tc.name), "原始文件")
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &run, "scan")
			entry := run.Entries[0]
			if entry.ResultCode != tc.code || entry.Target != "" || entry.Message == "请选择对应作品" {
				t.Fatal("特殊内容在匹配前也应保留具体编号原因", run)
			}
			run.Status = "partial"
			tasks := mediaTasks(run)
			if len(tasks) != 1 || tasks[0].Status != "failed" || tasks[0].Message != entry.Message {
				t.Fatal("详情不能用通用匹配提示覆盖编号原因", tasks)
			}
			mediaCheckpointResults(&run, MediaOrganizeRun{Status: "running"})
			if len(run.recognitionEvents) != 1 || run.recognitionEvents[0].details.ResultCode != tc.code {
				t.Fatal("匹配前的最终失败日志应使用具体编号结果码", run.recognitionEvents)
			}
		})
	}
}
