package service

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type mediaRecursiveSubtitleFixture struct {
	add          func(string)
	run          func() MediaOrganizeRun
	sourceExists func(string) bool
	targetExists func(MediaOrganizeEntry) bool
}

func newMediaRecursiveSubtitleFixture(t *testing.T, storage string) mediaRecursiveSubtitleFixture {
	t.Helper()
	var fixture mediaRecursiveSubtitleFixture
	var organizer *MediaOrganizer
	var rule MediaOrganizeRule
	var first *MediaOrganizeRun
	switch storage {
	case "local":
		organizer, _, _, rule = newMediaOrganizeTest(t)
		fixture.add = func(relative string) { mediaTestFile(t, rule.Source, relative, relative) }
		fixture.sourceExists = func(relative string) bool {
			_, err := os.Stat(filepath.Join(rule.Source, filepath.FromSlash(relative)))
			return err == nil
		}
		fixture.targetExists = func(entry MediaOrganizeEntry) bool {
			_, err := os.Stat(entry.Target)
			return err == nil
		}
	case "cd2":
		s, _, _, connection, configured := newMediaCD2Organizer(t)
		organizer, rule = s, configured
		fs := connection.filesystem
		fixture.add = func(relative string) {
			parent := rule.Source
			for _, name := range strings.Split(path.Dir(relative), "/") {
				if name == "." {
					continue
				}
				parent = path.Join(parent, name)
				if _, exists := fs.files[parent]; !exists {
					fs.add(parent, true, 0)
				}
			}
			fs.add(path.Join(rule.Source, relative), false, 123)
		}
		fixture.sourceExists = func(relative string) bool {
			_, exists := fs.files[path.Join(rule.Source, relative)]
			return exists
		}
		fixture.targetExists = func(entry MediaOrganizeEntry) bool {
			actual, exists := fs.files[entry.Target]
			return exists && actual.Size == entry.File.Size
		}
	case "115":
		s, drive, files, run, _ := newMedia115PipelineTest(t)
		organizer, rule, first = s, run.Rule, run
		t.Cleanup(files.close)
		directories := map[string]string{".": rule.SourceID}
		ids := make(map[string]string)
		serial := 3000
		fixture.add = func(relative string) {
			parent, prefix := rule.SourceID, "."
			for _, name := range strings.Split(path.Dir(relative), "/") {
				if name == "." {
					continue
				}
				prefix = path.Join(prefix, name)
				id, exists := directories[prefix]
				if !exists {
					serial++
					id = strconv.Itoa(serial)
					directories[prefix] = id
				}
				if !media115PipelineHas(drive, parent, id) {
					drive.dirs[parent] = append(drive.dirs[parent], p115.ShareFile{ID: id, Name: name, Directory: true})
					drive.paths[id] = path.Join(rule.Source, prefix)
				}
				parent = id
			}
			serial++
			id := strconv.Itoa(serial)
			ids[relative] = id
			drive.dirs[parent] = append(drive.dirs[parent], p115.ShareFile{ID: id, Name: path.Base(relative), Size: 123})
		}
		fixture.sourceExists = func(relative string) bool {
			return media115PipelineHas(drive, directories[path.Dir(relative)], ids[relative])
		}
		fixture.targetExists = func(entry MediaOrganizeEntry) bool {
			for parent, children := range drive.dirs {
				for _, child := range children {
					if child.ID == entry.File.ID && path.Join(drive.paths[parent], child.Name) == entry.Target {
						return true
					}
				}
			}
			return false
		}
	default:
		t.Fatalf("未知测试存储：%s", storage)
	}
	rule.AttachmentCleanupEnabled, rule.AttachmentGraceSeconds = true, 0
	configured, err := organizer.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	rule = configured
	fixture.run = func() MediaOrganizeRun {
		var run MediaOrganizeRun
		if first != nil {
			run, first = *first, nil
			run.Rule = rule
		} else {
			var err error
			run, err = organizer.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		mediaTestProcess(t, organizer, &run, "auto")
		return run
	}
	return fixture
}

func TestMediaOrganizerRecursiveSameNameSubtitles(t *testing.T) {
	const stem = "Work.S01E01.1080p"
	for _, storage := range []string{"local", "115", "cd2"} {
		for _, tc := range []struct {
			name, video, subtitle, tail string
		}{
			{"arbitrary nested directory", "Season 1/" + stem + ".mkv", "Season 1/补充资料/中文/" + stem + ".chs.ass", ".chs"},
			{"subtitle scanned first", "z-video/" + stem + ".mkv", "a-import/" + stem + ".ass", ""},
		} {
			t.Run(storage+"/"+tc.name, func(t *testing.T) {
				fixture := newMediaRecursiveSubtitleFixture(t, storage)
				fixture.add(tc.subtitle)
				fixture.add(tc.video)
				run := fixture.run()
				var video, subtitle MediaOrganizeEntry
				for _, entry := range run.Entries {
					switch entry.File.SourcePath {
					case tc.video:
						video = entry
					case tc.subtitle:
						subtitle = entry
					}
				}
				if video.Status != "done" || subtitle.Status != "done" || subtitle.VideoID != video.File.ID {
					t.Fatal("任意子目录的完整同名字幕应跟随唯一视频整理", run.Entries)
				}
				want := strings.TrimSuffix(video.Target, path.Ext(video.Target)) + tc.tail + ".ass"
				if subtitle.Target != want || !fixture.targetExists(video) || !fixture.targetExists(subtitle) {
					t.Fatal("字幕应沿用视频目标主文件名并保留后缀", subtitle.Target, want)
				}
				if fixture.sourceExists(tc.video) || fixture.sourceExists(tc.subtitle) {
					t.Fatal("整理成功后来源视频与字幕应已移走")
				}
			})
		}
	}
}

func TestMediaOrganizerRecursiveAmbiguousSubtitleIsRetained(t *testing.T) {
	for _, storage := range []string{"local", "115", "cd2"} {
		t.Run(storage, func(t *testing.T) {
			fixture := newMediaRecursiveSubtitleFixture(t, storage)
			fixture.add("a-video/Work.S01E01.mkv")
			const source = "m-import/Work.S01E01.chs.ass"
			fixture.add(source)
			fixture.add("z-video/Work.S01E01.mp4")
			run := fixture.run()
			if !fixture.sourceExists(source) {
				t.Fatal("清理已开启时，跨目录同名视频歧义字幕也应保留来源", run.Entries)
			}
			for _, entry := range run.Entries {
				if entry.File.SourcePath == source && (entry.VideoID != "" || entry.Target != "" || entry.Status == "done") {
					t.Fatal("扫描顺序不能使歧义字幕归到其中一个视频", entry)
				}
			}
		})
	}
}

func TestMediaOrganizerRecursiveLateSubtitleUsesVideoMapping(t *testing.T) {
	for _, storage := range []string{"115", "cd2"} {
		t.Run(storage, func(t *testing.T) {
			fixture := newMediaRecursiveSubtitleFixture(t, storage)
			fixture.add("video/Work.S01E01.mkv")
			first := fixture.run()
			var target string
			for _, entry := range first.Entries {
				if entry.File.MediaType == "video" && entry.Status == "done" {
					target = strings.TrimSuffix(entry.Target, path.Ext(entry.Target)) + ".chs.ass"
				}
			}
			if target == "" {
				t.Fatal("首轮视频整理失败", first.Entries)
			}
			const source = "导入文件/中文/Work.S01E01.chs.ass"
			fixture.add(source)
			second := fixture.run()
			for _, entry := range second.Entries {
				if entry.File.SourcePath == source && entry.Status == "done" && entry.Target == target && fixture.targetExists(entry) && !fixture.sourceExists(source) {
					return
				}
			}
			t.Fatal("跨目录晚到字幕应使用已确认视频目标，不能当作孤立字幕清理", second.Entries)
		})
	}
}
