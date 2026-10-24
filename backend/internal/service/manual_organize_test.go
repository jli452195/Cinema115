package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/naming"
)

type organizeFakeDrive struct {
	dirs                    map[string][]p115.ShareFile
	cookies                 []string
	renames, moves, creates int
	moveErr, renameErr      error
	applyMove, applyRename  bool
	beforeRename            func()
	pageSize                int
	listCalls               map[string]int
	renameBatches           [][]p115.RenameTarget
	moveBatches             [][]string
	renameApplyIDs          map[string]bool
	moveApplyIDs            map[string]bool
	afterRename             func()
	afterMove               func()
	beforeRead              func(string, int) error
}

func (f *organizeFakeDrive) DriveFiles(ctx context.Context, cid string, offset int, cookie string) (*p115.SharePage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.cookies = append(f.cookies, cookie)
	if f.listCalls == nil {
		f.listCalls = make(map[string]int)
	}
	f.listCalls[cid]++
	if f.beforeRead != nil {
		if err := f.beforeRead(cid, offset); err != nil {
			return nil, err
		}
	}
	files, ok := f.dirs[cid]
	if !ok {
		return nil, fmt.Errorf("目录不存在")
	}
	if offset > len(files) {
		return nil, fmt.Errorf("分页无效")
	}
	end := len(files)
	if f.pageSize > 0 && offset+f.pageSize < end {
		end = offset + f.pageSize
	}
	return &p115.SharePage{Files: append([]p115.ShareFile{}, files[offset:end]...), Count: len(files), Offset: offset}, nil
}
func (f *organizeFakeDrive) CreateDirectory(_ context.Context, parent, name, cookie string) (string, error) {
	f.cookies = append(f.cookies, cookie)
	f.creates++
	id := fmt.Sprint(1000 + f.creates)
	f.dirs[parent] = append(f.dirs[parent], p115.ShareFile{ID: id, Name: name, Directory: true})
	f.dirs[id] = []p115.ShareFile{}
	return id, nil
}
func (f *organizeFakeDrive) RenameEntries(_ context.Context, entries []p115.RenameTarget, cookie string) error {
	f.cookies = append(f.cookies, cookie)
	f.renames++
	f.renameBatches = append(f.renameBatches, append([]p115.RenameTarget{}, entries...))
	if f.beforeRename != nil {
		f.beforeRename()
	}
	for _, entry := range entries {
		apply := f.renameErr == nil || f.applyRename
		if f.renameApplyIDs != nil {
			apply = f.renameApplyIDs[entry.ID]
		}
		if !apply {
			continue
		}
		for cid := range f.dirs {
			for i := range f.dirs[cid] {
				if f.dirs[cid][i].ID == entry.ID {
					f.dirs[cid][i].Name = entry.Name
				}
			}
		}
	}
	if f.afterRename != nil {
		f.afterRename()
	}
	return f.renameErr
}
func (f *organizeFakeDrive) MoveEntriesBatch(_ context.Context, ids []string, cid, cookie string) error {
	f.cookies = append(f.cookies, cookie)
	f.moves++
	f.moveBatches = append(f.moveBatches, append([]string{}, ids...))
	for _, id := range ids {
		apply := f.moveErr == nil || f.applyMove
		if f.moveApplyIDs != nil {
			apply = f.moveApplyIDs[id]
		}
		if !apply {
			continue
		}
		found := false
		for parent, files := range f.dirs {
			for i, file := range files {
				if file.ID == id {
					f.dirs[parent] = append(files[:i], files[i+1:]...)
					f.dirs[cid] = append(f.dirs[cid], file)
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	if f.afterMove != nil {
		f.afterMove()
	}
	return f.moveErr
}

type organizeFakeMetadata struct{}
type organizeDefaultTemplates struct{}

func (organizeDefaultTemplates) NamingTemplate(_ context.Context, kind naming.Kind) (naming.Snapshot, error) {
	template, err := naming.Default(kind)
	return naming.Snapshot{Template: template}, err
}

func newManualOrganizeTest(store accountSecretStore, accounts *P115Accounts, drive OrganizeDrive, metadata OrganizeMetadata) (*ManualOrganize, error) {
	engine, err := NewOrganizeEngine(store, accounts, drive, metadata, organizeDefaultTemplates{})
	if err != nil {
		return nil, err
	}
	return NewManualOrganize(engine), nil
}

func (organizeFakeMetadata) Details(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, Title: "测试作品", Date: "2024-01-01"}}, nil
}
func newOrganizeTest(t *testing.T) (*ManualOrganize, *organizeFakeDrive, string, *failingAccountStore) {
	t.Helper()
	accounts, err := newP115Accounts(&failingAccountStore{}, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := accounts.AddPrimary("整理账号")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_snapshot; SEID=secret-cookie"
	if err := accounts.Update(id, 1, "整理账号", &cookie); err != nil {
		t.Fatal(err)
	}
	drive := &organizeFakeDrive{dirs: map[string][]p115.ShareFile{
		"0":  {{ID: "10", Name: "来源", Directory: true}, {ID: "20", Name: "目标", Directory: true}},
		"10": {{ID: "30", Name: "待整理", Directory: true}},
		"20": {},
		"30": {{ID: "101", Name: "Wrong.S01E01.1080p.mkv", Size: 100}, {ID: "102", Name: "Wrong.S01E01.1080p.zh-CN.srt", Size: 10}, {ID: "103", Name: "Wrong.S01E02.1080p.mkv", Size: 200}, {ID: "104", Name: "readme.txt", Size: 1}},
	}}
	store := &failingAccountStore{}
	s, err := newManualOrganizeTest(store, accounts, drive, organizeFakeMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveConfig(OrganizeConfig{AccountID: id, SourceCID: "10", SourcePath: "/来源", TargetCID: "20", TargetPath: "/目标"}); err != nil {
		t.Fatal(err)
	}
	return s, drive, id, store
}
func organizeTestPreview(t *testing.T, s *ManualOrganize, account string, selections []OrganizeSelection) OrganizeJob {
	t.Helper()
	scan, err := s.Scan(context.Background(), OrganizeScanInput{AccountID: account, ParentID: "10", ParentPath: "/来源", EntryID: "30"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Preview(context.Background(), account, OrganizePreviewInput{ScanID: scan.ID, Kind: "tv", TMDBID: 42, Subtitles: true, Files: selections})
	if err != nil {
		t.Fatal(err)
	}
	return job
}
func organizeTestRun(t *testing.T, s *ManualOrganize, account string, plan OrganizeJob) OrganizeJob {
	t.Helper()
	if _, err := s.Start(account, plan.ID); err != nil {
		t.Fatal(err)
	}
	pending := s.pending
	if err := s.runJob(context.Background(), &pending.job, pending.cookie); err != nil {
		t.Fatal(err)
	}
	return s.History()[0]
}

func TestManualOrganizeCompletionNotifiesAfterReleasingWriter(t *testing.T) {
	s, _, id, _ := newOrganizeTest(t)
	plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	called := false
	s.OnCompleted = func(_ context.Context, config OrganizeConfig) {
		called = true
		if config.AccountID != id || s.History()[0].Status != "completed" {
			t.Error("notification preceded committed success")
		}
		release, err := s.ReserveDirectoryWork()
		if err != nil {
			t.Error("writer was not released", err)
		} else {
			release()
		}
		cancel()
	}
	if _, err := s.Start(id, plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("completion was not notified")
	}
}

func TestManualOrganizeMovieTVAndSubtitles(t *testing.T) {
	s, drive, id, store := newOrganizeTest(t)
	drive.pageSize = 1 // Includes all pages when scanning and checking collisions.
	plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}, {ID: "103", Season: 1, Episode: 2}})
	if drive.renames+drive.moves+drive.creates != 0 {
		t.Fatal("预览不能修改网盘")
	}
	if len(plan.Items) != 3 || plan.Items[2].Name != "测试作品.S01E01.1080p.zh-CN.srt" {
		t.Fatalf("字幕未正确匹配: %+v", plan.Items)
	}
	job := organizeTestRun(t, s, id, plan)
	if job.Status != "completed" || drive.moves != 1 || drive.renames != 1 {
		t.Fatalf("整理未完成: %+v", job)
	}
	if len(drive.dirs["30"]) != 1 || drive.dirs["30"][0].Name != "readme.txt" {
		t.Fatal("应保留未选择的文件和原文件夹")
	}
	if strings.Contains(store.data, "secret-cookie") || strings.Contains(store.data, "UID=") {
		t.Fatal("记录中出现凭据")
	}
	if _, err := s.Start(id, plan.ID); err != nil || drive.moves != 1 {
		t.Fatal("重复提交不得重复执行", err)
	}
	restarted, err := newManualOrganizeTest(store, s.accounts, drive, organizeFakeMetadata{})
	if err != nil || restarted.History()[0].Status != "completed" || restarted.Config().AccountID != id {
		t.Fatal("配置或记录未持久化", err)
	}

	s2, _, id2, _ := newOrganizeTest(t)
	scan, err := s2.Scan(context.Background(), OrganizeScanInput{AccountID: id2, ParentID: "10", ParentPath: "/来源", EntryID: "30"})
	if err != nil {
		t.Fatal(err)
	}
	movie, err := s2.Preview(context.Background(), id2, OrganizePreviewInput{ScanID: scan.ID, Kind: "movie", TMDBID: 42, Files: []OrganizeSelection{{ID: "101"}}})
	if err != nil || movie.Items[0].TargetPath != "/目标/测试作品 (2024) {tmdb-42}/测试作品 (2024).1080p.mkv" {
		t.Fatal(movie, err)
	}
}

func TestManualOrganizeExplicitAccountAndSnapshot(t *testing.T) {
	s, drive, id, _ := newOrganizeTest(t)
	if _, err := s.Browse(context.Background(), "", "0", 0); err == nil {
		t.Fatal("空账号不能回退唯一账号")
	}
	config := s.Config()
	config.AccountID = "secondary"
	if s.SaveConfig(config) == nil {
		t.Fatal("保存必须校验账号")
	}
	plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
	if _, err := s.Start(id, plan.ID); err != nil {
		t.Fatal(err)
	}
	old := s.pending.cookie
	newCookie := "UID=123_other; SEID=other-secret"
	if err := s.accounts.Update(id, 2, "修改账号", &newCookie); err != nil {
		t.Fatal(err)
	}
	config = s.Config()
	config.TargetCID = "0"
	config.TargetPath = "/"
	if err := s.SaveConfig(config); err != nil {
		t.Fatal(err)
	}
	drive.cookies = nil
	if err := s.runJob(context.Background(), &s.pending.job, s.pending.cookie); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range drive.cookies {
		if cookie != old {
			t.Fatal("任务中途切换了凭据")
		}
	}
	if s.History()[0].Config.TargetCID != "20" {
		t.Fatal("任务中途切换了目标目录")
	}
}

func TestManualOrganizeRejectChangedDeletedOrClearedAccount(t *testing.T) {
	for _, action := range []string{"changed", "deleted", "cleared"} {
		t.Run(action, func(t *testing.T) {
			s, drive, id, _ := newOrganizeTest(t)
			plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
			var err error
			switch action {
			case "deleted":
				err = s.accounts.Delete(id, 2)
			case "cleared":
				empty := ""
				err = s.accounts.Update(id, 2, "整理账号", &empty)
			default:
				err = s.accounts.Update(id, 2, "新名称", nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Start(id, plan.ID); err == nil {
				t.Fatal("应要求重新配置和扫描")
			}
			if drive.renames+drive.moves+drive.creates != 0 {
				t.Fatal("无效账号不得修改文件")
			}
		})
	}
}

func TestManualOrganizeConflictsAndStaleSource(t *testing.T) {
	t.Run("duplicate names", func(t *testing.T) {
		s, drive, id, _ := newOrganizeTest(t)
		plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}, {ID: "103", Season: 1, Episode: 1}})
		if plan.Items[0].Conflict == "" || plan.Items[1].Conflict == "" {
			t.Fatal("未发现目标重名")
		}
		if _, err := s.Start(id, plan.ID); err == nil || drive.moves != 0 {
			t.Fatal("冲突任务不能执行")
		}
	})
	t.Run("destination added after preview", func(t *testing.T) {
		s, drive, id, _ := newOrganizeTest(t)
		plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
		drive.dirs["20"] = []p115.ShareFile{{ID: "40", Name: strings.Split(plan.Items[0].Directory, "/")[0], Directory: true}}
		drive.dirs["40"] = []p115.ShareFile{{ID: "50", Name: "Season 01", Directory: true}}
		drive.dirs["50"] = []p115.ShareFile{{ID: "60", Name: plan.Items[0].Name}}
		job := organizeTestRun(t, s, id, plan)
		if job.Status != "partial" || drive.moves+drive.renames != 0 {
			t.Fatal("执行前未重新检查冲突")
		}
	})
	t.Run("source changed", func(t *testing.T) {
		s, drive, id, _ := newOrganizeTest(t)
		plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
		drive.dirs["30"][0].Name = "changed.mkv"
		job := organizeTestRun(t, s, id, plan)
		if job.Status != "partial" || drive.moves+drive.renames != 0 {
			t.Fatal("不能操作过期文件快照")
		}
	})
	t.Run("source rename collision", func(t *testing.T) {
		s, drive, id, _ := newOrganizeTest(t)
		plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
		drive.dirs["30"] = append(drive.dirs["30"], p115.ShareFile{ID: "60", Name: plan.Items[0].Name})
		job := organizeTestRun(t, s, id, plan)
		if job.Status != "partial" || drive.moves+drive.renames != 0 {
			t.Fatal("不能产生来源重名")
		}
	})
}

func TestManualOrganizeTimeoutReadbackAndRestart(t *testing.T) {
	for _, applied := range []bool{false, true} {
		t.Run(fmt.Sprint(applied), func(t *testing.T) {
			s, drive, id, store := newOrganizeTest(t)
			plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
			drive.moveErr, drive.applyMove = context.DeadlineExceeded, applied
			job := organizeTestRun(t, s, id, plan)
			if applied {
				if job.Status != "completed" {
					t.Fatal("读回确认成功后应完成", job)
				}
			} else if job.Status != "partial" || job.Items[0].Status != "uncertain" || drive.moves != 1 {
				t.Fatal("未确认的移动不能重试或继续", job)
			}
			raw, _ := json.Marshal(job)
			if strings.Contains(string(raw), "secret-cookie") || strings.Contains(store.data, "secret-cookie") {
				t.Fatal("凭据泄漏")
			}
		})
	}
	s, drive, id, store := newOrganizeTest(t)
	plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
	if _, err := s.Start(id, plan.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := newManualOrganizeTest(store, s.accounts, drive, organizeFakeMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if restarted.History()[0].Status != "interrupted" || restarted.pending != nil {
		t.Fatal("重启后不得自动重放操作")
	}
}

func TestManualOrganizeRejectForgedFilesAndEpisodes(t *testing.T) {
	s, _, id, _ := newOrganizeTest(t)
	scan, err := s.Scan(context.Background(), OrganizeScanInput{AccountID: id, ParentID: "10", ParentPath: "/来源", EntryID: "30"})
	if err != nil {
		t.Fatal(err)
	}
	for _, selection := range []OrganizeSelection{{ID: "999", Season: 1, Episode: 1}, {ID: "101", Season: 1, Episode: 0}, {ID: "101", Season: 1, Episode: 10001}, {ID: "101", Season: -1, Episode: 1}, {ID: "101", Season: 1001, Episode: 1}, {ID: "101", Season: 1, Episode: 2, EndEpisode: 1}, {ID: "101", Season: 1, Episode: 1, EndEpisode: 10001}} {
		if _, err := s.Preview(context.Background(), id, OrganizePreviewInput{ScanID: scan.ID, Kind: "tv", TMDBID: 42, Files: []OrganizeSelection{selection}}); err == nil {
			t.Fatal("接受了非法文件或季集", selection)
		}
	}
	config := s.Config()
	config.TargetCID = "30"
	config.TargetPath = "/来源/待整理"
	if err := s.SaveConfig(config); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(context.Background(), OrganizeScanInput{AccountID: id, ParentID: "10", ParentPath: "/来源", EntryID: "30"}); err == nil {
		t.Fatal("不能将目标放在所选目录内")
	}
}

func TestManualOrganizeCheckpointBeforeMutation(t *testing.T) {
	s, drive, id, store := newOrganizeTest(t)
	plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
	if _, err := s.Start(id, plan.ID); err != nil {
		t.Fatal(err)
	}
	store.err = errors.New("disk full")
	if err := s.runJob(context.Background(), &s.pending.job, s.pending.cookie); err == nil {
		t.Fatal("必须报告记录写入失败")
	}
	if drive.moves+drive.renames+drive.creates != 0 {
		t.Fatal("无法保存执行记录时不得修改网盘")
	}
}

func TestManualOrganizeWorkerStopsWithContext(t *testing.T) {
	s, _, _, _ := newOrganizeTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker 未退出")
	}
}

func TestManualOrganizeScansDefaultVideoFormats(t *testing.T) {
	s, drive, id, _ := newOrganizeTest(t)
	extensions := strings.Split(DefaultVideoExtensions, ",")
	drive.dirs["30"] = []p115.ShareFile{{ID: "104", Name: "readme.txt", Size: 1}}
	for i, ext := range extensions {
		drive.dirs["30"] = append(drive.dirs["30"], p115.ShareFile{ID: fmt.Sprint(200 + i), Name: "视频." + strings.ToUpper(ext), Size: 100})
	}
	scan, err := s.Scan(context.Background(), OrganizeScanInput{AccountID: id, ParentID: "10", ParentPath: "/来源", EntryID: "30"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Files) != len(extensions) {
		t.Fatalf("扫描遗漏默认视频格式: %+v", scan.Files)
	}
	for _, file := range scan.Files {
		if file.MediaType != "video" {
			t.Fatalf("错误的媒体类型: %+v", file)
		}
	}
}

func TestManualOrganizeRMVBWithSubtitles(t *testing.T) {
	for _, ext := range []string{"rmvb", "RMVB"} {
		for _, single := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/single=%t", ext, single), func(t *testing.T) {
				s, drive, id, _ := newOrganizeTest(t)
				drive.dirs["30"] = []p115.ShareFile{
					{ID: "101", Name: "閃靈二人組 - 01." + ext, Size: 100},
					{ID: "102", Name: "閃靈二人組 - 01.zh-CN.srt", Size: 10},
					{ID: "104", Name: "readme.txt", Size: 1},
				}
				parent, parentPath, entry := "10", "/来源", "30"
				if single {
					parent, parentPath, entry = "30", "/来源/待整理", "101"
				}
				scan, err := s.Scan(context.Background(), OrganizeScanInput{AccountID: id, ParentID: parent, ParentPath: parentPath, EntryID: entry})
				if err != nil {
					t.Fatal(err)
				}
				if len(scan.Files) != 2 || scan.Files[0].ID != "101" || scan.Files[0].MediaType != "video" || scan.Files[1].ID != "102" || scan.Files[1].MediaType != "subtitle" {
					t.Fatalf("RMVB 或配套字幕未被正确扫描: %+v", scan.Files)
				}
				plan, err := s.Preview(context.Background(), id, OrganizePreviewInput{ScanID: scan.ID, Kind: "tv", TMDBID: 42, Subtitles: true, Files: []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}}})
				if err != nil {
					t.Fatal(err)
				}
				if len(plan.Items) != 2 || plan.Items[0].Name != "测试作品.S01E01.rmvb" || plan.Items[1].Name != "测试作品.S01E01.zh-CN.srt" {
					t.Fatalf("RMVB 或字幕命名错误: %+v", plan.Items)
				}
				job := organizeTestRun(t, s, id, plan)
				if job.Status != "completed" || len(drive.dirs["30"]) != 1 || drive.dirs["30"][0].ID != "104" {
					t.Fatalf("RMVB 整理失败或影响未选文件: %+v", job)
				}
				files := drive.dirs[job.Items[0].TargetCID]
				if len(files) != 2 || files[0].ID != "101" || files[0].Size != 100 || files[0].Name != plan.Items[0].Name {
					t.Fatalf("整理后应保留原文件身份和格式: %+v", files)
				}
			})
		}
	}
}

func TestManualOrganizeSingleVideoAndAmbiguousSubtitles(t *testing.T) {
	s, drive, id, _ := newOrganizeTest(t)
	input := OrganizeScanInput{AccountID: id, ParentID: "30", ParentPath: "/来源/待整理", EntryID: "101"}
	scan, err := s.Scan(context.Background(), input)
	if err != nil || len(scan.Files) != 2 || scan.Files[1].ID != "102" {
		t.Fatal("单文件应只带上匹配字幕", scan, err)
	}
	drive.dirs["30"] = append(drive.dirs["30"], p115.ShareFile{ID: "105", Name: "Wrong.S01E01.1080p.mp4", Size: 100})
	scan, err = s.Scan(context.Background(), input)
	if err != nil || len(scan.Files) != 1 {
		t.Fatal("同名视频扩展名不同时不能猜测字幕所属", scan, err)
	}
}

func TestManualOrganizeScanScopeDoesNotChangeDefaults(t *testing.T) {
	for _, tc := range []struct{ name, parent, path, entry string }{
		{"subdirectory", "30", "/来源/待整理", "101"},
		{"history-source", "40", "/旧任务", "30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, drive, account, store := newOrganizeTest(t)
			drive.dirs["40"] = []p115.ShareFile{{ID: "30", Name: "待整理", Directory: true}}
			defaults, stored := s.Config(), store.Value()
			scan, err := s.Scan(context.Background(), OrganizeScanInput{AccountID: account, ParentID: tc.parent, ParentPath: tc.path, EntryID: tc.entry})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := s.Preview(context.Background(), account, OrganizePreviewInput{ScanID: scan.ID, Kind: "tv", TMDBID: 42, Subtitles: true, Files: []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			if scan.Config.SourceCID != tc.parent || scan.Config.SourcePath != tc.path || plan.Config != scan.Config || plan.Config.TargetCID != defaults.TargetCID || !strings.HasPrefix(plan.Items[0].File.SourcePath, tc.path+"/") {
				t.Fatal("scan lost its current source or saved target", scan, plan)
			}
			if s.Config() != defaults || store.Value() != stored || drive.creates+drive.renames+drive.moves != 0 {
				t.Fatal("scan or preview changed defaults or drive files")
			}
			job := organizeTestRun(t, s, account, plan)
			if job.Status != "completed" || s.Config() != defaults || job.Config != scan.Config {
				t.Fatal("execution changed defaults or lost the scan snapshot", job)
			}
		})
	}
}

func TestManualOrganizeRejectInvalidScanScope(t *testing.T) {
	for _, tc := range []struct{ account, parent, path, entry string }{
		{"", "30", "/来源/待整理", "101"},
		{"other", "30", "/来源/待整理", "101"},
		{"main", "-1", "/来源/待整理", "101"},
		{"main", "30", "", "101"},
		{"main", "30", "relative", "101"},
		{"main", "30", "/bad\npath", "101"},
		{"main", "30", "/来源/待整理", "0"},
	} {
		s, drive, account, _ := newOrganizeTest(t)
		if tc.account == "main" {
			tc.account = account
		}
		_, err := s.Scan(context.Background(), OrganizeScanInput{AccountID: tc.account, ParentID: tc.parent, ParentPath: tc.path, EntryID: tc.entry})
		if !errors.Is(err, ErrOrganizeInput) || len(drive.listCalls) != 0 {
			t.Fatal("invalid scan scope reached the drive", tc, err, drive.listCalls)
		}
	}
}

func TestManualOrganizePreviewExpiresAndRenamingTimeout(t *testing.T) {
	s, drive, id, _ := newOrganizeTest(t)
	plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
	expired := plan
	expired.CreatedAt = time.Now().Add(-31 * time.Minute)
	s.plans[plan.ID] = expired
	if _, err := s.Start(id, plan.ID); err == nil {
		t.Fatal("过期预览不能执行")
	}
	plan = organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
	drive.renameErr = fmt.Errorf("upstream echoed UID=123_snapshot; SEID=secret-cookie")
	job := organizeTestRun(t, s, id, plan)
	if job.Items[0].Status != "uncertain" || drive.moves != 0 || drive.renames != 1 {
		t.Fatal("重命名未确认时不能移动或重试", job)
	}
	if strings.Contains(job.Items[0].Message, "secret-cookie") {
		t.Fatal("上游错误泄漏凭据")
	}
}

func TestManualOrganizeConcurrentWorkerHistoryAndSettings(t *testing.T) {
	s, drive, id, _ := newOrganizeTest(t)
	plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
	if _, err := s.Start(id, plan.ID); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}, 1), make(chan struct{})
	drive.beforeRename = func() {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("worker 未退出")
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("worker 未开始")
	}
	for range 20 {
		history := s.History()
		if len(history) != 1 || history[0].Status != "running" {
			close(release)
			t.Fatal("执行记录缺失")
		}
		if err := s.SaveConfig(s.Config()); err != nil {
			close(release)
			t.Fatal(err)
		}
		if _, err := s.Start(id, plan.ID); err != nil {
			close(release)
			t.Fatal("重复请求应返回同一任务", err)
		}
	}
	close(release)
}
