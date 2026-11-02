package service

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMediaOrganizerCD2OrganizesBeforeReadingLaterDirectory(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	first := fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	fs.add(path.Join(rule.Source, "later"), true, 0)
	fs.add(path.Join(rule.Source, "later/Work.S01E02.mkv"), false, 123)
	checked := false
	fs.onList = func(directory string) error {
		if directory == path.Join(rule.Source, "later") && !checked {
			checked = true
			if _, exists := fs.files[first.Path]; exists {
				t.Fatal("开始读取后续目录前，首个目录的视频应已完成整理")
			}
		}
		return nil
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	if !checked || run.Status != "completed" {
		t.Fatal("目录分批整理未完成", checked, run.Status)
	}
	if len(mediaTasks(run)) != 2 {
		t.Fatal("分批整理不能丢失前一个目录的持久记录", run.Entries)
	}
}

func TestMediaOrganizerCD2SHA1ConflictPolicy(t *testing.T) {
	for _, scenario := range []string{"same", "different", "missing-source", "missing-target", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			fs := connection.filesystem
			rule.DeleteDuplicates = scenario != "disabled"
			source := fs.add(path.Join(rule.Source, "original.mkv"), false, 100)
			target := fs.add(path.Join(rule.Target, "renamed.mkv"), false, 200)
			source.Hashes = map[uint32]string{2: strings.Repeat("a", 40)}
			target.Hashes = map[uint32]string{2: strings.Repeat("A", 40)}
			switch scenario {
			case "different":
				target.Hashes[2] = strings.Repeat("b", 40)
			case "missing-source":
				source.Hashes = nil
			case "missing-target":
				target.Hashes = nil
			}
			fs.files[source.Path], fs.files[target.Path] = source, target
			entry := mediaCD2Entry(source, rule.Source, "renamed.mkv")
			entry.Status = "ready"
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			run.Rule, run.Entries = rule, []MediaOrganizeEntry{entry}
			files, err := s.files(context.Background(), rule)
			if err != nil {
				t.Fatal(err)
			}
			defer files.close()
			s.bindCD2Checkpoint(files, &run)
			if err := s.execute(context.Background(), files, &run); err != nil {
				t.Fatal(err)
			}
			_, sourceExists := fs.files[source.Path]
			if scenario == "same" {
				if sourceExists || len(fs.deleted) != 1 || run.Entries[0].Result.Action != "duplicate" || run.Status != "completed" {
					t.Fatal("相同 SHA1 即可删除来源，不额外比较大小", fs.deleted, run.Entries)
				}
				if err := files.move(context.Background(), run.Entries[0]); !errors.Is(err, errMediaDuplicateDone) {
					t.Fatal("删除写入回执应支持恢复核对", err)
				}
			} else if !sourceExists || len(fs.deleted) != 0 || run.Entries[0].Status != "conflict" {
				t.Fatal("未满足 SHA1 判重条件必须保留来源", run.Entries)
			}
			if actual := fs.files[target.Path]; actual.ID != target.ID || actual.Size != target.Size {
				t.Fatal("目标原文件被改动", actual)
			}
		})
	}
}

func TestMediaOrganizerCD2LateSubtitleUsesPersistedVideoMapping(t *testing.T) {
	s, _, store, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	first, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &first, "auto")
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.cd2 = connection
	late := fs.add(path.Join(rule.Source, "Work.S01E01.zh.ass"), false, 8)
	late.Modified = time.Now()
	fs.files[late.Path] = late
	second, err := restarted.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, restarted, &second, "auto")
	if _, exists := fs.files[late.Path]; exists || slices.Contains(fs.deleted, late.Path) {
		t.Fatal("晚到字幕应补入历史视频目录，不能被当成孤立字幕删除", second.Entries, fs.deleted)
	}
	if len(second.Entries) != 2 || !slices.ContainsFunc(second.Entries, func(entry MediaOrganizeEntry) bool { return entry.CD2Reference && entry.Status == "done" }) {
		t.Fatal("晚到字幕缺少持久来源映射", second.Entries)
	}
}

func TestMediaOrganizerCD2PartialScanNeverCleansEarlierOrphans(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	orphan := fs.add(path.Join(rule.Source, "advertisement.zip"), false, 8)
	broken := fs.add(path.Join(rule.Source, "broken"), true, 0)
	fs.onList = func(directory string) error {
		if directory == broken.Path {
			return errors.New("模拟后续目录读取失败")
		}
		return nil
	}
	run, _ := s.Start(rule.ID)
	if err := s.process(context.Background(), &run, "auto"); err == nil {
		t.Fatal("不完整扫描应返回错误")
	}
	if _, exists := fs.files[orphan.Path]; !exists || len(fs.deleted) != 0 {
		t.Fatal("未完成全部扫描之前不应清理来源文件", fs.deleted)
	}
	if !slices.ContainsFunc(run.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.MediaType == "video" && entry.Status == "done" }) {
		t.Fatal("后续扫描失败不能丢弃已经完成的整理结果")
	}
}

func TestMediaOrganizerCD2LocalRecheckDoesNotScanOtherDirectories(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	child := fs.add(path.Join(rule.Source, "other"), true, 0)
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	defer files.close()
	files.cd2ScanPath, files.cd2Shallow = rule.Source, true
	entries, err := files.scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Status, entries[0].Relative = "ready", "video.mkv"
	if err := files.recheckLocalCompanions(context.Background(), &MediaOrganizeRun{Rule: rule, Entries: entries}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(fs.listed, child.Path) {
		t.Fatal("单目录执行前复查不应扫描整棵树", fs.listed)
	}
}

func TestMediaOrganizerCD2ConfigurablePollingSchedule(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		elapsed time.Duration
		enabled bool
		initial bool
		pending string
		queued  bool
	}{
		{name: "initial-scan", enabled: true, initial: true, queued: true},
		{name: "before-interval", elapsed: 36 * time.Second, enabled: true},
		{name: "at-interval", elapsed: 37 * time.Second, enabled: true, queued: true},
		{name: "disabled", elapsed: time.Minute},
		{name: "queued", elapsed: time.Minute, enabled: true, pending: "queued"},
		{name: "scanning", elapsed: time.Minute, enabled: true, pending: "scanning"},
		{name: "running", elapsed: time.Minute, enabled: true, pending: "running"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, _, _, _, rule := newMediaCD2Organizer(t)
			rule.IntervalSeconds = 37
			rule.ScheduleEnabled, rule.LastScan = scenario.enabled, time.Now().Add(-time.Minute)
			rule.NextScanAt = rule.LastScan.Add(37 * time.Second)
			if scenario.initial {
				rule.LastScan, rule.NextScanAt = time.Time{}, time.Time{}
			}
			s.doc.Rules = []MediaOrganizeRule{rule}
			if scenario.pending != "" {
				s.doc.Runs = []MediaOrganizeRun{{ID: "pending", Rule: rule, Status: scenario.pending}}
			}
			now := time.Now()
			if !scenario.initial {
				now = rule.LastScan.Add(scenario.elapsed)
			}
			s.monitorOptions.now = func() time.Time { return now }
			if err := s.schedule(); err != nil {
				t.Fatal(err)
			}
			if (len(s.actions) > 0) != scenario.queued {
				t.Fatal("CD2 应使用规则间隔轮询，停用或已有任务时不能重复入队", s.actions)
			}
			status, monitored := s.State().Monitoring[rule.ID]
			if monitored != scenario.enabled || monitored && status.Mode != "polling" {
				t.Fatal("启用的 CD2 规则应直接显示轮询状态", status)
			}
			if scenario.queued {
				if !s.doc.Rules[0].LastScan.After(rule.LastScan) {
					t.Fatal("轮询入队必须记录最近扫描时间")
				}
				if err := s.schedule(); err != nil || len(s.actions) != 1 || len(s.doc.Runs) != 1 {
					t.Fatal("重复调度不能叠加同一规则的扫描", s.doc.Runs, err)
				}
			}
		})
	}
}

func TestMediaOrganizerCD2FreshVideoAndCompanionsMoveImmediately(t *testing.T) {
	for _, offset := range []time.Duration{0, time.Minute} {
		t.Run(offset.String(), func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			fs := connection.filesystem
			for _, name := range []string{"Work.S01E01.mkv", "Work.S01E01.zh.ass", "Work.S01E01.nfo", "Work.S01E01-poster.jpg"} {
				file := fs.add(path.Join(rule.Source, name), false, 123)
				file.Modified = time.Now().Add(offset)
				fs.files[file.Path] = file
			}
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &run, "auto")
			if run.Status != "completed" || len(run.Entries) != 4 || len(fs.deleted) != 0 {
				t.Fatal("刚写入的视频、字幕及附件应在本轮全部整理，不按修改时间等待", run.Entries, fs.deleted)
			}
			for _, entry := range run.Entries {
				if _, exists := fs.files[entry.File.ID]; exists || entry.Status != "done" {
					t.Fatal("同批媒体及附件不应遗漏或留在来源", entry)
				}
				if target, exists := fs.files[entry.Target]; !exists || target.Size != entry.File.Size {
					t.Fatal("目标缺少已整理的视频、字幕或附件", entry)
				}
			}
		})
	}
}

func TestMediaOrganizerCD2CompanionsArrivingBeforeVideoAreRetained(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	for _, name := range []string{"Work.S01E01.zh.ass", "Work.S01E01.nfo", "Work.S01E01-poster.jpg"} {
		file := fs.add(path.Join(rule.Source, name), false, 8)
		file.Modified = time.Now()
		fs.files[file.Path] = file
	}
	unrelated := fs.add(path.Join(rule.Source, "advertisement.zip"), false, 8)
	first, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &first, "auto")
	if !slices.Equal(fs.deleted, []string{unrelated.Path}) {
		t.Fatal("CD2 应只清理普通无关文件，不删除先到的字幕及附件", fs.deleted)
	}
	for _, entry := range first.Entries {
		if entry.File.MediaType != "subtitle" && entry.File.MediaType != "attachment" {
			continue
		}
		if _, exists := fs.files[entry.File.ID]; !exists || entry.Status != "retained" {
			t.Fatal("视频尚未到达时应保留字幕及附件", entry)
		}
	}
	video := fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	video.Modified = time.Now()
	fs.files[video.Path] = video
	second, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &second, "auto")
	if second.Status != "completed" || len(second.Entries) != 4 {
		t.Fatal("视频到达后应立即补齐此前保留的字幕及附件", second.Entries)
	}
	for _, entry := range second.Entries {
		_, sourceExists := fs.files[entry.File.ID]
		_, targetExists := fs.files[entry.Target]
		if sourceExists || !targetExists || entry.Status != "done" {
			t.Fatal("视频与先到字幕、附件应全部整理到目标", entry)
		}
	}
}

func TestMediaOrganizerCD2RetainedVideoNeverReactivatedByLaterBatch(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	first := fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	fs.add(path.Join(rule.Source, "later"), true, 0)
	second := fs.add(path.Join(rule.Source, "later/Work.S01E02.mkv"), false, 123)
	rootLists := 0
	fs.onList = func(directory string) error {
		if directory == rule.Source {
			rootLists++
			if rootLists == 2 {
				late := fs.add(path.Join(rule.Source, "Work.S01E01.ass"), false, 8)
				late.Modified = time.Now()
				fs.files[late.Path] = late
			}
		}
		return nil
	}
	run, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &run, "auto")
	if _, exists := fs.files[first.Path]; !exists {
		t.Fatal("首目录因新到字幕保留的视频，不能在下一目录被重新激活", run.Entries)
	}
	if _, exists := fs.files[second.Path]; exists {
		t.Fatal("后续目录仍应正常整理", run.Entries)
	}
	if !slices.ContainsFunc(run.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == first.Path && entry.Status == "retained" }) {
		t.Fatal("首目录的保留状态被后续批次覆盖", run.Entries)
	}
	next, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &next, "auto")
	for _, source := range []string{first.Path, path.Join(rule.Source, "Work.S01E01.ass")} {
		if _, exists := fs.files[source]; exists || slices.Contains(fs.deleted, source) {
			t.Fatal("下一轮应把视频与新到字幕一起移走，不能误删字幕", source, next.Entries, fs.deleted)
		}
	}
	if next.Status != "completed" || len(next.Entries) != 2 {
		t.Fatal("下一轮应补齐此前保留的视频和字幕", next.Entries)
	}
}

func TestMediaOrganizerCD2LateSubtitleWithoutHashesAndChangedTarget(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid-receipt", true: "changed-target"}[changed], func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			fs := connection.filesystem
			fs.pathIDs = true
			video := fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
			video.Hashes = nil
			fs.files[video.Path] = video
			first, _ := s.Start(rule.ID)
			mediaTestProcess(t, s, &first, "auto")
			late := fs.add(path.Join(rule.Source, "Work.S01E01.ass"), false, 8)
			if changed {
				for _, entry := range first.Entries {
					if entry.File.MediaType == "video" {
						target := fs.files[entry.Target]
						target.Size++
						fs.files[entry.Target] = target
					}
				}
			}
			second, _ := s.Start(rule.ID)
			err := s.process(context.Background(), &second, "auto")
			_, retained := fs.files[late.Path]
			if slices.Contains(fs.deleted, late.Path) || !changed && (err != nil || retained) || changed && (err == nil || !retained) {
				t.Fatal("无哈希应使用目标写回执，目标改变应保留附件", err, fs.deleted, second.Entries)
			}
		})
	}
}

func TestMediaOrganizerCD2LateSharedArtworkUsesParentScope(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	first, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &first, "auto")
	folder := fs.add(path.Join(rule.Source, ".actors"), true, 0)
	art := fs.add(path.Join(folder.Path, "actor.jpg"), false, 8)
	art.Modified = time.Now()
	fs.files[art.Path] = art
	second, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &second, "auto")
	if _, retained := fs.files[art.Path]; retained || slices.Contains(fs.deleted, art.Path) {
		t.Fatal("晚到共享附件应按父级作用范围关联历史视频", second.Entries, fs.deleted)
	}
}

func TestMediaOrganizerCD2FailedScanRetriesAtPollingInterval(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	rule.ScheduleEnabled = true
	s.doc.Rules = []MediaOrganizeRule{rule}
	connection.filesystem.onList = func(string) error { return errors.New("模拟扫描失败") }
	if err := s.schedule(); err != nil {
		t.Fatal(err)
	}
	run := s.doc.Runs[0]
	delete(s.actions, run.ID)
	if err := s.process(context.Background(), &run, "auto"); err == nil {
		t.Fatal("扫描异常应上报错误")
	}
	run.Status = "failed"
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	started := s.doc.Rules[0].LastScan
	now := started.Add(19 * time.Second)
	s.monitorOptions.now = func() time.Time { return now }
	if err := s.schedule(); err != nil || len(s.actions) != 0 {
		t.Fatal("失败扫描不能立即循环重试", s.actions, err)
	}
	now = started.Add(20 * time.Second)
	if err := s.schedule(); err != nil || len(s.actions) != 1 {
		t.Fatal("失败扫描应在下一个 20 秒轮询周期重试", s.actions, err)
	}
}

func TestMediaOrganizerCD2DuplicateVideoStillAcceptsNewSubtitle(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	original := fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	first, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &first, "auto")
	rule.DeleteDuplicates = true
	if _, err := s.SaveRule(rule); err != nil {
		t.Fatal(err)
	}
	duplicate := fs.add(original.Path, false, 456)
	duplicate.Hashes = original.Hashes
	fs.files[duplicate.Path] = duplicate
	subtitle := fs.add(path.Join(rule.Source, "Work.S01E01.ass"), false, 8)
	second, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &second, "auto")
	if second.Status != "completed" || !slices.Equal(fs.deleted, []string{duplicate.Path}) {
		t.Fatal("只删除同 SHA1 来源视频，继续补齐字幕", second.Entries, fs.deleted)
	}
	if _, exists := fs.files[subtitle.Path]; exists {
		t.Fatal("重复视频已由目标承接，新增字幕应继续移动")
	}
	if !slices.ContainsFunc(second.Entries, func(entry MediaOrganizeEntry) bool {
		return entry.File.MediaType == "video" && entry.Result != nil && entry.Result.Action == "duplicate"
	}) {
		t.Fatal("去重结果必须明确记录，不能冒充移动成功", second.Entries)
	}
}

func TestMediaOrganizerCD2LateSubtitleOnlyChecksRelatedHistoricalTarget(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	fs.add(path.Join(rule.Source, "Work.S01E02.mkv"), false, 123)
	first, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &first, "auto")
	unrelated := ""
	for _, entry := range first.Entries {
		if entry.File.Episode == 2 {
			unrelated = entry.Target
			delete(fs.files, unrelated)
		}
	}
	if unrelated == "" {
		t.Fatal("测试缺少无关历史目标")
	}
	late := fs.add(path.Join(rule.Source, "Work.S01E01.ass"), false, 8)
	fs.statCalls = nil
	second, _ := s.Start(rule.ID)
	mediaTestProcess(t, s, &second, "auto")
	if slices.Contains(fs.statCalls, unrelated) {
		t.Fatal("单条字幕不应核验无关历史目标", fs.statCalls)
	}
	if _, retained := fs.files[late.Path]; retained {
		t.Fatal("无关历史目标被删除不能阻塞有效字幕补齐", second.Entries)
	}
}
