package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"strm115/backend/internal/client/clouddrive2"
)

type mediaCD2BatchFake struct {
	*mediaCD2Fake
	renames          [][]clouddrive2.RenameOperation
	moves            [][]string
	destinations     []string
	singleRenames    []string
	singleMoves      [][]string
	renameFailures   map[string]error
	renameUncertain  map[string]error
	moveFailures     map[string]error
	preserveModified bool
	afterRename      func()
	beforeMove       func([]string)
}

func (f *mediaCD2BatchFake) Rename(ctx context.Context, source, name string) (string, error) {
	f.singleRenames = append(f.singleRenames, source)
	return f.mediaCD2Fake.Rename(ctx, source, name)
}

func (f *mediaCD2BatchFake) Move(ctx context.Context, sources []string, destination string) ([]string, error) {
	f.singleMoves = append(f.singleMoves, slices.Clone(sources))
	return f.mediaCD2Fake.Move(ctx, sources, destination)
}

func (f *mediaCD2BatchFake) RenameMany(ctx context.Context, operations []clouddrive2.RenameOperation) ([]clouddrive2.BatchFileResult, error) {
	f.renames = append(f.renames, slices.Clone(operations))
	results := make([]clouddrive2.BatchFileResult, 0, len(operations))
	failures := make([]error, 0)
	for _, operation := range operations {
		result := clouddrive2.BatchFileResult{Source: operation.Source, Target: path.Join(path.Dir(operation.Source), operation.Name)}
		before := f.files[operation.Source]
		if err := f.renameFailures[operation.Source]; err != nil {
			delete(f.renameFailures, operation.Source)
			result.Err = err
		} else {
			_, result.Err = f.mediaCD2Fake.Rename(ctx, operation.Source, operation.Name)
			if result.Err == nil {
				if f.preserveModified {
					file := f.files[result.Target]
					file.Modified = before.Modified
					f.files[result.Target] = file
				}
				if err := f.renameUncertain[operation.Source]; err != nil {
					delete(f.renameUncertain, operation.Source)
					result.Err = err
				} else {
					result.File, result.Err = f.Stat(ctx, result.Target, true)
				}
			}
		}
		if result.Err != nil {
			failures = append(failures, result.Err)
		}
		results = append(results, result)
	}
	if f.afterRename != nil {
		f.afterRename()
	}
	return results, errors.Join(failures...)
}

func (f *mediaCD2BatchFake) MoveMany(ctx context.Context, sources []string, destination string) ([]clouddrive2.BatchFileResult, error) {
	f.moves = append(f.moves, slices.Clone(sources))
	f.destinations = append(f.destinations, destination)
	if f.beforeMove != nil {
		f.beforeMove(sources)
	}
	results := make([]clouddrive2.BatchFileResult, 0, len(sources))
	failures := make([]error, 0)
	for _, source := range sources {
		result := clouddrive2.BatchFileResult{Source: source, Target: path.Join(destination, path.Base(source))}
		before := f.files[source]
		if err := f.moveFailures[source]; err != nil {
			delete(f.moveFailures, source)
			result.Err = err
		} else {
			_, result.Err = f.mediaCD2Fake.Move(ctx, []string{source}, destination)
			if result.Err == nil {
				if f.preserveModified {
					file := f.files[result.Target]
					file.Modified = before.Modified
					f.files[result.Target] = file
				}
				result.File, result.Err = f.Stat(ctx, result.Target, true)
			}
		}
		if result.Err != nil {
			failures = append(failures, result.Err)
		}
		results = append(results, result)
	}
	return results, errors.Join(failures...)
}

type mediaCD2BatchAcquirer struct{ filesystem *mediaCD2BatchFake }

func (a mediaCD2BatchAcquirer) Acquire(ctx context.Context, id string, revision uint64) (*CD2Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id != "independent-cd2" || revision != 3 {
		return nil, ErrMediaOrganizeInput
	}
	return &CD2Lease{ID: id, Revision: revision, Client: a.filesystem, release: func() error { return nil }}, nil
}

func newMediaCD2BatchOrganizer(t *testing.T) (*MediaOrganizer, *mediaCD2BatchFake, *failingAccountStore, MediaOrganizeRule) {
	t.Helper()
	s, _, store, connection, rule := newMediaCD2Organizer(t)
	fs := &mediaCD2BatchFake{
		mediaCD2Fake:   connection.filesystem,
		renameFailures: make(map[string]error), renameUncertain: make(map[string]error), moveFailures: make(map[string]error),
	}
	s.cd2 = mediaCD2BatchAcquirer{filesystem: fs}
	return s, fs, store, rule
}

func mediaCD2BatchEntry(fs *mediaCD2BatchFake, rule MediaOrganizeRule, source, relative string) MediaOrganizeEntry {
	full := path.Join(rule.Source, source)
	if parent := path.Dir(full); parent != rule.Source {
		if _, exists := fs.files[parent]; !exists {
			fs.add(parent, true, 0)
		}
	}
	file := fs.add(full, false, 128)
	entry := mediaCD2Entry(file, rule.Source, relative)
	entry.Status, entry.Target, entry.Group = "ready", path.Join(rule.Target, relative), "work"
	return entry
}

func mediaCD2BatchRun(t *testing.T, s *MediaOrganizer, rule MediaOrganizeRule, entries ...MediaOrganizeEntry) MediaOrganizeRun {
	t.Helper()
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Entries = entries
	run.Groups = []MediaOrganizeGroup{{ID: "work", Kind: "tv", TMDBID: 42, Title: "作品"}}
	if err := s.checkpoint(&run); err != nil {
		t.Fatal(err)
	}
	return run
}

func mediaCD2BatchPersistedRun(t *testing.T, store *failingAccountStore, id string) MediaOrganizeRun {
	t.Helper()
	var doc mediaOrganizeDocument
	if err := json.Unmarshal([]byte(store.Value()), &doc); err != nil {
		t.Fatal(err)
	}
	for _, run := range doc.Runs {
		if run.ID == id {
			return run
		}
	}
	t.Fatal("持久记录中缺少整理批次", id)
	return MediaOrganizeRun{}
}

func mediaCD2BatchAssertDone(t *testing.T, fs *mediaCD2BatchFake, run MediaOrganizeRun) {
	t.Helper()
	for _, entry := range run.Entries {
		file, exists := fs.files[entry.Target]
		if entry.Status != "done" || !exists || entry.CD2Current == nil || entry.CD2Current.Path != entry.Target || entry.CD2Current.FileID != file.ID {
			t.Fatal("成功结果必须保留目标文件及实际位置", entry, file)
		}
		if _, exists := fs.files[path.Join(run.Rule.Source, entry.File.SourcePath)]; exists {
			t.Fatal("整理成功后来源仍存在", entry)
		}
	}
}

func TestMediaOrganizerCD2BatchGroupsSeasonAndSplitsTwenty(t *testing.T) {
	s, fs, store, rule := newMediaCD2BatchOrganizer(t)
	entries := make([]MediaOrganizeEntry, 0, 26)
	for episode := 1; episode <= 23; episode++ {
		entries = append(entries, mediaCD2BatchEntry(fs, rule, fmt.Sprintf("original-%02d.mkv", episode), fmt.Sprintf("Work/Season 01/S01E%02d.mkv", episode)))
		if episode <= 3 {
			entries = append(entries, mediaCD2BatchEntry(fs, rule, fmt.Sprintf("extra-%02d.mkv", episode), fmt.Sprintf("Work/Season 02/S02E%02d.mkv", episode)))
		}
	}
	run := mediaCD2BatchRun(t, s, rule, entries...)
	if err := s.process(context.Background(), &run, "execute"); err != nil {
		t.Fatal(err)
	}
	if run.Status != "completed" || len(fs.moves) != 3 || len(fs.renames) != 3 || len(fs.singleMoves)+len(fs.singleRenames) != 0 {
		t.Fatal("普通整季文件应走批量接口", run.Status, fs.moves, fs.renames, fs.singleMoves, fs.singleRenames)
	}
	wantSizes := []int{20, 3, 3}
	wantDirectories := []string{path.Join(rule.Target, "Work/Season 01"), path.Join(rule.Target, "Work/Season 01"), path.Join(rule.Target, "Work/Season 02")}
	for i, batch := range fs.moves {
		if len(batch) != wantSizes[i] || len(fs.renames[i]) != wantSizes[i] || fs.destinations[i] != wantDirectories[i] {
			t.Fatal("批次必须按实际目标目录分组，超过二十项拆批", fs.moves, fs.destinations)
		}
	}
	mediaCD2BatchAssertDone(t, fs, mediaCD2BatchPersistedRun(t, store, run.ID))
}

func TestMediaOrganizerCD2BatchVideoFailureRetainsSubtitle(t *testing.T) {
	s, fs, store, rule := newMediaCD2BatchOrganizer(t)
	video := mediaCD2BatchEntry(fs, rule, "original.mkv", "Work/Season 01/S01E01.mkv")
	subtitle := mediaCD2BatchEntry(fs, rule, "original.ass", "Work/Season 01/S01E01.ass")
	other := mediaCD2BatchEntry(fs, rule, "other.mkv", "Work/Season 01/S01E02.mkv")
	subtitle.File.MediaType, subtitle.VideoID = "subtitle", video.File.ID
	fs.renameFailures[video.File.ID] = errors.New("模拟视频重命名失败")
	run := mediaCD2BatchRun(t, s, rule, video, subtitle, other)
	if err := s.process(context.Background(), &run, "execute"); err != nil {
		t.Fatal(err)
	}
	saved := mediaCD2BatchPersistedRun(t, store, run.ID)
	if saved.Status != "partial" || len(fs.renames) != 1 || len(fs.renames[0]) != 2 || len(fs.moves) != 1 || len(fs.moves[0]) != 1 || len(fs.singleMoves)+len(fs.singleRenames) != 0 {
		t.Fatal("视频失败后字幕不得触发任何写入", saved, fs.renames, fs.moves)
	}
	byID := make(map[string]MediaOrganizeEntry, len(saved.Entries))
	for _, entry := range saved.Entries {
		byID[entry.File.ID] = entry
	}
	for _, id := range []string{video.File.ID, subtitle.File.ID} {
		entry, exists := byID[id]
		if !exists {
			t.Fatal("失败视频和依赖字幕必须保留", id, saved.Entries)
		}
		if entry.Status != "error" || entry.CD2Current != nil {
			t.Fatal("失败及字幕依赖结果应持久保留", entry)
		}
		if _, exists := fs.files[entry.File.ID]; !exists {
			t.Fatal("视频或字幕来源被误移动", entry)
		}
	}
	if !strings.Contains(byID[subtitle.File.ID].Message, "对应视频尚未整理成功") {
		t.Fatal("字幕未说明视频依赖失败", byID[subtitle.File.ID])
	}
	completed := byID[other.File.ID]
	if completed.Status != "done" || !completed.Reference || completed.Result == nil || completed.Result.Status != "done" || completed.Result.At.IsZero() || fs.moves[0][0] != path.Join(rule.Source, path.Base(other.Target)) {
		t.Fatal("同批无关视频应保留精简成功结果与附件关联映射", completed, fs.moves)
	}
}

func TestMediaOrganizerCD2BatchPartialResultsPersistAndRetryOnlyFailures(t *testing.T) {
	s, fs, store, rule := newMediaCD2BatchOrganizer(t)
	fs.pathIDs = true
	entries := make([]MediaOrganizeEntry, 0, 3)
	for episode := 1; episode <= 3; episode++ {
		entries = append(entries, mediaCD2BatchEntry(fs, rule, fmt.Sprintf("original-%d.mkv", episode), fmt.Sprintf("Work/Season 01/S01E%02d.mkv", episode)))
	}
	fs.renameFailures[entries[1].File.ID] = errors.New("模拟一个文件改名失败")
	thirdRenamed := path.Join(rule.Source, path.Base(entries[2].Target))
	fs.moveFailures[thirdRenamed] = errors.New("模拟一个文件移动失败")
	run := mediaCD2BatchRun(t, s, rule, entries...)
	fs.beforeMove = func(sources []string) {
		// 外部移动开始前，成功改名的位置必须已经落盘。
		saved := mediaCD2BatchPersistedRun(t, store, run.ID)
		for _, source := range sources {
			found := false
			for _, entry := range saved.Entries {
				found = found || entry.CD2Current != nil && entry.CD2Current.Path == source
			}
			if !found {
				t.Fatal("未保存改名位置就开始移动", source, saved.Entries)
			}
		}
	}
	if err := s.process(context.Background(), &run, "execute"); err != nil {
		t.Fatal(err)
	}
	fs.beforeMove = nil
	saved := mediaCD2BatchPersistedRun(t, store, run.ID)
	if saved.Status != "partial" || saved.Entries[0].Status != "done" || saved.Entries[1].Status != "error" || saved.Entries[1].CD2Current != nil || saved.Entries[2].Status != "error" || saved.Entries[2].CD2Current == nil || saved.Entries[2].CD2Current.Path != thirdRenamed || saved.Entries[2].CD2Current.FileID != thirdRenamed {
		t.Fatal("部分成功必须逐文件保存实际位置", saved)
	}
	completed := saved.Entries[0]
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.cd2 = mediaCD2BatchAcquirer{filesystem: fs}
	retry, err := restarted.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.process(context.Background(), &retry, "execute"); err != nil {
		t.Fatal(err)
	}
	if len(fs.renames) != 2 || len(fs.renames[1]) != 1 || fs.renames[1][0].Source != entries[1].File.ID || len(fs.moves) != 2 || len(fs.moves[1]) != 1 || len(fs.singleMoves) != 1 || !slices.Equal(fs.singleMoves[0], []string{thirdRenamed}) || len(fs.singleRenames) != 0 {
		t.Fatal("重试应只改名尚未改名项，并从已保存位置恢复移动", fs.renames, fs.moves, fs.singleMoves, fs.singleRenames)
	}
	final := mediaCD2BatchPersistedRun(t, store, run.ID)
	if !reflect.DeepEqual(final.Entries[0], completed) {
		t.Fatal("已成功文件被重试改写", completed, final.Entries[0])
	}
	mediaCD2BatchAssertDone(t, fs, final)
}

func TestMediaOrganizerCD2BatchUncertainRenameRecoversIntermediatePath(t *testing.T) {
	for _, tc := range []struct {
		name         string
		pathIDs      bool
		noHash       bool
		wantRecovery bool
	}{
		{name: "stable-id", wantRecovery: true},
		{name: "path-id-with-hash", pathIDs: true, wantRecovery: true},
		{name: "insufficient-identity", pathIDs: true, noHash: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, fs, store, rule := newMediaCD2BatchOrganizer(t)
			fs.pathIDs = tc.pathIDs
			entry := mediaCD2BatchEntry(fs, rule, "original.mkv", "Work/Season 01/S01E01.mkv")
			other := mediaCD2BatchEntry(fs, rule, "other.mkv", "Work/Season 01/S01E02.mkv")
			if tc.noHash {
				file := fs.files[entry.File.ID]
				file.Hashes, entry.CD2Hashes = nil, nil
				fs.files[file.Path] = file
			}
			fs.renameUncertain[entry.File.ID] = errors.New("模拟已改名但回执核对暂时失败")
			run := mediaCD2BatchRun(t, s, rule, entry, other)
			if err := s.process(context.Background(), &run, "execute"); err != nil {
				t.Fatal(err)
			}
			saved := mediaCD2BatchPersistedRun(t, store, run.ID)
			if saved.Entries[0].Status != "error" || saved.Entries[0].CD2Current != nil || saved.Entries[1].Status != "done" || len(fs.moves) != 1 || len(fs.moves[0]) != 1 || fs.moves[0][0] != path.Join(rule.Source, path.Base(other.Target)) {
				t.Fatal("未确认的改名不能继续移动，同批成功项应独立完成", saved, fs.moves)
			}
			renamed := path.Join(rule.Source, path.Base(entry.Target))
			if _, exists := fs.files[renamed]; !exists {
				t.Fatal("用例未模拟出实际已改名状态")
			}
			completed := saved.Entries[1]
			if err := s.process(context.Background(), &saved, "execute"); err != nil {
				t.Fatal(err)
			}
			if len(fs.renames) != 1 || len(fs.moves) != 1 || len(fs.singleRenames) != 0 {
				t.Fatal("重试必须核验中间路径，不得重复改名或重放成功项", fs.renames, fs.moves, fs.singleRenames)
			}
			final := mediaCD2BatchPersistedRun(t, store, run.ID)
			if !reflect.DeepEqual(final.Entries[1], completed) {
				t.Fatal("重试不应改写同批已完成文件", final.Entries[1], completed)
			}
			if tc.wantRecovery {
				if len(fs.singleMoves) != 1 || !slices.Equal(fs.singleMoves[0], []string{renamed}) {
					t.Fatal("确认中间路径身份后应只补做一次移动", fs.singleMoves)
				}
				mediaCD2BatchAssertDone(t, fs, final)
				return
			}
			if final.Entries[0].Status != "error" || final.Entries[0].CD2Current != nil || len(fs.singleMoves) != 0 {
				t.Fatal("无哈希且身份变化时应保留待核实，不能凭中间路径猜测", final, fs.singleMoves)
			}
			if _, exists := fs.files[renamed]; !exists {
				t.Fatal("身份未核实的文件应保留在原目录")
			}
		})
	}
}

func TestMediaOrganizerCD2BatchSameTargetUsesSHA1AndRetainsConflicts(t *testing.T) {
	s, fs, store, rule := newMediaCD2BatchOrganizer(t)
	rule.DeleteDuplicates = true
	var err error
	if rule, err = s.SaveRule(rule); err != nil {
		t.Fatal(err)
	}
	entries := make([]MediaOrganizeEntry, 0, 4)
	for i, hash := range []string{"aabbcc", "AABBCC", "different", ""} {
		entry := mediaCD2BatchEntry(fs, rule, fmt.Sprintf("source-%d/original.mkv", i), "Work/Season 01/S01E01.mkv")
		file := fs.files[entry.File.ID]
		file.Hashes = map[uint32]string{2: hash}
		if i == 1 {
			// 判重只比较双方现有 SHA1，不附加大小条件。
			file.Size, entry.File.Size = 256, 256
		}
		fs.files[file.Path] = file
		entry.CD2Hashes = map[uint32]string{2: hash}
		entries = append(entries, entry)
	}
	run := mediaCD2BatchRun(t, s, rule, entries...)
	if err := s.process(context.Background(), &run, "execute"); err != nil {
		t.Fatal(err)
	}
	saved := mediaCD2BatchPersistedRun(t, store, run.ID)
	if len(fs.moves) != 1 || len(fs.moves[0]) != 1 || len(fs.singleMoves) != 0 || !slices.Equal(fs.deleted, []string{entries[1].File.ID}) {
		t.Fatal("同一目标只应先移动一份，再串行删除相同SHA1来源", fs.moves, fs.singleMoves, fs.deleted, saved.Entries)
	}
	if saved.Entries[0].Status != "done" || saved.Entries[1].Status != "done" || saved.Entries[1].Result == nil || saved.Entries[1].Result.Action != "duplicate" || saved.Entries[2].Status != "conflict" || saved.Entries[3].Status != "conflict" {
		t.Fatal("重复与冲突结果不正确", saved.Entries)
	}
	if target := fs.files[entries[0].Target]; target.Hashes[2] != "aabbcc" {
		t.Fatal("已有目标被覆盖", target)
	}
	for _, entry := range entries[2:] {
		if _, exists := fs.files[entry.File.ID]; !exists {
			t.Fatal("SHA1不同或缺失的来源应保留", entry)
		}
	}
}

func TestMediaOrganizerCD2BatchNoHashUsesSameStorageReceipt(t *testing.T) {
	s, fs, store, rule := newMediaCD2BatchOrganizer(t)
	fs.pathIDs, fs.preserveModified = true, true
	entries := []MediaOrganizeEntry{
		mediaCD2BatchEntry(fs, rule, "original.mkv", "Work/Season 01/S01E01.mkv"),
		mediaCD2BatchEntry(fs, rule, "other.mkv", "Work/Season 01/S01E02.mkv"),
	}
	fs.add(path.Join(rule.Target, "Work"), true, 0)
	fs.add(path.Join(rule.Target, "Work/Season 01"), true, 0)
	for full, file := range fs.files {
		file.CloudName, file.CloudAccount = "其他网盘", "同一账号"
		file.Hashes = nil
		fs.files[full] = file
	}
	for i := range entries {
		entries[i].CD2Hashes = nil
	}
	run := mediaCD2BatchRun(t, s, rule, entries...)
	if err := s.process(context.Background(), &run, "execute"); err != nil {
		t.Fatal(err)
	}
	saved := mediaCD2BatchPersistedRun(t, store, run.ID)
	mediaCD2BatchAssertDone(t, fs, saved)
	if saved.Status != "completed" || len(fs.renames) != 1 || len(fs.renames[0]) != 2 || len(fs.moves) != 1 || len(fs.moves[0]) != 2 {
		t.Fatal("无哈希的同存储操作应接受已核验回执并保存改名后的身份", saved, fs.renames, fs.moves)
	}
	for _, entry := range saved.Entries {
		if entry.CD2Current.FileID != entry.Target {
			t.Fatal("未保存路径变化后的文件身份", entry)
		}
	}
}

func TestMediaOrganizerCD2BatchCheckpointFailureStopsBeforeMove(t *testing.T) {
	s, fs, store, rule := newMediaCD2BatchOrganizer(t)
	entry := mediaCD2BatchEntry(fs, rule, "original.mkv", "Work/Season 01/S01E01.mkv")
	other := mediaCD2BatchEntry(fs, rule, "other.mkv", "Work/Season 01/S01E02.mkv")
	run := mediaCD2BatchRun(t, s, rule, entry, other)
	wantErr := errors.New("模拟整理位置保存失败")
	fs.afterRename = func() { store.err = wantErr }
	err := s.process(context.Background(), &run, "execute")
	store.err = nil
	if !errors.Is(err, wantErr) || len(fs.renames) != 1 || len(fs.renames[0]) != 2 || len(fs.moves)+len(fs.singleMoves) != 0 {
		t.Fatal("改名位置保存失败后必须停止后续移动", err, fs.renames, fs.moves, fs.singleMoves)
	}
	for _, candidate := range []MediaOrganizeEntry{entry, other} {
		if _, exists := fs.files[path.Join(rule.Source, path.Base(candidate.Target))]; !exists {
			t.Fatal("已完成改名的来源应留在原目录等待核对", candidate)
		}
		if _, exists := fs.files[candidate.Target]; exists {
			t.Fatal("持久化失败后仍写入了目标", candidate)
		}
	}
	saved := mediaCD2BatchPersistedRun(t, store, run.ID)
	for _, candidate := range saved.Entries {
		if candidate.Status != "ready" || candidate.CD2Current != nil {
			t.Fatal("保存失败不得伪造成功回执", saved)
		}
	}
}

func TestMediaOrganizerCD2BatchSingleFileUsesDirectPath(t *testing.T) {
	s, fs, store, rule := newMediaCD2BatchOrganizer(t)
	entry := mediaCD2BatchEntry(fs, rule, "original.mkv", "Work/Season 01/S01E01.mkv")
	run := mediaCD2BatchRun(t, s, rule, entry)
	if err := s.process(context.Background(), &run, "execute"); err != nil {
		t.Fatal(err)
	}
	if len(fs.renames)+len(fs.moves) != 0 || !slices.Equal(fs.singleRenames, []string{entry.File.ID}) || len(fs.singleMoves) != 1 || !slices.Equal(fs.singleMoves[0], []string{path.Join(rule.Source, path.Base(entry.Target))}) {
		t.Fatal("零散单文件应直接改名移动，跳过批量预检", fs.renames, fs.moves, fs.singleRenames, fs.singleMoves)
	}
	mediaCD2BatchAssertDone(t, fs, mediaCD2BatchPersistedRun(t, store, run.ID))
}
