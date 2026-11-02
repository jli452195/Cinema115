package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

const media115DuplicateHash = "0123456789ABCDEF0123456789ABCDEF01234567"

func newMedia115DuplicateTest(t *testing.T) (*MediaOrganizer, *mediaCleanupDrive, *mediaOrganizeFiles, MediaOrganizeEntry, p115.ShareFile) {
	t.Helper()
	s, d, rule := newMediaCleanupCloud(t)
	rule.DeleteDuplicates = true
	e := MediaOrganizeEntry{
		File: OrganizeFile{
			ShareFile:  p115.ShareFile{ID: "500", Name: "Work.S01E01.mkv", SHA1: media115DuplicateHash, Size: 10},
			ParentID:   "10",
			SourcePath: "Work.S01E01.mkv",
			MediaType:  "video",
		},
		Relative: "renamed.mkv",
	}
	target := p115.ShareFile{ID: "600", Name: "renamed.mkv", SHA1: strings.ToLower(media115DuplicateHash), Size: 20}
	d.dirs["10"], d.dirs["20"] = []p115.ShareFile{e.File.ShareFile}, []p115.ShareFile{target}
	f, err := s.files(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	return s, d, f, e, target
}

func TestMedia115DuplicateSHA1Only(t *testing.T) {
	for _, scenario := range []struct {
		name string
		edit func(*MediaOrganizeEntry, *p115.ShareFile)
		want bool
	}{
		{"different sizes and hash letter case", func(*MediaOrganizeEntry, *p115.ShareFile) {}, true},
		{"source hash missing", func(e *MediaOrganizeEntry, _ *p115.ShareFile) { e.File.SHA1 = "" }, false},
		{"target hash missing", func(_ *MediaOrganizeEntry, f *p115.ShareFile) { f.SHA1 = "" }, false},
		{"partial hash", func(e *MediaOrganizeEntry, f *p115.ShareFile) { e.File.SHA1, f.SHA1 = "ABCDEF", "ABCDEF" }, false},
		{"invalid hash", func(e *MediaOrganizeEntry, f *p115.ShareFile) {
			e.File.SHA1, f.SHA1 = strings.Repeat("G", 40), strings.Repeat("G", 40)
		}, false},
		{"different hash", func(_ *MediaOrganizeEntry, f *p115.ShareFile) { f.SHA1 = strings.Repeat("A", 40) }, false},
		{"source is directory", func(e *MediaOrganizeEntry, _ *p115.ShareFile) { e.File.Directory = true }, false},
		{"target is directory", func(_ *MediaOrganizeEntry, f *p115.ShareFile) { f.Directory = true }, false},
		{"same identity", func(e *MediaOrganizeEntry, f *p115.ShareFile) { f.ID = e.File.ID }, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, _, _, e, target := newMedia115DuplicateTest(t)
			scenario.edit(&e, &target)
			if got := media115SameSHA1(e, target); got != scenario.want {
				t.Fatalf("same SHA1 = %v, want %v", got, scenario.want)
			}
		})
	}
}

func TestMedia115DuplicateRetainsChangedSourceAndTarget(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		edit     func(*mediaCleanupDrive, *mediaOrganizeFiles, *MediaOrganizeEntry, *p115.ShareFile)
		conflict bool
	}{
		{"disabled", func(_ *mediaCleanupDrive, f *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			f.rule.DeleteDuplicates = false
		}, true},
		{"different hash", func(_ *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, target *p115.ShareFile) {
			target.SHA1 = strings.Repeat("A", 40)
		}, true},
		{"missing hash", func(_ *mediaCleanupDrive, _ *mediaOrganizeFiles, e *MediaOrganizeEntry, _ *p115.ShareFile) {
			e.File.SHA1 = ""
		}, true},
		{"source disappeared", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.dirs["10"] = nil
		}, false},
		{"source size changed", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.dirs["10"][0].Size++
		}, false},
		{"source name changed", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.dirs["10"][0].Name = "other.mkv"
		}, false},
		{"source hash changed", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.dirs["10"][0].SHA1 = strings.Repeat("A", 40)
		}, false},
		{"source identity changed", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.dirs["10"][0].ID = "501"
		}, false},
		{"source became directory", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.dirs["10"][0].Directory = true
		}, false},
		{"source parent moved", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.paths["10"] = "/other"
		}, false},
		{"target parent moved", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.paths["20"] = "/other"
		}, false},
		{"target disappeared", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.dirs["20"] = nil
		}, false},
		{"target replaced", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.dirs["20"][0].ID = "601"
		}, false},
		{"target hash changed", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.dirs["20"][0].SHA1 = strings.Repeat("A", 40)
		}, false},
		{"source read failed", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.beforeRead = func(cid string, _ int) error {
				if cid == "10" {
					return context.DeadlineExceeded
				}
				return nil
			}
		}, false},
		{"target read failed", func(d *mediaCleanupDrive, _ *mediaOrganizeFiles, _ *MediaOrganizeEntry, _ *p115.ShareFile) {
			d.beforeRead = func(cid string, _ int) error {
				if cid == "20" {
					return context.DeadlineExceeded
				}
				return nil
			}
		}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, d, f, e, target := newMedia115DuplicateTest(t)
			scenario.edit(d, f, &e, &target)
			err := f.delete115Duplicate(context.Background(), e, "20", target)
			if err == nil || errors.Is(err, errMediaDuplicateDone) || (scenario.conflict && !errors.Is(err, errMediaTargetExists)) {
				t.Fatal("changed file must be retained", err)
			}
			if len(d.deleted) != 0 {
				t.Fatal("unexpected recycle request", d.deleted)
			}
		})
	}
}

func TestMedia115DuplicateRecycleAndVerification(t *testing.T) {
	for _, scenario := range []string{"success", "recycle failure", "timeout after applying", "false success", "target lost after delete", "verification failure"} {
		t.Run(scenario, func(t *testing.T) {
			_, d, f, e, target := newMedia115DuplicateTest(t)
			switch scenario {
			case "recycle failure":
				d.deleteErr = context.DeadlineExceeded
			case "timeout after applying":
				d.deleteErr, d.applyOnError = context.DeadlineExceeded, true
			case "false success":
				d.noDelete = true
			case "target lost after delete":
				d.beforeDelete = func() { d.dirs["20"] = nil }
			case "verification failure":
				d.beforeDelete = func() {
					d.beforeRead = func(string, int) error { return context.DeadlineExceeded }
				}
			}
			err := f.delete115Duplicate(context.Background(), e, "20", target)
			if (scenario == "success") != errors.Is(err, errMediaDuplicateDone) {
				t.Fatal("unexpected recycle result", err)
			}
			if scenario != "success" && err == nil {
				t.Fatal("uncertain deletion reported success")
			}
			if (scenario == "recycle failure" || scenario == "timeout after applying" || scenario == "verification failure") && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("underlying error was lost", err)
			}
			if !reflect.DeepEqual(d.batches, [][]string{{e.File.ID}}) {
				t.Fatal("recycle was retried or included the target", d.batches)
			}
			if scenario != "target lost after delete" && !reflect.DeepEqual(d.dirs["20"], []p115.ShareFile{target}) {
				t.Fatal("target changed", d.dirs["20"])
			}
		})
	}
}

func TestMedia115DuplicateMoveUsesFrozenPrimaryCredentials(t *testing.T) {
	s, d, f, e, target := newMedia115DuplicateTest(t)
	if err := f.conflict(context.Background(), e); err != nil {
		t.Fatal("SHA1 duplicate should be executable", err)
	}
	newCookie := "UID=123_snapshot; SEID=new-cookie"
	if err := s.accounts.Update(f.rule.AccountID, 2, "整理账号", &newCookie); err != nil {
		t.Fatal(err)
	}
	if err := f.move(context.Background(), e); !errors.Is(err, errMediaDuplicateDone) {
		t.Fatal("native 115 duplicate did not finish", err)
	}
	if len(d.dirs["10"]) != 0 || !reflect.DeepEqual(d.dirs["20"], []p115.ShareFile{target}) {
		t.Fatal("source was not recycled with the target retained", d.dirs)
	}
	for _, cookie := range d.cookies {
		if cookie != f.cookie || cookie == newCookie {
			t.Fatal("task credentials changed during duplicate deletion")
		}
	}
	if d.renames != 0 || d.moves != 0 {
		t.Fatal("duplicate deletion must not rename or move the source", d.renames, d.moves)
	}
}
