package service

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/clouddrive2"
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
	files.cd2ScanPaths, files.cd2Shallow = []string{rule.Source}, true
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

func TestMediaOrganizerCD2SecondsFallbackAndEventScopedScan(t *testing.T) {
	s, _, _, _, rule := newMediaCD2Organizer(t)
	if rule.IntervalSeconds != 20 {
		t.Fatal("CD2 默认应为 20 秒", rule)
	}
	rule.Enabled = true
	s.doc.Rules[0] = rule
	s.monitorRunning = true
	s.reconcileCD2MonitorsLocked()
	monitor := s.cd2Monitors[rule.ID]
	now := time.Now()
	monitor.forceFull, monitor.lastFull = false, now
	monitor.status.Mode, monitor.status.LastFallbackAt = "polling", now
	if s.cd2ScheduleDueLocked(rule, now.Add(19*time.Second)) || !s.cd2ScheduleDueLocked(rule, now.Add(20*time.Second)) {
		t.Fatal("CD2 秒级补查时间错误")
	}
	monitor.status.Mode = "realtime"
	if !s.cd2ScheduleDueLocked(rule, now.Add(time.Minute)) {
		t.Fatal("实时模式也必须保留配置的全量补查，防止漏事件")
	}
	directory := path.Join(rule.Source, "new")
	s.handleCD2Event(CD2Event{Mode: "realtime", Revision: rule.ConnectionRevision, Change: &clouddrive2.Change{Type: "create", Path: path.Join(directory, "Work.S01E01.mkv")}})
	if s.cd2ScheduleDueLocked(rule, now) || !s.cd2ScheduleDueLocked(rule, time.Now().Add(time.Second)) {
		t.Fatal("事件应在短暂合并后触发整理")
	}
	queued, err := s.queueScanLocked(rule)
	if err != nil {
		t.Fatal(err)
	}
	s.cd2QueuedLocked(rule, queued.ID, time.Now().Add(time.Second))
	actual, err := s.Snapshot(queued.ID)
	if err != nil || !slices.Equal(actual.CD2ScanPaths, []string{directory}) {
		t.Fatal("事件应仅补扫变化目录", actual.CD2ScanPaths, err)
	}
}

func TestMediaOrganizerCD2ReadyRequiresUploadCompletion(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	rule.Enabled = true
	s.doc.Rules[0] = rule
	s.reconcileCD2MonitorsLocked()
	file := connection.filesystem.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	file.Modified = time.Now()
	ready := s.cd2FileReadiness(rule)
	s.handleCD2Event(CD2Event{Change: &clouddrive2.Change{Type: "create", Path: file.Path}})
	if ready(file) {
		t.Fatal("普通创建事件不能作为上传完成证据")
	}
	s.handleCD2Event(CD2Event{Change: &clouddrive2.Change{Type: "upload_finished", Path: file.Path, Ready: true}})
	if !ready(file) {
		t.Fatal("官方上传完成事件应允许立即整理")
	}
	s.handleCD2Event(CD2Event{Change: &clouddrive2.Change{Type: "modify", Path: file.Path}})
	if ready(file) {
		t.Fatal("完成后再次修改应撤销就绪证据")
	}
	s.handleCD2Event(CD2Event{Change: &clouddrive2.Change{Type: "upload_finished", Path: file.Path, Ready: true}})
	s.handleCD2Event(CD2Event{Change: &clouddrive2.Change{Type: "create", Path: file.Path}})
	if ready(file) {
		t.Fatal("同路径新文件创建不能继承旧上传完成证据")
	}
}

func TestMediaOrganizerCD2UploadCompletionDoesNotCompareRemoteAndLocalClocks(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	rule.Enabled = true
	s.doc.Rules[0] = rule
	s.reconcileCD2MonitorsLocked()
	file := connection.filesystem.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	file.Modified = time.Now().Add(time.Minute)
	ready := s.cd2FileReadiness(rule)
	s.handleCD2Event(CD2Event{Change: &clouddrive2.Change{Type: "upload_finished", Path: file.Path, Ready: true, File: &clouddrive2.File{Path: file.Path, Size: file.Size}}})
	wrongSize := file
	wrongSize.Size++
	if ready(wrongSize) {
		t.Fatal("首个快照必须匹配官方上传完成的大小")
	}
	if !ready(file) {
		t.Fatal("云端时钟领先本机不能使明确上传完成的文件等待稳定期")
	}
	changed := file
	changed.ID += "-new"
	if ready(changed) {
		t.Fatal("同路径的新文件不能继承已绑定文件的完成证据")
	}
	changed = file
	changed.Modified = changed.Modified.Add(time.Second)
	if ready(changed) {
		t.Fatal("完成后修改的文件不能复用旧快照")
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
		t.Fatal("首目录等待未稳定字幕的视频，不能在下一目录被重新激活", run.Entries)
	}
	if _, exists := fs.files[second.Path]; exists {
		t.Fatal("后续目录仍应正常整理", run.Entries)
	}
	if !slices.ContainsFunc(run.Entries, func(entry MediaOrganizeEntry) bool { return entry.File.ID == first.Path && entry.Status == "retained" }) {
		t.Fatal("首目录的保留状态被后续批次覆盖", run.Entries)
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
	second, _ := s.Start(rule.ID)
	second.CD2ScanPaths = []string{folder.Path}
	mediaTestProcess(t, s, &second, "auto")
	if _, retained := fs.files[art.Path]; retained || slices.Contains(fs.deleted, art.Path) {
		t.Fatal("晚到共享附件应按父级作用范围关联历史视频", second.Entries, fs.deleted)
	}
}

func TestMediaOrganizerCD2FailedScanRetriesAtConfiguredInterval(t *testing.T) {
	s, _, _, _, rule := newMediaCD2Organizer(t)
	rule.Enabled = true
	s.doc.Rules[0] = rule
	s.monitorRunning = true
	s.reconcileCD2MonitorsLocked()
	monitor := s.cd2Monitors[rule.ID]
	now := time.Now()
	monitor.lastFull, monitor.forceFull = now, false
	s.cd2ScanFailed(rule)
	if s.cd2ScheduleDueLocked(rule, now.Add(time.Second)) || !s.cd2ScheduleDueLocked(rule, now.Add(21*time.Second)) {
		t.Fatal("失败扫描应恢复补扫并按配置间隔重试")
	}
	monitor.forceFull, monitor.retryDue, monitor.status.Mode = false, time.Time{}, "polling"
	s.handleCD2Event(CD2Event{Mode: "polling", Rescan: true})
	if monitor.forceFull {
		t.Fatal("持续重连失败不应把 20 秒补查变成每 3 秒全量扫描")
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
