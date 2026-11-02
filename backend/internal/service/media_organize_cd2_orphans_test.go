package service

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"strm115/backend/internal/storage"
)

func mediaOrphanScan(t *testing.T, s *MediaOrganizer, rule MediaOrganizeRule) MediaOrganizeRun {
	t.Helper()
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	return run
}

func mediaCD2AssertOrphansPresent(t *testing.T, fs *mediaCD2Fake, paths ...string) {
	t.Helper()
	for _, full := range paths {
		if _, exists := fs.files[full]; !exists || slices.Contains(fs.deleted, full) {
			t.Fatalf("尚受保护的字幕或附件不应被删除：%s，删除记录=%v", full, fs.deleted)
		}
	}
}

func mediaCD2AssertOrphansDeleted(t *testing.T, fs *mediaCD2Fake, paths ...string) {
	t.Helper()
	for _, full := range paths {
		if _, exists := fs.files[full]; exists || !slices.Contains(fs.deleted, full) {
			t.Fatalf("到期的孤立字幕或附件应删除：%s，删除记录=%v", full, fs.deleted)
		}
	}
}

func TestMediaOrganizerCD2OldBatchCannotRestoreChangedOrphanBinding(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	fs.add(path.Join(rule.Source, "Work.S01E01.zh.ass"), false, 16)
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	defer files.close()
	run.Entries, err = files.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.active = run.ID
	updated := rule
	updated.Source = "/新的待整理目录"
	fs.add(updated.Source, true, 0)
	if _, err := s.SaveRule(updated); err != nil {
		t.Fatal(err)
	}
	if err := s.prepareOrphanCleanup(files, &run); err != nil {
		t.Fatal(err)
	}
	if _, exists := s.doc.Orphans[rule.ID]; exists {
		t.Fatal("旧批次不得把旧来源的孤立文件宽限记录写回新规则")
	}
}

func TestMediaOrganizerOrphansExpireTenMinutesAfterFirstDiscovery(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	started := now
	s.monitorOptions.now = func() time.Time { return now }
	paths := []string{path.Join(rule.Source, "Work.S01E01.zh.ass"), path.Join(rule.Source, "Work.S01E01.nfo")}
	for _, full := range paths {
		file := fs.add(full, false, 16)
		file.Modified = now.Add(-48 * time.Hour)
		fs.files[full] = file
	}
	zip := fs.add(path.Join(rule.Source, "advertisement.zip"), false, 4)
	first := mediaOrphanScan(t, s, rule)
	mediaCD2AssertOrphansPresent(t, fs, paths...)
	if !slices.Equal(fs.deleted, []string{zip.Path}) {
		t.Fatal("首次发现即应清理普通无关文件，字幕和附件按本地发现时间等待", fs.deleted)
	}
	now = started.Add(9*time.Minute + 59*time.Second)
	mediaOrphanScan(t, s, rule)
	mediaCD2AssertOrphansPresent(t, fs, paths...)
	if _, err := s.Snapshot(first.ID); !errors.Is(err, ErrMediaOrganizeInput) {
		t.Fatal("测试应经过旧无视频批次自动清理，倒计时不能依赖历史批次", err)
	}
	now = started.Add(10 * time.Minute)
	mediaOrphanScan(t, s, rule)
	mediaCD2AssertOrphansDeleted(t, fs, paths...)
}

func TestMediaOrganizerCD2OrphanIdentityChangesRestartGracePeriod(t *testing.T) {
	for _, field := range []string{"id", "size", "modified", "hash"} {
		t.Run(field, func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			fs := connection.filesystem
			now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
			started := now
			s.monitorOptions.now = func() time.Time { return now }
			file := fs.add(path.Join(rule.Source, "Work.S01E01.zh.ass"), false, 16)
			mediaOrphanScan(t, s, rule)
			now = started.Add(9 * time.Minute)
			changed := fs.files[file.Path]
			switch field {
			case "id":
				changed.ID += "-replacement"
			case "size":
				changed.Size++
			case "modified":
				changed.Modified = changed.Modified.Add(time.Second)
			case "hash":
				changed.Hashes = map[uint32]string{2: "changed-content"}
			}
			fs.files[file.Path] = changed
			mediaOrphanScan(t, s, rule)
			now = started.Add(10 * time.Minute)
			mediaOrphanScan(t, s, rule)
			mediaCD2AssertOrphansPresent(t, fs, file.Path)
			now = started.Add(18*time.Minute + 59*time.Second)
			mediaOrphanScan(t, s, rule)
			mediaCD2AssertOrphansPresent(t, fs, file.Path)
			now = started.Add(19 * time.Minute)
			mediaOrphanScan(t, s, rule)
			mediaCD2AssertOrphansDeleted(t, fs, file.Path)
		})
	}
}

func TestMediaOrganizerCD2OrphanGraceSurvivesSQLiteRestart(t *testing.T) {
	base, _, _, connection, rule := newMediaCD2Organizer(t)
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "organize.db")
	db, err := storage.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if db != nil {
			_ = db.Close()
		}
	})
	s, err := OpenMediaOrganizerSQLite(ctx, db, directory, "isolated-test", base.metadata, base.templates, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.cd2 = connection
	rule.ID = ""
	rule, err = s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC)
	started := now
	s.monitorOptions.now = func() time.Time { return now }
	file := connection.filesystem.add(path.Join(rule.Source, "Work.S01E01.nfo"), false, 16)
	mediaOrphanScan(t, s, rule)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenMediaOrganizerSQLite(ctx, db, directory, "isolated-test", base.metadata, base.templates, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.cd2 = connection
	restarted.monitorOptions.now = func() time.Time { return now }
	now = started.Add(9*time.Minute + 59*time.Second)
	mediaOrphanScan(t, restarted, rule)
	mediaCD2AssertOrphansPresent(t, connection.filesystem, file.Path)
	now = started.Add(10 * time.Minute)
	mediaOrphanScan(t, restarted, rule)
	mediaCD2AssertOrphansDeleted(t, connection.filesystem, file.Path)
}

func TestMediaOrganizerCD2ExpiredOrphansJoinArrivingVideo(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	now := time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC)
	s.monitorOptions.now = func() time.Time { return now }
	for _, name := range []string{"Work.S01E01.zh.ass", "Work.S01E01.nfo"} {
		fs.add(path.Join(rule.Source, name), false, 16)
	}
	mediaOrphanScan(t, s, rule)
	now = now.Add(10 * time.Minute)
	fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 128)
	run := mediaOrphanScan(t, s, rule)
	if len(fs.deleted) != 0 || len(run.Entries) != 3 || run.Status != "completed" {
		t.Fatal("到期时已有对应视频，字幕和附件应整理而不是删除", run.Entries, fs.deleted)
	}
	for _, entry := range run.Entries {
		if entry.Status != "done" {
			t.Fatal("对应视频到达后所有关联文件应成功整理", entry)
		}
		if _, exists := fs.files[entry.Target]; !exists {
			t.Fatal("整理目标缺少视频或关联文件", entry)
		}
	}
}

func TestMediaOrganizerCD2ExpiredOrphansKeepTemporaryDownload(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	now := time.Date(2026, 11, 3, 4, 0, 0, 0, time.UTC)
	s.monitorOptions.now = func() time.Time { return now }
	subtitle := fs.add(path.Join(rule.Source, "Work.S01E01.zh.ass"), false, 16)
	attachment := fs.add(path.Join(rule.Source, "Work.S01E01.nfo"), false, 16)
	mediaOrphanScan(t, s, rule)
	now = now.Add(10 * time.Minute)
	partial := fs.add(path.Join(rule.Source, "Work.S01E01.mkv.part"), false, 64)
	mediaOrphanScan(t, s, rule)
	mediaCD2AssertOrphansPresent(t, fs, subtitle.Path, attachment.Path, partial.Path)
}

func TestMediaOrganizerCD2ExpiredOrphansRecheckVideoBeforeDelete(t *testing.T) {
	for _, suffix := range []string{".mkv", ".mkv.part"} {
		t.Run(suffix, func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			fs := connection.filesystem
			now := time.Date(2026, 11, 3, 5, 0, 0, 0, time.UTC)
			s.monitorOptions.now = func() time.Time { return now }
			subtitle := fs.add(path.Join(rule.Source, "Work.S01E01.zh.ass"), false, 16)
			attachment := fs.add(path.Join(rule.Source, "Work.S01E01.nfo"), false, 16)
			mediaOrphanScan(t, s, rule)
			now = now.Add(10 * time.Minute)
			lists := 0
			arrived := false
			fs.onList = func(directory string) error {
				if directory == rule.Source {
					lists++
					if lists == 2 {
						arrived = true
						fs.add(path.Join(rule.Source, "Work.S01E01"+suffix), false, 128)
					}
				}
				return nil
			}
			mediaOrphanScan(t, s, rule)
			if !arrived {
				t.Fatal("删除孤立侧车前应重新列举对应目录")
			}
			mediaCD2AssertOrphansPresent(t, fs, subtitle.Path, attachment.Path)
		})
	}
}

func TestMediaOrganizerCD2FailedFullScanNeverDeletesExpiredOrphans(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	now := time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)
	s.monitorOptions.now = func() time.Time { return now }
	subtitle := fs.add(path.Join(rule.Source, "Work.S01E01.zh.ass"), false, 16)
	attachment := fs.add(path.Join(rule.Source, "Work.S01E01.nfo"), false, 16)
	mediaOrphanScan(t, s, rule)
	now = now.Add(10 * time.Minute)
	later := fs.add(path.Join(rule.Source, "later"), true, 0)
	wantErr := errors.New("模拟后续目录读取失败")
	fs.onList = func(directory string) error {
		if directory == later.Path {
			return wantErr
		}
		return nil
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	delete(s.actions, run.ID)
	s.mu.Unlock()
	if err = s.process(context.Background(), &run, "auto"); !errors.Is(err, wantErr) {
		t.Fatal("测试必须经过后续目录扫描失败路径", err)
	}
	mediaCD2AssertOrphansPresent(t, fs, subtitle.Path, attachment.Path)
	if len(fs.deleted) != 0 {
		t.Fatal("完整扫描失败时不得执行清理", fs.deleted)
	}
}
