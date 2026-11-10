package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestMedia115PendingIntentPreventsCorrectionUntilReconciled(t *testing.T) {
	s, _, _, run := newMedia115ExecutionTest(t)
	run.Status = "partial"
	run.Groups = []MediaOrganizeGroup{{ID: "1", Kind: "tv", TMDBID: 42}}
	run.Entries = []MediaOrganizeEntry{{
		File:  OrganizeFile{ShareFile: p115.ShareFile{ID: "501", Name: "Work.S01E01.mkv"}, MediaType: "video", Season: 1, Episode: 1},
		Group: "1", Status: "error", Relative: "original.mkv", Target: "/library/original.mkv", P115Move: &Media115MoveIntent{},
	}}
	if err := s.checkpoint(run); err != nil {
		t.Fatal(err)
	}
	delete(s.actions, run.ID)
	s.releaseHistoryLocked()
	before, err := s.Snapshot(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	season := 2
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: "501", Season: &season}); !errors.Is(err, ErrMediaOrganizeInput) {
		t.Fatal("未核实写入不能改写原计划", err)
	}
	after, err := s.Snapshot(run.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("拒绝修正必须保持原批次完整不变", err)
	}
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: "501"}); err != nil {
		t.Fatal("仍应允许直接重试核实原写入", err)
	}
}

func TestMedia115RenderKeepsPendingVideoAndCompanionPlans(t *testing.T) {
	s, _, files, run := newMedia115ExecutionTest(t)
	s.metadata = nil
	run.Entries = []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "501", Name: "Work.S01E01.mkv"}, MediaType: "video"}, Group: "1", Status: "error", Relative: "original.mkv", Target: "/library/original.mkv", P115Move: &Media115MoveIntent{}},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "502", Name: "Work.S01E01.srt"}, MediaType: "subtitle"}, Group: "1", VideoID: "501", Status: "error", Relative: "original.srt", Target: "/library/original.srt", P115Move: &Media115MoveIntent{}},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "503", Name: "Work.S01E01.nfo"}, MediaType: "attachment"}, Group: "1", VideoID: "501", Status: "error", Relative: "original.nfo", Target: "/library/original.nfo", P115Move: &Media115MoveIntent{}},
	}
	var before MediaOrganizeRun
	cloneMediaRun(*run, &before)
	if err := s.render(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Entries, run.Entries) {
		t.Fatal("重试渲染不得清空未知写入的文件目标或附件关联", run.Entries)
	}
}

func TestMedia115PendingIntentKeepsCompanionsOutOfQuarantine(t *testing.T) {
	run := &MediaOrganizeRun{Rule: MediaOrganizeRule{Storage: "115", Failure: "/failed"}, Entries: []MediaOrganizeEntry{
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "501"}, MediaType: "video"}, Status: "conflict", P115Move: &Media115MoveIntent{}},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "502"}, MediaType: "subtitle"}, VideoID: "501", Status: "invalid"},
		{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "503"}, MediaType: "video"}, Status: "invalid"},
	}}
	failures := mediaFailureEntries(run)
	if failures["501"] || failures["502"] || !failures["503"] {
		t.Fatal("原写入未核实前字幕必须保留，独立失败任务仍可隔离", failures)
	}
}

func TestMedia115NamingFactsReusedAcrossDistinctSourceGroups(t *testing.T) {
	s, drive, files, run, metadata := newMedia115PipelineTest(t)
	drive.dirs["10"] = []p115.ShareFile{{ID: "501", Name: "Work.S01E01.{tmdb-42}.mkv", Size: 123}, {ID: "30", Name: "work02", Directory: true}}
	drive.paths["30"] = "/inbox/work02"
	drive.dirs["30"] = []p115.ShareFile{{ID: "502", Name: "Work.S01E02.{tmdb-42}.mkv", Size: 123}}
	if err := s.process115Auto(context.Background(), files, run); err != nil {
		t.Fatal(err)
	}
	if len(run.Groups) != 2 || metadata.browseCalls != 0 || metadata.details != 3 || run.Status != "completed" {
		t.Fatal("独立文件夹分组分别识别，同一作品的命名详情应只准备一次", len(run.Groups), metadata.details, run.Status)
	}
}
