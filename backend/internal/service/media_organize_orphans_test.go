package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type mediaOrphanFixture struct {
	organizer *MediaOrganizer
	rule      MediaOrganizeRule
	add       func(string) string
	present   func(string) bool
}

func newMediaOrphanFixture(t *testing.T, storage string) mediaOrphanFixture {
	t.Helper()
	switch storage {
	case "local":
		s, _, _, rule := newMediaOrganizeTest(t)
		return mediaOrphanFixture{s, rule, func(name string) string {
			mediaTestFile(t, rule.Source, name, "字幕或附件")
			return name
		}, func(name string) bool {
			_, err := os.Stat(filepath.Join(rule.Source, name))
			return err == nil
		}}
	case "115":
		s, drive, rule := newMediaCleanupCloud(t)
		return mediaOrphanFixture{s, rule, func(name string) string {
			id := strconv.Itoa(500 + len(drive.dirs[rule.SourceID]))
			drive.dirs[rule.SourceID] = append(drive.dirs[rule.SourceID], p115.ShareFile{ID: id, Name: name, Size: 16, SHA1: "AABBCC"})
			return id
		}, func(name string) bool {
			for _, file := range drive.dirs[rule.SourceID] {
				if file.Name == name {
					return true
				}
			}
			return false
		}}
	case "cd2":
		s, _, _, connection, rule := newMediaCD2Organizer(t)
		return mediaOrphanFixture{s, rule, func(name string) string {
			return connection.filesystem.add(path.Join(rule.Source, name), false, 16).Path
		}, func(name string) bool {
			_, exists := connection.filesystem.files[path.Join(rule.Source, name)]
			return exists
		}}
	}
	t.Fatalf("不支持的测试存储：%s", storage)
	return mediaOrphanFixture{}
}

func TestMediaOrganizerSharedOrphanGracePersistsAcrossRestartAndEdits(t *testing.T) {
	for _, storage := range []string{"local", "115", "cd2"} {
		t.Run(storage, func(t *testing.T) {
			fixture := newMediaOrphanFixture(t, storage)
			s, rule := fixture.organizer, fixture.rule
			rule.AttachmentGraceSeconds = 15
			var err error
			rule, err = s.SaveRule(rule)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC)
			started := now
			s.monitorOptions.now = func() time.Time { return now }
			id := fixture.add("orphan.ass")
			fixture.add("other.nfo")
			mediaOrphanScan(t, s, rule)
			if !fixture.present("orphan.ass") || !fixture.present("other.nfo") {
				t.Fatal("首次发现应保留附件")
			}
			if !s.doc.Orphans[rule.ID].Files[id].Since.Equal(started) {
				t.Fatal("应保存首次发现时间")
			}
			restarted, err := NewMediaOrganizer(s.db.(*testMediaOrganizeStore).source, s.metadata, s.templates, s.classification, s.accounts, s.drive, s.reserve)
			if err != nil {
				t.Fatal(err)
			}
			restarted.cd2 = s.cd2
			restarted.monitorOptions.now = func() time.Time { return now }
			now = started.Add(14 * time.Second)
			mediaOrphanScan(t, restarted, rule)
			if !fixture.present("orphan.ass") || !restarted.doc.Orphans[rule.ID].Files[id].Since.Equal(started) {
				t.Fatal("重启和重复扫描不能重置计时")
			}
			rule.AttachmentGraceSeconds = 10
			rule, err = restarted.SaveRule(rule)
			if err != nil {
				t.Fatal(err)
			}
			mediaOrphanScan(t, restarted, rule)
			if fixture.present("orphan.ass") || fixture.present("other.nfo") {
				t.Fatal("缩短宽限后应按首次发现时间判断到期")
			}
			if len(restarted.doc.Orphans[rule.ID].Files) != 0 {
				t.Fatal("删除成功后应清除对应计时")
			}
		})
	}
}

func TestMediaOrganizerDisabledOrphanCleanupKeepsFirstDiscovery(t *testing.T) {
	for _, storage := range []string{"local", "115", "cd2"} {
		t.Run(storage, func(t *testing.T) {
			fixture := newMediaOrphanFixture(t, storage)
			s, rule := fixture.organizer, fixture.rule
			rule.AttachmentCleanupEnabled, rule.AttachmentGraceSeconds = false, 0
			var err error
			rule, err = s.SaveRule(rule)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC)
			started := now
			s.monitorOptions.now = func() time.Time { return now }
			id := fixture.add("orphan.ass")
			mediaOrphanScan(t, s, rule)
			now = now.Add(time.Hour)
			mediaOrphanScan(t, s, rule)
			if !fixture.present("orphan.ass") || !s.doc.Orphans[rule.ID].Files[id].Since.Equal(started) {
				t.Fatal("关闭时应保留文件与原首次发现时间")
			}
			rule.AttachmentCleanupEnabled = true
			rule, err = s.SaveRule(rule)
			if err != nil {
				t.Fatal(err)
			}
			mediaOrphanScan(t, s, rule)
			if fixture.present("orphan.ass") {
				t.Fatal("重新开启且宽限为0时应复核后清理")
			}
		})
	}
}

func TestMediaOrganizerOrphanThemeUsesGraceAndKeepsStandaloneAudio(t *testing.T) {
	for _, storage := range []string{"local", "115", "cd2"} {
		t.Run(storage, func(t *testing.T) {
			fixture := newMediaOrphanFixture(t, storage)
			s, rule := fixture.organizer, fixture.rule
			rule.AttachmentGraceSeconds = 10
			var err error
			rule, err = s.SaveRule(rule)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
			s.monitorOptions.now = func() time.Time { return now }
			for _, name := range []string{"theme.mp3", "theme(1).mp3", "song.mp3", "album.flac", "font.ttf", "movie.iso"} {
				fixture.add(name)
			}
			mediaOrphanScan(t, s, rule)
			if !fixture.present("theme.mp3") || !fixture.present("theme(1).mp3") {
				t.Fatal("影视主题音频应先等待附件宽限")
			}
			now = now.Add(10 * time.Second)
			mediaOrphanScan(t, s, rule)
			if fixture.present("theme.mp3") || fixture.present("theme(1).mp3") {
				t.Fatal("无视频的主题音频应在宽限到期后清理")
			}
			for _, name := range []string{"song.mp3", "album.flac", "font.ttf", "movie.iso"} {
				if !fixture.present(name) {
					t.Fatal("普通音频、字体和未支持的视频不能作为无关文件删除", name)
				}
			}
		})
	}
}

func TestMediaOrganizerThemeFollowsVideoAndKeepsOneCopy(t *testing.T) {
	for _, storage := range []string{"local", "115", "cd2"} {
		t.Run(storage, func(t *testing.T) {
			fixture := newMediaOrphanFixture(t, storage)
			if storage == "115" {
				s, drive, _, previous, _ := newMedia115PipelineTest(t)
				fixture = mediaOrphanFixture{s, previous.Rule, func(name string) string {
					id := strconv.Itoa(500 + len(drive.dirs["10"]))
					drive.dirs["10"] = append(drive.dirs["10"], p115.ShareFile{ID: id, Name: name, Size: 16})
					return id
				}, func(name string) bool {
					for _, file := range drive.dirs["10"] {
						if file.Name == name {
							return true
						}
					}
					return false
				}}
			}
			for _, name := range []string{"Work.S01E01.mkv", "theme.mp3", "theme(1).mp3"} {
				fixture.add(name)
			}
			run, err := fixture.organizer.Start(fixture.rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, fixture.organizer, &run, "auto")
			if fixture.present("Work.S01E01.mkv") || fixture.present("theme.mp3") || fixture.present("theme(1).mp3") {
				t.Fatal("主题音频应随视频整理并按同角色留一", run.Entries)
			}
			moved, kept := 0, 0
			for _, entry := range run.Entries {
				if !mediaAttachment(entry.File.Name) || path.Ext(entry.File.Name) != ".mp3" || entry.Result == nil || entry.Status != "done" {
					continue
				}
				if entry.Result.Action == "attachment_kept" {
					kept++
				} else if path.Base(entry.Target) == "theme.mp3" {
					moved++
				}
			}
			if moved != 1 || kept != 1 {
				t.Fatal("目标应保留一份主题音频，另一份确认回收", moved, kept, run.Entries)
			}
		})
	}
}

func TestMediaOrganizerLocalOrphanReplacementResetsNativeIdentityGrace(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	rule.AttachmentGraceSeconds = 20
	var err error
	rule, err = s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 4, 0, 0, 0, time.UTC)
	s.monitorOptions.now = func() time.Time { return now }
	mediaTestFile(t, rule.Source, "orphan.ass", "old")
	full := filepath.Join(rule.Source, "orphan.ass")
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	mediaOrphanScan(t, s, rule)
	first := s.doc.Orphans[rule.ID].Files["orphan.ass"]
	if err := os.Rename(full, filepath.Join(t.TempDir(), "old.ass")); err != nil {
		t.Fatal(err)
	}
	mediaTestFile(t, rule.Source, "orphan.ass", "new")
	if err := os.Chtimes(full, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(19 * time.Second)
	mediaOrphanScan(t, s, rule)
	second := s.doc.Orphans[rule.ID].Files["orphan.ass"]
	if first.Identity == second.Identity || !second.Since.Equal(now) {
		t.Fatal("同路径大小和时间相同的替换文件必须重置原生身份和宽限", first, second)
	}
	now = now.Add(time.Second)
	mediaOrphanScan(t, s, rule)
	if _, err := os.Stat(full); err != nil {
		t.Fatal("替换文件不能继承旧文件到期时间", err)
	}
	now = now.Add(19 * time.Second)
	mediaOrphanScan(t, s, rule)
	if _, err := os.Stat(full); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("新文件自身到期后应清理", err)
	}
}

func TestMediaOrganizerStoredAttachmentDefaultsAndExplicitZero(t *testing.T) {
	for _, raw := range []string{`{"storage":"local","intervalSeconds":300}`, `{"storage":"cd2","intervalSeconds":20}`} {
		rule, err := mediaStoredRule(json.RawMessage(raw))
		if err != nil || !rule.AttachmentCleanupEnabled || rule.AttachmentGraceSeconds != 10 {
			t.Fatal("缺失字段默认应为开启和10秒", rule, err)
		}
	}
	rule, err := mediaStoredRule(json.RawMessage(`{"storage":"115","intervalSeconds":300,"attachmentCleanupEnabled":false,"attachmentGraceSeconds":0}`))
	if err != nil || rule.AttachmentCleanupEnabled || rule.AttachmentGraceSeconds != 0 {
		t.Fatal("必须保留明确关闭和0秒", rule, err)
	}
}

func TestMediaOrganizerOldCleanupCannotForgetEditedBinding(t *testing.T) {
	s, _, _, _, rule := newMediaCD2Organizer(t)
	updated := rule
	updated.Source = "/其他网盘/new-inbox"
	state := mediaOrphanBinding(updated)
	state.Files = map[string]mediaOrphan{"same-file": {Since: time.Now(), FileID: "new-file"}}
	s.doc.Orphans = map[string]mediaOrphanState{rule.ID: state}
	s.mu.Lock()
	s.forgetOrphanLocked(rule, "same-file")
	s.mu.Unlock()
	if _, exists := s.doc.Orphans[rule.ID].Files["same-file"]; !exists {
		t.Fatal("旧批次完成删除不能清除编辑后新绑定的计时")
	}
}

func TestMediaOrganizerUnstableLocalOrphanKeepsFirstDiscovery(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	rule.AttachmentGraceSeconds = 10
	var err error
	rule, err = s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestFile(t, rule.Source, "orphan.ass", "subtitle")
	now := time.Date(2026, 11, 3, 5, 0, 0, 0, time.UTC)
	started := now
	s.monitorOptions.now = func() time.Time { return now }
	run, _ := s.Start(rule.ID)
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	files.localReady = func(string, os.FileInfo) bool { return false }
	run.Entries, err = files.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.prepareOrphanCleanup(files, &run); err != nil {
		t.Fatal(err)
	}
	if len(files.orphanDue) != 0 || !s.doc.Orphans[rule.ID].Files["orphan.ass"].Since.Equal(started) {
		t.Fatal("未稳定附件应开始计时但不能立即删除")
	}
	now = now.Add(11 * time.Second)
	files.localReady = func(string, os.FileInfo) bool { return true }
	run.Entries, err = files.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.prepareOrphanCleanup(files, &run); err != nil {
		t.Fatal(err)
	}
	if !files.orphanDue["orphan.ass"] || !s.doc.Orphans[rule.ID].Files["orphan.ass"].Since.Equal(started) {
		t.Fatal("稳定后应沿用最初发现时间判断宽限")
	}
}
