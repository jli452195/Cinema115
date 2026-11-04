package service

import (
	"context"
	"fmt"
	"path"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
)

func TestMediaOrganizerCloudNumberingAndSubtitles(t *testing.T) {
	for _, storage := range []string{"115", "cd2"} {
		for _, tc := range []struct {
			stem, title, date string
			season, episode   int
			resolution        string
		}{
			{"花朵盛开 (2026) - S01E26 - 1080p.Hami Video.WEB-DL.H264.AAC 2.0", "花朵盛开", "2026-01-01", 1, 26, "1080p"},
			{"极恶老大 (2020) - S03E07 - 1080p.Amazon.WEB-DL.H264.DDP 5.1", "极恶老大", "2020-01-01", 3, 7, "1080p"},
			{"未确认进行式.2014.S01E08.420P", "未确认进行式", "2014-01-01", 1, 8, "420p"},
		} {
			t.Run(storage+"/"+tc.title, func(t *testing.T) {
				metadata := &mediaRecognitionFake{items: []tmdb.Item{{ID: 42, Kind: "tv", Title: tc.title, Date: tc.date}}}
				var run MediaOrganizeRun
				var checkLocation func(MediaOrganizeEntry)
				if storage == "115" {
					s, drive, files, snapshot, _ := newMedia115PipelineTest(t)
					defer files.close()
					s.metadata = metadata
					drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: tc.stem + ".mkv", Size: 123}, {ID: "502", Name: tc.stem + ".zh.ass", Size: 8}}
					if err := s.process115Auto(context.Background(), files, snapshot); err != nil {
						t.Fatal(err)
					}
					run = *snapshot
					checkLocation = func(entry MediaOrganizeEntry) {
						t.Helper()
						cid, err := files.cloudDestination(context.Background(), entry.Relative, false)
						if err != nil || media115PipelineHas(drive, "10", entry.File.ID) || !media115PipelineHas(drive, cid, entry.File.ID) {
							t.Fatal("115 文件或字幕位置错误", entry, err)
						}
					}
				} else {
					s, _, _, connection, rule := newMediaCD2Organizer(t)
					s.metadata = metadata
					fs := connection.filesystem
					fs.add(path.Join(rule.Source, tc.stem+".mkv"), false, 123)
					fs.add(path.Join(rule.Source, tc.stem+".zh.ass"), false, 8)
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
							t.Fatal("CD2 文件或字幕位置错误", entry)
						}
					}
				}
				if run.Status != "completed" || len(run.Entries) != 2 {
					t.Fatal("规格标签不能阻止主文件与配套字幕完成整理", run.Status, run.Entries)
				}
				videoID := ""
				for _, entry := range run.Entries {
					if entry.File.MediaType == "video" {
						videoID = entry.File.ID
						if entry.File.Season != tc.season || entry.File.Episode != tc.episode || entry.File.EndEpisode != 0 {
							t.Fatal("主文件丢失明确季集", entry)
						}
					}
				}
				for _, entry := range run.Entries {
					if videoID == "" || entry.Status != "done" || !strings.Contains(path.Base(entry.Target), fmt.Sprintf(".S%02dE%02d.", tc.season, tc.episode)) || path.Base(path.Dir(entry.Target)) != fmt.Sprintf("Season %02d", tc.season) {
						t.Fatal("视频及字幕没有使用一致的目标季集", entry)
					}
					if !strings.Contains(path.Base(entry.Target), "."+tc.resolution+".") {
						t.Fatal("视频及字幕没有保留明确声明的分辨率", entry)
					}
					if entry.File.MediaType == "subtitle" && entry.VideoID != videoID {
						t.Fatal("字幕没有跟随对应主文件", entry)
					}
					checkLocation(entry)
				}
			})
		}
	}
}
