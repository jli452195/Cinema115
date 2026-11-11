package service

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
)

func TestMediaOrganizeDeclaredSpecialNumbering(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		season, episode, end int
	}{
		{"天降之物 (2009)/Season 2/天降之物 - S02E10 - S02E10.mkv", 2, 10, 0},
		{"绯弹的亚里亚 (2011)/Specials/绯弹的亚里亚 - S00E01 - OVA#13「武侦来到温泉研修」.mkv", 0, 1, 0},
		{"出包王女 (2008)/Specials/出包王女 - S00E01 - OVA#1「梨斗，变成女生」.mkv", 0, 1, 0},
		{"七人魔法使 (2014)/Specials/七人魔法使 - S00E01 - OVA 七大罪与七位魔法使.mkv", 0, 1, 0},
		{"传颂之物 (2006)/Specials/传颂之物 - S00E13 - OVA Utawarerumono Picture Drama 2.mkv", 0, 13, 0},
		{"Specials/精灵使的剑舞 - S00E06 - 特典小型OVA Vol.6-精灵使的休息.mkv", 0, 6, 0},
		{"Specials/出包王女 - S00E15 - Darkness OVA#9「First accident」.mkv", 0, 15, 0},
		{"Specials/Show.S00E01.mkv", 0, 1, 0},
		{"Specials/Show.S00E01.OVA#13.mkv", 0, 1, 0},
		{"Specials/Show.S00E01 OVA#13.mkv", 0, 1, 0},
		{"Specials/Show.S00E01 - OVA#13 [1080p] [HEVC].mkv", 0, 1, 0},
		{"Specials/Show.S00E01-E02 - OVA#13.mkv", 0, 1, 2},
		{"Specials/Show OVA01.mkv", 1, 0, 0},
		{"Specials/Show.S01E01 - OVA#13.mkv", 1, 1, 0},
		{"Specials/Show.S00E01.5 - OVA#13.mkv", 1, 0, 0},
		{"Specials/Show.S00E01A - OVA#13.mkv", 1, 0, 0},
		{"Specials/Show.S00E01foo - OVA#13.mkv", 1, 0, 0},
		{"Specials/Show.S00E00 - OVA#13.mkv", 0, 0, 0},
		{"Specials/Show.S00E10001 - OVA#13.mkv", 1, 0, 0},
		{"Specials/Show.S00E02-01 - OVA#13.mkv", 1, 0, 0},
		{"Specials/Show.S00E01-E02+04 - OVA#13.mkv", 1, 0, 0},
		{"Specials/Show.S00E01 S00E02 - OVA#13.mkv", 1, 0, 0},
		{"Specials/Show.S00E01 - OVA S01E02.mkv", 1, 0, 0},
		{"Specials/Show.S00E01 [02] - OVA#13.mkv", 1, 0, 0},
		{"Specials/Show.S00E01 - OVA E02.mkv", 1, 0, 0},
		{"Specials/Show.S00E01 - OVA [02].mkv", 1, 0, 0},
		{"Specials/Show.S00E01 - OVA [02.5].mkv", 1, 0, 0},
		{"Specials/Show.S00E01 - OVA [02A].mkv", 1, 0, 0},
		{"Specials/Show.S00E01 - OVA [02-03].mkv", 1, 0, 0},
		{"Specials/Show.S00E01 - OVA 第02集.mkv", 0, 1, 0},
		{"Specials/Show.S00E01 - OVA - 02.mkv", 1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := OrganizeFile{ShareFile: p115.ShareFile{Name: path.Base(tc.name)}, SourcePath: tc.name, MediaType: "video", Season: 1}
			strict := file
			organizeFileNumbering(&strict)
			if strict.Episode != 0 {
				t.Fatal("磁力整理的特殊内容确认规则发生变化", strict)
			}
			mediaOrganizeFileNumbering(&file)
			if file.filenameFacts == nil {
				t.Fatal("自动整理编号应保留本次提取的文件事实供作品识别复用")
			}
			if file.Season != tc.season || file.Episode != tc.episode || file.EndEpisode != tc.end {
				t.Fatalf("got S%dE%d-%d; want S%dE%d-%d", file.Season, file.Episode, file.EndEpisode, tc.season, tc.episode, tc.end)
			}
		})
	}
}

func TestMediaOrganizerDeclaredSpecialsAndCompanions(t *testing.T) {
	for _, storage := range []string{"local", "115", "cd2"} {
		for _, tc := range []struct {
			stem, title     string
			season, episode int
		}{
			{"天降之物 - S02E10 - S02E10", "天降之物", 2, 10},
			{"一间房 - S02E00 - 第 0 集", "一间房", 2, 0},
			{"Show - S00E00 - OVA#13", "Show", 0, 0},
			{"绯弹的亚里亚 - S00E01 - OVA#13「武侦来到温泉研修」", "绯弹的亚里亚", 0, 1},
			{"出包王女 - S00E01 - OVA#1「梨斗，变成女生」", "出包王女", 0, 1},
			{"七人魔法使 - S00E01 - OVA 七大罪与七位魔法使", "七人魔法使", 0, 1},
			{"传颂之物 - S00E13 - OVA Utawarerumono Picture Drama 2", "传颂之物", 0, 13},
			{"恶魔高校D×D - S00E16 - OAD 第13话 无法苏醒的不死鸟", "恶魔高校D×D", 0, 16},
			{"绯弹的亚里亚AA - S01E02 - 2弹 危险的关系", "绯弹的亚里亚AA", 1, 2},
			{"绯弹的亚里亚AA - S01E02 - 3弹 危险的关系", "绯弹的亚里亚AA", 1, 2},
			{"魔法老师！？ - S01E09 - 第 9 话 “听说要去露营。”", "魔法老师！？", 1, 9},
		} {
			t.Run(storage+"/"+tc.title, func(t *testing.T) {
				metadata := &mediaRecognitionFake{items: []tmdb.Item{{ID: 42, Kind: "tv", Title: tc.title, Date: "2024-01-01"}}}
				extensions := []string{".mkv", ".zh.ass", ".nfo", "-thumb.jpg"}
				var run MediaOrganizeRun
				var checkLocation func(MediaOrganizeEntry)
				switch storage {
				case "local":
					s, _, _, rule := newMediaOrganizeTest(t)
					s.metadata = metadata
					for _, extension := range extensions {
						mediaTestFile(t, rule.Source, path.Join("Specials", tc.stem+extension), "原文件内容")
					}
					var err error
					run, err = s.Start(rule.ID)
					if err != nil {
						t.Fatal(err)
					}
					mediaTestProcess(t, s, &run, "auto")
					checkLocation = func(entry MediaOrganizeEntry) {
						t.Helper()
						body, err := os.ReadFile(filepath.FromSlash(entry.Target))
						if err != nil || string(body) != "原文件内容" {
							t.Fatal("本地整理未保留原文件", entry, err)
						}
						if _, err := os.Stat(filepath.Join(rule.Source, filepath.FromSlash(entry.File.ID))); !os.IsNotExist(err) {
							t.Fatal("来源文件未移走", entry, err)
						}
					}
				case "115":
					s, drive, files, snapshot, _ := newMedia115PipelineTest(t)
					defer files.close()
					s.metadata = metadata
					drive.dirs["10"] = []p115.ShareFile{{ID: "50", Name: "Specials", Directory: true}}
					drive.paths["50"] = path.Join(snapshot.Rule.Source, "Specials")
					for i, extension := range extensions {
						drive.dirs["50"] = append(drive.dirs["50"], p115.ShareFile{ID: fmt.Sprint(501 + i), Name: tc.stem + extension, Size: 123})
					}
					if err := s.process115Auto(context.Background(), files, snapshot); err != nil {
						t.Fatal(err)
					}
					run = *snapshot
					checkLocation = func(entry MediaOrganizeEntry) {
						t.Helper()
						cid, err := files.cloudDestination(context.Background(), entry.Relative, false)
						if err != nil || media115PipelineHas(drive, "50", entry.File.ID) || !media115PipelineHas(drive, cid, entry.File.ID) {
							t.Fatal("115 文件位置错误", entry, err)
						}
					}
				case "cd2":
					s, _, _, connection, rule := newMediaCD2Organizer(t)
					s.metadata = metadata
					fs := connection.filesystem
					fs.add(path.Join(rule.Source, "Specials"), true, 0)
					for _, extension := range extensions {
						fs.add(path.Join(rule.Source, "Specials", tc.stem+extension), false, 123)
					}
					var err error
					run, err = s.Start(rule.ID)
					if err != nil {
						t.Fatal(err)
					}
					mediaTestProcess(t, s, &run, "auto")
					checkLocation = func(entry MediaOrganizeEntry) {
						t.Helper()
						_, sourceExists := fs.files[entry.File.ID]
						target, targetExists := fs.files[entry.Target]
						if sourceExists || !targetExists || target.Size != entry.File.Size {
							t.Fatal("CD2 文件位置错误", entry)
						}
					}
				}
				mediaEntries := make([]MediaOrganizeEntry, 0, len(extensions))
				for _, entry := range run.Entries {
					if entry.File.MediaType != "cleanup" {
						mediaEntries = append(mediaEntries, entry)
					}
				}
				if run.Status != "completed" || len(mediaEntries) != len(extensions) {
					t.Fatal("明确特殊集应无需人工确认即可自动整理", run.Status, run.Entries)
				}
				videoID := ""
				for _, entry := range mediaEntries {
					if entry.File.MediaType == "video" {
						videoID = entry.File.ID
						if entry.File.Season != tc.season || entry.File.Episode != tc.episode || entry.File.EndEpisode != 0 {
							t.Fatal("主文件丢失声明的特殊集坐标", entry)
						}
					}
				}
				for _, entry := range mediaEntries {
					if videoID == "" || entry.Status != "done" || !strings.Contains(path.Base(entry.Target), fmt.Sprintf(".S%02dE%02d", tc.season, tc.episode)) || path.Base(path.Dir(entry.Target)) != fmt.Sprintf("Season %02d", tc.season) {
						t.Fatal("视频与附件未使用一致的特殊集命名", entry)
					}
					if entry.File.MediaType != "video" && entry.VideoID != videoID {
						t.Fatal("附件未跟随唯一主文件", entry)
					}
					checkLocation(entry)
				}
			})
		}
	}
}

func TestMediaOrganizerDeclaredSpecialRetry(t *testing.T) {
	for _, tc := range []struct {
		name       string
		unmatched  bool
		correction bool
	}{
		{name: "已匹配作品缺少集号"},
		{name: "尚未匹配作品且保留默认季号", unmatched: true},
		{name: "保留用户明确修正", unmatched: true, correction: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, metadata, _, rule := newMediaOrganizeTest(t)
			if tc.unmatched {
				metadata.items = nil
			}
			const stem = "Work.S00E01 - OVA#13"
			fileID := path.Join("Specials", stem+".mkv")
			for _, extension := range []string{".mkv", ".ass", ".nfo"} {
				mediaTestFile(t, rule.Source, path.Join("Specials", stem+extension), "原文件")
			}
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &run, "scan")
			for i := range run.Entries {
				entry := &run.Entries[i]
				if entry.File.MediaType == "video" {
					// 复原旧记录的零集数；未匹配作品的旧记录还保留默认第一季。
					entry.File.Episode = 0
					entry.File.EpisodeExplicit = false
					entry.Status = "invalid"
					if tc.unmatched {
						entry.File.Season = 1
						entry.Status = "needs_match"
					}
				} else if tc.unmatched && (entry.VideoID != fileID || !strings.Contains(entry.Message, "对应视频尚未就绪")) {
					t.Fatal("已关联的附件应说明在等待主文件", entry)
				}
			}
			if err := s.checkpoint(&run); err != nil {
				t.Fatal(err)
			}
			metadata.items = []tmdb.Item{{ID: 42, Kind: "tv", Title: "作品", OriginalTitle: "Work", Date: "2024-01-01"}}
			correction := MediaOrganizeCorrection{FileID: fileID}
			season, episode := 0, 1
			if tc.correction {
				season, episode = 2, 9
				correction.Season, correction.Episode = &season, &episode
			}
			if _, err := s.Retry(run.ID, correction); err != nil {
				t.Fatal(err)
			}
			retry, err := s.Snapshot(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &retry, "retry")
			for _, entry := range retry.Entries {
				if entry.Status != "done" || !strings.Contains(entry.Target, fmt.Sprintf(".S%02dE%02d.", season, episode)) {
					t.Fatal("当前任务重试应恢复季集并带上原有附件，用户修正优先", entry)
				}
			}
		})
	}
}
