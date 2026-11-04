package service

import (
	"context"
	"path"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestMediaAttachment115SameNameFilesKeepOneAtTarget(t *testing.T) {
	s, drive, files, run := newMedia115BatchTest(t, 0)
	t.Cleanup(files.close)
	source := []p115.ShareFile{
		{ID: "501", Name: "tvshow.nfo", Size: 13},
		{ID: "502", Name: "tvshow.nfo", Size: 17},
		{ID: "503", Name: "tvshow.nfo", Size: 19},
	}
	drive.dirs["10"], drive.dirs["20"] = source, nil
	run.Groups = []MediaOrganizeGroup{{ID: "1", Kind: "tv", TMDBID: 42}}
	run.Entries = []MediaOrganizeEntry{{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "video", Name: "Work.S01E01.mkv"}, MediaType: "video"}, Group: "1", Relative: "Work.S01E01.mkv", Status: "done"}}
	for _, file := range source {
		run.Entries = append(run.Entries, MediaOrganizeEntry{File: OrganizeFile{ShareFile: file, ParentID: "10", SourcePath: file.Name, MediaType: "attachment"}, Group: "1", VideoID: "video", Relative: file.Name, Target: path.Join(run.Rule.Target, file.Name), Status: "ready"})
	}
	if err := s.prepareAttachmentChoices(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(run.Entries); i++ {
		if err := s.executeMediaEntry(context.Background(), files, run, i); err != nil {
			t.Fatal(err)
		}
	}
	if len(drive.dirs["10"]) != 0 || len(drive.dirs["20"]) != 1 || len(drive.deleted) != 2 || drive.renames != 0 {
		t.Fatal("同名附件应按 ID 移动一份到目标并回收其余副本", drive.dirs, drive.deleted, drive.renames)
	}
	for _, entry := range run.Entries {
		if entry.Status != "done" {
			t.Fatal("附件完成后不能残留同名等待或影响视频成功", entry)
		}
	}
}

func TestMedia115RenameStillRejectsSourceNameCollision(t *testing.T) {
	_, drive, files, run := newMedia115BatchTest(t, 0)
	t.Cleanup(files.close)
	source := p115.ShareFile{ID: "501", Name: "copy.nfo", Size: 13}
	drive.dirs["10"] = []p115.ShareFile{source, {ID: "502", Name: "tvshow.nfo", Size: 17}}
	drive.dirs["20"] = nil
	entry := MediaOrganizeEntry{File: OrganizeFile{ShareFile: source, ParentID: "10", SourcePath: source.Name, MediaType: "attachment"}, Relative: "tvshow.nfo", Target: path.Join(run.Rule.Target, "tvshow.nfo")}
	if err := files.move(context.Background(), entry); err == nil || drive.renames != 0 || drive.moves != 0 {
		t.Fatal("实际需要改名时仍应保留来源同名文件", err, drive.renames, drive.moves)
	}
}
