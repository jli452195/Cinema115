package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type subtitleFakeDrive struct {
	*organizeFakeDrive
	uploads      int
	uploadErr    error
	beforeUpload func()
	noApply      bool
}

func (d *subtitleFakeDrive) DeleteEntries(context.Context, []string, string) error {
	return errors.New("unexpected delete during subtitle import")
}

func (d *subtitleFakeDrive) UploadSubtitle(ctx context.Context, parent, name string, data []byte, cookie string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.uploads++
	d.cookies = append(d.cookies, cookie)
	if d.beforeUpload != nil {
		d.beforeUpload()
	}
	if !d.noApply {
		d.dirs[parent] = append(d.dirs[parent], p115.ShareFile{ID: fmt.Sprint(9000 + d.uploads), Name: name, Size: int64(len(data))})
	}
	return d.uploadErr
}
func newSubtitleTest(t *testing.T) (*ManualOrganize, *subtitleFakeDrive, string, OrganizeJob, *failingAccountStore) {
	t.Helper()
	s, drive, id, store := newOrganizeTest(t)
	plan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}, {ID: "103", Season: 1, Episode: 2}})
	job := organizeTestRun(t, s, id, plan)
	s.pending = nil
	d := &subtitleFakeDrive{organizeFakeDrive: drive}
	s.drive = d
	s.metadata = nil // Subtitle import never uses TMDB.
	return s, d, id, job, store
}
func subtitleSelection(local, video, name string, data []byte) SubtitleSelection {
	h := sha256.Sum256(data)
	return SubtitleSelection{LocalID: local, VideoID: video, OriginalName: name, Language: "zh-CN", Size: int64(len(data)), SHA256: hex.EncodeToString(h[:])}
}
func previewSubtitles(t *testing.T, s *ManualOrganize, id, job string, selections ...SubtitleSelection) SubtitleImport {
	t.Helper()
	plan, err := s.PreviewSubtitles(context.Background(), SubtitlePreviewInput{AccountID: id, JobID: job, Files: selections})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestManualOrganizeSubtitleImportUsesCurrentNameAndReplay(t *testing.T) {
	s, d, id, job, store := newSubtitleTest(t)
	video := job.Items[0]
	for i := range d.dirs[video.TargetCID] {
		if d.dirs[video.TargetCID][i].ID == "101" {
			d.dirs[video.TargetCID][i].Name = "新名称 - S01E01.mkv"
		}
	}
	data := []byte("1\n00:00:00,000 --> 00:00:01,000\nhello\n")
	plan := previewSubtitles(t, s, id, job.ID, subtitleSelection("1", "101", "网上下载第01集.srt", data))
	if plan.Items[0].Name != "新名称 - S01E01.zh-CN.srt" || d.uploads != 0 {
		t.Fatalf("plan=%+v", plan)
	}
	result, err := s.ImportSubtitles(context.Background(), id, plan.ID, map[string][]byte{"1": data})
	if err != nil || result.Status != "completed" || result.Items[0].Status != "done" || d.uploads != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if strings.Contains(store.data, "secret-cookie") || strings.Contains(store.data, "hello") {
		t.Fatal("credentials or subtitle payload persisted")
	}
	restarted, err := NewManualOrganize(store, s.accounts, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = restarted.ImportSubtitles(context.Background(), id, plan.ID, nil)
	if err != nil || d.uploads != 1 {
		t.Fatalf("replayed upload: %v", err)
	}
}

func TestManualOrganizeSubtitleValidationAndAccountBinding(t *testing.T) {
	s, d, id, job, _ := newSubtitleTest(t)
	data := []byte("subtitle")
	good := subtitleSelection("1", "101", "01.ass", data)
	for _, mutate := range []func(*SubtitleSelection){
		func(f *SubtitleSelection) { f.VideoID = "foreign" }, func(f *SubtitleSelection) { f.OriginalName = "x.exe" }, func(f *SubtitleSelection) { f.SHA256 = "bad" },
		func(f *SubtitleSelection) { f.Size = p115.MaxSubtitleBytes + 1 }, func(f *SubtitleSelection) { f.Version = "../escape" }, func(f *SubtitleSelection) { f.Language = "../x" },
	} {
		bad := good
		mutate(&bad)
		if _, err := s.PreviewSubtitles(context.Background(), SubtitlePreviewInput{AccountID: id, JobID: job.ID, Files: []SubtitleSelection{bad}}); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if _, err := s.SubtitleVideos(context.Background(), "", job.ID); err == nil {
		t.Fatal("fallback account")
	}
	if _, err := s.SubtitleVideos(context.Background(), id, "foreign"); err == nil {
		t.Fatal("foreign job")
	}
	plan := previewSubtitles(t, s, id, job.ID, good)
	if _, err := s.ImportSubtitles(context.Background(), id, plan.ID, map[string][]byte{"1": []byte("changed!")}); err == nil {
		t.Fatal("content changed after preview")
	}
	a, _ := s.accounts.Get(id)
	cookie := "new-cookie"
	if err := s.accounts.Update(id, a.Revision, "changed", &cookie); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportSubtitles(context.Background(), id, plan.ID, map[string][]byte{"1": data}); err == nil {
		t.Fatal("accepted changed credentials")
	}
	if d.uploads != 0 {
		t.Fatal("invalid request uploaded files")
	}
}

func TestManualOrganizeSubtitleConflictsAndPairs(t *testing.T) {
	s, d, id, job, _ := newSubtitleTest(t)
	data := []byte("subtitle")
	selection := subtitleSelection("1", "101", "01.srt", data)
	plan := previewSubtitles(t, s, id, job.ID, selection)
	item := plan.Items[0]
	d.dirs[item.Video.ParentID] = append(d.dirs[item.Video.ParentID], p115.ShareFile{ID: "500", Name: item.Name, Size: 999})
	result, err := s.ImportSubtitles(context.Background(), id, plan.ID, map[string][]byte{"1": data})
	if err != nil || result.Items[0].Status != "skipped" || d.uploads != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	plan = previewSubtitles(t, s, id, job.ID, selection)
	if plan.Items[0].Status != "skipped" {
		t.Fatal("existing subtitle not skipped in preview")
	}
	duplicate := selection
	duplicate.LocalID = "2"
	if _, err := s.PreviewSubtitles(context.Background(), SubtitlePreviewInput{AccountID: id, JobID: job.ID, Files: []SubtitleSelection{selection, duplicate}}); err == nil {
		t.Fatal("duplicate destination accepted")
	}
	idx := subtitleSelection("1", "101", "01.idx", data)
	if _, err := s.PreviewSubtitles(context.Background(), SubtitlePreviewInput{AccountID: id, JobID: job.ID, Files: []SubtitleSelection{idx}}); err == nil {
		t.Fatal("unpaired idx accepted")
	}
	pair := previewSubtitles(t, s, id, job.ID, idx, subtitleSelection("2", "101", "01.sub", data))
	if len(pair.Items) != 2 {
		t.Fatal("pair missing")
	}
}

func TestManualOrganizeSubtitleStopsAndPersistsUncertain(t *testing.T) {
	for _, failure := range []string{"timeout", "no-apply", "moved", "save", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			s, d, id, job, store := newSubtitleTest(t)
			data := []byte("subtitle")
			plan := previewSubtitles(t, s, id, job.ID, subtitleSelection("1", "101", "01.ass", data), subtitleSelection("2", "103", "02.ass", data))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch failure {
			case "timeout":
				d.uploadErr = errors.New("timeout secret-cookie")
			case "no-apply":
				d.noApply = true
			case "moved":
				d.dirs[plan.Items[0].Video.ParentID] = nil
			case "save":
				store.err = errors.New("disk full")
			case "cancel":
				cancel()
			}
			result, err := s.ImportSubtitles(ctx, id, plan.ID, map[string][]byte{"1": data, "2": data})
			if failure == "save" {
				if err == nil || d.uploads != 0 {
					t.Fatal("write despite persistence failure")
				}
				return
			}
			if err != nil || result.Status != "partial" || strings.Contains(result.Message, "secret-cookie") {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if failure == "timeout" || failure == "no-apply" {
				if d.uploads != 1 || result.Items[0].Status != "uncertain" || result.Items[1].Status != "pending" {
					t.Fatal("continued after uncertain upload")
				}
			} else if d.uploads != 0 {
				t.Fatal("uploaded after failed preflight")
			}
			_, _ = s.ImportSubtitles(context.Background(), id, plan.ID, map[string][]byte{"1": data, "2": data})
			if d.uploads > 1 {
				t.Fatal("replayed failed upload")
			}
		})
	}
}

func TestManualOrganizeSubtitleSnapshotMutualExclusionAndRestart(t *testing.T) {
	s, d, id, job, store := newSubtitleTest(t)
	data := []byte("subtitle")
	plan := previewSubtitles(t, s, id, job.ID, subtitleSelection("1", "101", "01.ass", data), subtitleSelection("2", "103", "02.ass", data))
	a, _ := s.accounts.Get(id)
	d.beforeUpload = func() {
		if _, err := s.PreviewCleanup(context.Background(), OrganizeCleanupInput{AccountID: id, ParentID: "10", ParentPath: "/来源", Mode: "empty"}); !errors.Is(err, ErrOrganizeConflict) {
			t.Fatalf("cleanup overlapped upload: %v", err)
		}
		cookie := "new-cookie"
		if err := s.accounts.Update(id, a.Revision, "changed", &cookie); err != nil {
			t.Fatal(err)
		}
		d.beforeUpload = nil
	}
	result, err := s.ImportSubtitles(context.Background(), id, plan.ID, map[string][]byte{"1": data, "2": data})
	if err != nil || result.Status != "completed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, cookie := range d.cookies {
		if cookie != a.Cookie {
			t.Fatal("account snapshot changed")
		}
	}
	result.Status = "running"
	result.Items[0].Status = "uncertain"
	if err := s.subtitleCheckpoint(result); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewManualOrganize(store, s.accounts, d, nil)
	if err != nil || restarted.SubtitleHistory()[0].Status != "interrupted" {
		t.Fatalf("restart: %v", err)
	}
	// Expired preview is rejected independently of the live account revision.
	plan = previewSubtitles(t, s, id, job.ID, subtitleSelection("1", "101", "01.srt", data))
	p := s.subtitlePlans[plan.ID]
	p.CreatedAt = time.Now().Add(-11 * time.Minute)
	s.subtitlePlans[plan.ID] = p
	if _, err := s.ImportSubtitles(context.Background(), id, plan.ID, map[string][]byte{"1": data}); err == nil {
		t.Fatal("expired preview accepted")
	}
}
