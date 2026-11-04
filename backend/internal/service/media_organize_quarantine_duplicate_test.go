package service

import (
	"context"
	"errors"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"strm115/backend/internal/client/clouddrive2"
	"strm115/backend/internal/client/p115"
)

type mediaQuarantine115Drive struct{ *mediaCleanupDrive }

func (d *mediaQuarantine115Drive) CreateDirectory(ctx context.Context, parent, name, cookie string) (string, error) {
	id, err := d.mediaCleanupDrive.CreateDirectory(ctx, parent, name, cookie)
	if err == nil {
		d.paths[id] = path.Join(d.paths[parent], name)
	}
	return id, err
}

func TestMediaQuarantine115DuplicateRestartCorrectionAndLateReference(t *testing.T) {
	s, drive, rule := newMediaCleanupCloud(t)
	d := &mediaQuarantine115Drive{drive}
	s.drive = d
	rule.Failure, rule.FailureID, rule.DeleteDuplicates = "/failed", "40", true
	rule, err := s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	video := p115.ShareFile{ID: "500", Name: "Work.S01E01.mkv", Size: 10, SHA1: media115DuplicateHash}
	sub := p115.ShareFile{ID: "501", Name: "Work.S01E01.ass", Size: 11, SHA1: strings.Repeat("B", 40)}
	keeper, subKeeper := video, sub
	keeper.ID, keeper.Size, keeper.Name = "600", 20, "WORK.S01E01.MKV"
	subKeeper.ID, subKeeper.Size, subKeeper.Name = "601", 21, "WORK.S01E01.ASS"
	d.dirs["10"], d.dirs["40"] = []p115.ShareFile{video, sub}, []p115.ShareFile{keeper, subKeeper}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	for _, entry := range run.Entries {
		if entry.File.MediaType == "cleanup" {
			continue
		}
		if entry.Status == "done" || entry.Failure == nil || entry.Failure.State != "stored" || entry.Failure.Message != "" {
			t.Fatal("隔离去重完成后主任务仍失败，但不能遗留 moving", entry)
		}
		wantID, wantSize := "600", int64(20)
		if entry.File.MediaType == "subtitle" {
			wantID, wantSize = "601", 21
			if entry.VideoID != video.ID {
				t.Fatal("字幕任务不能换绑到 keeper", entry)
			}
		}
		if entry.Failure.FileID != wantID || entry.File.Size != wantSize || entry.File.SourcePath != entry.File.Name {
			t.Fatal("应保存实际 keeper 大小与身份，保留原关联键", entry)
		}
	}
	if !reflect.DeepEqual(d.deleted, []string{"500", "501"}) {
		t.Fatal("应仅回收两个原始来源", d.deleted)
	}
	store := s.db.(*testMediaOrganizeStore).source
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, s.accounts, d, s.reserve)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Retry(run.ID, MediaOrganizeCorrection{FileID: video.ID, Kind: "tv", TMDBID: 42}); err != nil {
		t.Fatal(err)
	}
	corrected, _ := restarted.Snapshot(run.ID)
	mediaTestProcess(t, restarted, &corrected, "retry")
	for _, entry := range corrected.Entries {
		if entry.File.MediaType == "cleanup" {
			continue
		}
		if entry.Status != "done" || entry.Failure != nil || entry.Result == nil || entry.Result.Action != "duplicate" {
			t.Fatal("纠正应移动保留文件并保留已核验的不同 FID 关系", entry)
		}
		if entry.File.MediaType == "subtitle" && (entry.File.ID != sub.ID || entry.VideoID != video.ID) || entry.File.MediaType == "video" && entry.File.ID != video.ID {
			t.Fatal("纠正不能改变任务或附件关联键", entry)
		}
	}
	if !reflect.DeepEqual(d.deleted, []string{"500", "501"}) || !reflect.DeepEqual(d.moveBatches, [][]string{{"600"}, {"601"}}) {
		t.Fatal("纠正只能移动 keeper，不能重复删除原始来源", d.deleted, d.moveBatches)
	}
	// 再次重启只保留成功映射，晚到附件仍须通过目标完整 SHA1 验证。
	restarted, err = NewMediaOrganizer(store, s.metadata, s.templates, s.classification, s.accounts, d, s.reserve)
	if err != nil {
		t.Fatal(err)
	}
	late, err := restarted.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	f, err := restarted.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	entries := []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "900", Name: "Work.S01E01.chs.ass", Size: 1}, ParentID: "10", SourcePath: "Work.S01E01.chs.ass", MediaType: "subtitle"}}}
	if err := restarted.add115References(context.Background(), f, &late, entries); err != nil || len(late.Entries) != 1 || !late.Entries[0].Reference || late.Entries[0].File.ID != video.ID {
		t.Fatal("成功 keeper 应能验证旧任务的视频引用", late.Entries, err)
	}
}

func TestMediaQuarantineCD2DuplicateRestartCorrection(t *testing.T) {
	s, recognition, store, connection, rule := newMediaCD2Organizer(t)
	recognition.items = nil
	rule.Failure, rule.DeleteDuplicates = "/其他网盘/failed", true
	rule, err := s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	fs := connection.filesystem
	fs.changeIDs = true
	video := fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 10)
	sub := fs.add(path.Join(rule.Source, "Work.S01E01.ass"), false, 11)
	for _, source := range []clouddrive2.File{video, sub} {
		keeper := fs.add(path.Join(rule.Failure, source.Name), false, source.Size+10)
		keeper.Hashes = maps.Clone(source.Hashes)
		fs.files[keeper.Path] = keeper
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	for _, entry := range run.Entries {
		if entry.Status == "done" || entry.Failure == nil || entry.Failure.State != "stored" || entry.Failure.Message != "" || entry.CD2Current == nil || entry.CD2Duplicate {
			t.Fatal("CD2 隔离去重不能保留 moving 或最终入库去重标记", entry)
		}
		keeper := fs.files[path.Join(rule.Failure, entry.File.SourcePath)]
		if entry.CD2Current.FileID != keeper.ID || entry.Failure.CD2FileID != keeper.ID || entry.File.Size != keeper.Size || entry.Failure.FileID != "" || entry.File.ID != path.Join(rule.Source, entry.File.SourcePath) {
			t.Fatal("CD2 原生身份独立保存，不能当作 115 CID 或覆盖任务键", entry)
		}
	}
	deletes := append([]string(nil), fs.deleted...)
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.cd2 = connection
	if _, err := restarted.Retry(run.ID, MediaOrganizeCorrection{FileID: video.Path, Kind: "tv", TMDBID: 42}); err != nil {
		t.Fatal(err)
	}
	corrected, _ := restarted.Snapshot(run.ID)
	mediaTestProcess(t, restarted, &corrected, "retry")
	for _, entry := range corrected.Entries {
		if entry.Status != "done" || entry.Failure != nil || entry.CD2Current == nil || entry.CD2Current.Path != entry.Target || !strings.HasPrefix(entry.CD2Current.FileID, "跨云-") {
			t.Fatal("纠正后应保留跨云写入得到的新原生身份", entry)
		}
		if entry.File.MediaType == "subtitle" && (entry.File.ID != sub.Path || entry.VideoID != video.Path) || entry.File.MediaType == "video" && entry.File.ID != video.Path {
			t.Fatal("CD2 纠正不能改变原任务和字幕关联", entry)
		}
	}
	if !reflect.DeepEqual(deletes, fs.deleted) || corrected.Rule.ConnectionRevision != rule.ConnectionRevision || corrected.Rule.AccountID != "" {
		t.Fatal("重试不能重复删除或改变冻结存储绑定", fs.deleted, corrected.Rule)
	}
}

func TestMediaQuarantine115MovingDuplicateRecoversFromFacts(t *testing.T) {
	for _, scenario := range []string{"missing-source", "applied-delete-error"} {
		t.Run(scenario, func(t *testing.T) {
			s, d, f, entry, keeper := newMedia115DuplicateTest(t)
			f.rule.Failure, f.rule.FailureID = "/failed", "40"
			keeper.Name = entry.File.Name
			d.dirs["40"] = []p115.ShareFile{keeper}
			entry.Status, entry.Message = "needs_match", "未匹配作品"
			entry.Failure = &MediaOrganizeFailure{State: "moving", ParentID: "40", Reason: entry.Message, Message: "无关旧文案"}
			run, err := s.Start(f.rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			run.Rule, run.Status, run.Entries = f.rule, "partial", []MediaOrganizeEntry{entry}
			if scenario == "missing-source" {
				d.dirs["10"] = nil
			} else {
				d.deleteErr, d.applyOnError = context.DeadlineExceeded, true
				if _, err := s.quarantineEntry(context.Background(), f, &run, 0, true); err != nil || run.Entries[0].Failure.State != "moving" {
					t.Fatal("删除确认失败应保持未完成", run.Entries, err)
				}
				d.deleteErr = nil
			}
			deletes := len(d.deleted)
			if _, err := s.quarantineEntry(context.Background(), f, &run, 0, true); err != nil || run.Entries[0].Failure.State != "stored" || run.Entries[0].Failure.FileID != keeper.ID || run.Entries[0].Failure.Message != "" {
				t.Fatal("明确重试应依据目标和来源事实恢复", run.Entries, err)
			}
			if len(d.deleted) != deletes || run.Entries[0].Status == "done" {
				t.Fatal("隔离恢复不能再次删除或把主视频记为成功", d.deleted, run.Entries)
			}
		})
	}
}

func TestMediaQuarantineCD2MissingSourceDoesNotAcceptRenamedPathIdentity(t *testing.T) {
	_, _, _, connection, rule := newMediaCD2Organizer(t)
	rule.Failure = "/其他网盘/failed"
	fs := connection.filesystem
	file := fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 10)
	file.ID = file.Path
	fs.files[file.Path] = file
	keeper := fs.add(path.Join(rule.Failure, file.Name), false, 20)
	keeper.Hashes = maps.Clone(file.Hashes)
	fs.files[keeper.Path] = keeper
	entry := MediaOrganizeEntry{File: OrganizeFile{ShareFile: mediaCD2ShareFile(file), ParentID: rule.Source, SourcePath: file.Name, MediaType: "video"}, CD2FileID: file.ID, CD2Hashes: maps.Clone(file.Hashes), Failure: &MediaOrganizeFailure{State: "moving"}, Relative: file.Name}
	delete(fs.files, file.Path)
	file.Name, file.Path = "renamed.mkv", path.Join(rule.Source, "renamed.mkv")
	file.ID = file.Path
	fs.files[file.Path] = file
	f := &mediaOrganizeFiles{rule: rule, cd2: fs}
	f.rule.Failure, f.rule.Target, f.rule.DeleteDuplicates = path.Dir(keeper.Path), path.Dir(keeper.Path), true
	if err := f.moveCD2(context.Background(), entry); err == nil || errors.Is(err, errMediaDuplicateDone) || len(fs.deleted) != 0 {
		t.Fatal("路径型 ID 改名后的来源不能误认为已经删除", err, fs.deleted)
	}
}

func TestMediaDuplicateReceiptPreservesErrorsIs(t *testing.T) {
	if !errors.Is(&mediaDuplicateReceipt{fileID: "keeper", size: 1}, errMediaDuplicateDone) {
		t.Fatal("去重回执必须兼容现有成功分支")
	}
}

func TestMediaQuarantine115StoredKeeperRecoversAppliedMoveWithoutOldDirectories(t *testing.T) {
	_, d, f, entry, keeper := newMedia115DuplicateTest(t)
	f.rule.Failure, f.rule.FailureID = "/failed", "40"
	d.dirs["10"], d.dirs["20"] = nil, nil
	keeper.Name = entry.File.Name
	d.dirs["40"] = []p115.ShareFile{keeper}
	entry.File.Size = keeper.Size
	entry.Failure = &MediaOrganizeFailure{State: "stored", ParentID: "40", FileID: keeper.ID}
	d.moveErr, d.applyMove = context.DeadlineExceeded, true
	if err := f.move(context.Background(), entry); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("移动响应失败必须保留原始错误", err)
	}
	d.moveErr = nil
	delete(d.paths, "10")
	delete(d.paths, "40")
	if err := f.move(context.Background(), entry); !errors.Is(err, errMediaDuplicateDone) {
		t.Fatal("精确目标已含已保存 keeper 时不能再依赖旧目录", err)
	}
	if len(d.deleted) != 0 || d.moves != 1 || entry.File.ID != "500" || entry.File.SourcePath != "Work.S01E01.mkv" {
		t.Fatal("恢复不能重复写入或改变原任务键", d.deleted, d.moves, entry)
	}
}

func TestMediaQuarantineCD2MovingDuplicateRecoversFromFacts(t *testing.T) {
	for _, scenario := range []string{"missing-source", "applied-delete-error"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _, connection, rule := newMediaCD2Organizer(t)
			rule.Failure, rule.DeleteDuplicates = "/其他网盘/failed", true
			fs := &mediaAttachmentDeleteFake{mediaCD2Fake: connection.filesystem}
			source := fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 10)
			keeper := fs.add(path.Join(rule.Failure, source.Name), false, 20)
			keeper.Hashes = maps.Clone(source.Hashes)
			fs.files[keeper.Path] = keeper
			entry := MediaOrganizeEntry{File: OrganizeFile{ShareFile: mediaCD2ShareFile(source), ParentID: rule.Source, SourcePath: source.Name, MediaType: "video"}, CD2FileID: source.ID, CD2Hashes: maps.Clone(source.Hashes), Status: "needs_match", Failure: &MediaOrganizeFailure{State: "moving", Reason: "未匹配作品", Message: "任意旧文案"}}
			run, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			run.Rule, run.Status, run.Entries = rule, "partial", []MediaOrganizeEntry{entry}
			f := &mediaOrganizeFiles{rule: rule, cd2: fs}
			s.bindCD2Checkpoint(f, &run)
			if scenario == "missing-source" {
				delete(fs.files, source.Path)
			} else {
				fs.deleteErr = context.DeadlineExceeded
				if _, err := s.quarantineEntry(context.Background(), f, &run, 0, true); err != nil || run.Entries[0].Failure.State != "moving" {
					t.Fatal("删除响应失败应保持 moving", run.Entries, err)
				}
				fs.deleteErr = nil
			}
			deletes := fs.deleteCalls
			if _, err := s.quarantineEntry(context.Background(), f, &run, 0, true); err != nil || run.Entries[0].Failure.State != "stored" || run.Entries[0].Failure.Message != "" || run.Entries[0].CD2Current == nil || run.Entries[0].CD2Current.FileID != keeper.ID || run.Entries[0].File.Size != keeper.Size {
				t.Fatal("CD2 明确重试应凭来源缺失和完整保留目标恢复", run.Entries, err)
			}
			if fs.deleteCalls != deletes || run.Entries[0].Status == "done" || run.Entries[0].File.ID != source.Path {
				t.Fatal("恢复不得重复删除或改变任务结果与关联键", fs.deleted, run.Entries)
			}
		})
	}
}

func TestMediaQuarantineCD2OtherSameContentSourceIsIndependent(t *testing.T) {
	_, _, _, connection, rule := newMediaCD2Organizer(t)
	rule.Failure, rule.Target, rule.DeleteDuplicates = "/其他网盘/failed", "/其他网盘/failed", true
	fs := connection.filesystem
	source := fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 10)
	other := fs.add(path.Join(rule.Source, "independent.mkv"), false, 10)
	other.Hashes = maps.Clone(source.Hashes)
	fs.files[other.Path] = other
	keeper := fs.add(path.Join(rule.Target, source.Name), false, 20)
	keeper.Hashes = maps.Clone(source.Hashes)
	fs.files[keeper.Path] = keeper
	entry := MediaOrganizeEntry{File: OrganizeFile{ShareFile: mediaCD2ShareFile(source), ParentID: rule.Source, SourcePath: source.Name, MediaType: "video"}, CD2FileID: source.ID, CD2Hashes: maps.Clone(source.Hashes), Relative: source.Name}
	f := &mediaOrganizeFiles{rule: rule, cd2: fs}
	if err := f.moveCD2(context.Background(), entry); !errors.Is(err, errMediaDuplicateDone) || fs.files[other.Path].ID != other.ID || !reflect.DeepEqual(fs.deleted, []string{source.Path}) {
		t.Fatal("删除结果只核对指定来源，其他独立同内容文件不应阻塞或被删除", err, fs.deleted)
	}
}

func TestMediaQuarantineCD2FinalDuplicateRestartAndLateAttachment(t *testing.T) {
	s, recognition, store, connection, rule := newMediaCD2Organizer(t)
	recognition.items = nil
	rule.Failure, rule.DeleteDuplicates = "/其他网盘/failed", true
	rule, err := s.SaveRule(rule)
	if err != nil {
		t.Fatal(err)
	}
	fs := connection.filesystem
	source := fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 10)
	quarantineKeeper := fs.add(path.Join(rule.Failure, source.Name), false, 20)
	quarantineKeeper.Hashes = maps.Clone(source.Hashes)
	fs.files[quarantineKeeper.Path] = quarantineKeeper
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	if len(run.Entries) != 1 || run.Entries[0].Failure == nil || run.Entries[0].Failure.State != "stored" {
		t.Fatal("隔离去重必须先记录保留位置", run.Entries)
	}
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: source.Path, Kind: "tv", TMDBID: 42}); err != nil {
		t.Fatal(err)
	}
	corrected, _ := s.Snapshot(run.ID)
	files, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	defer files.close()
	if err := s.recognize(context.Background(), &corrected, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.render(context.Background(), files, &corrected); err != nil {
		t.Fatal(err)
	}
	relative := corrected.Entries[0].Relative
	parts := strings.Split(path.Dir(relative), "/")
	parent := rule.Target
	for _, part := range parts {
		if part != "." && part != "" {
			parent = path.Join(parent, part)
			fs.add(parent, true, 0)
		}
	}
	finalKeeper := fs.add(path.Join(parent, strings.ToUpper(path.Base(relative))), false, 30)
	finalKeeper.Hashes = maps.Clone(source.Hashes)
	fs.files[finalKeeper.Path] = finalKeeper
	delete(s.actions, run.ID)
	s.bindCD2Checkpoint(files, &corrected)
	if err := s.execute(context.Background(), files, &corrected); err != nil {
		t.Fatal(err)
	}
	entry := corrected.Entries[0]
	if entry.Status != "done" || entry.Result == nil || entry.Result.Action != "duplicate" || entry.File.ID != source.Path || entry.File.Size != finalKeeper.Size || entry.CD2Current == nil || entry.CD2Current.FileID != finalKeeper.ID || entry.CD2Current.Path != finalKeeper.Path || entry.Failure != nil {
		t.Fatal("最终去重结果应采用第三份保留文件的实际身份、位置和大小", entry)
	}
	deletes := slices.Clone(fs.deleted)
	if !slices.Equal(deletes, []string{source.Path, quarantineKeeper.Path}) {
		t.Fatal("只能回收指定来源与隔离 keeper，不能回收最终 keeper", fs.deleted)
	}
	if err := files.move(context.Background(), entry); !errors.Is(err, errMediaDuplicateDone) || !slices.Equal(deletes, fs.deleted) {
		t.Fatal("再次核验已保存的大小写目标必须保留去重结果且不重复删除", err, fs.deleted)
	}
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, s.classification, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.cd2 = connection
	late := fs.add(path.Join(rule.Source, "Work.S01E01.chs.ass"), false, 8)
	next, err := restarted.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	fs.statCalls = nil
	mediaTestProcess(t, restarted, &next, "auto")
	var video, subtitle *MediaOrganizeEntry
	for i := range next.Entries {
		if next.Entries[i].Reference && next.Entries[i].File.ID == source.Path {
			video = &next.Entries[i]
		}
		if next.Entries[i].File.ID == late.Path {
			subtitle = &next.Entries[i]
		}
	}
	if video == nil || video.CD2Current == nil || video.CD2Current.FileID != finalKeeper.ID || video.CD2Current.Path != finalKeeper.Path || video.File.Size != finalKeeper.Size || subtitle == nil || subtitle.Status != "done" || subtitle.VideoID != source.Path || path.Dir(subtitle.Target) != parent {
		t.Fatal("重启后晚到字幕应按旧任务键关联最终 keeper 的实际位置", next.Entries)
	}
	if !slices.Contains(fs.statCalls, finalKeeper.Path) || !slices.Equal(deletes, fs.deleted) {
		t.Fatal("引用必须读取实际大小写 keeper 路径且不得回收晚到字幕", fs.statCalls, fs.deleted)
	}
}
