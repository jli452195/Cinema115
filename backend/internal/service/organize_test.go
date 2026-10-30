package service

import (
	"context"
	"fmt"
	"path"
	"testing"

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

func newOrganizeEngineTest(store accountSecretStore, accounts *P115Accounts, drive OrganizeDrive, metadata OrganizeMetadata) (*OrganizeEngine, error) {
	return NewOrganizeEngine(store, accounts, drive, metadata, organizeDefaultTemplates{})
}

func (organizeFakeMetadata) Details(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, Title: "测试作品", Date: "2024-01-01"}}, nil
}
func newOrganizeTest(t *testing.T) (*OrganizeEngine, *organizeFakeDrive, string, *failingAccountStore) {
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
	s, err := newOrganizeEngineTest(store, accounts, drive, organizeFakeMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	return s, drive, id, store
}

func organizeTestConfig(account string) OrganizeConfig {
	return OrganizeConfig{AccountID: account, SourceCID: "10", SourcePath: "/来源", TargetCID: "20", TargetPath: "/目标"}
}

// Build a trusted one-directory fixture. Production cloud scans belong to the
// download organizer; tests of the shared planner need only explicit source files.
func organizeTestFiles(files []p115.ShareFile, parent, prefix string) []OrganizeFile {
	out := make([]OrganizeFile, 0, len(files))
	for _, file := range files {
		if file.Directory || organizeMedia(file.Name) == "" {
			continue
		}
		item := OrganizeFile{ShareFile: file, ParentID: parent, SourcePath: path.Join(prefix, file.Name), MediaType: organizeMedia(file.Name), Season: 1}
		organizeFileNumbering(&item)
		item.NamingValues = mediaFileValues(file.Name, DownloadFileSpec{})
		out = append(out, item)
	}
	mediaDirectorySubtitleValues(out)
	return out
}

func organizeTestSource(t *testing.T, s *OrganizeEngine, account string) OrganizeScan {
	t.Helper()
	a, err := s.accounts.Get(account)
	if err != nil {
		t.Fatal(err)
	}
	drive := s.drive.(*organizeFakeDrive)
	return OrganizeScan{Config: organizeTestConfig(account), Revision: a.Revision,
		Files: organizeTestFiles(drive.dirs["30"], "30", path.Join("/来源", drive.dirs["10"][0].Name))}
}

func organizeTestPreview(t *testing.T, s *OrganizeEngine, account string, selections []OrganizeSelection) OrganizeJob {
	t.Helper()
	scan := organizeTestSource(t, s, account)
	job, err := s.buildPlan(context.Background(), scan, OrganizePreviewInput{Kind: "tv", TMDBID: 42, Subtitles: true, Files: selections})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.checkPlan(context.Background(), &job, "fixture-cookie", true); err != nil {
		t.Fatal(err)
	}
	job, err = s.rememberPlan(job)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// Persist a trusted plan before invoking the shared batch executor directly.
// Submission idempotency and account guards are covered through DownloadOrganize.
func organizeTestPersist(t *testing.T, s *OrganizeEngine, plan OrganizeJob) (OrganizeJob, string) {
	t.Helper()
	a, err := s.accounts.Get(plan.Config.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	plan = cloneOrganizeJob(plan)
	plan.Status = "queued"
	doc := s.doc
	doc.Jobs = prependOrganizeJob(plan, doc.Jobs)
	if err := s.saveLocked(doc); err != nil {
		t.Fatal(err)
	}
	return plan, a.Cookie
}

func organizeTestRun(t *testing.T, s *OrganizeEngine, _ string, plan OrganizeJob) OrganizeJob {
	t.Helper()
	job, cookie := organizeTestPersist(t, s, plan)
	if err := s.runJob(context.Background(), &job, cookie); err != nil {
		t.Fatal(err)
	}
	job.Resumable = canContinueOrganize(job)
	return job
}
