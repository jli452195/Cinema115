package service

import (
	"context"
	"fmt"
	"path"
	"strm115/backend/internal/client/p115"
	"testing"
)

func TestMedia115PipelineMoreThanFiveThousandFilesFinishesSharedAttachments(t *testing.T) {
	s, drive, files, run, _ := newMedia115PipelineTest(t)
	drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "Work.S01E01.mkv", Size: 123}, {ID: "502", Name: "poster.png", Size: 8}}
	for directory := range 2 {
		id, name := fmt.Sprint(30+directory), fmt.Sprintf("kept-%d", directory)
		drive.paths[id] = path.Join("/inbox", name)
		drive.dirs["10"] = append(drive.dirs["10"], p115.ShareFile{ID: id, Name: name, Directory: true})
		for i := range 2500 {
			drive.dirs[id] = append(drive.dirs[id], p115.ShareFile{ID: fmt.Sprint(10000 + directory*2500 + i), Name: fmt.Sprintf("keep-%d.iso", i), Size: 123})
		}
	}
	if err := s.process115Auto(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "completed" || len(run.Entries) != 2 || files.cloudEmpty == nil || !files.cloudEmpty.complete {
		t.Fatal("超过五千个文件仍应完成整轮与共享附件", run.Status, len(run.Entries))
	}
	for _, entry := range run.Entries {
		if entry.Status != "done" {
			t.Fatal("共享附件未完成", entry)
		}
	}
	if len(drive.dirs["30"]) != 2500 || len(drive.dirs["31"]) != 2500 || len(drive.deleted) != 0 {
		t.Fatal("保留文件不能被清理")
	}
}

func TestMediaCD2PipelineMoreThanFiveThousandFilesFinishesSharedAttachments(t *testing.T) {
	s, _, _, connection, rule := newMediaCD2Organizer(t)
	fs := connection.filesystem
	fs.add(path.Join(rule.Source, "Work.S01E01.mkv"), false, 123)
	fs.add(path.Join(rule.Source, "poster.png"), false, 8)
	for directory := range 2 {
		parent := path.Join(rule.Source, fmt.Sprintf("kept-%d", directory))
		fs.add(parent, true, 0)
		for i := range 2500 {
			fs.add(path.Join(parent, fmt.Sprintf("keep-%d.iso", i)), false, 123)
		}
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	if run.Status != "completed" {
		t.Fatal("超过五千个文件仍应完成整轮", run.Status)
	}
	done := 0
	for _, entry := range run.Entries {
		if entry.File.MediaType == "video" || entry.File.MediaType == "attachment" {
			if entry.Status != "done" {
				t.Fatal("视频与共享附件应完成", entry)
			}
			done++
		}
	}
	if done != 2 || len(fs.deleted) != 0 {
		t.Fatal("共享附件未完成或保留文件被清理", done, fs.deleted)
	}
}
